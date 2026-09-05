package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"go/ast"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// This file is the orphan-count read surface: the number an operator gets from
// `pasture hook lifecycle orphans`, and the sentence that ships with it.
//
// The count is the LEGAL side of the boundary this command's failure-mode
// tests prove. Those tests prove that no occurrence ever names an absent blob,
// which is the corrupting state. This one reports the other side: a blob that
// no occurrence names, which is legal, expected, and reclaimable. Both numbers
// come from one predicate, so the count can never disagree with the invariant.

// runLifecycleOrphans runs the orphan report through the BUILT BINARY, which is
// the command a user runs. Nothing here calls the handler directly.
func runLifecycleOrphans(t *testing.T, binary, dbPath, format string) string {
	t.Helper()
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "orphans", "--format", format)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String(), "the orphan report is an ordinary read; it must not warn")
	return stdout.String()
}

// leaveOrphanBlobs reproduces what an ABANDONED INVOCATION leaves behind, using
// the production write itself. receipt.SQLiteBlobStore.Put is the FIRST of the
// two durable writes a hook performs; an invocation abandoned after it and
// before the journal append leaves exactly this state, one blob per abandoned
// invocation. Nothing here is a fixture of the count: the count reads the store
// afterwards and does not know how the blob arrived.
func leaveOrphanBlobs(t *testing.T, dbPath string, count int) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	blobs, err := tasks.NewLifecycleBlobStore(tracker)
	require.NoError(t, err)
	for i := 0; i < count; i++ {
		body := []byte(fmt.Sprintf(`{"abandoned-invocation":%d}`, i))
		require.NoError(t, blobs.Put(context.Background(), digest.FromBytes(body), body))
	}
}

// TestOrphanCountIsZeroOnACleanStoreAndTrueAfterAbandonedInvocations is the
// measurement itself, taken through the built binary in both renderers.
//
// A SUCCESSFUL invocation is ingested first, on purpose. Its blob IS named by
// an occurrence, so it must not be counted; a count that reported it would tell
// an operator that ordinary successful work leaves debris behind.
//
// MUTATION for the note assertions: drop OrphanPayloadNote from the text
// renderer in handlers.HookLifecycleOrphans, leaving only the count, and this
// test turns RED at BOTH readings. That mutation used to leave every owning
// package green, which is why the note is asserted here on the binary's own
// standard output and not only on the constant and the JSON.
func TestOrphanCountIsZeroOnACleanStoreAndTrueAfterAbandonedInvocations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	cleanText := runLifecycleOrphans(t, binary, dbPath, "text")
	assert.Contains(t, cleanText, "orphan payload blobs remaining: 0\n",
		"a clean store has no unnamed payload blob, and the report must say so rather than stay silent")
	assert.Contains(t, cleanText, "orphan payload blobs reclaimed by this run: 0\n",
		"a clean store gives the reclaim nothing to do, and the report says so rather than omitting the number")
	assert.Contains(t, cleanText, handlers.OrphanPayloadNote,
		"the note must reach the TEXT renderer, which is the DEFAULT format and the only one an "+
			"operator reads at a terminal. Pinning the constant and the JSON protects the COPIES "+
			"and not the DELIVERY: cutting the note out of the text renderer leaves 'orphan "+
			"payload blobs: 0' standing alone, which is the bare number this sentence exists to "+
			"prevent. The note ships at ZERO as well, because an operator meets the term for the "+
			"first time whichever number is beside it")

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_222.json"))
	require.NoError(t, err)
	ingest := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())

	assert.Contains(t, runLifecycleOrphans(t, binary, dbPath, "text"), "orphan payload blobs remaining: 0\n",
		"a COMMITTED invocation names its own blob, so successful work must never be counted as an orphan")

	const abandoned = 3
	leaveOrphanBlobs(t, dbPath, abandoned)

	abandonedText := runLifecycleOrphans(t, binary, dbPath, "text")
	assert.Contains(t, abandonedText, "orphan payload blobs remaining: 3\n",
		"one orphan arises per abandoned invocation, so three abandonments must read as three")
	assert.Contains(t, abandonedText, "orphan payload blobs reclaimed by this run: 0\n",
		"a blob the production store wrote moments ago is inside the writer window, so this run must reclaim none of the three: its writer may still be between its two writes")
	assert.Contains(t, abandonedText, handlers.OrphanPayloadNote,
		"a NON-ZERO reading is where the misreading is expensive, so the text renderer must carry "+
			"the note there too: an operator who reads a count alone concludes the store is damaged "+
			"and hunts for a corrupt state these proofs show cannot exist")

	var view orphanReportView
	require.NoError(t, json.Unmarshal([]byte(runLifecycleOrphans(t, binary, dbPath, "json")), &view))
	assert.Equal(t, int64(abandoned), view.Remaining, "both renderers must report the same remaining number")
	assert.Equal(t, int64(0), view.ReclaimedThisRun, "both renderers must report the same reclaimed number")
	assert.Equal(t, handlers.OrphanPayloadNote, view.Note,
		"the meaning travels with the number in JSON too: a dashboard that renders the count alone "+
			"reproduces the misreading the sentence exists to prevent")
}

