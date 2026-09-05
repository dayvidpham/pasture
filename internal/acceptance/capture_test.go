package acceptance_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/acceptance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// ingressTestdataRoot is the committed ingress corpus, relative to this
// package's directory, that the legacy exemption list is held against.
const ingressTestdataRoot = "../lifecycle/ingress"

// acceptedClearancePath is a committed CLEARANCE.md path in the shape every
// cleared capture records.
const acceptedClearancePath = "internal/lifecycle/ingress/claude/testdata/CLEARANCE.md"

func TestCaptureProvenancePathAndBytesValidationParity(t *testing.T) {
	t.Parallel()
	body := []byte(`{"hook_event_name":"SessionStart"}`)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "capture.json"), body, 0o600))
	provenance := validCaptureProvenance(body)
	require.NoError(t, provenance.ValidateFixtureBytes(body))
	require.NoError(t, provenance.ValidateFixture(root, "capture.json"))
	for name, tc := range map[string]struct {
		mutate func(*acceptance.CaptureProvenance)
		want   string
	}{
		"metadata":        {func(p *acceptance.CaptureProvenance) { p.CaptureSource = "" }, "known harness"},
		"timestamp":       {func(p *acceptance.CaptureProvenance) { p.CapturedAt = "not-time" }, "RFC3339 UTC"},
		"digest-format":   {func(p *acceptance.CaptureProvenance) { p.RawFileDigest = "bad" }, "sha256 digest"},
		"digest-mismatch": {func(p *acceptance.CaptureProvenance) { p.RawFileDigest = digest.FromString("different").String() }, "digest is"},
		"event":           {func(p *acceptance.CaptureProvenance) { p.Event = "" }, "event is empty"},
		"redaction":       {func(p *acceptance.CaptureProvenance) { p.Redaction = "" }, "redaction is empty"},
		"clearance":       {func(p *acceptance.CaptureProvenance) { p.Clearance = "" }, "clearance is empty"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := provenance
			tc.mutate(&p)
			bytesErr := p.ValidateFixtureBytes(body)
			pathErr := p.ValidateFixture(root, "capture.json")
			require.ErrorContains(t, bytesErr, tc.want)
			require.ErrorContains(t, pathErr, tc.want)
		})
	}
}

func TestCaptureProvenanceValidateFixtureBoundsRead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := []byte(strings.Repeat("x", acceptance.MaxCaptureFixtureBytes+1))
	require.NoError(t, os.WriteFile(filepath.Join(root, "large.json"), body, 0o600))
	err := validCaptureProvenance(body).ValidateFixture(root, "large.json")
	require.ErrorContains(t, err, "exceeds")
	require.ErrorContains(t, err, "native payload bound")
}

func TestNonAuthenticCaptureValidationDoesNotRead(t *testing.T) {
	t.Parallel()
	p := acceptance.CaptureProvenance{Origin: acceptance.OriginAuthored}
	require.NoError(t, p.ValidateFixture(t.TempDir(), "missing.json"))
	require.NoError(t, p.ValidateFixtureBytes(nil))
}

func TestCaptureProvenanceRejectsResolvedSymlinkEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := []byte(`{"synthetic":"rejection-control"}`)
	outside := filepath.Join(t.TempDir(), "outside.json")
	require.NoError(t, os.WriteFile(outside, body, 0o600))
	fixture := filepath.Join(root, "capture.json")
	if err := os.Symlink(outside, fixture); err != nil {
		t.Skipf("symlink creation unsupported: %v", err)
	}
	err := validCaptureProvenance(body).ValidateFixture(root, "capture.json")
	require.ErrorContains(t, err, "resolves outside corpus root")
}

