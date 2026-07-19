package inventory

import (
	"bytes"
	"fmt"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/fsatomic"
	"gopkg.in/yaml.v3"
)

type leafWire struct {
	Path   string `yaml:"path"`
	Type   string `yaml:"type"`
	Mode   string `yaml:"mode"`
	Digest string `yaml:"digest"`
}

type recordWire struct {
	Cell        string     `yaml:"cell"`
	Source      string     `yaml:"source"`
	Strategy    string     `yaml:"strategy"`
	Managed     bool       `yaml:"managed"`
	ArtifactID  string     `yaml:"artifact_id,omitempty"`
	Version     string     `yaml:"version,omitempty"`
	Selector    string     `yaml:"selector,omitempty"`
	Leaves      []leafWire `yaml:"leaves,omitempty"`
	Observation string     `yaml:"observation"`
	Trust       string     `yaml:"trust"`
	LastAction  string     `yaml:"last_action,omitempty"`
	LastOutcome string     `yaml:"last_outcome,omitempty"`
	Diagnostic  string     `yaml:"diagnostic,omitempty"`
}

type stateWire struct {
	Schema string       `yaml:"schema"`
	Cells  []recordWire `yaml:"cells"`
}

// Marshal encodes the inventory in the frozen nine-cell order.
func (inv Inventory) Marshal() ([]byte, error) {
	wire := stateWire{Schema: SchemaID}
	for _, r := range inv.Ordered() {
		leaves := make([]leafWire, 0, len(r.leaves))
		for _, l := range r.leaves {
			leaves = append(leaves, leafWire{
				Path:   l.Path().String(),
				Type:   l.Type().String(),
				Mode:   l.Mode().String(),
				Digest: l.Digest().String(),
			})
		}
		wire.Cells = append(wire.Cells, recordWire{
			Cell:        r.cell.String(),
			Source:      r.source.String(),
			Strategy:    r.strategy.String(),
			Managed:     r.managed,
			ArtifactID:  r.artifactID,
			Version:     r.version,
			Selector:    r.selector,
			Leaves:      leaves,
			Observation: r.observation.String(),
			Trust:       r.trust.String(),
			LastAction:  r.lastAction,
			LastOutcome: r.lastOutcome,
			Diagnostic:  r.diagnostic,
		})
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(wire); err != nil {
		return nil, cell.NewFault(
			"inventory encode", "serializable state",
			fmt.Sprintf("the inventory could not be encoded: %v", err),
			"internal/install/inventory.Marshal", "encoding confirmed state",
			"confirmed state cannot be persisted",
			"report this as an internal encoder failure", err,
		)
	}
	_ = encoder.Close()
	return buf.Bytes(), nil
}

// Parse decodes and validates a confirmed-state document. Unknown keys,
// duplicate cells, and unknown enum values are rejected.
func Parse(data []byte) (Inventory, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var wire stateWire
	if err := decoder.Decode(&wire); err != nil {
		return Inventory{}, cell.NewFault(
			"inventory decode", "well-formed state document",
			fmt.Sprintf("the state document could not be decoded: %v", err),
			"internal/install/inventory.Parse", "decoding confirmed state",
			"prior confirmed state cannot be trusted",
			"repair the state file or delete it to start from an empty inventory", err,
		)
	}
	if wire.Schema != SchemaID {
		return Inventory{}, cell.NewFault(
			"inventory decode", "frozen schema identifier",
			fmt.Sprintf("schema is %q, not %q", wire.Schema, SchemaID),
			"internal/install/inventory.Parse", "decoding confirmed state",
			"a different schema version could carry incompatible record semantics",
			fmt.Sprintf("set schema to %q", SchemaID), nil,
		)
	}
	inv := New()
	for i, rw := range wire.Cells {
		record, err := decodeRecord(rw, i)
		if err != nil {
			return Inventory{}, err
		}
		if _, dup := inv.records[record.Cell().String()]; dup {
			return Inventory{}, cell.NewFault(
				"inventory decode", "one record per cell",
				fmt.Sprintf("cell %s appears more than once", record.Cell()),
				"internal/install/inventory.Parse", "decoding confirmed state",
				"a duplicate cell record makes the confirmed state ambiguous",
				fmt.Sprintf("keep exactly one record for %s", record.Cell()), nil,
			)
		}
		_ = inv.Upsert(record)
	}
	return inv, nil
}

func decodeRecord(rw recordWire, index int) (Record, error) {
	c, err := cell.ParseCell(rw.Cell)
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
	observation, err := ParseObservation(rw.Observation)
	if err != nil {
		return Record{}, err
	}
	trust, err := ParseTrust(rw.Trust)
	if err != nil {
		return Record{}, err
	}
	leaves := make([]Leaf, 0, len(rw.Leaves))
	for _, lw := range rw.Leaves {
		leaf, err := decodeLeaf(lw)
		if err != nil {
			return Record{}, err
		}
		leaves = append(leaves, leaf)
	}
	return NewRecord(RecordInput{
		Cell:        c,
		Source:      source,
		Strategy:    strategy,
		Managed:     rw.Managed,
		ArtifactID:  rw.ArtifactID,
		Version:     rw.Version,
		Selector:    rw.Selector,
		Leaves:      leaves,
		Observation: observation,
		Trust:       trust,
		LastAction:  rw.LastAction,
		LastOutcome: rw.LastOutcome,
		Diagnostic:  rw.Diagnostic,
	})
}

func decodeLeaf(lw leafWire) (Leaf, error) {
	path, err := artifact.NewPath(lw.Path)
	if err != nil {
		return Leaf{}, err
	}
	kind, err := artifact.ParseEntryType(lw.Type)
	if err != nil {
		return Leaf{}, err
	}
	mode, err := artifact.ParseMode(lw.Mode)
	if err != nil {
		return Leaf{}, err
	}
	digest, err := artifact.ParseDigest(lw.Digest)
	if err != nil {
		return Leaf{}, err
	}
	return NewLeaf(path, kind, mode, digest)
}

func writeState(path string, data []byte) error {
	return fsatomic.WriteFile(path, data, 0o600)
}
