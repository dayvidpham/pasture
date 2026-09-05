// Package integration_test drives the built production `pasture` binary through
// its real installer CLI surface (install, uninstall, install status,
// install plan, and the scriptable apply-selection / apply-cell verbs) from
// unrelated temporary roots.
//
// Every invocation runs with a hand-built environment containing only an
// isolated HOME, an isolated XDG_STATE_HOME, a PATH holding one fixture
// directory, and the isolated Claude host's own variables. Nothing inherits
// the developer's environment, so no test can reach the real user home, the
// real Claude Code installation, or any network-capable helper program: the
// embedded installer has no network code path, and PATH contains no program
// that could add one.
//
// The Claude Code cells activate through the host's native plugin manager, so
// the suite supplies an isolated stand-in host binary
// (testdata/install/global/claudehost) that implements exactly the reviewed
// command grammar over a JSON state file. OpenCode and Codex cells write files
// directly beneath the isolated HOME and need no host program at all.
package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// pastureBinary and claudeHostBinary are built once for the whole suite.
var (
	pastureBinary    string
	claudeHostBinary string
	repoRoot         string
)

func TestMain(m *testing.M) {
	code, err := runSuite(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install integration suite: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runSuite(m *testing.M) (int, error) {
	root, err := moduleRoot()
	if err != nil {
		return 0, err
	}
	repoRoot = root
	buildDir, err := os.MkdirTemp("", "pasture-install-integration-*")
	if err != nil {
		return 0, fmt.Errorf("create suite build directory: %w", err)
	}
	defer os.RemoveAll(buildDir)

	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	pastureBinary = filepath.Join(buildDir, "pasture"+suffix)
	claudeHostBinary = filepath.Join(buildDir, "claude"+suffix)
	if err := goBuild(root, pastureBinary, "./cmd/pasture"); err != nil {
		return 0, err
	}
	// The isolated Claude host lives under testdata/ so the module's ordinary
	// build, vet, and test patterns never compile it; it is built explicitly
	// by directory path here.
	if err := goBuild(root, claudeHostBinary, "./testdata/install/global/claudehost"); err != nil {
		return 0, err
	}

	before, err := realHomeFingerprint()
	if err != nil {
		return 0, err
	}
	code := m.Run()
	after, err := realHomeFingerprint()
	if err != nil {
		return 0, err
	}
	if before != after {
		return 0, fmt.Errorf(
			"the real user home changed while the installer integration suite ran: installer destination roots went from %q to %q. "+
				"Every invocation in internal/install/integration builds its own environment with an isolated HOME, so the likely "+
				"cause is a test or helper that leaked the developer environment into a CLI subprocess; the other possibility is a "+
				"concurrent installer run outside this suite. Inspect those roots, repair the offending helper, and rerun on a quiet machine",
			before, after)
	}
	return code, nil
}

func goBuild(root, output, pkg string) error {
	// #nosec G204 — both arguments are suite constants, not user input.
	cmd := exec.Command("go", "build", "-o", output, pkg)
	cmd.Dir = root
	var combined bytes.Buffer
	cmd.Stdout, cmd.Stderr = &combined, &combined
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build %s into %q: %w; output: %s", pkg, output, err, combined.String())
	}
	return nil
}

func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", fmt.Errorf("no go.mod found above %q; run the suite from inside the Pasture module", wd)
		}
	}
}

// realHomeFingerprint stats the bounded set of roots the global installer would
// write to under the invoking user's real home. The suite asserts the
// fingerprint is unchanged across the whole run; adding or removing any entry
// in those directories changes their modification time and is therefore caught.
func realHomeFingerprint() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Without a resolvable real home there is nothing to protect.
		return "", nil
	}
	roots := []string{
		filepath.Join(home, ".local", "state", "pasture"),
		filepath.Join(home, ".local", "state", "pasture", "installations.yaml"),
		filepath.Join(home, ".config", "opencode", "skills"),
		filepath.Join(home, ".config", "opencode", "agent"),
		filepath.Join(home, ".config", "opencode", "plugins"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".codex", "agents"),
		filepath.Join(home, ".codex", "hooks"),
	}
	var builder strings.Builder
	for _, root := range roots {
		info, statErr := os.Lstat(root)
		if statErr != nil {
			fmt.Fprintf(&builder, "%s=absent;", root)
			continue
		}
		fmt.Fprintf(&builder, "%s=%d/%d/%s;", root, info.Size(), info.Mode(), info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z"))
	}
	return builder.String(), nil
}

