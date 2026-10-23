package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

var (
	ErrUnknownEpoch    = errors.New("unknown epoch")
	ErrNoOpenBinding   = errors.New("no open session binding")
	ErrBindingConflict = errors.New("session binding conflict")

	// ErrBindingRequest marks a bind/unbind request the caller can fix: a
	// missing epoch, an unknown role, or padded whitespace around a value. It is
	// a client error and maps to 400 invalid_body, never to a store fault.
	ErrBindingRequest = errors.New("invalid session binding request")

	// ErrBindingStoreFault marks a failure of the unified database handle or its
	// transaction: a closed handle, a refused statement, a commit failure, or an
	// expired caller deadline. No binding change was committed, and it maps to
	// 500 storage_fault rather than blaming the request.
	ErrBindingStoreFault = errors.New("session binding store fault")
)

// bindingRequestError wraps one request-validation reason in ErrBindingRequest
// with the fix a caller needs.
func bindingRequestError(reason string) error {
	return fmt.Errorf("%w: %s. Fix: correct the request body and retry.", ErrBindingRequest, reason)
}

// bindingStoreFault wraps one failed store operation. The cause stays reachable
// (including context.Canceled/DeadlineExceeded) so an operator can see why the
// transaction did not complete.
func bindingStoreFault(operation string, cause error) error {
	return fmt.Errorf("%w: %s failed: %w. Why: the unified database handle could not complete the transaction. Impact: no binding change was committed. Fix: retry once the store is writable; if the handle is closed, restart `pasture serve`.", ErrBindingStoreFault, operation, cause)
}

// SessionBindRequest is the explicit bind body.
type SessionBindRequest struct {
	EpochID         string `json:"epochId"`
	Role            string `json:"role"`
	ExpectedBinding string `json:"expectedBinding,omitempty"`
}

// SessionBindingResult is one bind or unbind outcome.
type SessionBindingResult struct {
	BindingID string     `json:"bindingId"`
	EpochID   string     `json:"epochId"`
	Role      string     `json:"role"`
	BoundAt   time.Time  `json:"boundAt"`
	UnboundAt *time.Time `json:"unboundAt,omitempty"`
	EndReason string     `json:"endReason,omitempty"`
	Revision  int64      `json:"revision"`
	Created   bool       `json:"created"`
}

// SessionAPIStore owns the explicit binding writes. It reuses the tracker's
// unified database handle so the serve process never opens a second writer.
type SessionAPIStore struct {
	db      *sql.DB
	profile timeouts.Profile
	clock   receipt.Clock
}

