package tasks

// assignment_authentication_test.go drives the write path's authentication
// through the SAME production entry point a command uses,
// authenticateAssignmentStarts, over REAL stores opened the way production opens
// them. No test-only export and no copy of the paging: the point of these
// subjects is that the code a command runs is the code proven here.
//
// WHAT IS PROVEN, in the order of the file:
//   - the evidence, material and command decoders refuse what a stored row
//     cannot mean (a store-free set);
//   - the paging driver authenticates a whole task's start history, including
//     composed commands, split and legacy reviews, and transfer chains;
//   - the refusals happen before any write and their text names no retired
//     machinery;
//   - the only bound on the read is the page cap, and 65 transfer predecessors
//     authenticate;
//   - every non-transfer assignment-start writer, and the transfer writer, emits
//     material a reader can find.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── shared fixtures ────────────────────────────────────────────────────────

// feasibilityActor registers one human agent for a fixture. It lives here
// because the surviving review-authority subjects and the authentication
// subjects below both need it, and neither owns a file of its own.
func feasibilityActor(t *testing.T, tracker *trackerImpl, handle string) provenance.ActorID {
	t.Helper()
	actor, err := tracker.RegisterHumanAgent(handle, "Feasibility Probe", handle+"@example.test")
	if err != nil {
		t.Fatalf("register actor %q: %v", handle, err)
	}
	return actor.ID
}

// seedRecoveryAssignmentWithParent commits ONE assignment episode plus the
// material fact that describes it, in a single journal Apply, citing an optional
// parent assignment. It is the fixture for an episode a command did not create.
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

// legacyRecoveryReviewRunner removes only the child material effects added by
// the newer StartReview writer, then delegates to the real composed allocator.
// It models the historical producer footprint without SQL edits to Provenance
// history. keepMaterial is how many child material effects survive, so 0 models a
// writer that emitted none and 1 models one that emitted only the first.
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

func (r recoveryAlteredComposedRunner) RunAllocateComposed(ctx context.Context, workflow string, authority provenance.JournalID, request provenance.GovernedAllocationComposedRequest) (provenance.GovernedAllocationComposedResult, error) {
	if r.alter != nil {
		r.alter(&request)
	}
	return r.composedAllocationRunner.RunAllocateComposed(ctx, workflow, authority, request)
}

// authenticationReadTracker substitutes ONE journal on the tracker, so a test
// can observe or alter a read at its own boundary. Nothing else about the
// tracker changes, and no production code constructs this type.
type authenticationReadTracker struct {
	provenance.Tracker
	journal provenance.Journal
}

func (t authenticationReadTracker) Journal() provenance.Journal { return t.journal }

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

// ─── the production driver, driven over a whole store ───────────────────────

// everyAssignmentStart reads every assignment start the store holds, in journal
// order, through the public assignment-start query.
func everyAssignmentStart(t *testing.T, store *trackerImpl) []provenance.AssignmentStartRow {
	t.Helper()
	api, ok := store.Journal().(provenance.AssignmentStartQueryAPI)
	require.True(t, ok, "the production journal must answer the public assignment-start query")
	var starts []provenance.AssignmentStartRow
	cursor := provenance.JournalID(0)
	for pageNumber := 0; ; pageNumber++ {
		require.Less(t, pageNumber, maxParentProofPages, "the test read must stay inside the production page bound")
		page, err := api.QueryAssignmentStarts(provenance.AssignmentStartQuery{
			Page: provenance.AssignmentStartPageRequest{Limit: maxParentProofPageRows, AfterJournalID: cursor},
		})
		require.NoError(t, err)
		starts = append(starts, page.Rows...)
		if page.Next == nil {
			return starts
		}
		require.Greater(t, page.Next.AfterJournalID, cursor, "the start cursor must advance")
		cursor = page.Next.AfterJournalID
	}
}

// authenticateWholeStore runs the PRODUCTION driver over every task in the
// store, one task at a time, because one authenticated page is one task's
// starts. It returns the proofs it established and the FIRST refusal it met, so
// a subject that expects a refusal can read its text and a subject that expects
// success can require no refusal at all.
//
// MUTATION: make authenticateAssignmentStarts return a zero proof and a nil
// error; every counting assertion in the re-homed subjects goes RED.
func authenticateWholeStore(t *testing.T, store *trackerImpl) (map[provenance.TaskID]assignmentRecoveryProof, error) {
	t.Helper()
	byTask := map[provenance.TaskID][]provenance.AssignmentStartRow{}
	var order []provenance.TaskID
	for _, row := range everyAssignmentStart(t, store) {
		if _, seen := byTask[row.TaskID]; !seen {
			order = append(order, row.TaskID)
		}
		byTask[row.TaskID] = append(byTask[row.TaskID], row)
	}
	proofs := map[provenance.TaskID]assignmentRecoveryProof{}
	for _, task := range order {
		proof, err := authenticateAssignmentStarts(t.Context(), store.Journal(), byTask[task], 0)
		if err != nil {
			return proofs, err
		}
		proofs[task] = proof
	}
	return proofs, nil
}

