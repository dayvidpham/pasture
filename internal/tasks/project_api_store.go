package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/peasant-labs/schema"

	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

var (
	// ErrProjectNotFound means the project has no mapping row at all. A project
	// whose last mapping was unbound still has history and is not this error.
	ErrProjectNotFound = errors.New("project mapping not found")

	// ErrProjectNoOpenBinding means the project has no active mapping to close.
	ErrProjectNoOpenBinding = errors.New("no open project binding")

	// ErrProjectBindingConflict means a caller's expectedBinding no longer names
	// the active mapping (or names one when none is active).
	ErrProjectBindingConflict = errors.New("project binding conflict")

	// ErrUnknownProjectEpoch means the requested epoch is not a task in the
	// unified store.
	ErrUnknownProjectEpoch = errors.New("unknown project epoch")

	// ErrProjectRequest marks a bind/unbind request the caller can fix: a
	// malformed project hash, a missing epoch, an unknown role, or padded
	// expectedBinding whitespace. It is a client error, never a store fault.
	ErrProjectRequest = errors.New("invalid project binding request")

	// ErrProjectStoreFault marks a failure of the unified database handle or its
	// transaction. No mapping change was committed.
	ErrProjectStoreFault = errors.New("project binding store fault")
)

// DefaultProjectBindingRole is the role recorded when a bind omits one. A
// project mapping describes the project's participation in an epoch; the
// common case is a repository being worked on.
const DefaultProjectBindingRole protocol.RoleId = protocol.RoleWorker

// ProjectBinding is one interval in a project-to-epoch mapping. UnboundAt is nil
// while the interval is the project's active mapping.
type ProjectBinding struct {
	BindingID   string
	ProjectHash string
	EpochID     string
	EpochTitle  string
	Role        string
	BoundAt     time.Time
	UnboundAt   *time.Time
	EndReason   string
}

// ProjectMapping is one project and its mapping history. Binding is the single
// open interval, or nil when the project is currently unmapped. Intervals are
// ordered by BoundAt ascending, then BindingID.
type ProjectMapping struct {
	ProjectHash string
	Binding     *ProjectBinding
	Intervals   []ProjectBinding
}

// ProjectBindRequest is the explicit project-to-epoch bind body.
type ProjectBindRequest struct {
	EpochID         string `json:"epochId"`
	Role            string `json:"role,omitempty"`
	ExpectedBinding string `json:"expectedBinding,omitempty"`
}

// ProjectAPIStore owns the project-to-epoch mapping writes. It reuses the
// tracker's unified database handle so serve and the CLI never open a second
// writer. A project mapping is a separate project-level relationship; it never
// writes or overrides a session binding.
type ProjectAPIStore struct {
	db      *sql.DB
	profile timeouts.Profile
	clock   receipt.Clock
}

// NewProjectAPIStore borrows the tracker's unified handle.
func NewProjectAPIStore(tracker protocol.TaskTracker) (*ProjectAPIStore, error) {
	store, ok := tracker.(sessionTailStore)
	if !ok || store.auditDBHandle() == nil {
		return nil, projectStoreFault("construct project store", errors.New("the tracker has no unified database handle"))
	}
	return &ProjectAPIStore{db: store.auditDBHandle(), profile: store.lifecycleTimeoutProfile(), clock: wallClock{}}, nil
}

// NewProjectAPIStoreWithDB is the test and embedding seam. Production callers
// use NewProjectAPIStore.
func NewProjectAPIStoreWithDB(db *sql.DB, profile timeouts.Profile, clock receipt.Clock) *ProjectAPIStore {
	return &ProjectAPIStore{db: db, profile: profile, clock: clock}
}

// NewProjectAPIStoreFromSessionAPIStore reuses the serve process's already-open
// write handle. The session API store and the project store write the same
// unified file, so sharing one handle keeps the serve process to one writer.
func NewProjectAPIStoreFromSessionAPIStore(store *SessionAPIStore) (*ProjectAPIStore, error) {
	if store == nil || store.db == nil {
		return nil, projectStoreFault("construct project store", errors.New("the session API store has no unified database handle"))
	}
	return &ProjectAPIStore{db: store.db, profile: store.profile, clock: store.clock}, nil
}

