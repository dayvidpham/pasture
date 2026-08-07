package handlers

import (
	stdcontext "context"
	"fmt"
	"io"

	lifecyclecontext "github.com/dayvidpham/pasture/internal/lifecycle/context"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// disclosurePolicyNote names the single static M5 disclosure policy recorded in
// every durable plan fact: disclose committed lifecycle records, links, and
// metamodel coordinates only. There is no alternate policy to select
// (ContextPolicyDefinitionRef stays a staked seam), so the note is a constant.
const disclosurePolicyNote = "pasture.lifecycle.context.m5-static: committed records, links, metamodel coordinates"

// HookLifecycleContextInput selects the store, the identity binding whose bounded
// committed state is disclosed, and the injected clock and operation identity
// source used by the single gated disclosure write.
type HookLifecycleContextInput struct {
	DBPath     string
	Binding    string
	Clock      receipt.Clock
	Operations receipt.OperationIDSource
}

// HookLifecycleContext builds the pure context-disclosure projection for one
// native identity binding, durably records it as ONE gated operation
// (plan + attempt + result facts) BEFORE printing the projection, and prints the
// projection to out.
//
// It is READ-SIDE for the host: the hook delivery path is never involved and
// host responses stay byte-identical Proceed (R3.2). The CLI is the sole
// disclosure consumer at M5. Ordering is UAT-resolution-6 exact: the disclosure
// operation commits BEFORE any byte is written to out (commit-before-print), so
// a post-commit stdout failure leaves the durable trail intact and is reported
// as an actionable error on the error path (routed to stderr, never exit code
// 2) — the same discipline as the landed encode-failure wrap in
// HookLifecycleNative. DisclosureReleased is the maximal honest disposition:
// Pasture commits and releases the projection to the stdout boundary but cannot
// observe host or operator consumption.
func HookLifecycleContext(ctx stdcontext.Context, out io.Writer, in HookLifecycleContextInput, format string) (int, error) {
	if ctx == nil || out == nil || in.Clock == nil || in.Operations == nil || (format != "text" && format != "json") {
		return listResult(fmt.Errorf("disclose lifecycle context: context, output, clock, operation identity source, and format text|json are required"))
	}
	binding, err := parseLifecycleBinding(in.Binding)
	if err != nil {
		return listResult(err)
	}

	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return listResult(err)
	}
	defer tracker.Close()
	if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
		return listResult(err)
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		return listResult(err)
	}
	records, err := readAllBoundOccurrences(ctx, reader, binding)
	if err != nil {
		return listResult(err)
	}

	linkReader := projection.LinkReader{Facts: tracker.Journal().Facts()}
	links, err := linkReader.Links()
	if err != nil {
		return listResult(err)
	}

	// Pure projection over committed records and links (no clock, store, or IO).
	input := lifecyclecontext.ContextInput{Scope: model.OccurrenceQuery{
		Bindings: []model.NativeBinding{binding},
		Page:     model.PageRequest{Size: model.MaxPageSize},
	}}
	projected, err := lifecyclecontext.Project(input, records, links)
	if err != nil {
		return listResult(err)
	}
	projectionBytes, err := projected.MarshalJSON()
	if err != nil {
		return listResult(err)
	}
	digest, err := projected.Digest()
	if err != nil {
		return listResult(err)
	}

	// Build the three disclosure facts committed together as one operation.
	plan := model.DisclosurePlanFact{
		Scope:      projected.ScopeFingerprint(),
		Projection: digest,
		Policy:     disclosurePolicyNote,
	}
	attempt := model.DisclosureAttemptFact{RecordedAt: in.Clock.Now().UTC()}
	result := model.DisclosureResultFact{Disposition: model.DisclosureReleased}
	write, err := receipt.NewDisclosure(plan, attempt, result)
	if err != nil {
		return listResult(err)
	}

	// Legalize exactly ONE disclosure operation through the normative gate; the
	// gate fingerprints the projection content digest as the write scope.
	intent, refusal := gate.NewDisclosureIntent(digest)
	if refusal != nil {
		return listResult(refusal)
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		return listResult(refusal)
	}
	service, err := tasks.NewLifecycleReceiptService(tracker, in.Clock, in.Operations)
	if err != nil {
		return listResult(err)
	}
	// Commit-before-print: the durable plan/attempt/result operation commits
	// here, before any byte is written to out below.
	if _, err := service.CommitDisclosure(ctx, warrant, write); err != nil {
		return listResult(err)
	}

	// The projection is now durably recorded (DisclosureReleased). Print it. A
	// failure here is POST-COMMIT: the durable trail is intact and only the
	// stdout release did not complete, mirroring the landed encode-failure wrap.
	if err := writeContextProjection(out, projected, projectionBytes, format); err != nil {
		return listResult(fmt.Errorf("disclose lifecycle context: the disclosure was durably committed but the projection could not be written to stdout (the durable plan/attempt/result trail is intact; only the stdout release failed): %w", err))
	}
	return 0, nil
}

// writeContextProjection releases the committed projection to out. The json form
// is the EXACT canonical projection bytes (its digest is the committed plan's
// projection fingerprint), so a consumer can verify the released bytes against
// the durable record. The text form is a bounded human summary. It runs only
// after the gated commit, preserving commit-before-print.
func writeContextProjection(out io.Writer, projected lifecyclecontext.ContextProjection, projectionBytes []byte, format string) error {
	if format == "json" {
		if _, err := out.Write(projectionBytes); err != nil {
			return err
		}
		_, err := out.Write([]byte("\n"))
		return err
	}
	scope := projected.ScopeFingerprint()
	digest, err := projected.Digest()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "disclosure released (scope %x, projection %x)\n%s\n",
		scope[:8], digest[:8], projectionBytes)
	return err
}