// TestCaptureProvenanceClearanceIsACommittedPath pins every Clearance refusal
// by the phrase that names what the value IS, and the one accepted shape.
// The bare-tracker-id refusal is pinned by its own phrase on purpose: the
// value would also fail the file-suffix rule, but the writer must be told that
// a tracker id is not a clearance record, not only that a suffix is missing.
func TestCaptureProvenanceClearanceIsACommittedPath(t *testing.T) {
	t.Parallel()
	body := []byte(`{"hook_event_name":"SessionStart","cwd":"/home/user/project"}`)
	for name, tc := range map[string]struct {
		clearance string
		want      string
	}{
		"empty":            {"", "clearance is empty"},
		"blank":            {"   ", "clearance is empty"},
		"bare-tracker-id":  {"tracker-item-ab12cd", "bare task-tracker id, not a committed path"},
		"not-clearance-md": {"docs/notes.md", "does not end in /CLEARANCE.md"},
		"root-level-file":  {"CLEARANCE.md", "does not end in /CLEARANCE.md"},
		"wrong-case-file":  {"internal/testdata/clearance.md", "does not end in /CLEARANCE.md"},
		"absolute":         {"/home/user/CLEARANCE.md", "absolute path, not a committed path"},
		"parent-traversal": {"internal/../secrets/CLEARANCE.md", "clean, forward-slash, repository-relative path"},
		"unclean":          {"internal//testdata/CLEARANCE.md", "clean, forward-slash, repository-relative path"},
		"backslash":        {`internal\testdata\CLEARANCE.md`, "clean, forward-slash, repository-relative path"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.ErrorContains(t, acceptance.ValidateClearancePath(tc.clearance), tc.want)
			p := validCaptureProvenance(body)
			p.Clearance = tc.clearance
			require.ErrorContains(t, p.ValidateFixtureBytes(body), tc.want)
		})
	}
	t.Run("accepted", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, acceptance.ValidateClearancePath(acceptedClearancePath))
		require.NoError(t, acceptance.ValidateClearancePath("internal/lifecycle/ingress/codex/testdata/CLEARANCE.md"))
		p := validCaptureProvenance(body)
		p.Clearance = acceptedClearancePath
		require.NoError(t, p.ValidateFixtureBytes(body))
	})
}

// claudeCorpusRoot is the committed Claude ingress corpus root, relative to
// this package's directory.
const claudeCorpusRoot = ingressTestdataRoot + "/claude/testdata"

// TestEveryCaptureNeedsEventRedactionAndClearance asserts that no committed
// path and no bytes earn an exemption from the three provenance fields: the
// committed SessionStart validates only WITH its sidecar's event, redaction
// and clearance, and the same bytes at the same committed path with those
// fields blank are refused naming the first missing one. New bytes at that
// path, and any bytes at any other path, are refused the same way.
func TestEveryCaptureNeedsEventRedactionAndClearance(t *testing.T) {
	t.Parallel()
	const fixture = "fixtures/session_start_2_1_261.json"
	abs, err := filepath.Abs(filepath.Join(claudeCorpusRoot, fixture))
	require.NoError(t, err)
	committedBytes, err := os.ReadFile(abs)
	require.NoError(t, err)
	fresh := []byte(`{"hook_event_name":"SessionStart","cwd":"/home/user/project","session_id":"fresh-bytes"}`)

	bare := func(body []byte, version string) acceptance.CaptureProvenance {
		p := validCaptureProvenance(body)
		p.HarnessVersion = version
		p.Event, p.Redaction, p.Clearance = "", "", ""
		return p
	}

	t.Run("the-committed-capture-validates-with-its-sidecar", func(t *testing.T) {
		t.Parallel()
		var committed acceptance.CaptureProvenance
		raw, err := os.ReadFile(strings.TrimSuffix(abs, ".json") + ".provenance.json")
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &committed))
		require.NotEmpty(t, committed.Event)
		require.NotEmpty(t, committed.Redaction)
		require.NotEmpty(t, committed.Clearance, "every committed sidecar carries its clearance path")
		require.NoError(t, committed.ValidateFixture(claudeCorpusRoot, fixture))
	})
	t.Run("the-committed-bytes-at-their-committed-path-are-not-exempt", func(t *testing.T) {
		t.Parallel()
		p := bare(committedBytes, "2.1.261")
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(abs, committedBytes), "event is empty")
		p.Event = "SessionStart"
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(abs, committedBytes), "redaction is empty")
		p.Redaction = "home-path-v1"
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(abs, committedBytes), "clearance is empty")
		require.ErrorContains(t, p.ValidateFixture(claudeCorpusRoot, fixture), "clearance is empty")
		p.Clearance = acceptedClearancePath
		require.NoError(t, p.ValidateCommittedFixtureBytes(abs, committedBytes))
	})
	t.Run("bytes-alone-need-all-three", func(t *testing.T) {
		t.Parallel()
		require.ErrorContains(t, bare(committedBytes, "2.1.261").ValidateFixtureBytes(committedBytes), "event is empty")
	})
	t.Run("new-bytes-at-any-pin-need-all-three", func(t *testing.T) {
		t.Parallel()
		for _, version := range []string{"2.1.251", "2.1.261", "2.1.210"} {
			p := bare(fresh, version)
			p.Event, p.Redaction = "SessionStart", "none"
			require.ErrorContains(t, p.ValidateCommittedFixtureBytes(abs, fresh), "clearance is empty", "host version %s", version)
		}
	})
	t.Run("a-digest-mismatch-is-refused-before-the-field-checks", func(t *testing.T) {
		t.Parallel()
		p := bare(committedBytes, "2.1.261")
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(abs, append([]byte(nil), append(committedBytes, '\n')...)), "digest is")
	})
}