// ---------------------------------------------------------------------------
// Isolated installer environment
// ---------------------------------------------------------------------------

// hostSeed describes the isolated Claude host's starting native state.
type hostSeed struct {
	// installedFixture names a file under testdata/install/global/claude-code
	// whose "installed" rows seed the host (empty means no installed plugins).
	installedFixture string
	// marketplaceFixture names a marketplace-list fixture (empty means none).
	marketplaceFixture string
	// omitVersion makes the host report versionless rows and write native
	// plugin manifests instead, exercising the manifest-reading probe path.
	omitVersion bool
	// failCommands injects a non-zero exit for any command whose joined argv
	// contains the given substring.
	failCommands map[string]string
	// hostVersion overrides the reported Claude Code version.
	hostVersion string
}

type installerEnv struct {
	t           *testing.T
	home        string
	stateHome   string
	binDir      string
	hostState   string
	hostLog     string
	installRoot string
	// workDir is the process working directory for every invocation. It
	// defaults to the isolated home and can be pointed at any unrelated empty
	// directory to prove the binary needs no source checkout.
	workDir string
}

// defaultHostVersion is what the fake host prints for --version: the recorded
// Claude Code version, read from the runtime contract, in the host's spelling.
var defaultHostVersion = pastureruntime.ClaudeCode2_1_210().Versions().Min().String() + " (Claude Code)"

// newEnv creates one isolated installer environment: an empty HOME, an empty
// state home, a PATH containing only the isolated Claude host, and a seeded
// host state file kept outside HOME so HOME contains installer output only.
func newEnv(t *testing.T, seed hostSeed) *installerEnv {
	t.Helper()
	base := t.TempDir()
	env := &installerEnv{
		t:           t,
		home:        filepath.Join(base, "home"),
		stateHome:   filepath.Join(base, "state"),
		binDir:      filepath.Join(base, "bin"),
		hostState:   filepath.Join(base, "claude-host-state.json"),
		hostLog:     filepath.Join(base, "claude-host-log.txt"),
		installRoot: filepath.Join(base, "claude-plugins"),
	}
	for _, dir := range []string{env.home, env.stateHome, env.binDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated directory %q: %v", dir, err)
		}
	}
	linkName := filepath.Join(env.binDir, "claude")
	if runtime.GOOS == "windows" {
		linkName += ".exe"
	}
	if err := os.Symlink(claudeHostBinary, linkName); err != nil {
		t.Fatalf("publish the isolated Claude host at %q: %v", linkName, err)
	}
	env.workDir = env.home
	env.seedHost(seed)
	return env
}

func (e *installerEnv) seedHost(seed hostSeed) {
	e.t.Helper()
	version := seed.hostVersion
	if version == "" {
		version = defaultHostVersion
	}
	state := map[string]any{
		"host_version": version,
		"marketplaces": e.marketplaceRows(seed.marketplaceFixture),
		"installed":    e.installedRows(seed.installedFixture),
		"versions": map[string]string{
			"pasture-skills": pluginVersion(e.t, "pasture-skills"),
			"pasture-agents": pluginVersion(e.t, "pasture-agents"),
			"pasture-hooks":  pluginVersion(e.t, "pasture-hooks"),
		},
		"install_root": e.installRoot,
		"omit_version": seed.omitVersion,
		"fail":         failureRows(seed.failCommands),
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		e.t.Fatalf("encode isolated Claude host state: %v", err)
	}
	if err := os.WriteFile(e.hostState, append(body, '\n'), 0o600); err != nil {
		e.t.Fatalf("write isolated Claude host state %q: %v", e.hostState, err)
	}
}