// NewSessionAPIStore borrows the tracker's unified handle.
func NewSessionAPIStore(tracker protocol.TaskTracker) (*SessionAPIStore, error) {
	store, ok := tracker.(sessionTailStore)
	if !ok || store.auditDBHandle() == nil {
		return nil, sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	return &SessionAPIStore{db: store.auditDBHandle(), profile: store.lifecycleTimeoutProfile(), clock: wallClock{}}, nil
}

// NewSessionAPIStoreWithDB is the test and embedding seam. Production callers
// use NewSessionAPIStore; both share the same transaction rules.
func NewSessionAPIStoreWithDB(db *sql.DB, profile timeouts.Profile, clock receipt.Clock) *SessionAPIStore {
	return &SessionAPIStore{db: db, profile: profile, clock: clock}
}

func (s *SessionAPIStore) now() int64 {
	clock := s.clock
	if clock == nil {
		clock = wallClock{}
	}
	return clock.Now().UTC().UnixNano()
}

// Bind closes any open binding and opens the requested one. Same epoch/role is
// idempotent; a stale ExpectedBinding is a conflict. Request mistakes are
// ErrBindingRequest and store/transaction failures are ErrBindingStoreFault;
// the domain outcomes remain ErrUnknownEpoch and ErrBindingConflict.
func (s *SessionAPIStore) Bind(ctx context.Context, harness, session string, req SessionBindRequest) (SessionBindingResult, error) {
	if err := validateBindingKey(harness, session); err != nil {
		return SessionBindingResult{}, bindingRequestError(err.Error())
	}
	epochID := strings.TrimSpace(req.EpochID)
	role := strings.TrimSpace(req.Role)
	if epochID == "" {
		return SessionBindingResult{}, bindingRequestError("epochId is required")
	}
	if !protocol.RoleId(role).IsValid() {
		return SessionBindingResult{}, bindingRequestError(fmt.Sprintf("role must be one of %v", protocol.AllRoleIds))
	}
	if strings.TrimSpace(req.ExpectedBinding) != req.ExpectedBinding {
		return SessionBindingResult{}, bindingRequestError("expectedBinding must not have leading or trailing whitespace")
	}
	bounded, cancel := context.WithTimeout(ctx, s.profile.WorkflowResult())
	defer cancel()
	tx, err := s.db.BeginTx(bounded, nil)
	if err != nil {
		return SessionBindingResult{}, bindingStoreFault("begin bind transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(bounded, `SELECT 1 FROM tasks WHERE id = ?`, epochID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionBindingResult{}, fmt.Errorf("%w: %s", ErrUnknownEpoch, epochID)
		}
		return SessionBindingResult{}, bindingStoreFault("read epoch", err)
	}

	var openID, openEpoch, openRole string
	var openBound int64
	err = tx.QueryRowContext(bounded, `
		SELECT binding_id, epoch_id, role, bound_at
		FROM pasture_session_epoch_binding
		WHERE harness = ? AND session = ? AND unbound_at IS NULL`, harness, session).
		Scan(&openID, &openEpoch, &openRole, &openBound)
	hasOpen := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SessionBindingResult{}, bindingStoreFault("read open binding", err)
	}
	if hasOpen {
		if req.ExpectedBinding != "" && req.ExpectedBinding != openID {
			return SessionBindingResult{}, fmt.Errorf("%w: expected %s, open is %s", ErrBindingConflict, req.ExpectedBinding, openID)
		}
		if openEpoch == epochID && openRole == role {
			revision, revErr := readSessionAPIRevision(bounded, tx)
			if revErr != nil {
				return SessionBindingResult{}, revErr
			}
			if err := tx.Commit(); err != nil {
				return SessionBindingResult{}, bindingStoreFault("commit idempotent bind", err)
			}
			return SessionBindingResult{BindingID: openID, EpochID: openEpoch, Role: openRole, BoundAt: time.Unix(0, openBound).UTC(), Revision: revision}, nil
		}
		if _, err := tx.ExecContext(bounded, `
			UPDATE pasture_session_epoch_binding
			SET unbound_at = ?, unbound_reason = 'unbind'
			WHERE binding_id = ?`, s.now(), openID); err != nil {
			return SessionBindingResult{}, bindingStoreFault("close previous binding", err)
		}
	} else if req.ExpectedBinding != "" {
		return SessionBindingResult{}, fmt.Errorf("%w: expected %s, no open binding", ErrBindingConflict, req.ExpectedBinding)
	}

	bindingID := uuid.NewString()
	boundAt := s.now()
	if _, err := tx.ExecContext(bounded, `
		INSERT INTO pasture_session_epoch_binding
			(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES (?, ?, ?, ?, ?, ?, NULL, NULL)`,
		bindingID, harness, session, epochID, role, boundAt); err != nil {
		return SessionBindingResult{}, bindingStoreFault("insert binding", err)
	}
	if _, err := tx.ExecContext(bounded, `
		INSERT INTO pasture_session_state
			(harness, session, first_seen, last_seen, last_event, last_journal_id, updated_at)
		VALUES (?, ?, ?, ?, '', 0, ?)
		ON CONFLICT(harness, session) DO UPDATE SET
			last_seen = max(pasture_session_state.last_seen, excluded.last_seen),
			updated_at = max(pasture_session_state.updated_at, excluded.updated_at)`,
		harness, session, boundAt, boundAt, boundAt); err != nil {
		return SessionBindingResult{}, bindingStoreFault("upsert session state", err)
	}
	revision, err := bumpSessionAPIRevision(bounded, tx)
	if err != nil {
		return SessionBindingResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionBindingResult{}, bindingStoreFault("commit bind", err)
	}
	return SessionBindingResult{BindingID: bindingID, EpochID: epochID, Role: role, BoundAt: time.Unix(0, boundAt).UTC(), Revision: revision, Created: true}, nil
}

