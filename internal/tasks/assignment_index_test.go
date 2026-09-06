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
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
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
		{"no authority", func(e startedEpisode) startedEpisode { e.Authority = 0; return e }, "can never be evaluated"},
		{"no actor", func(e startedEpisode) startedEpisode { e.Actor = provenance.ActorID{}; return e }, "can never be found"},
		{"no task", func(e startedEpisode) startedEpisode { e.Task = provenance.TaskID{}; return e }, "says nothing a gate can use"},
		{"no assignment", func(e startedEpisode) startedEpisode { e.Assignment = ""; return e }, "would overwrite another episode"},
		{"unknown slot", func(e startedEpisode) startedEpisode { e.Role = AssignmentRole(99); return e }, "cannot be judged"},
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
// two, and that a re-record updates rather than duplicates.
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

	// A later record of the same episode under a higher authority replaces it.
	episode.Authority = 11
	if err := recordAssignmentStart(t.Context(), tracker.auditDB, episode); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if rows := countIndexRows(t, tracker); rows != 1 {
		t.Fatalf("re-recording one episode left %d row(s); want 1", rows)
	}
	var authority int64
	if err := tracker.auditDB.QueryRow(`SELECT authority_journal_id FROM pasture_actor_assignment WHERE assignment_id = ?`, "episode-1").Scan(&authority); err != nil {
		t.Fatalf("read back the record: %v", err)
	}
	if authority != 11 {
		t.Fatalf("the record holds authority %d after a re-record; want 11, the latest", authority)
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