func (s *ProjectAPIStore) now() int64 {
	clock := s.clock
	if clock == nil {
		clock = wallClock{}
	}
	return clock.Now().UTC().UnixNano()
}

// List returns every project with an active mapping. Projects whose mappings
// are all closed are omitted; their history remains available from Get.
func (s *ProjectAPIStore) List(ctx context.Context) ([]ProjectMapping, error) {
	if s == nil || s.db == nil {
		return nil, projectStoreFault("list project mappings", errors.New("no database handle"))
	}
	bounded, cancel := context.WithTimeout(nonNilTaskContext(ctx), s.profile.WorkflowResult())
	defer cancel()
	tx, err := s.db.BeginTx(bounded, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, projectStoreFault("begin list transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(bounded, `
		SELECT b.project_hash, b.binding_id, b.epoch_id, b.role, b.bound_at, COALESCE(t.title, '')
		FROM pasture_project_epoch_binding b
		LEFT JOIN tasks t ON t.id = b.epoch_id
		WHERE b.unbound_at IS NULL
		ORDER BY b.project_hash`)
	if err != nil {
		return nil, projectStoreFault("list project mappings", err)
	}
	defer rows.Close()
	out := make([]ProjectMapping, 0, 8)
	for rows.Next() {
		var mapping ProjectMapping
		var binding ProjectBinding
		var boundAt int64
		if err := rows.Scan(&mapping.ProjectHash, &binding.BindingID, &binding.EpochID, &binding.Role, &boundAt, &binding.EpochTitle); err != nil {
			return nil, projectStoreFault("scan project mapping", err)
		}
		binding.ProjectHash = mapping.ProjectHash
		binding.BoundAt = time.Unix(0, boundAt).UTC()
		mapping.Binding = &binding
		out = append(out, mapping)
	}
	if err := rows.Err(); err != nil {
		return nil, projectStoreFault("iterate project mappings", err)
	}
	return out, nil
}

// Get returns one project's active mapping and full interval history. A project
// that was never mapped is ErrProjectNotFound.
func (s *ProjectAPIStore) Get(ctx context.Context, projectHash string) (ProjectMapping, error) {
	if s == nil || s.db == nil {
		return ProjectMapping{}, projectStoreFault("read project mapping", errors.New("no database handle"))
	}
	normalized, err := validateProjectHash(projectHash)
	if err != nil {
		return ProjectMapping{}, err
	}
	bounded, cancel := context.WithTimeout(nonNilTaskContext(ctx), s.profile.WorkflowResult())
	defer cancel()
	tx, err := s.db.BeginTx(bounded, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProjectMapping{}, projectStoreFault("begin project read transaction", err)
	}
	defer func() { _ = tx.Rollback() }()
	return readProjectMapping(bounded, tx, normalized)
}

// Bind closes the project's open mapping, if any, and opens the requested one.
// Binding the already-active epoch and role is idempotent. A stale
// ExpectedBinding is ErrProjectBindingConflict; an epoch absent from tasks is
// ErrUnknownProjectEpoch. Request mistakes are ErrProjectRequest, and
// transaction failures are ErrProjectStoreFault.
func (s *ProjectAPIStore) Bind(ctx context.Context, projectHash string, req ProjectBindRequest) (ProjectMapping, error) {
	if s == nil || s.db == nil {
		return ProjectMapping{}, projectStoreFault("bind project", errors.New("no database handle"))
	}
	normalized, err := validateProjectHash(projectHash)
	if err != nil {
		return ProjectMapping{}, err
	}
	epochID, role, expected, err := validateProjectBindRequest(req)
	if err != nil {
		return ProjectMapping{}, err
	}
	bounded, cancel := context.WithTimeout(nonNilTaskContext(ctx), s.profile.WorkflowResult())
	defer cancel()
	tx, err := s.db.BeginTx(bounded, nil)
	if err != nil {
		return ProjectMapping{}, projectStoreFault("begin project bind transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	var epochTitle string
	err = tx.QueryRowContext(bounded, `SELECT COALESCE(title, '') FROM tasks WHERE id = ?`, epochID).Scan(&epochTitle)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectMapping{}, fmt.Errorf("%w: %s is not a task in the store; create the epoch or correct --epoch, then retry", ErrUnknownProjectEpoch, epochID)
	}
	if err != nil {
		return ProjectMapping{}, projectStoreFault("read project epoch", err)
	}

	var openID, openEpoch, openRole string
	var openBound int64
	err = tx.QueryRowContext(bounded, `
		SELECT binding_id, epoch_id, role, bound_at
		FROM pasture_project_epoch_binding
		WHERE project_hash = ? AND unbound_at IS NULL`, normalized).
		Scan(&openID, &openEpoch, &openRole, &openBound)
	switch {
	case err == nil:
		if expected != "" && expected != openID {
			return ProjectMapping{}, fmt.Errorf("%w: expected %s, open is %s", ErrProjectBindingConflict, expected, openID)
		}
		if openEpoch == epochID && openRole == role {
			mapping, readErr := readProjectMapping(bounded, tx, normalized)
			if readErr != nil {
				return ProjectMapping{}, readErr
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return ProjectMapping{}, projectStoreFault("commit idempotent project bind", commitErr)
			}
			return mapping, nil
		}
	case errors.Is(err, sql.ErrNoRows):
		if expected != "" {
			return ProjectMapping{}, fmt.Errorf("%w: expected %s, but the project has no open mapping", ErrProjectBindingConflict, expected)
		}
	default:
		return ProjectMapping{}, projectStoreFault("read open project binding", err)
	}

	now := s.now()
	if err == nil {
		if _, execErr := tx.ExecContext(bounded, `
			UPDATE pasture_project_epoch_binding
			SET unbound_at = ?, unbound_reason = 'rebind'
			WHERE binding_id = ?`, now, openID); execErr != nil {
			return ProjectMapping{}, projectStoreFault("close previous project binding", execErr)
		}
	}
	if _, execErr := tx.ExecContext(bounded, `
		INSERT INTO pasture_project_epoch_binding
			(binding_id, project_hash, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES (?, ?, ?, ?, ?, NULL, NULL)`,
		uuid.NewString(), normalized, epochID, role, now); execErr != nil {
		return ProjectMapping{}, projectStoreFault("insert project binding", execErr)
	}
	mapping, readErr := readProjectMapping(bounded, tx, normalized)
	if readErr != nil {
		return ProjectMapping{}, readErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return ProjectMapping{}, projectStoreFault("commit project bind", commitErr)
	}
	return mapping, nil
}

// Unbind closes the project's open mapping and keeps the interval history. A
// project with no open mapping is ErrProjectNoOpenBinding; a stale
// ExpectedBinding is ErrProjectBindingConflict.
func (s *ProjectAPIStore) Unbind(ctx context.Context, projectHash, expectedBinding string) (ProjectMapping, error) {
	if s == nil || s.db == nil {
		return ProjectMapping{}, projectStoreFault("unbind project", errors.New("no database handle"))
	}
	normalized, err := validateProjectHash(projectHash)
	if err != nil {
		return ProjectMapping{}, err
	}
	if strings.TrimSpace(expectedBinding) != expectedBinding {
		return ProjectMapping{}, projectRequestError("expectedBinding must not have leading or trailing whitespace")
	}
	bounded, cancel := context.WithTimeout(nonNilTaskContext(ctx), s.profile.WorkflowResult())
	defer cancel()
	tx, err := s.db.BeginTx(bounded, nil)
	if err != nil {
		return ProjectMapping{}, projectStoreFault("begin project unbind transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	var openID string
	err = tx.QueryRowContext(bounded, `
		SELECT binding_id FROM pasture_project_epoch_binding
		WHERE project_hash = ? AND unbound_at IS NULL`, normalized).Scan(&openID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectMapping{}, ErrProjectNoOpenBinding
	}
	if err != nil {
		return ProjectMapping{}, projectStoreFault("read open project mapping", err)
	}
	if expectedBinding != "" && expectedBinding != openID {
		return ProjectMapping{}, fmt.Errorf("%w: expected %s, open is %s", ErrProjectBindingConflict, expectedBinding, openID)
	}
	if _, execErr := tx.ExecContext(bounded, `
		UPDATE pasture_project_epoch_binding
		SET unbound_at = ?, unbound_reason = 'unbind'
		WHERE binding_id = ?`, s.now(), openID); execErr != nil {
		return ProjectMapping{}, projectStoreFault("close project binding", execErr)
	}
	mapping, readErr := readProjectMapping(bounded, tx, normalized)
	if readErr != nil {
		return ProjectMapping{}, readErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return ProjectMapping{}, projectStoreFault("commit project unbind", commitErr)
	}
	return mapping, nil
}

// readProjectMapping reads one project's intervals inside an existing
// transaction. The caller supplies the validated project hash.
func readProjectMapping(ctx context.Context, tx *sql.Tx, projectHash string) (ProjectMapping, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT b.binding_id, b.epoch_id, b.role, b.bound_at, b.unbound_at, b.unbound_reason, COALESCE(t.title, '')
		FROM pasture_project_epoch_binding b
		LEFT JOIN tasks t ON t.id = b.epoch_id
		WHERE b.project_hash = ?
		ORDER BY b.bound_at ASC, b.binding_id ASC`, projectHash)
	if err != nil {
		return ProjectMapping{}, projectStoreFault("read project mapping history", err)
	}
	defer rows.Close()
	mapping := ProjectMapping{ProjectHash: projectHash}
	for rows.Next() {
		var interval ProjectBinding
		var boundAt int64
		var unboundAt sql.NullInt64
		var endReason sql.NullString
		if err := rows.Scan(&interval.BindingID, &interval.EpochID, &interval.Role, &boundAt, &unboundAt, &endReason, &interval.EpochTitle); err != nil {
			return ProjectMapping{}, projectStoreFault("scan project mapping history", err)
		}
		interval.ProjectHash = projectHash
		interval.BoundAt = time.Unix(0, boundAt).UTC()
		if unboundAt.Valid {
			end := time.Unix(0, unboundAt.Int64).UTC()
			interval.UnboundAt = &end
			interval.EndReason = endReason.String
		} else {
			if mapping.Binding != nil {
				return ProjectMapping{}, projectStoreFault("read project mapping history", errors.New("more than one open mapping exists for one project; the one-open index is missing or corrupt"))
			}
			current := interval
			mapping.Binding = &current
		}
		mapping.Intervals = append(mapping.Intervals, interval)
	}
	if err := rows.Err(); err != nil {
		return ProjectMapping{}, projectStoreFault("iterate project mapping history", err)
	}
	if len(mapping.Intervals) == 0 {
		return ProjectMapping{}, ErrProjectNotFound
	}
	return mapping, nil
}

func validateProjectHash(raw string) (string, error) {
	hash, err := schema.NewProjectHash(raw)
	if err != nil {
		return "", projectRequestError("the project hash is not a 64-character lowercase hex value; pass the projectHash reported by Peasant")
	}
	return hash.String(), nil
}

func validateProjectBindRequest(req ProjectBindRequest) (epochID, role, expected string, err error) {
	epochID = strings.TrimSpace(req.EpochID)
	if epochID == "" {
		return "", "", "", projectRequestError("epochId is required")
	}
	role = strings.TrimSpace(req.Role)
	if role == "" {
		role = string(DefaultProjectBindingRole)
	}
	if !protocol.RoleId(role).IsValid() {
		return "", "", "", projectRequestError(fmt.Sprintf("role must be one of %v", protocol.AllRoleIds))
	}
	expected = req.ExpectedBinding
	if strings.TrimSpace(expected) != expected {
		return "", "", "", projectRequestError("expectedBinding must not have leading or trailing whitespace")
	}
	return epochID, role, expected, nil
}

func projectRequestError(reason string) error {
	return fmt.Errorf("%w: %s. Fix: correct the project bind request and retry.", ErrProjectRequest, reason)
}

func projectStoreFault(operation string, cause error) error {
	return fmt.Errorf("%w: %s failed: %w. Why: the unified database handle could not complete the operation. Impact: no project mapping change was committed. Fix: retry once the store is writable; if the handle is closed, restart `pasture serve`.", ErrProjectStoreFault, operation, cause)
}

// nonNilTaskContext keeps the store methods safe for callers that pass nil.
func nonNilTaskContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
