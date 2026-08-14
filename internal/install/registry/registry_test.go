package registry_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

const validGlobalDocument = `schema: pasture.install.registry/v1
global_installations:
  - cell: claude-code.skills
    source: installer
    strategy: native-plugin
    managed: false
    observation: installed
    trust: not-applicable
    last_operation: none
    last_outcome: none
project_installations: []
`

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

func TestPersistedCompleteRecordsRoundTripExactly(t *testing.T) {
	c, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	globalKey, _ := registry.GlobalKey(c)
	root, _ := registry.CanonicalProjectRoot(t.TempDir())
	projectKey, _ := registry.ProjectKey(root, c)
	bundleID, _ := artifact.ParseBundleID("artifact.bundle.v1:sha256:" + strings.Repeat("b", 64))
	leafPath, _ := artifact.NewPath("skills/pasture/SKILL.md")
	leafMode, _ := artifact.NewMode(0o644)
	leaf, _ := registry.NewLeaf(leafPath, artifact.RegularFileType(), leafMode, artifact.DigestBytes([]byte("skill")))
	dir, _ := artifact.NewPath("skills/pasture")
	configPath, _ := artifact.NewPath(".claude/settings.json")
	identity, _ := registry.NewSharedConfigIdentity("pasture-hooks")
	config, _ := registry.NewSharedConfigOwnership(configPath, identity, artifact.DigestBytes([]byte("entry")))
	version, _ := registry.NewVersion("claude-code@2.1.210")
	selector, _ := registry.NewSelector("pasture-skills@user")
	globalRecord, err := registry.NewRecord(registry.RecordInput{Key: globalKey, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, ArtifactID: bundleID, Version: version, Selector: selector, Leaves: []registry.Leaf{leaf}, CreatedDirs: []artifact.Path{dir}, Observation: registry.ObservationInstalled, Trust: registry.TrustPending, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted, Diagnostic: "global confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	projectVersion, _ := registry.NewVersion("claude-code@2.1.211")
	projectSelector, _ := registry.NewSelector("pasture-project@local")
	projectRecord, err := registry.NewRecord(registry.RecordInput{Key: projectKey, Source: registry.SourceHomeManager, Strategy: activation.NativePluginKindValue(), Managed: false, Version: projectVersion, Selector: projectSelector, SharedConfig: []registry.SharedConfigOwnership{config}, Observation: registry.ObservationAbsent, Trust: registry.TrustTrusted, LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeFailed, Diagnostic: "project removed"})
	if err != nil {
		t.Fatal(err)
	}
	store := registry.New()
	if err := store.Upsert(projectRecord); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(globalRecord); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "installations.yaml")
	if err := registry.Save(path, store); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	loaded, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loaded.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical bytes changed\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	status := loaded.Status()
	if len(status) != 2 || status[0].Scope != registry.ScopeGlobal || status[1].Scope != registry.ScopeProject || status[0].Cell != status[1].Cell {
		t.Fatalf("scoped status=%v", status)
	}
	g := status[0].Record
	if g.Key() != globalKey || g.Source() != registry.SourceInstaller || g.Strategy() != activation.DirectFileKindValue() || !g.Managed() || g.ArtifactID() != bundleID || g.Version() != version || g.Selector() != selector || g.Observation() != registry.ObservationInstalled || g.Trust() != registry.TrustPending || g.LastOperation() != registry.OperationEnsure || g.LastOutcome() != registry.OutcomeCompleted || g.Diagnostic() != "global confirmed" || len(g.Leaves()) != 1 || g.Leaves()[0].Path() != leafPath || g.Leaves()[0].Type() != artifact.RegularFileType() || g.Leaves()[0].Mode() != leafMode || g.Leaves()[0].Digest() != leaf.Digest() || len(g.CreatedDirs()) != 1 || g.CreatedDirs()[0] != dir {
		t.Fatalf("global record mismatch: %+v", g)
	}
	p := status[1].Record
	if p.Key() != projectKey || p.Source() != registry.SourceHomeManager || p.Strategy() != activation.NativePluginKindValue() || p.Managed() || p.ArtifactID().String() != "" || p.Version() != projectVersion || p.Selector() != projectSelector || p.Observation() != registry.ObservationAbsent || p.Trust() != registry.TrustTrusted || p.LastOperation() != registry.OperationRemove || p.LastOutcome() != registry.OutcomeFailed || p.Diagnostic() != "project removed" || len(p.Leaves()) != 0 || len(p.CreatedDirs()) != 0 {
		t.Fatalf("project record mismatch: %+v", p)
	}
	if len(g.SharedConfig()) != 0 || len(p.SharedConfig()) != 1 || p.SharedConfig()[0].Path() != configPath || p.SharedConfig()[0].Identity() != identity || p.SharedConfig()[0].Digest() != config.Digest() {
		t.Fatalf("shared config scope leakage")
	}
	projects := loaded.Projects()
	if len(projects) != 1 || projects[0].Scope != registry.ScopeProject || projects[0].ProjectRoot != root {
		t.Fatalf("Projects=%v", projects)
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

func TestStrictCodecRequiresTablesAndRecordFields(t *testing.T) {
	cases := map[string]string{
		"schema only":     "schema: pasture.install.registry/v1\n",
		"missing global":  "schema: pasture.install.registry/v1\nproject_installations: []\n",
		"missing project": "schema: pasture.install.registry/v1\nglobal_installations: []\n",
		"null global":     "schema: pasture.install.registry/v1\nglobal_installations: null\nproject_installations: []\n",
		"null project":    "schema: pasture.install.registry/v1\nglobal_installations: []\nproject_installations: null\n",
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := registry.Parse([]byte(document)); err == nil || !strings.Contains(err.Error(), "logical tables") {
				t.Fatalf("Parse error=%v", err)
			}
		})
	}
	if store, err := registry.Parse([]byte("schema: pasture.install.registry/v1\nglobal_installations: []\nproject_installations: []\n")); err != nil || store.Len() != 0 {
		t.Fatalf("explicit empty arrays: store=%v err=%v", store, err)
	}
	if store, err := registry.Parse([]byte(validGlobalDocument)); err != nil || store.Status()[0].Record.Managed() {
		t.Fatalf("managed:false not preserved: store=%v err=%v", store, err)
	}

	required := []string{"cell", "source", "strategy", "managed", "observation", "trust", "last_operation", "last_outcome"}
	for _, field := range required {
		field := field
		t.Run("missing "+field, func(t *testing.T) {
			values := []struct{ name, value string }{{"cell", "claude-code.skills"}, {"source", "installer"}, {"strategy", "native-plugin"}, {"managed", "false"}, {"observation", "installed"}, {"trust", "not-applicable"}, {"last_operation", "none"}, {"last_outcome", "none"}}
			doc := "schema: pasture.install.registry/v1\nglobal_installations:\n"
			first := true
			for _, item := range values {
				if item.name == field {
					continue
				}
				marker := "    "
				if first {
					marker = "  - "
					first = false
				}
				doc += marker + item.name + ": " + item.value + "\n"
			}
			doc += "project_installations: []\n"
			_, err := registry.Parse([]byte(doc))
			if err == nil || !strings.Contains(err.Error(), "required record fields") {
				t.Fatalf("field %s error=%v", field, err)
			}
		})
	}
	for _, field := range required {
		field := field
		t.Run("null "+field, func(t *testing.T) {
			needle := field + ":"
			doc := validGlobalDocument
			start := strings.Index(doc, needle)
			if start < 0 {
				t.Fatal("missing fixture field")
			}
			end := strings.Index(doc[start:], "\n") + start
			doc = doc[:start] + field + ": null" + doc[end:]
			_, err := registry.Parse([]byte(doc))
			if err == nil || !strings.Contains(err.Error(), "required record fields") {
				t.Fatalf("null %s error=%v", field, err)
			}
		})
	}
}

func TestStrictCodecRejectsEachEnumAndDuplicateBoundary(t *testing.T) {
	cases := []struct{ name, old, replacement, want string }{
		{"cell", "claude-code.skills", "gemini.skills", "harness"},
		{"source", "source: installer", "source: unknown", "source"},
		{"strategy", "strategy: native-plugin", "strategy: unknown", "strategy"},
		{"observation", "observation: installed", "observation: maybe", "observation"},
		{"trust", "trust: not-applicable", "trust: maybe", "trust"},
		{"operation", "last_operation: none", "last_operation: maybe", "operation"},
		{"outcome", "last_outcome: none", "last_outcome: maybe", "outcome"},
		{"nested unknown", "    managed: false", "    managed: false\n    surprise: true", "field surprise"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			doc := strings.Replace(validGlobalDocument, tc.old, tc.replacement, 1)
			_, err := registry.Parse([]byte(doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
	duplicate := strings.Replace(validGlobalDocument, "project_installations: []", strings.TrimPrefix(validGlobalDocument, "schema: pasture.install.registry/v1\nglobal_installations:\n"), 1)
	if _, err := registry.Parse([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "appears more than once") {
		t.Fatalf("duplicate global key error=%v", err)
	}
	mappingDuplicate := strings.Replace(validGlobalDocument, "    managed: false", "    managed: false\n    managed: true", 1)
	if _, err := registry.Parse([]byte(mappingDuplicate)); err == nil || !strings.Contains(err.Error(), "already defined") {
		t.Fatalf("duplicate mapping key error=%v", err)
	}
}

func TestRecordAndCodecRejectContradictoryOperationOutcomePairs(t *testing.T) {
	c, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	key, _ := registry.GlobalKey(c)
	for _, tc := range []struct {
		name      string
		operation registry.Operation
		outcome   registry.Outcome
	}{
		{"none with failed", registry.OperationNone, registry.OutcomeFailed},
		{"ensure with none", registry.OperationEnsure, registry.OutcomeNone},
	} {
		t.Run(tc.name+" constructor", func(t *testing.T) {
			_, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Observation: registry.ObservationUnknown, Trust: registry.TrustNotApplicable, LastOperation: tc.operation, LastOutcome: tc.outcome})
			if err == nil || !strings.Contains(err.Error(), "contradict") {
				t.Fatalf("NewRecord error=%v", err)
			}
		})
		t.Run(tc.name+" codec", func(t *testing.T) {
			doc := strings.Replace(validGlobalDocument, "last_operation: none", "last_operation: "+tc.operation.String(), 1)
			doc = strings.Replace(doc, "last_outcome: none", "last_outcome: "+tc.outcome.String(), 1)
			if _, err := registry.Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), "contradict") {
				t.Fatalf("Parse error=%v", err)
			}
		})
	}
	for _, operation := range []registry.Operation{registry.OperationEnsure, registry.OperationRemove, registry.OperationInspect} {
		for _, outcome := range []registry.Outcome{registry.OutcomeCompleted, registry.OutcomeFailed} {
			if _, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Observation: registry.ObservationUnknown, Trust: registry.TrustNotApplicable, LastOperation: operation, LastOutcome: outcome}); err != nil {
				t.Fatalf("valid pair %s/%s rejected: %v", operation, outcome, err)
			}
		}
	}
}