// requireWholeStoreAuthenticates is the success form: every start the store
// holds authenticates, and the returned proofs are the ones to count in.
func requireWholeStoreAuthenticates(t *testing.T, store *trackerImpl) map[provenance.TaskID]assignmentRecoveryProof {
	t.Helper()
	proofs, err := authenticateWholeStore(t, store)
	require.NoError(t, err, "every assignment start this store holds must authenticate")
	return proofs
}

// countAuthenticatedAssignments counts the authenticated episodes whose
// assignment id carries the given prefix, across every task.
func countAuthenticatedAssignments(proofs map[provenance.TaskID]assignmentRecoveryProof, prefix string) int {
	total := 0
	for _, proof := range proofs {
		for _, row := range proof.Rows {
			if strings.HasPrefix(string(row.Assignment), prefix) {
				total++
			}
		}
	}
	return total
}

// findAuthenticatedEpisode returns the one authenticated episode with this
// assignment id, or nil.
func findAuthenticatedEpisode(proofs map[provenance.TaskID]assignmentRecoveryProof, assignment provenance.AssignmentID) *startedEpisode {
	for _, proof := range proofs {
		for _, row := range proof.Rows {
			if row.Assignment == assignment {
				found := row
				return &found
			}
		}
	}
	return nil
}

// ─── the decoders, store-free ───────────────────────────────────────────────

// TestAssignmentAuthenticationEvidenceUsesProducerAndStoredRepresentations pins
// the two representations of a command's bytes: the PRODUCER's canonical form,
// which is what the content digest covers, and the STORED normalized form, which
// is what the journal holds. They are different bytes, and the decoder must
// accept only the pairing the producer wrote.
//
// RED when: the digest is computed over the stored bytes instead of the
// producer's, or the stored form is no longer required to be normalized.
func TestAssignmentAuthenticationEvidenceUsesProducerAndStoredRepresentations(t *testing.T) {
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
	require.True(t, bytes.Contains(normalized, []byte("\\u003c")),
		"the stored form escapes the angle bracket, which is what makes it different from the producer's bytes")

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

// TestRecoveryMaterialDecoderRejectsCorruptedPublicRows takes a material row the
// journal itself produced and feeds the decoder metadata that a sound public
// Apply cannot author. These are decoder mutations of a real returned row, not
// fabricated rows: the store is real, and only the row handed to the decoder
// differs.
//
// RED when: a material row with no producer, a producer above the snapshot, or a
// present authority of zero is accepted.
func TestRecoveryMaterialDecoderRejectsCorruptedPublicRows(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "material-defense")
	task := createHumanTestTask(t, store, "task")
	seedRecoveryAssignmentWithParent(t, store, task, "material-defense", RoleOwnerResponsibility, actor, "material-defense-start", "")
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
		_, err := decodeRecoveryMaterials([]provenance.TaskEventRow{row}, page.SnapshotMaxJournalID)
		require.Error(t, err)
	})
}

// ─── the production driver, driven by real commands ─────────────────────────

// TestNativeCommandParentUsesPublicIdentityNotAdjacentAuthority pins that a
// command's parent authority is the EXACT public assignment start, not a
// neighbouring journal row and not a guess. An adjacent material row sits one
// position from the real authority, and a wrong actor names a different episode;
// a correct resolution must be accepted and both of those refused.
//
// RED when: the resolver accepts an authority one row below the real one, or
// accepts a start row whose occupant is not the resolved actor.
func TestNativeCommandParentUsesPublicIdentityNotAdjacentAuthority(t *testing.T) {
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
}

// TestCurrentReworkRefusesLegacyCandidateSubmissionAlone pins that a legacy
// candidate submission, with no current review-round or axis binding, cannot
// satisfy rework validation, and that the refusal leaves the stored evidence
// exactly where it was.
//
// RED when: legacy evidence alone satisfies rework validation, or the refusal
// deletes or rewrites the stored row.
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

