package tasks

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

type recoveryCountJournal struct {
	provenance.Journal
	starts                                                                 provenance.AssignmentStartQueryAPI
	assignmentCalls, materialCalls, evidenceCalls, lookupCalls, auditCalls int
	boundaries                                                             []provenance.JournalID
	beforeAudit                                                            func() error
	afterAssignmentQuery                                                   func(provenance.AssignmentStartPage) error
	beforeLookup                                                           func(provenance.OperationID) error
}

func TestGateRecoveryReachabilityExcludesOperatorAndPrivateJournalReads(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	functions := map[string]*ast.FuncDecl{}
	for _, path := range files {
		isTest, err := filepath.Match("*_test.go", path)
		require.NoError(t, err)
		if isTest {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				functions[function.Name.Name] = function
			}
		}
	}
	forbidden := map[string]bool{
		"LookupCommitted": true, "VerifyIntegrity": true, "ReplayProjections": true,
		"TaskAttributions": true, "commandResultFromCommitted": true,
		"RebuildAssignmentIndex": true, "authenticateOperatorTransfers": true,
	}
	privateSQL := regexp.MustCompile(`(?i)\b(?:from|join|into|update)\s+(?:journal(?:\b|_)|tasks\b|edges\b|agents\b)`)
	visited := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		function := functions[name]
		require.NotNil(t, function, "gate reachability must inspect %s", name)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				text, err := strconv.Unquote(literal.Value)
				require.NoError(t, err)
				require.False(t, privateSQL.MatchString(text), "private journal SQL reachable through %s", name)
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			var target string
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				target = callee.Name
			case *ast.SelectorExpr:
				target = callee.Sel.Name
			}
			require.False(t, forbidden[target], "%s reaches forbidden %s", name, target)
			if functions[target] != nil && target != "Error" && target != "String" {
				walk(target)
			}
			return true
		})
	}
	walk("prepareAssignmentCatchUp")
	walk("persistAssignmentCatchUp")
	require.True(t, visited["authenticateRecoveryRows"])
	require.True(t, visited["normalizeRecoveryJSON"])
	require.True(t, visited["applyRecoveryPrefixTx"])
	require.False(t, visited["authenticateOperatorTransfers"])
	// A positive control ensures the private-table detector is not vacuous.
	require.True(t, privateSQL.MatchString("SELECT value FROM journal_evidence"))
}

func (j *recoveryCountJournal) QueryAssignmentStarts(q provenance.AssignmentStartQuery) (provenance.AssignmentStartPage, error) {
	j.assignmentCalls++
	page, err := j.starts.QueryAssignmentStarts(q)
	if err == nil && j.afterAssignmentQuery != nil {
		callback := j.afterAssignmentQuery
		j.afterAssignmentQuery = nil
		err = callback(page)
	}
	return page, err
}
func (j *recoveryCountJournal) QueryTaskEvents(q provenance.JournalQueryV1) (provenance.JournalTaskEventPageV1, error) {
	j.materialCalls++
	if len(q.TaskIDs) == 0 {
		return provenance.JournalTaskEventPageV1{}, fmt.Errorf("unfiltered material query")
	}
	return j.Journal.QueryTaskEvents(q)
}
func (j *recoveryCountJournal) Facts() provenance.FactQueryAPI {
	return recoveryCountFacts{j.Journal.Facts(), j}
}
func (j *recoveryCountJournal) LookupCommitted(op provenance.OperationID) (provenance.CommittedResult, error) {
	j.lookupCalls++
	if j.beforeLookup != nil {
		if err := j.beforeLookup(op); err != nil {
			return provenance.CommittedResult{}, err
		}
	}
	return j.Journal.LookupCommitted(op)
}

