package apply

import (
	"errors"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

func TestPendingTrustFailedRemovePreservesFailureHistoryAndAddsGuidance(t *testing.T) {
	t.Parallel()
	c, _ := cell.New(artifact.HarnessCodex, cell.HooksAxis())
	key, _ := registry.GlobalKey(c)
	request := DirectFileRequest{operation: RemoveOp(), source: InstallerSource(), key: key, cell: c, strategy: activation.DirectFileKindValue()}
	record, err := registry.NewRecord(registry.RecordInput{
		Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(),
		Managed: true, Observation: registry.ObservationInstalled, Trust: registry.TrustNotApplicable,
		LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeFailed,
		Diagnostic: "unlink failed at hooks.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := PendingNativeTrustDirectFile(c, func(DirectFileRequest) error { return nil }, "review Codex hook trust after repairing removal")
	if err != nil {
		t.Fatal(err)
	}
	actionErr := errors.New("permission denied while removing hooks.json")
	out, err := policy.apply(request, Outcome{Status: Failed(), Observation: registry.ObservationInstalled, Record: &record, Diagnostic: "live hook remains installed"}, actionErr)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Failed() || out.Observation != registry.ObservationInstalled || out.Record == nil || out.Record.Trust() != registry.TrustPending || out.Record.LastOutcome() != registry.OutcomeFailed {
		t.Fatalf("failed removal truth changed: %+v", out)
	}
	if !strings.Contains(out.Diagnostic, "live hook remains installed") {
		t.Fatalf("outcome lost live removal history: %q", out.Diagnostic)
	}
	for _, diagnostic := range []string{out.Diagnostic, out.Record.Diagnostic()} {
		if !strings.Contains(diagnostic, "review Codex hook trust") {
			t.Fatalf("diagnostic lacks trust guidance: %q", diagnostic)
		}
	}
	if !strings.Contains(out.Record.Diagnostic(), "unlink failed") {
		t.Fatalf("record lost actionable removal history: %q", out.Record.Diagnostic())
	}
}