// TestCurrentReworkSliceRealProducerRecovery drives the REAL replacement writer
// after a real finished review, then authenticates every start the store holds,
// including the replacement's own. The replacement is reached by the actual
// caller that governs the old candidate, not by synthesized evidence shaped
// like one.
//
// RED when: the replacement's start does not authenticate, or its task or role
// differs from the replacement the command returned.
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
	proofs := requireWholeStoreAuthenticates(t, store)
	replacementTask, err := provenance.ParseTaskID(string(replacement.Candidate))
	require.NoError(t, err)
	authenticated := findAuthenticatedEpisode(proofs, "slice-rework-replacement-candidate-owner")
	require.NotNil(t, authenticated, "the replacement's own start must be authenticated by the production driver")
	require.Equal(t, replacementTask, authenticated.Task)
	require.Equal(t, RoleOwnerResponsibility, authenticated.Role)
	require.Positive(t, authenticated.Authority)
}

// TestNonemptyReviewDoesNotInferCrossActorDelegation pins that a reviewer's
// assignment on one axis confers nothing on a sibling: a nonempty submission
// needs a governing parent, and the failure commits nothing. An EMPTY
// submission on that reviewer's own axis still succeeds, so the refusal is about
// delegation and not about the reviewer.
//
// RED when: a nonempty submission under a reviewer-only assignment commits, or
// the refusal leaves a partial submission behind.
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

// TestAssignmentAuthenticationRejectsUnrelatedGenuineSupplementMaterial takes a
// GENUINE material fact, written by a real operation with a real digest, and
// transplants it onto a different command's supplement. The row is honest about
// itself and still must not authenticate the child, because the material
// producer is not the command's producer. The second half proves the command
// write path refuses the same transplant.
//
// RED when: material produced by one operation authenticates a child created by
// another, in the whole-store read or in the command parent proof.
func TestAssignmentAuthenticationRejectsUnrelatedGenuineSupplementMaterial(t *testing.T) {
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
	_, err = authenticateWholeStore(t, store)
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
	require.ErrorContains(t, err, "producer mismatch", "command parent proof must reject the same transplant as the whole-store read")
}

// TestAssignmentAuthenticationRejectsGenuineComposedCommandBindingAttacks
// rewrites the DECLARATION inside a genuine command evidence effect — its parent
// role, its parent assignment, its task, its actor, or its nested request — and
// re-signs the digest so the row is internally consistent. Nothing may
// authenticate, because the evidence no longer says what the producer wrote.
//
// RED when: any of the five attacks authenticates a composed child.
func TestAssignmentAuthenticationRejectsGenuineComposedCommandBindingAttacks(t *testing.T) {
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
			_, err = authenticateWholeStore(t, store)
			require.Error(t, err, "a genuine supplement with a forged Pasture declaration must not authenticate an assignment")
		})
	}
}

// TestAssignmentAuthenticationRequiresExactStartedReviewEvidence removes or
// reshapes the started review-round evidence a real StartReview commits, and
// then re-signs the digest so the row is internally consistent. The command
// evidence that accompanies it does not stand in for the review evidence.
//
// RED when: a missing, duplicated, re-pointed, reshaped or mis-digested review
// evidence still authenticates the review's children.
func TestAssignmentAuthenticationRequiresExactStartedReviewEvidence(t *testing.T) {
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
							copied := effect
							copied.ResultSlot = "duplicate-review-evidence"
							effects = append(effects, copied)
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
			_, err = authenticateWholeStore(t, store)
			require.Error(t, err, "command evidence cannot replace exact started review-round evidence")
		})
	}
}

// TestAssignmentAuthenticationRejectsDuplicateIntegrationCommandMembers
// duplicates one entry of an integration command's repository array, once with a
// repeated repository and once with a repeated candidate, and re-signs the
// command request so the row is internally consistent.
//
// RED when: either duplicate authenticates the integration child.
func TestAssignmentAuthenticationRejectsDuplicateIntegrationCommandMembers(t *testing.T) {
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
			_, err = authenticateWholeStore(t, store)
			require.ErrorContains(t, err, "duplicate integration repository or candidate")
		})
	}
}

// TestAssignmentAuthenticationRealComposedCommandsAndSplitReview drives the real
// composed commands end to end — slice, candidate, integration candidate, a
// review, a finalized review and a replacement — and then authenticates every
// start the store holds through the production driver.
//
// RED when: a real composed child does not authenticate, or the review's
// thirteen declared members are not all present.
func TestAssignmentAuthenticationRealComposedCommandsAndSplitReview(t *testing.T) {
	t.Parallel()
	runComposedRecoveryCommands(t, false)
}