// TestOrphanCountAgreesWithTheEnumerationTheInvariantTestUses is the agreement
// this number owes. The abandonment invariant enumerates the orphan set with
// receipt.SQLiteBlobStore.Reclaimable; if the count answered a different
// question, the operator surface and the proof would describe different stores
// and the operator's would be the one nobody checked.
func TestOrphanCountAgreesWithTheEnumerationTheInvariantTestUses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	leaveOrphanBlobs(t, dbPath, 4)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	blobs, err := tasks.NewLifecycleBlobStore(tracker)
	require.NoError(t, err)

	enumerated, err := blobs.Reclaimable(context.Background(), receipt.MaxReclaimablePayloads)
	require.NoError(t, err)
	counted, err := blobs.ReclaimableCount(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(len(enumerated)), counted,
		"the count and the enumeration read one predicate; disagreeing means the operator-facing "+
			"number describes a set no proof covers")
	assert.Equal(t, int64(4), counted)
}

// TestOrphanCountWordingSaysWhatItIsThatItIsExpectedAndWhatALargeNumberMeans
// pins the operator text, because the number is a TRUTH CLAIM and a correct
// number can still teach a false lesson.
//
// The misreading it prevents is specific and expensive: an operator who reads
// a large count and nothing else concludes the store is damaged and starts
// hunting for corruption — for the one state this command's sibling proofs
// show CANNOT exist. Meanwhile the real signal, repeated abandonment caused by
// store contention, goes uninvestigated.
//
// Each of the three claims below is pinned by a phrase, so an edit that
// quietly turns "expected and reclaimable" into something that reads as a fault
// turns this test RED.
//
// WHAT IT VISITS: the THREE claims listed below, in the wording this command
// ships today.
// WHAT IT DOES NOT READ: any fourth claim added to that output, and any
// rewording that keeps the pinned phrases while changing what surrounds them.
// It reads phrases, not the whole sentence.
func TestOrphanCountWordingSaysWhatItIsThatItIsExpectedAndWhatALargeNumberMeans(t *testing.T) {
	t.Parallel()

	note := handlers.OrphanPayloadNote

	// 1. WHAT AN ORPHAN IS.
	assert.Contains(t, note, "a payload blob that no recorded occurrence names",
		"the reader must be told what the thing being counted IS")
	assert.Contains(t, note, "abandoned between its two durable writes",
		"and where it comes from: the gap between the blob write and the journal append")
	assert.Contains(t, note, "at most one arises per abandoned invocation",
		"the per-invocation bound is what makes the number readable as a count of abandonments")

	// 2. THAT IT IS EXPECTED AND RECLAIMABLE, NOT DAMAGE.
	assert.Contains(t, note, "expected and reclaimable, not damage",
		"without this the count reads as a defect report")
	assert.Contains(t, note, "a journal row naming an absent blob could not be repaired at all",
		"the reader must be told WHY this order was chosen, or 'not damage' is an assertion to take on trust")

	// 3. WHAT A LARGE NUMBER MEANS.
	assert.Contains(t, note, "does not mean the store is corrupt",
		"the misreading must be refused by name, not merely left unstated")
	assert.Contains(t, note, "the thing to investigate is the store contention that caused the abandonment",
		"the reader must leave with the action to take, which is the whole value of the number")

	// 4. THAT THE COMMAND CHANGES WHAT IT MEASURES, AND BY HOW MUCH.
	assert.Contains(t, note, "at most 1024 per run",
		"the reader must be told the reclaim is bounded, or a shrinking number reads as data loss")
	assert.Contains(t, note, "the reclaimed count says by how much",
		"the reader must be told which number explains the change")

	// The words that would make it a fault report must not appear.
	for _, forbidden := range []string{"corruption detected", "damaged", "data loss", "inconsistent"} {
		assert.NotContains(t, strings.ToLower(note), forbidden,
			"the note must not read as a fault report: %q sends the operator hunting for a state that cannot exist", forbidden)
	}
}

