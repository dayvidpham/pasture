package runtime

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// runtimeError builds the shared actionable diagnostic used across the runtime
// contract package. It reuses ir.Diagnostic so runtime, IR, and effect errors
// present the same what/why/where/phase/impact/fix/cause shape to callers.
func runtimeError(what, why, where, impact, fix string, cause error) error {
	return &ir.Diagnostic{
		What:   what,
		Why:    why,
		Where:  where,
		Phase:  "runtime contract validation",
		Impact: impact,
		Fix:    fix,
		Cause:  cause,
	}
}

// HostVersion is a parsed, comparable harness host version. It follows semantic
// versioning precedence: a release triple MAJOR.MINOR.PATCH, an optional
// dot-separated prerelease series (which lowers precedence below the same
// release), and optional build metadata (which never changes precedence). It
// is opaque and constructor-owned: the only way to produce a non-zero value is
// ParseHostVersion, so a HostVersion in hand always parsed cleanly and a
// contract never has to re-parse a raw, possibly-garbage version string.
type HostVersion struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []string
	build      []string
	parsed     bool
}

// ParseHostVersion parses one harness host version. It accepts a leading "v"
// (so `v2.1.210` and `2.1.210` are the same version), a MAJOR.MINOR.PATCH
// triple, an optional `-prerelease` series, and optional `+build` metadata. It
// rejects empty, padded, or otherwise unparsable output actionably rather than
// letting a garbage string silently compare as some default version.
func ParseHostVersion(value string) (HostVersion, error) {
	where := "ParseHostVersion"
	if strings.TrimSpace(value) != value || value == "" {
		return HostVersion{}, runtimeError(
			fmt.Sprintf("host version %q is empty or has surrounding whitespace", value),
			"a version must have one exact spelling to compare against a contract's accepted range",
			where, "the host cannot be matched against any runtime contract",
			"supply the harness version as an exact MAJOR.MINOR.PATCH string", nil,
		)
	}
	core := strings.TrimPrefix(value, "v")

	var build []string
	if plus := strings.IndexByte(core, '+'); plus >= 0 {
		rawBuild := core[plus+1:]
		core = core[:plus]
		parsedBuild, err := parseDotSeries(rawBuild, "build metadata", where)
		if err != nil {
			return HostVersion{}, err
		}
		build = parsedBuild
	}

	var prerelease []string
	if dash := strings.IndexByte(core, '-'); dash >= 0 {
		rawPre := core[dash+1:]
		core = core[:dash]
		parsedPre, err := parseDotSeries(rawPre, "prerelease", where)
		if err != nil {
			return HostVersion{}, err
		}
		prerelease = parsedPre
	}

	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return HostVersion{}, runtimeError(
			fmt.Sprintf("host version %q is not a MAJOR.MINOR.PATCH triple", value),
			"contract matching compares three numeric release components in order",
			where, "the host cannot be matched against any runtime contract",
			"supply exactly three dot-separated numeric components, e.g. 2.1.210", nil,
		)
	}
	numbers := make([]uint64, 3)
	for index, field := range fields {
		if field == "" || (len(field) > 1 && field[0] == '0') {
			return HostVersion{}, runtimeError(
				fmt.Sprintf("host version %q has an empty or zero-padded numeric component", value),
				"one release identity must have one canonical spelling",
				where, "the host cannot be matched against any runtime contract",
				"remove leading zeros and empty components", nil,
			)
		}
		parsed, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return HostVersion{}, runtimeError(
				fmt.Sprintf("host version %q has a non-numeric release component %q", value, field),
				"release components must be comparable base-10 integers",
				where, "the host cannot be matched against any runtime contract",
				"supply numeric MAJOR.MINOR.PATCH components", err,
			)
		}
		numbers[index] = parsed
	}
	return HostVersion{
		major: numbers[0], minor: numbers[1], patch: numbers[2],
		prerelease: prerelease, build: build, parsed: true,
	}, nil
}

