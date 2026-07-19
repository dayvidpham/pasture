package inventory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/inventory"
)

func nativeRecord(t *testing.T, harness ir.HarnessID, axis cell.Extension, obs inventory.Observation) inventory.Record {
	t.Helper()
	c, _ := cell.New(harness, axis)
	r, err := inventory.NewRecord(inventory.RecordInput{
		Cell:        c,
		Source:      inventory.InstallerSource(),
		Strategy:    activation.NativePluginKindValue(),
		Managed:     true,
		ArtifactID:  "artifact.bundle.v1:sha256:" + strings.Repeat("a", 64),
		Version:     "claude-code/claude-code@2.1.210",
		Selector:    "pasture-skills@user",
		Observation: obs,
		Trust:       inventory.TrustNotApplicable(),
		LastAction:  "ensure",
		LastOutcome: "completed",
	})
	if err != nil {
		t.Fatalf("NewRecord: %v", err)
	}
	return r
}

func TestUpsertLookupAndCanonicalOrder(t *testing.T) {
	inv := inventory.New()
	// insert out of canonical order
	_ = inv.Upsert(nativeRecord(t, ir.HarnessCodex, cell.HooksAxis(), inventory.Installed()))
	_ = inv.Upsert(nativeRecord(t, ir.HarnessClaudeCode, cell.SkillsAxis(), inventory.Installed()))
	ordered := inv.Ordered()
	if len(ordered) != 2 {
		t.Fatalf("len = %d", len(ordered))
	}
	if ordered[0].Cell().String() != "claude-code.skills" || ordered[1].Cell().String() != "codex.hooks" {
		t.Errorf("canonical order broken: %s then %s", ordered[0].Cell(), ordered[1].Cell())
	}
}

func TestMarshalParseRoundTripWithLeafAndTombstone(t *testing.T) {
	inv := inventory.New()
	// a direct-file managed leaf record
	p, _ := artifact.NewPath("plugin/pasture-hooks.ts")
	mode, _ := artifact.NewMode(0o644)
	leaf, _ := inventory.NewLeaf(p, artifact.RegularFileType(), mode, artifact.DigestBytes([]byte("x")))
	oc, _ := cell.New(ir.HarnessOpenCode, cell.HooksAxis())
	df, err := inventory.NewRecord(inventory.RecordInput{
		Cell:        oc,
		Source:      inventory.HomeManagerSource(),
		Strategy:    activation.DirectFileKindValue(),
		Managed:     true,
		Leaves:      []inventory.Leaf{leaf},
		Observation: inventory.Installed(),
		Trust:       inventory.TrustNotApplicable(),
		LastAction:  "ensure",
		LastOutcome: "completed",
	})
	if err != nil {
		t.Fatalf("directfile record: %v", err)
	}
	_ = inv.Upsert(df)
	// an absent tombstone
	_ = inv.Upsert(nativeRecord(t, ir.HarnessCodex, cell.HooksAxis(), inventory.Absent()))

	data, err := inv.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := inventory.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	roundLeaf, ok := got.Lookup(oc)
	if !ok || len(roundLeaf.Leaves()) != 1 {
		t.Fatalf("leaf record lost in round trip")
	}
	if roundLeaf.Leaves()[0].Digest() != leaf.Digest() {
		t.Errorf("leaf digest changed across round trip")
	}
	codexHooks, _ := cell.New(ir.HarnessCodex, cell.HooksAxis())
	tomb, ok := got.Lookup(codexHooks)
	if !ok || tomb.Observation() != inventory.Absent() {
		t.Errorf("absent tombstone lost")
	}
}

func TestParseRejectsDuplicateCell(t *testing.T) {
	doc := `schema: pasture.install.state/v1
cells:
  - cell: claude-code.skills
    source: installer
    strategy: native-plugin
    managed: true
    observation: installed
    trust: not-applicable
  - cell: claude-code.skills
    source: installer
    strategy: native-plugin
    managed: true
    observation: absent
    trust: not-applicable
`
	if _, err := inventory.Parse([]byte(doc)); err == nil {
		t.Fatal("duplicate cell = nil error, want rejection")
	}
}

func TestParseRejectsUnknownEnum(t *testing.T) {
	doc := `schema: pasture.install.state/v1
cells:
  - cell: claude-code.skills
    source: installer
    strategy: native-plugin
    managed: true
    observation: maybe
    trust: not-applicable
`
	if _, err := inventory.Parse([]byte(doc)); err == nil {
		t.Fatal("unknown observation = nil error, want rejection")
	}
}

func TestSaveLoadAtomicAndModeAndSymlinkSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installations.yaml")
	inv := inventory.New()
	_ = inv.Upsert(nativeRecord(t, ir.HarnessClaudeCode, cell.SkillsAxis(), inventory.Installed()))
	if err := inventory.Save(path, inv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %o, want 600", info.Mode().Perm())
	}
	loaded, err := inventory.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Len() != 1 {
		t.Errorf("loaded len = %d", loaded.Len())
	}

	// A symlinked state file is rejected before read.
	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "installations.yaml")
	if err := os.Symlink(path, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := inventory.Load(linkPath); err == nil {
		t.Fatal("Load of symlinked state file = nil error, want rejection")
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	inv, err := inventory.Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if inv.Len() != 0 {
		t.Errorf("missing load len = %d, want 0", inv.Len())
	}
}
