package acceptance

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
)

// MaxCaptureFixtureBytes bounds one native capture payload during validation.
const MaxCaptureFixtureBytes = 1 << 20

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
	file, err := os.Open(path)
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
	if err := p.ValidateFixtureBytes(body); err != nil {
		return fmt.Errorf("validate authentic capture fixture %q: %w", fixture, err)
	}
	return nil
}

// ValidateFixtureBytes is the single metadata and digest authority for already
// bounded capture bytes. Non-authentic provenance remains non-normative.
func (p CaptureProvenance) ValidateFixtureBytes(body []byte) error {
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
	if got := digest.FromBytes(body); got != want {
		return fmt.Errorf("authentic capture bytes digest is %s, want %s", got, want)
	}
	return nil
}