// clearHostFailures repairs the isolated host by removing every injected
// failure while preserving the native state the installer already confirmed.
// It is the fixture equivalent of the user fixing their Claude installation
// before rerunning the same command.
func (e *installerEnv) clearHostFailures() {
	e.t.Helper()
	data, err := os.ReadFile(e.hostState)
	if err != nil {
		e.t.Fatalf("read isolated Claude host state %q: %v", e.hostState, err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		e.t.Fatalf("decode isolated Claude host state %q: %v", e.hostState, err)
	}
	state["fail"] = []map[string]string{}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		e.t.Fatalf("encode isolated Claude host state: %v", err)
	}
	if err := os.WriteFile(e.hostState, append(body, '\n'), 0o600); err != nil {
		e.t.Fatalf("write isolated Claude host state %q: %v", e.hostState, err)
	}
}

func failureRows(failures map[string]string) []map[string]string {
	rows := make([]map[string]string, 0, len(failures))
	matches := make([]string, 0, len(failures))
	for match := range failures {
		matches = append(matches, match)
	}
	sort.Strings(matches)
	for _, match := range matches {
		rows = append(rows, map[string]string{"match": match, "message": failures[match]})
	}
	return rows
}

// fixtureHomePlaceholder is the placeholder path the checked-in Claude host
// fixtures use for the isolated home they were captured against.
const fixtureHomePlaceholder = "/isolated-home"

func (e *installerEnv) installedRows(fixture string) []map[string]any {
	e.t.Helper()
	if fixture == "" {
		return []map[string]any{}
	}
	var document struct {
		Installed []map[string]any `json:"installed"`
	}
	e.decodeFixture(fixture, &document)
	for _, row := range document.Installed {
		if path, ok := row["installPath"].(string); ok {
			row["installPath"] = strings.Replace(path, fixtureHomePlaceholder, e.home, 1)
		}
	}
	return document.Installed
}

func (e *installerEnv) marketplaceRows(fixture string) []map[string]any {
	e.t.Helper()
	if fixture == "" {
		return []map[string]any{}
	}
	var rows []map[string]any
	e.decodeFixture(fixture, &rows)
	for _, row := range rows {
		if path, ok := row["installLocation"].(string); ok {
			row["installLocation"] = strings.Replace(path, fixtureHomePlaceholder, e.home, 1)
		}
	}
	return rows
}

func (e *installerEnv) decodeFixture(fixture string, target any) {
	e.t.Helper()
	path := filepath.Join(repoRoot, "testdata", "install", "global", "claude-code", fixture)
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatalf("read Claude host fixture %q: %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		e.t.Fatalf("decode Claude host fixture %q: %v", path, err)
	}
}

// pluginVersion reads the shipped plugin version straight from the embedded
// Claude target assets so the isolated host reports the same release identity
// the installer proves after every native action.
func pluginVersion(t *testing.T, pkg string) string {
	t.Helper()
	path := filepath.Join(repoRoot, "internal", "target", "claudecode", "assets", pkg, ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shipped plugin manifest %q: %v", path, err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode shipped plugin manifest %q: %v", path, err)
	}
	if manifest.Version == "" {
		t.Fatalf("shipped plugin manifest %q has no version; the isolated host cannot report a release identity", path)
	}
	return manifest.Version
}

type outcome struct {
	stdout   string
	stderr   string
	exitCode int
}

func (o outcome) combined() string { return o.stdout + o.stderr }

