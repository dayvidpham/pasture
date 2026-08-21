package registry

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

// Version, Selector, and SharedConfigIdentity are validated ownership values.
// Strings exist only in the wire codec. Their zero values explicitly mean
// absent; every non-zero value is constructor-validated and cannot be forged.
type Version struct{ text string }
type Selector struct{ text string }
type SharedConfigIdentity struct{ text string }

func NewVersion(value string) (Version, error) {
	if err := validateIdentityText("version", value); err != nil {
		return Version{}, err
	}
	return Version{text: value}, nil
}
func NewSelector(value string) (Selector, error) {
	if err := validateIdentityText("selector", value); err != nil {
		return Selector{}, err
	}
	return Selector{text: value}, nil
}
func NewSharedConfigIdentity(value string) (SharedConfigIdentity, error) {
	if err := validateIdentityText("shared config identity", value); err != nil {
		return SharedConfigIdentity{}, err
	}
	return SharedConfigIdentity{text: value}, nil
}
func (v Version) String() string              { return v.text }
func (v Version) IsSet() bool                 { return v.text != "" }
func (s Selector) String() string             { return s.text }
func (s Selector) IsSet() bool                { return s.text != "" }
func (i SharedConfigIdentity) String() string { return i.text }
func (i SharedConfigIdentity) IsSet() bool    { return i.text != "" }
func validateIdentityText(kind, value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fault(kind+" construction", "non-empty canonical text", fmt.Sprintf("%s %q is empty, padded, or contains a control character", kind, value), "internal/install/registry.validateIdentityText", "validating registry ownership identity", "identity comparison would be ambiguous", "use non-empty UTF-8 text without surrounding whitespace or control characters", nil)
	}
	return nil
}

type Source uint8

const (
	SourceInvalid Source = iota
	SourceInstaller
	SourceHomeManager
)

func (s Source) String() string {
	if s == SourceInstaller {
		return "installer"
	}
	if s == SourceHomeManager {
		return "home-manager"
	}
	return "invalid"
}
func (s Source) IsValid() bool { return s == SourceInstaller || s == SourceHomeManager }
func ParseSource(v string) (Source, error) {
	if v == "installer" {
		return SourceInstaller, nil
	}
	if v == "home-manager" {
		return SourceHomeManager, nil
	}
	return SourceInvalid, fault("source decode", "installer or home-manager", fmt.Sprintf("source %q is unknown", v), "internal/install/registry.ParseSource", "decoding a registry record", "controller conflicts could be hidden", "use exactly installer or home-manager", nil)
}

type Observation uint8

const (
	ObservationInvalid Observation = iota
	ObservationInstalled
	ObservationAbsent
	ObservationUnknown
)

