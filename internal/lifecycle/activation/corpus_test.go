package activation_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestLoadCorpusRejectsVacuousCoverageWithExactBits(t *testing.T) {
	t.Parallel()
	_, err := activation.LoadCorpus(filepath.Join("testdata", "captures_vacuous.yaml"))
	var coverage *activation.CoverageError
	require.ErrorAs(t, err, &coverage)
	for _, bit := range []activation.MissingCoverage{activation.MissingCoverageMustFail, activation.MissingCoverageNonAuthenticOrigin, activation.MissingCoverageDigestMismatch, activation.MissingCoverageVersionOutOfRange, activation.MissingCoveragePathEscape} {
		require.True(t, coverage.MissingCoverage.Has(bit))
	}
	require.False(t, coverage.MissingCoverage.Has(activation.MissingCoverageMustPass))
}

func TestLoadCorpusStrictRejections(t *testing.T) {
	t.Parallel()
	valid := `cases:
- name: pass
  input: {fixture: f.json}
  expected: {decision: enabled, reason: ""}
  classification: must-pass
  provenance: {source: requirement, ref: ref}
  mutation: {description: pass}
- name: origin
  input: {fixture: f.json}
  expected: {decision: withheld, reason: non-authentic-origin}
  classification: must-fail
  provenance: {source: bug, ref: ref}
  mutation: {description: origin}
- name: digest
  input: {fixture: f.json}
  expected: {decision: withheld, reason: digest-mismatch}
  classification: must-fail
  provenance: {source: bug, ref: ref}
  mutation: {description: digest}
- name: version
  input: {fixture: f.json}
  expected: {decision: withheld, reason: version-out-of-range}
  classification: must-fail
  provenance: {source: enum, ref: ref}
  mutation: {description: version}
- name: escape
  input: {fixture: ../f.json}
  expected: {decision: withheld, reason: path-escape}
  classification: must-fail
  provenance: {source: boundary, ref: ref}
  mutation: {description: escape}
`
	cases := map[string]string{
		"unknown":      stringsReplace(valid, "cases:", "unknown: true\ncases:"),
		"duplicate":    stringsReplace(valid, "- name: origin", "- name: pass"),
		"alias":        "base: &x {fixture: f.json}\n" + stringsReplace(valid, "{fixture: f.json}", "*x"),
		"tag":          stringsReplace(valid, "name: pass", "name: !custom pass"),
		"trailing":     valid + "---\n{}\n",
		"missing":      stringsReplace(valid, "  mutation: {description: pass}\n", ""),
		"inconsistent": stringsReplace(valid, "classification: must-pass", "classification: must-fail"),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "corpus.yaml")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := activation.LoadCorpus(path)
			require.Error(t, err)
		})
	}
}

