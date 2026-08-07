// Package codebook is the generated, content-addressed lifecycle interpretation
// vocabulary (D1). One document describes the FULL pinned per-harness event
// catalogs (Claude 30, OpenCode 47, Codex 10) — for each event: native name,
// semantic, blocking, mutation, failure, stop-loop, and declared identity kinds.
//
// The codebook describes the interpretation FUNCTION, which is defined over the
// whole contract; it is descriptive vocabulary, NOT event enablement. Activation
// (unchanged) governs admission separately. Describing a withheld event here
// never admits it.
//
// The canonical body is generated from the pinned runtime lifecycle profiles
// (the interpretation truth the waist executes) into metamodel.gen.go by the
// go:generate walk below, and its sha256 is the content identity every
// interpreted.v2 record cites. Because the body derives only from static pinned
// profiles, regeneration is deterministic (make generate twice is zero-diff).
package metamodel

//go:generate go run gen.go

import (
	"crypto/sha256"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

const (
	// LifecycleMetamodelID is the stable definition identity of the codebook document. It
	// does not change across versions.
	LifecycleMetamodelID model.DefinitionID = "pasture.lifecycle.codebook"
	// LifecycleMetamodelVersion is the codebook document version. It is 1 at M5 and bumps
	// only when the canonical body content changes.
	LifecycleMetamodelVersion uint32 = 1
)

// Active returns the compile-time active codebook coordinate derived from the
// generated canonical body. Its content identity is the sha256 of Body(), so a
// coordinate can never cite a body the running binary does not carry.
func Active() model.LifecycleMetamodelManifest {
	return model.LifecycleMetamodelManifest{
		ID:      LifecycleMetamodelID,
		Version: LifecycleMetamodelVersion,
		Content: activeContent(),
	}
}

// Body returns a defensive copy of the canonical generated codebook body — the
// canonical JSON evidence snapshot the definition journal stores.
func Body() []byte {
	return append([]byte(nil), lifecycleMetamodel...)
}

func activeContent() model.ContentIdentity {
	return model.ContentIdentity(sha256.Sum256(lifecycleMetamodel))
}
