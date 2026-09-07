package tasks

// assignment_index_test.go guards the started-episode index and, above all, the
// claim that it has ONE writer.
//
// The choke-point guard is STRUCTURAL and derives BOTH of its sets from the
// package's own source: the places that BUILD an assignment-start payload, and
// the places that COMMIT one. Neither list is written here, so a writer added
// later is covered with no edit.
//
// THE TWO SETS ARE NOT THE SAME SIZE, and that is the fact the guard exists to
// hold on to: FOUR payload constructors flow into THREE commit sites, because
// two candidate commands share one. A guard that expected one commit per
// constructor would be red on correct code; a guard that only counted
// constructors would be green on a bypass. So the derivation maps each
// constructor to the commit site its payload reaches, and requires THAT SITE to
// write the index.

import (
	"bytes"
	stderrors "errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

const (
	assignmentPayloadType = "assignmentStartPayload"
	chokePointCall        = "recordAssignmentStart"
	indexHelperCall       = "indexComposedEpisode"
	indexBatchHelperCall  = "indexComposedBatch"
	assignmentRoleType    = "AssignmentRole"
	transferRecordCall    = "recordTransferredEpisode"
)

// composedCommitCalls are the journal entry points that commit a composed
// allocation. A function that calls one of these is a COMMIT SITE.
var composedCommitCalls = map[string]bool{
	"RunAllocateComposed":      true,
	"RunAllocateComposedBatch": true,
}

// packageFunctions parses the package's own production source and returns, per
// function name, the names it calls and the composite-literal types it builds.
type functionFacts struct {
	calls    map[string]bool
	literals map[string]bool
	params   map[string]bool
	file     string
}

func parsePackageFunctions(t *testing.T) map[string]functionFacts {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(entry fs.FileInfo) bool {
		return !strings.HasSuffix(entry.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse this package's own source: %v", err)
	}

	facts := map[string]functionFacts{}
	files := 0
	for _, pkg := range packages {
		for name, file := range pkg.Files {
			files++
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				current := functionFacts{calls: map[string]bool{}, literals: map[string]bool{}, params: map[string]bool{}, file: filepath.Base(name)}
				if fn.Type.Params != nil {
					for _, field := range fn.Type.Params.List {
						if ident, ok := field.Type.(*ast.Ident); ok {
							current.params[ident.Name] = true
						}
					}
				}
				ast.Inspect(fn, func(node ast.Node) bool {
					switch typed := node.(type) {
					case *ast.CallExpr:
						switch callee := typed.Fun.(type) {
						case *ast.Ident:
							current.calls[callee.Name] = true
						case *ast.SelectorExpr:
							current.calls[callee.Sel.Name] = true
						}
					case *ast.CompositeLit:
						if ident, ok := typed.Type.(*ast.Ident); ok {
							current.literals[ident.Name] = true
						}
					}
					return true
				})
				facts[fn.Name.Name] = current
			}
		}
	}
	if files == 0 {
		t.Fatalf("parsed this package and visited no production file; the derivation read nothing")
	}
	if len(facts) == 0 {
		t.Fatalf("parsed %d production file(s) and found no function; the derivation read nothing", files)
	}
	return facts
}

// TestEveryAssignmentStartWriterReachesTheChokePoint is the structural guard.
//
// RED when: a function builds an assignment-start payload and the commit site
// its payload reaches does not write the index. That is the fifth-writer case,
// and the failure names the site.
func TestEveryAssignmentStartWriterReachesTheChokePoint(t *testing.T) {
	facts := parsePackageFunctions(t)

	// SET ONE, derived: who builds an assignment-start payload.
	var constructors []string
	for name, fn := range facts {
		if fn.literals[assignmentPayloadType] {
			constructors = append(constructors, name)
		}
	}
	sort.Strings(constructors)
	// The constructor set is INFORMATIONAL. It includes the decoder and the
	// transfer recorder, which build the same shape for other reasons, and it
	// is reported rather than enforced. The ENFORCED population is the commit
	// sites below, because an episode is started by a commit and not by
	// building a payload: the site found by this guard on its first run built
	// no payload at all.

	// SET TWO, derived: who commits a composed allocation.
	var commitSites []string
	for name, fn := range facts {
		for call := range fn.calls {
			if composedCommitCalls[call] {
				commitSites = append(commitSites, name)
				break
			}
		}
	}
	sort.Strings(commitSites)

	// NON-VACUITY: a guard over an empty set proves nothing, and the two sets
	// are known to differ in size, so both are checked.
	if len(constructors) < 2 {
		t.Fatalf("derived %d assignment-start payload constructor(s) (%v); want at least 2, or the derivation is not reading the source it thinks it is", len(constructors), constructors)
	}
	if len(commitSites) < 2 {
		t.Fatalf("derived %d composed commit site(s) (%v); want at least 2", len(commitSites), commitSites)
	}

	// EVERY COMMIT SITE MUST WRITE THE INDEX, directly or through the one helper
	// that does. This is the claim: whatever reaches a commit site is recorded.
	var bypassing []string
	for _, site := range commitSites {
		fn := facts[site]
		if fn.calls[chokePointCall] || fn.calls[indexHelperCall] || fn.calls[indexBatchHelperCall] {
			continue
		}
		bypassing = append(bypassing, site+" (in "+fn.file+")")
	}
	if len(bypassing) > 0 {
		sort.Strings(bypassing)
		t.Fatalf("%d composed commit site(s) do not record the started episode: %v; every site that commits an assignment must call %s, directly or through %s or %s, or a gate will not know the holder it just created", len(bypassing), bypassing, chokePointCall, indexHelperCall, indexBatchHelperCall)
	}

	// EVERY COMMIT SITE MUST BE GIVEN THE SLOT, not choose it. The slot decides
	// which actions the occupant may take, so it is declared by the command that
	// hands the work out and read here. A site that names a slot itself has
	// moved the policy into the last place a reader looks for it.
	var undeclared []string
	for _, site := range commitSites {
		if !facts[site].params[assignmentRoleType] {
			undeclared = append(undeclared, site+" (in "+facts[site].file+")")
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		t.Fatalf("%d composed commit site(s) are not given the slot the occupant holds: %v; every site must take it as a parameter from the command that defines the allocation, because the slot decides what the occupant may do and belongs beside that command, not at the point of recording", len(undeclared), undeclared)
	}

	// AND THE ONE HELPER MUST ITSELF REACH THE CHOKE POINT, so that satisfying
	// the check above by calling the helper is not a way around it.
	for _, helperName := range []string{indexHelperCall, indexBatchHelperCall} {
		helper, ok := facts[helperName]
		if !ok {
			t.Fatalf("the index helper %s does not exist, so a commit site could pass this check by calling something that records nothing", helperName)
		}
		if !helper.calls[chokePointCall] {
			t.Fatalf("the index helper %s does not call %s; a commit site that calls it would then record nothing", helperName, chokePointCall)
		}
	}

	// THE TRANSFER IS A WRITER TOO, and it is not a composed allocation, so the
	// derivation above cannot see it. It is named here as the one site of its
	// own kind, and the claim is the same: it must reach the choke point.
	transfer, ok := facts["TransferTaskAssignment"]
	if !ok {
		t.Fatalf("TransferTaskAssignment is not in the parsed source; the transfer writer cannot be checked, and it starts episodes like any other")
	}
	if !transfer.calls[transferRecordCall] {
		t.Fatalf("TransferTaskAssignment does not call %s; a transfer starts an episode, so a gate would not know the new holder and would refuse them work they now own", transferRecordCall)
	}
	recorder, ok := facts[transferRecordCall]
	if !ok || !recorder.calls[chokePointCall] {
		t.Fatalf("%s does not reach %s; the transfer would then record nothing", transferRecordCall, chokePointCall)
	}

	t.Logf("derived %d assignment-start payload constructor(s) %v flowing into %d composed commit site(s) %v", len(constructors), constructors, len(commitSites), commitSites)
}

// TestTheIndexRefusesARowItCouldNeverEvaluate proves the hard error on a row
// with no governing authority, and on the other fields a gate needs.
//
// RED when: a record with a zero authority, or with no actor, task, assignment
// or known slot, is written instead of refused.
func TestTheIndexRefusesARowItCouldNeverEvaluate(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "index-refusal")
	task := createHumanTestTask(t, tracker, "index-refusal")
	sound := startedEpisode{Assignment: "episode-1", Actor: actor, Task: task, Role: RoleOwnerResponsibility, Authority: 7}

	// The control: the sound record is accepted, so the refusals below cannot
	// pass because nothing is ever accepted.
	if err := recordAssignmentStart(t.Context(), tracker.auditDB, sound); err != nil {
		t.Fatalf("a sound record was refused: %v", err)
	}

	for _, broken := range []struct {
		name   string
		mutate func(startedEpisode) startedEpisode
		phrase string
	}{
		{
			name: "no authority",
			mutate: func(e startedEpisode) startedEpisode {
				e.Authority = 0
				return e
			},
			phrase: "can never be evaluated",
		},
		{
			name: "no actor",
			mutate: func(e startedEpisode) startedEpisode {
				e.Actor = provenance.ActorID{}
				return e
			},
			phrase: "can never be found",
		},
		{
			name: "no task",
			mutate: func(e startedEpisode) startedEpisode {
				e.Task = provenance.TaskID{}
				return e
			},
			phrase: "says nothing a gate can use",
		},
		{
			name: "no assignment",
			mutate: func(e startedEpisode) startedEpisode {
				e.Assignment = ""
				return e
			},
			phrase: "would overwrite another episode",
		},
		{
			name: "unknown slot",
			mutate: func(e startedEpisode) startedEpisode {
				e.Role = AssignmentRole(99)
				return e
			},
			phrase: "cannot be judged",
		},
	} {
		t.Run(broken.name, func(t *testing.T) {
			err := recordAssignmentStart(t.Context(), tracker.auditDB, broken.mutate(sound))
			if err == nil {
				t.Fatalf("a record with %s was written; want a hard refusal, because a row a gate cannot evaluate is worse than a missing one: a caller reads it and gets an answer", broken.name)
			}
			// The phrase is checked in the REPORT, which is what a person
			// reads. The one-line form carries only the category and the
			// headline, so a check against it would pass on a message that
			// tells the reader nothing.
			report := renderStructuredReport(t, err)
			if !strings.Contains(report, broken.phrase) {
				t.Errorf("the refusal for %s does not say why it matters (want the phrase %q):\n%s", broken.name, broken.phrase, report)
			}
		})
	}
}

// TestTheIndexWriteIsIdempotent proves a replayed command lands one row, not
// two, and that a conflicting re-record preserves the original authority.
//
// RED when: the write inserts a second row for one episode.
func TestTheIndexWriteIsIdempotent(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "index-idempotent")
	task := createHumanTestTask(t, tracker, "index-idempotent")
	episode := startedEpisode{Assignment: "episode-1", Actor: actor, Task: task, Role: RoleOwnerResponsibility, Authority: 7}

	for i := 0; i < 3; i++ {
		if err := recordAssignmentStart(t.Context(), tracker.auditDB, episode); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if rows := countIndexRows(t, tracker); rows != 1 {
		t.Fatalf("three records of one episode left %d row(s); want 1, because a replayed command must not double an episode", rows)
	}

	// A conflicting authority is not a replay. It commits dirty invalidation
	// before returning a typed fault, and never overwrites the authentic row.
	episode.Authority = 11
	var stale *IndexStaleError
	if err := recordAssignmentStart(t.Context(), tracker.auditDB, episode); !stderrors.As(err, &stale) {
		t.Fatalf("conflicting re-record error=%v, want typed stale", err)
	}
	state, err := readAssignmentRecoveryState(t.Context(), tracker.auditDB)
	if err != nil || state.Status != assignmentCoverageDirty || state.Destructive == state.CertifiedDestructive {
		t.Fatalf("returned conflict did not persist dirty invalidation: state=%+v err=%v", state, err)
	}
	if rows := countIndexRows(t, tracker); rows != 1 {
		t.Fatalf("re-recording one episode left %d row(s); want 1", rows)
	}
	var authority int64
	if err := tracker.auditDB.QueryRow(`SELECT authority_journal_id FROM pasture_actor_assignment WHERE assignment_id = ?`, "episode-1").Scan(&authority); err != nil {
		t.Fatalf("read back the record: %v", err)
	}
	if authority != 7 {
		t.Fatalf("the record holds authority %d after a conflict; want original 7", authority)
	}
}

func countIndexRows(t *testing.T, tracker *trackerImpl) int {
	t.Helper()
	var rows int
	if err := tracker.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_actor_assignment`).Scan(&rows); err != nil {
		t.Fatalf("count index rows: %v", err)
	}
	return rows
}

// TestTheIndexTableRefusesAZeroAuthorityAtTheDatabase proves the refusal is not
// only in Go: the table itself will not hold an unusable row.
//
// RED when: the CHECK is dropped and a zero authority reaches the disk.
func TestTheIndexTableRefusesAZeroAuthorityAtTheDatabase(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	_, err := tracker.auditDB.Exec(
		`INSERT INTO pasture_actor_assignment (assignment_id, actor_id, task_id, role, authority_journal_id) VALUES (?, ?, ?, ?, ?)`,
		"direct", "actor", "task", "owner-responsibility", 0)
	if err == nil {
		t.Fatalf("the table accepted a record with no governing authority; want the database itself to refuse it, so a writer that skips the Go check still cannot store a row nothing can evaluate")
	}
}

// TestTheWatermarkStartsAtNothingIndexed proves the singleton watermark exists
// and that its absence reads as nothing indexed rather than as an error.
//
// RED when: the watermark table is missing, which would make the record's
// progress unrecordable.
func TestTheWatermarkStartsAtNothingIndexed(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	var rows int
	if err := tracker.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_actor_assignment_watermark`).Scan(&rows); err != nil {
		t.Fatalf("read the watermark table: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a fresh store has %d watermark row(s); want 0, meaning nothing has been indexed yet", rows)
	}
	if _, err := tracker.auditDB.Exec(
		`INSERT INTO pasture_actor_assignment_watermark (singleton_id, last_indexed_jid) VALUES (0, 12)
		 ON CONFLICT(singleton_id) DO UPDATE SET last_indexed_jid = excluded.last_indexed_jid`); err != nil {
		t.Fatalf("write the watermark: %v", err)
	}
	if _, err := tracker.auditDB.Exec(
		`INSERT INTO pasture_actor_assignment_watermark (singleton_id, last_indexed_jid) VALUES (1, 12)`); err == nil {
		t.Fatalf("the watermark table accepted a second row; want a singleton, so two watermarks cannot disagree")
	}
}

