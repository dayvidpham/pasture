package provadapter

import (
	"errors"
	"fmt"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// facade.go is the thin Apply/LookupCommitted facade over the pinned Provenance
// JournalAPI (pasture#14). It is a SINGLE write path to the global journal — there
// is deliberately no split audit-store — and it routes every call through the
// adapter's own identity/authority/digest conversions (refs.go, authority.go,
// digest.go) so the portable protocol domain and the durable Provenance domain
// meet in exactly one place.
//
// Scope boundary (pasture#14, deferred to #43). The facade does NOT define any
// pasture.* material event, effect-to-command mapping, task-closure policy, or the
// meaning of an operation's effects. #43, the exclusive consumer, builds the
// caller-validated []provenance.Effect and passes it opaquely through Apply; the
// facade only assembles the operation identity/authority/digest around those
// effects and hands the whole operation to the journal in one atomic commit.
//
// Closed outcome surface. Both Apply and LookupCommitted return the same closed
// three-way Outcome — Absent, Committed, or Conflict — mirroring Provenance's own
// CommittedResultKind exactly (§3.2, §9.4). LookupCommitted, keyed only on an
// OperationID, can never observe a Conflict (it compares no identity), so it
// yields only Absent or Committed; Apply, which compares the full four-field
// replay identity, is the sole source of a Conflict outcome (§9.4, §11).
//
// Typed-error round-tripping. On a conflict Apply returns BOTH the closed Conflict
// outcome AND the underlying typed error, so a caller may switch on Outcome.Kind
// or recover the provenance typed error with errors.Is(err,
// provenance.ErrOperationConflict) / errors.As(err, **provenance.OperationConflict).
// Every other journal error (validation, authority scope, genesis discipline, …)
// is returned verbatim with a zero Outcome so its provenance sentinel/typed shape
// survives the boundary unchanged.

// OutcomeKind is the closed variant of a facade Apply/LookupCommitted result. It
// is a 1:1 mirror of provenance.CommittedResultKind, re-expressed in the adapter's
// own vocabulary so callers switch on a total, exhaustive set.
type OutcomeKind int

const (
	// OutcomeAbsent: no committed operation exists for the mutation reference.
	// LookupCommitted returns this with a nil error and no side effects; Apply
	// never returns it (a fresh Apply either commits or conflicts).
	OutcomeAbsent OutcomeKind = iota
	// OutcomeCommitted: the operation is committed. For a fresh Apply it is the
	// just-folded result; for a replayed Apply or a LookupCommitted it is the
	// reconstructed already-committed result (Committed.ShortCircuited distinguishes
	// an idempotent Apply replay).
	OutcomeCommitted
	// OutcomeConflict: the mutation reference was reused with a differing four-field
	// replay identity (§11). Only Apply produces it; nothing was committed.
	OutcomeConflict
)

func (k OutcomeKind) String() string {
	switch k {
	case OutcomeAbsent:
		return "OutcomeAbsent"
	case OutcomeCommitted:
		return "OutcomeCommitted"
	case OutcomeConflict:
		return "OutcomeConflict"
	default:
		return fmt.Sprintf("OutcomeKind(%d)", int(k))
	}
}

// Outcome is the closed result of a facade Apply or LookupCommitted. Committed
// carries the underlying provenance.CommittedResult verbatim (anchor JournalID,
// the emitted task-event closure in JournalID order, and the slot-keyed result
// bindings) for the OutcomeCommitted case; Conflict carries the typed
// provenance.OperationConflict for the OutcomeConflict case. Both are the exact
// values Provenance returned — the facade re-shapes only the discriminator, never
// the payload.
type Outcome struct {
	Kind      OutcomeKind
	Committed provenance.CommittedResult
	Conflict  *provenance.OperationConflict
}

// ApplyRequest is one logical operation expressed in the adapter's boundary
// vocabulary. The facade converts Mutation and Command through the adapter's own
// conversions (OperationIDFromRef, CommandDigestBytes) and validates Actor before
// touching the journal; Effects are the caller-validated, #43-produced operation
// body passed through opaquely.
type ApplyRequest struct {
	// Mutation is the portable idempotency handle for this logical operation; it
	// converts to the Provenance OperationID the §9.4 replay short-circuit keys on,
	// so a stable Mutation across retries is a stable OperationID.
	Mutation portable.MutationRef
	// Actor is the already-resolved committing actor of the whole operation (§2.1):
	// the pasture-system default from activation, or a historical actor from
	// HistoricalActorID. It is validated with ValidateActorID before the store call.
	Actor provenance.ActorID
	// Authority is the JournalID of the bootstrap/assignment authority this
	// operation executes under; nil marks a genesis operation whose sole effect is
	// one bootstrap authority (§4.6, §10 rule 6).
	Authority *provenance.JournalID
	// Command is the #43-produced canonical command digest; the facade maps it
	// byte-preservingly to the Provenance CommandDigest with no re-canonicalization
	// and no caller digest flag (digest.go).
	Command ir.CanonicalCommandDigest
	// MutationDigest is the opaque structural mutation digest #43 supplies; it is
	// required by Apply (§3.1) and passes through unchanged to participate in the
	// four-field replay identity.
	MutationDigest []byte
	// RecordedAt is the caller-supplied wall-clock stamp copied onto the operation
	// anchor for audit/display only (§12); it never establishes causality or order.
	RecordedAt int64
	// Effects is the caller-validated, #43-produced operation body folded in slice
	// order (§9.3.1). The facade neither defines nor interprets it.
	Effects []provenance.Effect
}

// Journal is the thin facade over a single Provenance JournalAPI. Construct it
// with NewJournal over the JournalAPI from an open Provenance tracker; every
// mutation flows through the one underlying journal, so there is no split write
// path.
type Journal struct {
	api provenance.JournalAPI
}

// NewJournal wraps a Provenance JournalAPI (e.g. Tracker.Journal()) in the facade.
// A nil api is rejected so the failure surfaces at construction rather than as a
// nil-dereference on the first call.
func NewJournal(api provenance.JournalAPI) (*Journal, error) {
	if api == nil {
		return nil, errors.New(
			"provadapter: cannot construct facade Journal — what: the Provenance JournalAPI is nil; " +
				"why: the facade routes every Apply/LookupCommitted to exactly one underlying journal and " +
				"has no fallback store; where: internal/provadapter NewJournal; when: at facade construction; " +
				"impact: no operation can be committed or looked up; fix: pass Tracker.Journal() from an open " +
				"Provenance tracker")
	}
	return &Journal{api: api}, nil
}

// Apply commits one logical operation atomically through the single journal write
// path (§9.5). It converts the mutation reference to an OperationID, the canonical
// command digest to raw Provenance command-digest bytes, and validates the
// committing actor, then folds the caller's effects in one operation. It returns:
//
//   - OutcomeCommitted, nil — the operation committed (fresh) or replayed
//     idempotently (Committed.ShortCircuited is true on a §9.4 replay).
//   - OutcomeConflict, err — the OperationID was reused with a differing four-field
//     identity (§11); nothing was committed and err wraps
//     provenance.ErrOperationConflict for errors.Is/As recovery.
//   - zero Outcome, err — a boundary conversion or journal error (validation,
//     authority scope, genesis discipline, …), returned verbatim.
func (j *Journal) Apply(req ApplyRequest) (Outcome, error) {
	op, err := OperationIDFromRef(req.Mutation)
	if err != nil {
		return Outcome{}, fmt.Errorf("provadapter: facade Apply: convert mutation reference: %w", err)
	}
	if err := ValidateActorID(req.Actor); err != nil {
		return Outcome{}, fmt.Errorf("provadapter: facade Apply for operation %q: %w", op, err)
	}
	command, err := CommandDigestBytes(req.Command)
	if err != nil {
		return Outcome{}, fmt.Errorf("provadapter: facade Apply for operation %q: convert command digest: %w", op, err)
	}
	if len(req.MutationDigest) == 0 {
		return Outcome{}, fmt.Errorf(
			"provadapter: facade Apply for operation %q — what: MutationDigest is empty; why: Provenance "+
				"requires both a command digest and a structural mutation digest to form the four-field replay "+
				"identity (§3.1); where: internal/provadapter Journal.Apply; when: before the store call; "+
				"impact: no operation is committed; fix: supply the #43-produced structural mutation digest",
			op)
	}

	res, err := j.api.Apply(provenance.OperationInput{
		OperationID:        op,
		ActorID:            req.Actor,
		AuthorityJournalID: req.Authority,
		CommandDigest:      command,
		MutationDigest:     req.MutationDigest,
		RecordedAt:         req.RecordedAt,
		Effects:            req.Effects,
	})
	if err != nil {
		// A reuse conflict is a closed outcome AND a typed error: surface both so a
		// caller may switch on Kind or recover the typed conflict with errors.Is/As.
		if errors.Is(err, provenance.ErrOperationConflict) {
			return Outcome{Kind: OutcomeConflict, Conflict: res.Conflict}, err
		}
		return Outcome{}, fmt.Errorf("provadapter: facade Apply for operation %q: %w", op, err)
	}
	return outcomeFromResult(res), nil
}

// LookupCommitted returns the side-effect-free committed result for a mutation
// reference (§9.4). It converts the reference to an OperationID and reads the
// journal, returning OutcomeAbsent for a never-applied operation (nil error, no
// side effects) or OutcomeCommitted with the reconstructed result. It never
// returns OutcomeConflict: a lookup compares no identity, so an absent and a
// committed result are the only closed variants (§3.2).
func (j *Journal) LookupCommitted(mutation portable.MutationRef) (Outcome, error) {
	op, err := OperationIDFromRef(mutation)
	if err != nil {
		return Outcome{}, fmt.Errorf("provadapter: facade LookupCommitted: convert mutation reference: %w", err)
	}
	res, err := j.api.LookupCommitted(op)
	if err != nil {
		return Outcome{}, fmt.Errorf("provadapter: facade LookupCommitted for operation %q: %w", op, err)
	}
	return outcomeFromResult(res), nil
}

// outcomeFromResult maps a provenance.CommittedResult's closed kind onto the
// facade's closed Outcome, carrying the underlying payload verbatim. An unknown
// kind is impossible against the pinned surface but is surfaced as a zero Outcome
// rather than silently mis-mapped.
func outcomeFromResult(res provenance.CommittedResult) Outcome {
	switch res.Kind {
	case provenance.CommittedAbsent:
		return Outcome{Kind: OutcomeAbsent}
	case provenance.CommittedExact:
		return Outcome{Kind: OutcomeCommitted, Committed: res}
	case provenance.CommittedConflict:
		return Outcome{Kind: OutcomeConflict, Conflict: res.Conflict}
	default:
		return Outcome{}
	}
}
