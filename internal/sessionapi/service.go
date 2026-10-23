package sessionapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/peasantproject"
)

const defaultActiveWindow = 15 * time.Minute

// projectLookupTimeout bounds one project-enrichment lookup. Project labels are
// optional decoration on a session view: a slow Peasant must not hold a Pasture
// read open, and a failure simply leaves the fields empty.
const projectLookupTimeout = 2 * time.Second

// peasantDisabledMessage is the actionable disabled state /api/v1/inspect
// reports. Disabled is the default and is distinct from unavailable.
const peasantDisabledMessage = "Peasant project enrichment is disabled; pass --peasant-db or --peasant-url to `pasture serve` to enable it"

// Service reads the session cache. Ensure is supplied by the serve command and
// runs the journal-aware rebuild when the cache is dirty or behind.
type Service struct {
	DB           *sql.DB
	Now          func() time.Time
	ActiveWindow time.Duration
	Ensure       func(context.Context) error
	// ProjectSource is the opt-in Peasant project enrichment. Nil means the
	// integration is disabled: no Peasant database or network access happens.
	ProjectSource peasantproject.Source
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

// joinedRow is one session row with the EFFECTIVE current binding: the latest
// explicit binding when the session has one, otherwise the open inferred
// derivation.
type joinedRow struct {
	harness        string
	session        string
	firstSeen      int64
	lastEvent      string
	lastJournal    int64
	claimedAt      sql.NullInt64
	bindingIsInfer sql.NullInt64
	bindingID      sql.NullString
	epochID        sql.NullString
	role           sql.NullString
	boundAt        sql.NullInt64
	unboundAt      sql.NullInt64
	unboundReason  sql.NullString
	confidence     sql.NullString
	epochTitle     sql.NullString
	lastSeen       int64
	updated        int64
	bindings       []SessionBindingInterval
}

// bindingKey identifies one session for a batched interval read. The read path
// fetches every page's intervals in a bounded number of queries, never once per
// session.
type bindingKey struct {
	harness string
	session string
}

// inferredBindingColumns is the closed shape the read path requires before it
// reads the inference cache. A store with a missing or differently shaped table
// degrades to explicit-only instead of failing every session read.
var inferredBindingColumns = []string{
	"binding_id", "harness", "session", "epoch_id", "role", "bound_at",
	"unbound_at", "unbound_reason", "inferred_at", "reason", "confidence",
}

// currentBindingLastSeenExpr is the ONE definition of a session's liveness
// instant. The session page, the active/idle view, and the epoch active count
// all measure against it, so an explicit and an inferred binding cannot drift
// into two different notions of "last seen". The aliases are part of the
// contract: every caller joins pasture_session_state AS s, binding_current AS b,
// and pasture_session_claim AS c.
const currentBindingLastSeenExpr = `MAX(COALESCE(s.last_seen, 0), COALESCE(b.bound_at, 0), COALESCE(b.unbound_at, 0), COALESCE(c.claimed_at, 0))`

// currentBindingUpdatedExpr is the freshness twin of
// currentBindingLastSeenExpr, used for the page's deterministic ordering. It is
// separate on purpose: updated_at answers "when did this row last change" while
// last_seen answers "when was this session last alive".
const currentBindingUpdatedExpr = `MAX(COALESCE(s.updated_at, 0), COALESCE(b.bound_at, 0), COALESCE(b.unbound_at, 0), COALESCE(c.claimed_at, 0))`

// bindingCurrentCTE is the one definition of the effective current binding.
// Explicit wins in every state: a session with any explicit binding row is
// described by its latest explicit row, even when that row is closed. Only a
// session with no explicit history at all falls through to its open inferred
// derivation.
func bindingCurrentCTE(inferredUsable bool) string {
	inferredCTE := ""
	inferredUnion := ""
	if inferredUsable {
		inferredCTE = `,
	inferred_ranked AS (
		SELECT harness, session, binding_id, epoch_id, role, bound_at, confidence,
		       ROW_NUMBER() OVER (PARTITION BY harness, session ORDER BY inferred_at DESC, rowid DESC) AS rn
		FROM pasture_session_inferred_binding
		WHERE unbound_at IS NULL
	),
	inferred_current AS (
		SELECT harness, session, binding_id, epoch_id, role, bound_at, confidence
		FROM inferred_ranked WHERE rn = 1
	)`
		inferredUnion = `
	UNION ALL
	SELECT i.harness, i.session, i.binding_id, i.epoch_id, i.role, i.bound_at,
	       NULL, NULL, i.confidence, 1
	FROM inferred_current i
	WHERE NOT EXISTS (
		SELECT 1 FROM explicit_ranked e WHERE e.harness = i.harness AND e.session = i.session
	)`
	}
	return `WITH explicit_ranked AS (
	SELECT harness, session, binding_id, epoch_id, role, bound_at, unbound_at, unbound_reason,
	       ROW_NUMBER() OVER (PARTITION BY harness, session ORDER BY bound_at DESC, rowid DESC) AS rn
	FROM pasture_session_epoch_binding
),
explicit_current AS (
	SELECT harness, session, binding_id, epoch_id, role, bound_at, unbound_at, unbound_reason
	FROM explicit_ranked WHERE rn = 1
)` + inferredCTE + `,
binding_current AS (
	SELECT harness, session, binding_id, epoch_id, role, bound_at, unbound_at, unbound_reason,
	       NULL AS confidence, 0 AS inferred
	FROM explicit_current` + inferredUnion + `
)`
}

// joinedSessionSelect renders the page query over the effective binding. The
// derived columns keep the historical ordering and pagination intact; only the
// current-binding columns come from the union.
func joinedSessionSelect(inferredUsable bool) string {
	inferredInventory := ""
	if inferredUsable {
		inferredInventory = `
	UNION
	SELECT harness, session FROM pasture_session_inferred_binding`
	}
	return bindingCurrentCTE(inferredUsable) + `,
inventory AS (
	SELECT harness, session FROM pasture_session_state
	UNION
	SELECT harness, session FROM pasture_session_claim
	UNION
	SELECT harness, session FROM pasture_session_epoch_binding` + inferredInventory + `
),
joined AS (
	SELECT i.harness, i.session,
	       COALESCE(s.first_seen, 0) AS first_seen,
	       COALESCE(s.last_event, '') AS last_event,
	       COALESCE(s.last_journal_id, 0) AS last_journal,
	       c.claimed_at AS claimed_at,
	       b.inferred, b.binding_id, b.epoch_id, b.role, b.bound_at, b.unbound_at,
	       b.unbound_reason, b.confidence, t.title AS epoch_title,
	       ` + currentBindingLastSeenExpr + ` AS last_seen,
	       ` + currentBindingUpdatedExpr + ` AS updated
	FROM inventory i
	LEFT JOIN pasture_session_state s ON s.harness = i.harness AND s.session = i.session
	LEFT JOIN pasture_session_claim c ON c.harness = i.harness AND c.session = i.session
	LEFT JOIN binding_current b ON b.harness = i.harness AND b.session = i.session
	LEFT JOIN tasks t ON t.id = b.epoch_id
)
SELECT harness, session, first_seen, last_event, last_journal, claimed_at,
       inferred, binding_id, epoch_id, role, bound_at, unbound_at, unbound_reason,
       confidence, epoch_title, last_seen, updated
FROM joined
`
}

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
	cutoff := s.activeCutoff(now)
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
	s.enrichWithProjects(ctx, items)
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
	sessions := []SessionView{s.view(rows[0], now)}
	s.enrichWithProjects(ctx, sessions)
	return Detail{Revision: revision, AsOf: now, Session: sessions[0]}, nil
}