func TestLoadCorpusCompleteBoundaryMatrix(t *testing.T) {
	t.Parallel()
	valid := validCorpusYAML("fixtures/pass.json")
	duplicateMutations := map[string]string{
		"root":       "cases: []\n" + valid,
		"case":       strings.Replace(valid, "- name: pass", "- name: pass\n  name: shadow", 1),
		"input":      strings.Replace(valid, "input: {fixture: fixtures/pass.json}", "input: {fixture: fixtures/pass.json, fixture: shadow}", 1),
		"expected":   strings.Replace(valid, "expected: {decision: enabled, reason: \"\"}", "expected: {decision: enabled, decision: withheld, reason: \"\"}", 1),
		"provenance": strings.Replace(valid, "provenance: {source: requirement, ref: ref}", "provenance: {source: requirement, source: bug, ref: ref}", 1),
		"mutation":   strings.Replace(valid, "mutation: {description: pass}", "mutation: {description: pass, description: shadow}", 1),
	}
	for name, body := range duplicateMutations {
		name, body := name, body
		t.Run("duplicate-"+name, func(t *testing.T) {
			t.Parallel()
			_, err := loadCorpusText(t, body)
			require.ErrorContains(t, err, "duplicate YAML key")
			require.ErrorContains(t, err, "line")
		})
	}

	rejections := map[string]struct{ body, want string }{
		"missing-cases": {"{}\n", "require 1.."}, "empty-cases": {"cases: []\n", "require 1.."},
		"empty-name":                   {strings.Replace(valid, "name: pass", "name: \"\"", 1), "required field name is empty"},
		"empty-fixture":                {strings.Replace(valid, "fixtures/pass.json", "\"\"", 1), "required field input.fixture is empty"},
		"empty-ref":                    {strings.Replace(valid, "ref: ref", "ref: \"\"", 1), "required field provenance.ref is empty"},
		"empty-mutation":               {strings.Replace(valid, "description: pass", "description: \"\"", 1), "required field mutation.description is empty"},
		"missing-name":                 {strings.Replace(valid, "- name: pass\n", "-\n", 1), "missing required key name"},
		"missing-input":                {strings.Replace(valid, "  input: {fixture: fixtures/pass.json}\n", "", 1), "missing required key input"},
		"missing-input-fixture":        {strings.Replace(valid, "input: {fixture: fixtures/pass.json}", "input: {}", 1), "missing required key input.fixture"},
		"missing-expected":             {strings.Replace(valid, "  expected: {decision: enabled, reason: \"\"}\n", "", 1), "missing required key expected"},
		"missing-expected-decision":    {strings.Replace(valid, "expected: {decision: enabled, reason: \"\"}", "expected: {reason: \"\"}", 1), "missing required key expected.decision"},
		"missing-expected-reason":      {strings.Replace(valid, "expected: {decision: enabled, reason: \"\"}", "expected: {decision: enabled}", 1), "missing required key expected.reason"},
		"missing-classification":       {strings.Replace(valid, "  classification: must-pass\n", "", 1), "missing required key classification"},
		"missing-provenance":           {strings.Replace(valid, "  provenance: {source: requirement, ref: ref}\n", "", 1), "missing required key provenance"},
		"missing-provenance-source":    {strings.Replace(valid, "provenance: {source: requirement, ref: ref}", "provenance: {ref: ref}", 1), "missing required key provenance.source"},
		"missing-provenance-ref":       {strings.Replace(valid, "provenance: {source: requirement, ref: ref}", "provenance: {source: requirement}", 1), "missing required key provenance.ref"},
		"missing-mutation":             {strings.Replace(valid, "  mutation: {description: pass}\n", "", 1), "missing required key mutation"},
		"missing-mutation-description": {strings.Replace(valid, "mutation: {description: pass}", "mutation: {}", 1), "missing required key mutation.description"},
		"invalid-classification":       {strings.Replace(valid, "must-pass", "other", 1), "unknown classification \"other\"; use must-pass or must-fail"},
		"invalid-decision":             {strings.Replace(valid, "decision: enabled", "decision: other", 1), "unknown decision \"other\"; use enabled or withheld"},
		"invalid-reason":               {strings.Replace(valid, "reason: non-authentic-origin", "reason: other", 1), "unknown reason \"other\""},
		"invalid-source":               {strings.Replace(valid, "source: requirement", "source: other", 1), "unknown provenance source \"other\""},
		"enabled-with-reason":          {strings.Replace(valid, "decision: enabled, reason: \"\"", "decision: enabled, reason: digest-mismatch", 1), "violates classification/decision/reason combination"},
		"withheld-with-none":           {strings.Replace(valid, "decision: withheld, reason: non-authentic-origin", "decision: withheld, reason: \"\"", 1), "violates classification/decision/reason combination"},
		"must-pass-withheld":           {strings.Replace(valid, "decision: enabled", "decision: withheld", 1), "violates classification/decision/reason combination"},
		"must-fail-enabled":            {strings.Replace(valid, "decision: withheld", "decision: enabled", 1), "violates classification/decision/reason combination"},
		"duplicate-case-name":          {strings.Replace(valid, "name: origin", "name: pass", 1), "duplicate case name \"pass\""},
		"unknown-key":                  {strings.Replace(valid, "name: pass", "unknown: value\n  name: pass", 1), "field unknown not found"},
		"custom-tag":                   {strings.Replace(valid, "name: pass", "name: !custom pass", 1), "custom tag \"!custom\""},
		"alias":                        {"anchor: &x value\n" + strings.Replace(valid, "fixtures/pass.json", "*x", 1), "alias"},
		"trailing-document":            {valid + "---\n{}\n", "exactly one YAML document"},
	}
	for name, tc := range rejections {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := loadCorpusText(t, tc.body)
			require.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("corpus-byte-bound", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "large.yaml")
		require.NoError(t, os.WriteFile(path, []byte(strings.Repeat(" ", activation.MaxCorpusBytes+1)), 0o600))
		_, err := activation.LoadCorpus(path)
		require.ErrorContains(t, err, "exceeds")
	})
	t.Run("case-count-bound", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		b.WriteString("cases:\n")
		for i := 0; i < activation.MaxCorpusCases+1; i++ {
			fmt.Fprintf(&b, "- {name: n%d}\n", i)
		}
		_, err := loadCorpusText(t, b.String())
		require.ErrorContains(t, err, "require 1..")
	})
	for name, wantPath := range map[string]string{"name": "name", "fixture": "input.fixture", "ref": "provenance.ref", "mutation": "mutation.description"} {
		name, wantPath := name, wantPath
		t.Run("oversized-"+name, func(t *testing.T) {
			t.Parallel()
			body := valid
			switch name {
			case "name":
				body = strings.Replace(body, "name: pass", "name: "+strings.Repeat("x", activation.MaxFieldBytes+1), 1)
			case "fixture":
				body = strings.Replace(body, "fixtures/pass.json", strings.Repeat("x", activation.MaxFieldBytes+1), 1)
			case "ref":
				body = strings.Replace(body, "ref: ref", "ref: "+strings.Repeat("x", activation.MaxFieldBytes+1), 1)
			case "mutation":
				body = strings.Replace(body, "description: pass", "description: "+strings.Repeat("x", activation.MaxFieldBytes+1), 1)
			}
			_, err := loadCorpusText(t, body)
			require.ErrorContains(t, err, "required field "+wantPath+" exceeds")
		})
	}
	t.Run("escaping-metadata-accepted", func(t *testing.T) {
		t.Parallel()
		corpus, err := loadCorpusText(t, validCorpusYAML("../outside.json"))
		require.NoError(t, err)
		require.Len(t, corpus.Cases(), 5)
	})
}