func TestOperatorTransferReceiptAfterSnapshotIsNotPublished(t *testing.T) {
	t.Parallel()
	fixture := newTaskAssignmentTransferFixture(t)
	fixture.seedOwnerAssignment(t, "snapshot-before")
	store := fixture.tracker
	prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	_, err = store.auditDB.Exec(`CREATE TRIGGER hold_snapshot_transfer BEFORE INSERT ON pasture_actor_assignment WHEN NEW.assignment_id='snapshot-after' BEGIN SELECT RAISE(ABORT,'held index'); END`)
	require.NoError(t, err)
	request := protocol.TransferTaskAssignmentRequest{
		TaskID: fixture.task, Slot: provenance.SlotOwnerResponsibility,
		NextAssignmentID: "snapshot-after", ActorID: fixture.actorA, NextOccupant: fixture.actorB,
	}
	_, err = store.TransferTaskAssignment(t.Context(), request)
	require.Error(t, err)
	_, err = store.auditDB.Exec(`DROP TRIGGER hold_snapshot_transfer`)
	require.NoError(t, err)
	starts, err := store.Journal().(provenance.AssignmentStartQueryAPI).QueryAssignmentStarts(provenance.AssignmentStartQuery{
		AssignmentIDs: []provenance.AssignmentID{"snapshot-after"},
		Page:          provenance.AssignmentStartPageRequest{Limit: 64},
	})
	require.NoError(t, err)
	require.Len(t, starts.Rows, 1)
	start := starts.Rows[0]
	material, err := MapMaterialEvent(AssignmentStartedEvent{
		Task: start.TaskID, Assignment: start.AssignmentID, Role: RoleOwnerResponsibility, Occupant: start.Occupant,
	})
	require.NoError(t, err)
	material.Payload, err = canonicalJSON(assignmentStartPayload{
		Assignment: string(start.AssignmentID), Role: RoleOwnerResponsibility.String(),
		Occupant: start.Occupant.String(), AuthorityJournalID: int64(start.AuthorityJournalID),
	})
	require.NoError(t, err)
	_, err = store.Journal().Apply(provenance.OperationInput{
		OperationID: "uncertified-transfer-material", ActorID: start.Occupant,
		AuthorityJournalID: &start.AuthorityJournalID, CommandDigest: []byte("uncertified"),
		Effects: []provenance.Effect{material},
	})
	require.NoError(t, err)
	scan, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 64)
	require.NoError(t, err)
	facts, err := store.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{fixture.task},
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()}, Limit: 64,
		SnapshotMaxJournalID: scan.State.Snapshot,
	})
	require.NoError(t, err)
	original := store.prov
	journal := countRecoveryJournal(t, store)
	wrapped := store.prov
	operation := transferMaterialFactOperationID(taskAssignmentTransferOperationID(request))
	journal.beforeLookup = func(wanted provenance.OperationID) error {
		if wanted != operation {
			return nil
		}
		journal.beforeLookup = nil
		store.prov = original
		_, err := store.TransferTaskAssignment(t.Context(), request)
		store.prov = wrapped
		return err
	}
	err = authenticateOperatorTransfers(t.Context(), store, &scan.State, starts.Rows, facts.Events)
	require.ErrorContains(t, err, "operator transfer anchor")
	receipt, err := original.Journal().LookupCommitted(operation)
	require.NoError(t, err)
	require.Greater(t, receipt.AnchorJournalID, scan.State.Snapshot)
	cached, err := loadRecoveryCache(t.Context(), store.auditDB, scan.State, "transfer", string(start.ProducingOperationID))
	require.NoError(t, err)
	require.Empty(t, cached)
}
func (j *recoveryCountJournal) VerifyIntegrity() error {
	j.auditCalls++
	if j.beforeAudit != nil {
		if err := j.beforeAudit(); err != nil {
			return err
		}
	}
	return j.Journal.VerifyIntegrity()
}
func (j *recoveryCountJournal) AuthorityGovernsTaskAt(auth provenance.JournalID, task provenance.TaskID, before provenance.JournalID) (bool, error) {
	j.boundaries = append(j.boundaries, before)
	return j.Journal.AuthorityGovernsTaskAt(auth, task, before)
}