// TestARefusedFormatNamesTheValueGivenAndTheValuesAccepted pins the refusal an
// operator meets when they mistype --format.
//
// The refusal is operator text, so it is a truth claim and it is pinned. What
// it must carry is the three things a person at a terminal needs and cannot
// get anywhere else: WHAT THEY GAVE, echoed back, so a typo or a shell that ate
// a quote is visible; WHAT IS ACCEPTED, so the next attempt is not another
// guess; and WHAT EACH ACCEPTED VALUE PRINTS, so the choice is informed. It
// must NOT recite internal preconditions such as a context or an output writer:
// those cannot be produced from a command line, so naming them to an operator
// sends them looking for a flag that does not exist.
//
// MUTATION: drop the %q value from the refusal in
// internal/handlers.HookLifecycleOrphans, or restore the older wording that
// listed the context and the output writer, and this test turns RED.
func TestARefusedFormatNamesTheValueGivenAndTheValuesAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "orphans", "--format", "yamll")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()

	require.Error(t, err, "a format this command cannot print must be refused, not printed as text")
	assert.Empty(t, stdout.String(), "a refused read prints no report")

	refusal := stderr.String()
	assert.Contains(t, refusal, `"yamll"`,
		"the refusal must ECHO THE VALUE IT REFUSED; without it an operator cannot see a typo or a "+
			"quote their shell removed, and re-running is a guess")
	assert.Contains(t, refusal, "accepted values are text and json",
		"the refusal must name what IS accepted, or the operator's next attempt is another guess")
	assert.Contains(t, refusal, "--format text",
		"the operator must be told what to DO, in the exact form they will type")
	assert.NotContains(t, refusal, "context",
		"a context is an internal precondition an operator cannot supply or withhold from a "+
			"command line; naming it sends them hunting for a flag that does not exist")
	assert.NotContains(t, refusal, "output writer",
		"an output writer is an internal precondition, for the same reason")
}

// TestAWiringFaultIsNamedAsOneAndNotAsAnOperatorMistake pins the OTHER refusal,
// and it is separate from the format refusal on purpose: the two have different
// readers, and one message serving both told an operator at a terminal to
// supply a context and an output writer, which no command line can do.
//
// This is the ONE test in this file that calls the handler directly, because
// the state it pins CANNOT BE REACHED THROUGH THE BINARY: the command always
// passes a context and a writer. Reaching it needs the caller to be wrong, so
// the caller is the test.
//
// MUTATION: fold the two refusals back into one condition in
// handlers.HookLifecycleOrphans and this test turns RED, because the merged
// message names a format to a caller that gave a valid one.
func TestAWiringFaultIsNamedAsOneAndNotAsAnOperatorMistake(t *testing.T) {
	t.Parallel()

	code, err := handlers.HookLifecycleOrphans(nil, io.Discard, handlers.HookLifecycleOrphansInput{}, "text") //nolint:staticcheck // the nil context IS the state under test
	require.Error(t, err, "a missing context must be refused, not counted around")
	assert.NotEqual(t, 0, code, "a refused read must not report success")
	assert.Contains(t, err.Error(), "wiring fault inside pasture",
		"the reader of this message is a maintainer, so it must say the fault is inside pasture "+
			"rather than describing a flag an operator could have got wrong")
	assert.NotContains(t, err.Error(), "--format",
		"the caller gave a valid format; naming one here sends a maintainer to the wrong place")
}

