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
	}, func(_ apply.DirectFileRequest, out apply.Outcome) (apply.Outcome, error) { return out, nil })
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

func TestDirectFilePolicyAllowsPresentationDecorationButRejectsOwnershipRewrite(t *testing.T) {
	t.Parallel()
	c, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
	root := filepath.Join(t.TempDir(), "target")
	key, binding := directBinding(t, c, root)
	presentation, _ := apply.NewDirectFilePolicy(c, func(apply.DirectFileRequest) error { return nil }, func(_ apply.DirectFileRequest, out apply.Outcome) (apply.Outcome, error) {
		out.Status = apply.ManagedDeclaratively()
		out.Diagnostic = "declared by policy"
		return out, nil
	})
	activator, _ := apply.NewDirectFileActivator(presentation)
	out, err := activator.Inspect(context.Background(), apply.HomeManagerSource(), key, binding, nil)
	if err != nil || out.Status != apply.ManagedDeclaratively() || out.Diagnostic != "declared by policy" {
		t.Fatalf("decorated outcome=%+v err=%v", out, err)
	}

	malicious, _ := apply.NewDirectFilePolicy(c, func(apply.DirectFileRequest) error { return nil }, func(_ apply.DirectFileRequest, out apply.Outcome) (apply.Outcome, error) {
		record := out.Record
		rewritten, err := registry.NewRecord(registry.RecordInput{Key: record.Key(), Source: registry.SourceInstaller, Strategy: record.Strategy(), Managed: record.Managed(), ArtifactID: record.ArtifactID(), Version: record.Version(), Selector: record.Selector(), Leaves: record.Leaves(), CreatedDirs: record.CreatedDirs(), SharedConfig: record.SharedConfig(), Observation: record.Observation(), Trust: record.Trust(), LastOperation: record.LastOperation(), LastOutcome: record.LastOutcome(), Diagnostic: record.Diagnostic()})
		if err != nil {
			return out, err
		}
		out.Record = &rewritten
		return out, nil
	})
	activator, _ = apply.NewDirectFileActivator(malicious)
	_, err = activator.Inspect(context.Background(), apply.HomeManagerSource(), key, binding, nil)
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("ownership rewrite error=%v", err)
	}
}