func (o Observation) String() string {
	switch o {
	case ObservationInstalled:
		return "installed"
	case ObservationAbsent:
		return "absent"
	case ObservationUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}
func (o Observation) IsValid() bool {
	return o >= ObservationInstalled && o <= ObservationUnknown
}
func ParseObservation(v string) (Observation, error) {
	switch v {
	case "installed":
		return ObservationInstalled, nil
	case "absent":
		return ObservationAbsent, nil
	case "unknown":
		return ObservationUnknown, nil
	default:
		return ObservationInvalid, fault("observation decode", "installed, absent, or unknown", fmt.Sprintf("observation %q is unknown", v), "internal/install/registry.ParseObservation", "decoding a registry record", "status could misreport live state", "use installed, absent, or unknown", nil)
	}
}

type Trust uint8

const (
	TrustInvalid Trust = iota
	TrustNotApplicable
	TrustPending
	TrustTrusted
)

func (t Trust) String() string {
	switch t {
	case TrustNotApplicable:
		return "not-applicable"
	case TrustPending:
		return "pending"
	case TrustTrusted:
		return "trusted"
	default:
		return "invalid"
	}
}
func (t Trust) IsValid() bool { return t >= TrustNotApplicable && t <= TrustTrusted }
func ParseTrust(v string) (Trust, error) {
	switch v {
	case "not-applicable":
		return TrustNotApplicable, nil
	case "pending":
		return TrustPending, nil
	case "trusted":
		return TrustTrusted, nil
	default:
		return TrustInvalid, fault("trust decode", "known trust disposition", fmt.Sprintf("trust %q is unknown", v), "internal/install/registry.ParseTrust", "decoding a registry record", "hooks could be reported active without approval", "use not-applicable, pending, or trusted", nil)
	}
}

type Operation uint8

const (
	OperationNone Operation = iota
	OperationEnsure
	OperationRemove
	OperationInspect
)

func (o Operation) String() string {
	switch o {
	case OperationEnsure:
		return "ensure"
	case OperationRemove:
		return "remove"
	case OperationInspect:
		return "inspect"
	case OperationNone:
		return "none"
	default:
		return "invalid"
	}
}
func ParseOperation(v string) (Operation, error) {
	switch v {
	case "none":
		return OperationNone, nil
	case "ensure":
		return OperationEnsure, nil
	case "remove":
		return OperationRemove, nil
	case "inspect":
		return OperationInspect, nil
	default:
		return OperationNone, fault("operation decode", "known last operation", fmt.Sprintf("operation %q is unknown", v), "internal/install/registry.ParseOperation", "decoding a registry record", "the last action cannot be explained", "use none, ensure, remove, or inspect", nil)
	}
}

type Outcome uint8

const (
	OutcomeNone Outcome = iota
	OutcomeCompleted
	OutcomeFailed
)

func (o Outcome) String() string {
	switch o {
	case OutcomeCompleted:
		return "completed"
	case OutcomeFailed:
		return "failed"
	case OutcomeNone:
		return "none"
	default:
		return "invalid"
	}
}
func ParseOutcome(v string) (Outcome, error) {
	switch v {
	case "none":
		return OutcomeNone, nil
	case "completed":
		return OutcomeCompleted, nil
	case "failed":
		return OutcomeFailed, nil
	default:
		return OutcomeNone, fault("outcome decode", "known last outcome", fmt.Sprintf("outcome %q is unknown", v), "internal/install/registry.ParseOutcome", "decoding a registry record", "the last result cannot be explained", "use none, completed, or failed", nil)
	}
}

// Leaf is exact direct-file ownership evidence.
type Leaf struct {
	path   artifact.Path
	kind   artifact.EntryType
	mode   artifact.Mode
	digest artifact.Digest
	valid  bool
}

func NewLeaf(path artifact.Path, kind artifact.EntryType, mode artifact.Mode, digest artifact.Digest) (Leaf, error) {
	if path.String() == "" || kind.String() == "" || mode.String() == "" || digest.String() == "" {
		return Leaf{}, fault("leaf construction", "complete leaf identity", "a leaf identity field is empty", "internal/install/registry.NewLeaf", "recording file ownership", "safe update and removal cannot prove ownership", "construct every field with artifact validators", nil)
	}
	return Leaf{path: path, kind: kind, mode: mode, digest: digest, valid: true}, nil
}
func (l Leaf) Path() artifact.Path      { return l.path }
func (l Leaf) Type() artifact.EntryType { return l.kind }
func (l Leaf) Mode() artifact.Mode      { return l.mode }
func (l Leaf) Digest() artifact.Digest  { return l.digest }

// SharedConfigOwnership is project-only proof for one exact Pasture-owned entry
// in a harness shared configuration file.
type SharedConfigOwnership struct {
	path     artifact.Path
	identity SharedConfigIdentity
	digest   artifact.Digest
	valid    bool
}

func NewSharedConfigOwnership(path artifact.Path, identity SharedConfigIdentity, digest artifact.Digest) (SharedConfigOwnership, error) {
	if path.String() == "" || !identity.IsSet() || digest.String() == "" {
		return SharedConfigOwnership{}, fault("shared config ownership construction", "complete path, identity, and digest", "shared config ownership is incomplete", "internal/install/registry.NewSharedConfigOwnership", "recording project config ownership", "a later merge could touch an unproved entry", "provide the exact config path, typed entry identity, and digest", nil)
	}
	return SharedConfigOwnership{path: path, identity: identity, digest: digest, valid: true}, nil
}
func (o SharedConfigOwnership) Path() artifact.Path            { return o.path }
func (o SharedConfigOwnership) Identity() SharedConfigIdentity { return o.identity }
func (o SharedConfigOwnership) Digest() artifact.Digest        { return o.digest }

type Record struct {
	key          Key
	source       Source
	strategy     activation.StrategyKind
	managed      bool
	artifactID   artifact.BundleID
	version      Version
	selector     Selector
	leaves       []Leaf
	createdDirs  []artifact.Path
	sharedConfig []SharedConfigOwnership
	observation  Observation
	trust        Trust
	operation    Operation
	outcome      Outcome
	diagnostic   string
	valid        bool
}
type RecordInput struct {
	Key           Key
	Source        Source
	Strategy      activation.StrategyKind
	Managed       bool
	ArtifactID    artifact.BundleID
	Version       Version
	Selector      Selector
	Leaves        []Leaf
	CreatedDirs   []artifact.Path
	SharedConfig  []SharedConfigOwnership
	Observation   Observation
	Trust         Trust
	LastOperation Operation
	LastOutcome   Outcome
	Diagnostic    string
}

func NewRecord(in RecordInput) (Record, error) {
	if !in.Key.IsValid() {
		return Record{}, fault("record construction", "valid scoped key", "the key is invalid", "internal/install/registry.NewRecord", "constructing a registry record", "the record cannot enter a logical table", "construct a global or project key first", nil)
	}
	if in.Source != SourceInstaller && in.Source != SourceHomeManager {
		return Record{}, fault("record construction", "known source", "the source is invalid", "internal/install/registry.NewRecord", "constructing a registry record", "controller ownership is unknown", "use SourceInstaller or SourceHomeManager", nil)
	}
	if !in.Strategy.IsValid() {
		return Record{}, fault("record construction", "known activation strategy", "the strategy is invalid", "internal/install/registry.NewRecord", "constructing a registry record", "reconciliation cannot select a controller", "use a validated activation strategy kind", nil)
	}
	if in.Observation < ObservationInstalled || in.Observation > ObservationUnknown {
		return Record{}, fault("record construction", "known observation", "the observation is invalid", "internal/install/registry.NewRecord", "constructing a registry record", "status cannot report factual state", "use an Observation constant", nil)
	}
	if in.Trust < TrustNotApplicable || in.Trust > TrustTrusted {
		return Record{}, fault("record construction", "known trust disposition", "the trust value is invalid", "internal/install/registry.NewRecord", "constructing a registry record", "status could overclaim hook trust", "use a Trust constant", nil)
	}
	if in.LastOperation < OperationNone || in.LastOperation > OperationInspect || in.LastOutcome < OutcomeNone || in.LastOutcome > OutcomeFailed {
		return Record{}, fault("record construction", "known operation and outcome", "last operation or outcome is invalid", "internal/install/registry.NewRecord", "constructing a registry record", "the last result cannot be explained", "use typed operation and outcome constants", nil)
	}
	if (in.LastOperation == OperationNone) != (in.LastOutcome == OutcomeNone) {
		return Record{}, fault("record construction", "none/none or a concrete operation with a concrete outcome", fmt.Sprintf("last operation %q and outcome %q contradict each other", in.LastOperation, in.LastOutcome), "internal/install/registry.NewRecord", "validating the last operation/outcome pair", "status formats could report different or incomplete retry facts", "use OperationNone with OutcomeNone, or pair ensure/remove/inspect with completed/failed", nil)
	}
	if in.Key.Scope() == ScopeGlobal && len(in.SharedConfig) > 0 {
		return Record{}, fault("record construction", "project-only shared config ownership", "a global record contains shared config ownership", "internal/install/registry.NewRecord", "constructing a global record", "global and project ownership would be conflated", "attach shared config ownership only to a project key", nil)
	}
	if in.ArtifactID.String() != "" {
		if _, err := artifact.ParseBundleID(in.ArtifactID.String()); err != nil {
			return Record{}, fault("record construction", "validated artifact bundle identity", err.Error(), "internal/install/registry.NewRecord", "validating registry ownership", "the installed artifact cannot be compared reliably", "construct the identity with artifact.ParseBundleID", err)
		}
	}
	leaves := append([]Leaf(nil), in.Leaves...)
	for _, leaf := range leaves {
		if !leaf.valid {
			return Record{}, fault("record construction", "validated file ownership", "a managed leaf is the invalid zero value", "internal/install/registry.NewRecord", "canonicalizing file ownership", "safe update and removal cannot prove the leaf identity", "construct every leaf with NewLeaf", nil)
		}
	}
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].path.String() < leaves[j].path.String() })
	for i := 1; i < len(leaves); i++ {
		if leaves[i-1].path == leaves[i].path {
			return Record{}, fault("record construction", "unique leaf paths", fmt.Sprintf("leaf %s is duplicated", leaves[i].path), "internal/install/registry.NewRecord", "canonicalizing file ownership", "duplicate ownership is ambiguous", "keep one ownership entry per path", nil)
		}
	}
	dirs := append([]artifact.Path(nil), in.CreatedDirs...)
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].String() < dirs[j].String() })
	for i := 1; i < len(dirs); i++ {
		if dirs[i-1] == dirs[i] {
			return Record{}, fault("record construction", "unique created directories", fmt.Sprintf("directory %s is duplicated", dirs[i]), "internal/install/registry.NewRecord", "canonicalizing directory ownership", "duplicate ownership is ambiguous", "keep one entry per directory", nil)
		}
	}
	config := append([]SharedConfigOwnership(nil), in.SharedConfig...)
	for _, ownership := range config {
		if !ownership.valid {
			return Record{}, fault("record construction", "validated shared config ownership", "a shared config entry is the invalid zero value", "internal/install/registry.NewRecord", "canonicalizing project config ownership", "a later merge cannot prove which entry Pasture owns", "construct every shared config entry with NewSharedConfigOwnership", nil)
		}
	}
	sort.Slice(config, func(i, j int) bool {
		if config[i].path == config[j].path {
			return config[i].identity.text < config[j].identity.text
		}
		return config[i].path.String() < config[j].path.String()
	})
	for i := 1; i < len(config); i++ {
		if config[i-1].path == config[i].path && config[i-1].identity == config[i].identity {
			return Record{}, fault("record construction", "unique shared config entries", fmt.Sprintf("shared config entry %s:%s is duplicated", config[i].path, config[i].identity), "internal/install/registry.NewRecord", "canonicalizing shared config ownership", "duplicate ownership is ambiguous", "keep one entry per path and identity", nil)
		}
	}
	return Record{key: in.Key, source: in.Source, strategy: in.Strategy, managed: in.Managed, artifactID: in.ArtifactID, version: in.Version, selector: in.Selector, leaves: leaves, createdDirs: dirs, sharedConfig: config, observation: in.Observation, trust: in.Trust, operation: in.LastOperation, outcome: in.LastOutcome, diagnostic: in.Diagnostic, valid: true}, nil
}
func (r Record) Key() Key                          { return r.key }
func (r Record) Cell() cell.Cell                   { return r.key.Cell() }
func (r Record) Source() Source                    { return r.source }
func (r Record) Strategy() activation.StrategyKind { return r.strategy }
func (r Record) Managed() bool                     { return r.managed }
func (r Record) ArtifactID() artifact.BundleID     { return r.artifactID }
func (r Record) Version() Version                  { return r.version }
func (r Record) Selector() Selector                { return r.selector }
func (r Record) Leaves() []Leaf                    { return append([]Leaf(nil), r.leaves...) }
func (r Record) CreatedDirs() []artifact.Path      { return append([]artifact.Path(nil), r.createdDirs...) }
func (r Record) SharedConfig() []SharedConfigOwnership {
	return append([]SharedConfigOwnership(nil), r.sharedConfig...)
}
func (r Record) Observation() Observation { return r.observation }
func (r Record) Trust() Trust             { return r.trust }
func (r Record) LastOperation() Operation { return r.operation }
func (r Record) LastOutcome() Outcome     { return r.outcome }
func (r Record) Diagnostic() string       { return r.diagnostic }
func (r Record) IsValid() bool            { return r.valid }
