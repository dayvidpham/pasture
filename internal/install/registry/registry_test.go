package registry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestStrictV1CodecCanonicalizesLogicalTables(t *testing.T) {
	s, err := registry.Parse(fixture(t, "valid_out_of_order.yaml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Len() != 4 {
		t.Fatalf("Len=%d, want 4", s.Len())
	}
	status := s.Status()
	want := []string{"global||claude-code.skills", "global||codex.hooks", "project|/srv/alpha|claude-code.skills", "project|/srv/zeta|claude-code.skills"}
	for i, row := range status {
		got := row.Scope.String() + "|" + row.ProjectRoot.String() + "|" + row.Cell.String()
		if got != want[i] {
			t.Errorf("status[%d]=%q, want %q", i, got, want[i])
		}
	}
	projects := s.Projects()
	if len(projects) != 2 || projects[0].Scope != registry.ScopeProject {
		t.Fatalf("Projects=%v, want two project rows", projects)
	}
	encoded, err := s.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(encoded)
	if strings.Index(text, "claude-code.skills") > strings.Index(text, "codex.hooks") {
		t.Fatalf("global table not canonical:\n%s", text)
	}
	if strings.Index(text, "/srv/alpha") > strings.Index(text, "/srv/zeta") {
		t.Fatalf("project table not canonical:\n%s", text)
	}
	round, err := registry.Parse(encoded)
	if err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}
	if round.Len() != s.Len() {
		t.Fatalf("round-trip Len=%d, want %d", round.Len(), s.Len())
	}
}

func TestSameCellGlobalAndProjectKeysRemainDistinct(t *testing.T) {
	c, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	global, _ := registry.GlobalKey(c)
	dir := t.TempDir()
	root, err := registry.CanonicalProjectRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := registry.ProjectKey(root, c)
	makeRecord := func(k registry.Key) registry.Record {
		r, e := registry.NewRecord(registry.RecordInput{Key: k, Source: registry.SourceInstaller, Strategy: activation.NativePluginKindValue(), Managed: true, Observation: registry.ObservationInstalled, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	s := registry.New()
	_ = s.Upsert(makeRecord(global))
	_ = s.Upsert(makeRecord(project))
	if s.Len() != 2 {
		t.Fatalf("same cell across scopes collapsed: Len=%d", s.Len())
	}
	if _, ok := s.Lookup(global); !ok {
		t.Fatal("global key missing")
	}
	if _, ok := s.Lookup(project); !ok {
		t.Fatal("project key missing")
	}
}

func TestCanonicalProjectRootCollapsesSymlinkAliases(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	a, err := registry.CanonicalProjectRoot(real)
	if err != nil {
		t.Fatal(err)
	}
	b, err := registry.CanonicalProjectRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("canonical roots differ: %q != %q", a, b)
	}
}

func TestStrictCodecRejectsInvalidInput(t *testing.T) {
	for _, name := range []string{"duplicate_project_key.yaml", "unknown_field.yaml", "invalid_schema.yaml", "missing_outcome.yaml"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := registry.Parse(fixture(t, name)); err == nil {
				t.Fatalf("Parse(%s)=nil error", name)
			}
		})
	}
	if _, err := registry.Parse(append(fixture(t, "valid_out_of_order.yaml"), []byte("---\nschema: other\n")...)); err == nil {
		t.Fatal("trailing document accepted")
	}
}

func TestParseRejectsExistingSymlinkProjectRoot(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	doc := "schema: pasture.install.registry/v1\nglobal_installations: []\nproject_installations:\n" +
		"  - canonical_project_root: " + alias + "\n" +
		"    cell: claude-code.skills\n    source: installer\n    strategy: direct-file\n    managed: true\n" +
		"    observation: installed\n    trust: not-applicable\n    last_operation: ensure\n    last_outcome: completed\n"
	if _, err := registry.Parse([]byte(doc)); err == nil {
		t.Fatal("existing symlink alias accepted as canonical project root")
	}
}

func TestPersistenceIsAtomicMode0600AndSymlinkSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installations.yaml")
	s, err := registry.Parse(fixture(t, "valid_out_of_order.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("mode/type=%v, want regular 0600", info.Mode())
	}
	loaded, err := registry.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Len() != 4 {
		t.Fatalf("loaded Len=%d", loaded.Len())
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "installations.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(link, s); err == nil {
		t.Fatal("Save through symlink succeeded")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "sentinel" {
		t.Fatalf("symlink target changed to %q", got)
	}
	realParent := t.TempDir()
	linkedParent := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(filepath.Join(linkedParent, "installations.yaml"), s); err == nil {
		t.Fatal("Save through symlinked parent succeeded")
	}
}

func TestGlobalRecordRejectsProjectOnlySharedConfigOwnership(t *testing.T) {
	c, _ := cell.New(ir.HarnessOpenCode, cell.HooksAxis())
	k, _ := registry.GlobalKey(c)
	if _, err := registry.NewRecord(registry.RecordInput{Key: k, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Observation: registry.ObservationInstalled, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted, SharedConfig: []registry.SharedConfigOwnership{{}}}); err == nil {
		t.Fatal("global shared config ownership accepted")
	}
}
