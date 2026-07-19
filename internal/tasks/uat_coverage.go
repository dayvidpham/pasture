// Package tasks — Implementation-UAT carry-forward and coverage digest (#49).
//
// uat_coverage.go computes the two deterministic quantities the Implementation-UAT gate and
// the land command depend on, both as pure functions over typed inputs:
//
//  1. RequiredDecisions — the sorted, unique set of decision targets an Implementation UAT
//     must resolve. There is NO "covered baseline" and NO caller RequiresUAT flag. The
//     required set is exactly:
//       - every material agent-judgment entry id in the entire current epoch decision
//         ledger (an entry whose Decider is a non-human agent);
//       - every held question from the current deferred Plan decision; and
//       - every deferred Plan-feedback id from that decision;
//     minus the targets of earlier valid typed resolutions stored in prior canonical
//     Implementation-UAT payloads. One target has at most one effective resolution.
//
//  2. ComputeCoverageDigest — the version-prefixed, length-delimited digest over the sorted
//     required refs, the exact resolutions, the PlanDecisionID, the IntegrationCandidateSetID,
//     and the input ledger revision. Land recomputes it from the exact accepted inputs and
//     compares it to the stored digest (VerifyCoverage); any later ledger append changes the
//     required set and therefore the digest, invalidating a stale UAT.
//
// Because the required set and digest are pure functions of their inputs, a permuted input
// is stable (same digest) while any changed, tampered, or stale input yields a different
// digest — the exact stability/mismatch oracle the acceptance criteria require.

package tasks

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// RequiredRefKind is the closed discriminator of a required-decision reference. It records
// which carry-forward domain a target belongs to so two domains that happen to share an id
// string never collide.
type RequiredRefKind int

const (
	requiredRefInvalid RequiredRefKind = iota
	// RequiredLedgerDecision is a material agent-judgment ledger entry.
	RequiredLedgerDecision
	// RequiredHeldQuestion is a held question carried from the deferred Plan decision.
	RequiredHeldQuestion
	// RequiredDeferredFeedback is a deferred Plan-feedback item carried forward.
	RequiredDeferredFeedback
)

func (k RequiredRefKind) String() string {
	switch k {
	case RequiredLedgerDecision:
		return "ledger-decision"
	case RequiredHeldQuestion:
		return "held-question"
	case RequiredDeferredFeedback:
		return "deferred-feedback"
	default:
		return fmt.Sprintf("RequiredRefKind(%d)", int(k))
	}
}

// RequiredRef is one required-decision target: its domain and its id string.
type RequiredRef struct {
	Kind RequiredRefKind
	ID   string
}

// less orders required refs canonically by (kind, id) so the sorted set is deterministic.
func (r RequiredRef) less(o RequiredRef) bool {
	if r.Kind != o.Kind {
		return r.Kind < o.Kind
	}
	return r.ID < o.ID
}

// CoverageInput is the exact typed input the required-decision set is computed from. The
// entire current epoch ledger is scanned for material agent-judgments; the deferred Plan
// decision supplies the held questions and deferred feedback; PriorResolutions are the
// resolutions from earlier canonical Implementation-UAT payloads whose targets are removed.
type CoverageInput struct {
	LedgerEntries    []DecisionLedgerEntry
	DeferredPlan     *PlanDeferredByAFK
	PriorResolutions []ImplUATPayload
}

// isMaterialAgentJudgment reports whether a ledger entry is a material agent-judgment: a
// decision whose Decider is a non-human agent (machine-learning or software). A human
// (user) decision is not carried forward — it is already the user's own decision.
func isMaterialAgentJudgment(e DecisionLedgerEntry) bool {
	return e.Actor.DeciderKind != provenance.AgentKindHuman
}