// run executes the production binary with a completely hand-built environment.
// Nothing from the developer's environment is inherited.
func (e *installerEnv) run(args ...string) outcome {
	e.t.Helper()
	// #nosec G204 — pastureBinary is built by this suite from ./cmd/pasture.
	cmd := exec.Command(pastureBinary, args...)
	cmd.Dir = e.workDir
	cmd.Env = []string{
		"HOME=" + e.home,
		"XDG_STATE_HOME=" + e.stateHome,
		"PATH=" + e.binDir,
		"PASTURE_FAKE_CLAUDE_STATE=" + e.hostState,
		"PASTURE_FAKE_CLAUDE_LOG=" + e.hostLog,
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatalf("execute %s %s: %v\nstdout: %s\nstderr: %s", pastureBinary, strings.Join(args, " "), err, stdout.String(), stderr.String())
		}
		code = exitErr.ExitCode()
	}
	return outcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: code}
}

// mustRun fails the test unless the invocation exits zero.
func (e *installerEnv) mustRun(args ...string) outcome {
	e.t.Helper()
	got := e.run(args...)
	if got.exitCode != 0 {
		e.t.Fatalf("`pasture %s` exited %d, want 0\nstdout: %s\nstderr: %s", strings.Join(args, " "), got.exitCode, got.stdout, got.stderr)
	}
	return got
}

