package acceptance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/acceptance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

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

func validCaptureProvenance(body []byte) acceptance.CaptureProvenance {
	return acceptance.CaptureProvenance{Origin: acceptance.OriginAuthenticCapture, Harness: acceptance.HarnessClaudeCode, HarnessVersion: "2.1.210", CaptureSource: "reviewed-test-evidence", RawFileDigest: digest.FromBytes(body).String(), CapturedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)}
}
