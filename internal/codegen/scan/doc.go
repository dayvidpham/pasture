// Package scan implements Pasture's read-only Goldmark inventory and
// classification of native harness syntax across canonical Markdown source
// roots (github.com/dayvidpham/pasture issue #47).
//
// The scanner never rewrites source and never infers semantics. It:
//
//  1. independently walks a code-owned closed list of canonical source roots
//     (see CanonicalRoots), rejecting symlinked owners and applying an
//     explicit, closed exclusion list for generated/vendor/test-fixture
//     content (see isExcludedPath and excludedPathSegments);
//  2. reconciles that independently discovered file set against a checked-in
//     owner manifest that records an explicit active/dead disposition for
//     every owner (see OwnerManifest) — the manifest is reconciliation input,
//     never the discovery source, so an unlisted active file, a stale
//     manifest entry, or a missing disposition is a hard scan failure;
//  3. parses every active owner through the same Goldmark configuration used
//     by internal/codegen/ir (goldmark.New(), no extensions) and reports
//     every candidate occurrence of a closed, code-owned pattern registry
//     (see PatternID) — across prose, inline code, fenced/indented code,
//     block HTML (including HTML comments), and inline raw HTML — with its
//     owner, file, AST node context, exact byte/source range, and exact
//     snippet (see Candidate);
//  4. classifies every candidate against a checked-in classification
//     manifest (see ClassificationManifest) into one of the closed
//     Classification values, or explicitly reports it unclassified — there
//     is no implicit default — and conversely fails the scan if any checked-
//     in classification entry matches no real candidate (see
//     RequireNoOrphanedClassifications), so the manifest can drift stale in
//     neither direction without a scan failure; and
//  5. hashes the canonical tree before and after scanning and fails if a
//     single byte changed (see HashTree), proving the scan is read-only.
//     This proof covers every byte of every non-excluded file under a
//     canonical root; it does not cover the content of excluded segments
//     (testdata/vendor/.git/.opencode — a scanner bug that wrote there would
//     not be caught) or file permission bits (see HashTree's own doc
//     comment).
//
// The resulting Inventory is the input #46 (process/Git/filesystem effects),
// #43 (task effects), and #40 (runtime contracts) consume before freezing
// their own closed sets, and the input #42's strict migration gate consumes
// via RequireZeroUnclassified.
package scan
