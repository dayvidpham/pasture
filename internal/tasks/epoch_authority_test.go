package tasks

import (
	"errors"
	"testing"

	"github.com/dayvidpham/provenance"
)

const (
	authorityTestCommitA provenance.GitOID = "0123456789abcdef0123456789abcdef01234567"
	authorityTestCommitB provenance.GitOID = "fedcba9876543210fedcba9876543210fedcba90"
)

func TestEpochAuthorityContractsRejectMalformedAndDuplicateValues(t *testing.T) {
	t.Parallel()
	epoch := EpochRootID("tasks--018f0000-0000-7000-8000-000000000001")
	candidate := IntegrationCandidateSetID("tasks--018f0000-0000-7000-8000-000000000002")
	memberA := candidateMember{Repository: RepositoryID("repo-a"), Candidate: ImplementationCandidateID(candidate), Commit: authorityTestCommitA}
	memberB := candidateMember{Repository: RepositoryID("repo-b"), Candidate: ImplementationCandidateID("tasks--018f0000-0000-7000-8000-000000000003"), Commit: authorityTestCommitB}
	manifest, err := newIntegrationCandidateManifest(epoch, candidate, []candidateMember{memberB, memberA}, "manifest-order")
	if err != nil {
		t.Fatalf("newIntegrationCandidateManifest: %v", err)
	}
	if len(manifest.Members) != 2 || manifest.Members[0] != memberA || manifest.Members[1] != memberB {
		t.Fatalf("manifest members = %+v, want canonical repository order", manifest.Members)
	}
	if len(manifest.Members) != 2 || manifest.Members[0].Repository != "repo-a" {
		t.Fatalf("manifest did not retain its own immutable member copy: %+v", manifest.Members)
	}

	duplicateRepository := []candidateMember{memberA, {Repository: memberA.Repository, Candidate: memberB.Candidate, Commit: memberB.Commit}}
	if _, err := newIntegrationCandidateManifest(epoch, candidate, duplicateRepository, "manifest-duplicate-repository"); err == nil {
		t.Fatal("duplicate repository accepted by candidate manifest")
	}
	duplicateCandidate := []candidateMember{memberA, {Repository: memberB.Repository, Candidate: memberA.Candidate, Commit: memberB.Commit}}
	if _, err := newIntegrationCandidateManifest(epoch, candidate, duplicateCandidate, "manifest-duplicate-candidate"); err == nil {
		t.Fatal("duplicate candidate accepted by candidate manifest")
	}
	if _, err := newIntegrationCandidateManifest(epoch, candidate, []candidateMember{{Repository: "repo-a", Candidate: memberA.Candidate}}, "manifest-missing-commit"); err == nil {
		t.Fatal("malformed GitOID accepted by candidate manifest")
	}

	publicationA := repositoryPublication{Repository: "repo-a", Candidate: memberA.Candidate, Ref: GitRef("refs/heads/main"), Commit: memberA.Commit, VerificationOperation: "verify-a"}
	publicationB := repositoryPublication{Repository: "repo-b", Candidate: memberB.Candidate, Ref: GitRef("refs/heads/main"), Commit: memberB.Commit, VerificationOperation: "verify-b"}
	publicationSet, err := newCandidatePublicationSet(epoch, candidate, []repositoryPublication{publicationB, publicationA}, "publication-order")
	if err != nil {
		t.Fatalf("newCandidatePublicationSet: %v", err)
	}
	if len(publicationSet.Publications) != 2 || publicationSet.Publications[0] != publicationA || publicationSet.Publications[1] != publicationB {
		t.Fatalf("publication set = %+v, want canonical repository order", publicationSet.Publications)
	}
	if _, err := newCandidatePublicationSet(epoch, candidate, []repositoryPublication{publicationA, publicationA}, "publication-duplicate"); err == nil {
		t.Fatal("duplicate publication accepted")
	}
	if _, err := newCandidatePublicationSet(epoch, candidate, []repositoryPublication{{Repository: publicationA.Repository, Candidate: publicationA.Candidate, Ref: GitRef("refs/heads/../main"), Commit: publicationA.Commit, VerificationOperation: "verify-bad-ref"}}, "publication-bad-ref"); err == nil {
		t.Fatal("malformed Git ref accepted")
	}

	started, err := newReviewStartedAuthority(epoch, candidate, "round-1", "review-start")
	if err != nil {
		t.Fatalf("newReviewStartedAuthority: %v", err)
	}
	if started.State != reviewStarted || !zeroReviewAxes(started.Axes) {
		t.Fatalf("started authority = %+v, want zero axis authority", started)
	}
	clean, err := newFinalizedReviewAuthority(epoch, candidate, "round-1", [3]reviewAxisAuthority{
		{Axis: AxisCorrectness, Event: 11, Verdict: VerdictAccept},
		{Axis: AxisTestQuality, Event: 12, Verdict: VerdictAccept},
		{Axis: AxisElegance, Event: 13, Verdict: VerdictAccept},
	}, "review-finalize")
	if err != nil || clean.State != reviewFinalizedClean {
		t.Fatalf("clean authority = %+v, err=%v", clean, err)
	}
	if _, err := newFinalizedReviewAuthority(epoch, candidate, "round-1", [3]reviewAxisAuthority{
		{Axis: AxisTestQuality, Event: 11, Verdict: VerdictAccept},
		{Axis: AxisCorrectness, Event: 12, Verdict: VerdictAccept},
		{Axis: AxisElegance, Event: 13, Verdict: VerdictAccept},
	}, "review-invalid-order"); err == nil {
		t.Fatal("non-canonical review axis order accepted")
	}
	invalidated, err := newInvalidatedReviewAuthority(epoch, candidate, "round-1", "review-invalidate")
	if err != nil || invalidated.State != reviewInvalidated || invalidated.Round != "round-1" {
		t.Fatalf("invalidated authority = %+v, err=%v", invalidated, err)
	}

	humanState, err := newHumanSubjectStateEvidence(mustParseTaskID(string(epoch)), mustParseTaskID(string(candidate)), subjectStateImplementationAccepted, decisionIDForOperation("human-state"), DecisionImplementationUAT, "human-state")
	if err != nil {
		t.Fatalf("newHumanSubjectStateEvidence: %v", err)
	}
	if humanState.Source != subjectStateSourceHumanDecision || humanState.Decision == "" || humanState.DecisionKind == "" {
		t.Fatalf("human state source = %+v, want exact decision binding", humanState)
	}
	assignmentState, err := newAssignmentSubjectStateEvidence(mustParseTaskID(string(epoch)), mustParseTaskID(string(candidate)), subjectStateReworked, "assignment-rework")
	if err != nil {
		t.Fatalf("newAssignmentSubjectStateEvidence: %v", err)
	}
	if assignmentState.Source != subjectStateSourceAssignmentOperation || assignmentState.Decision != "" || assignmentState.DecisionKind != "" {
		t.Fatalf("assignment state source = %+v, want operation-only binding", assignmentState)
	}
}

