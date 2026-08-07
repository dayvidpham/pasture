package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/lineage"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// HookLifecycleLineageInput selects the store, the identity binding whose
// occurrence chain is materialized, and the injected clock and operation
// identity source used only if a gated lineage write is required.
type HookLifecycleLineageInput struct {
	DBPath     string
	Binding    string
	Clock      receipt.Clock
	Operations receipt.OperationIDSource
}

// HookLifecycleLineage materializes and prints the committed occurrence lineage
// for one native identity binding.
//
// It is READ-SIDE: the hook delivery path is never involved and stays
// byte-equivalent. This command rebuilds the disposable occurrence projection,
// reads the bounded occurrences matching the binding plus every committed link,
// derives the MISSING predecessor edges (pure lineage.DeriveLinks), and — ONLY
// when edges are missing — legalizes exactly one lineage-links operation through
// the normative write gate and commits it BEFORE any bytes are written to out
// (commit-before-stdout). A re-run derives nothing and commits nothing, so the
// second invocation is a no-op that simply reprints the same chain.
func HookLifecycleLineage(ctx context.Context, out io.Writer, in HookLifecycleLineageInput, format string) (int, error) {
	if ctx == nil || out == nil || in.Clock == nil || in.Operations == nil || (format != "text" && format != "json") {
		return listResult(fmt.Errorf("materialize lifecycle lineage: context, output, clock, operation identity source, and format text|json are required"))
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
	committed, err := linkReader.Links()
	if err != nil {
		return listResult(err)
	}

	missing, err := lineage.DeriveLinks(records, committed)
	if err != nil {
		return listResult(err)
	}

	materialized := 0
	if len(missing) > 0 {
		harness, err := singleHarness(missing)
		if err != nil {
			return listResult(err)
		}
		// Legalize exactly ONE lineage-links operation through the normative
		// gate. The gate bounds the write to MaxLinksPerOperation and refuses an
		// over-cap derivation actionably (pagination is deferred).
		intent, refusal := gate.NewLineageIntent(harness, len(missing))
		if refusal != nil {
			return listResult(refusal)
		}
		warrant, refusal := gate.Legalize(intent)
		if refusal != nil {
			return listResult(refusal)
		}
		write, err := receipt.NewLineageLinks(missing)
		if err != nil {
			return listResult(err)
		}
		service, err := tasks.NewLifecycleReceiptService(tracker, in.Clock, in.Operations)
		if err != nil {
			return listResult(err)
		}
		// Commit-before-stdout: the durable operation commits here, before any
		// byte is written to out below.
		receiptResult, err := service.CommitLineage(ctx, warrant, write)
		if err != nil {
			return listResult(err)
		}
		materialized = receiptResult.Committed
		// Re-read committed links so the printed chain reflects durable truth,
		// including the edges just materialized.
		committed, err = linkReader.Links()
		if err != nil {
			return listResult(err)
		}
	}

	if err := writeLineageChain(out, records, committed, materialized, format); err != nil {
		return listResult(err)
	}
	return 0, nil
}

// readAllBoundOccurrences reads every occurrence matching the binding across all
// bounded pages, so derivation sees the complete chain. Each page is bounded
// (model.MaxPageSize); pagination follows the reader's own cursor.
func readAllBoundOccurrences(ctx context.Context, reader model.LifecycleReader, binding model.NativeBinding) ([]model.LifecycleRecord, error) {
	query := model.OccurrenceQuery{
		Bindings: []model.NativeBinding{binding},
		Page:     model.PageRequest{Size: model.MaxPageSize},
	}
	var records []model.LifecycleRecord
	for {
		page, err := reader.Records(ctx, query)
		if err != nil {
			return nil, err
		}
		records = append(records, page.Records()...)
		if page.State.Next == nil {
			break
		}
		query.Page.Cursor = page.State.Next
	}
	return records, nil
}

// singleHarness returns the one harness shared by every derived edge, or an
// actionable error when a binding's derivation spans more than one host — a
// lineage operation is legalized for exactly one harness, and chains are per
// host.
func singleHarness(facts []lineage.LinkFact) (ir.HarnessID, error) {
	harness := facts[0].Harness
	for _, fact := range facts {
		if fact.Harness != harness {
			return "", fmt.Errorf(
				"materialize lifecycle lineage: this binding's occurrences span harnesses %q and %q, but one lineage operation belongs to exactly one host; narrow the scope with --binding to a single harness's identity so the derivation yields one host's chain",
				harness, fact.Harness)
		}
	}
	return harness, nil
}

type lineageEdgeView struct {
	Harness string `json:"harness"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	From    int64  `json:"from"`
	To      int64  `json:"to"`
	LinkID  int64  `json:"linkId"`
}

type lineageView struct {
	Materialized int               `json:"materialized"`
	Links        []lineageEdgeView `json:"links"`
}

// writeLineageChain prints the committed links relevant to the queried
// occurrences (those whose endpoints are in the read set), in a stable order.
// It runs only after any gated commit, preserving commit-before-stdout.
func writeLineageChain(out io.Writer, records []model.LifecycleRecord, committed []model.LinkRecord, materialized int, format string) error {
	inScope := make(map[int64]struct{}, len(records))
	for _, record := range records {
		inScope[int64(record.Occurrence.JournalID())] = struct{}{}
	}
	edges := make([]lineageEdgeView, 0, len(committed))
	for _, link := range committed {
		_, from := inScope[int64(link.From.JournalID())]
		_, to := inScope[int64(link.To.JournalID())]
		if !from && !to {
			continue
		}
		edges = append(edges, lineageEdgeView{
			Harness: string(link.Harness),
			Kind:    kindString(link.Kind),
			Value:   link.Value,
			From:    int64(link.From.JournalID()),
			To:      int64(link.To.JournalID()),
			LinkID:  int64(link.LinkID.JournalID()),
		})
	}
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Value != b.Value {
			return a.Value < b.Value
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})

	if format == "json" {
		return json.NewEncoder(out).Encode(lineageView{Materialized: materialized, Links: edges})
	}
	if _, err := fmt.Fprintf(out, "materialized: %d new link(s)\n", materialized); err != nil {
		return err
	}
	if len(edges) == 0 {
		_, err := fmt.Fprintln(out, "no lineage links for this binding")
		return err
	}
	for _, edge := range edges {
		if _, err := fmt.Fprintf(out, "%s %s %s: %d -> %d (link %d)\n", edge.Harness, edge.Kind, edge.Value, edge.From, edge.To, edge.LinkID); err != nil {
			return err
		}
	}
	return nil
}

func kindString(kind runtime.NativeIdentityKind) string {
	if s := kind.String(); s != "" {
		return s
	}
	return fmt.Sprintf("kind-%d", uint8(kind))
}
