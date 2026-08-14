// Package inventory provides the global-installation view over
// the shared registry. It owns no persistence or independent state model.
package inventory

import (
	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

const SchemaID = registry.SchemaID

type Source = registry.Source
type Observation = registry.Observation
type Trust = registry.Trust
type Leaf = registry.Leaf
type Version = registry.Version
type Selector = registry.Selector
type Operation = registry.Operation
type Outcome = registry.Outcome

const (
	OperationNone    = registry.OperationNone
	OperationEnsure  = registry.OperationEnsure
	OperationRemove  = registry.OperationRemove
	OperationInspect = registry.OperationInspect
	OutcomeNone      = registry.OutcomeNone
	OutcomeCompleted = registry.OutcomeCompleted
	OutcomeFailed    = registry.OutcomeFailed
)

func InstallerSource() Source                        { return registry.SourceInstaller }
func HomeManagerSource() Source                      { return registry.SourceHomeManager }
func ParseSource(v string) (Source, error)           { return registry.ParseSource(v) }
func Installed() Observation                         { return registry.ObservationInstalled }
func Absent() Observation                            { return registry.ObservationAbsent }
func Unknown() Observation                           { return registry.ObservationUnknown }
func ParseObservation(v string) (Observation, error) { return registry.ParseObservation(v) }
func TrustNotApplicable() Trust                      { return registry.TrustNotApplicable }
func TrustPending() Trust                            { return registry.TrustPending }
func TrustTrusted() Trust                            { return registry.TrustTrusted }
func ParseTrust(v string) (Trust, error)             { return registry.ParseTrust(v) }
func NewLeaf(path artifact.Path, kind artifact.EntryType, mode artifact.Mode, digest artifact.Digest) (Leaf, error) {
	return registry.NewLeaf(path, kind, mode, digest)
}

// Record is a global row from the shared registry.
type Record struct{ value registry.Record }
type RecordInput struct {
	Cell          cell.Cell
	Source        Source
	Strategy      activation.StrategyKind
	Managed       bool
	ArtifactID    artifact.BundleID
	Version       Version
	Selector      Selector
	Leaves        []Leaf
	CreatedDirs   []artifact.Path
	Observation   Observation
	Trust         Trust
	LastOperation Operation
	LastOutcome   Outcome
	Diagnostic    string
}

func NewRecord(in RecordInput) (Record, error) {
	k, err := registry.GlobalKey(in.Cell)
	if err != nil {
		return Record{}, err
	}
	r, err := registry.NewRecord(registry.RecordInput{Key: k, Source: in.Source, Strategy: in.Strategy, Managed: in.Managed, ArtifactID: in.ArtifactID, Version: in.Version, Selector: in.Selector, Leaves: in.Leaves, CreatedDirs: in.CreatedDirs, Observation: in.Observation, Trust: in.Trust, LastOperation: in.LastOperation, LastOutcome: in.LastOutcome, Diagnostic: in.Diagnostic})
	return Record{value: r}, err
}
func (r Record) Cell() cell.Cell                   { return r.value.Cell() }
func (r Record) Source() Source                    { return r.value.Source() }
func (r Record) Strategy() activation.StrategyKind { return r.value.Strategy() }
func (r Record) Managed() bool                     { return r.value.Managed() }
func (r Record) ArtifactID() artifact.BundleID     { return r.value.ArtifactID() }
func (r Record) Version() Version                  { return r.value.Version() }
func (r Record) Selector() Selector                { return r.value.Selector() }
func (r Record) Leaves() []Leaf                    { return r.value.Leaves() }
func (r Record) CreatedDirs() []artifact.Path      { return r.value.CreatedDirs() }
func (r Record) Observation() Observation          { return r.value.Observation() }
func (r Record) Trust() Trust                      { return r.value.Trust() }
func (r Record) LastOperation() Operation          { return r.value.LastOperation() }
func (r Record) Outcome() Outcome                  { return r.value.LastOutcome() }

// LastAction and LastOutcome are text projections retained for the existing
// status renderer; mutation and construction boundaries use typed values.
func (r Record) LastAction() string {
	if r.value.LastOperation() == registry.OperationNone {
		return ""
	}
	return r.value.LastOperation().String()
}
func (r Record) LastOutcome() string {
	if r.value.LastOutcome() == registry.OutcomeNone {
		return ""
	}
	return r.value.LastOutcome().String()
}
func (r Record) Diagnostic() string { return r.value.Diagnostic() }
func (r Record) IsValid() bool      { return r.value.IsValid() }

// Inventory is a mutable global projection over a caller-owned registry.Store.
// View is the production constructor; persistence remains exclusively owned by
// registry.Load and registry.Save.
type Inventory struct{ store *registry.Store }

func View(store *registry.Store) Inventory { return Inventory{store: store} }

// New is test/construction convenience for callers beginning with an empty
// shared store. Production persistence callers load via registry.Load then View.
func New() Inventory                       { store := registry.New(); return View(&store) }
func (i *Inventory) Upsert(r Record) error { return i.store.Upsert(r.value) }
func (i Inventory) Lookup(c cell.Cell) (Record, bool) {
	k, err := registry.GlobalKey(c)
	if err != nil {
		return Record{}, false
	}
	r, ok := i.store.Lookup(k)
	return Record{value: r}, ok
}
func (i Inventory) Ordered() []Record {
	rows := i.store.Status()
	out := make([]Record, 0, len(rows))
	for _, row := range rows {
		if row.Scope == registry.ScopeGlobal {
			out = append(out, Record{value: row.Record})
		}
	}
	return out
}
func (i Inventory) Len() int { return len(i.Ordered()) }
func Load(path string) (Inventory, error) {
	s, err := registry.Load(path)
	return View(&s), err
}

// UnifiedStatus exposes both scopes from one registry store to new callers.
func UnifiedStatus(store registry.Store) []registry.Status { return store.Status() }

// Projects filters UnifiedStatus without opening or maintaining another store.
func Projects(store registry.Store) []registry.Status { return store.Projects() }
