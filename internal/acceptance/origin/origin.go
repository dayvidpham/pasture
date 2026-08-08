// Package origin defines the closed capture-origin provenance enum shared by
// the acceptance corpus (re-exported through package acceptance) and the
// lifecycle receipt/envelope origin carriers.
//
// The definition lives in this leaf package because the acceptance corpus
// package imports internal/tasks, which imports the lifecycle receipt and model
// packages: a lifecycle package importing acceptance directly would close an
// import cycle. Lifecycle consumers import this package, and acceptance
// re-exports the same type and values under its historical names, keeping one
// IsValid/UnmarshalText source of truth.
package origin

import "fmt"

// CaptureOrigin is the closed set of capture provenance origins recorded on
// durable lifecycle evidence. Values are classified by normativity: only
// OriginAuthenticCapture is normative (its provenance is verified against a
// reviewed corpus fixture); every other value is non-normative and bypasses
// fixture verification (see acceptance.CaptureProvenance.ValidateFixtureBytes).
type CaptureOrigin string

const (
	OriginAuthenticCapture CaptureOrigin = "authentic-capture"
	OriginPinnedContract   CaptureOrigin = "pinned-contract"
	OriginReviewFinding    CaptureOrigin = "review-finding"
	OriginAuthored         CaptureOrigin = "authored"
	// OriginRaw marks raw ingestion (M4): imports and migration pass through a
	// typed, versioned raw decoder instead of a native harness capture. Like
	// authored, it is non-normative and never the default path.
	OriginRaw CaptureOrigin = "raw"
)

func (o CaptureOrigin) IsValid() bool {
	return o == OriginAuthenticCapture || o == OriginPinnedContract || o == OriginReviewFinding || o == OriginAuthored || o == OriginRaw
}

func (o *CaptureOrigin) UnmarshalText(text []byte) error {
	value := CaptureOrigin(text)
	if !value.IsValid() {
		return fmt.Errorf("unknown capture origin %q", text)
	}
	*o = value
	return nil
}
