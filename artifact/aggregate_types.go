package artifact

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

const AggregateManifestSchema = "pasture.aggregate-release/v1"

// ReleaseChannel is the closed aggregate release channel.
type ReleaseChannel string

const (
	ReleaseFinal      ReleaseChannel = "final"
	ReleasePrerelease ReleaseChannel = "prerelease"
)

// Harness identifies one supported installation harness.
type Harness string

const (
	HarnessClaudeCode Harness = "claude-code"
	HarnessOpenCode   Harness = "opencode"
	HarnessCodex      Harness = "codex"
)

// Extension identifies one supported generated component class.
type Extension string

const (
	ExtensionSkills Extension = "skills"
	ExtensionAgents Extension = "agents"
	ExtensionHooks  Extension = "hooks"
)

// ComponentID is the canonical harness.extension identity.
type ComponentID string

// Revision is an immutable lowercase Git commit identity.
type Revision string

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Version is a validated semantic version without a leading v.
type Version struct {
	value string
	major uint64
	minor uint64
	patch uint64
	pre   string
}

// ParseVersion parses the canonical SemVer form used by aggregate releases.
func ParseVersion(value string) (Version, error) {
	if value == "" || strings.HasPrefix(value, "v") || strings.Contains(value, "+") {
		return Version{}, aggregateInvalid("version decoding", "version", fmt.Sprintf("%q is not canonical release SemVer", value), "release selection and compatibility cannot be evaluated", "use MAJOR.MINOR.PATCH with an optional hyphen prerelease and no leading v or build metadata", fs.ErrInvalid)
	}
	core, pre, hasPre := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 || (hasPre && !validPrerelease(pre)) {
		return Version{}, aggregateInvalid("version decoding", "version", fmt.Sprintf("%q is not canonical release SemVer", value), "release selection and compatibility cannot be evaluated", "use MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-prerelease", fs.ErrInvalid)
	}
	numbers := make([]uint64, 3)
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, aggregateInvalid("version decoding", "version", fmt.Sprintf("numeric component %q is empty or has a leading zero", part), "version ordering would be ambiguous", "use canonical base-10 SemVer numeric components", fs.ErrInvalid)
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, aggregateInvalid("version decoding", "version", fmt.Sprintf("numeric component %q is invalid", part), "version ordering cannot be evaluated", "use unsigned base-10 SemVer numeric components", err)
		}
		numbers[i] = n
	}
	return Version{value: value, major: numbers[0], minor: numbers[1], patch: numbers[2], pre: pre}, nil
}

func validPrerelease(value string) bool {
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		allNumeric := true
		for _, r := range part {
			allNumeric = allNumeric && r >= '0' && r <= '9'
		}
		if part == "" || (allNumeric && len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, r := range part {
			if !(r == '-' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
				return false
			}
		}
	}
	return true
}

func (v Version) String() string     { return v.value }
func (v Version) IsPrerelease() bool { return v.pre != "" }
func (r Revision) String() string    { return string(r) }

// Compare returns -1, 0, or 1 according to SemVer precedence.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]uint64{{v.major, other.major}, {v.minor, other.minor}, {v.patch, other.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if v.pre == other.pre {
		return 0
	}
	if v.pre == "" {
		return 1
	}
	if other.pre == "" {
		return -1
	}
	a, b := strings.Split(v.pre, "."), strings.Split(other.pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		an, ae := strconv.ParseUint(a[i], 10, 64)
		bn, be := strconv.ParseUint(b[i], 10, 64)
		if ae == nil && be == nil {
			if an < bn {
				return -1
			}
			return 1
		}
		if ae == nil {
			return -1
		}
		if be == nil {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
		return 1
	}
	if len(a) < len(b) {
		return -1
	}
	return 1
}

func parseRevision(value, field string) (Revision, error) {
	if !revisionPattern.MatchString(value) {
		return "", aggregateInvalid("manifest decoding", field, fmt.Sprintf("%q is not a 40-character lowercase Git commit", value), "the aggregate cannot be tied to immutable source", "publish the exact lowercase Git commit SHA", fs.ErrInvalid)
	}
	return Revision(value), nil
}

// ParseRevision validates an immutable Git commit identity.
func ParseRevision(value string) (Revision, error) { return parseRevision(value, "revision") }

// ParseComponentID validates one member of the closed three-by-three matrix.
func ParseComponentID(value string) (ComponentID, error) {
	harnessText, extensionText, ok := strings.Cut(value, ".")
	if !ok {
		return "", aggregateInvalid("component identity decoding", "component", fmt.Sprintf("%q is not harness.extension", value), "the component cannot be addressed safely", "use one canonical harness.extension identity", fs.ErrInvalid)
	}
	harness, err := parseHarness(harnessText)
	if err != nil {
		return "", err
	}
	extension, err := parseExtension(extensionText)
	if err != nil {
		return "", err
	}
	return canonicalComponentID(harness, extension), nil
}

func parseHarness(value string) (Harness, error) {
	switch Harness(value) {
	case HarnessClaudeCode, HarnessOpenCode, HarnessCodex:
		return Harness(value), nil
	}
	return "", aggregateInvalid("manifest decoding", "harness", fmt.Sprintf("unsupported harness %q", value), "the component cannot be placed in the closed installation matrix", "use claude-code, opencode, or codex", fs.ErrInvalid)
}

func parseExtension(value string) (Extension, error) {
	switch Extension(value) {
	case ExtensionSkills, ExtensionAgents, ExtensionHooks:
		return Extension(value), nil
	}
	return "", aggregateInvalid("manifest decoding", "extension", fmt.Sprintf("unsupported extension %q", value), "the component cannot be placed in the closed installation matrix", "use skills, agents, or hooks", fs.ErrInvalid)
}

func canonicalComponentID(h Harness, e Extension) ComponentID {
	return ComponentID(string(h) + "." + string(e))
}
