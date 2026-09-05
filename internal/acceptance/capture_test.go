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

// legacyClaudeCorpusRoot is the committed Claude ingress corpus root, relative
// to this package's directory.
const legacyClaudeCorpusRoot = ingressTestdataRoot + "/claude/testdata"

// TestCaptureProvenanceExemptionIsEnumeratedNotVersioned asserts the exemption
// boundary directly. A listed legacy capture, at its committed path with its
// committed bytes, validates without an event, a redaction record or a
// clearance path. Everything else needs all three: the same bytes at an
// unlisted path, new bytes at a frozen host pin, and new bytes at an older
// pin alike. The exemption is keyed on what and where the capture IS, never
// on how old its host was.
func TestCaptureProvenanceExemptionIsEnumeratedNotVersioned(t *testing.T) {
	t.Parallel()
	const legacyFixture = "fixtures/session_start_2_1_222.json"
	legacyAbs, err := filepath.Abs(filepath.Join(legacyClaudeCorpusRoot, legacyFixture))
	require.NoError(t, err)
	legacy, err := os.ReadFile(legacyAbs)
	require.NoError(t, err)
	require.True(t, acceptance.IsLegacyExemptCapture(legacyAbs, digest.FromBytes(legacy)), "the committed 2.1.222 session start must be on the legacy list for this test to mean anything")
	fresh := []byte(`{"hook_event_name":"SessionStart","cwd":"/home/user/project","session_id":"fresh-bytes-not-on-the-legacy-list"}`)
	require.False(t, acceptance.IsLegacyExemptCapture(legacyAbs, digest.FromBytes(fresh)), "new bytes at a listed path are not exempt")

	bare := func(body []byte, version string) acceptance.CaptureProvenance {
		p := validCaptureProvenance(body)
		p.HarnessVersion = version
		p.Event, p.Redaction, p.Clearance = "", "", ""
		return p
	}

	t.Run("listed-legacy-capture-at-its-committed-path-needs-no-clearance", func(t *testing.T) {
		t.Parallel()
		var committed acceptance.CaptureProvenance
		raw, err := os.ReadFile(strings.TrimSuffix(legacyAbs, ".json") + ".provenance.json")
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &committed))
		require.Empty(t, committed.Clearance, "the committed legacy sidecar carries no clearance; that is what the exemption exists for")
		require.NoError(t, committed.ValidateFixture(legacyClaudeCorpusRoot, legacyFixture))
		require.NoError(t, bare(legacy, "2.1.222").ValidateCommittedFixtureBytes(legacyAbs, legacy))
	})
	t.Run("listed-bytes-at-an-unlisted-path-need-clearance", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		copied := filepath.Join(root, "fixtures", "session_start_copy.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(copied), 0o700))
		require.NoError(t, os.WriteFile(copied, legacy, 0o600))
		p := bare(legacy, "2.1.222")
		p.Event, p.Redaction = "SessionStart", "home-path-v1"
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(copied, legacy), "clearance is empty")
		require.ErrorContains(t, p.ValidateFixture(root, "fixtures/session_start_copy.json"), "clearance is empty")
	})
	t.Run("bytes-alone-never-earn-the-exemption", func(t *testing.T) {
		t.Parallel()
		require.ErrorContains(t, bare(legacy, "2.1.222").ValidateFixtureBytes(legacy), "event is empty")
	})
	t.Run("unlisted-bytes-at-a-frozen-pin-need-clearance", func(t *testing.T) {
		t.Parallel()
		p := bare(fresh, "2.1.251")
		p.Event, p.Redaction = "SessionStart", "none"
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(legacyAbs, fresh), "clearance is empty")
	})
	t.Run("unlisted-bytes-at-an-old-pin-need-clearance", func(t *testing.T) {
		t.Parallel()
		p := bare(fresh, "2.1.210")
		p.Event, p.Redaction = "SessionStart", "none"
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(legacyAbs, fresh), "clearance is empty")
	})
	t.Run("unlisted-bytes-need-event-and-redaction", func(t *testing.T) {
		t.Parallel()
		p := bare(fresh, "2.1.251")
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(legacyAbs, fresh), "event is empty")
		p.Event = "SessionStart"
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(legacyAbs, fresh), "redaction is empty")
		p.Redaction = "none"
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(legacyAbs, fresh), "clearance is empty")
		p.Clearance = acceptedClearancePath
		require.NoError(t, p.ValidateCommittedFixtureBytes(legacyAbs, fresh))
	})
	t.Run("legacy-bytes-still-fail-the-digest-check", func(t *testing.T) {
		t.Parallel()
		p := bare(legacy, "2.1.222")
		require.ErrorContains(t, p.ValidateCommittedFixtureBytes(legacyAbs, append([]byte(nil), append(legacy, '\n')...)), "digest is")
	})
}

