package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// EpochController is the substrate-neutral control surface the epoch lifecycle
// and signal handlers depend on. Keeping handlers behind this narrow interface
// (instead of the durable engine directly) keeps them unit-testable with a fake
// and isolates them from the substrate. The production implementation is
// DBOS-backed; lifecycle verbs map to RunWorkflow/CancelWorkflow and signals map
// to Send by topic.
type EpochController interface {
	// StartEpoch launches the durable control workflow for epochId (its
	// workflow id is the epoch id, so signals address it by that id).
	StartEpoch(ctx context.Context, epochId string) error
	// CancelEpoch requests cancellation of the running epoch workflow.
	CancelEpoch(ctx context.Context, epochId string) error
	// TerminateEpoch records an EpochCancelled audit event carrying the
	// operator's reason, then requests cancellation of the running epoch
	// workflow. reason may be empty (event is still recorded, with an empty
	// reason payload). Record-before-cancel order is intentional: cancel often
	// targets a wedged workflow where a subsequent signal would not fire.
	TerminateEpoch(ctx context.Context, epochId, reason string) error
	// AdvancePhase delivers an advance-phase signal.
	AdvancePhase(ctx context.Context, epochId string, sig protocol.PhaseAdvanceSignal) error
	// SubmitVote delivers a review-vote signal.
	SubmitVote(ctx context.Context, epochId string, sig protocol.ReviewVoteSignal) error
	// ReportSliceProgress delivers a slice-progress signal.
	ReportSliceProgress(ctx context.Context, epochId string, sig protocol.SliceProgressSignal) error
	// RegisterSession delivers a register-session signal.
	RegisterSession(ctx context.Context, epochId string, sig protocol.RegisterSessionSignal) error
	// StartSlice delivers a start-slice configuration signal to a slice
	// sub-workflow (addressed by its slice workflow id).
	StartSlice(ctx context.Context, sliceId string, sig protocol.SliceStartSignal) error
	// CompleteSlice delivers a complete-slice override signal to a slice
	// sub-workflow (addressed by its slice workflow id).
	CompleteSlice(ctx context.Context, sliceId string, sig protocol.SliceCompleteSignal) error
	// Close releases the controller's resources.
	Close() error
}

// dbosController is the DBOS-backed EpochController. It owns a lightweight
// database-backed client and maps each operation onto durable DBOS records.
type dbosController struct {
	client      dbos.Client
	db          *sql.DB
	trail       audit.Trail
	trailCloser interface{ Close() error }
}

// OpenEpochController opens a DBOS-backed controller on the unified database.
// Empty dbPath resolves to tasks.DefaultDBPath(). The returned controller does
// not construct or launch an engine: lifecycle verbs enqueue durable DBOS
// records and signals against the shared SQLite file, while pastured hosts and
// executes the registered workflows.
func OpenEpochController(dbPath string) (EpochController, error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}

	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		return nil, err
	}
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		_ = trail.Close()
		return nil, err
	}
	client, err := dbos.NewClient(context.Background(), dbos.ClientConfig{
		SqliteSystemDB: db,
	})
	if err != nil {
		_ = db.Close()
		_ = trail.Close()
		return nil, err
	}
	return &dbosController{client: client, db: db, trail: trail, trailCloser: trail}, nil
}

func (c *dbosController) StartEpoch(ctx context.Context, epochId string) error {
	_, err := dbos.Enqueue[engine.ControlInput, protocol.EpochState](c.client,
		engine.ControlQueueName,
		engine.EpochControlWorkflowName,
		engine.ControlInput{EpochId: epochId},
		dbos.WithEnqueueWorkflowID(epochId),
		dbos.WithEnqueueApplicationVersion(engine.DefaultApplicationVersion),
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("The epoch %q couldn't be started.", epochId),
			Why:      "The durable engine rejected the start request — likely a storage or engine initialisation failure.",
			Where:    "Starting the epoch (internal/handlers/controller.go in dbosController.StartEpoch).",
			Impact:   "The epoch did not start, so no phase transitions will run for it.",
			Fix: "1. Confirm the database is readable and writable:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Retry once you've confirmed the database is healthy.\n" +
				"   Note: starting an epoch with an id that is already running is an idempotent no-op.",
			Cause: err,
		}
	}
	return nil
}

