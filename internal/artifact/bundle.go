// Package artifact defines the neutral, opaque bundle value a native target
// publishes so a packaged CLI can materialize a target's generated components
// without the source checkout.
//
// A Bundle carries the exact bytes of every generated component (embedded in the
// binary at build time via go:embed, or assembled in memory) keyed by a clean,
// canonical relative path. Its Manifest freezes, per entry, the component type,
// the octal file mode, and a sha256:<64 lowercase hex> exact-byte content
// digest. Manifest entries are sorted lexicographically by relative path, so a
// manifest is a deterministic, order-stable oracle for the bundle's contents.
//
// The package is target-neutral: it names no harness and encodes no OpenCode,
// Claude Code, or Codex specifics. A target descriptor builds a Bundle from its
// own generated components; downstream installation (issue #39) consumes the
// opaque value and its manifest and never reaches inside it.
//
// # Delivered-surface divergence
//
// Issue #27 lists issue #48's neutral artifact bundle package as a dependency.
// That package was not present in this slice's base commit, so this file
// delivers the minimal consumed contract the OpenCode target requires: an
// opaque, digest-manifested, go:embed-friendly Bundle value. When #48 lands, its
// richer catalog can absorb or replace this package; the OpenCode target
// descriptor depends only on the small surface documented here.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// EntryType is the closed classification of a bundle component. It is a
// strongly-typed enum rather than a free string so a manifest can never carry an
// out-of-vocabulary component kind, and so consumers switch exhaustively over a
// fixed set.
type EntryType string

const (
	// EntryTypeSkill is a whole-skill-directory file (for example a SKILL.md or a
	// skill's supporting asset) preserved verbatim for the target.
	EntryTypeSkill EntryType = "skill"
	// EntryTypeAgent is a standalone agent role file.
	EntryTypeAgent EntryType = "agent"
	// EntryTypeHook is an embedded server-hook module file.
	EntryTypeHook EntryType = "hook"
	// EntryTypeManifest is a target configuration or manifest file (for example a
	// host discovery config) that is neither a skill, agent, nor hook.
	EntryTypeManifest EntryType = "manifest"
)

// IsValid reports whether t is one of the closed EntryType members.
func (t EntryType) IsValid() bool {
	switch t {
	case EntryTypeSkill, EntryTypeAgent, EntryTypeHook, EntryTypeManifest:
		return true
	default:
		return false
	}
}

// digestPrefix is the required prefix of every content digest. A digest is
// exactly this prefix followed by 64 lowercase hexadecimal characters.
const digestPrefix = "sha256:"

// digestOf returns the canonical sha256:<64 lowercase hex> digest of content.
func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return digestPrefix + hex.EncodeToString(sum[:])
}

// ManifestEntry is one frozen record in a bundle manifest: the component's clean
// relative path, its type, its octal file mode, and its exact-byte content
// digest. A ManifestEntry carries no content; it is the stable oracle a consumer
// checks a materialized tree against.
type ManifestEntry struct {
	Path   string      `json:"path"`
	Type   EntryType   `json:"type"`
	Mode   fs.FileMode `json:"mode"`
	Digest string      `json:"digest"`
}

// Manifest is the deterministic, lexicographically-sorted list of a bundle's
// entries. Two bundles with identical components produce byte-identical
// manifests regardless of the order their sources were supplied.
type Manifest struct {
	Entries []ManifestEntry `json:"entries"`
}

// Digest returns a single sha256:<hex> digest over the whole manifest, computed
// from the canonical newline-joined "path type mode digest" serialization of the
// already-sorted entries. It is a compact identity for the entire bundle.
func (m Manifest) Digest() string {
	var b strings.Builder
	for _, e := range m.Entries {
		fmt.Fprintf(&b, "%s %s %o %s\n", e.Path, e.Type, e.Mode.Perm(), e.Digest)
	}
	return digestOf([]byte(b.String()))
}

// Source is one input component supplied to NewBundle. Mode's permission bits
// are retained; any non-permission bits are ignored so a manifest freezes a
// clean octal file mode.
type Source struct {
	Path    string
	Type    EntryType
	Mode    fs.FileMode
	Content []byte
}

// Bundle is the opaque, immutable value a target publishes. It owns the exact
// bytes of every component and a frozen manifest. The only way to obtain a
// non-zero Bundle is NewBundle (or FromFS), so a Bundle in hand has already
// passed path, type, and uniqueness validation.
type Bundle struct {
	id          string
	content     map[string][]byte
	manifest    Manifest
	constructed bool
}

