package acceptance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

type SemanticKind string

const (
	SemanticGraph       SemanticKind = "graph"
	SemanticAssignments SemanticKind = "assignments"
	SemanticDecisions   SemanticKind = "decisions"
	SemanticEvidence    SemanticKind = "evidence"
	SemanticActivities  SemanticKind = "activities"
	SemanticEvents      SemanticKind = "events"
	SemanticJournal     SemanticKind = "journal"
	SemanticProjection  SemanticKind = "projection"
)

type SnapshotLimits struct {
	MaxTables       int
	MaxRows         int
	MaxRowsPerTable int
	MaxCellBytes    int
	MaxTotalBytes   int
}

func DefaultSnapshotLimits() SnapshotLimits {
	return SnapshotLimits{MaxTables: 256, MaxRows: 1_000_000, MaxRowsPerTable: 250_000, MaxCellBytes: 1 << 20, MaxTotalBytes: 64 << 20}
}

type CanonicalRow struct {
	Identity string `json:"identity"`
	Value    string `json:"value"`
}

type TableSnapshot struct {
	Name       string         `json:"name"`
	Columns    []string       `json:"columns"`
	Rows       []CanonicalRow `json:"rows"`
	RowCount   int            `json:"rowCount"`
	ByteDigest string         `json:"byteDigest"`
}

type SemanticSnapshot struct {
	Kind       SemanticKind    `json:"kind"`
	Tables     []TableSnapshot `json:"tables"`
	RowCount   int             `json:"rowCount"`
	ByteDigest string          `json:"byteDigest"`
}

type StoreSnapshot struct {
	Tables          []TableSnapshot    `json:"tables"`
	Semantics       []SemanticSnapshot `json:"semantics"`
	RowCount        int                `json:"rowCount"`
	ByteDigest      string             `json:"byteDigest"`
	UnrelatedDigest string             `json:"unrelatedDigest"`
}

var semanticTables = map[SemanticKind][]string{
	SemanticGraph:       {"agents", "agents_human", "agents_ml", "agents_software", "tasks", "edges", "labels", "comments"},
	SemanticAssignments: {"journal_authority_assignment_episodes", "journal_authority_assignment_transitions"},
	SemanticDecisions:   {"journal_decisions"},
	SemanticEvidence:    {"journal_evidence"},
	SemanticActivities:  {"activities"},
	SemanticEvents:      {"journal_task_events", "journal_task_event_contexts", "audit_events", "context_edges"},
	SemanticJournal:     {"journal", "journal_operations", "journal_operation_result_slots", "journal_authorities", "journal_authority_bootstraps", "journal_authority_assignment_episodes", "journal_authority_assignment_transitions", "journal_decisions", "journal_evidence", "journal_task_events", "journal_task_event_contexts", "sqlite_sequence"},
	SemanticProjection:  {"tasks", "edges", "labels", "comments", "task_attributions"},
}