// TestLegacyImplementationReviewRecoversThirteenMembers is the same shape with a
// historical review writer that emitted no child material at all, so every
// child must be authenticated from the command and review evidence alone.
//
// RED when: a child with no material fact fails to authenticate.
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
	// Deliberately reverse repository order. Authentication must reconstruct
	// the recorded command order, not a separately sorted manifest order.
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
	proofs := requireWholeStoreAuthenticates(t, store)
	require.Equal(t, 13, countAuthenticatedAssignments(proofs, "recover-review-"),
		"the native implementation review declares thirteen children and every one must be authenticated")
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
	proofs = requireWholeStoreAuthenticates(t, store)
	replacementTask, err := provenance.ParseTaskID(string(replacement.Candidate))
	require.NoError(t, err)
	authenticated := findAuthenticatedEpisode(proofs, "recover-integration-replacement-candidate-owner")
	require.NotNil(t, authenticated, "the replacement candidate's own start must be authenticated")
	require.Equal(t, replacementTask, authenticated.Task)
}

// TestAssignmentAuthenticationPlanReviewCurrentAndLegacyBadPresent starts a
// plan review with the current writer and with the historical writer that emits
// no child material, and counts the four declared children in both. For the
// legacy shape it then writes a genuine material fact for one child from a
// DIFFERENT operation, which must be refused rather than believed.
//
// RED when: a plan review authenticates fewer or more than four children, or a
// foreign material fact authenticates a child.
func TestAssignmentAuthenticationPlanReviewCurrentAndLegacyBadPresent(t *testing.T) {
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
			proofs := requireWholeStoreAuthenticates(t, store)
			require.Equal(t, 4, countAuthenticatedAssignments(proofs, "recover-plan-review-"))
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
			_, err = authenticateWholeStore(t, store)
			require.ErrorContains(t, err, "producer mismatch")
		})
	}
}

// TestPartialReviewMaterialRecoversAllFourDeclaredMembers starts a plan review
// with a writer that emitted material for only the FIRST child. Three children
// therefore have no material at all and must authenticate from the command and
// review evidence, while the one that does has its material bound to its own
// producer.
//
// RED when: a child with no material fails, or the child with material is
// authenticated in a role its material does not declare.
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
	proofs := requireWholeStoreAuthenticates(t, store)
	require.Equal(t, 4, countAuthenticatedAssignments(proofs, "partial-review-start-"))
	for _, proof := range proofs {
		for _, row := range proof.Rows {
			if strings.HasPrefix(string(row.Assignment), "partial-review-start-") {
				require.Equal(t, RoleAxisReviewer, row.Role)
			}
		}
	}
}

// finishRecoveryReviewWithDeferredFinding submits every canonical axis, proves
// each submission's producer contract, and finalizes the round, returning the
// rework submission a caller may then act on.
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

// assertCurrentReworkReaderRefusals is the read-boundary proof for the current
// rework reader: each attack reshapes or withholds a genuine persisted
// submission row at the read boundary, and every one must be refused by a single
// bounded read.
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
			store.prov = authenticationReadTracker{Tracker: original, journal: journal}
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

// ─── the refusals ───────────────────────────────────────────────────────────

// authenticationFaultSteps returns every step literal the production
// authentication file passes to authenticationFault, read from the source
// rather than from a list kept beside it.
func authenticationFaultSteps(t *testing.T) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "assignment_authentication.go", nil, 0)
	require.NoError(t, err, "the production authentication file must parse")
	var steps []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		callee, isName := call.Fun.(*ast.Ident)
		if !isName || callee.Name != "authenticationFault" || len(call.Args) == 0 {
			return true
		}
		literal, isLiteral := call.Args[0].(*ast.BasicLit)
		if !isLiteral {
			t.Errorf(
				"authenticationFault is called with a computed step at %s; every step must be a literal so this test can render every refusal",
				fileSet.Position(call.Pos()),
			)
			return true
		}
		steps = append(steps, strings.Trim(literal.Value, `"`))
		return true
	})
	sort.Strings(steps)
	return steps
}