func TestCaseCorpusAndEvaluationZeroAndDefensiveSurfaces(t *testing.T) {
	t.Parallel()
	var c activation.Case
	require.False(t, c.IsValid())
	require.Empty(t, c.Name())
	require.Zero(t, c.Classification())
	require.Zero(t, c.ExpectedDecision())
	require.Zero(t, c.ExpectedReason())
	var e activation.Evaluation
	require.False(t, e.IsValid())
	require.Empty(t, e.CaseName())
	require.Zero(t, e.Decision())
	require.Zero(t, e.Reason())
	event, ok := e.Event()
	require.False(t, ok)
	require.Zero(t, event)
}

func TestEvaluateRealAuthenticCaptureAndOracleIndependence(t *testing.T) {
	t.Parallel()
	root, corpus := copiedReviewedEvidenceCorpus(t)
	cases := corpus.Cases()
	require.Len(t, cases, 5)
	cases[0] = activation.Case{}
	require.True(t, corpus.Cases()[0].IsValid())
	got, err := activation.ClaudeCodeEvaluator().Evaluate(root, corpus.Cases()[0])
	require.NoError(t, err)
	require.True(t, got.IsValid())
	require.Equal(t, "pass", got.CaseName())
	require.Equal(t, activation.DecisionEnabled, got.Decision())
	event, ok := got.Event()
	require.True(t, ok)
	require.Equal(t, registration.EventSessionStart, event)
}

func TestEvaluateControlsFollowBindingPrecedence(t *testing.T) {
	t.Parallel()
	root, corpus := copiedReviewedEvidenceCorpus(t)
	for _, c := range corpus.Cases()[1:] {
		got, err := activation.ClaudeCodeEvaluator().Evaluate(root, c)
		require.NoError(t, err, c.Name())
		require.True(t, got.IsValid())
		require.Equal(t, c.ExpectedReason(), got.Reason())
		_, ok := got.Event()
		require.False(t, ok)
	}
	_, err := activation.ClaudeCodeEvaluator().Evaluate(root, activation.Case{})
	require.Error(t, err)
	var coverage *activation.CoverageError
	require.False(t, errors.As(err, &coverage))
}

