package sessionapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultActiveWindow = 15 * time.Minute

// Service reads the session cache. Ensure is supplied by the serve command and
// runs the journal-aware rebuild when the cache is dirty or behind.
type Service struct {
	DB           *sql.DB
	Now          func() time.Time
	ActiveWindow time.Duration
	Ensure       func(context.Context) error
}

func NewService(db *sql.DB) *Service {
	return &Service{DB: db, Now: time.Now, ActiveWindow: defaultActiveWindow}
}

type pageCursor struct {
	Journal int64  `json:"j"`
	Updated int64  `json:"u"`
	Harness string `json:"h"`
	Session string `json:"s"`
}

type joinedRow struct {
	harness       string
	session       string
	firstSeen     int64
	lastEvent     string
	lastJournal   int64
	claimedAt     sql.NullInt64
	bindingID     sql.NullString
	epochID       sql.NullString
	role          sql.NullString
	boundAt       sql.NullInt64
	unboundAt     sql.NullInt64
	unboundReason sql.NullString
	epochTitle    sql.NullString
	lastSeen      int64
	updated       int64
}

const joinedSelect = `
WITH inventory AS (
	SELECT harness, session FROM pasture_session_state
	UNION
	SELECT harness, session FROM pasture_session_claim
	UNION
	SELECT harness, session FROM pasture_session_epoch_binding
),
ranked_bindings AS (
	SELECT harness, session, binding_id, epoch_id, role, bound_at, unbound_at, unbound_reason,
	       ROW_NUMBER() OVER (PARTITION BY harness, session ORDER BY bound_at DESC, rowid DESC) AS rn
	FROM pasture_session_epoch_binding
),
joined AS (
	SELECT i.harness, i.session,
	       COALESCE(s.first_seen, 0) AS first_seen,
	       COALESCE(s.last_event, '') AS last_event,
	       COALESCE(s.last_journal_id, 0) AS last_journal,
	       c.claimed_at AS claimed_at,
	       b.binding_id, b.epoch_id, b.role, b.bound_at, b.unbound_at, b.unbound_reason,
	       t.title AS epoch_title,
	       MAX(COALESCE(s.last_seen, 0), COALESCE(b.bound_at, 0), COALESCE(c.claimed_at, 0)) AS last_seen,
	       MAX(COALESCE(s.updated_at, 0), COALESCE(b.bound_at, 0), COALESCE(c.claimed_at, 0)) AS updated
	FROM inventory i
	LEFT JOIN pasture_session_state s ON s.harness = i.harness AND s.session = i.session
	LEFT JOIN pasture_session_claim c ON c.harness = i.harness AND c.session = i.session
	LEFT JOIN ranked_bindings b ON b.harness = i.harness AND b.session = i.session AND b.rn = 1
	LEFT JOIN tasks t ON t.id = b.epoch_id
)
SELECT harness, session, first_seen, last_event, last_journal, claimed_at,
       binding_id, epoch_id, role, bound_at, unbound_at, unbound_reason,
       epoch_title, last_seen, updated
FROM joined
`

// List returns one page of sessions.
func (s *Service) List(ctx context.Context, opts ListOptions) (Page, error) {
	if s == nil || s.DB == nil {
		return Page{}, errors.New("session API has no database handle")
	}
	if opts.Limit == 0 {
		opts.Limit = 50
	}
	if opts.Limit < 1 || opts.Limit > 100 {
		return Page{}, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidFilter)
	}
	if opts.State != "" {
		switch opts.State {
		case StateActive, StateIdle, StateEnded, StateUnknown:
		default:
			return Page{}, fmt.Errorf("%w: unknown session state %q", ErrInvalidFilter, opts.State)
		}
	}
	if s.ActiveWindow <= 0 {
		return Page{}, errors.New("session API active window must be positive")
	}

	now := s.now()
	where := " WHERE 1=1"
	args := make([]any, 0, 8)
	if opts.Harness != "" {
		where += " AND harness = ?"
		args = append(args, opts.Harness)
	}
	if opts.Epoch != "" {
		where += " AND epoch_id = ?"
		args = append(args, opts.Epoch)
	}
	cutoff := now.Add(-s.ActiveWindow).UnixNano()
	switch opts.State {
	case StateEnded:
		where += " AND unbound_at IS NOT NULL"
	case StateUnknown:
		where += " AND binding_id IS NULL"
	case StateActive:
		where += " AND binding_id IS NOT NULL AND unbound_at IS NULL AND last_seen >= ?"
		args = append(args, cutoff)
	case StateIdle:
		where += " AND binding_id IS NOT NULL AND unbound_at IS NULL AND last_seen < ?"
		args = append(args, cutoff)
	}
	if opts.Cursor != "" {
		cursor, err := decodeCursor(opts.Cursor)
		if err != nil {
			return Page{}, err
		}
		where += " AND (last_journal, updated, harness, session) < (?, ?, ?, ?)"
		args = append(args, cursor.Journal, cursor.Updated, cursor.Harness, cursor.Session)
	}

	rows, revision, err := s.query(ctx, now, where, args, opts.Limit+1)
	if err != nil {
		return Page{}, err
	}
	next := ""
	if len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
		last := rows[len(rows)-1]
		next = encodeCursor(pageCursor{Journal: last.lastJournal, Updated: last.updated, Harness: last.harness, Session: last.session})
	}
	items := make([]SessionView, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.view(row, now))
	}
	return Page{Revision: revision, AsOf: now, Items: items, Next: next}, nil
}

