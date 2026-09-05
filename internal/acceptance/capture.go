package acceptance

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/acceptance/origin"
)

// MaxCaptureFixtureBytes bounds one native capture payload during validation.
const MaxCaptureFixtureBytes = 1 << 20

// CaptureOrigin aliases the single capture-origin enum definition in
// internal/acceptance/origin. The definition lives in that leaf package so the
// lifecycle receipt and envelope carriers can import it without closing an
// import cycle through acceptance -> internal/tasks -> lifecycle; the corpus
// re-exports the type and values here under their historical names.
type CaptureOrigin = origin.CaptureOrigin

const (
	OriginAuthenticCapture = origin.OriginAuthenticCapture
	OriginPinnedContract   = origin.OriginPinnedContract
	OriginReviewFinding    = origin.OriginReviewFinding
	OriginAuthored         = origin.OriginAuthored
	OriginRaw              = origin.OriginRaw
)

// RedactionRule is one substitution rule applied to a capture before its bytes
// were committed. The set is closed: a sidecar that names a rule outside it is
// refused, so a rule can never be invented in a sidecar alone.
type RedactionRule string

const (
	// RedactionNone states that no substitution was applied. It stands alone.
	RedactionNone RedactionRule = "none"
	// RedactionHomePath replaces the capturing user's home directory.
	RedactionHomePath RedactionRule = "home-path-v1"
	// RedactionFreeText replaces free-text fields (prompt text, tool arguments,
	// tool output) with placeholders of the same shape.
	RedactionFreeText RedactionRule = "free-text-v1"
)

// redactionRuleSeparator joins the rules of a Redaction value in the order they
// were applied.
const redactionRuleSeparator = ","

func (r RedactionRule) IsValid() bool {
	switch r {
	case RedactionNone, RedactionHomePath, RedactionFreeText:
		return true
	default:
		return false
	}
}

// ParseRedaction reads a Redaction value: "none", or every applied rule in the
// order it was applied, joined by ",". A rule outside the closed set, a rule
// listed twice, "none" combined with a rule, and an empty value are refused.
func ParseRedaction(value string) ([]RedactionRule, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("authentic capture provenance redaction is empty; record %q when no substitution was applied, or list every applied rule in order, joined by %q, from %s", RedactionNone, redactionRuleSeparator, knownRedactionRules())
	}
	parts := strings.Split(value, redactionRuleSeparator)
	rules := make([]RedactionRule, 0, len(parts))
	seen := make(map[RedactionRule]struct{}, len(parts))
	for _, part := range parts {
		rule := RedactionRule(strings.TrimSpace(part))
		if !rule.IsValid() {
			return nil, fmt.Errorf("authentic capture provenance redaction %q names unknown rule %q; the closed set is %s", value, rule, knownRedactionRules())
		}
		if _, duplicate := seen[rule]; duplicate {
			return nil, fmt.Errorf("authentic capture provenance redaction %q lists rule %q twice; list each applied rule once, in the order applied", value, rule)
		}
		seen[rule] = struct{}{}
		rules = append(rules, rule)
	}
	if _, none := seen[RedactionNone]; none && len(rules) > 1 {
		return nil, fmt.Errorf("authentic capture provenance redaction %q combines %q with an applied rule; %q stands alone", value, RedactionNone, RedactionNone)
	}
	return rules, nil
}

func knownRedactionRules() string {
	return fmt.Sprintf("%q, %q, %q", RedactionNone, RedactionHomePath, RedactionFreeText)
}

// clearanceFileName is the file that holds the user's verbatim acceptance of a
// cleared capture batch. A Clearance value is the committed path of that file.
const clearanceFileName = "CLEARANCE.md"

// bareTrackerIDPattern recognises a bare task-tracker identifier. A tracker id
// is not durable and is not a clearance record, so it is refused by name and
// not only by the missing file suffix: the refusal must tell the writer what
// the value is, not only what it is not.
var bareTrackerIDPattern = regexp.MustCompile(`^[a-z0-9-]+-[a-z0-9]{5,6}$`)

