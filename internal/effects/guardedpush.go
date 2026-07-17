package effects

import "fmt"

// GuardedPushOutcome is the closed set of successful guarded-push outcomes. Its
// zero value is invalid: a proof only ever carries one of these two verified
// outcomes.
type GuardedPushOutcome string

const (
	// GuardedPushPushed means this call advanced the remote ref to the exact
	// commit and re-verified it.
	GuardedPushPushed GuardedPushOutcome = "pushed"
	// GuardedPushIdempotentReplay means the remote ref already held the exact
	// commit when this call ran, so the landing is a verified replay.
	GuardedPushIdempotentReplay GuardedPushOutcome = "idempotent-replay"
)

func (o GuardedPushOutcome) IsValid() bool {
	switch o {
	case GuardedPushPushed, GuardedPushIdempotentReplay:
		return true
	default:
		return false
	}
}

// GuardedPushInput is the opaque, validated input to the one landing push
// effect. It names the repository, the exact local commit and the tree that
// commit must carry, the exact destination ref, and the expected prior state of
// that ref (including explicit absence). It is an Effect variant classified
// parent-mediated: only the parent orchestrator performs a landing push.
type GuardedPushInput struct {
	repository  RepositoryID
	commit      CommitOID
	tree        TreeDigest
	remoteRef   RemoteRef
	expectedOld ExpectedOldOID
	constructed bool
}

// NewGuardedPushInput validates every operand of a guarded landing push.
func NewGuardedPushInput(repository RepositoryID, commit CommitOID, tree TreeDigest, remoteRef RemoteRef, expectedOld ExpectedOldOID) (GuardedPushInput, error) {
	if !repository.IsValid() {
		return GuardedPushInput{}, invalidGitRepository("NewGuardedPushInput")
	}
	if !commit.IsValid() {
		return GuardedPushInput{}, effectError(
			"guarded-push commit id is zero or invalid",
			"a guarded push targets one exact local commit",
			"NewGuardedPushInput", "guarded-push validation",
			"the push has no exact object to land",
			"construct the commit id with NewCommitOID", nil,
		)
	}
	if !tree.IsValid() {
		return GuardedPushInput{}, effectError(
			"guarded-push tree digest is zero or invalid",
			"the exact tree the commit must carry is verified before any push",
			"NewGuardedPushInput", "guarded-push validation",
			"the local object cannot be verified before landing",
			"construct the tree digest with NewTreeDigest", nil,
		)
	}
	if !remoteRef.IsValid() {
		return GuardedPushInput{}, effectError(
			"guarded-push remote ref is zero or invalid",
			"a guarded push updates exactly one destination ref",
			"NewGuardedPushInput", "guarded-push validation",
			"the push has no exact destination",
			"construct the ref with NewRemoteRef", nil,
		)
	}
	if !expectedOld.IsValid() {
		return GuardedPushInput{}, effectError(
			"guarded-push expected-old state is unspecified",
			"a guarded push must state the remote's expected prior state explicitly, including explicit absence",
			"NewGuardedPushInput", "guarded-push validation",
			"the push could clobber an unexpected remote state",
			"use ExpectAbsentRemote or ExpectRemoteAt", nil,
		)
	}
	return GuardedPushInput{
		repository:  repository,
		commit:      commit,
		tree:        tree,
		remoteRef:   remoteRef,
		expectedOld: expectedOld,
		constructed: true,
	}, nil
}

func (g GuardedPushInput) Repository() RepositoryID    { return g.repository }
func (g GuardedPushInput) Commit() CommitOID           { return g.commit }
func (g GuardedPushInput) Tree() TreeDigest            { return g.tree }
func (g GuardedPushInput) RemoteRef() RemoteRef        { return g.remoteRef }
func (g GuardedPushInput) ExpectedOld() ExpectedOldOID { return g.expectedOld }
func (g GuardedPushInput) IsValid() bool               { return g.constructed }
func (g GuardedPushInput) Classify() RuntimeClass      { return RuntimeClassParentMediated }
func (g GuardedPushInput) isEffect()                   {}

