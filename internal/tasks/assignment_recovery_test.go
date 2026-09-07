package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func TestAssignmentRecoveryFactoryCertifiesOnlyAnEmptyPrefix(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	opened, err := OpenTaskTracker(path)
	require.NoError(t, err)
	store := opened.(*trackerImpl)
	defer store.Close()
	var status string
	var generation, revision, destructive, certified, complete int64
	require.NoError(t, store.auditDB.QueryRow(`SELECT coverage_status, generation, state_revision,
		destructive_revision, certified_destructive_revision, recovery_complete
		FROM pasture_actor_assignment_state WHERE singleton_id=0`).Scan(
		&status, &generation, &revision, &destructive, &certified, &complete))
	require.Equal(t, "valid", status)
	require.Zero(t, generation)
	require.Positive(t, revision)
	require.Equal(t, destructive, certified)
	require.EqualValues(t, 1, complete)
	var schemaBefore, schemaAfter int
	require.NoError(t, store.auditDB.QueryRow(`PRAGMA schema_version`).Scan(&schemaBefore))
	require.NoError(t, ensureAssignmentRecoverySchema(t.Context(), store.auditDB))
	require.NoError(t, store.auditDB.QueryRow(`PRAGMA schema_version`).Scan(&schemaAfter))
	require.Equal(t, schemaBefore, schemaAfter, "ordinary reopen must not rewrite existing trigger schema")
}

func TestAssignmentRecoveryEvidenceUsesProducerAndStoredRepresentations(t *testing.T) {
	t.Parallel()
	parent := deterministicTask("encoding-fixture", "parent")
	actor := provenance.ActorID{Namespace: parent.Namespace, UUID: parent.UUID}
	payload := struct {
		Plan       provenance.TaskID       `json:"plan"`
		Assignment provenance.AssignmentID `json:"assignment"`
	}{parent, "parent<&>"}
	originalPayload, err := canonicalJSON(payload)
	require.NoError(t, err)
	originalRequest, err := assignmentRequestCommand(MutationCreateSlice, EpochRootID(parent.String()), payload)
	require.NoError(t, err)
	record := assignmentCommandRecord{
		Mutation:   MutationCreateSlice,
		Epoch:      EpochRootID(parent.String()),
		Payload:    originalPayload,
		Request:    originalRequest,
		Assignment: payload.Assignment,
		Role:       RoleGoverningSupervisor,
		Occupant:   actor,
		Authority:  7,
		Task:       parent,
	}
	original, err := canonicalJSON(record)
	require.NoError(t, err)
	normalized, err := normalizeRecoveryJSON(original)
	require.NoError(t, err)
	digest := sha256.Sum256(original)
	row := provenance.EvidenceRow{
		TaskID:                      &parent,
		EffectiveActorID:            actor,
		EvidenceKind:                assignmentCommandEvidenceKind,
		Payload:                     normalized,
		ContentDigest:               digest[:],
		ProducingOperationID:        provenance.GovernedAllocationSupplementOperationID("encoding-fixture"),
		ProducingOperationJournalID: 9,
	}
	require.NotEqual(t, original, normalized)
	require.True(t, bytes.Contains(original, []byte("<&>")))
	require.True(t, bytes.Contains(normalized, []byte(`\u003c`)))

	_, err = decodeRecoveryCommand(row)
	require.NoError(t, err)

	var outer assignmentCommandRecord
	require.NoError(t, json.Unmarshal(normalized, &outer))
	outerBytes, err := canonicalJSON(outer)
	require.NoError(t, err)
	outerDigest := sha256.Sum256(outerBytes)
	require.NotEqual(t, digest, outerDigest)
	storedDigest := sha256.Sum256(normalized)
	require.NotEqual(t, digest, storedDigest)

	reject := func(t *testing.T, payload []byte, hash []byte) {
		t.Helper()
		bad := row
		bad.Payload = payload
		bad.ContentDigest = hash
		_, err := decodeRecoveryCommand(bad)
		require.Error(t, err)
	}
	t.Run("outer-only-digest", func(t *testing.T) {
		reject(t, normalized, outerDigest[:])
	})
	t.Run("stored-digest", func(t *testing.T) {
		reject(t, normalized, storedDigest[:])
	})
	t.Run("tampered-digest", func(t *testing.T) {
		reject(t, normalized, bytes.Repeat([]byte{0}, sha256.Size))
	})
	t.Run("trailing", func(t *testing.T) {
		reject(t, append(append([]byte{}, normalized...), []byte(` {}`)...), digest[:])
	})
	t.Run("duplicate-nested-key", func(t *testing.T) {
		reject(t, []byte(strings.Replace(string(normalized), `"plan":`, `"plan":null,"plan":`, 1)), digest[:])
	})
	t.Run("unknown-nested-member", func(t *testing.T) {
		bad, err := normalizeRecoveryJSON([]byte(strings.Replace(string(normalized), `"payload":{`, `"payload":{"unknown":true,`, 1)))
		require.NoError(t, err)
		reject(t, bad, digest[:])
	})
	t.Run("explicit-null", func(t *testing.T) {
		var value map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(normalized, &value))
		value["authority"] = json.RawMessage(`null`)
		bad, err := json.Marshal(value)
		require.NoError(t, err)
		reject(t, bad, digest[:])
	})
	t.Run("omitted-authority", func(t *testing.T) {
		var value map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(normalized, &value))
		delete(value, "authority")
		bad, err := json.Marshal(value)
		require.NoError(t, err)
		reject(t, bad, digest[:])
	})
	t.Run("exponent-integer", func(t *testing.T) {
		reject(t, []byte(strings.Replace(string(normalized), `"authority":7`, `"authority":7e0`, 1)), digest[:])
	})
	t.Run("request-mismatch", func(t *testing.T) {
		badRecord := record
		badRecord.Request = []byte(`{"mutation":5,"epoch":"wrong","payload":{}}`)
		badOriginal, err := canonicalJSON(badRecord)
		require.NoError(t, err)
		bad, err := normalizeRecoveryJSON(badOriginal)
		require.NoError(t, err)
		badDigest := sha256.Sum256(badOriginal)
		reject(t, bad, badDigest[:])
	})
}

func TestAssignmentRecoveryOrdinaryConflictCommitsDirtyWithoutOverwrite(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "ordinary-conflict")
	task := createHumanTestTask(t, store, "ordinary-conflict")
	state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	row := startedEpisode{
		Assignment: "conflict",
		Actor:      actor,
		Task:       task,
		Role:       RoleOwnerResponsibility,
		Authority:  state.Through + 1,
	}
	require.NoError(t, recordAssignmentStart(t.Context(), store.auditDB, row))
	require.NoError(t, recordAssignmentStart(t.Context(), store.auditDB, row))
	row.Authority++
	err = recordAssignmentStart(t.Context(), store.auditDB, row)
	var stale *IndexStaleError
	require.ErrorAs(t, err, &stale)
	actual, err := readAssignmentRecoveryState(context.Background(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageDirty, actual.Status)
	require.Greater(t, actual.Destructive, actual.CertifiedDestructive)
	var authority provenance.JournalID
	require.NoError(
		t,
		store.auditDB.QueryRow(`SELECT authority_journal_id FROM pasture_actor_assignment WHERE assignment_id='conflict'`).Scan(&authority),
	)
	require.Equal(t, row.Authority-1, authority)
	require.ErrorAs(t, recordAssignmentStart(t.Context(), store.auditDB, row), &stale)
}

