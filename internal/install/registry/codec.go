package registry

import (
	"bytes"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"gopkg.in/yaml.v3"
)

type leafWire struct {
	Path   string `yaml:"path"`
	Type   string `yaml:"type"`
	Mode   string `yaml:"mode"`
	Digest string `yaml:"digest"`
}
type configWire struct {
	Path     string `yaml:"path"`
	Identity string `yaml:"identity"`
	Digest   string `yaml:"digest"`
}
type recordWire struct {
	Cell          string     `yaml:"cell"`
	Source        string     `yaml:"source"`
	Strategy      string     `yaml:"strategy"`
	Managed       bool       `yaml:"managed"`
	ArtifactID    string     `yaml:"artifact_id,omitempty"`
	Version       string     `yaml:"version,omitempty"`
	Selector      string     `yaml:"selector,omitempty"`
	Leaves        []leafWire `yaml:"leaves,omitempty"`
	CreatedDirs   []string   `yaml:"created_dirs,omitempty"`
	Observation   string     `yaml:"observation"`
	Trust         string     `yaml:"trust"`
	LastOperation *string    `yaml:"last_operation"`
	LastOutcome   *string    `yaml:"last_outcome"`
	Diagnostic    string     `yaml:"diagnostic,omitempty"`
}
type projectRecordWire struct {
	CanonicalProjectRoot string       `yaml:"canonical_project_root"`
	SharedConfig         []configWire `yaml:"shared_config_ownership,omitempty"`
	recordWire           `yaml:",inline"`
}
type documentWire struct {
	Schema   string              `yaml:"schema"`
	Global   []recordWire        `yaml:"global_installations"`
	Projects []projectRecordWire `yaml:"project_installations"`
}

// Marshal emits global_installations first, then project_installations ordered
// by canonical root and cell. Nested ownership sets are canonicalized by the
// Record constructor.
func (s Store) Marshal() ([]byte, error) {
	w := documentWire{Schema: SchemaID, Global: []recordWire{}, Projects: []projectRecordWire{}}
	for _, r := range s.Ordered() {
		rw := encodeRecord(r)
		if r.Key().Scope() == ScopeGlobal {
			w.Global = append(w.Global, rw)
		} else {
			w.Projects = append(w.Projects, projectRecordWire{CanonicalProjectRoot: r.Key().ProjectRoot().String(), SharedConfig: encodeConfig(r.SharedConfig()), recordWire: rw})
		}
	}
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(w); err != nil {
		return nil, fault("registry encode", "serializable v1 registry", fmt.Sprintf("encoding failed: %v", err), "internal/install/registry.Store.Marshal", "encoding registry state", "the registry cannot be persisted", "report the invalid in-memory record", err)
	}
	if err := e.Close(); err != nil {
		return nil, fault("registry encode", "closed YAML encoder", fmt.Sprintf("closing encoder failed: %v", err), "internal/install/registry.Store.Marshal", "finishing registry encoding", "the registry cannot be persisted", "retry and report a repeatable encoder failure", err)
	}
	return b.Bytes(), nil
}
func encodeRecord(r Record) recordWire {
	operation := r.LastOperation().String()
	outcome := r.LastOutcome().String()
	rw := recordWire{Cell: r.Cell().String(), Source: r.Source().String(), Strategy: r.Strategy().String(), Managed: r.Managed(), ArtifactID: r.ArtifactID(), Version: r.Version(), Selector: r.Selector(), Observation: r.Observation().String(), Trust: r.Trust().String(), LastOperation: &operation, LastOutcome: &outcome, Diagnostic: r.Diagnostic()}
	for _, l := range r.Leaves() {
		rw.Leaves = append(rw.Leaves, leafWire{l.Path().String(), l.Type().String(), l.Mode().String(), l.Digest().String()})
	}
	for _, d := range r.CreatedDirs() {
		rw.CreatedDirs = append(rw.CreatedDirs, d.String())
	}
	return rw
}
func encodeConfig(in []SharedConfigOwnership) []configWire {
	out := make([]configWire, 0, len(in))
	for _, o := range in {
		out = append(out, configWire{o.Path().String(), o.Identity(), o.Digest().String()})
	}
	return out
}