// TestEveryCommittedAuthenticSidecarCarriesAClearance walks EVERY committed
// provenance sidecar of every harness and holds each authentic one to the
// full provenance: an event, a redaction record that parses, a clearance path,
// and a digest that is the digest of its sibling fixture's committed bytes,
// so ValidateFixture accepts it. The population is derived from the corpora,
// so a fixture added later is covered the day it is committed. Controls: the
// walk read at least one sidecar per harness; and the two Claude controls
// that deliberately break the digest or the origin are named and expected.
func TestEveryCommittedAuthenticSidecarCarriesAClearance(t *testing.T) {
	t.Parallel()
	sidecars, err := filepath.Glob(filepath.Join(ingressTestdataRoot, "*", "testdata", "fixtures", "*.provenance.json"))
	require.NoError(t, err)
	require.NotEmpty(t, sidecars, "the walk read no sidecar at all under %s; the ingress corpora must still exist for this guard to assert anything", ingressTestdataRoot)

	perHarness := map[string]int{}
	expectedRefusals := map[string]string{
		"session_start_2_1_261_digest_mismatch.provenance.json": "digest is",
	}
	for _, sidecar := range sidecars {
		raw, err := os.ReadFile(sidecar)
		require.NoError(t, err)
		var p acceptance.CaptureProvenance
		require.NoError(t, json.Unmarshal(raw, &p), "sidecar %s is not a CaptureProvenance", sidecar)
		harnessDir := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(sidecar))))
		perHarness[harnessDir]++
		fixture := strings.TrimSuffix(sidecar, ".provenance.json") + ".json"
		_, err = os.Stat(fixture)
		require.NoError(t, err, "sidecar %s has no sibling fixture", sidecar)
		if p.Origin != acceptance.OriginAuthenticCapture {
			require.Equal(t, "session_start_2_1_261_origin_authored.provenance.json", filepath.Base(sidecar), "the only non-authentic sidecar is the origin control")
			continue
		}
		require.NotEmpty(t, p.Event, "sidecar %s carries no event", sidecar)
		_, err = acceptance.ParseRedaction(p.Redaction)
		require.NoError(t, err, "sidecar %s", sidecar)
		require.NotEmpty(t, p.Clearance, "sidecar %s carries no clearance path; no committed capture is exempt", sidecar)
		require.NoError(t, acceptance.ValidateClearancePath(p.Clearance), "sidecar %s", sidecar)
		root := filepath.Dir(filepath.Dir(sidecar))
		rel, err := filepath.Rel(root, fixture)
		require.NoError(t, err)
		err = p.ValidateFixture(root, rel)
		if want, control := expectedRefusals[filepath.Base(sidecar)]; control {
			require.ErrorContains(t, err, want, "the digest-mismatch control must be refused by the digest check")
			continue
		}
		require.NoError(t, err, "sidecar %s does not validate its committed fixture", sidecar)
	}
	for _, harness := range []string{"claude", "codex", "opencode"} {
		require.Positive(t, perHarness[harness], "the %s corpus contributed no sidecar; the corpus walk is broken or the directory moved", harness)
	}
}