// TestTheOrphanCountLivesOnlyOnTheOrphansReadSurface is the placement guard.
//
// It is structural because the hazard is structural. On the hook path the count
// would run inside the hook-invocation deadline, and it READS THE STORE, which
// is the resource that contends. So it would be slowest under exactly the
// condition that produces orphans, and a slow enough count would push the
// invocation into its deadline and leave ONE MORE ORPHAN. The counter would
// become a cause of the thing it counts, and worst precisely when it matters
// most. The read command is the only home where the measurement cannot perturb
// the measurement.
//
// THE SCOPE IS THE WHOLE REPOSITORY, NOT A LIST OF FILENAMES AND NOT TWO
// PACKAGES. A named deny list only guards the sources that existed when it was
// written, so a hook-path source ADDED LATER escapes it in SILENCE, and a guard
// that goes quiet as the code grows is worse than none: it reads as covered. A
// named list is also a truth claim about each file it names, and this one was
// wrong - it called `context` and `lineage` "the hook path" when both are READ
// commands, printing committed lineage and disclosing bounded context.
//
// A GLOB OVER TWO PACKAGES HAD THE SAME HOLE IN A SMALLER SHAPE. The count is
// a method on a store, so any package that holds the store can call it: the
// symbol added to internal/lifecycle/nativeresponse, or to
// internal/tasks/lifecycle_identity.go, left the two-package version GREEN.
// Reasoning about WHICH package could one day host a hook-path caller is the
// same losing game as naming files, so the walk covers every Go source in the
// repository and takes exactly two exemptions.
//
// So the claim is inverted into one that stays true as sources are added: the
// count is DEFINED in one place and CALLED from exactly one production source,
// the orphans handler, and from nowhere else in the repository. Every new
// source anywhere is covered the day it is written, whether it is on the hook
// path or beside it, and nothing has to be remembered.
//
// MUTATION: call blobs.ReclaimableCount from any other production source - the
// lifecycle dispatch handler, internal/lifecycle/nativeresponse and
// internal/tasks/lifecycle_identity.go are the three the two-package version
// missed - and this test turns RED naming that file. Each was added and
// removed in turn, and each turned it RED.
// WHAT IT VISITS: every non-test Go file under the package roots listed
// below, which are the surfaces a hook invocation can reach.
// WHAT IT DOES NOT READ: a root not on that list. The list grew once
// already, when a two-package version missed three files, and it is prose
// rather than a derivation because "the surfaces a hook can reach" has no
// source in the tree to read it from.
func TestTheOrphanCountLivesOnlyOnTheOrphansReadSurface(t *testing.T) {
	t.Parallel()

	repository := filepath.Join("..", "..")

	// The two exemptions, named by PATH and not by base name, so an exemption
	// cannot spread to a same-named file in another package. One DEFINES the
	// count; the other is its single production CALLER.
	exemptions := map[string]bool{
		filepath.Join(repository, "internal", "lifecycle", "receipt", "journal.go"):    false,
		filepath.Join(repository, "internal", "handlers", "hook_lifecycle_orphans.go"): false,
	}
	// Offending file NAMES are collected, and the assertion reads the names.
	// Asserting on each file BODY would print a whole source file into the
	// failure, which buries the one fact the reader needs.
	counting := []string{}
	walked := 0

	require.NoError(t, filepath.WalkDir(repository, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// A checkout carries directories that are not this module's
			// sources. Walking them costs time and can only produce a name
			// nobody can act on.
			switch entry.Name() {
			case ".git", "testdata", "legacy", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		clean := filepath.Clean(path)
		if _, exempt := exemptions[clean]; exempt {
			exemptions[clean] = true
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		walked++
		if strings.Contains(string(body), "ReclaimableCount") {
			counting = append(counting, clean)
		}
		return nil
	}), "every production source of the repository must be readable, or the guard is reading less than it claims")

	assert.Empty(t, counting,
		"these sources must not count orphans. The count belongs to the orphans read surface alone: "+
			"anywhere on the hook path it would spend the invocation deadline reading the very store "+
			"that contends, and a slow enough count leaves one more orphan behind")
	// MUTATION: point the walk at a directory with no production source, or
	// narrow it back to a handful of named files or two packages, and this
	// turns RED. It guards the guard: a small scope passes every other
	// assertion here while reading almost nothing, which is the exact way the
	// deny-list version went quiet, and the two-package version after it.
	assert.Greater(t, walked, 300,
		"the walk must actually reach the production sources of the whole repository; a scope that "+
			"covered only a package or two would pass while leaving every other package free to "+
			"call the count")

	for path, reached := range exemptions {
		assert.True(t, reached,
			"the exempted source %s was never walked; if it is renamed or moved the exemption "+
				"silently stops matching, and the guard would then look stricter than it is while "+
				"a real home of the count goes unnamed", path)
	}
}

// orphanReportView is the JSON shape the orphans command prints: what this
// run reclaimed, what remains, and the note.
type orphanReportView struct {
	ReclaimedThisRun int64  `json:"orphanPayloadBlobsReclaimedThisRun"`
	Remaining        int64  `json:"orphanPayloadBlobsRemaining"`
	Note             string `json:"note"`
}

// lifecycleStoreHandle opens the unified database handle the production
// reader uses, for seeding and inspecting payload blob rows directly.
func lifecycleStoreHandle(t *testing.T, dbPath string) (*sql.DB, func()) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	concrete, ok := reader.(projection.Reader)
	require.True(t, ok, "the production reader %T must expose the unified handle", reader)
	return concrete.DB, func() { _ = tracker.Close() }
}