func TestAssignmentRecoveryResetAndValidTailResume(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	store := openHumanTestTracker(t, path)
	actor := feasibilityActor(t, store, "resume")
	for i := 0; i < 3; i++ {
		task := createHumanTestTask(t, store, fmt.Sprintf("resume-%d", i))
		seedFeasibilityEpisode(
			t,
			store,
			task,
			provenance.AssignmentID(fmt.Sprintf("resume-%d", i)),
			actor,
			provenance.OperationID(fmt.Sprintf("start-%d", i)),
		)
	}
	scan, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 2)
	require.NoError(t, err)
	require.NotNil(t, scan.Page.Next)
	var proof assignmentRecoveryProof
	for _, row := range scan.Page.Rows {
		material, err := drainRecoveryMaterial(t.Context(), store.auditDB, store.Journal(), &scan.State, row.TaskID)
		require.NoError(t, err)
		require.NotEmpty(t, material)
		proof.Rows = append(
			proof.Rows,
			startedEpisode{
				Assignment: row.AssignmentID,
				Actor:      row.Occupant,
				Task:       row.TaskID,
				Role:       RoleOwnerResponsibility,
				Authority:  row.AuthorityJournalID,
			},
		)
	}
	require.NotEmpty(t, proof.Rows, "nonterminal crash point must include durable material progress")
	state, err := publishAssignmentRecoveryPage(t.Context(), store.auditDB, scan, proof)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageValid, state.Status)
	require.False(t, state.Complete)
	require.NoError(t, store.Close())
	store = openHumanTestTracker(t, path)
	defer store.Close()
	task := createHumanTestTask(t, store, "after-snapshot")
	seedFeasibilityEpisode(t, store, task, "later", actor, "later-start")
	resumed, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 2)
	require.NoError(t, err)
	require.Equal(t, state.Snapshot, resumed.Page.SnapshotMaxJournalID)
	require.Equal(t, state.After, resumed.State.After)
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{PageSize: 2}))
	completed, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, state.Snapshot, completed.Through)
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{PageSize: 2}))
	later, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Greater(t, later.Through, completed.Through)
	publicRows, err := store.Journal().(provenance.AssignmentStartQueryAPI).QueryAssignmentStarts(provenance.AssignmentStartQuery{Page: provenance.AssignmentStartPageRequest{Limit: 64}})
	require.NoError(t, err)
	require.Nil(t, publicRows.Next)
	var expected []startedEpisode
	for _, row := range publicRows.Rows {
		expected = append(
			expected,
			startedEpisode{
				Assignment: row.AssignmentID,
				Actor:      row.Occupant,
				Task:       row.TaskID,
				Role:       RoleOwnerResponsibility,
				Authority:  row.AuthorityJournalID,
			},
		)
	}
	_, err = store.auditDB.Exec(`DELETE FROM pasture_actor_assignment WHERE assignment_id='later'`)
	require.NoError(t, err)
	dirty, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageDirty, dirty.Status)
	require.Error(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	require.NoError(
		t,
		RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}),
	)
	reset, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, dirty.Generation+1, reset.Generation)
	require.Equal(t, assignmentCoverageValid, reset.Status)
	assertIndexPage(t, store, assignmentIndexPage{Through: reset.Through, Rows: expected})
	_, err = resetAssignmentRecovery(t.Context(), store.auditDB, dirty)
	require.Error(t, err)
}

func TestAssignmentRecoveryAuxiliaryRowsAndCursorAreAtomic(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "aux-atomic")
	task := createHumanTestTask(t, store, "task")
	seedFeasibilityEpisode(t, store, task, "aux-assignment", actor, "aux-start")
	scan, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 64)
	require.NoError(t, err)
	after, complete, err := openRecoveryAux(t.Context(), store.auditDB, &scan.State, "material", task.String())
	require.NoError(t, err)
	require.False(t, complete)
	page, err := store.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy:              provenance.OrderByJournalID,
		TaskIDs:              []provenance.TaskID{task},
		EventKinds:           []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:                64,
		SnapshotMaxJournalID: scan.State.Snapshot,
	})
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	value, err := json.Marshal(page.Events[0])
	require.NoError(t, err)
	cache := []recoveryCacheRow{{page.Events[0].JournalID, value}}
	_, err = store.auditDB.Exec(`CREATE TRIGGER refuse_aux_cursor BEFORE UPDATE ON pasture_assignment_recovery_scan WHEN NEW.kind='material' BEGIN SELECT RAISE(ABORT,'aux cursor failure'); END`)
	require.NoError(t, err)
	err = commitRecoveryAux(
		t.Context(),
		store.auditDB,
		&scan.State,
		"material",
		task.String(),
		after,
		scan.State.Snapshot,
		true,
		cache,
	)
	require.Error(t, err)
	values, err := loadRecoveryCache(t.Context(), store.auditDB, scan.State, "material", task.String())
	require.NoError(t, err)
	require.Empty(t, values, "cursor failure must roll back the cache insertion")
	_, err = store.auditDB.Exec(`DROP TRIGGER refuse_aux_cursor`)
	require.NoError(t, err)
	require.NoError(
		t,
		commitRecoveryAux(
			t.Context(),
			store.auditDB,
			&scan.State,
			"material",
			task.String(),
			after,
			scan.State.Snapshot,
			true,
			cache,
		),
	)
	values, err = loadRecoveryCache(t.Context(), store.auditDB, scan.State, "material", task.String())
	require.NoError(t, err)
	require.Equal(t, [][]byte{value}, values)
	old := scan.State
	next, err := resetAssignmentRecovery(t.Context(), store.auditDB, old)
	require.NoError(t, err)
	require.Error(
		t,
		commitRecoveryAux(t.Context(), store.auditDB, &old, "material", task.String(), after, old.Snapshot, true, cache),
	)
	values, err = loadRecoveryCache(t.Context(), store.auditDB, next, "material", task.String())
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestAssignmentRecoverySnapshotRolloverDoesNotReuseAuxiliaryCompletion(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "rollover")
	task := createHumanTestTask(t, store, "task")
	seedFeasibilityEpisode(t, store, task, "rollover-one", actor, "rollover-start-one")
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	first, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	seedFeasibilityEpisode(t, store, task, "rollover-two", actor, "rollover-start-two")
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	second, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Greater(t, second.Through, first.Through)
	var count int
	require.NoError(
		t,
		store.auditDB.QueryRow(
			`SELECT COUNT(*) FROM pasture_assignment_recovery_cache WHERE generation=? AND snapshot_jid=? AND kind='material' AND identity=?`,
			second.Generation,
			second.Through,
			task.String(),
		).Scan(&count),
	)
	require.Equal(t, 2, count, "new snapshot must scan both material facts, not reuse the old complete flag")
}

func TestAssignmentRecoveryFactoryDoesNotCertifyLegacyOrUnexplainedRows(t *testing.T) {
	t.Parallel()
	t.Run("journal-start-below-missing-state", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pasture.db")
		store := openHumanTestTracker(t, path)
		actor := feasibilityActor(t, store, "legacy")
		task := createHumanTestTask(t, store, "task")
		seedFeasibilityEpisode(t, store, task, "legacy-assignment", actor, "legacy-start")
		_, err := store.auditDB.Exec(`DELETE FROM pasture_actor_assignment_state`)
		require.NoError(t, err)
		require.NoError(t, store.Close())
		store = openHumanTestTracker(t, path)
		defer store.Close()
		state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
		require.NoError(t, err)
		require.Equal(t, assignmentCoverageRebuilding, state.Status)
		_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
		require.Error(t, err)
		require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	})
	t.Run("local-row-without-journal-start", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pasture.db")
		store := openHumanTestTracker(t, path)
		actor := feasibilityActor(t, store, "local-only")
		task := createHumanTestTask(t, store, "task")
		_, err := store.auditDB.Exec(
			`INSERT INTO pasture_actor_assignment(assignment_id,actor_id,task_id,role,authority_journal_id) VALUES('local-only',?,?,?,99)`,
			actor.String(),
			task.String(),
			RoleOwnerResponsibility.String(),
		)
		require.NoError(t, err)
		_, err = store.auditDB.Exec(`DELETE FROM pasture_actor_assignment_state`)
		require.NoError(t, err)
		require.NoError(t, store.Close())
		store = openHumanTestTracker(t, path)
		defer store.Close()
		state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
		require.NoError(t, err)
		require.Equal(t, assignmentCoverageRebuilding, state.Status)
		require.Error(
			t,
			RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}),
			"unexplained legacy index rows cannot survive certification",
		)
		require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true}))
		require.Zero(t, countIndexRows(t, store))
	})
}

