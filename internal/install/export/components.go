package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/dayvidpham/pasture/artifact"
)

// ComponentSetSchema is the schema string of the component-set document the
// aggregate release producer consumes.
const ComponentSetSchema = "aura.aggregate-components/v1"

// ComponentSetFilename is the basename of the emitted component-set document.
const ComponentSetFilename = "components.json"

// componentSetDocument mirrors the exact strict document the aggregate release
// producer decodes: a schema string and one record per canonical component.
// The producer rejects unknown, duplicated, or case-folded fields, so this type
// must stay exactly these four per-record keys.
type componentSetDocument struct {
	Schema     string                  `json:"schema"`
	Components []componentSetRecordDoc `json:"components"`
}

type componentSetRecordDoc struct {
	ID       string `json:"id"`
	Artifact string `json:"artifact"`
	Asset    string `json:"asset"`
	BundleID string `json:"bundle_id"`
}

// AssetBasename returns the canonical immutable component asset basename for
// one release version and one component coordinate. Aggregate release
// validation accepts exactly this spelling and nothing else.
func AssetBasename(version artifact.Version, id artifact.ComponentID) (string, error) {
	if version.String() == "" {
		return "", archiveFault(
			"component asset naming", "a parsed release version", "the version was not constructed",
			"the asset could not be tied to one immutable release",
			"parse the version with artifact.ParseVersion before exporting", fs.ErrInvalid)
	}
	if !id.IsValid() {
		return "", archiveFault(
			"component asset naming", "a canonical component coordinate", "the component coordinate is zero or unsupported",
			"the asset could not be tied to one installation cell",
			"use a coordinate returned by artifact.ComponentIDs", fs.ErrInvalid)
	}
	return fmt.Sprintf("pasture-%s-%s-%s.tgz", version, assetStem(id.Harness()), id.Extension()), nil
}

// assetStem is the harness word used in asset basenames; Claude Code is spelled
// "claude" there while its component identity remains "claude-code".
func assetStem(harness artifact.Harness) string {
	if harness == artifact.HarnessClaudeCode {
		return "claude"
	}
	return string(harness)
}

// MarshalComponentSet encodes the component-set document for the given cell
// results, in canonical component order, with a trailing newline.
func MarshalComponentSet(cells []CellResult) ([]byte, error) {
	document := componentSetDocument{Schema: ComponentSetSchema, Components: make([]componentSetRecordDoc, 0, len(cells))}
	for _, result := range cells {
		if !result.ID.IsValid() {
			return nil, archiveFault(
				"component set encoding", "every record names a canonical component",
				"a cell result has a zero or unsupported component coordinate",
				"the aggregate producer could not address the component",
				"export through Export, which iterates artifact.ComponentIDs", fs.ErrInvalid)
		}
		document.Components = append(document.Components, componentSetRecordDoc{
			ID:       result.ID.String(),
			Artifact: result.Asset,
			Asset:    result.Asset,
			BundleID: result.BundleID.String(),
		})
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, archiveFault(
			"component set encoding", "the component set encodes as JSON",
			fmt.Sprintf("the document could not be encoded: %v", err),
			"the aggregate producer has no input document",
			"report this as a Pasture defect; the document contains only validated strings", err)
	}
	var buffer bytes.Buffer
	buffer.Write(encoded)
	buffer.WriteByte('\n')
	return buffer.Bytes(), nil
}