type recoveryCountFacts struct {
	provenance.FactQueryAPI
	journal *recoveryCountJournal
}

func (f recoveryCountFacts) QueryEvidence(q provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	f.journal.evidenceCalls++
	if len(q.Filter.OperationIDs) == 0 {
		return provenance.EvidencePage{}, fmt.Errorf("unfiltered evidence query")
	}
	return f.FactQueryAPI.QueryEvidence(q)
}

type recoveryCountTracker struct {
	provenance.Tracker
	journal provenance.Journal
}

func (t recoveryCountTracker) Journal() provenance.Journal { return t.journal }
func countRecoveryJournal(t *testing.T, store *trackerImpl) *recoveryCountJournal {
	t.Helper()
	old := store.prov
	j := &recoveryCountJournal{Journal: old.Journal(), starts: old.Journal().(provenance.AssignmentStartQueryAPI)}
	store.prov = recoveryCountTracker{old, j}
	t.Cleanup(func() { store.prov = old })
	return j
}

func TestOperatorRecoveryResetDuringRealAuditRefusesOldCompletion(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "audit-reset")
	task := createHumanTestTask(t, store, "task")
	seedFeasibilityEpisode(t, store, task, "audit-assignment", actor, "audit-start")
	j := countRecoveryJournal(t, store)
	j.beforeAudit = func() error {
		state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
		if err != nil {
			return err
		}
		_, err = resetAssignmentRecovery(t.Context(), store.auditDB, state)
		return err
	}
	require.Error(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Generation)
	require.Equal(t, assignmentCoverageRebuilding, state.Status)
	require.Zero(t, state.Through)
	require.Zero(t, state.After)
	j.beforeAudit = nil
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
}

func TestGateRecoverySkipsEmptyFiltersAndAggregatesQueries(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	j := countRecoveryJournal(t, store)
	prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.Equal(t, 1, j.assignmentCalls)
	require.Zero(t, j.materialCalls)
	require.Zero(t, j.evidenceCalls)
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	store.prov = store.prov.(recoveryCountTracker).Tracker
	actor := feasibilityActor(t, store, "aggregate")
	for i := 0; i < 3; i++ {
		task := createHumanTestTask(t, store, fmt.Sprintf("task-%d", i))
		seedFeasibilityEpisode(
			t,
			store,
			task,
			provenance.AssignmentID(fmt.Sprintf("episode-%d", i)),
			actor,
			provenance.OperationID(fmt.Sprintf("start-%d", i)),
		)
	}
	j = countRecoveryJournal(t, store)
	prepared, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.Equal(t, 1, j.assignmentCalls)
	require.Equal(t, 1, j.materialCalls)
	require.Zero(t, j.evidenceCalls, "same-Apply starts have no applicable supplement population")
	require.Len(t, prepared.Page.Rows, 3)
	for _, table := range []string{
		"pasture_assignment_recovery_scan",
		"pasture_assignment_recovery_cache",
		"pasture_assignment_recovery_member",
	} {
		_, err := store.auditDB.Exec(`CREATE TRIGGER refuse_gate_` + table + ` BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'gate auxiliary write'); END`)
		require.NoError(t, err)
	}
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	require.Zero(t, j.lookupCalls)
	require.Zero(t, j.auditCalls)
}

func TestGateRecoveryReviewerPredecessorCannotBecomeOwner(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "review-transfer")
	next := feasibilityActor(t, store, "next-review-transfer")
	task := createHumanTestTask(t, store, "task")
	seedAssignmentEpisode(t, store, task, "reviewer-before", RoleAxisReviewer, actor, "reviewer-start")
	prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.Len(t, prepared.Page.Rows, 1)
	old := prepared.Page.Rows[0]
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	_, err = store.prov.As(actor, old.Authority).TransferAssignment(
		provenance.AssignmentTransferRequest{
			TaskID:               task,
			SlotID:               provenance.SlotOwnerResponsibility,
			PreviousAssignmentID: "reviewer-before",
			NextAssignmentID:     "reviewer-after",
			NextOccupant:         next,
		},
		provenance.WithOperationID("reviewer-transfer"),
	)
	require.NoError(t, err)
	_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.ErrorContains(t, err, "certified owner predecessor")
}

