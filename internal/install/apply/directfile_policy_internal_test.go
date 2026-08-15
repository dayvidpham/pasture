package apply

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

func policyFixture(t *testing.T) (DirectFileRequest, registry.Record) {
	t.Helper()
	c, _ := cell.New(artifact.HarnessCodex, cell.HooksAxis())
	projectRoot, err := registry.CanonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := registry.ProjectKey(projectRoot, c)
	path, _ := artifact.NewPath("hooks.json")
	mode, _ := artifact.NewMode(0o644)
	entry, _ := artifact.NewFileEntry(path, mode, artifact.DigestBytes([]byte("hooks")))
	manifest, _ := artifact.NewManifest(entry)
	bundle, err := artifact.NewBundle(fstest.MapFS{"hooks.json": &fstest.MapFile{Data: []byte("hooks"), Mode: 0o644}}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := activation.NewDirectFile(bundle, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	act, err := activation.NewComponentActivation(c, direct)
	if err != nil {
		t.Fatal(err)
	}
	version, _ := registry.NewVersion("1.0.0")
	selector, _ := registry.NewSelector("hooks")
	leaf, _ := registry.NewLeaf(path, artifact.RegularFileType(), mode, artifact.DigestBytes([]byte("hooks")))
	dir, _ := artifact.NewPath("codex/hooks")
	configID, _ := registry.NewSharedConfigIdentity("hooks")
	config, _ := registry.NewSharedConfigOwnership(dir, configID, artifact.DigestBytes([]byte("hooks")))
	record, err := registry.NewRecord(registry.RecordInput{
		Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true,
		ArtifactID: bundle.ID(), Version: version, Selector: selector, Leaves: []registry.Leaf{leaf},
		CreatedDirs: []artifact.Path{dir}, SharedConfig: []registry.SharedConfigOwnership{config}, Observation: registry.ObservationInstalled,
		Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newDirectFileRequest(Ensure(), InstallerSource(), key, act, &record), record
}

func withRecord(t *testing.T, base registry.Record, mutate func(*registry.RecordInput)) registry.Record {
	t.Helper()
	in := registry.RecordInput{Key: base.Key(), Source: base.Source(), Strategy: base.Strategy(), Managed: base.Managed(), ArtifactID: base.ArtifactID(), Version: base.Version(), Selector: base.Selector(), Leaves: base.Leaves(), CreatedDirs: base.CreatedDirs(), SharedConfig: base.SharedConfig(), Observation: base.Observation(), Trust: base.Trust(), LastOperation: base.LastOperation(), LastOutcome: base.LastOutcome(), Diagnostic: base.Diagnostic()}
	mutate(&in)
	out, err := registry.NewRecord(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestValidateDirectFileDecorationRejectsEveryProtectedBoundary(t *testing.T) {
	t.Parallel()
	request, base := policyFixture(t)
	before := Outcome{Status: Completed(), Observation: registry.ObservationInstalled, Record: &base, Diagnostic: "generic"}
	cases := []struct {
		name   string
		mutate func(registry.Record) registry.Record
		want   string
	}{
		{"key", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) {
				other, _ := cell.New(artifact.HarnessCodex, cell.AgentsAxis())
				in.Key, _ = registry.ProjectKey(request.Key().ProjectRoot(), other)
			})
		}, "identity"},
		{"source", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Source = registry.SourceHomeManager })
		}, "source"},
		{"strategy", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Strategy = activation.NativePluginKindValue() })
		}, "strategy"},
		{"managed", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Managed = false })
		}, "ownership"},
		{"artifact", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) {
				in.ArtifactID, _ = artifact.ParseBundleID("artifact.bundle.v1:sha256:" + strings.Repeat("a", 64))
			})
		}, "ownership"},
		{"version", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Version, _ = registry.NewVersion("2.0.0") })
		}, "ownership"},
		{"selector", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Selector, _ = registry.NewSelector("other") })
		}, "ownership"},
		{"leaves", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Leaves = nil })
		}, "ownership"},
		{"created-dirs", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.CreatedDirs = nil })
		}, "ownership"},
		{"shared-config", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.SharedConfig = nil })
		}, "ownership"},
		{"observation", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Observation = registry.ObservationAbsent })
		}, "observation"},
		{"operation", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.LastOperation = registry.OperationInspect })
		}, "operation"},
		{"outcome", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.LastOutcome = registry.OutcomeFailed })
		}, "operation"},
		{"trust", func(r registry.Record) registry.Record {
			return withRecord(t, r, func(in *registry.RecordInput) { in.Trust = registry.TrustTrusted })
		}, "trusted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			afterRecord := tc.mutate(base)
			after := before
			after.Record = &afterRecord
			if err := validateDirectFileDecoration(request, before, after, nil); err == nil {
				t.Fatalf("mutation accepted: err=%v", err)
			}
		})
	}
	for _, tc := range []struct {
		name      string
		status    Status
		obs       registry.Observation
		actionErr error
	}{
		{"invalid-status", Status{name: "forged"}, registry.ObservationInstalled, nil},
		{"failed-success", Failed(), registry.ObservationInstalled, nil},
		{"success-failure", Completed(), registry.ObservationInstalled, errors.New("execute failed")},
		{"ensure-absent", Completed(), registry.ObservationAbsent, nil},
		{"bad-observation", Completed(), registry.Observation(99), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := before
			after.Status, after.Observation, after.Diagnostic = tc.status, tc.obs, "rewritten diagnostic"
			if err := validateDirectFileDecoration(request, before, after, tc.actionErr); err == nil {
				t.Fatal("contradictory outcome accepted")
			}
		})
	}
	remove := request
	remove.operation = RemoveOp()
	failedRecord := withRecord(t, base, func(in *registry.RecordInput) {
		in.LastOperation = registry.OperationRemove
		in.LastOutcome = registry.OutcomeFailed
	})
	failed := before
	failed.Status = Failed()
	failed.Record = &failedRecord
	removeBefore := before
	removeBefore.Record = &failedRecord
	if err := validateDirectFileDecoration(remove, removeBefore, failed, errors.New("remove failed")); err != nil {
		t.Fatalf("valid failed removal rejected: %v", err)
	}
}

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