// renderStructuredReport returns the full plain-language block a person sees,
// not the one-line form, so a phrase pin is checked against what is actually
// read.
func renderStructuredReport(t *testing.T, err error) string {
	t.Helper()
	var structured *pasterrors.StructuredError
	if !stderrors.As(err, &structured) {
		t.Fatalf("the refusal is not a structured error, so a reader gets no reason at all: %v", err)
	}
	var buffer bytes.Buffer
	structured.Report(&buffer)
	if buffer.Len() == 0 {
		t.Fatalf("the structured refusal rendered an empty report")
	}
	return buffer.String()
}

// ─── the transfer, end to end ────────────────────────────────────────────────

// TestATransferRecordsItsNewEpisodeAndRetiresTheOld is the behavioural half of
// the transfer arm: after a transfer the record names the NEW holder, the
// history no longer credits the old authority, and pasture's own fact for the
// new episode exists and carries the authority id.
//
// RED when: the transfer records nothing (a gate would then refuse the new
// holder work they now own), or the old authority still reads as governing, or
// the fact is written without the authority id the rebuild needs.
func TestATransferRecordsItsNewEpisodeAndRetiresTheOld(t *testing.T) {
	fixture := newTaskAssignmentTransferFixture(t)
	fixture.seedOwnerAssignment(t, "owner-a")
	tracker := fixture.tracker

	before := transferIndexRow(t, tracker, "owner-a")
	if before.Authority <= 0 {
		t.Logf("the seeded episode is not recorded, which is expected: it was written by the test fixture and not by a command")
	}

	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           fixture.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "owner-b",
		ActorID:          fixture.actorA,
		NextOccupant:     fixture.actorB,
	}
	if _, err := tracker.TransferTaskAssignment(t.Context(), request); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	// 1. THE NEW HOLDER IS RECORDED.
	row := transferIndexRow(t, tracker, "owner-b")
	if row.Authority <= 0 {
		t.Fatalf("after the transfer there is no record for the new episode; a gate would find no holder for this task and refuse the new occupant work they now own")
	}
	if row.Actor != fixture.actorB.String() {
		t.Fatalf("the record names %q as the holder; want the new occupant %q", row.Actor, fixture.actorB)
	}
	if row.Task != fixture.task.String() {
		t.Fatalf("the record names task %q; want %q", row.Task, fixture.task)
	}

	// 2. THE OLD AUTHORITY NO LONGER GOVERNS. This is what makes an unrecorded
	// transfer a WRONG answer rather than a stale one: nothing credits the old
	// holder, so an unrecorded task has no holder at all.
	governs, err := tracker.Journal().AuthorityGovernsTaskAt(provenance.JournalID(row.Authority), fixture.task, provenance.JournalID(math.MaxInt64))
	if err != nil {
		t.Fatalf("ask whether the recorded authority governs: %v", err)
	}
	if !governs {
		t.Fatalf("the authority the record holds does not govern the task; the record would be read and give a wrong answer")
	}

	// 3. PASTURE'S OWN FACT EXISTS AND CARRIES THE AUTHORITY ID, which is what
	// lets the episode be found again after the record is rebuilt from history.
	page, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy:    provenance.OrderByJournalID,
		TaskIDs:    []provenance.TaskID{fixture.task},
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:      16,
	})
	if err != nil {
		t.Fatalf("read the assignment-start facts: %v", err)
	}
	var carried int64
	for _, event := range page.Events {
		started, err := decodeAssignmentStart(event.Payload)
		if err != nil {
			t.Fatalf("decode an assignment-start fact: %v", err)
		}
		if started.Assignment == "owner-b" {
			carried = started.AuthorityJournalID
		}
	}
	if carried == 0 {
		t.Fatalf("no assignment-start fact for the transferred episode carries an authority id; a rebuild from history could not find this episode, because a transfer leaves nothing else to find")
	}
	if carried != row.Authority {
		t.Fatalf("the fact carries authority %d and the record holds %d; want the same id", carried, row.Authority)
	}
}