// RemoteState is the observed state of a remote ref: whether it exists and, if
// so, its exact commit. It is the read-back a guarded push verifies against.
type RemoteState struct {
	present bool
	commit  CommitOID
}

// AbsentRemoteState reports that the remote ref does not exist.
func AbsentRemoteState() RemoteState { return RemoteState{present: false} }

// PresentRemoteState reports that the remote ref exists at commit.
func PresentRemoteState(commit CommitOID) RemoteState {
	return RemoteState{present: true, commit: commit}
}

// Present reports whether the remote ref exists.
func (s RemoteState) Present() bool { return s.present }

// Commit returns the remote commit and true when the ref exists.
func (s RemoteState) Commit() (CommitOID, bool) {
	if !s.present {
		return CommitOID{}, false
	}
	return s.commit, true
}

// RepositoryPusher is the injected seam that performs the three primitive git
// operations a guarded push composes: verifying the exact local object,
// performing only the CommitOID:RemoteRef update, and re-reading the remote.
// Production wires this to a git executable; tests wire a fake. ReadRemote is
// called twice by the algorithm below — once before the push, to probe whether
// the remote already holds the exact target, and once after, to verify it. The
// guarded-push algorithm — verify, probe, push, re-read, and only-then construct
// the proof — lives entirely in GuardedPushExactCommit, never in an
// implementation of this seam.
type RepositoryPusher interface {
	// VerifyLocalObject confirms the local repository holds commit and that it
	// carries exactly tree. It returns an actionable error otherwise.
	VerifyLocalObject(repository RepositoryID, commit CommitOID, tree TreeDigest) error
	// PushExact performs only the commit:remoteRef update under the expected-old
	// guard. An error here is not by itself failure: the caller re-reads the
	// remote to decide, so an "already up to date" rejection can still be a
	// verified idempotent replay.
	PushExact(repository RepositoryID, commit CommitOID, remoteRef RemoteRef, expectedOld ExpectedOldOID) error
	// ReadRemote re-reads the current state of remoteRef.
	ReadRemote(repository RepositoryID, remoteRef RemoteRef) (RemoteState, error)
}

// VerifiedGuardedPush is the opaque, process-local proof that a guarded landing
// push reached its exact target. It is deliberately not a constructible exported
// struct: the only producer is GuardedPushExactCommit, which constructs one only
// after re-reading the remote and confirming it holds the exact commit. Its zero
// value is invalid. It has read-only accessors and Validate, and no codec:
// MarshalJSON deliberately fails so the proof can never serialize or leave the
// application process. A consumer (the authoritative task package) imports this
// package and hands the proof straight to its protected commit; the public wire
// form of a landing carries only an event id and the outcome, never this proof.
type VerifiedGuardedPush struct {
	repository RepositoryID
	commit     CommitOID
	tree       TreeDigest
	remoteRef  RemoteRef
	outcome    GuardedPushOutcome
	verified   bool
}

// newVerifiedGuardedPush is the single, package-private constructor. It is
// unreachable from outside this package, so no caller can forge a proof.
func newVerifiedGuardedPush(input GuardedPushInput, outcome GuardedPushOutcome) VerifiedGuardedPush {
	return VerifiedGuardedPush{
		repository: input.repository,
		commit:     input.commit,
		tree:       input.tree,
		remoteRef:  input.remoteRef,
		outcome:    outcome,
		verified:   true,
	}
}

func (v VerifiedGuardedPush) Repository() RepositoryID    { return v.repository }
func (v VerifiedGuardedPush) Commit() CommitOID           { return v.commit }
func (v VerifiedGuardedPush) Tree() TreeDigest            { return v.tree }
func (v VerifiedGuardedPush) RemoteRef() RemoteRef        { return v.remoteRef }
func (v VerifiedGuardedPush) Outcome() GuardedPushOutcome { return v.outcome }