// TestLegacyExemptionListEqualsCommittedSidecarsWithoutClearance holds the
// enumerated exemption equal, in both directions, to the population it exists
// for: every committed authentic sidecar in the ingress corpora whose declared
// rawFileDigest matches its fixture bytes and that carries NO clearance key,
// keyed on the committed path and the digest. The control is that the list is
// non-empty and every entry resolves to such a sidecar: an entry with no
// committed sidecar behind it, a wrong digest, or a wrong path is an error.
// The next pin bump deletes the legacy fixtures and this list together and
// replaces this control with the assertion that an exempt fixture no longer
// exists.
//
// WHAT IT READS: every *.provenance.json under internal/lifecycle/ingress/*/
// testdata/fixtures, decoded as a JSON object. WHAT IT DOES NOT READ: a
// sidecar without a rawFileDigest key (the provider-shaped Codex and OpenCode
// sidecars of the earlier captures, which no code path decodes as a
// CaptureProvenance); a sidecar whose declared digest does not match its
// fixture bytes (the validator refuses it before the exemption is consulted);
// a sidecar whose origin is not authentic-capture.
func TestLegacyExemptionListEqualsCommittedSidecarsWithoutClearance(t *testing.T) {
	t.Parallel()
	sidecars, err := filepath.Glob(filepath.Join(ingressTestdataRoot, "*", "testdata", "fixtures", "*.provenance.json"))
	require.NoError(t, err)
	require.NotEmpty(t, sidecars, "the walk read no sidecar at all under %s; the ingress corpora must still exist for this guard to assert anything", ingressTestdataRoot)

	population := map[string]digest.Digest{}
	claudeShaped, distinct := 0, map[digest.Digest]struct{}{}
	for _, sidecar := range sidecars {
		raw, err := os.ReadFile(sidecar)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields), "sidecar %s is not a JSON object", sidecar)
		var origin string
		require.NoError(t, json.Unmarshal(fields["origin"], &origin), "sidecar %s has no readable origin", sidecar)
		declaredRaw, hasDigest := fields["rawFileDigest"]
		_, hasClearance := fields["clearance"]
		if !hasDigest {
			continue
		}
		claudeShaped++
		if origin != string(acceptance.OriginAuthenticCapture) || hasClearance {
			continue
		}
		var declared string
		require.NoError(t, json.Unmarshal(declaredRaw, &declared))
		fixture := strings.TrimSuffix(sidecar, ".provenance.json") + ".json"
		body, err := os.ReadFile(fixture)
		require.NoError(t, err, "sidecar %s has no sibling fixture", sidecar)
		actual := digest.FromBytes(body)
		if actual.String() != declared {
			continue
		}
		rel, err := filepath.Rel(filepath.Join(ingressTestdataRoot, "..", ".."), fixture)
		require.NoError(t, err)
		population[filepath.ToSlash(filepath.Join("internal", rel))] = actual
		distinct[actual] = struct{}{}
	}
	t.Logf("populations: %d CaptureProvenance-shaped sidecars; %d reach the exemption; %d distinct payloads among them", claudeShaped, len(population), len(distinct))

	listed := acceptance.LegacyExemptCaptures()
	require.NotEmpty(t, listed, "the legacy exemption list is empty while committed sidecars without a clearance still exist: %v", population)
	listedByPath := map[string]digest.Digest{}
	for _, entry := range listed {
		_, duplicate := listedByPath[entry.Path]
		require.False(t, duplicate, "legacy exemption list names %s twice", entry.Path)
		listedByPath[entry.Path] = entry.Digest
		got, present := population[entry.Path]
		require.True(t, present, "legacy exemption list names %s, but no committed authentic sidecar without a clearance sits at that path with bytes matching its declared digest; remove the entry", entry.Path)
		require.Equal(t, got, entry.Digest, "legacy exemption list records the wrong digest for %s", entry.Path)
		abs, err := filepath.Abs(filepath.Join(ingressTestdataRoot, "..", "..", strings.TrimPrefix(entry.Path, "internal/")))
		require.NoError(t, err)
		require.True(t, acceptance.IsLegacyExemptCapture(abs, entry.Digest), "entry %s does not resolve through IsLegacyExemptCapture at its own absolute path", entry.Path)
	}
	for path, d := range population {
		require.Contains(t, listedByPath, path, "committed sidecar for %s has no clearance and is not on the legacy exemption list; either add a clearance path to the sidecar or, if it predates the clearance field, add its path and digest %s to the list", path, d)
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
		HarnessVersion: "2.1.210",
		CaptureSource:  "reviewed-test-evidence",
		RawFileDigest:  digest.FromBytes(body).String(),
		CapturedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Event:          "SessionStart",
		Redaction:      "none",
		Clearance:      acceptedClearancePath,
	}
}