// files lists every regular file below the isolated HOME as a slash-separated
// relative path, sorted. The confirmed-state file lives outside HOME, so this
// is exactly the set of artifacts the installer delivered.
func (e *installerEnv) files() []string {
	e.t.Helper()
	var found []string
	err := filepath.Walk(e.home, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(e.home, path)
		if relErr != nil {
			return relErr
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		e.t.Fatalf("walk isolated home %q: %v", e.home, err)
	}
	sort.Strings(found)
	return found
}

// hostRows returns the selectors the isolated Claude host currently reports as
// installed, sorted.
func (e *installerEnv) hostRows() []string {
	e.t.Helper()
	data, err := os.ReadFile(e.hostState)
	if err != nil {
		e.t.Fatalf("read isolated Claude host state %q: %v", e.hostState, err)
	}
	var state struct {
		Installed []struct {
			ID string `json:"id"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		e.t.Fatalf("decode isolated Claude host state %q: %v", e.hostState, err)
	}
	rows := make([]string, 0, len(state.Installed))
	for _, row := range state.Installed {
		rows = append(rows, row.ID)
	}
	sort.Strings(rows)
	return rows
}

// hostCommands returns every argv the installer issued to the isolated Claude
// host, in order.
func (e *installerEnv) hostCommands() []string {
	e.t.Helper()
	data, err := os.ReadFile(e.hostLog)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("read isolated Claude host command log %q: %v", e.hostLog, err)
	}
	var commands []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			commands = append(commands, line)
		}
	}
	return commands
}

// mutatingHostCommands filters the command log down to the state-changing
// native actions, discarding the read-only probes.
func (e *installerEnv) mutatingHostCommands() []string {
	e.t.Helper()
	var mutations []string
	for _, command := range e.hostCommands() {
		if strings.HasPrefix(command, "plugin install ") || strings.HasPrefix(command, "plugin uninstall ") || strings.HasPrefix(command, "plugin update ") {
			mutations = append(mutations, command)
		}
	}
	return mutations
}

func (e *installerEnv) resetHostLog() {
	e.t.Helper()
	if err := os.Remove(e.hostLog); err != nil && !os.IsNotExist(err) {
		e.t.Fatalf("reset isolated Claude host command log %q: %v", e.hostLog, err)
	}
}

// statusCell mirrors one row of `pasture install status --json`.
type statusCell struct {
	Scope       string `json:"scope"`
	Cell        string `json:"cell"`
	Observation string `json:"observation"`
	Strategy    string `json:"strategy"`
	Source      string `json:"source"`
	Managed     bool   `json:"managed"`
	Trust       string `json:"trust"`
	LastAction  string `json:"last_action"`
	LastOutcome string `json:"last_outcome"`
	Diagnostic  string `json:"diagnostic"`
}

// status reads the confirmed inventory through the production status verb.
func (e *installerEnv) status() map[string]statusCell {
	e.t.Helper()
	got := e.mustRun("install", "status", "--json")
	var document struct {
		StateFile string       `json:"state_file"`
		Cells     []statusCell `json:"cells"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &document); err != nil {
		e.t.Fatalf("decode `pasture install status --json`: %v\nbody: %s", err, got.stdout)
	}
	if want := filepath.Join(e.stateHome, "pasture", "installations.yaml"); document.StateFile != want {
		e.t.Fatalf("status read state file %q, want the isolated %q — the run leaked outside its temporary root", document.StateFile, want)
	}
	byCell := make(map[string]statusCell, len(document.Cells))
	for _, row := range document.Cells {
		byCell[row.Cell] = row
	}
	return byCell
}

// applyRow mirrors one per-cell row of the deterministic apply-result document.
type applyRow struct {
	Index       int    `json:"index"`
	Cell        string `json:"cell"`
	Harness     string `json:"harness"`
	Extension   string `json:"extension"`
	Operation   string `json:"operation"`
	Status      string `json:"status"`
	Management  string `json:"management"`
	Observation string `json:"observation"`
	Diagnostic  string `json:"diagnostic"`
}

// applyResult mirrors the deterministic apply-result document emitted by the
// install, uninstall, apply-cell, and apply-selection verbs under --json.
type applyResult struct {
	Schema string     `json:"schema"`
	Source string     `json:"source"`
	Scope  string     `json:"scope"`
	OK     bool       `json:"ok"`
	Cells  []applyRow `json:"cells"`
}

func (r applyResult) row(t *testing.T, cell string) applyRow {
	t.Helper()
	for _, row := range r.Cells {
		if row.Cell == cell {
			return row
		}
	}
	t.Fatalf("apply result has no row for %s; rows: %+v", cell, r.Cells)
	return applyRow{}
}

func (r applyResult) cellNames() []string {
	names := make([]string, 0, len(r.Cells))
	for _, row := range r.Cells {
		names = append(names, row.Cell)
	}
	return names
}

func decodeApply(t *testing.T, body string) applyResult {
	t.Helper()
	var result applyResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("decode apply-result document: %v\nbody: %s", err, body)
	}
	if result.Schema != "pasture.install.apply-result/v1" {
		t.Fatalf("apply-result schema is %q, want pasture.install.apply-result/v1", result.Schema)
	}
	return result
}

// writeDesired renders an exhaustive effective-selection document for the nine
// cells. TestInstallPlan_EmitsTheExhaustiveDocumentTheApplyVerbAccepts proves
// this rendering is byte-identical to what the production `install plan` verb
// normalizes, so hand-built desired documents cannot drift from the shape the
// apply verbs accept.
func (e *installerEnv) writeDesired(enabled map[string]bool) string {
	e.t.Helper()
	var body strings.Builder
	body.WriteString("schema: pasture.install.effective-selection/v1\ncells:\n")
	for _, harness := range []string{"claude-code", "opencode", "codex"} {
		fmt.Fprintf(&body, "  %s:\n", harness)
		for _, extension := range []string{"skills", "agents", "hooks"} {
			fmt.Fprintf(&body, "    %s: %t\n", extension, enabled[harness+"."+extension])
		}
	}
	path := filepath.Join(e.t.TempDir(), "desired.yaml")
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		e.t.Fatalf("write desired selection %q: %v", path, err)
	}
	return path
}

// writeConfig saves installer preferences at the isolated home's default
// configuration path, exactly where the production resolver looks for them.
func (e *installerEnv) writeConfig(body string) {
	e.t.Helper()
	dir := filepath.Join(e.home, ".config", "pasture")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatalf("create isolated config directory %q: %v", dir, err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		e.t.Fatalf("write isolated installer preferences %q: %v", path, err)
	}
}

func allCells(value bool) map[string]bool {
	selection := make(map[string]bool, 9)
	for _, harness := range []string{"claude-code", "opencode", "codex"} {
		for _, extension := range []string{"skills", "agents", "hooks"} {
			selection[harness+"."+extension] = value
		}
	}
	return selection
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