// Get returns one session.
func (s *Service) Get(ctx context.Context, harness, session string) (Detail, error) {
	if s == nil || s.DB == nil {
		return Detail{}, errors.New("session API has no database handle")
	}
	if strings.TrimSpace(harness) == "" || strings.TrimSpace(session) == "" {
		return Detail{}, ErrNotFound
	}
	if s.ActiveWindow <= 0 {
		return Detail{}, errors.New("session API active window must be positive")
	}
	now := s.now()
	rows, revision, err := s.query(ctx, now,
		" WHERE harness = ? AND session = ?", []any{harness, session}, 1)
	if err != nil {
		return Detail{}, err
	}
	if len(rows) == 0 {
		return Detail{}, ErrNotFound
	}
	return Detail{Revision: revision, AsOf: now, Session: s.view(rows[0], now)}, nil
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

func (s *Service) query(ctx context.Context, now time.Time, where string, args []any, limit int) ([]joinedRow, int64, error) {
	if s.Ensure != nil {
		if err := s.Ensure(ctx); err != nil {
			return nil, 0, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("opening session read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var revision, dirty int64
	if err := tx.QueryRowContext(ctx, `SELECT revision, dirty FROM pasture_session_api_state WHERE singleton_id = 0`).
		Scan(&revision, &dirty); err != nil {
		return nil, 0, fmt.Errorf("reading session API revision: %w", err)
	}
	if dirty != 0 {
		return nil, 0, ErrStaleCache
	}

	query := joinedSelect + where + " ORDER BY last_journal DESC, updated DESC, harness, session LIMIT ?"
	queryArgs := append(append([]any(nil), args...), limit)
	rows, err := tx.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying sessions: %w", err)
	}
	defer rows.Close()
	out := make([]joinedRow, 0, limit)
	for rows.Next() {
		var row joinedRow
		if err := rows.Scan(
			&row.harness, &row.session, &row.firstSeen, &row.lastEvent, &row.lastJournal,
			&row.claimedAt, &row.bindingID, &row.epochID, &row.role, &row.boundAt,
			&row.unboundAt, &row.unboundReason, &row.epochTitle, &row.lastSeen, &row.updated,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning session: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating sessions: %w", err)
	}
	return out, revision, nil
}

func (s *Service) view(row joinedRow, now time.Time) SessionView {
	view := SessionView{Harness: row.harness, NativeSessionID: row.session}
	first := firstPositive(row.firstSeen, row.boundAt, row.claimedAt)
	if first > 0 {
		t := time.Unix(0, first).UTC()
		view.Activity.FirstSeen = &t
	}
	if row.lastSeen > 0 {
		t := time.Unix(0, row.lastSeen).UTC()
		view.Activity.LastSeen = &t
	}
	view.Activity.LastEvent = row.lastEvent
	if row.bindingID.Valid {
		view.Binding = &SessionBinding{BindingID: row.bindingID.String, Role: row.role.String, BoundAt: time.Unix(0, row.boundAt.Int64).UTC()}
		if row.epochID.Valid {
			view.Epoch = &EpochRef{ID: row.epochID.String}
			if row.epochTitle.Valid {
				view.Epoch.Title = row.epochTitle.String
			}
		}
	}
	switch {
	case row.unboundAt.Valid:
		view.Activity.State = StateEnded
		reason := row.unboundReason.String
		if reason == "" {
			reason = "unbind"
		}
		view.End = &EndMarker{EndedAt: time.Unix(0, row.unboundAt.Int64).UTC(), EndReason: reason}
	case !row.bindingID.Valid:
		view.Activity.State = StateUnknown
	case row.lastSeen >= now.Add(-s.ActiveWindow).UnixNano():
		view.Activity.State = StateActive
	default:
		view.Activity.State = StateIdle
	}
	return view
}

func firstPositive(rowFirst int64, values ...sql.NullInt64) int64 {
	out := int64(0)
	if rowFirst > 0 {
		out = rowFirst
	}
	for _, value := range values {
		if value.Valid && value.Int64 > 0 && (out == 0 || value.Int64 < out) {
			out = value.Int64
		}
	}
	return out
}

func encodeCursor(cursor pageCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(encoded string) (pageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return pageCursor{}, fmt.Errorf("%w: cursor is not base64", ErrInvalidPageToken)
	}
	var cursor pageCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return pageCursor{}, fmt.Errorf("%w: cursor is not JSON", ErrInvalidPageToken)
	}
	if cursor.Harness == "" || cursor.Session == "" || cursor.Journal < 0 || cursor.Updated < 0 {
		return pageCursor{}, fmt.Errorf("%w: cursor is incomplete", ErrInvalidPageToken)
	}
	return cursor, nil
}
