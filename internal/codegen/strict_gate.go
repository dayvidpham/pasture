package codegen

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
)

// RequireClassifiedSource is the permanent strict rejection gate: it runs the
// canonical read-only scanner over the checked-in skills/ and agents/ source
// tree and fails closed unless every discovered harness-syntax candidate
// (TeamCreate/SendMessage/Skill/AskUserQuestion) carries an explicit
// classification in the checked-in classification manifest. It is the
// production wiring of scan.ScanCanonical + scan.RequireZeroUnclassified into
// code generation.
//
// The gate exists so a newly introduced, still-unreviewed harness-syntax
// candidate can never be silently emitted: it must be classified (migrated to
// a typed disposition, or explicitly retained as portable_verbatim /
// target_literal / neutral_false_positive, or its owner marked dead) before
// generation may proceed. Generate calls this before it writes anything, so a
// gate failure aborts generation with no partial or inconsistent output.
//
// root is the pasture module root (the directory containing skills/ and
// agents/). On any scan or classification failure the returned error carries
// the full underlying six-part diagnostic (which candidate, in which owner,
// at which section/range, and exactly how to classify it) so a maintainer can
// resolve it without re-deriving the scan.
func RequireClassifiedSource(root string) error {
	inventory, err := scan.ScanCanonical(root)
	if err != nil {
		return fmt.Errorf(
			"pasture#42 strict source-migration gate could not scan the canonical source tree "+
				"under %q, so generation was aborted with no partial output: %w",
			root, err,
		)
	}
	return requireZeroUnclassified(inventory)
}

// RequireClassifiedSourceWithManifests is the manifest-injectable core of
// RequireClassifiedSource. Production callers use RequireClassifiedSource,
// which supplies this repository's real embedded manifests and canonical
// roots; tests drive this variant against a synthetic source tree to exercise
// the gate against a deliberately unclassified candidate without depending on
// the checked-in canonical manifests staying at zero unclassified.
func RequireClassifiedSourceWithManifests(
	baseDir string,
	roots []string,
	owners scan.OwnerManifest,
	classifications scan.ClassificationManifest,
) error {
	inventory, err := scan.ScanWithManifests(baseDir, roots, owners, classifications)
	if err != nil {
		return fmt.Errorf(
			"pasture#42 strict source-migration gate could not scan the source tree "+
				"under %q, so generation was aborted with no partial output: %w",
			baseDir, err,
		)
	}
	return requireZeroUnclassified(inventory)
}

// requireZeroUnclassified applies the strict rejection check to an already
// scanned inventory. A nonzero unclassified count is a hard, fail-closed
// prerequisite failure: the wrapped diagnostic already names every
// unclassified candidate's owner/file/section/range and the exact fix, and
// the wrapper records that generation was aborted with no partial output.
func requireZeroUnclassified(inventory scan.Inventory) error {
	if err := scan.RequireZeroUnclassified(inventory); err != nil {
		return fmt.Errorf(
			"pasture#42 strict source-migration gate rejected the source tree and aborted "+
				"generation with no partial output; %d of %d candidate(s) are unclassified and "+
				"must be classified before generation may proceed: %w",
			inventory.UnclassifiedCount(), inventory.Len(), err,
		)
	}
	return nil
}