func TestEvaluateContainmentPrecedenceAndEvidenceErrors(t *testing.T) {
	t.Parallel()
	t.Run("absolute-and-lexical-escape-before-read", func(t *testing.T) {
		t.Parallel()
		for _, fixture := range []string{filepath.Join(string(filepath.Separator), "missing.json"), "../missing.json"} {
			root := t.TempDir()
			corpus, err := loadCorpusTextAt(t, root, validCorpusYAML(fixture))
			require.NoError(t, err)
			got, err := activation.ClaudeCodeEvaluator().Evaluate(root, corpus.Cases()[0])
			require.NoError(t, err)
			require.Equal(t, activation.CorpusReasonPathEscape, got.Reason())
		}
	})
	t.Run("fixture-resolved-escape", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.json")
		require.NoError(t, os.WriteFile(outside, []byte("{}"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(root, "fixtures"), 0o700))
		if err := os.Symlink(outside, filepath.Join(root, "fixtures", "pass.json")); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		corpus, err := loadCorpusTextAt(t, root, validCorpusYAML("fixtures/pass.json"))
		require.NoError(t, err)
		got, err := activation.ClaudeCodeEvaluator().Evaluate(root, corpus.Cases()[0])
		require.NoError(t, err)
		require.Equal(t, activation.CorpusReasonPathEscape, got.Reason())
	})
	t.Run("provenance-resolved-escape", func(t *testing.T) {
		t.Parallel()
		root, corpus := copiedReviewedEvidenceCorpus(t)
		outside := filepath.Join(t.TempDir(), "outside.provenance.json")
		require.NoError(t, os.WriteFile(outside, []byte("{}"), 0o600))
		path := filepath.Join(root, "fixtures", "session_start_2_1_210.provenance.json")
		require.NoError(t, os.Remove(path))
		if err := os.Symlink(outside, path); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		got, err := activation.ClaudeCodeEvaluator().Evaluate(root, corpus.Cases()[0])
		require.NoError(t, err)
		require.Equal(t, activation.CorpusReasonPathEscape, got.Reason())
	})

	for name, tc := range map[string]struct {
		mutate     func(map[string]any)
		wantReason activation.CorpusReason
		wantError  string
	}{
		"origin-before-bad-digest": {func(p map[string]any) { p["origin"] = "authored"; p["rawFileDigest"] = "bad" }, activation.CorpusReasonNonAuthenticOrigin, ""},
		"digest-before-bad-version": {func(p map[string]any) {
			p["rawFileDigest"] = "sha256:" + strings.Repeat("0", 64)
			p["harnessVersion"] = "bad"
		}, activation.CorpusReasonDigestMismatch, ""},
		"harness-before-bad-version": {func(p map[string]any) { p["harnessVersion"] = "2.2.0"; p["harness"] = "codex-cli" }, 0, `names harness "codex-cli", not "claude-code"`},
		"malformed-digest":           {func(p map[string]any) { p["rawFileDigest"] = "bad" }, 0, "malformed SHA-256"},
		"malformed-version":          {func(p map[string]any) { p["harnessVersion"] = "bad" }, 0, "malformed host version"},
		"wrong-harness":              {func(p map[string]any) { p["harness"] = "codex-cli" }, 0, `not "claude-code"`},
		"empty-event":                {func(p map[string]any) { p["event"] = "" }, 0, "empty or unknown event"},
		"unknown-event":              {func(p map[string]any) { p["event"] = "Unknown" }, 0, "empty or unknown event"},
		"non-target-event":           {func(p map[string]any) { p["event"] = "Setup" }, 0, "outside the claude-code activation target set"},
		"final-timestamp-validation": {func(p map[string]any) { p["capturedAt"] = "not-time" }, 0, "final fixture validation failed"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, corpus := copiedReviewedEvidenceCorpus(t)
			rewriteProvenance(t, filepath.Join(root, "fixtures", "session_start_2_1_210.provenance.json"), tc.mutate)
			got, err := activation.ClaudeCodeEvaluator().Evaluate(root, corpus.Cases()[0])
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.False(t, got.IsValid())
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantReason, got.Reason())
			_, ok := got.Event()
			require.False(t, ok)
		})
	}

	for name, tc := range map[string]struct {
		action func(string)
		want   string
	}{
		"missing-fixture": {func(root string) {
			require.NoError(t, os.Remove(filepath.Join(root, "fixtures", "session_start_2_1_210.json")))
		}, "resolve fixture"},
		"missing-provenance": {func(root string) {
			require.NoError(t, os.Remove(filepath.Join(root, "fixtures", "session_start_2_1_210.provenance.json")))
		}, "resolve provenance"},
		"malformed-json": {func(root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "fixtures", "session_start_2_1_210.provenance.json"), []byte("{"), 0o600))
		}, "decode provenance"},
		"trailing-json": {func(root string) {
			p := filepath.Join(root, "fixtures", "session_start_2_1_210.provenance.json")
			body, err := os.ReadFile(p)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(p, append(body, []byte("{}")...), 0o600))
		}, "exactly one JSON object"},
		"oversized-json": {func(root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "fixtures", "session_start_2_1_210.provenance.json"), []byte(strings.Repeat(" ", activation.MaxProvenanceBytes+1)), 0o600))
		}, "exceeds"},
		"oversized-fixture": {func(root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "fixtures", "session_start_2_1_210.json"), []byte(strings.Repeat("x", activation.MaxFixtureBytes+1)), 0o600))
		}, "native payload bound"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, corpus := copiedReviewedEvidenceCorpus(t)
			tc.action(root)
			got, err := activation.ClaudeCodeEvaluator().Evaluate(root, corpus.Cases()[0])
			require.ErrorContains(t, err, tc.want)
			require.False(t, got.IsValid())
		})
	}
}