// Validate reports whether this is a genuine verified proof. A zero value, or a
// value with a zero/invalid repository, commit, tree, ref, or outcome, is
// rejected. Only GuardedPushExactCommit can produce a value that passes.
func (v VerifiedGuardedPush) Validate() error {
	if !v.verified {
		return effectError(
			"guarded-push proof is unverified or forged",
			"only a completed exact-target verification produces a valid proof",
			"VerifiedGuardedPush.Validate", "guarded-push proof validation",
			"a protected commit would trust an unverified landing",
			"obtain the proof from GuardedPushExactCommit; it cannot be constructed directly", nil,
		)
	}
	if !v.repository.IsValid() || !v.commit.IsValid() || !v.tree.IsValid() || !v.remoteRef.IsValid() || !v.outcome.IsValid() {
		return effectError(
			"guarded-push proof has a zero or invalid field",
			"a valid proof carries the exact repository, commit, tree, ref, and outcome that were verified",
			"VerifiedGuardedPush.Validate", "guarded-push proof validation",
			"a protected commit would trust an incomplete landing proof",
			"obtain the proof from GuardedPushExactCommit", nil,
		)
	}
	return nil
}

// MarshalJSON always fails: the proof is a process-local capability, not data.
// This guarantees the proof can never be serialized, logged as a token, or
// leave the application process by accident.
func (v VerifiedGuardedPush) MarshalJSON() ([]byte, error) {
	return nil, effectError(
		"VerifiedGuardedPush cannot be serialized",
		"the proof is a process-local landing capability, not a portable token; serializing it would let an unverified landing be replayed elsewhere",
		"VerifiedGuardedPush.MarshalJSON", "guarded-push proof codec",
		"the marshal is refused so no proof token escapes the process",
		"pass the proof in-process to the protected commit; serialize only the public event id and outcome", nil,
	)
}