func SnapshotFile(ctx context.Context, path string, limits SnapshotLimits) (StoreSnapshot, error) {
	if ctx == nil {
		return StoreSnapshot{}, errors.New("acceptance snapshot: context is nil; pass a live or cancelled context so read cancellation is explicit")
	}
	if strings.TrimSpace(path) == "" {
		return StoreSnapshot{}, errors.New("acceptance snapshot: database path is empty; close the file-backed production store and pass its pasture.db path")
	}
	if err := validateSnapshotLimits(limits); err != nil {
		return StoreSnapshot{}, err
	}
	u := url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Set("mode", "ro")
	query.Set("_pragma", "query_only(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return StoreSnapshot{}, fmt.Errorf("acceptance snapshot: open %q read-only failed; no store bytes were changed; close writers and verify the path: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return StoreSnapshot{}, fmt.Errorf("acceptance snapshot: read-only connection to %q failed before capture; verify it is a valid accessible SQLite store: %w", path, err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return StoreSnapshot{}, fmt.Errorf("acceptance snapshot: begin consistent read-only transaction for %q: %w", path, err)
	}
	defer tx.Rollback()

	names, err := tableNames(ctx, tx, limits.MaxTables)
	if err != nil {
		return StoreSnapshot{}, err
	}
	result := StoreSnapshot{}
	budget := snapshotByteBudget{max: limits.MaxTotalBytes}
	for _, name := range names {
		table, err := snapshotTable(ctx, tx, name, limits, &budget)
		if err != nil {
			return StoreSnapshot{}, err
		}
		if result.RowCount+table.RowCount > limits.MaxRows {
			return StoreSnapshot{}, fmt.Errorf("acceptance snapshot: store exceeds MaxRows=%d while reading table %q; increase the reviewed bound or narrow the fixture", limits.MaxRows, name)
		}
		result.RowCount += table.RowCount
		result.Tables = append(result.Tables, table)
	}
	result.ByteDigest = digestTables(result.Tables)
	result.Semantics = semanticSnapshots(result.Tables)
	result.UnrelatedDigest = digestTables(unrelatedTables(result.Tables))
	if err := tx.Commit(); err != nil {
		return StoreSnapshot{}, fmt.Errorf("acceptance snapshot: finish read-only transaction for %q: %w", path, err)
	}
	return result, nil
}

func validateSnapshotLimits(l SnapshotLimits) error {
	if l.MaxTables <= 0 || l.MaxRows <= 0 || l.MaxRowsPerTable <= 0 || l.MaxCellBytes <= 0 || l.MaxTotalBytes <= 0 {
		return errors.New("acceptance snapshot: every bound must be positive; use DefaultSnapshotLimits or explicit finite limits")
	}
	if l.MaxRowsPerTable > l.MaxRows {
		return errors.New("acceptance snapshot: MaxRowsPerTable cannot exceed MaxRows")
	}
	return nil
}

func tableNames(ctx context.Context, tx *sql.Tx, limit int) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND (name NOT LIKE 'sqlite_%' OR name = 'sqlite_sequence') ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("acceptance snapshot: list SQLite tables: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("acceptance snapshot: scan table inventory: %w", err)
		}
		if len(names) == limit {
			return nil, fmt.Errorf("acceptance snapshot: table count exceeds MaxTables=%d at %q", limit, name)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("acceptance snapshot: iterate table inventory: %w", err)
	}
	return names, nil
}

type columnInfo struct {
	name string
	pk   int
}

type snapshotByteBudget struct {
	max  int
	used int
}

func (b *snapshotByteBudget) reserve(size int, where string) error {
	if size < 0 || size > b.max-b.used {
		return fmt.Errorf("acceptance snapshot: canonical retained data exceeds MaxTotalBytes=%d before retaining %s (used=%d, requested=%d); increase the reviewed bound or reduce the fixture", b.max, where, b.used, size)
	}
	b.used += size
	return nil
}

