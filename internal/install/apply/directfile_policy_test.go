package apply_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

func directBinding(t *testing.T, c cell.Cell, root string) (registry.Key, activation.ComponentActivation) {
	t.Helper()
	path, _ := artifact.NewPath("component.txt")
	mode, _ := artifact.NewMode(0o644)
	entry, _ := artifact.NewFileEntry(path, mode, artifact.DigestBytes([]byte("content")))
	manifest, _ := artifact.NewManifest(entry)
	bundle, err := artifact.NewBundle(fstest.MapFS{"component.txt": &fstest.MapFile{Data: []byte("content"), Mode: 0o644}}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	strategy, err := activation.NewDirectFile(bundle, root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := activation.NewComponentActivation(c, strategy)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := registry.GlobalKey(c)
	return key, binding
}

func TestDirectFileActivatorRejectsEmptyDuplicateMissingAndForeignPolicies(t *testing.T) {
	t.Parallel()
	if _, err := apply.NewDirectFileActivator(); err == nil {
		t.Fatal("empty compatibility activator accepted")
	}
	c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	policy, _ := apply.PassThroughDirectFile(c)
	if _, err := apply.NewDirectFileActivator(policy, policy); err == nil {
		t.Fatal("duplicate policy accepted")
	}
	activator, err := apply.NewDirectFileActivator(policy)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
	_, otherBinding := directBinding(t, other, filepath.Join(t.TempDir(), "other"))
	if err := activator.ValidateBindings([]activation.ComponentActivation{otherBinding}); err == nil {
		t.Fatal("missing and foreign policy set accepted")
	}
	command, _ := activation.NewCommandSchema("host", "plugin")
	native, _ := activation.NewNativePlugin("selector", command)
	nativeBinding, _ := activation.NewComponentActivation(c, native)
	if err := activator.ValidateBindings([]activation.ComponentActivation{nativeBinding}); err == nil {
		t.Fatal("non-DirectFile binding accepted")
	}
}

func TestNilDirectFileActivatorReturnsActionableErrors(t *testing.T) {
	t.Parallel()
	var activator *apply.DirectFileActivator
	c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	key, binding := directBinding(t, c, filepath.Join(t.TempDir(), "target"))
	if err := activator.ValidateBindings([]activation.ComponentActivation{binding}); err == nil || !strings.Contains(err.Error(), "receiver is nil") {
		t.Fatalf("binding error=%v", err)
	}
	if _, err := activator.Inspect(context.Background(), apply.InstallerSource(), key, binding, nil); err == nil || !strings.Contains(err.Error(), "receiver is nil") {
		t.Fatalf("inspect error=%v", err)
	}
	if _, err := activator.Ensure(context.Background(), apply.InstallerSource(), key, binding, nil); err == nil || !strings.Contains(err.Error(), "receiver is nil") {
		t.Fatalf("ensure error=%v", err)
	}
	record, _ := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, Observation: registry.ObservationInstalled, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted})
	if _, err := activator.Remove(context.Background(), apply.InstallerSource(), key, binding, record); err == nil || !strings.Contains(err.Error(), "receiver is nil") {
		t.Fatalf("remove error=%v", err)
	}
}

func TestDirectFilePolicyValidatesBeforeFilesystemAndPassThroughPreservesGenericResult(t *testing.T) {
	t.Parallel()
	c, _ := cell.New(artifact.HarnessOpenCode, cell.HooksAxis())
	root := filepath.Join(t.TempDir(), "must-not-be-opened")
	key, binding := directBinding(t, c, root)
	stop := errors.New("policy stopped request")
	policy, _ := apply.NewDirectFilePolicy(c, func(request apply.DirectFileRequest) error {
		if request.Cell() != c || request.Operation() != apply.Inspect() {
			t.Fatalf("request=%s/%s", request.Cell(), request.Operation())
		}
		return stop
	}, apply.PassThroughDecoration())
	activator, _ := apply.NewDirectFileActivator(policy)
	if _, err := activator.Inspect(context.Background(), apply.InstallerSource(), key, binding, nil); !errors.Is(err, stop) {
		t.Fatalf("validation error=%v", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("validation opened filesystem root: %v", err)
	}

	pass, _ := apply.PassThroughDirectFile(c)
	activator, _ = apply.NewDirectFileActivator(pass)
	out, err := activator.Inspect(context.Background(), apply.HomeManagerSource(), key, binding, nil)
	if err != nil || out.Status != apply.Completed() || out.Observation != registry.ObservationAbsent || out.Record == nil || out.Record.Source() != registry.SourceHomeManager || out.Diagnostic != "" {
		t.Fatalf("pass-through outcome=%+v err=%v", out, err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("read-only declarative inspection created root: %v", err)
	}
}

func TestDirectFilePolicyPendingTrustIsClosedAndConsistent(t *testing.T) {
	t.Parallel()
	c, _ := cell.New(artifact.HarnessCodex, cell.HooksAxis())
	root := filepath.Join(t.TempDir(), "target")
	key, binding := directBinding(t, c, root)
	pending, err := apply.PendingNativeTrustDirectFile(c, func(request apply.DirectFileRequest) error {
		if request.StrategyKind() != activation.DirectFileKindValue() || request.ArtifactID().String() == "" || request.DestinationRoot() != root {
			t.Fatalf("narrow request identities were incomplete: %+v", request)
		}
		return nil
	}, "review hooks in Codex native trust settings")
	if err != nil {
		t.Fatal(err)
	}
	activator, _ := apply.NewDirectFileActivator(pending)
	out, err := activator.Ensure(context.Background(), apply.InstallerSource(), key, binding, nil)
	if err != nil || out.Status != apply.InstalledPendingTrust() || out.Observation != registry.ObservationInstalled || out.Record == nil || out.Record.Trust() != registry.TrustPending || out.Diagnostic == "" {
		t.Fatalf("decorated outcome=%+v err=%v", out, err)
	}
	out, err = activator.Remove(context.Background(), apply.InstallerSource(), key, binding, *out.Record)
	if err != nil || out.Status != apply.Completed() || out.Observation != registry.ObservationAbsent || out.Record == nil || out.Record.Trust() != registry.TrustNotApplicable {
		t.Fatalf("removed outcome=%+v err=%v", out, err)
	}
	agents, _ := cell.New(artifact.HarnessCodex, cell.AgentsAxis())
	if _, err := apply.PendingNativeTrustDirectFile(agents, func(apply.DirectFileRequest) error { return nil }, "review"); err == nil {
		t.Fatal("pending trust accepted for non-hook cell")
	}
	if _, err := apply.NewDirectFilePolicy(c, func(apply.DirectFileRequest) error { return nil }, apply.DirectFileDecorationMode{}); err == nil {
		t.Fatal("zero decoration mode accepted")
	}
}