func parseDotSeries(raw, domain, where string) ([]string, error) {
	if raw == "" {
		return nil, runtimeError(
			fmt.Sprintf("host version %s series is empty", domain),
			"an empty series after '-' or '+' is an ambiguous, unparsable spelling",
			where, "the host cannot be matched against any runtime contract",
			fmt.Sprintf("omit the %s separator or supply a non-empty series", domain), nil,
		)
	}
	fields := strings.Split(raw, ".")
	for _, field := range fields {
		if field == "" {
			return nil, runtimeError(
				fmt.Sprintf("host version %s series has an empty identifier", domain),
				"dot-separated series identifiers must each be non-empty",
				where, "the host cannot be matched against any runtime contract",
				fmt.Sprintf("remove empty %s identifiers", domain), nil,
			)
		}
		for _, r := range field {
			if !(r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return nil, runtimeError(
					fmt.Sprintf("host version %s identifier %q has an unsupported character", domain, field),
					"portable version series use only alphanumerics and hyphen",
					where, "the host cannot be matched against any runtime contract",
					fmt.Sprintf("restrict %s identifiers to [0-9A-Za-z-]", domain), nil,
				)
			}
		}
		// Semver forbids leading zeroes on NUMERIC prerelease identifiers (the
		// same rule the release triple already enforces); build metadata is
		// exempt because it never participates in precedence.
		if domain == "prerelease" && len(field) > 1 && field[0] == '0' && isAllDigits(field) {
			return nil, runtimeError(
				fmt.Sprintf("host version %s identifier %q has a leading zero", domain, field),
				"numeric prerelease identifiers must not include leading zeroes, matching the release-triple rule",
				where, "the host cannot be matched against any runtime contract",
				"drop the leading zero or make the identifier alphanumeric", nil,
			)
		}
	}
	return fields, nil
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func (v HostVersion) IsValid() bool { return v.parsed }

// HasPrerelease reports whether the version carries a prerelease series, which
// a constraint must explicitly include before it matches.
func (v HostVersion) HasPrerelease() bool { return len(v.prerelease) > 0 }

// String renders the version in canonical form. Build metadata is preserved for
// display but never participates in precedence.
func (v HostVersion) String() string {
	if !v.parsed {
		return ""
	}
	out := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.prerelease) > 0 {
		out += "-" + strings.Join(v.prerelease, ".")
	}
	if len(v.build) > 0 {
		out += "+" + strings.Join(v.build, ".")
	}
	return out
}