func TestAssignmentRecoveryPinnedZeroScanAndRebuildingInvalidation(t *testing.T) {
	t.Parallel()
	opened, err := OpenTaskTracker(filepath.Join(t.TempDir(), "pasture.db"))
	require.NoError(t, err)
	store := opened.(*trackerImpl)
	defer store.Close()
	scan, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 64)
	require.NoError(t, err)
	require.Zero(t, scan.State.Snapshot)
	actor := feasibilityActor(t, store, "pinned-zero")
	task := createHumanTestTask(t, store, "task")
	episode := seedFeasibilityEpisode(t, store, task, "pinned-zero", actor, "pinned-start")
	resumed, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 64)
	require.NoError(t, err)
	require.Zero(t, resumed.Page.SnapshotMaxJournalID)
	require.Empty(t, resumed.Page.Rows)
	state, err := resetAssignmentRecovery(t.Context(), store.auditDB, resumed.State)
	require.NoError(t, err)
	require.NoError(
		t,
		recordAssignmentStart(
			t.Context(),
			store.auditDB,
			startedEpisode{
				Assignment: "pinned-zero",
				Actor:      actor,
				Task:       task,
				Role:       RoleOwnerResponsibility,
				Authority:  episode.authority,
			},
		),
	)
	_, err = store.auditDB.Exec(`UPDATE pasture_actor_assignment SET role=role WHERE assignment_id='pinned-zero'`)
	require.NoError(t, err)
	unchanged, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageRebuilding, unchanged.Status)
	_, err = store.auditDB.Exec(
		`UPDATE pasture_actor_assignment SET role=? WHERE assignment_id='pinned-zero'`,
		RoleGoverningSupervisor.String(),
	)
	require.NoError(t, err)
	dirty, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageDirty, dirty.Status)
	require.Equal(t, state.Generation, dirty.Generation)
	require.Error(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	reset, err := resetAssignmentRecovery(t.Context(), store.auditDB, dirty)
	require.NoError(t, err)
	_, err = store.auditDB.Exec(
		`INSERT INTO pasture_actor_assignment(assignment_id,actor_id,task_id,role,authority_journal_id,generation) VALUES('old-import',?,?,?,?,?)`,
		actor.String(),
		task.String(),
		RoleOwnerResponsibility.String(),
		episode.authority,
		reset.Generation-1,
	)
	require.NoError(t, err)
	_, err = store.auditDB.Exec(
		`UPDATE pasture_actor_assignment SET generation=? WHERE assignment_id='old-import'`,
		reset.Generation,
	)
	require.NoError(t, err)
	imported, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(
		t,
		assignmentCoverageDirty,
		imported.Status,
		"import into current generation must invalidate even when OLD generation differed",
	)
	_, err = store.auditDB.Exec(
		`INSERT INTO pasture_actor_assignment(assignment_id,actor_id,task_id,role,authority_journal_id,generation) VALUES('future-import',?,?,?,?,?)`,
		actor.String(),
		task.String(),
		RoleOwnerResponsibility.String(),
		episode.authority,
		imported.Generation+1,
	)
	require.NoError(t, err)
	clean, err := resetAssignmentRecovery(t.Context(), store.auditDB, imported)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageRebuilding, clean.Status)
	require.Zero(t, clean.Destructive)
	require.Zero(t, countIndexRows(t, store))
}

func TestAssignmentRecoveryRealComposedCommandsAndSplitReview(t *testing.T) {
	t.Parallel()
	runComposedRecoveryCommands(t, false)
}

func TestLegacyImplementationReviewRecoversThirteenMembers(t *testing.T) {
	t.Parallel()
	runComposedRecoveryCommands(t, true)
}

