package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
)

type CaptureOrigin string

const (
	OriginAuthenticCapture CaptureOrigin = "authentic-capture"
	OriginPinnedContract   CaptureOrigin = "pinned-contract"
	OriginReviewFinding    CaptureOrigin = "review-finding"
	OriginAuthored         CaptureOrigin = "authored"
)

func (o CaptureOrigin) IsValid() bool {
	return o == OriginAuthenticCapture || o == OriginPinnedContract || o == OriginReviewFinding || o == OriginAuthored
}

func (o *CaptureOrigin) UnmarshalText(text []byte) error {
	value := CaptureOrigin(text)
	if !value.IsValid() {
		return fmt.Errorf("unknown capture origin %q", text)
	}
	*o = value
	return nil
}

type CaptureProvenance struct {
	Origin         CaptureOrigin
	Harness        HarnessKind
	HarnessVersion string
	CaptureSource  string
	RawFileDigest  string
	CapturedAt     string
}

func (p CaptureProvenance) ValidateFixture(root, fixture string) error {
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
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path, err := filepath.Abs(filepath.Join(cleanRoot, fixture))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(cleanRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("authentic capture fixture %q escapes corpus root %q", fixture, root)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read authentic capture fixture %q: %w", fixture, err)
	}
	if got := digest.FromBytes(body); got != want {
		return fmt.Errorf("authentic capture fixture %q digest is %s, want %s", fixture, got, want)
	}
	return nil
}
