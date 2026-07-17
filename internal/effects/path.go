package effects

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// pathGlobMetacharacters are the glob operators an exact owned path must never
// contain. Rejecting them is what prevents a filesystem effect from ever naming
// a set of files (for example a wildcard removal) instead of one exact path.
const pathGlobMetacharacters = "*?[]"

// OwnedPath is an exact, normalized, slash-separated relative path an effect is
// permitted to name. It is opaque and constructor-owned. It cannot be a glob,
// cannot escape its root with "..", and cannot be absolute — so a filesystem
// effect always names exactly one path it owns and can never expand to an
// unowned set of files.
type OwnedPath struct {
	value       string
	constructed bool
}

// NewOwnedPath validates and normalizes an exact relative path.
func NewOwnedPath(raw string) (OwnedPath, error) {
	if !utf8.ValidString(raw) {
		return OwnedPath{}, effectError(
			"owned path is not valid UTF-8",
			"a portable path must survive exact JSON and filesystem operations",
			"NewOwnedPath", "path validation",
			"the filesystem effect cannot be constructed",
			"supply a valid UTF-8 relative path", nil,
		)
	}
	if raw == "" || strings.TrimSpace(raw) != raw {
		return OwnedPath{}, effectError(
			"owned path is empty or padded",
			"a filesystem effect must name exactly one path with one exact spelling",
			"NewOwnedPath", "path validation",
			"the target path is ambiguous",
			"supply a non-empty relative path without surrounding whitespace", nil,
		)
	}
	if r, ok := containsControl(raw); ok {
		return OwnedPath{}, effectError(
			fmt.Sprintf("owned path contains control character U+%04X", r),
			"control characters are unsafe in a portable path",
			"NewOwnedPath", "path validation",
			"the target path cannot be represented safely",
			"remove control characters from the path", nil,
		)
	}
	if strings.HasPrefix(raw, "/") {
		return OwnedPath{}, effectError(
			fmt.Sprintf("owned path %q is absolute", raw),
			"an owned effect path is resolved relative to a publisher-supplied root, never an absolute filesystem location",
			"NewOwnedPath", "path validation",
			"the effect could escape its owned root",
			"supply a path relative to the effect root", nil,
		)
	}
	if i := strings.IndexAny(raw, pathGlobMetacharacters); i >= 0 {
		return OwnedPath{}, effectError(
			fmt.Sprintf("owned path %q contains glob metacharacter %q", raw, string(raw[i])),
			"a filesystem effect names exactly one owned path; a glob could match and mutate unowned files",
			"NewOwnedPath", "path validation",
			"the effect could delete or overwrite files it does not own",
			"name one exact path instead of a glob pattern", nil,
		)
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || cleaned == "/" {
		return OwnedPath{}, effectError(
			fmt.Sprintf("owned path %q does not name a specific entry", raw),
			"an effect must name a specific owned entry, not the root or a traversal",
			"NewOwnedPath", "path validation",
			"the effect target is not a single owned entry",
			"name a specific relative path under the effect root", nil,
		)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return OwnedPath{}, effectError(
			fmt.Sprintf("owned path %q escapes its root", raw),
			"a '..' segment lets an effect reach outside the root it owns",
			"NewOwnedPath", "path validation",
			"the effect could reach an unowned location",
			"remove '..' segments so the path stays within its root", nil,
		)
	}
	if cleaned != raw {
		return OwnedPath{}, effectError(
			fmt.Sprintf("owned path %q is not in normalized form (%q)", raw, cleaned),
			"accepting multiple spellings of one path would make ownership and equality checks unstable",
			"NewOwnedPath", "path validation",
			"two spellings of one path could compare unequal",
			fmt.Sprintf("supply the normalized form %q", cleaned), nil,
		)
	}
	return OwnedPath{value: cleaned, constructed: true}, nil
}

func (p OwnedPath) String() string { return p.value }
func (p OwnedPath) IsValid() bool  { return p.constructed && p.value != "" }

// Equal reports exact owned-path equality.
func (p OwnedPath) Equal(other OwnedPath) bool {
	return p.IsValid() && other.IsValid() && p.value == other.value
}