func runComposedRecoveryCommands(t *testing.T, legacyReview bool) {
	t.Helper()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "composed-recovery")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "supervisor", RoleGoverningSupervisor, actor, "supervisor-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	slice, err := service.CreateSlice(
		t.Context(),
		CreateSliceInput{
			Meta:       CommandMeta{OperationID: "recover-slice"},
			Epoch:      EpochRootID(epoch.String()),
			Plan:       plan,
			Assignment: "supervisor",
		},
	)
	require.NoError(t, err)
	prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	commit := provenance.GitOID("0123456789abcdef0123456789abcdef01234567")
	// Use the native composed owner returned by CreateSlice, not an adjacent
	// same-Apply substitute.
	member, err := service.SetSliceCandidate(
		t.Context(),
		SetSliceCandidateInput{
			Meta:       CommandMeta{OperationID: "recover-member"},
			Epoch:      EpochRootID(epoch.String()),
			Slice:      slice.Slice,
			Repository: "repo-z",
			Commit:     commit,
			Assignment: "recover-slice-slice-owner",
		},
	)
	require.NoError(t, err)
	secondSlice, err := service.CreateSlice(
		t.Context(),
		CreateSliceInput{
			Meta:       CommandMeta{OperationID: "recover-second-slice"},
			Epoch:      EpochRootID(epoch.String()),
			Plan:       plan,
			Assignment: "supervisor",
		},
	)
	require.NoError(t, err)
	secondMember, err := service.SetSliceCandidate(
		t.Context(),
		SetSliceCandidateInput{
			Meta:       CommandMeta{OperationID: "recover-second-member"},
			Epoch:      EpochRootID(epoch.String()),
			Slice:      secondSlice.Slice,
			Repository: "repo-a",
			Commit:     commit,
			Assignment: "recover-second-slice-slice-owner",
		},
	)
	require.NoError(t, err)
	// Deliberately reverse repository order. Recovery must reconstruct the
	// recorded command order, not the separately sorted manifest order.
	integration, err := service.CreateIntegrationCandidate(
		t.Context(),
		CreateIntegrationCandidateInput{
			Meta:       CommandMeta{OperationID: "recover-integration"},
			Epoch:      EpochRootID(epoch.String()),
			Plan:       plan,
			Assignment: "supervisor",
			Repositories: []RepositoryCandidate{
				{Repository: "repo-z", Candidate: member.Candidate, Commit: commit},
				{Repository: "repo-a", Candidate: secondMember.Candidate, Commit: commit},
			},
		},
	)
	require.NoError(t, err)
	// StartReview requires one governing role in scope. Retain the plan's active
	// lineage through a non-governing owner while it starts the native batch.
	integrationTask, err := provenance.ParseTaskID(string(integration.Candidate))
	require.NoError(t, err)
	endAssignmentEpisode(
		t,
		store,
		integrationTask,
		"recover-integration-candidate-owner",
		actor,
		"native-integration-governor-end",
	)
	seedRecoveryAssignmentWithParent(t, store, integrationTask, "integration-review-owner",
		RoleOwnerResponsibility, actor, "integration-review-owner-start", "supervisor")
	if legacyReview {
		store.allocationRunner = legacyRecoveryReviewRunner{composedAllocationRunner: store.allocationRunner}
	}
	started, err := service.StartReview(
		t.Context(),
		StartReviewInput{
			Meta:    CommandMeta{OperationID: "recover-review"},
			Epoch:   EpochRootID(epoch.String()),
			Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: string(integration.Candidate)},
		},
	)
	require.NoError(t, err)
	// FinalizeReview has an explicit subject-scoped governing assignment. Adding
	// it after StartReview avoids ambiguity without altering the native 13 graph.
	seedRecoveryAssignmentWithParent(t, store, integrationTask, "integration-finalizer",
		RoleGoverningSupervisor, actor, "integration-finalizer-start", "supervisor")
	j := countRecoveryJournal(t, store)
	prepared, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.Equal(t, 1, j.assignmentCalls)
	require.Equal(t, 1, j.materialCalls)
	require.Equal(t, 1, j.evidenceCalls)
	require.Zero(t, j.lookupCalls)
	require.Zero(t, j.auditCalls)
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	store.prov = store.prov.(recoveryCountTracker).Tracker
	require.NoError(
		t,
		RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}),
	)
	var count int
	require.NoError(
		t,
		store.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_actor_assignment WHERE assignment_id LIKE 'recover-review-%'`).Scan(&count),
	)
	require.Equal(t, 13, count)
	state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.True(t, state.Complete)
	require.Equal(t, assignmentCoverageValid, state.Status)
	if legacyReview {
		return
	}

	// Exercise both actual replacement writers after a real finalized review,
	// rather than synthesizing command evidence shaped like a replacement.
	rework := finishRecoveryReviewWithDeferredFinding(
		t,
		store,
		service,
		EpochRootID(epoch.String()),
		started,
		"integration-finalizer",
		actor,
		"integration-finish",
	)
	assertCurrentReworkReaderRefusals(t, store, service, EpochRootID(epoch.String()), integrationTask, rework)
	replacementInput := ReworkIntegrationCandidateInput{
		Meta:       CommandMeta{OperationID: "recover-integration-replacement"},
		Epoch:      EpochRootID(epoch.String()),
		Candidate:  integration.Candidate,
		Assignment: "supervisor",
		Replacement: IntegrationCandidateReplacement{Repositories: []RepositoryCandidate{
			{Repository: "repo-z", Candidate: member.Candidate, Commit: commit},
			{Repository: "repo-a", Candidate: secondMember.Candidate, Commit: commit},
		}},
		Rework: rework,
	}
	childOnly := replacementInput
	childOnly.Meta.OperationID = "child-only-replacement"
	childOnly.Assignment = "integration-finalizer"
	_, err = service.ReworkIntegrationCandidate(t.Context(), childOnly)
	require.Error(t, err, "candidate authority cannot mutate its plan")
	missing, err := store.Journal().LookupCommitted(childOnly.Meta.OperationID)
	require.NoError(t, err)
	require.Equal(t, provenance.CommittedAbsent, missing.Kind)
	seedAssignmentEpisode(
		t,
		store,
		plan,
		"unrelated-plan-governor",
		RoleGoverningSupervisor,
		actor,
		"unrelated-plan-governor-start",
	)
	wrongLineage := replacementInput
	wrongLineage.Meta.OperationID = "wrong-lineage-replacement"
	wrongLineage.Assignment = "unrelated-plan-governor"
	_, err = service.ReworkIntegrationCandidate(t.Context(), wrongLineage)
	require.ErrorContains(t, err, "does not govern the old integration candidate")
	replacement, err := service.ReworkIntegrationCandidate(t.Context(), replacementInput)
	require.NoError(t, err)
	replayed, err := service.ReworkIntegrationCandidate(t.Context(), replacementInput)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	changed := replacementInput
	changed.Replacement.Repositories = append([]RepositoryCandidate(nil), replacementInput.Replacement.Repositories...)
	changed.Replacement.Repositories[0].Commit = "1123456789abcdef0123456789abcdef01234567"
	_, err = service.ReworkIntegrationCandidate(t.Context(), changed)
	require.Error(t, err, "changed replacement retry cannot alter a committed operation")
	require.NoError(
		t,
		RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}),
	)
	replacementTask, err := provenance.ParseTaskID(string(replacement.Candidate))
	require.NoError(t, err)
	require.Equal(
		t,
		replacementTask.String(),
		transferIndexRow(t, store, "recover-integration-replacement-candidate-owner").Task,
	)
}

type reworkSubmissionReadJournal struct {
	provenance.Journal
	queries []provenance.EvidenceQuery
	change  func(provenance.EvidenceQuery, *provenance.EvidencePage)
}

func (j *reworkSubmissionReadJournal) Facts() provenance.FactQueryAPI {
	return reworkSubmissionReadFacts{FactQueryAPI: j.Journal.Facts(), journal: j}
}

type reworkSubmissionReadFacts struct {
	provenance.FactQueryAPI
	journal *reworkSubmissionReadJournal
}

func (f reworkSubmissionReadFacts) QueryEvidence(query provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	page, err := f.FactQueryAPI.QueryEvidence(query)
	if err != nil {
		return page, err
	}
	if len(query.Kinds) != 1 || query.Kinds[0] != reviewSubmissionEvidenceKind {
		return page, nil
	}
	f.journal.queries = append(f.journal.queries, query)
	if f.journal.change != nil {
		// These are read-boundary corruption tests over genuine persisted rows.
		// They neither modify the store nor claim that impossible metadata (such
		// as a later row of an already committed operation) is publicly writable.
		page.Rows = append([]provenance.EvidenceRow(nil), page.Rows...)
		for i := range page.Rows {
			page.Rows[i].Payload = append([]byte(nil), page.Rows[i].Payload...)
		}
		f.journal.change(query, &page)
	}
	return page, nil
}

func assertCurrentReworkReaderRefusals(
	t *testing.T,
	store *trackerImpl,
	service EpochService,
	epoch EpochRootID,
	candidate provenance.TaskID,
	submission ReworkSubmission,
) {
	t.Helper()
	reader := service.(*epochService).EpochAssignmentService.(*epochAssignmentService)
	finalized, err := reader.validateReworkFindings(t.Context(), epoch, candidate, submission)
	require.NoError(t, err)
	original := store.prov
	for _, attack := range []string{
		"unknown-field",
		"missing-assignment",
		"wrong-assignment",
		"wrong-actor",
		"wrong-axis",
		"wrong-round",
		"wrong-epoch",
		"wrong-kind",
		"wrong-producer",
		"wrong-task",
		"post-finalized-row",
		"post-finalized-producer",
		"duplicate-row",
		"nonterminal",
		"malformed-submission",
		"duplicate-finding",
		"wrong-verdict",
		"missing-current",
	} {
		t.Run("current-rework-reader/"+attack, func(t *testing.T) {
			journal := &reworkSubmissionReadJournal{Journal: original.Journal()}
			journal.change = func(query provenance.EvidenceQuery, page *provenance.EvidencePage) {
				if attack == "missing-current" {
					page.Rows = nil
					page.Next = nil
					return
				}
				require.NotEmpty(t, page.Rows, "attack must reach a genuine current submission")
				row := &page.Rows[0]
				switch attack {
				case "wrong-producer":
					row.ProducingOperationID = "unrelated-submit-operation"
					return
				case "wrong-task":
					row.TaskID = &candidate
					return
				case "post-finalized-row":
					row.JournalID = finalized.journalID + 1
					return
				case "post-finalized-producer":
					row.ProducingOperationJournalID = finalized.journalID + 1
					return
				case "duplicate-row":
					page.Rows = append(page.Rows, *row)
					return
				case "nonterminal":
					page.Next = &provenance.FactCursor{}
					return
				}
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(row.Payload, &fields))
				set := func(key string, value any) {
					encoded, err := json.Marshal(value)
					require.NoError(t, err)
					fields[key] = encoded
				}
				switch attack {
				case "unknown-field":
					set("unexpected", true)
				case "missing-assignment":
					delete(fields, "assignment")
				case "wrong-assignment":
					set("assignment", "unrelated-axis-assignment")
				case "wrong-actor":
					set("actor", provenance.ActorID{Namespace: "other", UUID: candidate.UUID})
				case "wrong-axis":
					set("axis", ReviewAxis(3))
				case "wrong-round":
					set("round", "unrelated-round")
				case "wrong-epoch":
					set("epoch", "unrelated-epoch")
				case "wrong-kind":
					set("kind", SubjectPlan)
				case "malformed-submission":
					set("submission", map[string]any{"Verdict": 0, "Findings": nil})
				case "duplicate-finding", "wrong-verdict":
					var implementation ImplementationReviewSubmission
					require.NoError(t, json.Unmarshal(fields["submission"], &implementation))
					require.NotEmpty(t, implementation.Findings)
					if attack == "duplicate-finding" {
						implementation.Findings = append(implementation.Findings, implementation.Findings[0])
					} else {
						implementation.Verdict = VerdictRevise
						implementation.Findings[0].Severity = SeverityBlocker
					}
					set("submission", implementation)
				}
				row.Payload, err = json.Marshal(fields)
				require.NoError(t, err)
			}
			store.prov = recoveryCountTracker{Tracker: original, journal: journal}
			t.Cleanup(func() { store.prov = original })
			_, err := reader.validateReworkFindings(t.Context(), epoch, candidate, submission)
			require.Error(t, err)
			require.Len(t, journal.queries, 1, "a bad first axis must not trigger a legacy or page-two scan")
			query := journal.queries[0]
			require.Equal(t, provenance.FactTaskExact, query.Filter.TaskScope.Kind)
			require.NotEqual(t, candidate, query.Filter.TaskScope.TaskID)
			require.Len(t, query.Filter.OperationIDs, 1)
			require.Equal(t, 2, query.Page.Limit)
			require.Equal(t, finalized.journalID, query.Page.SnapshotMaxJournalID)
		})
	}
	store.prov = original

	// Disposition accounting is independent of evidence decoding.
	t.Run("current-rework-reader/dispositions", func(t *testing.T) {
		duplicate := ReworkSubmission{Findings: []FindingResolution{submission.Findings[0], submission.Findings[0]}}
		_, err := reader.validateReworkFindings(t.Context(), epoch, candidate, duplicate)
		require.ErrorContains(t, err, "disposed more than once")
		_, err = reader.validateReworkFindings(t.Context(), epoch, candidate, ReworkSubmission{})
		require.ErrorContains(t, err, "rework disposes 0 of 1")
		added := ReworkSubmission{Findings: []FindingResolution{{Finding: candidate, Outcome: FindingDeferred}}}
		_, err = reader.validateReworkFindings(t.Context(), epoch, candidate, added)
		require.ErrorContains(t, err, "not in the finalized review")
		fixed := ReworkSubmission{
			Findings: []FindingResolution{
				{
					Finding:  submission.Findings[0].Finding,
					Outcome:  FindingFixed,
					Evidence: []provenance.JournalID{finalized.journalID + 100000},
				},
			},
		}
		_, err = reader.validateReworkFindings(t.Context(), epoch, candidate, fixed)
		require.ErrorContains(t, err, "cites missing evidence")
		fixed.Findings[0].Evidence = nil
		_, err = reader.validateReworkFindings(t.Context(), epoch, candidate, fixed)
		require.ErrorContains(t, err, "has no fix evidence")
		_, err = reader.validateReworkFindings(t.Context(), epoch, candidate, submission)
		require.NoError(t, err, "deferred findings do not require fix evidence")
	})
}

func TestCurrentReworkRefusesLegacyCandidateSubmissionAlone(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "legacy-submission")
	epoch := createHumanTestTask(t, store, "epoch")
	candidate := createHumanTestTask(t, store, "legacy-candidate")
	finding := createHumanTestTask(t, store, "legacy-finding")
	_, root, found, err := readSystemIdentity(store.auditDB)
	require.NoError(t, err)
	require.True(t, found)
	legacyPayload, err := canonicalJSON(struct {
		Kind       SubjectKind                    `json:"kind"`
		Submission ImplementationReviewSubmission `json:"submission"`
	}{
		Kind: SubjectImplementation,
		Submission: ImplementationReviewSubmission{
			Verdict:  VerdictAccept,
			Findings: []ReviewFinding{{Task: finding, Severity: SeverityImportant, Summary: "Legacy follow-up"}},
		},
	})
	require.NoError(t, err)
	digest := sha256.Sum256(legacyPayload)
	_, err = store.Journal().Apply(provenance.OperationInput{
		OperationID:        "legacy-submission-only",
		ActorID:            actor,
		AuthorityJournalID: &root,
		CommandDigest:      []byte("legacy-submission-only"),
		Effects: []provenance.Effect{{
			Sort:          provenance.EffectEvidence,
			TaskID:        candidate,
			EvidenceKind:  reviewSubmissionEvidenceKind,
			ContentDigest: digest[:],
			Payload:       legacyPayload,
		}},
	})
	require.NoError(t, err)
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	reader := service.(*epochService).EpochAssignmentService.(*epochAssignmentService)
	_, err = reader.validateReworkFindings(t.Context(), EpochRootID(epoch.String()), candidate, ReworkSubmission{
		Findings: []FindingResolution{{Finding: finding, Outcome: FindingDeferred}},
	})
	require.Error(t, err, "legacy submission alone lacks the mandatory current review and axis bindings")
	page, err := store.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{
			TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate},
			OperationIDs: []provenance.OperationID{"legacy-submission-only"},
		},
		Kinds: []provenance.EvidenceKind{reviewSubmissionEvidenceKind},
		Page:  provenance.FactPageRequest{Limit: 2},
	})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1, "refusal must not delete or migrate the legacy evidence")
}

func TestCurrentReworkSliceRealProducerRecovery(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "slice-rework")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(
		t,
		store,
		plan,
		"slice-rework-supervisor",
		RoleGoverningSupervisor,
		actor,
		"slice-rework-supervisor-start",
	)
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	slice, err := service.CreateSlice(t.Context(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: "slice-rework-create"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "slice-rework-supervisor",
	})
	require.NoError(t, err)
	member, err := service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
		Meta:       CommandMeta{OperationID: "slice-rework-member"},
		Epoch:      EpochRootID(epoch.String()),
		Slice:      slice.Slice,
		Repository: "repo-slice",
		Commit:     "0123456789abcdef0123456789abcdef01234567",
		Assignment: "slice-rework-create-slice-owner",
	})
	require.NoError(t, err)
	candidate, err := provenance.ParseTaskID(string(member.Candidate))
	require.NoError(t, err)
	started, err := service.StartReview(t.Context(), StartReviewInput{
		Meta:    CommandMeta{OperationID: "slice-rework-review"},
		Epoch:   EpochRootID(epoch.String()),
		Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: candidate.String()},
	})
	require.NoError(t, err)
	seedRecoveryAssignmentWithParent(t, store, candidate, "slice-rework-reviewer",
		RoleGoverningSupervisor, actor, "slice-rework-reviewer-start", "slice-rework-create-slice-owner")
	rework := finishRecoveryReviewWithDeferredFinding(t, store, service, EpochRootID(epoch.String()), started,
		"slice-rework-reviewer", actor, "slice-rework-finish")

	// Prove the actual replacement caller governs the old candidate before
	// invoking the command. The fixture does not substitute bootstrap authority.
	owner, err := store.Journal().(provenance.AssignmentStartQueryAPI).QueryAssignmentStarts(provenance.AssignmentStartQuery{
		AssignmentIDs: []provenance.AssignmentID{"slice-rework-create-slice-owner"},
		Page:          provenance.AssignmentStartPageRequest{Limit: 64},
	})
	require.NoError(t, err)
	require.Len(t, owner.Rows, 1)
	governs, err := store.Journal().AuthorityGovernsTaskAt(owner.Rows[0].AuthorityJournalID, candidate, provenance.JournalID(^uint64(0)>>1))
	require.NoError(t, err)
	require.True(t, governs)
	replacement, err := service.ReworkSlice(t.Context(), ReworkSliceInput{
		Meta:       CommandMeta{OperationID: "slice-rework-replacement"},
		Epoch:      EpochRootID(epoch.String()),
		Slice:      slice.Slice,
		Candidate:  member.Candidate,
		Assignment: "slice-rework-create-slice-owner",
		Replacement: SliceCandidateReplacement{
			Repository: "repo-slice",
			Commit:     "1123456789abcdef0123456789abcdef01234567",
		},
		Rework: rework,
	})
	require.NoError(t, err)
	require.NoError(
		t,
		RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}),
	)
	row := transferIndexRow(t, store, "slice-rework-replacement-candidate-owner")
	require.Equal(t, string(replacement.Candidate), row.Task)
	require.Equal(t, RoleOwnerResponsibility.String(), row.Role)
	require.Positive(t, row.Authority)
}

func finishRecoveryReviewWithDeferredFinding(
	t *testing.T,
	store *trackerImpl,
	service EpochService,
	epoch EpochRootID,
	started ReviewStartResult,
	governor provenance.AssignmentID,
	actor provenance.ActorID,
	prefix string,
) ReworkSubmission {
	t.Helper()
	finding := createHumanTestTask(t, store, prefix+"-finding")
	for i, axis := range canonicalReviewAxes() {
		assignment := provenance.AssignmentID(string(started.OperationID) + "-axis-" + axis.String())
		submission := ImplementationReviewSubmission{Verdict: VerdictAccept}
		if i == 0 {
			submission.Findings = []ReviewFinding{{Task: finding, Severity: SeverityImportant, Summary: "Follow up <&> after review"}}
		}
		input := SubmitReviewInput{
			Meta:       CommandMeta{OperationID: provenance.OperationID(fmt.Sprintf("%s-submit-%d", prefix, i))},
			Epoch:      epoch,
			Round:      started.Round,
			Axis:       axis,
			Assignment: assignment,
			Submission: submission,
		}
		_, err := service.SubmitReview(t.Context(), input)
		require.NoError(t, err)
		replay, err := service.SubmitReview(t.Context(), input)
		require.NoError(t, err)
		require.True(t, replay.Replayed)
		changed := input
		changed.Submission = ImplementationReviewSubmission{Verdict: VerdictRevise, Findings: []ReviewFinding{
			{Task: finding, Severity: SeverityBlocker, Summary: "Changed retry"},
		}}
		_, err = service.SubmitReview(t.Context(), changed)
		require.Error(t, err, "a changed retry must not append a different review")

		commandRows, err := store.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
			Filter: provenance.FactFilter{
				TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskAny},
				OperationIDs: []provenance.OperationID{input.Meta.OperationID},
			},
			Kinds: []provenance.EvidenceKind{assignmentCommandEvidenceKind},
			Page:  provenance.FactPageRequest{Limit: 2},
		})
		require.NoError(t, err)
		require.Len(t, commandRows.Rows, 1)
		var commandRecord assignmentCommandRecord
		require.NoError(t, strictJSON(commandRows.Rows[0].Payload, &commandRecord))
		require.NotNil(t, commandRows.Rows[0].TaskID)
		require.Equal(t, commandRecord.Task, *commandRows.Rows[0].TaskID)
		require.Equal(t, actor, commandRecord.Occupant)
		if i == 0 {
			require.NotEqual(t, assignment, commandRecord.Assignment, "nonempty findings cite the actual parent authority")
			require.Equal(t, RoleGoverningSupervisor, commandRecord.Role)
		} else {
			require.Equal(t, assignment, commandRecord.Assignment, "empty implementation submissions keep their axis route")
			require.Equal(t, RoleAxisReviewer, commandRecord.Role)
		}
		governsAtBirth, err := store.Journal().AuthorityGovernsTaskAt(commandRecord.Authority, commandRecord.Task, commandRecord.Authority+1)
		require.NoError(t, err)
		require.True(t, governsAtBirth, "command authority/task must remain coherent for replay")

		// Observe the actual producer contract, separately from rework's reader.
		axisTask := deterministicTask(started.OperationID, "axis-"+axis.String())
		query := provenance.EvidenceQuery{
			Filter: provenance.FactFilter{
				TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: axisTask},
				OperationIDs: []provenance.OperationID{provenance.OperationID(fmt.Sprintf("%s-submit-%d", prefix, i))},
			},
			Kinds: []provenance.EvidenceKind{reviewSubmissionEvidenceKind},
			Page:  provenance.FactPageRequest{Limit: 2},
		}
		axisRows, err := store.Journal().Facts().QueryEvidence(query)
		require.NoError(t, err)
		require.Len(t, axisRows.Rows, 1)
		require.Nil(t, axisRows.Next)
		subject, err := provenance.ParseTaskID(started.Subject.SnapshotID)
		require.NoError(t, err)
		query.Filter.TaskScope.TaskID = subject
		candidateRows, err := store.Journal().Facts().QueryEvidence(query)
		require.NoError(t, err)
		require.Empty(t, candidateRows.Rows, "current submissions belong to the axis, not the candidate")
	}
	_, err := service.FinalizeReview(
		t.Context(),
		FinalizeReviewInput{
			Meta:       CommandMeta{OperationID: provenance.OperationID(prefix + "-finalize")},
			Epoch:      epoch,
			Round:      started.Round,
			Assignment: governor,
		},
	)
	require.NoError(t, err)
	// The producer accepts absent fix evidence for the Deferred union arm.
	// Its typed nil slice is reconstructed as null, not fabricated as [].
	return ReworkSubmission{Findings: []FindingResolution{{Finding: finding, Outcome: FindingDeferred}}}
}

func TestNativeCommandParentUsesPublicIdentityNotAdjacentOrIndexAuthority(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "native-parent")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "native-plan", RoleGoverningSupervisor, actor, "native-plan-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	slice, err := service.CreateSlice(t.Context(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: "native-parent-slice"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "native-plan",
	})
	require.NoError(t, err)
	reader := service.(*epochService).EpochAssignmentService.(*epochAssignmentService)
	resolution, err := reader.resolveAssignment(t.Context(), slice.Slice, "native-parent-slice-slice-owner", RoleOwnerResponsibility)
	require.NoError(t, err)
	verified, err := reader.exactCandidateParentAuthority(t.Context(), resolution)
	require.NoError(t, err)
	require.Equal(t, resolution.authority, verified.authority)
	material, err := store.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy:    provenance.OrderByJournalID,
		TaskIDs:    []provenance.TaskID{slice.Slice},
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:      64,
	})
	require.NoError(t, err)
	require.Len(t, material.Events, 1)
	require.NotEqual(t, verified.authority, material.Events[0].JournalID-1)
	adjacent := resolution
	adjacent.authority = material.Events[0].JournalID - 1
	_, err = reader.exactCandidateParentAuthority(t.Context(), adjacent)
	require.Error(t, err)
	wrongActor := resolution
	wrongActor.occupant = provenance.ActorID{Namespace: "wrong", UUID: actor.UUID}
	_, err = reader.exactCandidateParentAuthority(t.Context(), wrongActor)
	require.Error(t, err)
	_, err = store.auditDB.Exec(
		`UPDATE pasture_actor_assignment SET role=? WHERE assignment_id=?`,
		RoleAxisReviewer.String(),
		string(resolution.id),
	)
	require.NoError(t, err)
	verified, err = reader.exactCandidateParentAuthority(t.Context(), resolution)
	require.NoError(t, err, "mutable index data is not command authority")
	require.Equal(t, RoleOwnerResponsibility, verified.role)
}

func TestNonemptyReviewDoesNotInferCrossActorDelegation(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	parentActor := feasibilityActor(t, store, "review-parent")
	otherActor := feasibilityActor(t, store, "other-reviewer")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "delegation-plan", RoleGoverningSupervisor, parentActor, "delegation-plan-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	slice, err := service.CreateSlice(t.Context(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: "delegation-slice"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "delegation-plan",
	})
	require.NoError(t, err)
	member, err := service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
		Meta:       CommandMeta{OperationID: "delegation-member"},
		Epoch:      EpochRootID(epoch.String()),
		Slice:      slice.Slice,
		Repository: "repo",
		Commit:     "0123456789abcdef0123456789abcdef01234567",
		Assignment: "delegation-slice-slice-owner",
	})
	require.NoError(t, err)
	started, err := service.StartReview(t.Context(), StartReviewInput{
		Meta:    CommandMeta{OperationID: "delegation-review"},
		Epoch:   EpochRootID(epoch.String()),
		Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: string(member.Candidate)},
	})
	require.NoError(t, err)
	axis := canonicalReviewAxes()[0]
	axisTask := deterministicTask(started.OperationID, "axis-"+axis.String())
	seedRecoveryAssignmentWithParent(t, store, axisTask, "other-axis-reviewer", RoleAxisReviewer,
		otherActor, "other-axis-reviewer-start", "delegation-plan")
	finding := createHumanTestTask(t, store, "finding")
	input := SubmitReviewInput{
		Meta:       CommandMeta{OperationID: "unsupported-delegation"},
		Epoch:      EpochRootID(epoch.String()),
		Round:      started.Round,
		Axis:       axis,
		Assignment: "other-axis-reviewer",
		Submission: ImplementationReviewSubmission{
			Verdict: VerdictAccept,
			Findings: []ReviewFinding{
				{Task: finding, Severity: SeverityImportant, Summary: "A real finding"},
			},
		},
	}

	_, err = service.SubmitReview(t.Context(), input)
	require.ErrorContains(t, err, "delegation")
	committed, err := store.Journal().LookupCommitted(input.Meta.OperationID)
	require.NoError(t, err)
	require.Equal(
		t,
		provenance.CommittedAbsent,
		committed.Kind,
		"failed delegation must commit no partial submission",
	)

	// Empty submissions mutate only the reviewer's own axis and retain the old
	// route. This does not grant that reviewer authority over sibling groups.
	input.Meta.OperationID = "other-reviewer-empty"
	input.Submission = ImplementationReviewSubmission{Verdict: VerdictAccept}
	_, err = service.SubmitReview(t.Context(), input)
	require.NoError(t, err)
	replayed, err := service.SubmitReview(t.Context(), input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
}

func seedRecoveryAssignmentWithParent(
	t *testing.T,
	store *trackerImpl,
	task provenance.TaskID,
	assignment provenance.AssignmentID,
	role AssignmentRole,
	actor provenance.ActorID,
	operation provenance.OperationID,
	parent provenance.AssignmentID,
) {
	t.Helper()
	_, root, found, err := readSystemIdentity(store.auditDB)
	require.NoError(t, err)
	require.True(t, found)
	material, err := MapMaterialEvent(AssignmentStartedEvent{
		Task:       task,
		Assignment: assignment,
		Role:       role,
		Occupant:   actor,
	})
	require.NoError(t, err)
	_, err = store.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            actor,
		AuthorityJournalID: &root,
		CommandDigest:      []byte(operation),
		Effects: []provenance.Effect{
			{
				Sort:         provenance.EffectAssignmentStart,
				ResultSlot:   "authority",
				TaskID:       task,
				AssignmentID: assignment,
				Parent:       parent,
				SlotID:       provenance.SlotOwnerResponsibility,
				Occupant:     actor,
			},
			material,
		},
	})
	require.NoError(t, err)
}

// This fixture removes only the child material effects added by the newer
// StartReview writer, then delegates to the real composed allocator. It models
// the historical producer footprint without SQL edits to Provenance history.
type legacyRecoveryReviewRunner struct {
	composedAllocationRunner
	keepMaterial int
}

func (r legacyRecoveryReviewRunner) RunAllocateComposedBatch(ctx context.Context, workflow string, authority provenance.JournalID, request provenance.GovernedAllocationComposedRequest) (provenance.GovernedAllocationComposedResult, error) {
	var effects []provenance.Effect
	kept := 0
	for _, effect := range request.SupplementalEffects {
		if effect.Sort == provenance.EffectTaskEvent && effect.EventKind == FamilyAssignmentStarted.EventKind() {
			if kept >= r.keepMaterial {
				continue
			}
			kept++
		}
		effects = append(effects, effect)
	}
	request.SupplementalEffects = effects
	return r.composedAllocationRunner.RunAllocateComposedBatch(ctx, workflow, authority, request)
}

type recoveryAlteredComposedRunner struct {
	composedAllocationRunner
	alter func(*provenance.GovernedAllocationComposedRequest)
}

type recoveryAlteredBatchRunner struct {
	composedAllocationRunner
	alter func(*provenance.GovernedAllocationComposedRequest)
}

func (r recoveryAlteredBatchRunner) RunAllocateComposedBatch(ctx context.Context, workflow string, authority provenance.JournalID, request provenance.GovernedAllocationComposedRequest) (provenance.GovernedAllocationComposedResult, error) {
	if r.alter != nil {
		r.alter(&request)
	}
	return r.composedAllocationRunner.RunAllocateComposedBatch(ctx, workflow, authority, request)
}

func TestAssignmentRecoveryRequiresExactStartedReviewEvidence(t *testing.T) {
	t.Parallel()
	for _, attack := range []string{
		"missing",
		"duplicate",
		"wrong-operation",
		"axis-events",
		"wrong-graph",
		"digest",
		"omit-axis-events",
		"null-axis-events",
	} {
		t.Run(attack, func(t *testing.T) {
			store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
			defer store.Close()
			bindTestGovernedAllocation(t, store)
			actor := feasibilityActor(t, store, "review-evidence")
			epoch := createHumanTestTask(t, store, "epoch")
			plan := createHumanTestTask(t, store, "plan")
			other := createHumanTestTask(t, store, "other")
			seedAssignmentEpisode(
				t,
				store,
				plan,
				"review-evidence-parent",
				RoleGoverningSupervisor,
				actor,
				"review-evidence-parent-start",
			)
			store.allocationRunner = recoveryAlteredBatchRunner{store.allocationRunner, func(request *provenance.GovernedAllocationComposedRequest) {
				var effects []provenance.Effect
				for _, effect := range request.SupplementalEffects {
					if effect.Sort == provenance.EffectEvidence && effect.EvidenceKind == reviewRoundAuthorityEvidenceKind {
						if attack == "missing" {
							continue
						}
						var value reviewRoundAuthority
						require.NoError(t, json.Unmarshal(effect.Payload, &value))
						switch attack {
						case "wrong-operation":
							value.Operation = "unrelated-review"
						case "axis-events":
							value.AxisEvents[0] = 1
						case "wrong-graph":
							value.Graph[0].Task = other
						}
						var err error
						effect.Payload, err = canonicalJSON(value)
						require.NoError(t, err)
						hash := sha256.Sum256(effect.Payload)
						effect.ContentDigest = hash[:]
						if attack == "omit-axis-events" || attack == "null-axis-events" {
							var fields map[string]json.RawMessage
							require.NoError(t, json.Unmarshal(effect.Payload, &fields))
							if attack == "omit-axis-events" {
								delete(fields, "axis_events")
							} else {
								fields["axis_events"] = json.RawMessage(`null`)
							}
							effect.Payload, err = json.Marshal(fields)
							require.NoError(t, err)
						}
						if attack == "digest" {
							effect.ContentDigest = bytes.Repeat([]byte{0}, sha256.Size)
						}
						if attack == "duplicate" {
							copy := effect
							copy.ResultSlot = "duplicate-review-evidence"
							effects = append(effects, copy)
						}
					}
					effects = append(effects, effect)
				}
				request.SupplementalEffects = effects
			}}
			service, err := store.NewEpochService(EpochServiceOptions{})
			require.NoError(t, err)
			_, err = service.StartReview(
				t.Context(),
				StartReviewInput{
					Meta:    CommandMeta{OperationID: "review-evidence-attack"},
					Epoch:   EpochRootID(epoch.String()),
					Subject: ReviewSubjectRef{Kind: ReviewSubjectDocumentRevision, SnapshotID: plan.String()},
				},
			)
			require.NoError(t, err)
			require.NoError(t, store.Journal().VerifyIntegrity())
			_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
			require.Error(t, err, "command evidence cannot replace exact started review-round evidence")
		})
	}
}

func (r recoveryAlteredComposedRunner) RunAllocateComposed(ctx context.Context, workflow string, authority provenance.JournalID, request provenance.GovernedAllocationComposedRequest) (provenance.GovernedAllocationComposedResult, error) {
	if r.alter != nil {
		r.alter(&request)
	}
	return r.composedAllocationRunner.RunAllocateComposed(ctx, workflow, authority, request)
}

func TestAssignmentRecoveryRejectsGenuineComposedCommandBindingAttacks(t *testing.T) {
	t.Parallel()
	for _, attack := range []string{"parent-role", "parent-assignment", "evidence-task", "actor", "nested-request"} {
		t.Run(attack, func(t *testing.T) {
			store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
			defer store.Close()
			bindTestGovernedAllocation(t, store)
			actor := feasibilityActor(t, store, "command-actor")
			other := feasibilityActor(t, store, "other-actor")
			epoch := createHumanTestTask(t, store, "epoch")
			plan := createHumanTestTask(t, store, "plan")
			otherTask := createHumanTestTask(t, store, "other-task")
			seedAssignmentEpisode(t, store, plan, "command-parent", RoleGoverningSupervisor, actor, "command-parent-start")
			store.allocationRunner = recoveryAlteredComposedRunner{store.allocationRunner, func(request *provenance.GovernedAllocationComposedRequest) {
				for i := range request.SupplementalEffects {
					effect := &request.SupplementalEffects[i]
					if effect.Sort != provenance.EffectEvidence || effect.EvidenceKind != assignmentCommandEvidenceKind {
						continue
					}
					var record assignmentCommandRecord
					require.NoError(t, json.Unmarshal(effect.Payload, &record))
					var payload struct {
						Plan       provenance.TaskID       `json:"plan"`
						Assignment provenance.AssignmentID `json:"assignment"`
					}
					require.NoError(t, json.Unmarshal(record.Payload, &payload))
					switch attack {
					case "parent-role":
						record.Role = RoleOwnerResponsibility
					case "parent-assignment":
						record.Assignment = "other-parent"
						payload.Assignment = "other-parent"
					case "evidence-task":
						record.Task = otherTask
						payload.Plan = otherTask
					case "actor":
						record.Occupant = other
					}
					var err error
					record.Payload, err = canonicalJSON(payload)
					require.NoError(t, err)
					record.Request, err = assignmentRequestCommand(record.Mutation, record.Epoch, payload)
					require.NoError(t, err)
					if attack == "nested-request" {
						record.Request, err = assignmentRequestCommand(MutationSetSliceCandidate, record.Epoch, payload)
						require.NoError(t, err)
					}
					effect.Payload, err = canonicalJSON(record)
					require.NoError(t, err)
					digest := sha256.Sum256(effect.Payload)
					effect.ContentDigest = digest[:]
				}
			}}
			service, err := store.NewEpochService(EpochServiceOptions{})
			require.NoError(t, err)
			_, err = service.CreateSlice(
				t.Context(),
				CreateSliceInput{
					Meta:       CommandMeta{OperationID: "attacked-command"},
					Epoch:      EpochRootID(epoch.String()),
					Plan:       plan,
					Assignment: "command-parent",
				},
			)
			require.NoError(t, err)
			require.NoError(t, store.Journal().VerifyIntegrity())
			_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
			require.Error(t, err, "a genuine supplement with a forged Pasture declaration must not certify an assignment")
		})
	}
}

func TestAssignmentRecoveryRejectsUnrelatedGenuineSupplementMaterial(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "supplement-actor")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "supplement-parent", RoleGoverningSupervisor, actor, "supplement-parent-start")
	delegate := store.allocationRunner
	store.allocationRunner = recoveryAlteredComposedRunner{delegate, func(request *provenance.GovernedAllocationComposedRequest) {
		var effects []provenance.Effect
		for _, effect := range request.SupplementalEffects {
			if effect.Sort == provenance.EffectTaskEvent && effect.EventKind == FamilyAssignmentStarted.EventKind() {
				continue
			}
			effects = append(effects, effect)
		}
		request.SupplementalEffects = effects
	}}
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	victim, err := service.CreateSlice(
		t.Context(),
		CreateSliceInput{
			Meta:       CommandMeta{OperationID: "victim"},
			Epoch:      EpochRootID(epoch.String()),
			Plan:       plan,
			Assignment: "supplement-parent",
		},
	)
	require.NoError(t, err)
	store.allocationRunner = recoveryAlteredComposedRunner{delegate, func(request *provenance.GovernedAllocationComposedRequest) {
		material, err := MapMaterialEvent(AssignmentStartedEvent{
			Task:       victim.Slice,
			Assignment: "victim-slice-owner",
			Role:       RoleOwnerResponsibility,
			Occupant:   actor,
		})
		require.NoError(t, err)
		material.ResultSlot = "transplanted-material"
		request.SupplementalEffects = append(request.SupplementalEffects, material)
		request.ReferenceScope = provenance.GovernedAllocationReferenceScope{
			Kind:     provenance.GovernedAllocationReferenceDescendants,
			Subjects: []provenance.TaskID{victim.Slice},
		}
	}}
	_, err = service.CreateSlice(
		t.Context(),
		CreateSliceInput{
			Meta:       CommandMeta{OperationID: "unrelated-supplement"},
			Epoch:      EpochRootID(epoch.String()),
			Plan:       plan,
			Assignment: "supplement-parent",
		},
	)
	require.NoError(t, err)
	require.NoError(t, store.Journal().VerifyIntegrity())
	_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.ErrorContains(t, err, "producer mismatch")
	starts, err := store.Journal().(provenance.AssignmentStartQueryAPI).QueryAssignmentStarts(provenance.AssignmentStartQuery{
		AssignmentIDs: []provenance.AssignmentID{"victim-slice-owner"},
		Page:          provenance.AssignmentStartPageRequest{Limit: 64},
	})
	require.NoError(t, err)
	require.Len(t, starts.Rows, 1)
	reader := service.(*epochService).EpochAssignmentService.(*epochAssignmentService)
	_, err = reader.exactCandidateParentAuthority(t.Context(), assignmentResolution{
		id:        "victim-slice-owner",
		task:      victim.Slice,
		occupant:  actor,
		role:      RoleOwnerResponsibility,
		authority: starts.Rows[0].AuthorityJournalID,
	})
	require.ErrorContains(t, err, "producer mismatch", "command parent proof must reject the same transplant as recovery")
}

func TestAssignmentRecoveryRejectsDuplicateIntegrationCommandMembers(t *testing.T) {
	t.Parallel()
	for _, duplicateRepository := range []bool{false, true} {
		t.Run(fmt.Sprintf("repository=%t", duplicateRepository), func(t *testing.T) {
			store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
			defer store.Close()
			bindTestGovernedAllocation(t, store)
			actor := feasibilityActor(t, store, "repo-attack")
			epoch := createHumanTestTask(t, store, "epoch")
			plan := createHumanTestTask(t, store, "plan")
			other := createHumanTestTask(t, store, "other")
			seedAssignmentEpisode(t, store, plan, "repo-supervisor", RoleGoverningSupervisor, actor, "repo-supervisor-start")
			service, err := store.NewEpochService(EpochServiceOptions{})
			require.NoError(t, err)
			slice, err := service.CreateSlice(
				t.Context(),
				CreateSliceInput{
					Meta:       CommandMeta{OperationID: "repo-slice"},
					Epoch:      EpochRootID(epoch.String()),
					Plan:       plan,
					Assignment: "repo-supervisor",
				},
			)
			require.NoError(t, err)
			seedAssignmentEpisode(t, store, slice.Slice, "repo-owner", RoleOwnerResponsibility, actor, "repo-owner-start")
			commit := provenance.GitOID("0123456789abcdef0123456789abcdef01234567")
			member, err := service.SetSliceCandidate(
				t.Context(),
				SetSliceCandidateInput{
					Meta:       CommandMeta{OperationID: "repo-member"},
					Epoch:      EpochRootID(epoch.String()),
					Slice:      slice.Slice,
					Repository: "repo-a",
					Commit:     commit,
					Assignment: "repo-owner",
				},
			)
			require.NoError(t, err)
			store.allocationRunner = recoveryAlteredComposedRunner{store.allocationRunner, func(request *provenance.GovernedAllocationComposedRequest) {
				for i := range request.SupplementalEffects {
					effect := &request.SupplementalEffects[i]
					if effect.Sort != provenance.EffectEvidence || effect.EvidenceKind != assignmentCommandEvidenceKind {
						continue
					}
					var record assignmentCommandRecord
					require.NoError(t, json.Unmarshal(effect.Payload, &record))
					var payload struct {
						Plan         provenance.TaskID       `json:"plan"`
						Repositories []RepositoryCandidate   `json:"repositories"`
						Assignment   provenance.AssignmentID `json:"assignment"`
					}
					require.NoError(t, json.Unmarshal(record.Payload, &payload))
					require.Len(t, payload.Repositories, 1)
					extra := payload.Repositories[0]
					if duplicateRepository {
						extra.Candidate = ImplementationCandidateID(other.String())
					} else {
						extra.Repository = "repo-b"
					}
					payload.Repositories = append(payload.Repositories, extra)
					var err error
					record.Payload, err = canonicalJSON(payload)
					require.NoError(t, err)
					record.Request, err = assignmentRequestCommand(record.Mutation, record.Epoch, payload)
					require.NoError(t, err)
					effect.Payload, err = canonicalJSON(record)
					require.NoError(t, err)
					hash := sha256.Sum256(effect.Payload)
					effect.ContentDigest = hash[:]
				}
			}}
			_, err = service.CreateIntegrationCandidate(
				t.Context(),
				CreateIntegrationCandidateInput{
					Meta:         CommandMeta{OperationID: "repo-integration-attack"},
					Epoch:        EpochRootID(epoch.String()),
					Plan:         plan,
					Assignment:   "repo-supervisor",
					Repositories: []RepositoryCandidate{{Repository: "repo-a", Candidate: member.Candidate, Commit: commit}},
				},
			)
			require.NoError(t, err)
			require.NoError(t, store.Journal().VerifyIntegrity())
			_, err = store.prepareAssignmentCatchUp(t.Context(), 0, false)
			require.ErrorContains(t, err, "duplicate integration repository or candidate")
		})
	}
}

func TestAssignmentRecoveryPlanReviewCurrentAndLegacyBadPresent(t *testing.T) {
	t.Parallel()
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
			defer store.Close()
			bindTestGovernedAllocation(t, store)
			if legacy {
				store.allocationRunner = legacyRecoveryReviewRunner{composedAllocationRunner: store.allocationRunner}
			}
			actor := feasibilityActor(t, store, "plan-review")
			epoch := createHumanTestTask(t, store, "epoch")
			plan := createHumanTestTask(t, store, "plan")
			seedAssignmentEpisode(t, store, plan, "plan-supervisor", RoleGoverningSupervisor, actor, "plan-supervisor-start")
			service, err := store.NewEpochService(EpochServiceOptions{})
			require.NoError(t, err)
			_, err = service.StartReview(
				t.Context(),
				StartReviewInput{
					Meta:    CommandMeta{OperationID: "recover-plan-review"},
					Epoch:   EpochRootID(epoch.String()),
					Subject: ReviewSubjectRef{Kind: ReviewSubjectDocumentRevision, SnapshotID: plan.String()},
				},
			)
			require.NoError(t, err)
			require.NoError(
				t,
				RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}),
			)
			var count int
			require.NoError(
				t,
				store.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_actor_assignment WHERE assignment_id LIKE 'recover-plan-review-%'`).Scan(&count),
			)
			require.Equal(t, 4, count)
			if !legacy {
				return
			}
			starts, err := store.Journal().(provenance.AssignmentStartQueryAPI).QueryAssignmentStarts(provenance.AssignmentStartQuery{
				OperationIDs: []provenance.OperationID{"recover-plan-review"},
				Page:         provenance.AssignmentStartPageRequest{Limit: 64},
			})
			require.NoError(t, err)
			require.Len(t, starts.Rows, 4)
			child := starts.Rows[0]
			effect, err := MapMaterialEvent(AssignmentStartedEvent{
				Task:       child.TaskID,
				Assignment: child.AssignmentID,
				Role:       RoleAxisReviewer,
				Occupant:   child.Occupant,
			})
			require.NoError(t, err)
			_, err = store.Journal().Apply(provenance.OperationInput{
				OperationID:        "bad-present-review-material",
				ActorID:            child.Occupant,
				AuthorityJournalID: &child.AuthorityJournalID,
				CommandDigest:      []byte("bad-present"),
				Effects:            []provenance.Effect{effect},
			})
			require.NoError(t, err)
			require.NoError(t, store.Journal().VerifyIntegrity())
			require.ErrorContains(
				t,
				RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}),
				"producer mismatch",
			)
		})
	}
}