// TestNoCommittedSidecarCarriesATrackerIdOrALocalPath walks EVERY committed
// provenance sidecar of every harness and refuses any string field whose value
// is a bare task-tracker id or a path into the uncommitted .agents.local tree.
// Such a value is data the product carries, so a prose sweep never finds it;
// the population is derived from the corpora, so a sidecar added later is
// covered the day it is committed. The control is that the walk read at least
// one sidecar and at least one string field.
func TestNoCommittedSidecarCarriesATrackerIdOrALocalPath(t *testing.T) {
	t.Parallel()
	sidecars, err := filepath.Glob(filepath.Join(ingressTestdataRoot, "*", "testdata", "fixtures", "*.provenance.json"))
	require.NoError(t, err)
	require.NotEmpty(t, sidecars, "the walk read no sidecar at all under %s", ingressTestdataRoot)
	fields := 0
	var visit func(sidecar, field string, value any)
	visit = func(sidecar, field string, value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				visit(sidecar, field+"."+key, child)
			}
		case []any:
			for index, child := range v {
				visit(sidecar, fmt.Sprintf("%s[%d]", field, index), child)
			}
		case string:
			fields++
			require.False(t, acceptance.IsBareTrackerID(v), "%s field %s carries the bare task-tracker id %q; a tracker id is not durable and names nothing a reader can open from the repository", sidecar, strings.TrimPrefix(field, "."), v)
			require.False(t, strings.HasPrefix(v, ".agents.local/") || strings.Contains(v, "/.agents.local/"), "%s field %s carries the uncommitted local path %q; a committed sidecar may cite only committed paths", sidecar, strings.TrimPrefix(field, "."), v)
		}
	}
	for _, sidecar := range sidecars {
		raw, err := os.ReadFile(sidecar)
		require.NoError(t, err)
		var value any
		require.NoError(t, json.Unmarshal(raw, &value), "sidecar %s is not JSON", sidecar)
		visit(sidecar, "", value)
	}
	require.NotZero(t, fields, "no string field was read, so nothing above was asserted")
}

// TestCaptureProvenanceRedactionRewriteRecomputesDigest is the rewrite rule:
// after every listed substitution the digest is computed over the COMMITTED
// bytes and the redaction record lists every rule in order. A stale digest
// over the raw bytes stays a hard error.
func TestCaptureProvenanceRedactionRewriteRecomputesDigest(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"hook_event_name":"UserPromptSubmit","cwd":"/home/alice/project","prompt":"fix the login bug in /home/alice/project/auth.go"}`)
	homeRedacted := []byte(strings.ReplaceAll(string(raw), "/home/alice", "/home/user"))
	committed := []byte(strings.Replace(string(homeRedacted), `"prompt":"fix the login bug in /home/user/project/auth.go"`, `"prompt":"free-text-v1 placeholder"`, 1))
	require.NotEqual(t, raw, committed)

	rewritten := validCaptureProvenance(committed)
	rewritten.Event = "UserPromptSubmit"
	rewritten.Redaction = "home-path-v1,free-text-v1"
	require.NoError(t, rewritten.ValidateFixtureBytes(committed))

	stale := rewritten
	stale.RawFileDigest = digest.FromBytes(raw).String()
	require.ErrorContains(t, stale.ValidateFixtureBytes(committed), "digest is")

	rules, err := acceptance.ParseRedaction("free-text-v1,home-path-v1")
	require.NoError(t, err)
	require.Equal(t, []acceptance.RedactionRule{acceptance.RedactionFreeText, acceptance.RedactionHomePath}, rules, "the order applied is the order recorded")
	none, err := acceptance.ParseRedaction("none")
	require.NoError(t, err)
	require.Equal(t, []acceptance.RedactionRule{acceptance.RedactionNone}, none)

	for name, tc := range map[string]struct{ value, want string }{
		"empty":        {"", "redaction is empty"},
		"unknown-rule": {"home-path-v2", `unknown rule "home-path-v2"`},
		"duplicate":    {"home-path-v1,home-path-v1", `lists rule "home-path-v1" twice`},
		"none-plus":    {"none,home-path-v1", `"none" stands alone`},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := acceptance.ParseRedaction(tc.value)
			require.ErrorContains(t, err, tc.want)
			p := rewritten
			p.Redaction = tc.value
			require.ErrorContains(t, p.ValidateFixtureBytes(committed), tc.want)
		})
	}
}

func validCaptureProvenance(body []byte) acceptance.CaptureProvenance {
	return acceptance.CaptureProvenance{
		Origin:         acceptance.OriginAuthenticCapture,
		Harness:        acceptance.HarnessClaudeCode,
		HarnessVersion: "2.1.261",
		CaptureSource:  "reviewed-test-evidence",
		RawFileDigest:  digest.FromBytes(body).String(),
		CapturedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Event:          "SessionStart",
		Redaction:      "none",
		Clearance:      acceptedClearancePath,
	}
}