func TestEvaluateIgnoresCorpusOracleAndReviewMetadata(t *testing.T) {
	t.Parallel()
	root, _ := copiedReviewedEvidenceCorpus(t)
	variants := []string{
		strings.Replace(validCorpusYAML("fixtures/session_start_2_1_210.json"), "source: requirement, ref: ref", "source: bug, ref: contradictory-review", 1),
		strings.Replace(validCorpusYAML("fixtures/session_start_2_1_210.json"), "description: pass", "description: contradictory mutation prose", 1),
		strings.Replace(validCorpusYAML("fixtures/session_start_2_1_210_origin_authored.json"), "decision: enabled, reason: \"\"", "decision: enabled, reason: \"\"", 1),
	}
	for i, body := range variants {
		corpus, err := loadCorpusTextAt(t, root, body)
		require.NoError(t, err, "variant %d", i)
		got, err := activation.ClaudeCodeEvaluator().Evaluate(root, corpus.Cases()[0])
		require.NoError(t, err)
		if i < 2 {
			require.Equal(t, activation.DecisionEnabled, got.Decision())
			_, ok := got.Event()
			require.True(t, ok)
		} else {
			require.Equal(t, activation.CorpusReasonNonAuthenticOrigin, got.Reason())
			_, ok := got.Event()
			require.False(t, ok)
		}
	}
}

func copiedReviewedEvidenceCorpus(t *testing.T) (string, activation.Corpus) {
	t.Helper()
	root := t.TempDir()
	fixtureDir := filepath.Join(root, "fixtures")
	require.NoError(t, os.Mkdir(fixtureDir, 0o700))
	source := filepath.Join("..", "ingress", "claude", "testdata", "fixtures")
	names := []string{"session_start_2_1_210", "session_start_2_1_210_origin_authored", "session_start_2_1_210_digest_mismatch", "session_start_2_1_210_version_out_of_range"}
	for _, name := range names {
		for _, ext := range []string{".json", ".provenance.json"} {
			body, err := os.ReadFile(filepath.Join(source, name+ext))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(fixtureDir, name+ext), body, 0o600))
		}
		// A copy under a temp root is not at the committed path of the legacy
		// exemption, so it is a new capture and carries a clearance like one.
		rewriteProvenance(t, filepath.Join(fixtureDir, name+".provenance.json"), func(p map[string]any) {
			p["clearance"] = "internal/lifecycle/ingress/claude/testdata/CLEARANCE.md"
		})
	}
	corpusYAML := `cases:
- name: pass
  input: {fixture: fixtures/session_start_2_1_210.json}
  expected: {decision: enabled, reason: ""}
  classification: must-pass
  provenance: {source: requirement, ref: reviewed-capture}
  mutation: {description: copied reviewed capture bytes}
- name: origin
  input: {fixture: fixtures/session_start_2_1_210_origin_authored.json}
  expected: {decision: withheld, reason: non-authentic-origin}
  classification: must-fail
  provenance: {source: bug, ref: control}
  mutation: {description: non-authentic origin control}
- name: digest
  input: {fixture: fixtures/session_start_2_1_210_digest_mismatch.json}
  expected: {decision: withheld, reason: digest-mismatch}
  classification: must-fail
  provenance: {source: bug, ref: control}
  mutation: {description: digest control}
- name: version
  input: {fixture: fixtures/session_start_2_1_210_version_out_of_range.json}
  expected: {decision: withheld, reason: version-out-of-range}
  classification: must-fail
  provenance: {source: enum, ref: control}
  mutation: {description: version control}
- name: escape
  input: {fixture: ../outside.json}
  expected: {decision: withheld, reason: path-escape}
  classification: must-fail
  provenance: {source: boundary, ref: control}
  mutation: {description: lexical path escape control}
`
	path := filepath.Join(root, "captures.yaml")
	require.NoError(t, os.WriteFile(path, []byte(corpusYAML), 0o600))
	corpus, err := activation.LoadCorpus(path)
	require.NoError(t, err)
	return root, corpus
}

func stringsReplace(s, old, replacement string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + replacement + s[i+len(old):]
		}
	}
	return s
}