func (c *dbosController) CancelEpoch(ctx context.Context, epochId string) error {
	if err := c.client.CancelWorkflow(epochId); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't stop the epoch %q.", epochId),
			Why:      "The durable engine rejected the cancellation (the epoch may not be running).",
			Where:    "Cancelling the epoch (internal/handlers/controller.go in dbosController.CancelEpoch).",
			Impact:   "The epoch is unchanged; the cancellation did not take effect.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

// TerminateEpoch records an EpochCancelled audit event carrying the operator's
// reason (empty string is allowed — the payload will contain key "reason" with
// an empty value), then requests cancellation of the running epoch workflow.
//
// The event is written via the non-dedup RecordEvent path (NULL dedup_key)
// because a CLI terminate is a one-shot action, not a replayed durable step.
// Record-before-cancel order is deliberate: cancel often targets a wedged
// workflow where a subsequent signal would not fire; the audit record must
// survive even when cancellation itself fails.
//
// The event is attributed to the engine automaton agent (find-or-created by
// the legacy-role bridge inside the audit trail). If recording fails, the
// method returns the record error without attempting cancellation.
func (c *dbosController) TerminateEpoch(ctx context.Context, epochId, reason string) error {
	ev := protocol.AuditEvent{
		EpochId:   epochId,
		Role:      engine.EngineAgentName,
		EventType: protocol.EventEpochCancelled,
		Payload:   map[string]any{"reason": reason},
		Timestamp: time.Now().UTC(),
	}
	if err := c.trail.RecordEvent(ctx, ev); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't record the cancellation event for epoch %q before stopping it.", epochId),
			Why:      "The audit trail rejected the write (storage error or database not accessible).",
			Where:    "Terminating the epoch (internal/handlers/controller.go in dbosController.TerminateEpoch).",
			Impact:   "The epoch was not cancelled. No audit record was written.",
			Fix: "1. Confirm the database is readable and writable:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Retry once the database is healthy.",
			Cause: err,
		}
	}
	if err := c.client.CancelWorkflow(epochId); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't stop the epoch %q (the cancellation event was recorded).", epochId),
			Why:      "The durable engine rejected the cancellation (the epoch may not be running).",
			Where:    "Terminating the epoch (internal/handlers/controller.go in dbosController.TerminateEpoch).",
			Impact:   "The epoch is unchanged; the cancellation did not take effect. The audit record was written.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

// sendSignal is the shared send path; topic names come from the typed constants.
func (c *dbosController) sendSignal(epochId string, topic protocol.SignalTopic, send func() error) error {
	if err := send(); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't deliver the %q signal to epoch %q.", topic, epochId),
			Why:      "The durable engine rejected the signal (the epoch may not be running).",
			Where:    "Delivering an epoch signal (internal/handlers/controller.go in dbosController.sendSignal).",
			Impact:   "The signal was not delivered, so the epoch's state is unchanged.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

func (c *dbosController) AdvancePhase(ctx context.Context, epochId string, sig protocol.PhaseAdvanceSignal) error {
	return c.sendSignal(epochId, protocol.SignalAdvancePhase, func() error {
		return c.client.Send(epochId, sig, protocol.SignalAdvancePhase.String())
	})
}

func (c *dbosController) SubmitVote(ctx context.Context, epochId string, sig protocol.ReviewVoteSignal) error {
	return c.sendSignal(epochId, protocol.SignalSubmitVote, func() error {
		return c.client.Send(epochId, sig, protocol.SignalSubmitVote.String())
	})
}

func (c *dbosController) ReportSliceProgress(ctx context.Context, epochId string, sig protocol.SliceProgressSignal) error {
	return c.sendSignal(epochId, protocol.SignalSliceProgress, func() error {
		return c.client.Send(epochId, sig, protocol.SignalSliceProgress.String())
	})
}

func (c *dbosController) RegisterSession(ctx context.Context, epochId string, sig protocol.RegisterSessionSignal) error {
	return c.sendSignal(epochId, protocol.SignalRegisterSession, func() error {
		return c.client.Send(epochId, sig, protocol.SignalRegisterSession.String())
	})
}

func (c *dbosController) StartSlice(ctx context.Context, sliceId string, sig protocol.SliceStartSignal) error {
	return c.sendSignal(sliceId, protocol.SignalStartSlice, func() error {
		return c.client.Send(sliceId, sig, protocol.SignalStartSlice.String())
	})
}

func (c *dbosController) CompleteSlice(ctx context.Context, sliceId string, sig protocol.SliceCompleteSignal) error {
	return c.sendSignal(sliceId, protocol.SignalCompleteSlice, func() error {
		return c.client.Send(sliceId, sig, protocol.SignalCompleteSlice.String())
	})
}

func (c *dbosController) Close() error {
	if c.client != nil {
		// The DBOS client owns and closes the SqliteSystemDB handle supplied at
		// construction; closing c.db separately would double-close the same DB.
		c.client.Shutdown(5 * time.Second)
	}
	if c.trailCloser != nil {
		return c.trailCloser.Close()
	}
	return nil
}
