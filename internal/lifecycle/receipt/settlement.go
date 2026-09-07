package receipt

import (
	"context"
	"fmt"
	"sync"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

type settlementState uint8

const (
	settlementIdle settlementState = iota
	settlementActive
	settlementFinished
	settlementAbandoned
)

// CommitSettlement serializes the timeout choice with entry into the real
// journal append and publication of its complete Outcome. Do not copy it.
// Cancellation before entry prevents a later append. Cancellation after entry
// waits for settlement; it is NOT an absolute response-time cap or a promise
// that the extension is brief. An external process kill is outside this fence.
type CommitSettlement struct {
	mu        sync.Mutex
	state     settlementState
	done      chan struct{}
	outcome   hostexit.Outcome
	committed bool
}

func NewCommitSettlement() *CommitSettlement {
	return &CommitSettlement{done: make(chan struct{})}
}

// Run admits one real append. Outcome must already be encoded from the same
// validated decision as its effects. It is never exposed before success.
func (s *CommitSettlement) Run(ctx context.Context, outcome hostexit.Outcome, appendReceipt func() (model.OccurrenceID, error)) (id model.OccurrenceID, err error) {
	return s.run(ctx, outcome, true, appendReceipt)
}

// RunRecordOnly fences a refused capture's occurrence without manufacturing
// an evaluated host Outcome. Timeout still waits for this append to settle.
func (s *CommitSettlement) RunRecordOnly(ctx context.Context, appendReceipt func() (model.OccurrenceID, error)) (model.OccurrenceID, error) {
	return s.run(ctx, hostexit.Outcome{}, false, appendReceipt)
}

func (s *CommitSettlement) run(ctx context.Context, outcome hostexit.Outcome, publish bool, appendReceipt func() (model.OccurrenceID, error)) (id model.OccurrenceID, err error) {
	if s == nil || ctx == nil || appendReceipt == nil {
		return 0, fmt.Errorf("receipt.CommitSettlement.Run: missing fence, context or append callback; nothing was committed; construct the fence and pass the real journal appender")
	}
	if _, known := outcome.Exit.Code(); publish && !known {
		return 0, fmt.Errorf("receipt.CommitSettlement.Run: outcome has no valid exit status; no append was admitted; encode the complete native Outcome before committing")
	}
	outcome = copyOutcome(outcome)
	s.mu.Lock()
	if s.state != settlementIdle {
		s.mu.Unlock()
		return 0, fmt.Errorf("receipt.CommitSettlement.Run: invocation already settled, entered or abandoned; no new append was admitted; use one fresh fence per invocation")
	}
	if err := ctx.Err(); err != nil {
		s.state = settlementAbandoned
		if s.done == nil {
			s.done = make(chan struct{})
		}
		close(s.done)
		s.mu.Unlock()
		return 0, fmt.Errorf("receipt.CommitSettlement.Run: cancellation won before commit entry; no occurrence was appended; retry with a live invocation: %w", err)
	}
	if s.done == nil {
		s.done = make(chan struct{})
	}
	s.state = settlementActive
	s.mu.Unlock()

	committed := false
	defer func() {
		s.mu.Lock()
		s.committed = committed
		if committed {
			s.outcome = copyOutcome(outcome)
		}
		s.state = settlementFinished
		close(s.done)
		s.mu.Unlock()
	}()
	id, err = appendReceipt()
	if err == nil && id == 0 {
		err = fmt.Errorf("receipt.CommitSettlement.Run: appender returned no occurrence identity; no committed Outcome can be published; inspect the journal result before retrying")
	}
	committed = publish && err == nil && id > 0
	return id, err
}

// Expire atomically prevents later entry or returns the active settlement
// signal, including the COMMIT-to-Apply-return interval. The caller cancels
// its work context first, then MUST receive from this channel before choosing
// a fault or reading CommittedOutcome. The channel closes on actual settlement,
// not on the nominal deadline. It is already closed when expiry wins entry.
func (s *CommitSettlement) Expire() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done == nil {
		s.done = make(chan struct{})
	}
	if s.state == settlementIdle {
		s.state = settlementAbandoned
		close(s.done)
	}
	return s.done
}

func (s *CommitSettlement) CommittedOutcome() (hostexit.Outcome, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyOutcome(s.outcome), s.committed
}

func copyOutcome(outcome hostexit.Outcome) hostexit.Outcome {
	outcome.Stdout = append([]byte(nil), outcome.Stdout...)
	return outcome
}