func loadCorpusText(t *testing.T, body string) (activation.Corpus, error) {
	t.Helper()
	return loadCorpusTextAt(t, t.TempDir(), body)
}
func loadCorpusTextAt(t *testing.T, root, body string) (activation.Corpus, error) {
	t.Helper()
	path := filepath.Join(root, "corpus.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return activation.LoadCorpus(path)
}

func rewriteProvenance(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(body, &value))
	mutate(value)
	body, err = json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

func validCorpusYAML(passFixture string) string {
	return fmt.Sprintf(`cases:
- name: pass
  input: {fixture: %s}
  expected: {decision: enabled, reason: ""}
  classification: must-pass
  provenance: {source: requirement, ref: ref}
  mutation: {description: pass}
- name: origin
  input: {fixture: fixtures/origin.json}
  expected: {decision: withheld, reason: non-authentic-origin}
  classification: must-fail
  provenance: {source: bug, ref: ref}
  mutation: {description: origin}
- name: digest
  input: {fixture: fixtures/digest.json}
  expected: {decision: withheld, reason: digest-mismatch}
  classification: must-fail
  provenance: {source: bug, ref: ref}
  mutation: {description: digest}
- name: version
  input: {fixture: fixtures/version.json}
  expected: {decision: withheld, reason: version-out-of-range}
  classification: must-fail
  provenance: {source: enum, ref: ref}
  mutation: {description: version}
- name: escape
  input: {fixture: ../outside.json}
  expected: {decision: withheld, reason: path-escape}
  classification: must-fail
  provenance: {source: boundary, ref: ref}
  mutation: {description: escape}
`, passFixture)
}

// TestClaudeEvaluatorDecidesTheCommittedClaudeCorpusAsBefore is the
// equivalence proof for the harness-parameterised evaluator: every row of the
// committed Claude corpus evaluates to the decision and reason the corpus
// expected before the evaluator existed, through the explicit Claude
// evaluator and through EvaluatorFor.
func TestClaudeEvaluatorDecidesTheCommittedClaudeCorpusAsBefore(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "ingress", "claude", "testdata")
	corpus, err := activation.LoadCorpus(filepath.Join(root, "captures.yaml"))
	require.NoError(t, err)
	cases := corpus.Cases()
	require.Len(t, cases, 12, "the committed Claude corpus has twelve rows")
	selected, err := activation.EvaluatorFor(acceptance.HarnessClaudeCode)
	require.NoError(t, err)
	enabled := 0
	for _, c := range cases {
		for name, evaluate := range map[string]func(string, activation.Case) (activation.Evaluation, error){
			"explicit": activation.ClaudeCodeEvaluator().Evaluate,
			"selected": selected.Evaluate,
		} {
			got, err := evaluate(root, c)
			require.NoError(t, err, "%s: %s", name, c.Name())
			require.True(t, got.IsValid(), "%s: %s", name, c.Name())
			require.Equal(t, c.ExpectedDecision(), got.Decision(), "%s: %s", name, c.Name())
			require.Equal(t, c.ExpectedReason(), got.Reason(), "%s: %s", name, c.Name())
		}
		if c.ExpectedDecision() == activation.DecisionEnabled {
			enabled++
		}
	}
	require.Equal(t, 8, enabled, "the eight enabled Claude targets evaluate as enabled")
	require.Equal(t, acceptance.HarnessClaudeCode, selected.Harness())
	require.Equal(t, activation.ClaudeCode2_1_210TargetEvents(), selected.TargetEvents())
}

// harnessSample is one harness's real committed capture bytes, re-homed in a
// temp corpus under a canonical sidecar, so that the Codex and OpenCode
// evaluators are exercised on authentic bytes although the committed sidecars
// of those harnesses predate the canonical shape.
type harnessSample struct {
	harness    acceptance.HarnessKind
	version    string
	event      string
	source     string
	fixture    string
	wantEvent  model.ContractEventKind
	evaluator  activation.Evaluator
	outOfRange string
}

func harnessSamples() []harnessSample {
	return []harnessSample{
		{acceptance.HarnessClaudeCode, "2.1.222", "SessionStart", filepath.Join("..", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_222.json"), "session_start.json", registration.EventSessionStart, activation.ClaudeCodeEvaluator(), "2.2.0"},
		{acceptance.HarnessCodexCLI, "0.146.0", "SessionStart", filepath.Join("..", "ingress", "codex", "testdata", "fixtures", "session_start_0_146_0.json"), "session_start.json", registration.EventCodexSessionStart, activation.CodexEvaluator(), "0.146.1"},
		{acceptance.HarnessOpenCode, "1.18.10", "session.created", filepath.Join("..", "ingress", "opencode", "testdata", "fixtures", "session_created_1_18_10.capture.json"), "session_created.capture.json", registration.EventOpenCodeSessionCreated, activation.OpenCodeEvaluator(), "1.18.11"},
	}
}

// canonicalCorpus copies the sample's committed bytes into a temp corpus root
// under fixtures/<fixture>, writes a canonical sidecar beside it, and loads a
// corpus whose first case is that fixture.
func canonicalCorpus(t *testing.T, sample harnessSample) (string, activation.Corpus) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "fixtures"), 0o700))
	body, err := os.ReadFile(sample.source)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "fixtures", sample.fixture), body, 0o600))
	sidecar := acceptance.CaptureProvenance{
		Origin:         acceptance.OriginAuthenticCapture,
		Harness:        sample.harness,
		HarnessVersion: sample.version,
		CaptureSource:  "reviewed-test-evidence",
		RawFileDigest:  digest.FromBytes(body).String(),
		CapturedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Event:          sample.event,
		Redaction:      "none",
		Clearance:      "internal/lifecycle/ingress/" + strings.TrimSuffix(string(sample.harness), "-cli") + "/testdata/CLEARANCE.md",
	}
	raw, err := json.Marshal(sidecar)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "fixtures", activation.ProvenancePath(sample.fixture)), raw, 0o600))
	corpus, err := loadCorpusTextAt(t, root, validCorpusYAML("fixtures/"+sample.fixture))
	require.NoError(t, err)
	return root, corpus
}