func TestGateRecoveryConflictCommitsDirtyInItsOnlyAttempt(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "conflict-gate")
	task := createHumanTestTask(t, store, "task")
	seedFeasibilityEpisode(t, store, task, "gate-conflict", actor, "gate-start")
	prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.Len(t, prepared.Page.Rows, 1)
	row := prepared.Page.Rows[0]
	_, err = store.auditDB.Exec(
		`INSERT INTO pasture_actor_assignment(assignment_id,actor_id,task_id,role,authority_journal_id,generation) VALUES(?,?,?,?,?,?)`,
		string(row.Assignment),
		row.Actor.String(),
		row.Task.String(),
		row.Role.String(),
		row.Authority+1,
		prepared.State.Generation,
	)
	require.NoError(t, err)
	var stale *IndexStaleError
	require.ErrorAs(t, store.persistAssignmentCatchUp(t.Context(), prepared), &stale)
	state, err := readAssignmentRecoveryState(context.Background(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageDirty, state.Status)
	var authority provenance.JournalID
	require.NoError(
		t,
		store.auditDB.QueryRow(
			`SELECT authority_journal_id FROM pasture_actor_assignment WHERE assignment_id=?`,
			string(row.Assignment),
		).Scan(&authority),
	)
	require.Equal(t, row.Authority+1, authority)
}

func TestGateRecoveryTransferUsesHistoricalPredecessorWithoutReceipts(t *testing.T) {
	t.Parallel()
	f := newTaskAssignmentTransferFixture(t)
	f.seedOwnerAssignment(t, "before")
	store := f.tracker
	prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	_, err = store.auditDB.Exec(`CREATE TRIGGER hold_transfer_index BEFORE INSERT ON pasture_actor_assignment WHEN NEW.assignment_id='after' BEGIN SELECT RAISE(ABORT,'transfer crash gap'); END`)
	require.NoError(t, err)
	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           f.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "after",
		ActorID:          f.actorA,
		NextOccupant:     f.actorB,
	}
	_, err = store.TransferTaskAssignment(t.Context(), request)
	require.Error(t, err)
	_, err = store.auditDB.Exec(`DROP TRIGGER hold_transfer_index`)
	require.NoError(t, err)
	j := countRecoveryJournal(t, store)
	prepared, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.Len(t, prepared.Page.Rows, 1)
	require.Equal(t, 1, prepared.HistoricalPredicates)
	rows, err := j.starts.QueryAssignmentStarts(provenance.AssignmentStartQuery{Page: provenance.AssignmentStartPageRequest{Limit: 64}})
	require.NoError(t, err)
	var producer provenance.JournalID
	for _, row := range rows.Rows {
		if row.AssignmentID == "after" {
			producer = row.ProducingOperationJournalID
		}
	}
	require.Equal(t, []provenance.JournalID{producer}, j.boundaries)
	active, err := j.Journal.AuthorityGovernsTaskAt(prepared.Page.Rows[0].Authority, f.task, prepared.Page.Through+1)
	require.NoError(t, err)
	require.True(t, active, "successor current liveness is a separate predicate")
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	require.Zero(t, j.lookupCalls)
	require.Zero(t, j.auditCalls)
	store.prov = store.prov.(recoveryCountTracker).Tracker
	_, err = store.TransferTaskAssignment(t.Context(), request)
	require.NoError(t, err, "replay commits the second material operation after the recovered gap")
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true}))
}

