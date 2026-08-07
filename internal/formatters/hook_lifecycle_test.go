package formatters_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/lifecycle/codebook"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// TestHookLifecycleFormatterRendersCodebookColumn proves the read surface
// discloses the codebook coordinate for interpreted.v2 records and renders the
// pre-M5 unresolved disclosure for interpreted.v1 records — never inventing a
// coordinate for a legacy record.
func TestHookLifecycleFormatterRendersCodebookColumn(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle().ID()
	identities := []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "s"}}

	v1, err := model.NewInterpretedRecord(model.InterpretationID(10), model.OccurrenceID(1), runtime.SemanticObservation, identities, nil, contract)
	if err != nil {
		t.Fatalf("build v1 interpreted record: %v", err)
	}
	v2, err := model.NewInterpretedRecordWithCodebook(model.InterpretationID(20), model.OccurrenceID(2), runtime.SemanticObservation, identities, nil, contract, codebook.Active())
	if err != nil {
		t.Fatalf("build v2 interpreted record: %v", err)
	}

	rec1, err := model.NewLifecycleRecord(newOccurrence(1, contract), []model.InterpretedRecord{v1})
	if err != nil {
		t.Fatalf("build v1 lifecycle record: %v", err)
	}
	rec2, err := model.NewLifecycleRecord(newOccurrence(2, contract), []model.InterpretedRecord{v2})
	if err != nil {
		t.Fatalf("build v2 lifecycle record: %v", err)
	}
	page := model.LifecyclePage{Items: []model.LifecycleRecord{rec1, rec2}}

	var text bytes.Buffer
	if err := formatters.HookLifecycle(&text, page, "text"); err != nil {
		t.Fatalf("format text: %v", err)
	}
	out := text.String()
	if !strings.Contains(out, "codebook=unresolved (pre-M5)") {
		t.Fatalf("text output missing pre-M5 disclosure for the v1 record:\n%s", out)
	}
	active := codebook.Active()
	shortContent := hex.EncodeToString(active.Content[:])[:12]
	wantV2 := "codebook=pasture.lifecycle.codebook@1#" + shortContent
	if !strings.Contains(out, wantV2) {
		t.Fatalf("text output missing v2 coordinate %q:\n%s", wantV2, out)
	}
}

func newOccurrence(id int64, contract ir.RuntimeContractID) model.OccurrenceRecord {
	return model.NewOccurrenceRecord(model.OccurrenceID(id), model.ContractEventKind(1), contract, model.OccurrenceEnvelopeRef{}, time.Unix(0, 0).UTC(), provenance.AgentID{}, nil, model.CaptureValid, model.EvidencePayloadRef{})
}
