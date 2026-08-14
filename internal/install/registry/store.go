package registry

import (
	"sort"

	"github.com/dayvidpham/pasture/internal/install/cell"
)

// Store is the single authoritative set backing both logical tables.
type Store struct{ records map[string]Record }

func New() Store { return Store{records: map[string]Record{}} }
func (s *Store) Upsert(r Record) error {
	if !r.IsValid() {
		return fault("registry upsert", "valid record", "the record is invalid", "internal/install/registry.Store.Upsert", "updating the registry", "invalid state would be persisted", "construct the record with NewRecord", nil)
	}
	if s.records == nil {
		s.records = map[string]Record{}
	}
	s.records[r.Key().identity()] = r
	return nil
}
func (s Store) Lookup(k Key) (Record, bool) { r, ok := s.records[k.identity()]; return r, ok }
func (s Store) Len() int                    { return len(s.records) }
func (s Store) Ordered() []Record {
	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return lessKey(out[i].Key(), out[j].Key()) })
	return out
}
func lessKey(a, b Key) bool {
	if a.Scope() != b.Scope() {
		return a.Scope() < b.Scope()
	}
	if a.ProjectRoot().String() != b.ProjectRoot().String() {
		return a.ProjectRoot().String() < b.ProjectRoot().String()
	}
	return a.Cell().Index() < b.Cell().Index()
}

// Status is the deterministic unified status projection. Scope is always
// explicit, so equal cells in global and project tables cannot be confused.
type Status struct {
	Scope       Scope
	ProjectRoot ProjectRoot
	Cell        cell.Cell
	Record      Record
}

func (s Store) Status() []Status {
	records := s.Ordered()
	out := make([]Status, 0, len(records))
	for _, r := range records {
		k := r.Key()
		out = append(out, Status{Scope: k.Scope(), ProjectRoot: k.ProjectRoot(), Cell: k.Cell(), Record: r})
	}
	return out
}

// Projects is a filtered view over the same Store, not a second index or file.
func (s Store) Projects() []Status {
	all := s.Status()
	out := make([]Status, 0, len(all))
	for _, row := range all {
		if row.Scope == ScopeProject {
			out = append(out, row)
		}
	}
	return out
}