// enrichWithProjects applies the optional Peasant project mapping in one batch.
// A disabled source, a missing mapping, and any source failure all leave the
// project fields empty; enrichment never fails or changes the session read.
func (s *Service) enrichWithProjects(ctx context.Context, sessions []SessionView) {
	if s.ProjectSource == nil || len(sessions) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	keys := make([]peasantproject.Key, 0, len(sessions))
	for i := range sessions {
		keys = append(keys, peasantproject.Key{Harness: sessions[i].Harness, Session: sessions[i].NativeSessionID})
	}
	lookupCtx, cancel := context.WithTimeout(ctx, projectLookupTimeout)
	defer cancel()
	projects, err := s.ProjectSource.Lookup(lookupCtx, keys)
	if err != nil {
		return
	}
	for i := range sessions {
		project, ok := projects[peasantproject.Key{Harness: sessions[i].Harness, Session: sessions[i].NativeSessionID}]
		if !ok {
			continue
		}
		sessions[i].Project = project.Name
		sessions[i].ProjectHash = project.Hash
	}
}

// peasantIntegrationState reports the tri-state integration health for
// /api/v1/inspect. A nil source is disabled and never reported as unavailable.
func (s *Service) peasantIntegrationState() PeasantIntegration {
	if s.ProjectSource == nil {
		return PeasantIntegration{
			Enabled: false,
			Source:  peasantproject.SourceDisabled,
			Status:  PeasantIntegrationDisabled,
			Message: peasantDisabledMessage,
		}
	}
	status := s.ProjectSource.Status()
	state := PeasantIntegrationUnavailable
	switch status.State {
	case peasantproject.StateOK:
		state = PeasantIntegrationOK
	case peasantproject.StateDisabled:
		state = PeasantIntegrationDisabled
	}
	return PeasantIntegration{
		Enabled: status.Enabled,
		Source:  status.Source,
		Status:  state,
		Message: status.Message,
	}
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

// activeCutoff is the ONE definition of the active/idle boundary. Every site
// that decides active vs idle (the SQL state filters, the view, and the epoch
// counts) takes the cutoff from here, so the rule cannot drift between the
// query that selects rows and the view that labels them.
func (s *Service) activeCutoff(now time.Time) int64 {
	return now.Add(-s.ActiveWindow).UnixNano()
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
	// The inferred table is an additive derivation cache. A store whose table is
	// absent or shaped differently degrades to explicit-only here rather than
	// failing the whole session read.
	inferredUsable := inferredBindingUsable(ctx, tx)

	query := joinedSessionSelect(inferredUsable) + where + " ORDER BY last_journal DESC, updated DESC, harness, session LIMIT ?"
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
			&row.claimedAt, &row.bindingIsInfer, &row.bindingID, &row.epochID, &row.role, &row.boundAt,
			&row.unboundAt, &row.unboundReason, &row.confidence, &row.epochTitle, &row.lastSeen, &row.updated,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning session: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating sessions: %w", err)
	}
	if err := attachBindingIntervals(ctx, tx, out, inferredUsable); err != nil {
		return nil, 0, err
	}
	return out, revision, nil
}

