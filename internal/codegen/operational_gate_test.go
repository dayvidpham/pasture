package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type trackerMutation struct {
	Name      string `yaml:"name"`
	Owner     string `yaml:"owner"`
	File      string `yaml:"file"`
	Text      string `yaml:"text"`
	Generator string `yaml:"generator"`
}

func TestOperationalMutationsWithholdAllGenerationWrites(t *testing.T) {
	var fixtures []trackerMutation
	testutil.LoadFixtures(t, testutil.OperationalTracker, &fixtures)
	realRoot, err := scan.ModuleRoot()
	require.NoError(t, err)
	for _, fixture := range fixtures {
		if fixture.Generator != "" {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			root := t.TempDir()
			for _, source := range scan.OperationalSourceRoots() {
				path := filepath.Join(realRoot, source)
				info, err := os.Stat(path)
				require.NoError(t, err)
				if info.IsDir() {
					require.NoError(t, os.CopyFS(filepath.Join(root, source), os.DirFS(path)))
				} else {
					content, err := os.ReadFile(path)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(root, source), content, 0o644))
				}
			}
			undo := mutateTrackerSource(t, root, fixture)
			defer undo()
			before, err := trackerTreeBytes(root)
			require.NoError(t, err)
			targets, err := ResolveHarness([]string{"claude-code", "opencode", "codex"})
			require.NoError(t, err)
			if fixture.Owner == "candidate" {
				targets[len(targets)-1].Manifest = trackerMutationEmitter{fixture.Text}
			}
			result, errs := Generate(root, targets, DefaultOptions)
			require.NotEmpty(t, errs)
			require.Contains(t, errs[0].Error(), "operational tracker")
			require.Empty(t, result)
			after, err := trackerTreeBytes(root)
			require.NoError(t, err)
			require.Equal(t, before, after, "schema/harness files, new directories and inventory must remain untouched")
			if fixture.Owner == "raw" {
				_, err := scan.CheckOperationalTree(root, scan.OperationalOutputRoots(), "independent mutation sweep")
				require.Error(t, err, "independent sweep must share canonical document discovery")
				require.Contains(t, err.Error(), fixture.File)
			}
		})
	}
}

func mutateTrackerSource(t *testing.T, root string, fixture trackerMutation) func() {
	t.Helper()
	switch fixture.Owner {
	case "body":
		old := SkillBodySpecs["worker"]
		value := old
		value.Preamble += fixture.Text
		SkillBodySpecs["worker"] = value
		return func() { SkillBodySpecs["worker"] = old }
	case "fragment":
		old := SharedFragmentSpecs[FragTaskRecovery]
		value := old
		prose := *old.Prose
		prose.Content += fixture.Text
		value.Prose = &prose
		SharedFragmentSpecs[FragTaskRecovery] = value
		return func() { SharedFragmentSpecs[FragTaskRecovery] = old }
	case "candidate":
		return func() {}
	case "role":
		old := RoleSpecs[protocol.RoleWorker]
		value := old
		value.Introduction += fixture.Text
		RoleSpecs[protocol.RoleWorker] = value
		return func() { RoleSpecs[protocol.RoleWorker] = old }
	case "constraint":
		old := ConstraintSpecs["C-actionable-errors"]
		value := old
		value.Then += fixture.Text
		ConstraintSpecs["C-actionable-errors"] = value
		return func() { ConstraintSpecs["C-actionable-errors"] = old }
	case "procedure":
		old := ProcedureSteps[protocol.RoleWorker]
		value := append([]ProcedureStep(nil), old...)
		value[0].Instruction += fixture.Text
		ProcedureSteps[protocol.RoleWorker] = value
		return func() { ProcedureSteps[protocol.RoleWorker] = old }
	default:
		file := filepath.Join(root, filepath.FromSlash(fixture.File))
		old, err := os.ReadFile(file)
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
		require.NoError(t, os.WriteFile(file, append(old, []byte(fixture.Text)...), 0o644))
		return func() {}
	}
}

func TestSeparateAssetEntryPointsRejectWithoutStageWrites(t *testing.T) {
	t.Parallel()
	var fixtures []trackerMutation
	testutil.LoadFixtures(t, testutil.OperationalTracker, &fixtures)
	realRoot, err := scan.ModuleRoot()
	require.NoError(t, err)
	for _, fixture := range fixtures {
		if fixture.Generator == "" {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, dir := range []string{"internal", "tools", "pkg", "artifact", "skills", "agents", "hooks"} {
				require.NoError(t, os.CopyFS(filepath.Join(root, dir), os.DirFS(filepath.Join(realRoot, dir))))
			}
			for _, file := range []string{"go.mod", "go.sum"} {
				content, err := os.ReadFile(filepath.Join(realRoot, file))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(root, file), content, 0o644))
			}
			_ = mutateTrackerSource(t, root, fixture)
			before, err := trackerTreeBytes(root)
			require.NoError(t, err)
			command := exec.Command("go", "generate", fixture.Generator)
			command.Dir = root
			// This copied tree is an independent fixture module. The parent's
			// integration workspace names the real source tree, not this clone.
			// Keep the real generator and rejection assertions, but let Go select
			// the fixture's own go.mod instead of rejecting workspace membership.
			command.Env = append(os.Environ(), "GOWORK=off")
			output, err := command.CombinedOutput()
			require.Error(t, err, string(output))
			require.Contains(t, string(output), "operational tracker gate rejected")
			require.Contains(t, string(output), "bd ready")
			after, err := trackerTreeBytes(root)
			require.NoError(t, err)
			require.Equal(t, before, after, "the actual separate asset generator must reject before writes or pruning")
		})
	}
}

type trackerMutationEmitter struct{ text string }

func (emitter trackerMutationEmitter) Emit(root string, _ GenerateOptions) ([]GeneratedFile, error) {
	return []GeneratedFile{{Path: filepath.Join(root, ".codex/hooks/new.sh"), Content: emitter.text}}, nil
}

func trackerTreeBytes(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[file] = "directory"
			return nil
		}
		data, err := os.ReadFile(file)
		out[file] = string(data)
		return err
	})
	return out, err
}

func TestOperationalTrackerShippedTree(t *testing.T) {
	t.Parallel()
	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	require.NoError(t, RequireOperationalSource(root))
	report, err := scan.CheckOperationalTree(root, scan.OperationalOutputRoots(), "CI operational output sweep")
	require.NoError(t, err)
	require.Greater(t, report.Files, 0)
	t.Logf("operational output sweep: files=%d historical=%d executable=0 tree_sha256=%s", report.Files, report.Historical, report.TreeHash)
}

func TestGeneratedResultAccountsForInventoryStage(t *testing.T) {
	t.Parallel()
	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	result, errs := Generate(root, []TargetHarness{ClaudeCodeTarget}, GenerateOptions{})
	require.Empty(t, errs)
	require.Len(t, result.InventoryFiles, 2)
	for _, file := range result.InventoryFiles {
		require.Contains(t, file.Path, "internal/inventory/")
		require.Contains(t, file.Content, "Code generated by codegen.")
	}
}