// seedLegacyOrphans writes payload blobs stamped 0, the value the migration
// that added written_at gave every pre-existing row. They are older than any
// bound and no occurrence names them, so a read command reclaims them at once,
// up to the cap.
func seedLegacyOrphans(t *testing.T, dbPath string, count int) {
	t.Helper()
	db, closeStore := lifecycleStoreHandle(t, dbPath)
	defer closeStore()
	tx, err := db.Begin()
	require.NoError(t, err)
	for i := 0; i < count; i++ {
		body := []byte(fmt.Sprintf(`{"legacy-orphan":%d}`, i))
		_, err := tx.Exec(`INSERT INTO lifecycle_payload_blobs(digest, body, byte_count, written_at) VALUES(?,?,?,0)`, digest.FromBytes(body).String(), body, len(body))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
}

func payloadBlobRows(t *testing.T, dbPath string) (int, []string) {
	t.Helper()
	db, closeStore := lifecycleStoreHandle(t, dbPath)
	defer closeStore()
	rows, err := db.Query(`SELECT digest FROM lifecycle_payload_blobs ORDER BY digest`)
	require.NoError(t, err)
	defer rows.Close()
	digests := []string{}
	for rows.Next() {
		var d string
		require.NoError(t, rows.Scan(&d))
		digests = append(digests, d)
	}
	require.NoError(t, rows.Err())
	return len(digests), digests
}

// TestTheOrphansReportSaysWhatThisRunReclaimedAndWhatRemains: the orphans
// command reclaims inside its own rebuild, so it MUTATES what it measures by
// running. Both numbers are printed and each is true of the run that printed
// it. MUTATION: drop the reclaimed line from the text renderer and this is RED
// at the first reading; drop the JSON field and it is RED at the second.
func TestTheOrphansReportSaysWhatThisRunReclaimedAndWhatRemains(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	const legacy = 5
	seedLegacyOrphans(t, dbPath, legacy)

	text := runLifecycleOrphans(t, binary, dbPath, "text")
	assert.Contains(t, text, "orphan payload blobs reclaimed by this run: 5\n",
		"five legacy orphans are older than any bound, so this run reclaims all five and says so")
	assert.Contains(t, text, "orphan payload blobs remaining: 0\n",
		"after the reclaim nothing remains, and the remaining number is true of the store this run left behind")

	seedLegacyOrphans(t, dbPath, legacy)
	var view orphanReportView
	require.NoError(t, json.Unmarshal([]byte(runLifecycleOrphans(t, binary, dbPath, "json")), &view))
	assert.Equal(t, int64(legacy), view.ReclaimedThisRun, "the JSON reader gets the reclaimed number")
	assert.Equal(t, int64(0), view.Remaining, "the JSON reader gets the remaining number")
	assert.Equal(t, handlers.OrphanPayloadNote, view.Note)
}

// TestEveryReadCommandReachesTheReclaim is the REAL-BINARY reachability proof
// for the reclaim, over EVERY read command, derived and not listed.
//
// POPULATION: each registered leaf under `hook lifecycle` whose RunE closure
// over cmd/pasture calls an exported handler whose own closure over
// internal/handlers calls the tasks rebuild (the one path to the reclaim).
// NON-VACUITY: at least four are derived (list, context, lineage, orphans
// today); a derived command with no entry in the argument table is RED, so a
// new read command cannot arrive unproven.
// PER COMMAND, through the built binary: a store holding ONE referenced blob
// (a real ingested occurrence) and cap+3 legacy orphans (written_at 0); the
// command exits 0 with EMPTY standard error; afterwards exactly the cap was
// reclaimed, the three youngest-by-digest-order survivors remain, and the
// referenced blob SURVIVES.
// WHAT IT VISITS: the live command tree; the non-test sources of cmd/pasture
// and internal/handlers; the payload blob table before and after each run.
// WHAT IT DOES NOT READ: the command's standard output beyond exit status.
// MUTATION: remove the reclaim call from the projection rebuild and every
// derived command is RED naming the surviving count.
func TestEveryReadCommandReachesTheReclaim(t *testing.T) {
	// Serial on purpose: it reads the live command tree, which the whole
	// process shares.
	root := writerGuardModuleRoot(t)
	handlersGraph := writerGuardParse(t, filepath.Join(root, "internal", "handlers"))
	rebuilders := handlersGraph.functionsReaching(func(call *ast.CallExpr) bool {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		return ok && strings.HasPrefix(selector.Sel.Name, "RebuildLifecycleOccurrences")
	})
	require.NotEmpty(t, rebuilders, "no handler reaches the tasks rebuild; the derivation is broken, not the tree")
	cmdGraph := writerGuardParse(t, filepath.Join(root, "cmd", "pasture"))

	arguments := map[string][]string{
		"list":    {},
		"context": {"--binding", "session:session_id=3696b790-3973-49f2-b156-9d82146bf7ec"},
		"lineage": {"--binding", "session:session_id=3696b790-3973-49f2-b156-9d82146bf7ec"},
		"orphans": {},
	}
	binary := lifecycleBinary(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_222.json"))
	require.NoError(t, err)

	derived := []string{}
	for _, command := range hookLifecycleCmd.Commands() {
		if command.RunE == nil {
			continue
		}
		closure := cmdGraph.closure(cmdGraph.bodyOfFunc(t, command.RunE))
		if !callsHandlersWriter(closure, rebuilders) {
			continue
		}
		derived = append(derived, command.Name())
		args, known := arguments[command.Name()]
		require.True(t, known, "%s rebuilds the projection and so reclaims, but this proof has no arguments to run it with; add them so the new read command is proven", command.CommandPath())

		dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)
		ingest := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle",
			"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
		ingest.Stdin = bytes.NewReader(raw)
		require.NoError(t, ingest.Run(), "%s: the referenced occurrence must ingest", command.Name())
		before, referenced := payloadBlobRows(t, dbPath)
		require.Equal(t, 1, before, "%s: one referenced blob before seeding", command.Name())
		const survivors = 3
		seedLegacyOrphans(t, dbPath, projection.ReclaimCap+survivors)

		run := exec.Command(binary, append([]string{databaseFlagName.Argument(), dbPath, "hook", "lifecycle", command.Name()}, args...)...)
		var stdout, stderr bytes.Buffer
		run.Stdout = &stdout
		run.Stderr = &stderr
		require.NoError(t, run.Run(), "%s: %s%s", command.Name(), stdout.String(), stderr.String())
		assert.Empty(t, stderr.String(), "%s: a read command whose reclaim succeeds prints nothing on standard error", command.Name())

		after, digests := payloadBlobRows(t, dbPath)
		assert.Equal(t, 1+survivors, after, "%s: exactly the cap (%d) of %d legacy orphans is reclaimed by one run; %d rows survive", command.Name(), projection.ReclaimCap, projection.ReclaimCap+survivors, after)
		assert.Contains(t, digests, referenced[0], "%s: the blob a recorded occurrence names must survive the reclaim", command.Name())
	}
	sort.Strings(derived)
	require.GreaterOrEqual(t, len(derived), 4, "non-vacuity: at least list, context, lineage and orphans rebuild the projection; derived %v", derived)
}

// TestTheOrphansHelpSaysTheCommandReclaims pins the help text, because it is
// the most consequential sentence on this command's operator surface: whether
// the command deletes the user's data. The command reclaims orphan blobs
// inside its rebuild, so the help must say so and must not say the opposite.
// Read through the built binary, which is what a user runs.
// MUTATION: restore "The command deletes nothing" to the Long text, or drop
// the reclaim sentence, and this is RED.
func TestTheOrphansHelpSaysTheCommandReclaims(t *testing.T) {
	t.Parallel()
	command := exec.Command(lifecycleBinary(t), "hook", "lifecycle", "orphans", "--help")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	require.NoError(t, command.Run())
	help := stdout.String()
	assert.Contains(t, help, "Reclaim and count the payload blobs that no recorded occurrence names",
		"the first line must say the command reclaims, not only counts")
	assert.Contains(t, help, "reclaims orphan\nblobs older than the writer window, at most 1024 per run",
		"the help must say what is deleted, under what bound, and how much at most")
	assert.Contains(t, help, "how many blobs this run\nreclaimed, and how many remain",
		"the help must name the two numbers the output prints, so a reader can tell them apart")
	assert.NotContains(t, help, "deletes nothing",
		"a command that deletes must never carry help text that says it deletes nothing")
}