// inferredBindingUsable reports whether the inference cache carries the shape
// this build reads. A missing table, a failed probe, or a table missing any
// required column is "not usable": the caller serves explicit bindings only.
// It never returns an error, because a bad inference cache must not fail a
// session read.
func inferredBindingUsable(ctx context.Context, tx *sql.Tx) bool {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(pasture_session_inferred_binding)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	have := make(map[string]struct{}, len(inferredBindingColumns))
	for rows.Next() {
		var cid, notNull, pk int
		var name, declaredType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &declaredType, &notNull, &defaultValue, &pk); err != nil {
			return false
		}
		have[name] = struct{}{}
	}
	if rows.Err() != nil {
		return false
	}
	for _, required := range inferredBindingColumns {
		if _, ok := have[required]; !ok {
			return false
		}
	}
	return true
}

// attachBindingIntervals loads every interval for the page's sessions. One
// bounded query per interval table keeps the read path free of per-session
// queries while leaving page cardinality untouched.
func attachBindingIntervals(ctx context.Context, tx *sql.Tx, rows []joinedRow, inferredUsable bool) error {
	if len(rows) == 0 {
		return nil
	}
	keys := make([]bindingKey, 0, len(rows))
	seen := make(map[bindingKey]struct{}, len(rows))
	for _, row := range rows {
		key := bindingKey{harness: row.harness, session: row.session}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	intervals, err := loadBindingIntervals(ctx, tx, keys, inferredUsable)
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].bindings = intervals[bindingKey{harness: rows[i].harness, session: rows[i].session}]
	}
	return nil
}