// Unbind closes the open binding and records an explicit unbind end. Request
// mistakes are ErrBindingRequest; store/transaction failures are
// ErrBindingStoreFault; the domain outcomes remain ErrNoOpenBinding and
// ErrBindingConflict.
func (s *SessionAPIStore) Unbind(ctx context.Context, harness, session, expectedBinding string) (SessionBindingResult, error) {
	if err := validateBindingKey(harness, session); err != nil {
		return SessionBindingResult{}, bindingRequestError(err.Error())
	}
	if strings.TrimSpace(expectedBinding) != expectedBinding {
		return SessionBindingResult{}, bindingRequestError("expectedBinding must not have leading or trailing whitespace")
	}
	bounded, cancel := context.WithTimeout(ctx, s.profile.WorkflowResult())
	defer cancel()
	tx, err := s.db.BeginTx(bounded, nil)
	if err != nil {
		return SessionBindingResult{}, bindingStoreFault("begin unbind transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	var openID, openEpoch, openRole string
	var openBound int64
	err = tx.QueryRowContext(bounded, `
		SELECT binding_id, epoch_id, role, bound_at
		FROM pasture_session_epoch_binding
		WHERE harness = ? AND session = ? AND unbound_at IS NULL`, harness, session).
		Scan(&openID, &openEpoch, &openRole, &openBound)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionBindingResult{}, ErrNoOpenBinding
	}
	if err != nil {
		return SessionBindingResult{}, bindingStoreFault("read open binding", err)
	}
	if expectedBinding != "" && expectedBinding != openID {
		return SessionBindingResult{}, fmt.Errorf("%w: expected %s, open is %s", ErrBindingConflict, expectedBinding, openID)
	}
	unboundAt := s.now()
	if _, err := tx.ExecContext(bounded, `
		UPDATE pasture_session_epoch_binding
		SET unbound_at = ?, unbound_reason = 'unbind'
		WHERE binding_id = ?`, unboundAt, openID); err != nil {
		return SessionBindingResult{}, bindingStoreFault("close binding", err)
	}
	// Defense in depth: a session that is explicitly unbound must not keep an
	// inferred derivation. Inference never writes for a session with explicit
	// history and the explicit-binding trigger clears it on write, but an
	// operator's unbind is authoritative enough that the write path says so
	// itself rather than relying on those invariants holding.
	if err := clearSessionInferredBindings(bounded, tx, harness, session); err != nil {
		return SessionBindingResult{}, bindingStoreFault("clear inferred binding", err)
	}
	revision, err := bumpSessionAPIRevision(bounded, tx)
	if err != nil {
		return SessionBindingResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionBindingResult{}, bindingStoreFault("commit unbind", err)
	}
	end := time.Unix(0, unboundAt).UTC()
	return SessionBindingResult{BindingID: openID, EpochID: openEpoch, Role: openRole, BoundAt: time.Unix(0, openBound).UTC(), UnboundAt: &end, EndReason: "unbind", Revision: revision}, nil
}

func readSessionAPIRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM pasture_session_api_state WHERE singleton_id = 0`).Scan(&revision); err != nil {
		return 0, bindingStoreFault("read session API revision", err)
	}
	return revision, nil
}

func bumpSessionAPIRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	result, err := tx.ExecContext(ctx, `UPDATE pasture_session_api_state SET revision = revision + 1 WHERE singleton_id = 0`)
	if err != nil {
		return 0, bindingStoreFault("bump session API revision", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pasture_session_api_state(singleton_id, revision, dirty, completed_through_jid) VALUES (0, 1, 0, 0)`); err != nil {
			return 0, bindingStoreFault("initialize session API revision", err)
		}
		return 1, nil
	}
	return readSessionAPIRevision(ctx, tx)
}

func validateBindingKey(harness, session string) error {
	for _, value := range []string{harness, session} {
		if value == "" || len(value) > 512 || strings.Contains(value, "/") {
			return fmt.Errorf("harness and session must be non-empty, at most 512 bytes, and contain no slashes")
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return fmt.Errorf("harness and session must contain no control characters")
			}
		}
	}
	return nil
}