// CaptureProvenance is the sidecar record of one authentic native capture.
// JSON keys are the sidecar keys as committed next to each fixture.
type CaptureProvenance struct {
	Origin         CaptureOrigin `json:"origin"`
	Harness        HarnessKind   `json:"harness"`
	HarnessVersion string        `json:"harnessVersion"`
	CaptureSource  string        `json:"captureSource"`
	RawFileDigest  string        `json:"rawFileDigest"`
	CapturedAt     string        `json:"capturedAt"`
	// Event is the exact native event name the host declared for the capture.
	Event string `json:"event"`
	// Redaction lists every substitution rule applied to the committed bytes,
	// in the order applied; see ParseRedaction. The digest above is computed
	// over the committed bytes, after every listed rule was applied.
	Redaction string `json:"redaction"`
	// Clearance is the committed path of the CLEARANCE.md that holds the user's
	// verbatim acceptance of the batch this capture was cleared in. It is a
	// path, never a bare identifier.
	Clearance string `json:"clearance"`
}

func (p CaptureProvenance) ValidateFixture(root, fixture string) error {
	if p.Origin != OriginAuthenticCapture {
		return nil
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve authentic capture root %q: %w", root, err)
	}
	path, err := filepath.Abs(filepath.Join(cleanRoot, fixture))
	if err != nil {
		return fmt.Errorf("resolve authentic capture fixture %q: %w", fixture, err)
	}
	rel, err := filepath.Rel(cleanRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("authentic capture fixture %q escapes corpus root %q", fixture, root)
	}
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return fmt.Errorf("resolve authentic capture root symlinks %q: %w", root, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve authentic capture fixture symlinks %q: %w", fixture, err)
	}
	resolvedRel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("authentic capture fixture %q resolves outside corpus root %q; keep fixture symlinks within the reviewed root", fixture, root)
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		return fmt.Errorf("open authentic capture fixture %q: %w", fixture, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, MaxCaptureFixtureBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return fmt.Errorf("read bounded authentic capture fixture %q: %w", fixture, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close authentic capture fixture %q after bounded read: %w", fixture, closeErr)
	}
	if len(body) > MaxCaptureFixtureBytes {
		return fmt.Errorf("authentic capture fixture %q exceeds the %d-byte native payload bound; reduce or reject the capture", fixture, MaxCaptureFixtureBytes)
	}
	if err := p.ValidateCommittedFixtureBytes(resolvedPath, body); err != nil {
		return fmt.Errorf("validate authentic capture fixture %q: %w", fixture, err)
	}
	return nil
}

// ValidateFixtureBytes is the single metadata and digest authority for already
// bounded capture bytes. Non-authentic provenance remains non-normative.
//
// An authentic capture must carry its event, its redaction record and its
// clearance path. Bytes alone cannot prove a committed path, so this entry
// point never applies the legacy exemption; ValidateCommittedFixtureBytes and
// ValidateFixture do, because they know where the bytes are committed.
func (p CaptureProvenance) ValidateFixtureBytes(body []byte) error {
	return p.validateBytes("", body)
}

// ValidateCommittedFixtureBytes validates already bounded capture bytes that
// are committed at path (absolute or repository-relative, as resolved by the
// caller). A capture whose committed path AND digest are both on the legacy
// exemption list validates without its event, redaction and clearance; every
// other capture needs all three whatever its host version.
func (p CaptureProvenance) ValidateCommittedFixtureBytes(path string, body []byte) error {
	return p.validateBytes(path, body)
}

func (p CaptureProvenance) validateBytes(committedPath string, body []byte) error {
	if p.Origin != OriginAuthenticCapture {
		return nil
	}
	if !p.Harness.IsValid() || strings.TrimSpace(p.HarnessVersion) == "" || strings.TrimSpace(p.CaptureSource) == "" {
		return fmt.Errorf("authentic capture provenance requires a known harness, exact harness version, and capture source")
	}
	when, err := time.Parse(time.RFC3339, p.CapturedAt)
	if err != nil || when.Location() != time.UTC {
		return fmt.Errorf("authentic capture provenance capturedAt %q must be RFC3339 UTC", p.CapturedAt)
	}
	want, err := digest.Parse(p.RawFileDigest)
	if err != nil || want.Algorithm() != digest.SHA256 {
		return fmt.Errorf("authentic capture provenance rawFileDigest %q must be a sha256 digest", p.RawFileDigest)
	}
	got := digest.FromBytes(body)
	if got != want {
		return fmt.Errorf("authentic capture bytes digest is %s, want %s", got, want)
	}
	if committedPath != "" && IsLegacyExemptCapture(committedPath, got) {
		return nil
	}
	if strings.TrimSpace(p.Event) == "" {
		return fmt.Errorf("authentic capture provenance event is empty; record the exact native event name the host declared for this capture")
	}
	if _, err := ParseRedaction(p.Redaction); err != nil {
		return err
	}
	return ValidateClearancePath(p.Clearance)
}

// ValidateClearancePath refuses every Clearance value that is not a committed
// path of a CLEARANCE.md file. The refusals name what the value is, so a
// writer learns which rule it broke.
func ValidateClearancePath(clearance string) error {
	if strings.TrimSpace(clearance) == "" {
		return fmt.Errorf("authentic capture provenance clearance is empty; record the committed path of the %s that holds the user's verbatim acceptance of this capture batch", clearanceFileName)
	}
	if IsBareTrackerID(clearance) {
		return fmt.Errorf("authentic capture provenance clearance %q is a bare task-tracker id, not a committed path; a tracker id is not durable and is not a clearance record; record the committed path of the %s that holds the user's verbatim acceptance", clearance, clearanceFileName)
	}
	if strings.HasPrefix(clearance, "/") || filepath.IsAbs(clearance) {
		return fmt.Errorf("authentic capture provenance clearance %q is an absolute path, not a committed path; record the path of the %s relative to the repository root", clearance, clearanceFileName)
	}
	if clearance != path.Clean(clearance) || strings.Contains(clearance, `\`) || hasParentTraversal(clearance) {
		return fmt.Errorf("authentic capture provenance clearance %q is not a clean, forward-slash, repository-relative path; record the committed path of the %s without \"..\" or \"\\\" elements", clearance, clearanceFileName)
	}
	if path.Base(clearance) != clearanceFileName || !strings.HasSuffix(clearance, "/"+clearanceFileName) {
		return fmt.Errorf("authentic capture provenance clearance %q does not end in /%s; the clearance record is the %s file that holds the user's verbatim acceptance; record its committed path", clearance, clearanceFileName, clearanceFileName)
	}
	return nil
}

func hasParentTraversal(p string) bool {
	for _, element := range strings.Split(p, "/") {
		if element == ".." {
			return true
		}
	}
	return false
}

// LegacyExemptCapture is one committed capture that predates the event,
// redaction and clearance fields: its committed repository-relative path and
// the sha256 of its committed bytes. Both must match for the exemption to
// apply, so neither a new fixture that copies a legacy payload to another
// path nor new bytes at a legacy path can inherit it.
type LegacyExemptCapture struct {
	Path   string
	Digest digest.Digest
}

// legacyExemptCaptures enumerates the committed authentic captures that were
// committed before a capture sidecar carried an event, a redaction record and
// a clearance path. Only a capture at one of these paths with these bytes may
// validate without those three fields.
//
// WHAT IS COUNTED. The Claude ingress corpus holds 14 sidecars in the
// CaptureProvenance shape (the Codex and OpenCode sidecars of the same era use
// a provider shape that no code path decodes as a CaptureProvenance). Of the
// 14, two never reach this list: the origin-authored control returns before
// any field check, and the digest-mismatch control fails the digest check by
// design. The remaining 12 are listed here. They carry 11 distinct payloads:
// the session_start_2_1_210 capture and its version-out-of-range control share
// one.
//
// This list is a bridge, not a policy. The next pin bump recaptures these
// events at the frozen host versions, deletes these fixtures, and deletes this
// list with them; TestLegacyExemptionListEqualsCommittedSidecarsWithoutClearance
// holds the list equal, in both directions, to the committed sidecars that
// still lack a clearance path and requires it non-empty while they exist, so
// the list can neither rot, nor grow, nor outlive them.
var legacyExemptCaptures = [...]LegacyExemptCapture{
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/elicitation_2_1_222.json", Digest: "sha256:b3a426a5a273ff4a52c5834dc1846295617c706f2427d38a7e40f6b0f0e98112"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/elicitation_result_2_1_222.json", Digest: "sha256:661515e7e208f0fc8d27ebd8d0083be25708fa97a52739e4dff749bbca1d49f3"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/post_compact_2_1_222.json", Digest: "sha256:963fd4e58ff6424f9195a3e7c321fb01686bcfa4369dd50264e7af41322ccf20"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_batch_2_1_222.json", Digest: "sha256:2dd6c5e05902d1a07ca86258ef91adfd7b957118cac5c17d5d888f8b533b5e6e"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_2_1_222.json", Digest: "sha256:b7de90f8fa0afb1a62f947b311cde85799417e794e5a58305e920d0006bf3da9"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_failure_2_1_222.json", Digest: "sha256:a0ec3e466598b80607b244524e01b859dfb953c5fba3e50ba2546222f73b58b7"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/pre_compact_2_1_222.json", Digest: "sha256:5d5034c61e3ac28bba586d189323e9a0943d5c07a59e3eafe3da56b7ed714df1"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_222.json", Digest: "sha256:b147a832c7f9781b991443813dd0438ef9990af21af35f9113ad63e50435192e"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/session_end_2_1_222.json", Digest: "sha256:dfdbd5d6c525d2d057171c7c15c6439c235f7506e11988c6cd7ae80e827ba16e"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_222.json", Digest: "sha256:7b3b956017b2c9cf5e430878fb24d8cce77ce93eb6c4fed8a9ea3826124721d7"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_210.json", Digest: "sha256:30d524e5d2cb22d486faad05adbaa1a4b7e0d72cd6301f38fe18ca5e3f167003"},
	{Path: "internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_210_version_out_of_range.json", Digest: "sha256:30d524e5d2cb22d486faad05adbaa1a4b7e0d72cd6301f38fe18ca5e3f167003"},
}

// IsLegacyExemptCapture reports whether bytes with digest d committed at path
// predate the event, redaction and clearance fields and may validate without
// them. The path may be absolute or repository-relative: an entry matches when
// its committed path is the whole slash-normalised path or a "/"-bounded
// suffix of it, so an archive copy of the repository resolves and a copy of
// the payload under any other name does not.
func IsLegacyExemptCapture(path string, d digest.Digest) bool {
	normalised := filepath.ToSlash(path)
	for _, entry := range legacyExemptCaptures {
		if entry.Digest != d {
			continue
		}
		if normalised == entry.Path || strings.HasSuffix(normalised, "/"+entry.Path) {
			return true
		}
	}
	return false
}

// LegacyExemptCaptures returns the enumerated exemption as a fresh slice.
func LegacyExemptCaptures() []LegacyExemptCapture {
	out := make([]LegacyExemptCapture, len(legacyExemptCaptures))
	copy(out, legacyExemptCaptures[:])
	return out
}

// IsBareTrackerID reports whether a value has the shape of a bare
// task-tracker identifier. A tracker id is not durable and names nothing a
// reader can open from the repository, so no committed sidecar may carry one
// in any field.
func IsBareTrackerID(value string) bool {
	return bareTrackerIDPattern.MatchString(value)
}
