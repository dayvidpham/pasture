package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const manifestSchema = "artifact.manifest.v1"

const (
	regularFileTypeName = "regular-file"
	directoryTypeName   = "directory"
)

// EntryType is one of the closed artifact entry variants.
type EntryType struct{ name string }

// RegularFileType returns the regular-file entry variant.
func RegularFileType() EntryType { return EntryType{name: regularFileTypeName} }

// DirectoryType returns the directory entry variant.
func DirectoryType() EntryType { return EntryType{name: directoryTypeName} }

// ParseEntryType decodes a closed artifact entry variant.
func ParseEntryType(value string) (EntryType, error) {
	switch value {
	case regularFileTypeName:
		return RegularFileType(), nil
	case directoryTypeName:
		return DirectoryType(), nil
	default:
		return EntryType{}, invalid(
			"entry type decoding", "", "", "supported entry type",
			fmt.Sprintf("entry type %q is not %q or %q", value, regularFileTypeName, directoryTypeName),
			"the bundle cannot determine how to validate or expose the entry",
			fmt.Sprintf("use exactly %q for a file or %q for a directory", regularFileTypeName, directoryTypeName), fs.ErrInvalid,
		)
	}
}

func (t EntryType) String() string { return t.name }

func (t EntryType) MarshalText() ([]byte, error) {
	if _, err := ParseEntryType(t.name); err != nil {
		return nil, err
	}
	return []byte(t.name), nil
}

