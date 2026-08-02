package activation_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
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

	rejections := map[string]string{
		"missing-cases": "{}\n", "empty-cases": "cases: []\n",
		"empty-name":             strings.Replace(valid, "name: pass", "name: \"\"", 1),
		"empty-fixture":          strings.Replace(valid, "fixtures/pass.json", "\"\"", 1),
		"empty-ref":              strings.Replace(valid, "ref: ref", "ref: \"\"", 1),
		"empty-mutation":         strings.Replace(valid, "description: pass", "description: \"\"", 1),
		"missing-input":          strings.Replace(valid, "  input: {fixture: fixtures/pass.json}\n", "", 1),
		"missing-expected":       strings.Replace(valid, "  expected: {decision: enabled, reason: \"\"}\n", "", 1),
		"missing-provenance":     strings.Replace(valid, "  provenance: {source: requirement, ref: ref}\n", "", 1),
		"missing-mutation":       strings.Replace(valid, "  mutation: {description: pass}\n", "", 1),
		"invalid-classification": strings.Replace(valid, "must-pass", "other", 1),
		"invalid-decision":       strings.Replace(valid, "decision: enabled", "decision: other", 1),
		"invalid-reason":         strings.Replace(valid, "reason: non-authentic-origin", "reason: other", 1),
		"invalid-source":         strings.Replace(valid, "source: requirement", "source: other", 1),
		"enabled-with-reason":    strings.Replace(valid, "decision: enabled, reason: \"\"", "decision: enabled, reason: digest-mismatch", 1),
		"withheld-with-none":     strings.Replace(valid, "decision: withheld, reason: non-authentic-origin", "decision: withheld, reason: \"\"", 1),
		"must-pass-withheld":     strings.Replace(valid, "decision: enabled", "decision: withheld", 1),
		"must-fail-enabled":      strings.Replace(valid, "decision: withheld", "decision: enabled", 1),
		"duplicate-case-name":    strings.Replace(valid, "name: origin", "name: pass", 1),
		"unknown-key":            strings.Replace(valid, "name: pass", "unknown: true\n  name: pass", 1),
		"custom-tag":             strings.Replace(valid, "name: pass", "name: !custom pass", 1),
		"alias":                  "anchor: &x value\n" + strings.Replace(valid, "fixtures/pass.json", "*x", 1),
		"trailing-document":      valid + "---\n{}\n",
	}
	for name, body := range rejections {
		name, body := name, body
		t.Run(name, func(t *testing.T) { t.Parallel(); _, err := loadCorpusText(t, body); require.Error(t, err) })
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
	t.Run("required-string-bound", func(t *testing.T) {
		t.Parallel()
		_, err := loadCorpusText(t, strings.Replace(valid, "name: pass", "name: "+strings.Repeat("x", activation.MaxFieldBytes+1), 1))
		require.ErrorContains(t, err, "oversized")
	})
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
	got, err := activation.Evaluate(root, corpus.Cases()[0])
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
		got, err := activation.Evaluate(root, c)
		require.NoError(t, err, c.Name())
		require.True(t, got.IsValid())
		require.Equal(t, c.ExpectedReason(), got.Reason())
		_, ok := got.Event()
		require.False(t, ok)
	}
	_, err := activation.Evaluate(root, activation.Case{})
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
			got, err := activation.Evaluate(root, corpus.Cases()[0])
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
		got, err := activation.Evaluate(root, corpus.Cases()[0])
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
		got, err := activation.Evaluate(root, corpus.Cases()[0])
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
		"version-before-wrong-harness": {func(p map[string]any) { p["harnessVersion"] = "2.2.0"; p["harness"] = "codex-cli" }, activation.CorpusReasonVersionOutOfRange, ""},
		"malformed-digest":             {func(p map[string]any) { p["rawFileDigest"] = "bad" }, 0, "malformed SHA-256"},
		"malformed-version":            {func(p map[string]any) { p["harnessVersion"] = "bad" }, 0, "malformed host version"},
		"wrong-harness":                {func(p map[string]any) { p["harness"] = "codex-cli" }, 0, "not claude-code"},
		"empty-event":                  {func(p map[string]any) { p["event"] = "" }, 0, "empty or unknown event"},
		"unknown-event":                {func(p map[string]any) { p["event"] = "Unknown" }, 0, "empty or unknown event"},
		"non-target-event":             {func(p map[string]any) { p["event"] = "Setup" }, 0, "outside the activation target"},
		"final-timestamp-validation":   {func(p map[string]any) { p["capturedAt"] = "not-time" }, 0, "final fixture validation failed"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, corpus := copiedReviewedEvidenceCorpus(t)
			rewriteProvenance(t, filepath.Join(root, "fixtures", "session_start_2_1_210.provenance.json"), tc.mutate)
			got, err := activation.Evaluate(root, corpus.Cases()[0])
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
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, corpus := copiedReviewedEvidenceCorpus(t)
			tc.action(root)
			got, err := activation.Evaluate(root, corpus.Cases()[0])
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
		got, err := activation.Evaluate(root, corpus.Cases()[0])
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