// loadBindingIntervals reads explicit intervals, then inferred intervals when
// the inference table is usable. An absent or incompatible inferred table is
// normal: inference is additive, and the explicit history is the authoritative
// floor the read model must always be able to serve.
func loadBindingIntervals(ctx context.Context, tx *sql.Tx, keys []bindingKey, inferredUsable bool) (map[bindingKey][]SessionBindingInterval, error) {
	out := make(map[bindingKey][]SessionBindingInterval, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	if err := queryBindingIntervals(ctx, tx, "pasture_session_epoch_binding", BindingSourceExplicit, keys, out); err != nil {
		return nil, err
	}
	if inferredUsable {
		if err := queryBindingIntervals(ctx, tx, "pasture_session_inferred_binding", BindingSourceInferred, keys, out); err != nil {
			return nil, err
		}
	}
	for key := range out {
		sortBindingIntervals(out[key])
	}
	return out, nil
}

// bindingIntervalSelect reads the shared column shape of both interval tables.
// Explicit rows have no confidence column and are verified by definition;
// inferred rows store their own label. The table name is a package constant,
// never caller input.
const bindingIntervalSelect = `
SELECT b.harness, b.session, b.binding_id, b.role, b.bound_at, b.unbound_at, b.unbound_reason,
       b.epoch_id, COALESCE(t.title, ''), %s
FROM %s AS b
JOIN keys k ON k.harness = b.harness AND k.session = b.session
LEFT JOIN tasks t ON t.id = b.epoch_id`

// intervalConfidenceExpr is the per-table confidence expression: the explicit
// table stores authority, the inferred table stores its derivation label. A
// legacy or corrupt label degrades to observed rather than failing the read;
// NULLIF keeps an empty label from leaking through as a valid token.
func intervalConfidenceExpr(source BindingSource) string {
	if source == BindingSourceExplicit {
		return "'" + string(ConfidenceVerified) + "'"
	}
	return "COALESCE(NULLIF(b.confidence, ''), '" + string(ConfidenceObserved) + "')"
}

func queryBindingIntervals(ctx context.Context, tx *sql.Tx, table string, source BindingSource, keys []bindingKey, out map[bindingKey][]SessionBindingInterval) error {
	keyClause, args := bindingKeyValues(keys)
	statement := keyClause + fmt.Sprintf(bindingIntervalSelect, intervalConfidenceExpr(source), table)
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return fmt.Errorf("querying %s binding intervals: %w — verify the table matches the session interval contract", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var key bindingKey
		var bindingID, role, epochID, epochTitle, confidence string
		var boundAt int64
		var unboundAt sql.NullInt64
		var unboundReason sql.NullString
		if err := rows.Scan(&key.harness, &key.session, &bindingID, &role, &boundAt,
			&unboundAt, &unboundReason, &epochID, &epochTitle, &confidence); err != nil {
			return fmt.Errorf("scanning %s binding interval: %w", table, err)
		}
		interval := SessionBindingInterval{
			BindingID:  bindingID,
			Role:       role,
			BoundAt:    time.Unix(0, boundAt).UTC(),
			Source:     source,
			Confidence: normalizeConfidence(confidence, source),
		}
		if epochID != "" {
			interval.Epoch = &EpochRef{ID: epochID, Title: epochTitle}
		}
		if unboundAt.Valid {
			end := unboundAt.Int64
			if end < boundAt {
				// A legacy or corrupt row can end before it begins. Clamp to a
				// zero-length interval so the projection stays well ordered
				// instead of panicking or emitting an inverted range.
				end = boundAt
			}
			unbound := time.Unix(0, end).UTC()
			interval.UnboundAt = &unbound
			interval.EndReason = unboundReason.String
			if interval.EndReason == "" && source == BindingSourceExplicit {
				interval.EndReason = "unbind"
			}
		}
		out[key] = append(out[key], interval)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating %s binding intervals: %w", table, err)
	}
	return nil
}

// normalizeConfidence keeps a malformed stored label from escaping as a wire
// value. Explicit bindings are verified by construction; an inferred row with
// an unknown label is degraded to observed instead of erroring the read.
func normalizeConfidence(raw string, source BindingSource) Confidence {
	if source == BindingSourceExplicit {
		return ConfidenceVerified
	}
	parsed := Confidence(raw)
	for _, token := range confidenceTokens() {
		if parsed == token {
			return parsed
		}
	}
	return ConfidenceObserved
}

// bindingKeyValues renders the requested sessions as one VALUES CTE, so both
// interval reads stay bounded by page size instead of one query per session.
func bindingKeyValues(keys []bindingKey) (string, []any) {
	var values strings.Builder
	values.WriteString("WITH keys(harness, session) AS (VALUES ")
	args := make([]any, 0, len(keys)*2)
	for i, key := range keys {
		if i > 0 {
			values.WriteString(", ")
		}
		values.WriteString("(?, ?)")
		args = append(args, key.harness, key.session)
	}
	values.WriteString(")")
	return values.String(), args
}

// sortBindingIntervals orders intervals by boundAt ascending, then bindingId.
// Explicit rows sort before inferred rows only when both keys tie, which keeps
// the order total without changing the documented ordering.
func sortBindingIntervals(intervals []SessionBindingInterval) {
	sort.SliceStable(intervals, func(i, j int) bool {
		left, right := intervals[i], intervals[j]
		if !left.BoundAt.Equal(right.BoundAt) {
			return left.BoundAt.Before(right.BoundAt)
		}
		if left.BindingID != right.BindingID {
			return left.BindingID < right.BindingID
		}
		return left.Source == BindingSourceExplicit && right.Source != BindingSourceExplicit
	})
}

func (s *Service) view(row joinedRow, now time.Time) SessionView {
	view := SessionView{Harness: row.harness, NativeSessionID: row.session}
	view.Bindings = row.bindings
	if view.Bindings == nil {
		view.Bindings = []SessionBindingInterval{}
	}
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
	inferred := row.bindingIsInfer.Valid && row.bindingIsInfer.Int64 != 0
	if row.bindingID.Valid {
		source := BindingSourceExplicit
		confidence := ConfidenceVerified
		if inferred {
			source = BindingSourceInferred
			confidence = normalizeConfidence(row.confidence.String, BindingSourceInferred)
		}
		view.Binding = &SessionBinding{
			BindingID:  row.bindingID.String,
			Role:       row.role.String,
			BoundAt:    time.Unix(0, row.boundAt.Int64).UTC(),
			Source:     source,
			Confidence: confidence,
		}
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
		endedAt := row.unboundAt.Int64
		if row.boundAt.Valid && endedAt < row.boundAt.Int64 {
			endedAt = row.boundAt.Int64
		}
		view.End = &EndMarker{EndedAt: time.Unix(0, endedAt).UTC(), EndReason: reason}
	case !row.bindingID.Valid:
		view.Activity.State = StateUnknown
	case row.lastSeen >= s.activeCutoff(now):
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