// TestAssignmentAuthenticationFaultNamesNoRetiredMachinery renders every refusal
// the authentication file can produce and holds it to three rules: it names no
// retired machinery (no rebuild command, no "certif" stem, no generation, no
// index), it carries all six parts, and the transfer-predecessor cause says
// "no authenticated owner predecessor" rather than the retired word.
//
// RED when: a step literal is added that the rendering guard rejects, or the
// predecessor cause reverts to the retired wording.
func TestAssignmentAuthenticationFaultNamesNoRetiredMachinery(t *testing.T) {
	t.Parallel()
	steps := authenticationFaultSteps(t)
	require.NotEmpty(t, steps, "the production file must raise at least one authentication refusal, or nothing is proved")
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			rendered := (&AssignmentAuthenticationError{Step: step, Cause: stderrors.New("example cause")}).Error()
			for _, forbidden := range []string{"rebuild-index", "certif", "generation", "index"} {
				require.NotContains(t, strings.ToLower(rendered), forbidden,
					"the refusal names retired machinery an operator can no longer act on")
			}
			for _, part := range []string{"Why:", "Where:", "When:", "Impact:", "Fix:"} {
				require.Contains(t, rendered, part, "every part of the refusal must reach the reader")
			}
			require.Contains(t, rendered, "step "+step)
			require.Contains(t, rendered, "nothing was written")
		})
	}

	// The predecessor cause is reached by REAL production code: a start row that
	// cites a predecessor the authenticated page does not contain.
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "predecessor-cause")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "cause-parent", RoleGoverningSupervisor, actor, "cause-parent-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	_, err = service.CreateSlice(t.Context(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: "cause-slice"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "cause-parent",
	})
	require.NoError(t, err)
	var starts []provenance.AssignmentStartRow
	for _, row := range everyAssignmentStart(t, store) {
		if row.AssignmentID == "cause-slice-slice-owner" {
			starts = append(starts, row)
		}
	}
	require.Len(t, starts, 1)
	absent := provenance.AssignmentID("no-such-predecessor")
	mutated := append([]provenance.AssignmentStartRow(nil), starts...)
	mutated[0].PredecessorAssignmentID = &absent
	_, err = authenticateAssignmentStarts(t.Context(), store.Journal(), mutated, 0)
	require.Error(t, err)
	require.ErrorContains(t, err, "no authenticated owner predecessor")
	require.NotContains(t, strings.ToLower(err.Error()), "certif")
	var fault *AssignmentAuthenticationError
	require.ErrorAs(t, err, &fault, "the refusal must be the typed authentication refusal")
	require.Equal(t, "transfer predecessor", fault.Step)
}

// retiredTransferCountNames are the four names the per-predecessor budget used.
// None of them may appear in the write path's own production sources again.
var retiredTransferCountNames = []string{
	"maxParentTransferPredicates",
	"governance >",
	"historical predicate budget exhausted",
	"HistoricalPredicates",
}

// authenticatedWritePathSources are the production files that make up the write
// path a command authenticates under. The scan below reads exactly these, and
// the list is the call graph of the authentication driver rather than the whole
// directory: a read-side file never authenticates a command, so scanning it
// would make this subject report on code it does not own.
var authenticatedWritePathSources = []string{
	"assignment_authentication.go",
	"assignment_authority.go",
	"assignment_transfer.go",
	"governed_create_slice.go",
	"epoch_candidate_commands.go",
	"epoch_review_commands.go",
	"open_unified.go",
}

// lineOf reports the one-based line a phrase first appears on, so a failure
// names a line instead of printing a whole production file.
func lineOf(source, phrase string) int {
	return 1 + strings.Count(source[:strings.Index(source, phrase)], "\n")
}

// TestTransferParentFaultNamesNoRetiredCount is the direct proof that the
// retired per-predecessor budget is gone. It reads the write path's own
// production sources and rejects the old counter, the old comparison, the old
// fault text and the old proof field, so the 64-bound cannot return unnoticed.
// The page cap is then pinned as the ONE remaining bound.
//
// RED when: any of the four retired names reappears in a write-path production
// source, or the page cap changes.
func TestTransferParentFaultNamesNoRetiredCount(t *testing.T) {
	t.Parallel()
	for _, name := range authenticatedWritePathSources {
		source, err := os.ReadFile(name)
		require.NoError(t, err, "the scanned write-path source must be readable")
		for _, phrase := range retiredTransferCountNames {
			if !strings.Contains(string(source), phrase) {
				continue
			}
			t.Errorf("retired per-predecessor budget %q is back in %s, on line %d",
				phrase, name, lineOf(string(source), phrase))
		}
	}
	// The one remaining bound is the page cap, and it is the page cap: sixteen
	// pages of sixty-four starts, so at most about a thousand rows.
	require.Equal(t, 16, maxParentProofPages)
	require.Equal(t, 64, maxParentProofPageRows)
}