func TestGateRecoverySameApplyDistinguishesProducerAndAssignee(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, wrongProducer, wrongOccupant bool) {
		store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
		defer store.Close()
		producer := feasibilityActor(t, store, "producer-a")
		occupant := feasibilityActor(t, store, "occupant-b")
		task := createHumanTestTask(t, store, "task")
		_, authority, found, err := readSystemIdentity(store.auditDB)
		require.NoError(t, err)
		require.True(t, found)
		materialOccupant := occupant
		if wrongOccupant {
			materialOccupant = producer
		}
		event, err := MapMaterialEvent(AssignmentStartedEvent{Task: task, Assignment: "assigned-b", Role: RoleOwnerResponsibility, Occupant: materialOccupant})
		require.NoError(t, err)
		effects := []provenance.Effect{
			{
				Sort:         provenance.EffectAssignmentStart,
				TaskID:       task,
				AssignmentID: "assigned-b",
				SlotID:       provenance.SlotOwnerResponsibility,
				Occupant:     occupant,
			},
		}
		if !wrongProducer {
			effects = append(effects, event)
		}
		_, err = store.Journal().Apply(provenance.OperationInput{
			OperationID:        "assign-b",
			ActorID:            producer,
			AuthorityJournalID: &authority,
			CommandDigest:      []byte("assign-b"),
			Effects:            effects,
		})
		require.NoError(t, err)
		if wrongProducer {
			_, err = store.Journal().Apply(provenance.OperationInput{
				OperationID:        "unrelated-material",
				ActorID:            producer,
				AuthorityJournalID: &authority,
				CommandDigest:      []byte("unrelated"),
				Effects:            []provenance.Effect{event},
			})
			require.NoError(t, err)
		}
		require.NoError(t, store.Journal().VerifyIntegrity())
		prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
		if wrongProducer || wrongOccupant {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Len(t, prepared.Page.Rows, 1)
		require.Equal(t, occupant, prepared.Page.Rows[0].Actor)
		require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	}
	t.Run("producer-A-assignee-B", func(t *testing.T) { run(t, false, false) })
	t.Run("wrong-producer", func(t *testing.T) { run(t, true, false) })
	t.Run("wrong-occupant", func(t *testing.T) { run(t, false, true) })
}

func TestGateRecoveryOverflowIsBoundedAndOperatorSeesLaterDuplicates(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "overflow")
	task := createHumanTestTask(t, store, "task")
	for i := 0; i < 65; i++ {
		seedFeasibilityEpisode(
			t,
			store,
			task,
			provenance.AssignmentID(fmt.Sprintf("overflow-%d", i)),
			actor,
			provenance.OperationID(fmt.Sprintf("overflow-start-%d", i)),
		)
	}
	j := countRecoveryJournal(t, store)
	_, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.Error(t, err)
	require.Equal(t, 1, j.assignmentCalls)
	require.Zero(t, j.materialCalls)
	require.Zero(t, j.evidenceCalls)
	store.prov = store.prov.(recoveryCountTracker).Tracker
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	require.Equal(t, 65, countIndexRows(t, store))
	seedFeasibilityEpisode(t, store, task, "overflow-last", actor, "overflow-last-start")
	j = countRecoveryJournal(t, store)
	_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.Error(t, err)
	require.Equal(t, 1, j.assignmentCalls)
	require.Equal(t, 1, j.materialCalls)
	require.Zero(t, j.evidenceCalls)
	store.prov = store.prov.(recoveryCountTracker).Tracker
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	require.Equal(t, 66, countIndexRows(t, store))
	seedFeasibilityEpisode(t, store, task, "overflow-bad-tail", actor, "overflow-bad-tail-start")
	_, authority, found, err := readSystemIdentity(store.auditDB)
	require.NoError(t, err)
	require.True(t, found)
	duplicate, err := MapMaterialEvent(AssignmentStartedEvent{Task: task, Assignment: "overflow-0", Role: RoleOwnerResponsibility, Occupant: actor})
	require.NoError(t, err)
	_, err = store.Journal().Apply(provenance.OperationInput{
		OperationID:        "late-duplicate",
		ActorID:            actor,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte("late-duplicate"),
		Effects:            []provenance.Effect{duplicate},
	})
	require.NoError(t, err)
	require.ErrorContains(
		t,
		RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}),
		"duplicate assignment material",
	)
}