// NewBundle validates sources and returns an immutable Bundle. id must be
// non-empty and identifies the bundle's producing target. Every source path must
// be a clean relative path (no leading slash, no "." or ".." segment, no empty
// segment); every type must be a valid EntryType; paths must be unique. The
// returned manifest is sorted lexicographically by path.
func NewBundle(id string, sources []Source) (Bundle, error) {
	const where = "artifact.NewBundle"
	if strings.TrimSpace(id) == "" {
		return Bundle{}, fmt.Errorf(
			"%s: bundle id is empty — a bundle must name its producing target so downstream installation can attribute its components; "+
				"pass a non-empty id such as the target's RuntimeContractID string", where)
	}
	content := make(map[string][]byte, len(sources))
	entries := make([]ManifestEntry, 0, len(sources))
	for index, src := range sources {
		if err := validateRelPath(src.Path, where, index); err != nil {
			return Bundle{}, err
		}
		if !src.Type.IsValid() {
			return Bundle{}, fmt.Errorf(
				"%s: source %d (path %q) has invalid entry type %q — every bundle component must carry a closed EntryType so the manifest never freezes an out-of-vocabulary kind; "+
					"use one of EntryTypeSkill, EntryTypeAgent, EntryTypeHook, or EntryTypeManifest",
				where, index, src.Path, src.Type)
		}
		if _, duplicate := content[src.Path]; duplicate {
			return Bundle{}, fmt.Errorf(
				"%s: source %d duplicates relative path %q — a bundle path is a unique key so a materialized tree is unambiguous; "+
					"supply each component path exactly once",
				where, index, src.Path)
		}
		// Copy the bytes so a later mutation of the caller's slice cannot alter the
		// frozen digest or content.
		buf := append([]byte(nil), src.Content...)
		content[src.Path] = buf
		entries = append(entries, ManifestEntry{
			Path:   src.Path,
			Type:   src.Type,
			Mode:   src.Mode.Perm(),
			Digest: digestOf(buf),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return Bundle{
		id:          id,
		content:     content,
		manifest:    Manifest{Entries: entries},
		constructed: true,
	}, nil
}

// validateRelPath rejects absolute paths, "." and ".." traversal, empty
// segments, and any path that path.Clean would rewrite (a non-canonical path).
func validateRelPath(p string, where string, index int) error {
	if p == "" {
		return fmt.Errorf(
			"%s: source %d has an empty relative path — every component needs a clean relative path so it materializes at a determinate location; "+
				"supply a path like \"skills/worker/SKILL.md\"",
			where, index)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf(
			"%s: source %d path %q is absolute — bundle paths must be relative so a packaged CLI materializes them under a chosen install root; "+
				"strip the leading slash",
			where, index, p)
	}
	if p != path.Clean(p) {
		return fmt.Errorf(
			"%s: source %d path %q is not canonical (path.Clean yields %q) — bundle paths must be clean so the manifest is a stable oracle; "+
				"supply the cleaned relative path with no \".\", \"..\", trailing-slash, or doubled-slash segments",
			where, index, p, path.Clean(p))
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf(
				"%s: source %d path %q contains an empty or traversal segment %q — bundle paths may not escape the install root; "+
					"remove the offending segment",
				where, index, p, seg)
		}
	}
	return nil
}

// FromFS builds a Bundle from every regular file in fsys (an embed.FS or any
// fs.FS), classifying each file's EntryType and mode with classify. id names the
// producing target. It is the go:embed-backed constructor: a target embeds its
// generated tree and publishes it as an opaque Bundle with no source checkout.
func FromFS(id string, fsys fs.FS, classify func(relPath string) (EntryType, fs.FileMode, error)) (Bundle, error) {
	const where = "artifact.FromFS"
	if classify == nil {
		return Bundle{}, fmt.Errorf(
			"%s: classify function is nil — FromFS needs a classifier to assign each embedded file a component type and mode; "+
				"pass a non-nil func(relPath) (EntryType, fs.FileMode, error)", where)
	}
	var sources []Source
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("%s: walk embedded tree at %q failed — the embedded FS is unreadable, which is a build-time embed defect: %w", where, p, err)
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return fmt.Errorf("%s: read embedded file %q failed — the embedded FS is inconsistent, which is a build-time embed defect: %w", where, p, readErr)
		}
		typ, mode, classifyErr := classify(p)
		if classifyErr != nil {
			return fmt.Errorf("%s: classify embedded file %q failed: %w", where, p, classifyErr)
		}
		sources = append(sources, Source{Path: p, Type: typ, Mode: mode, Content: data})
		return nil
	})
	if err != nil {
		return Bundle{}, err
	}
	return NewBundle(id, sources)
}

// ID returns the bundle's producing-target identity.
func (b Bundle) ID() string { return b.id }

// IsValid reports whether the bundle was produced by a constructor.
func (b Bundle) IsValid() bool { return b.constructed }

// Manifest returns the frozen, lexicographically-sorted manifest. The returned
// value shares no mutable state with the bundle: its slice is a copy.
func (b Bundle) Manifest() Manifest {
	out := make([]ManifestEntry, len(b.manifest.Entries))
	copy(out, b.manifest.Entries)
	return Manifest{Entries: out}
}

// Paths returns every component path in lexicographic order.
func (b Bundle) Paths() []string {
	out := make([]string, len(b.manifest.Entries))
	for i, e := range b.manifest.Entries {
		out[i] = e.Path
	}
	return out
}

// Content returns a copy of the exact bytes stored at relPath. The second result
// is false when the bundle has no such component.
func (b Bundle) Content(relPath string) ([]byte, bool) {
	data, ok := b.content[relPath]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), data...), true
}