// ComparePrecedence orders two versions by semantic-version precedence. Build
// metadata is ignored; a prerelease has lower precedence than the same release.
// It returns -1, 0, or +1.
func ComparePrecedence(a, b HostVersion) int {
	if c := compareUint(a.major, b.major); c != 0 {
		return c
	}
	if c := compareUint(a.minor, b.minor); c != 0 {
		return c
	}
	if c := compareUint(a.patch, b.patch); c != 0 {
		return c
	}
	return comparePrerelease(a.prerelease, b.prerelease)
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func comparePrerelease(a, b []string) int {
	// A version without a prerelease has HIGHER precedence than one with.
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for index := 0; index < len(a) && index < len(b); index++ {
		if c := comparePrereleaseIdentifier(a[index], b[index]); c != 0 {
			return c
		}
	}
	return compareUint(uint64(len(a)), uint64(len(b)))
}

func comparePrereleaseIdentifier(a, b string) int {
	aNum, aIsNum := parseNumericIdentifier(a)
	bNum, bIsNum := parseNumericIdentifier(b)
	switch {
	case aIsNum && bIsNum:
		return compareUint(aNum, bNum)
	case aIsNum:
		// Numeric identifiers have lower precedence than alphanumeric ones.
		return -1
	case bIsNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func parseNumericIdentifier(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// VersionConstraint is a closed, inclusive host-version range [min, max]
// bounded by semantic-version precedence, plus an explicit prerelease-inclusion
// policy. A pinned point contract sets min == max. Prereleases are matched only
// when the constraint explicitly includes them, so an unreleased build never
// silently satisfies a stable contract.
type VersionConstraint struct {
	min                HostVersion
	max                HostVersion
	includePrereleases bool
	constructed        bool
}

// NewExactVersion returns a constraint that accepts exactly one release
// version. It is the constructor for the initial pinned point ranges.
func NewExactVersion(version HostVersion) (VersionConstraint, error) {
	return NewVersionConstraint(version, version, false)
}

// NewVersionConstraint returns an inclusive [min, max] host-version range.
// includePrereleases governs whether prerelease host versions are eligible at
// all. min must not exceed max by precedence.
func NewVersionConstraint(min, max HostVersion, includePrereleases bool) (VersionConstraint, error) {
	if !min.IsValid() || !max.IsValid() {
		return VersionConstraint{}, runtimeError(
			"version constraint has a zero or unparsed bound",
			"a range requires two parsed host versions",
			"NewVersionConstraint", "no runtime contract can be built on this range",
			"construct both bounds with ParseHostVersion", nil,
		)
	}
	if ComparePrecedence(min, max) > 0 {
		return VersionConstraint{}, runtimeError(
			fmt.Sprintf("version constraint lower bound %s exceeds upper bound %s", min, max),
			"an inclusive range must be non-empty",
			"NewVersionConstraint", "no host version could ever satisfy the range",
			"order the bounds so min <= max by precedence", nil,
		)
	}
	return VersionConstraint{min: min, max: max, includePrereleases: includePrereleases, constructed: true}, nil
}

func (c VersionConstraint) IsValid() bool { return c.constructed }

// Min and Max return the inclusive bounds.
func (c VersionConstraint) Min() HostVersion { return c.min }
func (c VersionConstraint) Max() HostVersion { return c.max }

// IncludesPrereleases reports the prerelease-inclusion policy.
func (c VersionConstraint) IncludesPrereleases() bool { return c.includePrereleases }

// Allows reports whether version satisfies the constraint. A prerelease host is
// eligible only when the constraint explicitly includes prereleases; build
// metadata never changes the decision.
func (c VersionConstraint) Allows(version HostVersion) bool {
	if !c.constructed || !version.IsValid() {
		return false
	}
	if version.HasPrerelease() && !c.includePrereleases {
		return false
	}
	return ComparePrecedence(version, c.min) >= 0 && ComparePrecedence(version, c.max) <= 0
}

// CapabilityVersionRange is an inclusive range over
// ir.CapabilityContractVersion values (MAJOR.MINOR.PATCH). BindCapability uses
// it to bound which capability contract versions a contribution honors, and to
// intersect against the requested capability's exact version.
type CapabilityVersionRange struct {
	min         ir.CapabilityContractVersion
	max         ir.CapabilityContractVersion
	constructed bool
}

// NewCapabilityVersionRange returns an inclusive [min, max] capability-version
// range. Both bounds must be well-formed MAJOR.MINOR.PATCH triples and min must
// not exceed max.
func NewCapabilityVersionRange(min, max ir.CapabilityContractVersion) (CapabilityVersionRange, error) {
	if !min.IsValid() || !max.IsValid() {
		return CapabilityVersionRange{}, runtimeError(
			"capability version range has an invalid bound",
			"capability contracts are range-bound by exact MAJOR.MINOR.PATCH identity",
			"NewCapabilityVersionRange", "the capability contribution cannot be bounded",
			`use MAJOR.MINOR.PATCH bounds such as "1.0.0"`, nil,
		)
	}
	if compareCapabilityVersions(min, max) > 0 {
		return CapabilityVersionRange{}, runtimeError(
			fmt.Sprintf("capability version range lower bound %q exceeds upper bound %q", min, max),
			"an inclusive range must be non-empty",
			"NewCapabilityVersionRange", "no capability version could satisfy the range",
			"order the bounds so min <= max", nil,
		)
	}
	return CapabilityVersionRange{min: min, max: max, constructed: true}, nil
}

// NewExactCapabilityVersion returns a range accepting exactly one capability
// contract version.
func NewExactCapabilityVersion(version ir.CapabilityContractVersion) (CapabilityVersionRange, error) {
	return NewCapabilityVersionRange(version, version)
}

func (r CapabilityVersionRange) IsValid() bool                     { return r.constructed }
func (r CapabilityVersionRange) Min() ir.CapabilityContractVersion { return r.min }
func (r CapabilityVersionRange) Max() ir.CapabilityContractVersion { return r.max }

// Includes reports whether version falls within the inclusive range.
func (r CapabilityVersionRange) Includes(version ir.CapabilityContractVersion) bool {
	if !r.constructed || !version.IsValid() {
		return false
	}
	return compareCapabilityVersions(version, r.min) >= 0 && compareCapabilityVersions(version, r.max) <= 0
}

// Intersects reports whether two ranges overlap.
func (r CapabilityVersionRange) Intersects(other CapabilityVersionRange) bool {
	if !r.constructed || !other.constructed {
		return false
	}
	return compareCapabilityVersions(maxCapabilityVersion(r.min, other.min), minCapabilityVersion(r.max, other.max)) <= 0
}

func compareCapabilityVersions(a, b ir.CapabilityContractVersion) int {
	aMajor, aMinor, aPatch := splitCapabilityVersion(a)
	bMajor, bMinor, bPatch := splitCapabilityVersion(b)
	if c := compareUint(aMajor, bMajor); c != 0 {
		return c
	}
	if c := compareUint(aMinor, bMinor); c != 0 {
		return c
	}
	return compareUint(aPatch, bPatch)
}

func splitCapabilityVersion(version ir.CapabilityContractVersion) (uint64, uint64, uint64) {
	fields := strings.Split(string(version), ".")
	if len(fields) != 3 {
		return 0, 0, 0
	}
	major, _ := strconv.ParseUint(fields[0], 10, 64)
	minor, _ := strconv.ParseUint(fields[1], 10, 64)
	patch, _ := strconv.ParseUint(fields[2], 10, 64)
	return major, minor, patch
}

func maxCapabilityVersion(a, b ir.CapabilityContractVersion) ir.CapabilityContractVersion {
	if compareCapabilityVersions(a, b) >= 0 {
		return a
	}
	return b
}

func minCapabilityVersion(a, b ir.CapabilityContractVersion) ir.CapabilityContractVersion {
	if compareCapabilityVersions(a, b) <= 0 {
		return a
	}
	return b
}