func TestGateRecoveryEvidenceOverflowDoesNotFetchPageTwo(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "evidence-overflow")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "overflow-parent", RoleGoverningSupervisor, actor, "overflow-parent-start")
	store.allocationRunner = recoveryAlteredComposedRunner{store.allocationRunner, func(request *provenance.GovernedAllocationComposedRequest) {
		var extras []provenance.Effect
		for _, effect := range request.SupplementalEffects {
			if effect.Sort == provenance.EffectEvidence && effect.EvidenceKind == assignmentCommandEvidenceKind {
				for i := 0; i < 65; i++ {
					extra := effect
					extra.ResultSlot = provenance.ResultSlotID(fmt.Sprintf("duplicate-command-%d", i))
					extras = append(extras, extra)
				}
			}
		}
		request.SupplementalEffects = append(request.SupplementalEffects, extras...)
	}}
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	_, err = service.CreateSlice(
		t.Context(),
		CreateSliceInput{
			Meta:       CommandMeta{OperationID: "evidence-overflow-slice"},
			Epoch:      EpochRootID(epoch.String()),
			Plan:       plan,
			Assignment: "overflow-parent",
		},
	)
	require.NoError(t, err)
	j := countRecoveryJournal(t, store)
	_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.ErrorContains(t, err, "evidence exactness requires an operator drain")
	require.Equal(t, 1, j.assignmentCalls)
	require.Equal(t, 1, j.materialCalls)
	require.Equal(t, 1, j.evidenceCalls)
	require.Zero(t, j.lookupCalls)
	require.Zero(t, j.auditCalls)
	store.prov = store.prov.(recoveryCountTracker).Tracker
	require.ErrorContains(
		t,
		RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true}),
		"duplicate evidence",
	)
}

func TestAssignmentRecoveryFactoryInterleavesWithRealJournalWriter(t *testing.T) {
	t.Parallel()
	for _, otherInitializer := range []bool{false, true} {
		t.Run(fmt.Sprintf("other-initializer=%t", otherInitializer), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pasture.db")
			store := openHumanTestTracker(t, path)
			defer store.Close()
			actor := feasibilityActor(t, store, "factory-race")
			task := createHumanTestTask(t, store, "task")
			_, err := store.auditDB.Exec(`DELETE FROM pasture_actor_assignment_state`)
			require.NoError(t, err)
			journal := store.Journal()
			var boundary provenance.JournalID
			wrapper := &recoveryCountJournal{Journal: journal, starts: journal.(provenance.AssignmentStartQueryAPI)}
			wrapper.afterAssignmentQuery = func(page provenance.AssignmentStartPage) error {
				boundary = page.SnapshotMaxJournalID
				episode := seedFeasibilityEpisode(t, store, task, "factory-race-assignment", actor, "factory-race-start")
				if otherInitializer {
					opened, err := OpenTaskTracker(path)
					if err != nil {
						return err
					}
					defer opened.Close()
					other := opened.(*trackerImpl)
					return recordAssignmentStart(
						t.Context(),
						other.auditDB,
						startedEpisode{
							Assignment: "factory-race-assignment",
							Actor:      actor,
							Task:       task,
							Role:       RoleOwnerResponsibility,
							Authority:  episode.authority,
						},
					)
				}
				return nil
			}
			require.NoError(t, ensureAssignmentIndexStateOnOpen(t.Context(), store.auditDB, wrapper))
			state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
			require.NoError(t, err)
			if otherInitializer {
				require.Equal(t, assignmentCoverageRebuilding, state.Status)
				require.Greater(t, state.Revision, int64(1))
				return
			}
			require.Equal(t, assignmentCoverageValid, state.Status)
			require.Equal(t, boundary, state.Through)
			prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
			require.NoError(t, err)
			require.Len(t, prepared.Page.Rows, 1)
			require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
		})
	}
}