func snapshotTable(ctx context.Context, tx *sql.Tx, name string, limits SnapshotLimits, budget *snapshotByteBudget) (TableSnapshot, error) {
	quoted := quoteIdentifier(name)
	infoRows, err := tx.QueryContext(ctx, "PRAGMA table_info("+quoted+")")
	if err != nil {
		return TableSnapshot{}, fmt.Errorf("acceptance snapshot: inspect columns for %q: %w", name, err)
	}
	var info []columnInfo
	for infoRows.Next() {
		var cid, notnull, pk int
		var col, typ string
		var defaultValue any
		if err := infoRows.Scan(&cid, &col, &typ, &notnull, &defaultValue, &pk); err != nil {
			_ = infoRows.Close()
			return TableSnapshot{}, fmt.Errorf("acceptance snapshot: scan column for %q: %w", name, err)
		}
		info = append(info, columnInfo{name: col, pk: pk})
	}
	if err := infoRows.Close(); err != nil {
		return TableSnapshot{}, fmt.Errorf("acceptance snapshot: close column query for %q: %w", name, err)
	}
	if len(info) == 0 {
		return TableSnapshot{}, fmt.Errorf("acceptance snapshot: table %q has no inspectable columns", name)
	}
	retainedMetadataBytes := len(name)
	for _, col := range info {
		retainedMetadataBytes += len(col.name)
	}
	if err := budget.reserve(retainedMetadataBytes, fmt.Sprintf("schema for table %q", name)); err != nil {
		return TableSnapshot{}, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quoted)
	if err != nil {
		return TableSnapshot{}, fmt.Errorf("acceptance snapshot: read table %q: %w", name, err)
	}
	defer rows.Close()
	table := TableSnapshot{Name: name}
	for _, col := range info {
		table.Columns = append(table.Columns, col.name)
	}
	for rows.Next() {
		if len(table.Rows) == limits.MaxRowsPerTable {
			return TableSnapshot{}, fmt.Errorf("acceptance snapshot: table %q exceeds MaxRowsPerTable=%d", name, limits.MaxRowsPerTable)
		}
		values := make([]any, len(info))
		pointers := make([]any, len(info))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return TableSnapshot{}, fmt.Errorf("acceptance snapshot: scan row %d from %q: %w", len(table.Rows), name, err)
		}
		cellSizes := make([]int, len(values))
		valueBytes := 0
		identityBytes := 0
		hasPrimaryKey := false
		for i, value := range values {
			size, err := canonicalCellSize(value, limits.MaxCellBytes)
			if err != nil {
				return TableSnapshot{}, fmt.Errorf("acceptance snapshot: table %q row %d column %q: %w", name, len(table.Rows), info[i].name, err)
			}
			cellSizes[i] = size
			valueBytes += 16 + size
			if info[i].pk > 0 {
				hasPrimaryKey = true
				identityBytes += 16 + size
			}
		}
		if name == "sqlite_sequence" && len(cellSizes) > 0 {
			identityBytes = 16 + cellSizes[0]
		} else if !hasPrimaryKey {
			identityBytes = 16 + valueBytes
		}
		if err := budget.reserve(valueBytes+identityBytes, fmt.Sprintf("row %d from table %q", len(table.Rows), name)); err != nil {
			return TableSnapshot{}, err
		}
		encoded := make([]string, len(values))
		for i, value := range values {
			cell, err := canonicalCell(value, limits.MaxCellBytes)
			if err != nil {
				return TableSnapshot{}, fmt.Errorf("acceptance snapshot: table %q row %d column %q: %w", name, len(table.Rows), info[i].name, err)
			}
			if len(cell) != cellSizes[i] {
				return TableSnapshot{}, fmt.Errorf("acceptance snapshot: canonical size mismatch for table %q row %d column %q", name, len(table.Rows), info[i].name)
			}
			encoded[i] = cell
		}
		identityParts := make([]string, 0, len(info))
		for ordinal := 1; ordinal <= len(info); ordinal++ {
			for i, col := range info {
				if col.pk == ordinal {
					identityParts = append(identityParts, encoded[i])
				}
			}
		}
		// sqlite_sequence has a unique semantic name key but declares no SQL
		// PRIMARY KEY. Use that key so independent sequence changes compare as
		// changed rows instead of unrelated remove/add pairs.
		if name == "sqlite_sequence" && len(encoded) > 0 {
			identityParts = []string{encoded[0]}
		}
		value := encodeFields(encoded)
		if len(identityParts) == 0 {
			identityParts = []string{value}
		}
		table.Rows = append(table.Rows, CanonicalRow{Identity: encodeFields(identityParts), Value: value})
	}
	if err := rows.Err(); err != nil {
		return TableSnapshot{}, fmt.Errorf("acceptance snapshot: iterate table %q: %w", name, err)
	}
	slices.SortFunc(table.Rows, func(a, b CanonicalRow) int {
		if c := strings.Compare(a.Identity, b.Identity); c != 0 {
			return c
		}
		return strings.Compare(a.Value, b.Value)
	})
	for i := 1; i < len(table.Rows); i++ {
		if table.Rows[i-1].Identity == table.Rows[i].Identity {
			return TableSnapshot{}, fmt.Errorf("acceptance snapshot: table %q has duplicate canonical row identity %q", name, table.Rows[i].Identity)
		}
	}
	table.RowCount = len(table.Rows)
	table.ByteDigest = digestTable(table)
	return table, nil
}