// governanceProbe records every governance predicate the production driver asks
// about, and delegates. It is a test observer on the PUBLIC journal API: it
// answers nothing itself, so a count it reports is a count of real questions.
type governanceProbe struct {
	provenance.Journal
	asked []governanceQuestion
}

type governanceQuestion struct {
	Authority provenance.JournalID
	Task      provenance.TaskID
	Before    provenance.JournalID
	Governs   bool
}

func (j *governanceProbe) AuthorityGovernsTaskAt(authority provenance.JournalID, task provenance.TaskID, before provenance.JournalID) (bool, error) {
	governs, err := j.Journal.AuthorityGovernsTaskAt(authority, task, before)
	j.asked = append(j.asked, governanceQuestion{Authority: authority, Task: task, Before: before, Governs: governs})
	return governs, err
}

// TestParentTransferAuthenticationHasNo64PredicateLimit drives SIXTY-FIVE
// transfer predecessors through the production authentication driver on one
// real task and counts the governance predicates it asks. All sixty-five are
// asked and all sixty-five answer yes, so the sixty-fifth predecessor
// authenticates: there is no per-predecessor budget to exhaust, only the page
// cap.
//
// RED when: a per-predecessor budget returns (the count stops below sixty-five),
// or the driver stops asking about a predecessor it once asked about.
func TestParentTransferAuthenticationHasNo64PredicateLimit(t *testing.T) {
	t.Parallel()
	const predecessors = 65
	fixture := newTaskAssignmentTransferFixture(t)
	fixture.seedOwnerAssignment(t, "chain-0")
	for i := 1; i <= predecessors; i++ {
		request := protocol.TransferTaskAssignmentRequest{
			TaskID:           fixture.task,
			Slot:             provenance.SlotOwnerResponsibility,
			NextAssignmentID: provenance.AssignmentID(fmt.Sprintf("chain-%d", i)),
			ActorID:          fixture.actorA,
			NextOccupant:     fixture.actorB,
		}
		if _, err := fixture.tracker.TransferTaskAssignment(t.Context(), request); err != nil {
			t.Fatalf("transfer %d of the predecessor chain: %v", i, err)
		}
	}

	starts := everyAssignmentStart(t, fixture.tracker)
	require.Len(t, starts, predecessors+1, "the original owner plus every successor is a start this store holds")
	probe := &governanceProbe{Journal: fixture.tracker.Journal()}
	proof, err := authenticateAssignmentStarts(t.Context(), probe, starts, 0)
	require.NoError(t, err, "every predecessor in the authenticated page must be checked, so the last one authenticates too")
	require.Len(t, proof.Rows, predecessors+1)
	require.Len(t, probe.asked, predecessors,
		"each of the %d transfer successors asks about exactly one predecessor; a per-predecessor budget would cut this short", predecessors)
	for i, question := range probe.asked {
		require.Equal(t, proof.Rows[i].Authority, question.Authority,
			"question %d is about the episode immediately before the successor it authenticates", i)
		require.Equal(t, fixture.task, question.Task)
		require.True(t, question.Governs, "question %d must be answered yes, or the successor would be refused", i)
	}
	for i, row := range proof.Rows {
		require.Equal(t, RoleOwnerResponsibility, row.Role, "row %d is a transfer successor in the owner slot", i)
		require.Equal(t, fixture.task, row.Task)
		require.Positive(t, row.Authority)
	}
	last := proof.Rows[len(proof.Rows)-1]
	require.Equal(t, provenance.AssignmentID(fmt.Sprintf("chain-%d", predecessors)), last.Assignment)
}

// ─── writer emission ────────────────────────────────────────────────────────