// TestARepeatedTransferResolvesToTheSameEpisode pins the repeat resolution: a
// task that has been transferred once has two owner facts, and the repeat must
// still resolve, by setting aside the episode it created.
//
// RED when: the repeat is refused as ambiguous, which is what happens if
// uniqueness is required over every fact instead of over the remainder.
func TestARepeatedTransferResolvesToTheSameEpisode(t *testing.T) {
	fixture := newTaskAssignmentTransferFixture(t)
	fixture.seedOwnerAssignment(t, "owner-a")
	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           fixture.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "owner-b",
		ActorID:          fixture.actorA,
		NextOccupant:     fixture.actorB,
	}
	first, err := fixture.tracker.TransferTaskAssignment(t.Context(), request)
	if err != nil {
		t.Fatalf("first transfer: %v", err)
	}
	repeat, err := fixture.tracker.TransferTaskAssignment(t.Context(), request)
	if err != nil {
		t.Fatalf("repeated transfer: %v; a task transferred once carries two owner facts, and the repeat must set aside the one it created rather than refuse the task as ambiguous", err)
	}
	if !repeat.Replayed {
		t.Fatalf("the repeated transfer was not reported as a repeat")
	}
	if repeat.Previous.AssignmentID != first.Previous.AssignmentID || repeat.Next.AssignmentID != first.Next.AssignmentID {
		t.Fatalf("the repeat resolved to %+v; want the same episodes as the first attempt %+v", repeat, first)
	}
	if rows := countIndexRows(t, fixture.tracker); rows != 1 {
		t.Fatalf("two transfers of one task left %d record(s); want 1, because the repeat re-records the same episode", rows)
	}
}

