package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// repoRootFromTest returns the module root (testdata are committed under
// cmd/pasture/testdata, tests run with cmd/pasture as the working directory).
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	return root
}

// renderHelp renders `cmd --help` through the production cobra tree, the same
// path a user's `pasture <cmd> --help` takes. Rendered output is
// deterministic: no TTY is attached, so cobra wraps at its fixed default
// width.
func renderHelp(t *testing.T, cmdPath ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := rootCmd
	cmd.SetArgs(append(append([]string{}, cmdPath...), "--help"))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pasture %s --help: %v", strings.Join(cmdPath, " "), err)
	}
	return out.String()
}

// newRawCommandRenderCommand builds the raw-shaped command exactly the way
// SLICE-2's hook_lifecycle_raw.go must wire it (Use = hookLifecycleRawUse,
// Long = hookLifecycleRawLong, cobra.NoArgs), so the banner renders through
// the production constants and cobra's real help renderer — never a
// test-only string copy.
func newRawCommandRenderCommand() *cobra.Command {
	return &cobra.Command{
		Use:  hookLifecycleRawUse,
		Long: hookLifecycleRawLong,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return nil },
	}
}

// TestRawBannerExactWording pins the required URD R4.3c marking (UAT-Q4
// default: "raw ingestion — for imports and migration; not the default
// path."). The banner is the visible authority §10 mark: raw is for imports
// and migration, never the default path.
func TestRawBannerExactWording(t *testing.T) {
	t.Parallel()

	const want = "raw ingestion — for imports and migration; not the default path."
	if hookLifecycleRawBanner != want {
		t.Errorf("hookLifecycleRawBanner = %q, want %q", hookLifecycleRawBanner, want)
	}
	if !strings.HasPrefix(hookLifecycleRawLong, want) {
		t.Errorf("hookLifecycleRawLong must open with the exact banner, got %q", hookLifecycleRawLong)
	}
	if hookLifecycleRawUse != "raw" {
		t.Errorf("hookLifecycleRawUse = %q, want %q", hookLifecycleRawUse, "raw")
	}
	if strings.Contains(hookLifecycleRawLong, "recommended") {
		t.Errorf("banner must not literally self-label as the recommended path: %q", hookLifecycleRawLong)
	}
}

// TestRawHelpRendersBanner renders a raw-shaped subcommand (the same
// production constants SLICE-2's hook_lifecycle_raw.go command must wire:
// Use=hookLifecycleRawUse, Long=hookLifecycleRawLong) and verifies the
// non-recommended marking is visible in the rendered --help output, and that
// the raw path is not presented as the default.
func TestRawHelpRendersBanner(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cmd := newRawCommandRenderCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("render raw help: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, hookLifecycleRawBanner) {
		t.Errorf("rendered raw help does not contain the required banner %q;\n%s", hookLifecycleRawBanner, rendered)
	}
	if !strings.Contains(rendered, "not the default path") {
		t.Errorf("rendered raw help must state raw is not the default path;\n%s", rendered)
	}
	if strings.Contains(strings.ToLower(rendered), "future events") {
		t.Errorf("rendered raw help presents raw promotion/recommendation language; fix banner;\n%s", rendered)
	}
}

// TestRawHelpRendersBuiltCLI pins the production binary surface rather than
// only the Cobra constants: the command must be registered, expose --dry-run,
// lead with the non-default banner, and state that preview performs no write.
func TestRawHelpRendersBuiltCLI(t *testing.T) {
	t.Parallel()

	binary := lifecycleBinary(t)
	command := exec.Command(binary, "hook", "lifecycle", "raw", "--help")
	rendered, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pasture hook lifecycle raw --help: %v\n%s", err, rendered)
	}
	help := string(rendered)
	for _, want := range []string{hookLifecycleRawBanner, "--dry-run", "no database opened or written"} {
		if !strings.Contains(help, want) {
			t.Errorf("built raw help does not contain %q;\n%s", want, help)
		}
	}
}