// TestEachHarnessEvaluatorResolvesAgainstItsOwnContract proves that the Codex
// and OpenCode evaluators resolve a fixture through THEIR contract, manifest
// and target set, and that a fixture offered to another harness's evaluator is
// refused naming both harnesses rather than evaluated against the wrong
// contract.
func TestEachHarnessEvaluatorResolvesAgainstItsOwnContract(t *testing.T) {
	t.Parallel()
	samples := harnessSamples()
	for _, sample := range samples {
		sample := sample
		t.Run(string(sample.harness), func(t *testing.T) {
			t.Parallel()
			root, corpus := canonicalCorpus(t, sample)
			got, err := sample.evaluator.Evaluate(root, corpus.Cases()[0])
			require.NoError(t, err)
			require.Equal(t, activation.DecisionEnabled, got.Decision())
			event, ok := got.Event()
			require.True(t, ok)
			require.Equal(t, sample.wantEvent, event, "the event resolves through this harness's own registration manifest")
			require.Equal(t, sample.harness, sample.evaluator.Harness())
			selected, err := activation.EvaluatorFor(sample.harness)
			require.NoError(t, err)
			require.Equal(t, sample.evaluator.Contract(), selected.Contract())
		})
	}
	for i, sample := range samples {
		foreign := samples[(i+1)%len(samples)]
		sample, foreign := sample, foreign
		t.Run(string(sample.harness)+"-fixture-offered-to-"+string(foreign.harness), func(t *testing.T) {
			t.Parallel()
			root, corpus := canonicalCorpus(t, sample)
			got, err := foreign.evaluator.Evaluate(root, corpus.Cases()[0])
			require.ErrorContains(t, err, fmt.Sprintf("names harness %q, not %q", sample.harness, foreign.harness))
			require.ErrorContains(t, err, fmt.Sprintf("evaluate the fixture with the %q evaluator", sample.harness))
			require.False(t, got.IsValid())
		})
	}
}

// TestEvaluatorRefusalsFireForEveryHarness runs the four withheld categories
// and the two hard refusals against each harness's own evaluator, on that
// harness's real bytes.
func TestEvaluatorRefusalsFireForEveryHarness(t *testing.T) {
	t.Parallel()
	for _, sample := range harnessSamples() {
		sample := sample
		t.Run(string(sample.harness), func(t *testing.T) {
			t.Parallel()
			for name, tc := range map[string]struct {
				mutate     func(map[string]any)
				wantReason activation.CorpusReason
				wantError  string
			}{
				"origin":        {func(p map[string]any) { p["origin"] = "authored" }, activation.CorpusReasonNonAuthenticOrigin, ""},
				"digest":        {func(p map[string]any) { p["rawFileDigest"] = "sha256:" + strings.Repeat("0", 64) }, activation.CorpusReasonDigestMismatch, ""},
				"version":       {func(p map[string]any) { p["harnessVersion"] = sample.outOfRange }, activation.CorpusReasonVersionOutOfRange, ""},
				"unknown-event": {func(p map[string]any) { p["event"] = "NoSuchEvent" }, 0, "empty or unknown event"},
			} {
				name, tc := name, tc
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					root, corpus := canonicalCorpus(t, sample)
					rewriteProvenance(t, filepath.Join(root, "fixtures", activation.ProvenancePath(sample.fixture)), tc.mutate)
					got, err := sample.evaluator.Evaluate(root, corpus.Cases()[0])
					if tc.wantError != "" {
						require.ErrorContains(t, err, tc.wantError)
						require.False(t, got.IsValid())
						return
					}
					require.NoError(t, err)
					require.Equal(t, activation.DecisionWithheld, got.Decision())
					require.Equal(t, tc.wantReason, got.Reason())
				})
			}
			t.Run("path-escape", func(t *testing.T) {
				t.Parallel()
				root, corpus := canonicalCorpus(t, sample)
				got, err := sample.evaluator.Evaluate(root, corpus.Cases()[4])
				require.NoError(t, err)
				require.Equal(t, activation.CorpusReasonPathEscape, got.Reason())
			})
			t.Run("no-clearance", func(t *testing.T) {
				// The committed Claude bytes are on the legacy exemption list, so
				// the clearance rule is exercised on bytes that are not: the
				// same capture with one byte appended, digest recomputed.
				t.Parallel()
				root, corpus := canonicalCorpus(t, sample)
				fixture := filepath.Join(root, "fixtures", sample.fixture)
				body, err := os.ReadFile(fixture)
				require.NoError(t, err)
				body = append(body, '\n')
				require.NoError(t, os.WriteFile(fixture, body, 0o600))
				rewriteProvenance(t, filepath.Join(root, "fixtures", activation.ProvenancePath(sample.fixture)), func(p map[string]any) {
					p["rawFileDigest"] = digest.FromBytes(body).String()
					p["clearance"] = ""
				})
				got, err := sample.evaluator.Evaluate(root, corpus.Cases()[0])
				require.ErrorContains(t, err, "final fixture validation failed")
				require.ErrorContains(t, err, "clearance is empty")
				require.False(t, got.IsValid())
			})
		})
	}
}