// TestEveryNonTransferAssignmentStartWriterMaterialIsReadableByOwnershipQuery
// drives the four composed assignment-start writers and the review batch, then
// reads each started task's FamilyAssignmentStarted material through the public
// journal read the ownership query is built on, and requires exactly ONE
// strict-decodable row per writer naming the declared assignment, role and
// occupant.
//
// The ownership read itself is the read side's own subject
// (TestGateReaderMapsOwnedTasksPerWriter), taken on the Provenance pin that
// carries QueryActorOwnership. What is proved HERE is the half this package
// owns: every non-transfer writer emits material, exactly once per started task,
// that decodes to the identity the writer declared.
//
// RED when: a writer emits no material, emits it twice for one episode, emits it
// for another task, or emits a payload that does not decode to the declared
// assignment, role and occupant.
func TestEveryNonTransferAssignmentStartWriterMaterialIsReadableByOwnershipQuery(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "writer-emission")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "writer-parent", RoleGoverningSupervisor, actor, "writer-parent-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	commit := provenance.GitOID("0123456789abcdef0123456789abcdef01234567")

	slice, err := service.CreateSlice(t.Context(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: "writer-slice"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "writer-parent",
	})
	require.NoError(t, err)
	member, err := service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
		Meta:       CommandMeta{OperationID: "writer-member"},
		Epoch:      EpochRootID(epoch.String()),
		Slice:      slice.Slice,
		Repository: "repo-w",
		Commit:     commit,
		Assignment: "writer-slice-slice-owner",
	})
	require.NoError(t, err)
	integration, err := service.CreateIntegrationCandidate(t.Context(), CreateIntegrationCandidateInput{
		Meta:         CommandMeta{OperationID: "writer-integration"},
		Epoch:        EpochRootID(epoch.String()),
		Plan:         plan,
		Assignment:   "writer-parent",
		Repositories: []RepositoryCandidate{{Repository: "repo-w", Candidate: member.Candidate, Commit: commit}},
	})
	require.NoError(t, err)
	memberTask, err := provenance.ParseTaskID(string(member.Candidate))
	require.NoError(t, err)
	integrationTask, err := provenance.ParseTaskID(string(integration.Candidate))
	require.NoError(t, err)

	// The review subject is the slice candidate, so its finalizing authority
	// lives on the candidate task and cites the slice owner. It is seeded AFTER
	// the review starts, because a second governing assignment in scope while
	// the review starts is exactly the ambiguity the resolver refuses.
	started, err := service.StartReview(t.Context(), StartReviewInput{
		Meta:    CommandMeta{OperationID: "writer-review"},
		Epoch:   EpochRootID(epoch.String()),
		Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: memberTask.String()},
	})
	require.NoError(t, err)
	seedRecoveryAssignmentWithParent(t, store, memberTask, "writer-finalizer",
		RoleGoverningSupervisor, actor, "writer-finalizer-start", "writer-slice-slice-owner")
	rework := finishRecoveryReviewWithDeferredFinding(t, store, service, EpochRootID(epoch.String()), started,
		"writer-finalizer", actor, "writer-finish")
	replacement, err := service.ReworkSlice(t.Context(), ReworkSliceInput{
		Meta:       CommandMeta{OperationID: "writer-replacement"},
		Epoch:      EpochRootID(epoch.String()),
		Slice:      slice.Slice,
		Candidate:  member.Candidate,
		Assignment: "writer-slice-slice-owner",
		Replacement: SliceCandidateReplacement{
			Repository: "repo-w",
			Commit:     "1123456789abcdef0123456789abcdef01234567",
		},
		Rework: rework,
	})
	require.NoError(t, err)
	replacementTask, err := provenance.ParseTaskID(string(replacement.Candidate))
	require.NoError(t, err)

	// ONE case per non-transfer writer that builds an assignment-start payload.
	writers := []struct {
		writer     string
		task       provenance.TaskID
		assignment provenance.AssignmentID
		role       AssignmentRole
		material   int
	}{
		{"slice", slice.Slice, "writer-slice-slice-owner", RoleOwnerResponsibility, 1},
		// The candidate task also carries the review's finalizing authority
		// fixture above, so it holds two start facts and the writer's is one of
		// them.
		{"candidate", memberTask, "writer-member-candidate-owner", RoleOwnerResponsibility, 2},
		{"integration", integrationTask, "writer-integration-candidate-owner", RoleGoverningSupervisor, 1},
		{"replacement", replacementTask, "writer-replacement-candidate-owner", RoleOwnerResponsibility, 1},
	}
	for _, writer := range writers {
		t.Run(writer.writer, func(t *testing.T) {
			payloads := readStartMaterials(t, store, writer.task)
			require.Len(t, payloads, writer.material,
				"a writer emits exactly one start fact for the episode it starts")
			mine := payloads[string(writer.assignment)]
			require.NotNil(t, mine, "this writer's start fact must be present on the task it started")
			require.Equal(t, writer.role.String(), mine.Role)
			require.Equal(t, actor.String(), mine.Occupant)
		})
	}

	// And the review batch, which declares several holders in one commit.
	reviewTasks := reviewChildTasks(t, store, started.OperationID)
	require.NotEmpty(t, reviewTasks, "the review batch must have created children")
	for _, task := range reviewTasks {
		payloads := readStartMaterials(t, store, task)
		require.Len(t, payloads, 1, "every review batch child carries exactly one start fact")
		for assignment, payload := range payloads {
			require.True(t, strings.HasPrefix(assignment, string(started.OperationID)+"-"),
				"a review child's start fact must name the review that created it, got %q", assignment)
			require.Equal(t, RoleAxisReviewer.String(), payload.Role)
			require.Equal(t, actor.String(), payload.Occupant)
		}
	}
}

