package inventory

import (
	"fmt"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

// Leaf is one managed direct-file leaf: its canonical relative path, exact
// octal mode, content digest, and entry type. It is the ownership token that
// gates safe update and removal (a leaf is touched only when its live
// (type,digest,mode) still exactly match this record).
type Leaf struct {
	path   artifact.Path
	kind   artifact.EntryType
	mode   artifact.Mode
	digest artifact.Digest
	valid  bool
}

// NewLeaf validates and owns a managed leaf ownership token.
func NewLeaf(path artifact.Path, kind artifact.EntryType, mode artifact.Mode, digest artifact.Digest) (Leaf, error) {
	if path.String() == "" {
		return Leaf{}, cell.NewFault(
			"leaf construction", "canonical relative path",
			"the leaf path is empty",
			"internal/install/inventory.NewLeaf", "recording a managed direct-file leaf",
			"the leaf cannot be re-identified for safe update or removal",
			"construct the path with artifact.NewPath", nil,
		)
	}
	return Leaf{path: path, kind: kind, mode: mode, digest: digest, valid: true}, nil
}

// Path returns the leaf's canonical relative path.
func (l Leaf) Path() artifact.Path { return l.path }

// Type returns the leaf's entry type.
func (l Leaf) Type() artifact.EntryType { return l.kind }

// Mode returns the leaf's exact octal mode.
func (l Leaf) Mode() artifact.Mode { return l.mode }

// Digest returns the leaf's content digest.
func (l Leaf) Digest() artifact.Digest { return l.digest }

// Record is one typed confirmed-inventory record for a mutated cell.
type Record struct {
	cell        cell.Cell
	source      Source
	strategy    activation.StrategyKind
	managed     bool
	artifactID  string
	version     string
	selector    string
	leaves      []Leaf
	createdDirs []artifact.Path
	observation Observation
	trust       Trust
	lastAction  string
	lastOutcome string
	diagnostic  string
	valid       bool
}

// RecordInput carries the fields for a new record. Validation happens in
// NewRecord so no partially-built record ever escapes.
type RecordInput struct {
	Cell       cell.Cell
	Source     Source
	Strategy   activation.StrategyKind
	Managed    bool
	ArtifactID string
	Version    string
	Selector   string
	Leaves     []Leaf
	// CreatedDirs are directory paths, relative to the destination root, that
	// Pasture created while materializing this cell's bundle. They are the
	// ownership token that authorizes a later Remove to reclaim exactly the
	// directory tree Pasture made (empty-only, deepest-first) without orphaning
	// intermediate directories or touching a directory it did not create.
	CreatedDirs []artifact.Path
	Observation Observation
	Trust       Trust
	LastAction  string
	LastOutcome string
	Diagnostic  string
}

// NewRecord validates and constructs a confirmed-inventory record.
func NewRecord(in RecordInput) (Record, error) {
	if !in.Cell.IsValid() {
		return Record{}, cell.NewFault(
			"record construction", "valid cell",
			"the record cell is invalid",
			"internal/install/inventory.NewRecord", "recording a confirmed cell state",
			"the record cannot be ordered or reconciled",
			"construct the cell with cell.New", nil,
		)
	}
	if !in.Source.IsValid() {
		return Record{}, cell.NewFault(
			"record construction", "known control source",
			fmt.Sprintf("cell %s has no valid control source", in.Cell),
			"internal/install/inventory.NewRecord", "recording a confirmed cell state",
			"a record without a control source cannot detect mixed-controller conflicts",
			"set Source to installer or home-manager", nil,
		)
	}
	if in.Strategy.String() == "" {
		return Record{}, cell.NewFault(
			"record construction", "known activation strategy",
			fmt.Sprintf("cell %s has no activation strategy kind", in.Cell),
			"internal/install/inventory.NewRecord", "recording a confirmed cell state",
			"the record cannot describe how the cell was activated",
			"set Strategy to a native-plugin, direct-file, or pending-trust kind", nil,
		)
	}
	if !in.Observation.IsValid() {
		return Record{}, cell.NewFault(
			"record construction", "known observation",
			fmt.Sprintf("cell %s has no valid observation", in.Cell),
			"internal/install/inventory.NewRecord", "recording a confirmed cell state",
			"the record cannot report what remains installed",
			"set Observation to installed, absent, or unknown", nil,
		)
	}
	if !in.Trust.IsValid() {
		return Record{}, cell.NewFault(
			"record construction", "known trust disposition",
			fmt.Sprintf("cell %s has no valid trust disposition", in.Cell),
			"internal/install/inventory.NewRecord", "recording a confirmed cell state",
			"the record could claim hooks are active before host approval",
			"set Trust to not-applicable, pending, or trusted", nil,
		)
	}
	return Record{
		cell:        in.Cell,
		source:      in.Source,
		strategy:    in.Strategy,
		managed:     in.Managed,
		artifactID:  in.ArtifactID,
		version:     in.Version,
		selector:    in.Selector,
		leaves:      append([]Leaf(nil), in.Leaves...),
		createdDirs: append([]artifact.Path(nil), in.CreatedDirs...),
		observation: in.Observation,
		trust:       in.Trust,
		lastAction:  in.LastAction,
		lastOutcome: in.LastOutcome,
		diagnostic:  in.Diagnostic,
		valid:       true,
	}, nil
}

// Accessors.
func (r Record) Cell() cell.Cell                   { return r.cell }
func (r Record) Source() Source                    { return r.source }
func (r Record) Strategy() activation.StrategyKind { return r.strategy }
func (r Record) Managed() bool                     { return r.managed }
func (r Record) ArtifactID() string                { return r.artifactID }
func (r Record) Version() string                   { return r.version }
func (r Record) Selector() string                  { return r.selector }
func (r Record) Leaves() []Leaf                    { return append([]Leaf(nil), r.leaves...) }
func (r Record) CreatedDirs() []artifact.Path      { return append([]artifact.Path(nil), r.createdDirs...) }
func (r Record) Observation() Observation          { return r.observation }
func (r Record) Trust() Trust                      { return r.trust }
func (r Record) LastAction() string                { return r.lastAction }
func (r Record) LastOutcome() string               { return r.lastOutcome }
func (r Record) Diagnostic() string                { return r.diagnostic }
func (r Record) IsValid() bool                     { return r.valid }