func TestStrictCodecRejectsIncompleteNestedOwnershipFields(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	leafDocument := strings.Replace(validGlobalDocument, "    observation:", "    leaves:\n      -\n        path: skills/pasture/SKILL.md\n        type: regular-file\n        mode: '0644'\n        digest: "+digest+"\n    observation:", 1)
	project := filepath.Join(t.TempDir(), "retained")
	configDocument := "schema: pasture.install.registry/v1\nglobal_installations: []\nproject_installations:\n  - canonical_project_root: " + project + "\n    cell: claude-code.hooks\n    source: installer\n    strategy: direct-file\n    managed: true\n    observation: installed\n    trust: pending\n    last_operation: ensure\n    last_outcome: completed\n    shared_config_ownership:\n      -\n        path: .claude/settings.json\n        identity: pasture-hooks\n        digest: " + digest + "\n"
	type nestedCase struct{ field, malformed string }
	sets := []struct {
		name, document, marker string
		fields                 []nestedCase
	}{
		{"leaf", leafDocument, "        ", []nestedCase{{"path", "../bad"}, {"type", "unknown"}, {"mode", "invalid"}, {"digest", "sha256:short"}}},
		{"shared config", configDocument, "        ", []nestedCase{{"path", "../bad"}, {"identity", "' padded '"}, {"digest", "sha256:short"}}},
	}
	for _, set := range sets {
		set := set
		for _, field := range set.fields {
			field := field
			linePrefix := set.marker + field.field + ":"
			start := strings.Index(set.document, linePrefix)
			if start < 0 {
				t.Fatalf("fixture lacks %s %s", set.name, field.field)
			}
			end := start + strings.Index(set.document[start:], "\n") + 1
			for _, mutation := range []struct{ name, replacement string }{
				{"omitted", ""},
				{"null", linePrefix + " null\n"},
				{"malformed", linePrefix + " " + field.malformed + "\n"},
			} {
				mutation := mutation
				t.Run(set.name+" "+field.field+" "+mutation.name, func(t *testing.T) {
					doc := set.document[:start] + mutation.replacement + set.document[end:]
					_, err := registry.Parse([]byte(doc))
					if err == nil || !strings.Contains(err.Error(), field.field) {
						t.Fatalf("error=%v, want nested field %q", err, field.field)
					}
				})
			}
		}
		t.Run(set.name+" unknown field", func(t *testing.T) {
			doc := strings.Replace(set.document, set.marker+set.fields[0].field+":", set.marker+"surprise: true\n"+set.marker+set.fields[0].field+":", 1)
			if _, err := registry.Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), "surprise") {
				t.Fatalf("unknown nested field error=%v", err)
			}
		})
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

