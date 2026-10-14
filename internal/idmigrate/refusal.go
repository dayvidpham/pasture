package idmigrate

import (
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// This file owns the ONE definition of the refusal texts the cutover guard and
// both pipeline frontends render. The guard (internal/tasks/id_format_guard.go)
// and the command (internal/handlers/migrate_ids.go) render these verbatim;
// neither carries a second literal, so the populated and unbacked-mark refusals
// can never drift apart between the two frontends.

// RefusePopulatedRefusal is the storage refusal a populated, markerless store
// earns on a normal open. It names the path, the converting command, the
// stopped operation, the unchanged state and the backup guidance.
func RefusePopulatedRefusal(storePath string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("The pasture database at %q still uses the retired two-dash task identity format.", storePath),
		Why: "Every namespaced identity in this store joins the namespace and UUID with \"--\", but this build " +
			"reads only the slash form \"<namespace>/<uuid>\". The store has not published the slash identity " +
			"marker, and pasture will not guess at a half-converted store by sampling its keys.",
		Where: "Opening the pasture database before converting it (internal/idmigrate/refusal.go in idmigrate.RefusePopulatedRefusal).",
		Impact: "Nothing was changed by this attempt. No task, edge, comment, agent or journal row was read or " +
			"written. This open stopped before schema work, actor registration, genesis reconciliation and " +
			"journal decode. The store stays exactly as it is until it is converted.",
		Fix: fmt.Sprintf("1. Stop every writer — the daemon and any CLI or hook process:\n"+
			"     pgrep -af 'pasture|pastured'\n"+
			"2. See what the conversion would change, without writing:\n"+
			"     pasture migrate ids --dry-run --db %q\n"+
			"3. Keep the consistent backup the dry run tells you it will make, then convert once:\n"+
			"     pasture migrate ids --db %q\n"+
			"4. Restart only upgraded binaries. After conversion the old two-dash input never resolves again, "+
			"and an older pasture binary refuses this file for every command until it is upgraded.",
			storePath, storePath),
	}
}

// UnbackedMarkRefusal is the storage refusal a marker-bearing store with an
// incomplete fence (missing/mismatched receipt, receipt without marker, or a
// recorded version below the receipt's floor) earns on a normal open or a
// command precheck. The state line is a DIAGNOSIS from the observed evidence,
// not a certainty: a genuine cutover store whose receipt row was lost or damaged
// presents identically.
func UnbackedMarkRefusal(storePath string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("The pasture database at %q carries the slash identity marker without a matching cutover receipt.", storePath),
		Why: "This store carries the slash-format marker but no complete cutover receipt — it was marked outside " +
			"a fenced, archived pasture cutover (an earlier build's conversion, or direct dependency use). A " +
			"genuine cutover store whose receipt row was lost or damaged presents identically.",
		Where:  "Opening the pasture database before converting it (internal/idmigrate/refusal.go in idmigrate.UnbackedMarkRefusal).",
		Impact: "Nothing was changed by this attempt. Pasture opens a store normally only when its fence evidence is complete — marker, receipt and floor. The store stays exactly as it is.",
		Fix:    "Choose the option that matches what you hold:\n" + unbackedMarkOptionsBlock(),
	}
}

// unbackedMarkOptionsBlock is the (a)/(b)/(c) block shared by every
// REFUSE-UNBACKED-MARK refusal. It is defined once; the P0 and the scoped P3a
// refusals both embed it.
func unbackedMarkOptionsBlock() string {
	return "  (a) If you hold an independent pre-marking copy of this store (an earlier `pasture migrate ids` run printed its backup path; check `--backup-dir` and the store directory): stop all writers, restore that copy over the store, remove the stale `-wal`/`-shm` siblings, and run the cutover under the fence. Restoring discards every write made after that copy, including all writes since the mark.\n" +
		"  (b) If the store holds history you need and no copy exists: pasture cannot open or convert this store; no in-place completion exists in this build. Copy the store aside (originals preserved) and export its contents with out-of-band SQLite tooling. A matching older build reads only what its own parser accepts — a store whose keys are all slash form is readable by an older build, but a store that still holds retired rows is refused by that build's slash-only parser, so out-of-band SQLite tooling is the reliable route. Keep it untouched until then.\n" +
		"  (c) If you do not need its history: move it aside and let pasture create a fresh store, which is cut over on first open.\n"
}