func quoteIdentifier(raw string) string { return `"` + strings.ReplaceAll(raw, `"`, `""`) + `"` }

func canonicalCell(value any, max int) (string, error) {
	switch v := value.(type) {
	case nil:
		return "n", nil
	case int64:
		return "i" + strconv.FormatInt(v, 10), nil
	case float64:
		return "f" + strconv.FormatUint(math.Float64bits(v), 16), nil
	case bool:
		if v {
			return "t", nil
		}
		return "f", nil
	case string:
		if len(v) > max {
			return "", fmt.Errorf("text cell has %d bytes, exceeding MaxCellBytes=%d", len(v), max)
		}
		return "s" + hex.EncodeToString([]byte(v)), nil
	case []byte:
		if len(v) > max {
			return "", fmt.Errorf("blob cell has %d bytes, exceeding MaxCellBytes=%d", len(v), max)
		}
		return "b" + hex.EncodeToString(v), nil
	default:
		return "", fmt.Errorf("unsupported SQLite scan type %T; update the canonical reader for this driver type", value)
	}
}

func canonicalCellSize(value any, max int) (int, error) {
	switch v := value.(type) {
	case nil:
		return 1, nil
	case int64:
		return 1 + len(strconv.FormatInt(v, 10)), nil
	case float64:
		return 1 + len(strconv.FormatUint(math.Float64bits(v), 16)), nil
	case bool:
		return 1, nil
	case string:
		if len(v) > max {
			return 0, fmt.Errorf("text cell has %d bytes, exceeding MaxCellBytes=%d", len(v), max)
		}
		return 1 + 2*len(v), nil
	case []byte:
		if len(v) > max {
			return 0, fmt.Errorf("blob cell has %d bytes, exceeding MaxCellBytes=%d", len(v), max)
		}
		return 1 + 2*len(v), nil
	default:
		return 0, fmt.Errorf("unsupported SQLite scan type %T; update the canonical reader for this driver type", value)
	}
}

func encodeFields(fields []string) string {
	var b strings.Builder
	var size [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		b.WriteString(hex.EncodeToString(size[:]))
		b.WriteString(field)
	}
	return b.String()
}