// TestEveryReviewBatchChildIsRecordedInTheDeclaredSlot pins the site the
// structural guard found: a start-review allocation creates several holders at
// once, and every one is recorded, in the slot the batch declares.
//
// RED when: a child is not recorded, or is recorded in a slot the batch did not
// declare.
func TestEveryReviewBatchChildIsRecordedInTheDeclaredSlot(t *testing.T) {
	if reviewBatchChildSlot != RoleAxisReviewer {
		t.Fatalf("the review batch declares slot %s; the approved policy gives every child of a review the reviewer slot", reviewBatchChildSlot)
	}
	if !reviewBatchChildSlot.valid() {
		t.Fatalf("the review batch declares a slot that is not a known one")
	}
}

type transferIndexRowValues struct {
	Actor     string
	Task      string
	Role      string
	Authority int64
}

func transferIndexRow(t *testing.T, tracker *trackerImpl, assignment string) transferIndexRowValues {
	t.Helper()
	var row transferIndexRowValues
	err := tracker.auditDB.QueryRow(
		`SELECT actor_id, task_id, role, authority_journal_id FROM pasture_actor_assignment WHERE assignment_id = ?`,
		assignment).Scan(&row.Actor, &row.Task, &row.Role, &row.Authority)
	if err != nil {
		return transferIndexRowValues{}
	}
	return row
}