func TestPartialReviewMaterialRecoversAllFourDeclaredMembers(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	store.allocationRunner = legacyRecoveryReviewRunner{composedAllocationRunner: store.allocationRunner, keepMaterial: 1}
	actor := feasibilityActor(t, store, "partial-review")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "partial-supervisor", RoleGoverningSupervisor, actor, "partial-supervisor-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	_, err = service.StartReview(t.Context(), StartReviewInput{
		Meta:    CommandMeta{OperationID: "partial-review-start"},
		Epoch:   EpochRootID(epoch.String()),
		Subject: ReviewSubjectRef{Kind: ReviewSubjectDocumentRevision, SnapshotID: plan.String()},
	})
	require.NoError(t, err)
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}))
	var count int
	require.NoError(t, store.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_actor_assignment WHERE assignment_id LIKE 'partial-review-start-%'`).Scan(&count))
	require.Equal(t, 4, count)
}

func TestRecoveryMaterialDecoderRejectsCorruptedPublicRows(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "material-defense")
	task := createHumanTestTask(t, store, "task")
	seedFeasibilityEpisode(t, store, task, "material-defense", actor, "material-defense-start")
	page, err := store.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy:    provenance.OrderByJournalID,
		TaskIDs:    []provenance.TaskID{task},
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:      64,
	})
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	_, err = decodeRecoveryMaterials(page.Events, page.SnapshotMaxJournalID)
	require.NoError(t, err)
	// Nil/future producer metadata cannot be authored through a sound public
	// Apply. These are defensive decoder mutations of a real returned row.
	t.Run("nil-producer", func(t *testing.T) {
		row := page.Events[0]
		row.ProducedByOperationJournalID = nil
		_, err := decodeRecoveryMaterials([]provenance.TaskEventRow{row}, page.SnapshotMaxJournalID)
		require.Error(t, err)
	})
	t.Run("future-producer", func(t *testing.T) {
		row := page.Events[0]
		future := page.SnapshotMaxJournalID + 1
		row.ProducedByOperationJournalID = &future
		_, err := decodeRecoveryMaterials([]provenance.TaskEventRow{row}, page.SnapshotMaxJournalID)
		require.Error(t, err)
	})
	t.Run("explicit-zero-authority", func(t *testing.T) {
		row := page.Events[0]
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(row.Payload, &fields))
		fields["authorityJournalId"] = json.RawMessage(`0`)
		row.Payload, err = json.Marshal(fields)
		require.NoError(t, err)
		_, err = decodeRecoveryMaterials([]provenance.TaskEventRow{row}, page.SnapshotMaxJournalID)
		require.Error(t, err)
	})
}