func TestEpochAuthorityFileBackedReviewCurrentFactInvalidation(t *testing.T) {
	tracker := openHumanTestTracker(t, t.TempDir()+"/pasture.db")
	defer tracker.Close()
	epoch := createHumanTestTask(t, tracker, "epoch")
	candidate := createHumanTestTask(t, tracker, "candidate")
	epochID := EpochRootID(epoch.String())
	candidateID := IntegrationCandidateSetID(candidate.String())

	started, err := newReviewStartedAuthority(epochID, candidateID, "round-1", "authority-review-start")
	if err != nil {
		t.Fatal(err)
	}
	startedEffect, err := newReviewAuthorityEvidenceEffect(candidate, started, "review-authority")
	if err != nil {
		t.Fatal(err)
	}
	startedResult := applyAuthorityTestEffect(t, tracker, "authority-review-start", nil, startedEffect)
	startedFact := factJournalID(t, tracker, candidate, implementationReviewAuthorityEvidenceKind, "authority-review-start")
	if startedFact == 0 || startedResult.AnchorJournalID == 0 {
		t.Fatalf("started fact/result = %d/%d, want committed file-backed bindings", startedFact, startedResult.AnchorJournalID)
	}

	clean, err := newFinalizedReviewAuthority(epochID, candidateID, "round-1", [3]reviewAxisAuthority{
		{Axis: AxisCorrectness, Event: 21, Verdict: VerdictAccept},
		{Axis: AxisTestQuality, Event: 22, Verdict: VerdictAccept},
		{Axis: AxisElegance, Event: 23, Verdict: VerdictAccept},
	}, "authority-review-finalize")
	if err != nil {
		t.Fatal(err)
	}
	cleanEffect, err := newReviewAuthorityEvidenceEffect(candidate, clean, "review-authority-clean")
	if err != nil {
		t.Fatal(err)
	}
	applyAuthorityTestEffect(t, tracker, "authority-review-finalize", nil, cleanEffect)
	cleanFact := factJournalID(t, tracker, candidate, implementationReviewAuthorityEvidenceKind, "authority-review-finalize")

	invalidated, err := newInvalidatedReviewAuthority(epochID, candidateID, "round-1", "authority-review-invalidate")
	if err != nil {
		t.Fatal(err)
	}
	invalidatedEffect, err := newReviewAuthorityEvidenceEffect(candidate, invalidated, "review-authority-invalidated")
	if err != nil {
		t.Fatal(err)
	}
	applyAuthorityTestEffect(t, tracker, "authority-review-invalidate", nil, invalidatedEffect)

	newStarted, err := newReviewStartedAuthority(epochID, candidateID, "round-2", "authority-stale-attempt")
	if err != nil {
		t.Fatal(err)
	}
	newStartedEffect, err := newReviewAuthorityEvidenceEffect(candidate, newStarted, "stale-review-authority")
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyAuthorityTestEffectWithError(t, tracker, "authority-stale-attempt", []provenance.Condition{reviewAuthorityCurrentCondition(candidate, cleanFact)}, newStartedEffect)
	if !errors.Is(err, provenance.ErrConditionFailed) {
		t.Fatalf("stale clean review condition error = %v, want provenance condition failure", err)
	}
	if got := factJournalID(t, tracker, candidate, implementationReviewAuthorityEvidenceKind, "authority-stale-attempt"); got != 0 {
		t.Fatalf("stale review attempt persisted fact %d", got)
	}
}

