package activation_test

import (
	"errors"
	"os"
	"path/filepath"
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