// ScopedP3aRefusal is the REFUSE-UNBACKED-MARK refusal that fires inside the
// cutover, after P1's archive and P2's committed catch-up. It names this run's
// archive and the applied catch-up, and it states the archive's OBSERVED marker
// fact: a markerless archive is a usable pre-marking copy, a marked archive
// post-dates the mark. archivePresent is false when this run made no archive
// (the store did not exist at P1's own check).
func ScopedP3aRefusal(storePath string, archivePresent bool, archive ArchiveMarker, archivePath string, appliedCatchUpSteps int) error {
	branch := "This run made no archive: P1 skipped it because the store path did not exist at P1's own check. A store created at that path after that check is neither archived nor covered by this run, and the offered paths are discard-and-recreate, or export out-of-band."
	if archivePresent {
		if archive.Present {
			branch = fmt.Sprintf("The archive this run recorded at P1 (%s) carries the mark and post-dates it: it is NOT the independent pre-marking copy option (a) refers to — restoring it does not clear the mark.", archivePath)
		} else {
			branch = fmt.Sprintf("The archive this run recorded at P1 (%s) is markerless: it was taken before the mark and is a usable pre-marking copy — stop all writers, copy it over the store, remove the stale `-wal`/`-shm` siblings, and re-run the cutover under the fence. Restoring discards every write made after the archive, including the mark.", archivePath)
		}
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("The pasture database at %q was marked during this cutover, outside the fenced archive.", storePath),
		Why: "This refusal fired inside the cutover: this run already archived the store at P1, and its audit catch-up " +
			"(P2) is committed. A concurrent writer or an out-of-band mark produced a marker-bearing state this " +
			"pipeline will not convert around.",
		Where: "Converting the store identity format under the fence (internal/idmigrate/refusal.go in idmigrate.ScopedP3aRefusal).",
		Impact: fmt.Sprintf("The conversion transaction wrote nothing and was rolled back; the committed catch-up remains "+
			"(%d step(s) applied this run; zero when the store was already current). The store keeps its fence state "+
			"and the marker is unchanged.", appliedCatchUpSteps),
		Fix: "Choose the option that matches what you hold:\n" + branch + "\n" + unbackedMarkOptionsBlock(),
	}
}

// CutoverGuidanceRefusal is the plain-migrate refusal for a markerless store
// (CUTOVER). Plain `pasture migrate` keeps its schema meaning only for a NORMAL
// store; a markerless store must be cut over by `pasture migrate ids` or by a
// normal open, both of which archive and write the fence evidence.
func CutoverGuidanceRefusal(storePath string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("The pasture database at %q has not been cut over to the slash identity format.", storePath),
		Why: "The store does not carry the slash identity marker and cutover receipt, so plain schema migration " +
			"must not touch it: the audit catch-up mints slash identities, and a plain migrate would populate " +
			"the store before its fence evidence exists.",
		Where:  "Migrating the pasture database (internal/idmigrate/refusal.go in idmigrate.CutoverGuidanceRefusal).",
		Impact: "Nothing was changed by this attempt. The store keeps its identities and no schema step wrote.",
		Fix: fmt.Sprintf("Run the offline identity cutover, which archives first:\n"+
			"     pasture migrate ids --dry-run --db %q\n"+
			"     pasture migrate ids --db %q\n"+
			"A normal pasture command also cuts the store over on first open.", storePath, storePath),
	}
}