func TestCanonicalProjectRootAcceptanceAndRejectionMatrix(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "broken")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), broken); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		ok         bool
	}{
		{"directory", dir, true}, {"missing", filepath.Join(t.TempDir(), "missing"), false}, {"regular file", file, false}, {"broken symlink", broken, false}, {"relative", "relative/project", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := registry.CanonicalProjectRoot(tc.path)
			if tc.ok != (err == nil) {
				t.Fatalf("CanonicalProjectRoot(%q) error=%v", tc.path, err)
			}
		})
	}

	absent := filepath.Join(t.TempDir(), "retained")
	doc := strings.Replace(validGlobalDocument, "project_installations: []", "project_installations:\n  - canonical_project_root: "+absent+"\n    cell: claude-code.skills\n    source: installer\n    strategy: direct-file\n    managed: false\n    observation: absent\n    trust: not-applicable\n    last_operation: remove\n    last_outcome: completed", 1)
	if _, err := registry.Parse([]byte(doc)); err != nil {
		t.Fatalf("retained absent root rejected: %v", err)
	}
	fileDoc := strings.Replace(doc, absent, file, 1)
	if _, err := registry.Parse([]byte(fileDoc)); err == nil || !strings.Contains(err.Error(), "non-directory") {
		t.Fatalf("existing file root error=%v", err)
	}
	unclean := t.TempDir() + string(filepath.Separator) + "root" + string(filepath.Separator) + ".." + string(filepath.Separator) + "retained"
	uncleanDoc := strings.Replace(doc, absent, unclean, 1)
	if _, err := registry.Parse([]byte(uncleanDoc)); err == nil || !strings.Contains(err.Error(), "clean and absolute") {
		t.Fatalf("unclean absolute root error=%v", err)
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

func TestTypedOwnershipCodecRejectsInvalidValues(t *testing.T) {
	stringType := reflect.TypeOf("")
	for name, target := range map[string]reflect.Type{
		"version":                reflect.TypeOf(registry.Version{}),
		"selector":               reflect.TypeOf(registry.Selector{}),
		"shared config identity": reflect.TypeOf(registry.SharedConfigIdentity{}),
	} {
		if stringType.ConvertibleTo(target) {
			t.Fatalf("external strings remain directly convertible to opaque %s", name)
		}
	}
	cases := []struct{ name, addition, want string }{
		{"bundle", "    artifact_id: not-a-bundle\n", "bundle ID"},
		{"version", "    version: ' padded '\n", "version"},
		{"selector", "    selector: \"bad\\u0000selector\"\n", "selector"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			doc := strings.Replace(validGlobalDocument, "    observation:", tc.addition+"    observation:", 1)
			_, err := registry.Parse([]byte(doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
	if _, err := registry.NewVersion(" padded "); err == nil {
		t.Fatal("padded Version accepted")
	}
	if _, err := registry.NewSelector("bad\nselector"); err == nil {
		t.Fatal("control-bearing Selector accepted")
	}
	if _, err := registry.NewSharedConfigIdentity(""); err == nil {
		t.Fatal("empty SharedConfigIdentity accepted")
	}
}

func TestCodecRejectsDuplicateOwnershipCollections(t *testing.T) {
	leaf := "    leaves:\n      - &leaf {path: skill.md, type: regular-file, mode: '0644', digest: sha256:" + strings.Repeat("a", 64) + "}\n      - *leaf\n"
	doc := strings.Replace(validGlobalDocument, "    observation:", leaf+"    observation:", 1)
	if _, err := registry.Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate leaf error=%v", err)
	}
	root := filepath.Join(t.TempDir(), "absent")
	config := "schema: pasture.install.registry/v1\nglobal_installations: []\nproject_installations:\n  - canonical_project_root: " + root + "\n    cell: claude-code.hooks\n    source: installer\n    strategy: direct-file\n    managed: true\n    observation: installed\n    trust: not-applicable\n    last_operation: ensure\n    last_outcome: completed\n    shared_config_ownership:\n      - &owned {path: .claude/settings.json, identity: pasture-hooks, digest: sha256:" + strings.Repeat("b", 64) + "}\n      - *owned\n"
	if _, err := registry.Parse([]byte(config)); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate shared config error=%v", err)
	}
	dirs := strings.Replace(validGlobalDocument, "    observation:", "    created_dirs: [skills, skills]\n    observation:", 1)
	if _, err := registry.Parse([]byte(dirs)); err == nil || !strings.Contains(err.Error(), "directory skills is duplicated") {
		t.Fatalf("duplicate created_dirs error=%v", err)
	}
}