// GuardedPushExactCommit is the one landing push. It verifies the exact local
// object, probes the remote before pushing (best-effort — an unreadable probe
// never blocks the push), performs only the CommitOID:RemoteRef update through
// pusher, re-reads the remote, and constructs the opaque VerifiedGuardedPush
// only when that re-read confirms the remote holds the exact commit. This
// re-read gate is the sole safety invariant and is unconditional: no outcome
// label ever bypasses it. A remote that already held the exact commit before
// this call ran is an idempotent replay success, labeled distinctly from a
// fresh push; a stale, racing, or different ref returns no proof. It makes no
// SQLite or multi-repository atomicity claim.
func GuardedPushExactCommit(input GuardedPushInput, pusher RepositoryPusher) (VerifiedGuardedPush, error) {
	if !input.IsValid() {
		return VerifiedGuardedPush{}, effectError(
			"guarded-push input is zero or invalid",
			"a landing push requires a constructor-validated GuardedPushInput",
			"GuardedPushExactCommit", "guarded push",
			"no push is attempted",
			"construct the input with NewGuardedPushInput", nil,
		)
	}
	if pusher == nil {
		return VerifiedGuardedPush{}, effectError(
			"guarded-push repository pusher is nil",
			"the verify/push/re-read primitives must be supplied by an injected RepositoryPusher",
			"GuardedPushExactCommit", "guarded push",
			"no push can be performed or verified",
			"pass a non-nil RepositoryPusher (a git-backed one in production, a fake in tests)", nil,
		)
	}
	if err := pusher.VerifyLocalObject(input.repository, input.commit, input.tree); err != nil {
		return VerifiedGuardedPush{}, effectError(
			fmt.Sprintf("local object %s could not be verified before landing", input.commit),
			"a guarded push confirms the exact local commit and tree exist before touching the remote",
			"GuardedPushExactCommit", "guarded-push local verification",
			"no push is attempted and no proof is produced",
			"ensure the exact commit and its tree exist locally before landing", err,
		)
	}

	// Best-effort pre-push probe: if the remote already holds the exact target,
	// a push that lands nothing (for example git's own "everything up to date"
	// no-op, which force-with-lease reports as success, not an error) must still
	// be labeled a replay rather than a fresh push. An unreadable or absent
	// probe result never blocks the push and never by itself produces a proof —
	// it only informs the outcome label decided after the mandatory post-push
	// re-read below.
	alreadyAtTarget := false
	if priorState, priorErr := pusher.ReadRemote(input.repository, input.remoteRef); priorErr == nil {
		if priorCommit, present := priorState.Commit(); present && priorCommit.Equal(input.commit) {
			alreadyAtTarget = true
		}
	}

	pushErr := pusher.PushExact(input.repository, input.commit, input.remoteRef, input.expectedOld)
	state, readErr := pusher.ReadRemote(input.repository, input.remoteRef)
	if readErr != nil {
		return VerifiedGuardedPush{}, effectError(
			fmt.Sprintf("remote ref %s could not be re-read after the push attempt", input.remoteRef),
			"a guarded push is verified only by re-reading the remote's exact state",
			"GuardedPushExactCommit", "guarded-push remote verification",
			"the landing is treated as unverified and no proof is produced",
			"ensure the remote ref is readable, then retry the guarded push", readErr,
		)
	}
	remoteCommit, present := state.Commit()
	if !present || !remoteCommit.Equal(input.commit) {
		return VerifiedGuardedPush{}, effectError(
			fmt.Sprintf("remote ref %s did not reach exact commit %s", input.remoteRef, input.commit),
			"the remote is absent, stale, or at a different commit, so the landing is not verified; a guarded push never returns a success value it did not confirm",
			"GuardedPushExactCommit", "guarded-push remote verification",
			"no proof is produced and the landing must be retried or investigated",
			"resolve the racing or stale remote state and retry the guarded push", pushErr,
		)
	}
	outcome := GuardedPushPushed
	switch {
	case alreadyAtTarget:
		// The pre-push probe confirmed the remote already held the exact target
		// before this call ran; the re-read above only reconfirms it. This call
		// advanced nothing, so it is a verified idempotent replay.
		outcome = GuardedPushIdempotentReplay
	case pushErr != nil:
		// The pre-push probe could not determine prior state (unreadable or
		// absent), but the pusher rejected the push (for example an
		// already-up-to-date rejection) and the mandatory re-read above still
		// confirms the exact target: this call did not itself advance the ref,
		// so it is an idempotent replay rather than a fresh push.
		outcome = GuardedPushIdempotentReplay
	}
	return newVerifiedGuardedPush(input, outcome), nil
}

// GuardedPushBatchResult is the per-repository result of a multi-repository
// guarded-push orchestration. Exactly one of Proof/Err is meaningful: a
// verified landing carries a Proof and nil Err; a failure carries a nil-proof
// Err. The batch makes no atomicity or rollback claim.
type GuardedPushBatchResult struct {
	Repository RepositoryID
	Proof      VerifiedGuardedPush
	Err        error
}

// Verified reports whether this repository's landing produced a valid proof.
func (r GuardedPushBatchResult) Verified() bool {
	return r.Err == nil && r.Proof.Validate() == nil
}

// GuardedPushBatch orchestrates a guarded push per repository input in order. It
// records an exact per-repository result and, on the first failure, stops
// attempting further pushes — but it never rolls back an already-verified
// landing and never claims cross-repository atomicity. Results already produced
// are returned as-is; not-yet-attempted repositories are absent from the slice.
func GuardedPushBatch(inputs []GuardedPushInput, pusher RepositoryPusher) []GuardedPushBatchResult {
	results := make([]GuardedPushBatchResult, 0, len(inputs))
	for _, input := range inputs {
		proof, err := GuardedPushExactCommit(input, pusher)
		results = append(results, GuardedPushBatchResult{
			Repository: input.Repository(),
			Proof:      proof,
			Err:        err,
		})
		if err != nil {
			// No rollback of prior verified landings; no atomicity claim. Stop
			// attempting further landings so a failure is not compounded.
			break
		}
	}
	return results
}