func TestEpochAuthorityFileBackedManifestAndPublicationCurrentFactReplacement(t *testing.T) {
	tracker := openHumanTestTracker(t, t.TempDir()+"/pasture.db")
	defer tracker.Close()
	epoch := createHumanTestTask(t, tracker, "epoch")
	candidateSet := createHumanTestTask(t, tracker, "candidate-set")
	memberTaskA := createHumanTestTask(t, tracker, "member-a")
	memberTaskB := createHumanTestTask(t, tracker, "member-b")
	epochID := EpochRootID(epoch.String())
	candidateID := IntegrationCandidateSetID(candidateSet.String())
	memberA := candidateMember{Repository: "repo-a", Candidate: ImplementationCandidateID(memberTaskA.String()), Commit: authorityTestCommitA}
	memberB := candidateMember{Repository: "repo-b", Candidate: ImplementationCandidateID(memberTaskB.String()), Commit: authorityTestCommitB}
	manifest, err := newIntegrationCandidateManifest(epochID, candidateID, []candidateMember{memberB, memberA}, "manifest-file-backed")
	if err != nil {
		t.Fatal(err)
	}
	manifestEffect, err := newCandidateManifestEvidenceEffect(candidateSet, manifest, "candidate-manifest")
	if err != nil {
		t.Fatal(err)
	}
	applyAuthorityTestEffect(t, tracker, "manifest-file-backed", nil, manifestEffect)
	manifestFact := factJournalID(t, tracker, candidateSet, candidateManifestEvidenceKind, "manifest-file-backed")
	if manifestFact == 0 {
		t.Fatal("manifest was not committed to the file-backed store")
	}

	firstPublication, err := newCandidatePublicationSet(epochID, candidateID, []repositoryPublication{{Repository: "repo-a", Candidate: memberA.Candidate, Ref: "refs/heads/main", Commit: memberA.Commit, VerificationOperation: "publish-a"}}, "publication-a")
	if err != nil {
		t.Fatal(err)
	}
	firstEffect, err := newCandidatePublicationSetEvidenceEffect(candidateSet, firstPublication, "publication-set-a")
	if err != nil {
		t.Fatal(err)
	}
	applyAuthorityTestEffect(t, tracker, "publication-a", []provenance.Condition{candidateManifestExactCondition(candidateSet, manifestFact)}, firstEffect)
	firstFact := factJournalID(t, tracker, candidateSet, candidatePublicationSetEvidenceKind, "publication-a")

	completePublication, err := newCandidatePublicationSet(epochID, candidateID, []repositoryPublication{
		{Repository: "repo-b", Candidate: memberB.Candidate, Ref: "refs/heads/main", Commit: memberB.Commit, VerificationOperation: "publish-b"},
		{Repository: "repo-a", Candidate: memberA.Candidate, Ref: "refs/heads/main", Commit: memberA.Commit, VerificationOperation: "publish-a"},
	}, "publication-b")
	if err != nil {
		t.Fatal(err)
	}
	completeEffect, err := newCandidatePublicationSetEvidenceEffect(candidateSet, completePublication, "publication-set-b")
	if err != nil {
		t.Fatal(err)
	}
	applyAuthorityTestEffect(t, tracker, "publication-b", []provenance.Condition{
		candidateManifestExactCondition(candidateSet, manifestFact),
		candidatePublicationSetCurrentCondition(candidateSet, firstFact),
	}, completeEffect)
	completeFact := factJournalID(t, tracker, candidateSet, candidatePublicationSetEvidenceKind, "publication-b")
	if completeFact == 0 || completeFact == firstFact {
		t.Fatalf("publication facts = first %d complete %d, want replacement snapshot", firstFact, completeFact)
	}
	if err := validatePublicationSetAgainstManifest(manifest, completePublication); err != nil {
		t.Fatalf("complete publication set rejected by manifest: %v", err)
	}
	if err := validatePublicationSetAgainstManifest(manifest, firstPublication); err == nil {
		t.Fatal("incomplete publication set accepted as complete against manifest")
	}

	staleReplacement, err := newCandidatePublicationSet(epochID, candidateID, completePublication.Publications, "publication-stale")
	if err != nil {
		t.Fatal(err)
	}
	staleEffect, err := newCandidatePublicationSetEvidenceEffect(candidateSet, staleReplacement, "publication-set-stale")
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyAuthorityTestEffectWithError(t, tracker, "publication-stale", []provenance.Condition{candidatePublicationSetCurrentCondition(candidateSet, firstFact)}, staleEffect)
	if !errors.Is(err, provenance.ErrConditionFailed) {
		t.Fatalf("stale publication condition error = %v, want provenance condition failure", err)
	}
	if got := factJournalID(t, tracker, candidateSet, candidatePublicationSetEvidenceKind, "publication-stale"); got != 0 {
		t.Fatalf("stale publication attempt persisted fact %d", got)
	}
}