// MaterialAgentJudgments returns the sorted, unique ids of the material agent-judgment
// entries in a ledger. It is exposed so a caller can inspect the raw judgment set
// independently of the resolution subtraction RequiredDecisions performs.
func MaterialAgentJudgments(entries []DecisionLedgerEntry) []DecisionLedgerEntryID {
	seen := map[DecisionLedgerEntryID]bool{}
	out := make([]DecisionLedgerEntryID, 0, len(entries))
	for _, e := range entries {
		if !isMaterialAgentJudgment(e) {
			continue
		}
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RequiredDecisions computes the sorted, unique required-decision set from a CoverageInput.
// The result is the union of the three domains minus every target resolved by a prior
// canonical Implementation-UAT payload. The input is never mutated.
func RequiredDecisions(in CoverageInput) []RequiredRef {
	// Build the resolved-target sets first so a resolved target is never emitted.
	resolvedLedger := map[DecisionLedgerEntryID]bool{}
	resolvedHeld := map[HeldUATQuestionID]bool{}
	resolvedFeedback := map[UATFeedbackID]bool{}
	for _, prior := range in.PriorResolutions {
		for _, r := range prior.LedgerDecisions {
			resolvedLedger[r.Target] = true
		}
		for _, r := range prior.HeldAnswers {
			resolvedHeld[r.Target] = true
		}
		for _, r := range prior.PlanFeedback {
			resolvedFeedback[r.Target] = true
		}
	}

	seen := map[RequiredRef]bool{}
	out := make([]RequiredRef, 0)
	add := func(ref RequiredRef) {
		if seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}

	for _, id := range MaterialAgentJudgments(in.LedgerEntries) {
		if resolvedLedger[id] {
			continue
		}
		add(RequiredRef{Kind: RequiredLedgerDecision, ID: string(id)})
	}
	if in.DeferredPlan != nil {
		for _, q := range in.DeferredPlan.HeldQuestions {
			if resolvedHeld[q.ID] {
				continue
			}
			add(RequiredRef{Kind: RequiredHeldQuestion, ID: string(q.ID)})
		}
		for _, f := range in.DeferredPlan.Feedback {
			if resolvedFeedback[f.ID] {
				continue
			}
			add(RequiredRef{Kind: RequiredDeferredFeedback, ID: string(f.ID)})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].less(out[j]) })
	return out
}

// CoverageDigestSpec is the exact set of inputs a coverage digest is computed over. The
// required refs and resolutions are sorted canonically inside ComputeCoverageDigest, so a
// caller need not pre-sort; the digest is invariant under input permutation but changes
// under any value change.
type CoverageDigestSpec struct {
	RequiredRefs   []RequiredRef
	Payload        ImplUATPayload
	PlanDecision   PlanUATDecisionID
	IntegrationSet IntegrationCandidateSetID
	InputLedger    DocumentRevisionID
}

const coverageDigestVersion = "pasture.coverage-digest/v1"

// ComputeCoverageDigest computes the deterministic coverage digest for a spec using a
// version-prefixed, length-delimited encoding. Every variable-length field is written as
// uvarint(len) followed by its bytes, so no two distinct field boundaries can alias, and
// the required refs and the three resolution domains are sorted canonically so input
// permutation does not change the digest. Duplicate targets within a domain are rejected
// upstream (validateImplUATPayload), so the encoded resolution list is a set.
//
// Scope: the digest folds each resolution's (domain, target, kind) plus the reported
// verdict — it deliberately excludes the free-text Note (and interaction/feedback bodies).
// This is intentionally decision-bearing-only: (domain, target, kind) is the exact
// information VerifyCoverage's mismatch check needs (a later ledger append changes the
// required-ref set, or a resolution's disposition changes, and either flips the digest);
// Note is documentation attached to a resolution, not itself a decision, so a Note-only
// edit does not stale a previously accepted coverage digest.
func ComputeCoverageDigest(spec CoverageDigestSpec) (CoverageDigest, error) {
	var buf bytes.Buffer
	writeDelimited(&buf, []byte(coverageDigestVersion))
	writeDelimited(&buf, []byte(spec.PlanDecision))
	writeDelimited(&buf, []byte(spec.IntegrationSet))
	writeDelimited(&buf, []byte(spec.InputLedger))

	refs := make([]RequiredRef, len(spec.RequiredRefs))
	copy(refs, spec.RequiredRefs)
	sort.Slice(refs, func(i, j int) bool { return refs[i].less(refs[j]) })
	writeCount(&buf, len(refs))
	for _, ref := range refs {
		writeDelimited(&buf, []byte(ref.Kind.String()))
		writeDelimited(&buf, []byte(ref.ID))
	}

	// Flatten the three resolution domains into one canonically-sorted, domain-tagged list
	// so the digest folds every resolution exactly once in a permutation-independent order.
	type resolution struct{ domain, target, kind string }
	resolutions := make([]resolution, 0,
		len(spec.Payload.LedgerDecisions)+len(spec.Payload.HeldAnswers)+len(spec.Payload.PlanFeedback))
	for _, r := range spec.Payload.LedgerDecisions {
		resolutions = append(resolutions, resolution{"ledger-decision", string(r.Target), r.Kind.String()})
	}
	for _, r := range spec.Payload.HeldAnswers {
		resolutions = append(resolutions, resolution{"held-question", string(r.Target), r.Kind.String()})
	}
	for _, r := range spec.Payload.PlanFeedback {
		resolutions = append(resolutions, resolution{"deferred-feedback", string(r.Target), r.Kind.String()})
	}
	sort.Slice(resolutions, func(i, j int) bool {
		if resolutions[i].domain != resolutions[j].domain {
			return resolutions[i].domain < resolutions[j].domain
		}
		return resolutions[i].target < resolutions[j].target
	})
	writeCount(&buf, len(resolutions))
	for _, r := range resolutions {
		writeDelimited(&buf, []byte(r.domain))
		writeDelimited(&buf, []byte(r.target))
		writeDelimited(&buf, []byte(r.kind))
	}

	// Fold the verdict last so an accept/changes-requested flip changes the digest.
	writeDelimited(&buf, []byte(spec.Payload.ReportedVerdict.String()))

	return CoverageDigest(sha256.Sum256(buf.Bytes())), nil
}

// VerifyCoverage recomputes the coverage digest from spec and compares it to the stored
// digest, returning an actionable error on any mismatch. It is the pure core of land's
// invalidation check: the live land command reloads the accepted Implementation-UAT entry,
// recomputes its required set from the exact accepted inputs, and calls VerifyCoverage; a
// later ledger append changes the required set and fails this comparison.
func VerifyCoverage(stored CoverageDigest, spec CoverageDigestSpec) error {
	recomputed, err := ComputeCoverageDigest(spec)
	if err != nil {
		return err
	}
	if recomputed != stored {
		storedText, _ := stored.MarshalText()
		recomputedText, _ := recomputed.MarshalText()
		return coverageErr("VerifyCoverage",
			fmt.Sprintf("the recomputed coverage digest %s does not match the accepted digest %s", recomputedText, storedText),
			"the required-decision set or resolutions changed after the UAT was accepted (for example a later ledger append), so the accepted coverage no longer holds",
			"re-run Implementation UAT against the current ledger before landing")
	}
	return nil
}

// writeDelimited writes a length-delimited byte field: uvarint(len(b)) followed by b.
func writeDelimited(buf *bytes.Buffer, b []byte) {
	writeCount(buf, len(b))
	buf.Write(b)
}

// writeCount writes a non-negative count as a uvarint.
func writeCount(buf *bytes.Buffer, n int) {
	var scratch [binary.MaxVarintLen64]byte
	m := binary.PutUvarint(scratch[:], uint64(n))
	buf.Write(scratch[:m])
}

func coverageErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected a coverage operation: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("Implementation-UAT coverage (internal/tasks/uat_coverage.go, %s).", where),
		Impact:   "The coverage digest did not verify; landing must not proceed.",
		Fix:      fix + ".",
	}
}