// TestNativeHelpGoldenBytesUnchanged is the ZERO-diff guard: the native help
// surface (hook, hook lifecycle, manifest, list, lineage, context) must stay
// byte-identical to the baseline golden captured from the pre-change binary.
// No SLICE-4 edit may appear in any native help byte. When the raw subcommand
// lands (SLICE-2), the `hook lifecycle` parent golden is a deliberate,
// reviewable diff — it is regenerated with UPDATE_GOLDEN=1 by the landing
// wave only.
// WHAT IT VISITS: the golden files listed below, one per native help surface
// this command ships.
// WHAT IT DOES NOT READ: a help surface with no golden. That every surface
// HAS one is a different claim from this one, and it is not made here.
func TestNativeHelpGoldenBytesUnchanged(t *testing.T) {
	goldenDir := filepath.Join("testdata", "help-golden")
	cases := []struct {
		name    string
		cmdPath []string
		golden  string
	}{
		{name: "hook", cmdPath: []string{"hook"}, golden: "hook.txt"},
		{name: "hook-lifecycle", cmdPath: []string{"hook", "lifecycle"}, golden: "hook-lifecycle.txt"},
		{name: "hook-lifecycle-manifest", cmdPath: []string{"hook", "lifecycle", "manifest"}, golden: "hook-lifecycle-manifest.txt"},
		{name: "hook-lifecycle-list", cmdPath: []string{"hook", "lifecycle", "list"}, golden: "hook-lifecycle-list.txt"},
		{name: "hook-lifecycle-lineage", cmdPath: []string{"hook", "lifecycle", "lineage"}, golden: "hook-lifecycle-lineage.txt"},
		{name: "hook-lifecycle-context", cmdPath: []string{"hook", "lifecycle", "context"}, golden: "hook-lifecycle-context.txt"},
		{name: "hook-lifecycle-orphans", cmdPath: []string{"hook", "lifecycle", "orphans"}, golden: "hook-lifecycle-orphans.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := []byte(renderHelp(t, tc.cmdPath...))
			goldenPath := filepath.Join(goldenDir, tc.golden)
			// UPDATE_GOLDEN=1 recaptures the baseline. It is the escape this
			// test has always named in its failure message, implemented here so
			// the message is true: a landing wave that adds a subcommand needs
			// it, and a reader told to run a command that does nothing would
			// hand-edit the bytes instead, which is how a golden stops meaning
			// anything. It writes only when asked, so an ordinary run can never
			// bless a diff by accident.
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("recapture golden %s: %v", goldenPath, err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (run UPDATE_GOLDEN=1 go test ./cmd/pasture to (re)capture)", goldenPath, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("native help bytes differ from baseline golden %s\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
			}
		})
	}
}

// TestGeneratedLifecycleDocsAbsenceOfRaw pins the generated-docs posture: the
// generated lifecycle registrations (per-harness hooks.json, activation
// catalogs, the opencode native module, the claude-code plugin bundle) MUST
// never advertise or register the raw path. Raw is invisible in every
// generated artifact BECAUSE it is the non-recommended escape hatch: it is
// only discoverable through the CLI's own --help marking.
func TestGeneratedLifecycleDocsAbsenceOfRaw(t *testing.T) {
	t.Parallel()

	root := repoRootFromTest(t)
	generated := []string{
		filepath.Join(root, "hooks", "hooks.json"),
		filepath.Join(root, "hooks", "pasture-activation.json"),
		filepath.Join(root, ".opencode", "pasture-opencode.json"),
		filepath.Join(root, "internal", "target", "claudecode", "assets", "pasture-hooks", ".claude-plugin", "plugin.json"),
	}
	for _, path := range generated {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read generated artifact %s: %v", path, err)
		}
		for _, needle := range []string{"lifecycle raw", "lifecycle raw --", "\"raw\""} {
			if bytes.Contains(data, []byte(needle)) {
				t.Errorf("generated artifact %s must not advertise the raw escape hatch, but contains %q", path, needle)
			}
		}
	}
}

// TestProtocolDocsNonRecommendedMarking is the protocol-docs acceptance check
// (R4.3c): the shipped lifecycle protocol doc (README.md, Lifecycle runtime
// section) must mark raw exactly — escape hatch, never the default, no second
// semantic model — in one precise paragraph.
func TestProtocolDocsNonRecommendedMarking(t *testing.T) {
	t.Parallel()

	root := repoRootFromTest(t)
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	body := string(data)
	// Collapse whitespace so prose wrapped across lines still matches the
	// canonical posture phrases.
	normalized := strings.Join(strings.Fields(body), " ")
	for _, phrase := range []string{
		"raw ingestion",
		"not the default path",
		"second semantic model",
	} {
		if !strings.Contains(normalized, phrase) {
			t.Errorf("README.md must carry the protocol posture phrase %q", phrase)
		}
	}
	re := regexp.MustCompile(`(?m)^.*` + regexp.QuoteMeta("raw ingestion") + `.*$`)
	if !re.MatchString(body) {
		t.Errorf("README.md must sentence the raw ingestion posture on a single line")
	}
}