func TestProvenancePathSitsBesideTheFixtureUnderThePlainName(t *testing.T) {
	t.Parallel()
	require.Equal(t, "fixtures/session_created_1_18_10.provenance.json", activation.ProvenancePath("fixtures/session_created_1_18_10.capture.json"))
	require.Equal(t, "fixtures/session_start_2_1_222.provenance.json", activation.ProvenancePath("fixtures/session_start_2_1_222.json"))
}

func TestEvaluatorForIsClosedAndTheZeroEvaluatorRefuses(t *testing.T) {
	t.Parallel()
	for _, harness := range []acceptance.HarnessKind{acceptance.HarnessAntigravity, acceptance.HarnessKind("gemini"), ""} {
		_, err := activation.EvaluatorFor(harness)
		require.ErrorContains(t, err, "has no activation evaluator")
	}
	require.False(t, (activation.Evaluator{}).IsValid())
	root, corpus := copiedReviewedEvidenceCorpus(t)
	_, err := (activation.Evaluator{}).Evaluate(root, corpus.Cases()[0])
	require.ErrorContains(t, err, "evaluator is not constructed")
}

// TestVersionAdmissionFollowsEachHarnessContract states the admission shape
// per harness and proves it on fixtures: Claude Code admits a RANGE, so a
// patch release inside it (the frozen 2.1.251 pin among them) is admitted;
// Codex and OpenCode admit EXACTLY their pinned version. A version outside
// admission is withheld with a detail that names the observed and the
// admitted versions, because a reader who cannot see both cannot act.
func TestVersionAdmissionFollowsEachHarnessContract(t *testing.T) {
	t.Parallel()
	require.False(t, activation.ClaudeCodeEvaluator().AdmitsExactly(), "Claude Code admits a range")
	require.True(t, activation.CodexEvaluator().AdmitsExactly(), "Codex admits exactly its pinned version")
	require.True(t, activation.OpenCodeEvaluator().AdmitsExactly(), "OpenCode admits exactly its pinned version")

	for _, tc := range []struct {
		sample   harnessSample
		version  string
		admitted bool
		detail   string
	}{
		{harnessSamples()[0], "2.1.222", true, ""},
		{harnessSamples()[0], "2.1.223", true, ""},
		{harnessSamples()[0], "2.1.251", true, ""},
		{harnessSamples()[0], "2.2.0", false, `observed host version "2.2.0" is outside the admitted claude-code versions, from 2.1.210 through 2.2.0-0`},
		{harnessSamples()[0], "2.1.209", false, `observed host version "2.1.209" is outside the admitted claude-code versions, from 2.1.210 through 2.2.0-0`},
		{harnessSamples()[1], "0.146.0", true, ""},
		{harnessSamples()[1], "0.146.1", false, `observed host version "0.146.1" is outside the admitted codex-cli versions, exactly 0.146.0`},
		{harnessSamples()[2], "1.18.10", true, ""},
		{harnessSamples()[2], "1.18.11", false, `observed host version "1.18.11" is outside the admitted opencode versions, exactly 1.18.10`},
	} {
		tc := tc
		t.Run(string(tc.sample.harness)+"-"+tc.version, func(t *testing.T) {
			t.Parallel()
			root, corpus := canonicalCorpus(t, tc.sample)
			rewriteProvenance(t, filepath.Join(root, "fixtures", activation.ProvenancePath(tc.sample.fixture)), func(p map[string]any) { p["harnessVersion"] = tc.version })
			got, err := tc.sample.evaluator.Evaluate(root, corpus.Cases()[0])
			require.NoError(t, err)
			if tc.admitted {
				require.Equal(t, activation.DecisionEnabled, got.Decision(), "version %s must be admitted", tc.version)
				require.Empty(t, got.Detail())
				return
			}
			require.Equal(t, activation.CorpusReasonVersionOutOfRange, got.Reason())
			require.Equal(t, tc.detail, got.Detail())
		})
	}
}
