package model

// LifecycleMetamodelManifest is the journal-independent versioned interpretation
// identity embedded in interpreted evidence payloads (D2). It names WHICH
// interpretation vocabulary (the metamodel document) an interpreted record was
// produced against, so a reader can resolve the record's meaning to a journaled
// definition rather than to whatever the current binary happens to believe.
//
// It embeds nonJournalValue: a coordinate is a value stamped INTO durable
// evidence, never itself a journal row, so it is mechanically non-journal and
// carries no guard classification entry.
type LifecycleMetamodelManifest struct {
	nonJournalValue
	// ID is the stable definition identity of the metamodel document
	// ("pasture.lifecycle.metamodel"). It does not change across versions.
	ID DefinitionID
	// Version is the metamodel document version. It is 1 at M5 and bumps only
	// when the canonical body content changes.
	Version uint32
	// Content is the sha256 over the canonical metamodel body — the
	// content-address that makes the coordinate self-verifying and lets
	// concurrent first deliveries collapse to one deterministic activation.
	Content ContentIdentity
}

// IsValid reports whether the coordinate names a nonzero definition id,
// version, and content identity. A zero coordinate can never be stamped onto an
// interpreted record or presented to the write gate.
func (c LifecycleMetamodelManifest) IsValid() bool {
	return c.ID != "" && c.Version != 0 && c.Content != (ContentIdentity{})
}
