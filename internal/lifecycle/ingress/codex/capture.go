// Package codex captures exact command-hook stdin bytes from the pinned Codex
// 0.146.0 CLI lifecycle contract. Unlike the OpenCode ingress, the captured
// bytes ARE the native command-hook stdin payload; there is no in-process
// callback object and no nested record envelope to unwrap.
package codex

import (
	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// Capture is the disposition and durable delivery derived from one exact Codex
// command-hook stdin payload.
type Capture struct {
	Digest      digest.Digest
	Disposition model.CaptureDisposition
	Delivery    receipt.Delivery
}

// Parse captures the exact command-hook stdin bytes for a selected Codex event
// and extracts the provider-specific native correlation identities.
//
// L1 skeleton: the body is implemented in L3 (M3-SLICE-2-L3). It returns a
// malformed disposition so the L2 production-path tests fail until the real
// implementation lands.
func Parse(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) Capture {
	_ = event
	_ = observedVersion
	_ = envelope
	return Capture{Digest: digest.FromBytes(raw), Disposition: model.CaptureMalformed}
}