func applyAuthorityTestEffect(t *testing.T, tracker *trackerImpl, operation provenance.OperationID, conditions []provenance.Condition, effect provenance.Effect) provenance.CommittedResult {
	t.Helper()
	result, err := applyAuthorityTestEffectWithError(t, tracker, operation, conditions, effect)
	if err != nil {
		t.Fatalf("Apply %q: %v", operation, err)
	}
	return result
}

func applyAuthorityTestEffectWithError(t *testing.T, tracker *trackerImpl, operation provenance.OperationID, conditions []provenance.Condition, effect provenance.Effect) (provenance.CommittedResult, error) {
	t.Helper()
	actor, authority, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil {
		t.Fatalf("read system identity: %v", err)
	}
	if !found {
		t.Fatal("system identity missing in file-backed authority test")
	}
	return tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            actor,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte("authority-test-command-" + string(operation)),
		RecordedAt:         100,
		Conditions:         conditions,
		Effects:            []provenance.Effect{effect},
	})
}

func factJournalID(t *testing.T, tracker *trackerImpl, subject provenance.TaskID, kind provenance.EvidenceKind, operation provenance.OperationID) provenance.JournalID {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}, OperationIDs: []provenance.OperationID{operation}},
		Kinds:  []provenance.EvidenceKind{kind},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	if err != nil {
		t.Fatalf("QueryEvidence %q: %v", operation, err)
	}
	if len(page.Rows) == 0 {
		return 0
	}
	if len(page.Rows) != 1 {
		t.Fatalf("QueryEvidence %q rows = %d, want at most one", operation, len(page.Rows))
	}
	return page.Rows[0].JournalID
}