// Parse strictly decodes only v1. Unknown fields, duplicate mapping keys,
// duplicate logical keys, trailing documents, invalid enums, and malformed
// ownership values are rejected.
func Parse(data []byte) (Store, error) {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	var w documentWire
	if err := d.Decode(&w); err != nil {
		return Store{}, fault("registry decode", "well-formed closed v1 document", fmt.Sprintf("decoding failed: %v", err), "internal/install/registry.Parse", "decoding external registry input", "persisted installation ownership cannot be trusted", "repair or remove the registry file, then retry", err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return Store{}, fault("registry decode", "exactly one YAML document", "trailing YAML content or another document is present", "internal/install/registry.Parse", "checking the end of registry input", "an appended document could hide conflicting ownership", "keep exactly one registry document", err)
	}
	if w.Schema != SchemaID {
		return Store{}, fault("registry decode", "exact v1 schema", fmt.Sprintf("schema %q is not %q", w.Schema, SchemaID), "internal/install/registry.Parse", "validating registry schema", "another schema may have incompatible ownership semantics", fmt.Sprintf("use schema %q; no migration is available", SchemaID), nil)
	}
	s := New()
	for i, rw := range w.Global {
		r, err := decodeRecord(rw, ScopeGlobal, ProjectRoot{}, nil)
		if err != nil {
			return Store{}, fault("registry decode", "valid global row", fmt.Sprintf("global_installations[%d] is invalid: %v", i, err), "internal/install/registry.Parse", "decoding the global logical table", "the registry cannot be trusted", "repair the named global row", err)
		}
		if _, ok := s.Lookup(r.Key()); ok {
			return Store{}, duplicateKey(r.Key())
		}
		_ = s.Upsert(r)
	}
	for i, pw := range w.Projects {
		root, err := parseStoredProjectRoot(pw.CanonicalProjectRoot)
		if err != nil {
			return Store{}, err
		}
		r, err := decodeRecord(pw.recordWire, ScopeProject, root, pw.SharedConfig)
		if err != nil {
			return Store{}, fault("registry decode", "valid project row", fmt.Sprintf("project_installations[%d] is invalid: %v", i, err), "internal/install/registry.Parse", "decoding the project logical table", "the registry cannot be trusted", "repair the named project row", err)
		}
		if _, ok := s.Lookup(r.Key()); ok {
			return Store{}, duplicateKey(r.Key())
		}
		_ = s.Upsert(r)
	}
	return s, nil
}
func duplicateKey(k Key) error {
	return fault("registry decode", "unique scoped keys", fmt.Sprintf("key %s/%s/%s appears more than once", k.Scope(), k.ProjectRoot(), k.Cell()), "internal/install/registry.Parse", "building logical tables", "duplicate ownership is ambiguous", "keep exactly one row for the scoped key", nil)
}
func decodeRecord(rw recordWire, scope Scope, root ProjectRoot, cw []configWire) (Record, error) {
	c, err := cell.ParseCell(rw.Cell)
	if err != nil {
		return Record{}, err
	}
	var k Key
	if scope == ScopeGlobal {
		k, err = GlobalKey(c)
	} else {
		k, err = ProjectKey(root, c)
	}
	if err != nil {
		return Record{}, err
	}
	source, err := ParseSource(rw.Source)
	if err != nil {
		return Record{}, err
	}
	strategy, err := activation.ParseStrategyKind(rw.Strategy)
	if err != nil {
		return Record{}, err
	}
	obs, err := ParseObservation(rw.Observation)
	if err != nil {
		return Record{}, err
	}
	trust, err := ParseTrust(rw.Trust)
	if err != nil {
		return Record{}, err
	}
	if rw.LastOperation == nil || rw.LastOutcome == nil {
		return Record{}, fault("registry record decode", "explicit last_operation and last_outcome", "last_operation or last_outcome is absent or null", "internal/install/registry.decodeRecord", "decoding a registry record", "the record cannot explain its last confirmed result", "set last_operation and last_outcome explicitly, using none when there is no prior action", nil)
	}
	op, err := ParseOperation(*rw.LastOperation)
	if err != nil {
		return Record{}, err
	}
	outcome, err := ParseOutcome(*rw.LastOutcome)
	if err != nil {
		return Record{}, err
	}
	leaves := make([]Leaf, 0, len(rw.Leaves))
	for _, lw := range rw.Leaves {
		p, e := artifact.NewPath(lw.Path)
		if e != nil {
			return Record{}, e
		}
		kind, e := artifact.ParseEntryType(lw.Type)
		if e != nil {
			return Record{}, e
		}
		mode, e := artifact.ParseMode(lw.Mode)
		if e != nil {
			return Record{}, e
		}
		digest, e := artifact.ParseDigest(lw.Digest)
		if e != nil {
			return Record{}, e
		}
		l, e := NewLeaf(p, kind, mode, digest)
		if e != nil {
			return Record{}, e
		}
		leaves = append(leaves, l)
	}
	dirs := make([]artifact.Path, 0, len(rw.CreatedDirs))
	for _, v := range rw.CreatedDirs {
		p, e := artifact.NewPath(v)
		if e != nil {
			return Record{}, e
		}
		dirs = append(dirs, p)
	}
	config := make([]SharedConfigOwnership, 0, len(cw))
	for _, v := range cw {
		p, e := artifact.NewPath(v.Path)
		if e != nil {
			return Record{}, e
		}
		digest, e := artifact.ParseDigest(v.Digest)
		if e != nil {
			return Record{}, e
		}
		o, e := NewSharedConfigOwnership(p, v.Identity, digest)
		if e != nil {
			return Record{}, e
		}
		config = append(config, o)
	}
	return NewRecord(RecordInput{Key: k, Source: source, Strategy: strategy, Managed: rw.Managed, ArtifactID: rw.ArtifactID, Version: rw.Version, Selector: rw.Selector, Leaves: leaves, CreatedDirs: dirs, SharedConfig: config, Observation: obs, Trust: trust, LastOperation: op, LastOutcome: outcome, Diagnostic: rw.Diagnostic})
}