func (t *EntryType) UnmarshalText(text []byte) error {
	if t == nil {
		return invalid(
			"entry type decoding", "", "", "non-nil decode target",
			"the destination EntryType pointer is nil", "the decoded type cannot be returned",
			"decode into a non-nil *artifact.EntryType", nil,
		)
	}
	parsed, err := ParseEntryType(string(text))
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// Entry is one immutable manifest entry.
type Entry struct {
	path   Path
	kind   EntryType
	mode   Mode
	digest Digest
	valid  bool
}

// NewFileEntry constructs a validated regular-file manifest entry.
func NewFileEntry(entryPath Path, mode Mode, digest Digest) (Entry, error) {
	if err := validatePathMode(entryPath, mode, "regular-file entry construction"); err != nil {
		return Entry{}, err
	}
	if _, err := ParseDigest(digest.value); err != nil {
		return Entry{}, invalid(
			"regular-file entry construction", "", entryPath.String(), "validated content digest",
			"the file digest was not constructed or is malformed", "the file bytes cannot be verified",
			"construct the digest with artifact.DigestBytes or artifact.ParseDigest", err,
		)
	}
	return Entry{path: entryPath, kind: RegularFileType(), mode: mode, digest: digest, valid: true}, nil
}

// NewDirectoryEntry constructs a validated directory manifest entry.
func NewDirectoryEntry(entryPath Path, mode Mode) (Entry, error) {
	if err := validatePathMode(entryPath, mode, "directory entry construction"); err != nil {
		return Entry{}, err
	}
	return Entry{path: entryPath, kind: DirectoryType(), mode: mode, valid: true}, nil
}

func validatePathMode(entryPath Path, mode Mode, stage string) error {
	if _, err := NewPath(entryPath.value); err != nil {
		return invalid(
			stage, "", entryPath.String(), "validated entry path",
			"the path was not constructed or is malformed", "the entry cannot be placed safely",
			"construct the path with artifact.NewPath", err,
		)
	}
	if !mode.valid {
		return invalid(
			stage, "", entryPath.String(), "validated permission mode",
			"the mode was not constructed", "the entry permissions are unknown",
			"construct the mode with artifact.NewMode or artifact.ParseMode", fs.ErrInvalid,
		)
	}
	return nil
}

func (e Entry) Path() Path        { return e.path }
func (e Entry) Type() EntryType   { return e.kind }
func (e Entry) Mode() Mode        { return e.mode }
func (e Entry) Digest() Digest    { return e.digest }
func (e Entry) IsRegular() bool   { return e.valid && e.kind == RegularFileType() }
func (e Entry) IsDirectory() bool { return e.valid && e.kind == DirectoryType() }

// Manifest is an immutable, lexicographically ordered artifact inventory.
type Manifest struct {
	entries []Entry
	valid   bool
}

// NewManifest validates ownership collisions and snapshots entries in
// lexicographic path order.
func NewManifest(entries ...Entry) (Manifest, error) {
	owned := append([]Entry(nil), entries...)
	for _, entry := range owned {
		if !entry.valid {
			return Manifest{}, invalid(
				"manifest construction", "", entry.path.String(), "validated entry",
				"an entry was not constructed by an artifact entry constructor",
				"the manifest cannot establish entry type, path, mode, or digest invariants",
				"construct every item with artifact.NewFileEntry or artifact.NewDirectoryEntry", fs.ErrInvalid,
			)
		}
	}
	sort.Slice(owned, func(i, j int) bool {
		return owned[i].path.value < owned[j].path.value
	})

	byPath := make(map[string]Entry, len(owned))
	for _, entry := range owned {
		name := entry.path.value
		if _, exists := byPath[name]; exists {
			return Manifest{}, invalid(
				"manifest construction", "", name, "unique entry path",
				fmt.Sprintf("path %q is declared more than once", name),
				"two manifest owners would compete for the same filesystem location",
				"remove the duplicate so each path has exactly one entry", fs.ErrExist,
			)
		}
		byPath[name] = entry
	}

	for _, entry := range owned {
		for parent := path.Dir(entry.path.value); parent != "."; parent = path.Dir(parent) {
			if owner, exists := byPath[parent]; exists && owner.IsRegular() {
				return Manifest{}, invalid(
					"manifest construction", "", entry.path.value, "no parent-file collision",
					fmt.Sprintf("regular file %q is also the parent of %q", parent, entry.path.value),
					"the file and its descendant cannot both own the same filesystem tree",
					fmt.Sprintf("remove %q or move %q outside that file path", parent, entry.path.value), fs.ErrExist,
				)
			}
		}
	}

	return Manifest{entries: owned, valid: true}, nil
}

// Len returns the number of explicit manifest entries.
func (m Manifest) Len() int { return len(m.entries) }

// Entries returns a copy in canonical lexicographic order.
func (m Manifest) Entries() []Entry { return append([]Entry(nil), m.entries...) }

// Equal reports semantic equality of two validated manifests.
func (m Manifest) Equal(other Manifest) bool {
	if !m.valid || !other.valid || len(m.entries) != len(other.entries) {
		return false
	}
	for i := range m.entries {
		if m.entries[i] != other.entries[i] {
			return false
		}
	}
	return true
}

type manifestWire struct {
	Schema  string      `json:"schema"`
	Entries []entryWire `json:"entries"`
}

type manifestDecodeWire struct {
	Schema  string       `json:"schema"`
	Entries *[]entryWire `json:"entries"`
}

type entryWire struct {
	Path   string  `json:"path"`
	Type   string  `json:"type"`
	Mode   string  `json:"mode"`
	Digest *string `json:"digest,omitempty"`
}

// MarshalJSON emits the canonical manifest codec.
func (m Manifest) MarshalJSON() ([]byte, error) {
	if !m.valid {
		return nil, invalid(
			"manifest encoding", "", "", "validated manifest",
			"the zero Manifest value was not constructed", "an invalid manifest cannot be serialized",
			"construct the manifest with artifact.NewManifest or artifact.ParseManifest", fs.ErrInvalid,
		)
	}
	wire := manifestWire{Schema: manifestSchema, Entries: make([]entryWire, 0, len(m.entries))}
	for _, entry := range m.entries {
		item := entryWire{
			Path: entry.path.String(),
			Type: entry.kind.String(),
			Mode: entry.mode.String(),
		}
		if entry.IsRegular() {
			digest := entry.digest.String()
			item.Digest = &digest
		}
		wire.Entries = append(wire.Entries, item)
	}
	return json.Marshal(wire)
}

// MarshalBinary emits the same canonical bytes as MarshalJSON.
func (m Manifest) MarshalBinary() ([]byte, error) { return m.MarshalJSON() }

// ParseManifest decodes and validates a manifest. Entry order in the input is
// canonicalized by NewManifest before the value can be observed.
func ParseManifest(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire manifestDecodeWire
	if err := decoder.Decode(&wire); err != nil {
		return Manifest{}, invalid(
			"manifest decoding", "", "", "valid manifest JSON",
			fmt.Sprintf("the manifest JSON could not be decoded: %v", err),
			"the bundle inventory is unavailable",
			"provide one JSON object using only schema and entries fields", err,
		)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if wire.Schema != manifestSchema {
		return Manifest{}, invalid(
			"manifest decoding", "", "", "supported manifest schema",
			fmt.Sprintf("schema %q is not %q", wire.Schema, manifestSchema),
			"the manifest may have incompatible identity or validation semantics",
			fmt.Sprintf("encode the manifest with schema %q", manifestSchema), fs.ErrInvalid,
		)
	}
	if wire.Entries == nil {
		return Manifest{}, invalid(
			"manifest decoding", "", "", "entries array present",
			"the entries field is missing or null", "the artifact inventory is ambiguous",
			"provide an entries JSON array, using [] for an empty manifest", fs.ErrInvalid,
		)
	}

	entries := make([]Entry, 0, len(*wire.Entries))
	for index, item := range *wire.Entries {
		entryPath, err := NewPath(item.Path)
		if err != nil {
			return Manifest{}, annotateWireEntryError(err, index, item.Path)
		}
		kind, err := ParseEntryType(item.Type)
		if err != nil {
			return Manifest{}, annotateWireEntryError(err, index, item.Path)
		}
		mode, err := ParseMode(item.Mode)
		if err != nil {
			return Manifest{}, annotateWireEntryError(err, index, item.Path)
		}

		var entry Entry
		switch kind {
		case RegularFileType():
			if item.Digest == nil {
				return Manifest{}, invalid(
					"manifest decoding", "", item.Path, "regular-file digest present",
					fmt.Sprintf("entry index %d is a regular file without a digest", index),
					"the file bytes cannot be verified",
					"add a digest in sha256:<64 lowercase hex> form", fs.ErrInvalid,
				)
			}
			digest, digestErr := ParseDigest(*item.Digest)
			if digestErr != nil {
				return Manifest{}, annotateWireEntryError(digestErr, index, item.Path)
			}
			entry, err = NewFileEntry(entryPath, mode, digest)
		case DirectoryType():
			if item.Digest != nil {
				return Manifest{}, invalid(
					"manifest decoding", "", item.Path, "directory has no content digest",
					fmt.Sprintf("entry index %d is a directory with digest %q", index, *item.Digest),
					"directory identity would have two competing representations",
					"remove the digest field from directory entries", fs.ErrInvalid,
				)
			}
			entry, err = NewDirectoryEntry(entryPath, mode)
		}
		if err != nil {
			return Manifest{}, annotateWireEntryError(err, index, item.Path)
		}
		entries = append(entries, entry)
	}
	return NewManifest(entries...)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("unexpected JSON value after manifest")
	}
	return invalid(
		"manifest decoding", "", "", "single manifest JSON value",
		fmt.Sprintf("data remains after the manifest object: %v", err),
		"multiple or trailing values make the inventory ambiguous",
		"remove all content after the one manifest JSON object", err,
	)
}

func annotateWireEntryError(err error, index int, entryPath string) error {
	var validation *ValidationError
	if !errors.As(err, &validation) {
		return err
	}
	copy := *validation
	copy.Stage = "manifest decoding"
	copy.Entry = entryPath
	copy.Reason = fmt.Sprintf("entry index %d failed: %s", index, validation.Reason)
	copy.Cause = err
	return &copy
}

// UnmarshalJSON replaces m only after the complete input validates.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	if m == nil {
		return invalid(
			"manifest decoding", "", "", "non-nil decode target",
			"the destination Manifest pointer is nil", "the decoded manifest cannot be returned",
			"decode into a non-nil *artifact.Manifest", nil,
		)
	}
	parsed, err := ParseManifest(data)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// UnmarshalBinary replaces m only after the complete input validates.
func (m *Manifest) UnmarshalBinary(data []byte) error { return m.UnmarshalJSON(data) }

func (m Manifest) clone() Manifest {
	return Manifest{entries: append([]Entry(nil), m.entries...), valid: m.valid}
}

func (m Manifest) entryMap() map[string]Entry {
	entries := make(map[string]Entry, len(m.entries))
	for _, entry := range m.entries {
		entries[entry.path.value] = entry
	}
	return entries
}

func (m Manifest) hasPathOrDescendant(name string) bool {
	for _, entry := range m.entries {
		if entry.path.value == name || strings.HasPrefix(entry.path.value, name+"/") {
			return true
		}
	}
	return false
}