// reviewChildTasks returns every task the review operation started, read from
// the public assignment-start query filtered by that operation.
func reviewChildTasks(t *testing.T, store *trackerImpl, operation provenance.OperationID) []provenance.TaskID {
	t.Helper()
	api, ok := store.Journal().(provenance.AssignmentStartQueryAPI)
	require.True(t, ok, "the production journal must answer the public assignment-start query")
	page, err := api.QueryAssignmentStarts(provenance.AssignmentStartQuery{
		OperationIDs: []provenance.OperationID{operation},
		Page:         provenance.AssignmentStartPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	require.Nil(t, page.Next, "one review fits a single page and the read must be complete")
	tasks := make([]provenance.TaskID, 0, len(page.Rows))
	for _, row := range page.Rows {
		tasks = append(tasks, row.TaskID)
	}
	return tasks
}

// readStartMaterials decodes every assignment-start material row this task
// carries, keyed by the assignment it names. Each row must strict-decode: a
// payload a reader cannot decode is material no reader can use.
func readStartMaterials(t *testing.T, store *trackerImpl, task provenance.TaskID) map[string]assignmentStartPayload {
	t.Helper()
	page, err := store.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy:    provenance.OrderByJournalID,
		TaskIDs:    []provenance.TaskID{task},
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:      provenance.MaxFactPageSize,
	})
	require.NoError(t, err)
	payloads := map[string]assignmentStartPayload{}
	for _, row := range page.Events {
		decoded, err := decodeAssignmentStart(row.Payload)
		require.NoError(t, err, "the material a writer emitted must strict-decode")
		_, duplicate := payloads[decoded.Assignment]
		require.False(t, duplicate, "two material rows on task %s name assignment %q", task, decoded.Assignment)
		payloads[decoded.Assignment] = decoded
	}
	return payloads
}

// TestTransferWriterEmitsStartMaterialUnderFollowUpOperation drives a real
// transfer and then finds the material the writer emitted by asking the journal
// for the FOLLOW-UP OPERATION it committed and reading the events that operation
// emitted. The lookup names the operation the writer actually committed; it is
// not production suffix matching and no production code calls it.
//
// RED when: the follow-up operation is absent, emits a different number of
// events of this kind, or emits one that does not decode to the transferred
// episode with its authority id.
func TestTransferWriterEmitsStartMaterialUnderFollowUpOperation(t *testing.T) {
	t.Parallel()
	fixture := newTaskAssignmentTransferFixture(t)
	fixture.seedOwnerAssignment(t, "owner-a")
	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           fixture.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "owner-b",
		ActorID:          fixture.actorA,
		NextOccupant:     fixture.actorB,
	}
	if _, err := fixture.tracker.TransferTaskAssignment(t.Context(), request); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	operation := taskAssignmentTransferOperationID(request)
	committed, err := fixture.tracker.Journal().LookupCommitted(transferMaterialFactOperationID(operation))
	require.NoError(t, err)
	require.Equal(t, provenance.CommittedExact, committed.Kind, "the transfer writer commits its material as a follow-up operation")
	require.NotEmpty(t, committed.EmittedEvents)

	page, err := fixture.tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy:        provenance.OrderByJournalID,
		TaskIDs:        []provenance.TaskID{fixture.task},
		EventKinds:     []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		AfterJournalID: committed.AnchorJournalID,
		Limit:          provenance.MaxFactPageSize,
	})
	require.NoError(t, err)
	emitted := map[provenance.JournalID]bool{}
	for _, id := range committed.EmittedEvents {
		emitted[id] = true
	}
	var matched []provenance.TaskEventRow
	for _, row := range page.Events {
		if emitted[row.JournalID] {
			matched = append(matched, row)
		}
	}
	require.Len(t, matched, 1, "exactly one emitted event of this kind is the transferred episode's start material")
	started, err := decodeAssignmentStart(matched[0].Payload)
	require.NoError(t, err, "the transfer's start material must strict-decode")
	require.Equal(t, "owner-b", started.Assignment)
	require.Equal(t, RoleOwnerResponsibility.String(), started.Role)
	require.Equal(t, fixture.actorB.String(), started.Occupant)
	require.Positive(t, started.AuthorityJournalID, "the material must carry the authority a later reader needs")
}