func digestTable(table TableSnapshot) string {
	h := sha256.New()
	h.Write([]byte(encodeFields(append(slices.Clone(table.Columns), table.Name))))
	for _, row := range table.Rows {
		h.Write([]byte(encodeFields([]string{row.Identity, row.Value})))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func digestTables(tables []TableSnapshot) string {
	copy := slices.Clone(tables)
	slices.SortFunc(copy, func(a, b TableSnapshot) int { return strings.Compare(a.Name, b.Name) })
	h := sha256.New()
	for _, table := range copy {
		h.Write([]byte(encodeFields([]string{table.Name, table.ByteDigest})))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func semanticSnapshots(tables []TableSnapshot) []SemanticSnapshot {
	byName := make(map[string]TableSnapshot, len(tables))
	for _, table := range tables {
		byName[table.Name] = table
	}
	kinds := []SemanticKind{SemanticGraph, SemanticAssignments, SemanticDecisions, SemanticEvidence, SemanticActivities, SemanticEvents, SemanticJournal, SemanticProjection}
	out := make([]SemanticSnapshot, 0, len(kinds))
	for _, kind := range kinds {
		s := SemanticSnapshot{Kind: kind}
		for _, name := range semanticTables[kind] {
			if table, ok := byName[name]; ok {
				if kind == SemanticJournal && name == "sqlite_sequence" {
					table = journalSequenceProjection(table)
				}
				s.Tables = append(s.Tables, table)
				s.RowCount += table.RowCount
			}
		}
		s.ByteDigest = digestTables(s.Tables)
		out = append(out, s)
	}
	return out
}

func journalSequenceProjection(table TableSnapshot) TableSnapshot {
	wantIdentity := encodeFields([]string{"s" + hex.EncodeToString([]byte("journal"))})
	projected := table
	projected.Rows = nil
	for _, row := range table.Rows {
		if row.Identity == wantIdentity {
			projected.Rows = append(projected.Rows, row)
		}
	}
	projected.RowCount = len(projected.Rows)
	projected.ByteDigest = digestTable(projected)
	return projected
}

func unrelatedTables(tables []TableSnapshot) []TableSnapshot {
	related := map[string]bool{}
	for _, names := range semanticTables {
		for _, name := range names {
			related[name] = true
		}
	}
	var out []TableSnapshot
	for _, table := range tables {
		if !related[table.Name] {
			out = append(out, table)
		}
	}
	return out
}

func (s StoreSnapshot) Semantic(kind SemanticKind) (SemanticSnapshot, bool) {
	for _, semantic := range s.Semantics {
		if semantic.Kind == kind {
			return semantic, true
		}
	}
	return SemanticSnapshot{}, false
}

type RowChangeKind string

const (
	RowAdded   RowChangeKind = "added"
	RowChanged RowChangeKind = "changed"
	RowRemoved RowChangeKind = "removed"
)

type RowChange struct {
	Table    string        `json:"table"`
	Identity string        `json:"identity"`
	Kind     RowChangeKind `json:"kind"`
}

// CompareRowChanges returns every exact row-identity change across the complete
// store. Rows sharing a table are compared independently, so permitting one
// changed row never exempts unrelated rows in that table.
func CompareRowChanges(before, after StoreSnapshot) ([]RowChange, error) {
	if err := compareTableStructure(before.Tables, after.Tables); err != nil {
		return nil, err
	}
	type key struct{ table, identity string }
	old := map[key]string{}
	next := map[key]string{}
	for _, table := range before.Tables {
		for _, row := range table.Rows {
			old[key{table.Name, row.Identity}] = row.Value
		}
	}
	for _, table := range after.Tables {
		for _, row := range table.Rows {
			next[key{table.Name, row.Identity}] = row.Value
		}
	}
	var changes []RowChange
	for k, value := range next {
		prior, exists := old[k]
		if !exists {
			changes = append(changes, RowChange{Table: k.table, Identity: k.identity, Kind: RowAdded})
		} else if prior != value {
			changes = append(changes, RowChange{Table: k.table, Identity: k.identity, Kind: RowChanged})
		}
	}
	for k := range old {
		if _, exists := next[k]; !exists {
			changes = append(changes, RowChange{Table: k.table, Identity: k.identity, Kind: RowRemoved})
		}
	}
	slices.SortFunc(changes, compareRowChange)
	return changes, nil
}

// AssertExactRowChanges requires the complete observed delta to equal allowed.
// Duplicate or malformed allowed identities fail rather than widening access.
func AssertExactRowChanges(before, after StoreSnapshot, allowed []RowChange) error {
	want := slices.Clone(allowed)
	for i, change := range want {
		if change.Table == "" || change.Identity == "" || (change.Kind != RowAdded && change.Kind != RowChanged && change.Kind != RowRemoved) {
			return fmt.Errorf("acceptance snapshot: allowed row change %d is malformed; table, identity, and a valid change kind are required", i)
		}
	}
	slices.SortFunc(want, compareRowChange)
	for i := 1; i < len(want); i++ {
		if compareRowChange(want[i-1], want[i]) == 0 {
			return fmt.Errorf("acceptance snapshot: duplicate allowed row change for table %q identity %q kind %q", want[i].Table, want[i].Identity, want[i].Kind)
		}
	}
	got, err := CompareRowChanges(before, after)
	if err != nil {
		return err
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("acceptance snapshot: exact row delta mismatch; observed=%v allowed=%v", got, want)
	}
	return nil
}

func compareTableStructure(before, after []TableSnapshot) error {
	left, err := tableStructureMap(before, "before")
	if err != nil {
		return err
	}
	right, err := tableStructureMap(after, "after")
	if err != nil {
		return err
	}
	leftNames := make([]string, 0, len(left))
	rightNames := make([]string, 0, len(right))
	for name := range left {
		leftNames = append(leftNames, name)
	}
	for name := range right {
		rightNames = append(rightNames, name)
	}
	slices.Sort(leftNames)
	slices.Sort(rightNames)
	if !slices.Equal(leftNames, rightNames) {
		return fmt.Errorf("acceptance snapshot: table inventory mismatch before row comparison; before=%v after=%v", leftNames, rightNames)
	}
	for _, name := range leftNames {
		if !slices.Equal(left[name], right[name]) {
			return fmt.Errorf("acceptance snapshot: canonical column schema mismatch for table %q before row comparison; before=%v after=%v", name, left[name], right[name])
		}
	}
	return nil
}

func tableStructureMap(tables []TableSnapshot, side string) (map[string][]string, error) {
	result := make(map[string][]string, len(tables))
	for i, table := range tables {
		if table.Name == "" || table.Columns == nil {
			return nil, fmt.Errorf("acceptance snapshot: %s table[%d] has an empty name or nil canonical column schema", side, i)
		}
		if _, exists := result[table.Name]; exists {
			return nil, fmt.Errorf("acceptance snapshot: %s snapshot repeats table %q", side, table.Name)
		}
		result[table.Name] = table.Columns
	}
	return result, nil
}

func compareRowChange(a, b RowChange) int {
	if c := strings.Compare(a.Table, b.Table); c != 0 {
		return c
	}
	if c := strings.Compare(a.Identity, b.Identity); c != 0 {
		return c
	}
	return strings.Compare(string(a.Kind), string(b.Kind))
}

func Delta(before, after SemanticSnapshot) (ExactDelta, error) {
	if before.Kind != after.Kind {
		return ExactDelta{}, fmt.Errorf("acceptance snapshot: cannot diff %q against %q", before.Kind, after.Kind)
	}
	if err := compareTableStructure(before.Tables, after.Tables); err != nil {
		return ExactDelta{}, err
	}
	type keyed struct{ table, id string }
	old := map[keyed]string{}
	next := map[keyed]string{}
	for _, table := range before.Tables {
		for _, row := range table.Rows {
			old[keyed{table.Name, row.Identity}] = row.Value
		}
	}
	for _, table := range after.Tables {
		for _, row := range table.Rows {
			next[keyed{table.Name, row.Identity}] = row.Value
		}
	}
	d := ExactDelta{Added: []string{}, Changed: []string{}, Removed: []string{}, RowCount: after.RowCount, ByteDigest: after.ByteDigest}
	for key, value := range next {
		prior, exists := old[key]
		token := key.table + ":" + key.id
		if !exists {
			d.Added = append(d.Added, token)
		} else if prior != value {
			d.Changed = append(d.Changed, token)
		}
	}
	for key := range old {
		if _, exists := next[key]; !exists {
			d.Removed = append(d.Removed, key.table+":"+key.id)
		}
	}
	slices.Sort(d.Added)
	slices.Sort(d.Changed)
	slices.Sort(d.Removed)
	return d, nil
}
