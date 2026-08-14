package artifact

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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

func (h Harness) IsValid() bool {
	return h == HarnessClaudeCode || h == HarnessOpenCode || h == HarnessCodex
}

// RuntimeContractID is one opaque constructor-owned, harness-bound target identity.
type RuntimeContractID struct {
	harness Harness
	value   string
}

// NewRuntimeContractID constructs a canonical <harness>/<profile> identity.
func NewRuntimeContractID(harness Harness, name string) (RuntimeContractID, error) {
	if !harness.IsValid() {
		return RuntimeContractID{}, aggregateInvalid("runtime contract construction", "harness", fmt.Sprintf("unsupported harness %q", harness), "the contract cannot be bound to a target", "use a supported Harness", fs.ErrInvalid)
	}
	prefix := string(harness) + "/"
	if !strings.HasPrefix(name, prefix) {
		name = prefix + name
	}
	return parseRuntimeContractID(name)
}

// ParseRuntimeContractID parses the same canonical identity emitted by target descriptors.
func ParseRuntimeContractID(value string) (RuntimeContractID, error) {
	return parseRuntimeContractID(value)
}

func parseRuntimeContractID(value string) (RuntimeContractID, error) {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || value == "" {
		return RuntimeContractID{}, aggregateInvalid("runtime contract decoding", "runtime_contract", fmt.Sprintf("contract %q is empty, padded, or invalid UTF-8", value), "runtime identity cannot be trusted", "use one canonical harness/profile identity", fs.ErrInvalid)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return RuntimeContractID{}, aggregateInvalid("runtime contract decoding", "runtime_contract", fmt.Sprintf("contract contains whitespace/control U+%04X", r), "runtime identity is ambiguous", "remove whitespace and controls", fs.ErrInvalid)
		}
	}
	for _, harness := range []Harness{HarnessClaudeCode, HarnessOpenCode, HarnessCodex} {
		prefix := string(harness) + "/"
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return RuntimeContractID{harness: harness, value: value}, nil
		}
	}
	return RuntimeContractID{}, aggregateInvalid("runtime contract decoding", "runtime_contract", fmt.Sprintf("contract %q is not bound to a supported harness", value), "runtime identity cannot match a target descriptor", "construct it with NewRuntimeContractID", fs.ErrInvalid)
}
func (id RuntimeContractID) Harness() Harness { return id.harness }
func (id RuntimeContractID) String() string   { return id.value }
func (id RuntimeContractID) IsValid() bool {
	parsed, err := parseRuntimeContractID(id.value)
	return err == nil && parsed.harness == id.harness
}
func (id RuntimeContractID) MarshalJSON() ([]byte, error) {
	if !id.IsValid() {
		return nil, aggregateInvalid("runtime contract encoding", "runtime_contract", "zero or invalid identity", "identity cannot be published", "construct it with NewRuntimeContractID", fs.ErrInvalid)
	}
	return json.Marshal(id.value)
}
func (id *RuntimeContractID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := parseRuntimeContractID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ProductionRuntimeContract returns the registered target profile accepted in aggregate releases.
func ProductionRuntimeContract(harness Harness) (RuntimeContractID, error) {
	names := map[Harness]string{HarnessClaudeCode: "claude-code@2.1.210", HarnessOpenCode: "opencode@1.18.10", HarnessCodex: "codex@0.146.0"}
	name, ok := names[harness]
	if !ok {
		return RuntimeContractID{}, aggregateInvalid("runtime contract lookup", "harness", fmt.Sprintf("unsupported harness %q", harness), "no production target profile is registered", "use a supported Harness", fs.ErrInvalid)
	}
	return NewRuntimeContractID(harness, name)
}

// Extension identifies one immutable generated component class.
type Extension uint8

const (
	ExtensionSkills Extension = iota + 1
	ExtensionAgents
	ExtensionHooks
)

func (e Extension) String() string {
	switch e {
	case ExtensionSkills:
		return "skills"
	case ExtensionAgents:
		return "agents"
	case ExtensionHooks:
		return "hooks"
	default:
		return ""
	}
}
func (e Extension) IsValid() bool {
	return e == ExtensionSkills || e == ExtensionAgents || e == ExtensionHooks
}
func ParseExtension(value string) (Extension, error) { return parseExtension(value) }
func (e Extension) MarshalText() ([]byte, error) {
	if !e.IsValid() {
		return nil, aggregateInvalid("extension encoding", "extension", "zero or unknown extension", "coordinate cannot be encoded", "use a supported Extension", fs.ErrInvalid)
	}
	return []byte(e.String()), nil
}
func (e *Extension) UnmarshalText(text []byte) error {
	parsed, err := ParseExtension(string(text))
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}

// ComponentID is the canonical target descriptor identity harness/extension.
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
		aNumeric, bNumeric := isNumericIdentifier(a[i]), isNumericIdentifier(b[i])
		if aNumeric && bNumeric {
			if len(a[i]) < len(b[i]) || len(a[i]) == len(b[i]) && a[i] < b[i] {
				return -1
			}
			return 1
		}
		if aNumeric {
			return -1
		}
		if bNumeric {
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

func isNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
	harnessText, extensionText, ok := strings.Cut(value, "/")
	if !ok {
		return "", aggregateInvalid("component identity decoding", "component", fmt.Sprintf("%q is not harness/extension", value), "the component cannot be addressed safely", "use the exact target descriptor identity", fs.ErrInvalid)
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
	switch value {
	case "skills":
		return ExtensionSkills, nil
	case "agents":
		return ExtensionAgents, nil
	case "hooks":
		return ExtensionHooks, nil
	}
	return 0, aggregateInvalid("manifest decoding", "extension", fmt.Sprintf("unsupported extension %q", value), "the component cannot be placed in the closed installation matrix", "use skills, agents, or hooks", fs.ErrInvalid)
}

func canonicalComponentID(h Harness, e Extension) ComponentID {
	return ComponentID(string(h) + "/" + e.String())
}
