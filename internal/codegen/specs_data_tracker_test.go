package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTrackerOpsContract proves the closed operation set is exactly the ten
// canonical ops, every op carries both backend recipes, and the two folded
// legacy verbs (relate, assign) do not appear as operations.
func TestTrackerOpsContract(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateTrackerOps())

	want := []TrackerOpId{
		TrackerOpCreate, TrackerOpLabel, TrackerOpBlock, TrackerOpNote,
		TrackerOpShow, TrackerOpQuery, TrackerOpReady, TrackerOpStatus,
		TrackerOpClose, TrackerOpWhoami,
	}
	require.Len(t, AllTrackerOpIds(), len(want))
	require.ElementsMatch(t, want, AllTrackerOpIds())

	for _, id := range AllTrackerOpIds() {
		op, ok := TrackerOps[id]
		require.Truef(t, ok, "operation %q must be in TrackerOps", id)
		require.Equal(t, id, op.Id)
		require.NotEmpty(t, op.Action, "operation %q must describe its action", id)
		require.Contains(t, op.Signature, trackerEntryPoint+" "+string(id),
			"operation %q must use the one call form", id)
		require.NotEmpty(t, op.Pasture, "operation %q must have a Pasture recipe", id)
		require.NotEmpty(t, op.Ledger, "operation %q must have a ledger recipe", id)
	}

	_, relate := TrackerOps["relate"]
	require.False(t, relate, "relate folds into block --kind; it is not an operation")
	_, assign := TrackerOps["assign"]
	require.False(t, assign, "assign becomes label owner:<role>; it is not an operation")

	require.Contains(t, TrackerOps[TrackerOpBlock].Signature, "--kind",
		"block must carry the typed-edge kind that absorbs relate")
	require.Contains(t, TrackerAssignmentForm, "owner:<role>",
		"the assignment fold must name the owner label form")

	for _, backend := range AllTrackerBackends() {
		require.True(t, backend.IsValid(), "backend %q must be a registered member", backend)
	}
	require.Len(t, AllTrackerBackends(), 3, "exactly three backends: pasture, ledger, error")
	require.False(t, TrackerBackend("unknown").IsValid(), "an unregistered backend must not validate")
}

// TestTrackerOpsValidationHasTwoSamples is the tenet-9 guard: one defective
// contract must fail, and the production contract must pass.
func TestTrackerOpsValidationHasTwoSamples(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateTrackerOps(SortedTrackerOps()), "clean sample must produce no finding")

	defective := SortedTrackerOps()
	defective[0].Ledger = ""
	require.Error(t, validateTrackerOps(defective), "an op without a ledger recipe must fail")

	defective = SortedTrackerOps()[:9]
	require.Error(t, validateTrackerOps(defective), "a missing op must fail")

	defective = SortedTrackerOps()
	defective[0].Signature = "pasture task create"
	require.Error(t, validateTrackerOps(defective), "a signature that bypasses the entry point must fail")
}

// TestTrackerFragmentIsBackendNeutral proves the generated appendix carries
// the entry point, the three backend states, the ten ops, EPOCH.md and the
// no-switch / write-failure rules, and that it names no native Pasture recipe.
func TestTrackerFragmentIsBackendNeutral(t *testing.T) {
	t.Parallel()

	fragment, ok := SharedFragmentSpecs[FragTrackerAdapter]
	require.True(t, ok, "the tracker adapter fragment must be registered")
	require.NotNil(t, fragment.Prose)
	content := fragment.Prose.Content

	require.Contains(t, content, ".agents.local/pasture/EPOCH.md")
	require.Contains(t, content, trackerEntryPoint)
	for _, backend := range AllTrackerBackends() {
		require.Contains(t, content, string(backend), "the appendix must name backend %q", backend)
	}
	for _, id := range AllTrackerOpIds() {
		require.Contains(t, content, string(id), "the appendix must name operation %q", id)
	}
	require.Contains(t, content, "owner:<role>")
	require.Contains(t, content, "retries 3 times")
	require.Contains(t, content, "Never switch backends")
	require.Contains(t, content, "never drop the record")
	require.Contains(t, content, "pending")

	require.NotRegexp(t, `pasture\s+task\s+(create|label|dep|comment|show|list|blocked|ready|close|update|comments|timeline|agents|assignment)\b`, content,
		"the adapter appendix must not copy a native Pasture recipe")

	for _, target := range []HarnessName{HarnessClaudeCode, HarnessOpenCode, HarnessCodex} {
		resolved, err := ResolveBodyMarkers(content, target)
		require.NoError(t, err, "adapter fragment must resolve for %s", target)
		require.Contains(t, resolved, trackerEntryPoint)
	}
}

// TestTrackerEpochHeader pins the fields that make the backend record
// auditable: the backend, the adapter path, both store locations and the
// ledger degradation.
func TestTrackerEpochHeader(t *testing.T) {
	t.Parallel()

	for _, want := range []string{"epoch:", "backend:", "probed_at:", "namespace:", "db:", "ledger:", "rec:", "degrades:"} {
		require.Contains(t, TrackerEpochHeaderTemplate, want)
	}
	for _, backend := range AllTrackerBackends() {
		require.Contains(t, TrackerEpochHeaderTemplate, string(backend))
	}
	require.Contains(t, TrackerEpochHeaderTemplate, "assign=claim-only")
	require.Contains(t, TrackerProbeLimits, "pasture --help")
	require.Contains(t, TrackerProbeLimits, "test -f")
	require.Contains(t, TrackerProbeLimits, "migrate --dry-run")
	require.Contains(t, TrackerProbeLimits, "cannot detect")
}

// TestProbeSubprocess runs the production probe as a subprocess against a fake
// Pasture binary. R21: the probe is side-effect free, checks capability before
// the store, and reports exactly one of pasture, ledger or error.
func TestProbeSubprocess(t *testing.T) {
	t.Parallel()

	t.Run("not-installed-is-ledger", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":        dir,
			"PASTURE_BIN": filepath.Join(dir, "absent-pasture"),
		})
		require.Equal(t, "ledger", result["BACKEND"])
		require.Equal(t, "not-installed", result["REASON"])
	})

	t.Run("missing-store-is-ledger-and-is-not-created", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin, log := writeFakePasture(t, dir)
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":            dir,
			"PASTURE_BIN":     bin,
			"PROBE_FAKE_LOG":  log,
			"PROBE_FAKE_MODE": "ok",
		})
		require.Equal(t, "ledger", result["BACKEND"])
		require.Equal(t, "no-store", result["REASON"])
		storePath := filepath.Join(dir, ".local", "share", "pasture", "pasture.db")
		_, err := os.Stat(storePath)
		require.True(t, os.IsNotExist(err), "the probe must never create a store")
		invocations, err := os.ReadFile(log)
		require.NoError(t, err)
		require.NotContains(t, string(invocations), "migrate", "a missing store must not reach migrate --dry-run")
		require.NotContains(t, string(invocations), " task ", "a missing store must not be opened by a task command")
	})

	t.Run("usable-store-is-pasture", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin, log := writeFakePasture(t, dir)
		store := filepath.Join(dir, "pasture.db")
		require.NoError(t, os.WriteFile(store, []byte("store"), 0o600))
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":              dir,
			"PASTURE_BIN":       bin,
			"PROBE_FAKE_LOG":    log,
			"PROBE_FAKE_MODE":   "ok",
			"PASTURE_DB_PATH":   store,
			"PASTURE_NAMESPACE": "https://example.test/repo",
		})
		require.Equal(t, "pasture", result["BACKEND"])
		require.Equal(t, store, result["DB"])
		require.Equal(t, "https://example.test/repo", result["NS"])
		require.Equal(t, "pasture-system/00000000-0000-0000-0000-000000000000", result["ACT"])
		require.Equal(t, "v9.9.9", result["VER"])
		invocations, err := os.ReadFile(log)
		require.NoError(t, err)
		require.Contains(t, string(invocations), "migrate --dry-run")
		require.Contains(t, string(invocations), "task agents show")
	})

	t.Run("unusable-store-is-error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin, log := writeFakePasture(t, dir)
		store := filepath.Join(dir, "pasture.db")
		require.NoError(t, os.WriteFile(store, []byte("store"), 0o600))
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":              dir,
			"PASTURE_BIN":       bin,
			"PROBE_FAKE_LOG":    log,
			"PROBE_FAKE_MODE":   "store-unusable",
			"PASTURE_DB_PATH":   store,
			"PASTURE_NAMESPACE": "https://example.test/repo",
		})
		require.Equal(t, "error", result["BACKEND"])
		require.Equal(t, "store-exit-2", result["REASON"])
	})

	t.Run("missing-verb-is-ledger-before-the-store", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin, log := writeFakePasture(t, dir)
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":            dir,
			"PASTURE_BIN":     bin,
			"PROBE_FAKE_LOG":  log,
			"PROBE_FAKE_MODE": "missing-verb",
		})
		require.Equal(t, "ledger", result["BACKEND"])
		require.Equal(t, "missing-verb:task label", result["REASON"])
		invocations, err := os.ReadFile(log)
		require.NoError(t, err)
		require.NotContains(t, string(invocations), "migrate", "capability is checked before the store")
	})

	t.Run("help-failure-is-ledger", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin, log := writeFakePasture(t, dir)
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":            dir,
			"PASTURE_BIN":     bin,
			"PROBE_FAKE_LOG":  log,
			"PROBE_FAKE_MODE": "help-failed",
		})
		require.Equal(t, "ledger", result["BACKEND"])
		require.Equal(t, "help-failed", result["REASON"])
	})

	t.Run("unregistered-author-is-error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin, log := writeFakePasture(t, dir)
		store := filepath.Join(dir, "pasture.db")
		require.NoError(t, os.WriteFile(store, []byte("store"), 0o600))
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":              dir,
			"PASTURE_BIN":       bin,
			"PROBE_FAKE_LOG":    log,
			"PROBE_FAKE_MODE":   "no-author",
			"PASTURE_DB_PATH":   store,
			"PASTURE_NAMESPACE": "https://example.test/repo",
		})
		require.Equal(t, "error", result["BACKEND"])
		require.Equal(t, "no-author", result["REASON"])
	})

	t.Run("missing-namespace-is-error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin, log := writeFakePasture(t, dir)
		store := filepath.Join(dir, "pasture.db")
		require.NoError(t, os.WriteFile(store, []byte("store"), 0o600))
		result := runTrackerProbe(t, dir, map[string]string{
			"HOME":            dir,
			"PASTURE_BIN":     bin,
			"PROBE_FAKE_LOG":  log,
			"PROBE_FAKE_MODE": "ok",
			"PASTURE_DB_PATH": store,
		})
		require.Equal(t, "error", result["BACKEND"])
		require.Equal(t, "no-namespace", result["REASON"])
	})
}

// runTrackerProbe executes the production probe script in dir with exactly the
// supplied environment (plus PATH) and parses its KEY=VALUE output.
func runTrackerProbe(t *testing.T, dir string, env map[string]string) map[string]string {
	t.Helper()
	return runProbeScript(t, dir, env, TrackerProbeScript)
}

// runProbeScript executes one probe variant and parses its KEY=VALUE output.
func runProbeScript(t *testing.T, dir string, env map[string]string, script string) map[string]string {
	t.Helper()

	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "probe failed: %s", out)

	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && key != "" {
			result[key] = value
		}
	}
	return result
}

// writeFakePasture writes a controllable fake Pasture binary and its invocation
// log. The fake answers --help and --version, and exits per PROBE_FAKE_MODE for
// the store and author checks.
//
// The fake CREATES the store on any real task call, exactly as the real CLI
// does on open. That is what makes the probe's "must never create a store"
// assertion live: a probe that runs a task command before the store-existence
// check creates the file here, and the assertion turns red.
func writeFakePasture(t *testing.T, dir string) (binPath, logPath string) {
	t.Helper()

	binPath = filepath.Join(dir, "pasture")
	logPath = filepath.Join(dir, "pasture.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$PROBE_FAKE_LOG"
if [ -n "${PROBE_FAKE_ARGV:-}" ]; then
  for _a in "$@"; do
    printf '%s\037' "$_a" >> "$PROBE_FAKE_ARGV"
  done
  printf '\036' >> "$PROBE_FAKE_ARGV"
fi
mode="${PROBE_FAKE_MODE:-ok}"
case "$1" in
  --version) printf 'pasture version v9.9.9\n'; exit 0 ;;
  --help)
    [ "$mode" = "help-failed" ] && exit 1
    exit 0 ;;
esac
# A real task command opens (and so creates) the selected store. --help never
# does, so the capability sweep is exempt.
case " $* " in
  *" --help "*) : ;;
  *" task "*)
    s="${PASTURE_DB_PATH:-${XDG_DATA_HOME:-$HOME/.local/share}/pasture/pasture.db}"
    mkdir -p "$(dirname "$s")"
    [ -f "$s" ] || : > "$s"
    ;;
esac
if [ "$1" = "--db" ]; then shift 2; fi
if [ "$1" = "migrate" ]; then
  [ "$mode" = "store-unusable" ] && exit 2
  exit 0
fi
if [ "$1" = "task" ]; then
  if [ "$2" = "agents" ] && [ "$3" = "show" ]; then
    [ "$mode" = "no-author" ] && exit 3
    exit 0
  fi
  if [ "$mode" = "missing-verb" ] && [ "$2" = "label" ] && [ "$3" = "--help" ]; then exit 1; fi
  if [ "$2" = "create" ]; then
    printf 'ID:        fake/00000000-0000-0000-0000-000000000001\n'
  fi
  exit 0
fi
exit 0
`
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))
	return binPath, logPath
}

// TestProbeStoreGuardIsLive is the tenet-9 two-sample guard for the
// store-existence step. The fake now creates the store on any real task call,
// so a probe that opens a missing store turns this test red instead of passing
// the "must never create a store" assertion.
func TestProbeStoreGuardIsLive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bin, log := writeFakePasture(t, dir)
	env := map[string]string{
		"HOME":            dir,
		"PASTURE_BIN":     bin,
		"PROBE_FAKE_LOG":  log,
		"PROBE_FAKE_MODE": "ok",
	}
	storePath := filepath.Join(dir, ".local", "share", "pasture", "pasture.db")

	// Clean sample: the production probe never opens the missing store.
	result := runTrackerProbe(t, dir, env)
	require.Equal(t, "ledger", result["BACKEND"])
	require.Equal(t, "no-store", result["REASON"])
	_, err := os.Stat(storePath)
	require.True(t, os.IsNotExist(err), "the clean probe must not create the store")
	invocations, err := os.ReadFile(log)
	require.NoError(t, err)
	require.NotContains(t, string(invocations), " task ", "the clean probe must not run a task command")

	// Defective sample: one real task command before the existence check. The
	// fake creates the store, so the production assertion would now fail.
	mutated := strings.Replace(TrackerProbeScript,
		`if [ ! -f "$DB" ]; then`,
		"\"$PASTURE_BIN\" --db \"$DB\" task list >/dev/null 2>&1\nif [ ! -f \"$DB\" ]; then",
		1)
	require.NotEqual(t, TrackerProbeScript, mutated, "the defective sample must actually change the probe")
	runProbeScript(t, dir, env, mutated)
	_, err = os.Stat(storePath)
	require.NoError(t, err, "the fake must create the store on a task call, so the no-create assertion can fail")
}

// TestRecAssetServesTheTenOps runs the shipped rec asset against a fake Pasture
// binary and proves every neutral op lowers to the argv declared in TrackerOps.
// It compares the argument vector, not a substring of a joined command line,
// with a namespace and without one.
func TestRecAssetServesTheTenOps(t *testing.T) {
	t.Parallel()

	ns := "https://example.test/repo"
	cases := []struct {
		name    string
		args    []string
		withNS  []string
		without []string
	}{
		{
			name:    "create",
			args:    []string{"create", "--kind", "task", "--title", "T", "--phase", "request", "--body", "B"},
			withNS:  []string{"task", "create", "T", "--type", "task", "--phase", "request", "--description", "B", "--namespace", ns, "--format", "text"},
			without: []string{"task", "create", "T", "--type", "task", "--phase", "request", "--description", "B", "--format", "text"},
		},
		{
			name:    "label",
			args:    []string{"label", "ID", "pasture:urd"},
			withNS:  []string{"task", "label", "add", "ID", "pasture:urd"},
			without: []string{"task", "label", "add", "ID", "pasture:urd"},
		},
		{
			name:    "block",
			args:    []string{"block", "P", "--by", "C"},
			withNS:  []string{"task", "dep", "add", "P", "--blocked-by", "C"},
			without: []string{"task", "dep", "add", "P", "--blocked-by", "C"},
		},
		{
			name:    "block-typed",
			args:    []string{"block", "P", "--by", "C", "--kind", "supersedes"},
			withNS:  []string{"task", "dep", "add", "P", "--target", "C", "--kind", "supersedes"},
			without: []string{"task", "dep", "add", "P", "--target", "C", "--kind", "supersedes"},
		},
		{
			name:    "note",
			args:    []string{"note", "ID", "--author", "actor/x", "--body", "hello"},
			withNS:  []string{"task", "comment", "add", "ID", "--author", "actor/x", "--", "hello"},
			without: []string{"task", "comment", "add", "ID", "--author", "actor/x", "--", "hello"},
		},
		{
			name:    "show",
			args:    []string{"show", "ID"},
			withNS:  []string{"task", "show", "ID", "--format", "json"},
			without: []string{"task", "show", "ID", "--format", "json"},
		},
		{
			name:    "show-notes",
			args:    []string{"show", "ID", "--notes"},
			withNS:  []string{"task", "comments", "ID", "--format", "json"},
			without: []string{"task", "comments", "ID", "--format", "json"},
		},
		{
			name:    "show-history",
			args:    []string{"show", "ID", "--history"},
			withNS:  []string{"task", "timeline", "ID"},
			without: []string{"task", "timeline", "ID"},
		},
		{
			name:    "query",
			args:    []string{"query", "--label", "pasture:urd", "--status", "open"},
			withNS:  []string{"task", "list", "--status", "open", "--label", "pasture:urd", "--namespace", ns, "--format", "json"},
			without: []string{"task", "list", "--status", "open", "--label", "pasture:urd", "--format", "json"},
		},
		{
			name:    "query-blocked",
			args:    []string{"query", "--blocked", "--label", "x"},
			withNS:  []string{"task", "blocked", "--label", "x", "--namespace", ns, "--format", "json"},
			without: []string{"task", "blocked", "--label", "x", "--format", "json"},
		},
		{
			name:    "query-tree",
			args:    []string{"query", "--tree", "ID"},
			withNS:  []string{"task", "dep", "tree", "ID"},
			without: []string{"task", "dep", "tree", "ID"},
		},
		{
			name:    "ready",
			args:    []string{"ready", "--label", "x"},
			withNS:  []string{"task", "ready", "--label", "x", "--namespace", ns, "--format", "json"},
			without: []string{"task", "ready", "--label", "x", "--format", "json"},
		},
		{
			name:    "status",
			args:    []string{"status", "ID", "--set", "in_progress"},
			withNS:  []string{"task", "update", "ID", "--status", "in_progress"},
			without: []string{"task", "update", "ID", "--status", "in_progress"},
		},
		{
			name:    "close",
			args:    []string{"close", "ID", "--reason", "done"},
			withNS:  []string{"task", "close", "ID", "--reason", "done"},
			without: []string{"task", "close", "ID", "--reason", "done"},
		},
		{
			name:    "whoami",
			args:    []string{"whoami"},
			withNS:  []string{"task", "agents", "show", "actor/x"},
			without: []string{"task", "agents", "show", "actor/x"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name+"/namespace", func(t *testing.T) {
			t.Parallel()
			got := recArgv(t, tc.args, true)
			require.Equal(t, tc.withNS, got, "pasture argv with a namespace")
			require.NotContains(t, got, "--all-namespaces")
		})
		t.Run(tc.name+"/no-namespace", func(t *testing.T) {
			t.Parallel()
			got := recArgv(t, tc.args, false)
			require.Equal(t, tc.without, got, "pasture argv without a namespace")
			require.NotContains(t, got, "--namespace")
		})
	}
}

// recArgv runs one op against the fake backend and returns the task argv of
// the first invocation, with --db and its value removed.
func recArgv(t *testing.T, args []string, withNS bool) []string {
	t.Helper()
	dir := t.TempDir()
	bin, log := writeFakePasture(t, dir)
	argvLog := filepath.Join(dir, "argv.log")
	header := filepath.Join(dir, "EPOCH.md")
	body := "backend: pasture\ndb: " + filepath.Join(dir, "store.db") + "\nauthor: actor/x\nrec: " + header + "\n"
	if withNS {
		body += "namespace: https://example.test/repo\n"
	}
	require.NoError(t, os.WriteFile(header, []byte(body), 0o600))
	out, code := runRec(t, dir, map[string]string{
		"HOME":            dir,
		"PASTURE_BIN":     bin,
		"PASTURE_DB_PATH": filepath.Join(dir, "store.db"),
		"EPOCH_FILE":      header,
		"PROBE_FAKE_LOG":  log,
		"PROBE_FAKE_ARGV": argvLog,
	}, args...)
	require.Zero(t, code, out)
	if args[0] == "whoami" {
		require.Equal(t, "actor/x\n", out, "whoami prints the recorded author and nothing else")
	}
	return firstTaskArgv(t, argvLog)
}

func firstTaskArgv(t *testing.T, argvLog string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvLog)
	require.NoError(t, err)
	record := strings.Split(string(raw), "\x1e")[0]
	fields := strings.Split(record, "\x1f")
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	if len(fields) >= 2 && fields[0] == "--db" {
		fields = fields[2:]
	}
	return fields
}

// TestRecAssetStopsOnErrorBackend proves the adapter's error backend prints an
// actionable message and exits 3 before any backend call.
func TestRecAssetStopsOnErrorBackend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bin, log := writeFakePasture(t, dir)
	header := filepath.Join(dir, "EPOCH.md")
	require.NoError(t, os.WriteFile(header, []byte("backend: error\ndb: "+filepath.Join(dir, "store.db")+"\n"), 0o600))
	out, code := runRec(t, dir, map[string]string{
		"PASTURE_BIN":    bin,
		"EPOCH_FILE":     header,
		"PROBE_FAKE_LOG": log,
	}, "create", "--kind", "task", "--title", "T", "--phase", "request")
	require.Equal(t, 3, code)
	require.Contains(t, out, "backend is error")
	require.Contains(t, out, "why:")
	require.Contains(t, out, "fix:")
	_, err := os.Stat(log)
	require.True(t, os.IsNotExist(err), "the error backend must stop before any backend call")
}

// TestRecAssetRequiresAHeader proves a missing EPOCH.md is an actionable stop,
// not a silent pass-through.
func TestRecAssetRequiresAHeader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out, code := runRec(t, dir, map[string]string{"EPOCH_FILE": filepath.Join(dir, "absent.md")}, "whoami")
	require.Equal(t, 3, code)
	require.Contains(t, out, "no epoch header")
	require.Contains(t, out, "Starting an Epoch")
}

// runRec runs the production rec asset with the supplied environment.
func runRec(t *testing.T, dir string, env map[string]string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", TrackerRecScript, "rec"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("run rec: %v", err)
	return "", -1
}

// TestRenderedTrackerCallsUseRegisteredOps is the vocabulary guard: every
// authored [[rec:...]] marker names a registered op, and after resolution the
// token after the entry point is a registered op too. A body that writes a
// native verb after the entry point fails here instead of shipping a second
// vocabulary.
func TestRenderedTrackerCallsUseRegisteredOps(t *testing.T) {
	t.Parallel()

	sources, err := renderedSources()
	require.NoError(t, err)
	marker := regexp.MustCompile(`\[\[rec([^\]]*)\]\]`)
	callOp := regexp.MustCompile(`^\s*([a-z_]+)`)

	for _, source := range sources {
		for _, match := range marker.FindAllStringSubmatch(source.Text, -1) {
			payload := match[1]
			require.True(t, strings.HasPrefix(payload, ":"),
				"%s %s writes a bare [[rec]] marker; name the operation: [[rec:<op>]]", source.Corpus, source.Owner)
			id := TrackerOpId(strings.TrimPrefix(payload, ":"))
			require.True(t, id.isRegistered(),
				"%s %s writes unregistered tracker op %q; the vocabulary is closed", source.Corpus, source.Owner, id)
		}

		for _, target := range []HarnessName{HarnessClaudeCode, HarnessOpenCode, HarnessCodex} {
			resolved, err := ResolveBodyMarkers(source.Text, target)
			require.NoError(t, err, "%s %s must resolve for %s", source.Corpus, source.Owner, target)
			require.Equalf(t, 0, countUnregisteredTrackerCalls(resolved, trackerEntryPoint, callOp),
				"%s %s resolves a native verb after the entry point for %s", source.Corpus, source.Owner, target)
		}
		neutral, err := ResolveBodyMarkersNeutral(source.Text)
		require.NoError(t, err, "%s %s must resolve for schema.xml", source.Corpus, source.Owner)
		require.Equalf(t, 0, countUnregisteredTrackerCalls(neutral, neutralTrackerEntryPoint, callOp),
			"%s %s resolves a native verb after the entry point for schema.xml", source.Corpus, source.Owner)
	}
}

// TestCreateRefIsHonored proves --ref is folded into the description instead of
// being parsed and dropped.
func TestCreateRefIsHonored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bin, log := writeFakePasture(t, dir)
	argvLog := filepath.Join(dir, "argv.log")
	header := filepath.Join(dir, "EPOCH.md")
	require.NoError(t, os.WriteFile(header, []byte("backend: pasture\ndb: "+filepath.Join(dir, "store.db")+"\nauthor: actor/x\nnamespace: https://example.test/repo\n"), 0o600))
	out, code := runRec(t, dir, map[string]string{
		"HOME":            dir,
		"PASTURE_BIN":     bin,
		"EPOCH_FILE":      header,
		"PROBE_FAKE_LOG":  log,
		"PROBE_FAKE_ARGV": argvLog,
	}, "create", "--kind", "task", "--title", "T", "--phase", "request", "--ref", "k=v", "--body", "hello")
	require.Zero(t, code, out)
	fields := firstTaskArgv(t, argvLog)
	desc := ""
	for i, field := range fields {
		if field == "--description" && i+1 < len(fields) {
			desc = fields[i+1]
		}
	}
	require.Equal(t, "---\nreferences:\n  k: v\n---\nhello", desc)
}

// TestWhoamiWithoutAuthorDoesNotCallTheBackend proves the one whoami mapping
// stops before any second command when no author is recorded.
func TestWhoamiWithoutAuthorDoesNotCallTheBackend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bin, log := writeFakePasture(t, dir)
	header := filepath.Join(dir, "EPOCH.md")
	require.NoError(t, os.WriteFile(header, []byte("backend: pasture\ndb: "+filepath.Join(dir, "store.db")+"\n"), 0o600))
	out, code := runRec(t, dir, map[string]string{
		"PASTURE_BIN":    bin,
		"EPOCH_FILE":     header,
		"PROBE_FAKE_LOG": log,
	}, "whoami")
	require.Equal(t, 3, code)
	require.Contains(t, out, "no recorded author")
	require.Contains(t, out, "fix:")
	_, err := os.Stat(log)
	require.True(t, os.IsNotExist(err), "whoami must not fall back to agents list")
}

// TestQueryAllNamespacesSkipsNamespace proves a caller that opts out does not
// receive a namespace flag.
func TestQueryAllNamespacesSkipsNamespace(t *testing.T) {
	t.Parallel()

	got := recArgv(t, []string{"query", "--blocked", "--all-namespaces"}, true)
	require.Equal(t, []string{"task", "blocked", "--all-namespaces", "--format", "json"}, got)
}

// TestTrackerDispatchScopeGuard is the two-sample guard: the production table
// passes, and a ready call that forgets to be scoped fails the build.
func TestTrackerDispatchScopeGuard(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateTrackerDispatch(TrackerOps))
	ops := make(map[TrackerOpId]TrackerOp, len(TrackerOps))
	for id, op := range TrackerOps {
		ops[id] = op
	}
	ready := ops[TrackerOpReady]
	calls := append([]pastureCall(nil), ready.PastureCalls...)
	require.NotEmpty(t, calls)
	calls[0].Scoped = false
	ready.PastureCalls = calls
	ops[TrackerOpReady] = ready
	require.Error(t, validateTrackerDispatch(ops))
}

// TestEmptyHomeInstallUsesLedger follows the install path: write the probe and
// adapter, run the probe against an empty home, write EPOCH.md, and complete
// the first operation on the ledger backend.
func TestEmptyHomeInstallUsesLedger(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	work := filepath.Join(dir, "work")
	require.NoError(t, os.MkdirAll(filepath.Join(work, ".agents.local", "pasture"), 0o755))
	probe := filepath.Join(work, ".agents.local", "pasture", "probe.sh")
	rec := filepath.Join(work, ".agents.local", "pasture", "rec")
	require.NoError(t, os.WriteFile(probe, []byte(TrackerProbeScript), 0o755))
	require.NoError(t, os.WriteFile(rec, []byte(TrackerRecScript), 0o755))
	bin, log := writeFakePasture(t, dir)

	cmd := exec.Command("sh", probe)
	cmd.Dir = work
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"PASTURE_BIN=" + bin,
		"PWD=" + work,
		"PROBE_FAKE_LOG=" + log,
		"PROBE_FAKE_MODE=ok",
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "probe: %s", out)
	probeOut := parseKV(string(out))
	require.Equal(t, "ledger", probeOut["BACKEND"])
	require.Equal(t, "no-store", probeOut["REASON"])
	store := filepath.Join(home, ".local", "share", "pasture", "pasture.db")
	_, statErr := os.Stat(store)
	require.True(t, os.IsNotExist(statErr), "probe must not create a store")

	header := filepath.Join(work, ".agents.local", "pasture", "EPOCH.md")
	epoch := "backend: " + probeOut["BACKEND"] + "\n" +
		"namespace: https://example.test/fresh\n" +
		"author: actor/fresh\n" +
		"ledger: " + probeOut["LEDGER"] + "\n" +
		"rec: " + rec + "\n" +
		"probed_at: " + probeOut["PROBED_AT"] + "\n"
	require.NoError(t, os.WriteFile(header, []byte(epoch), 0o600))

	created := exec.Command(rec, "create", "--kind", "task", "--title", "first", "--phase", "request", "--body", "hello")
	created.Dir = work
	created.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "EPOCH_FILE=" + header, "PASTURE_BIN=" + bin}
	var stdout, stderr bytes.Buffer
	created.Stdout = &stdout
	created.Stderr = &stderr
	err = created.Run()
	require.NoError(t, err, "first ledger op stdout=%s stderr=%s", stdout.String(), stderr.String())
	require.Contains(t, stderr.String(), "gitignore", "the ledger must offer a gitignore entry")
	id := strings.TrimSpace(stdout.String())
	require.True(t, strings.HasPrefix(id, "ledger/"), "create must print a ledger id, got %q", stdout.String())
	matches, err := filepath.Glob(filepath.Join(probeOut["LEDGER"], "*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "the first operation must write a ledger line")
	raw, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"op":"create"`)
	require.Contains(t, string(raw), `"title":"first"`)
}

func parseKV(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

// TestLedgerReplayParity runs one sequence through the production adapter on
// the ledger and on Pasture, then compares the equivalence projection.
func TestLedgerReplayParity(t *testing.T) {
	root := testModuleRoot(t)
	bin := filepath.Join(t.TempDir(), "pasture")
	build := exec.Command("go", "build", "-o", bin, "./cmd/pasture")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pasture: %v\n%s", err, output)
	}

	ns := "https://example.test/parity"
	pastureDir := t.TempDir()
	db := filepath.Join(pastureDir, "pasture.db")
	author := registerParityAuthor(t, bin, db, ns)
	pastureHeader := writeEpoch(t, pastureDir, "pasture", ns, author, db, "")
	ledgerDir := t.TempDir()
	ledgerPath := filepath.Join(ledgerDir, "ledger")
	ledgerHeader := writeEpoch(t, ledgerDir, "ledger", ns, author, "", ledgerPath)

	sequence := [][]string{
		{"create", "--kind", "task", "--title", "alpha", "--phase", "request", "--label", "L1", "--body", "hello"},
		{"create", "--kind", "task", "--title", "beta", "--phase", "request", "--body", "other"},
	}
	pastureIDs := runSequence(t, pastureHeader, bin, db, sequence)
	ledgerIDs := runSequence(t, ledgerHeader, "", "", sequence)
	require.Len(t, pastureIDs, 2)
	require.Len(t, ledgerIDs, 2)
	runOne(t, pastureHeader, bin, db, "label", pastureIDs[0], "L2")
	runOne(t, ledgerHeader, "", "", "label", ledgerIDs[0], "L2")
	runOne(t, pastureHeader, bin, db, "block", pastureIDs[0], "--by", pastureIDs[1])
	runOne(t, ledgerHeader, "", "", "block", ledgerIDs[0], "--by", ledgerIDs[1])
	runOne(t, pastureHeader, bin, db, "note", pastureIDs[0], "--author", author, "--body", "a note")
	runOne(t, ledgerHeader, "", "", "note", ledgerIDs[0], "--author", author, "--body", "a note")
	runOne(t, pastureHeader, bin, db, "status", pastureIDs[1], "--set", "in_progress")
	runOne(t, ledgerHeader, "", "", "status", ledgerIDs[1], "--set", "in_progress")
	runOne(t, pastureHeader, bin, db, "close", pastureIDs[1], "--reason", "done")
	runOne(t, ledgerHeader, "", "", "close", ledgerIDs[1], "--reason", "done")

	pastureProj := projectPasture(t, bin, db, ns)
	ledgerProj := projectLedger(t, ledgerPath)
	requireEquivalent(t, pastureProj, ledgerProj)

	other := writeEpochFile(t, filepath.Join(ledgerDir, "EPOCH-other.md"), "ledger", "https://example.test/other", author, "", ledgerPath)
	runOne(t, other, "", "", "create", "--kind", "task", "--title", "otherns", "--phase", "request", "--body", "x")
	scoped := ledgerQueryTitles(t, ledgerHeader)
	require.NotContains(t, scoped, "otherns")
	all := ledgerQueryTitles(t, ledgerHeader, "--all-namespaces")
	require.Contains(t, all, "otherns")
}

func writeEpoch(t *testing.T, dir, backend, ns, author, db, ledger string) string {
	t.Helper()
	return writeEpochFile(t, filepath.Join(dir, "EPOCH.md"), backend, ns, author, db, ledger)
}

func writeEpochFile(t *testing.T, header, backend, ns, author, db, ledger string) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "backend: %s\nnamespace: %s\nauthor: %s\n", backend, ns, author)
	if db != "" {
		fmt.Fprintf(&b, "db: %s\n", db)
	}
	if ledger != "" {
		fmt.Fprintf(&b, "ledger: %s\n", ledger)
	}
	require.NoError(t, os.WriteFile(header, []byte(b.String()), 0o600))
	return header
}

func registerParityAuthor(t *testing.T, bin, db, ns string) string {
	t.Helper()
	cmd := exec.Command(bin, "--db", db, "--format", "json", "--namespace", ns, "task", "agents", "register", "software", "--name", "rec-parity", "--version", "0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "register author: %s", out)
	var doc struct {
		AgentID string `json:"agentId"`
	}
	require.NoError(t, json.Unmarshal(out, &doc), "register output: %s", out)
	require.NotEmpty(t, doc.AgentID, "register output: %s", out)
	return doc.AgentID
}

func runSequence(t *testing.T, header, bin, db string, steps [][]string) []string {
	t.Helper()
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		out := runOne(t, header, bin, db, step...)
		if step[0] == "create" {
			ids = append(ids, strings.TrimSpace(out))
		}
	}
	return ids
}

func runOne(t *testing.T, header, bin, db string, args ...string) string {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", TrackerRecScript, "rec"}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "EPOCH_FILE=" + header}
	if bin != "" {
		cmd.Env = append(cmd.Env, "PASTURE_BIN="+bin, "PASTURE_DB_PATH="+db)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoErrorf(t, err, "rec %v\nstdout: %s\nstderr: %s", args, stdout.String(), stderr.String())
	return stdout.String()
}

type parityTask struct {
	ID       string
	Title    string
	Status   string
	Labels   []string
	Edges    []string
	Comments int
}

func projectPasture(t *testing.T, bin, db, ns string) []parityTask {
	t.Helper()
	raw := runPasture(t, bin, db, "--format", "json", "--namespace", ns, "task", "list")
	var listed []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &listed), raw)
	out := make([]parityTask, 0, len(listed))
	for _, item := range listed {
		labelsRaw := runPasture(t, bin, db, "--format", "json", "task", "label", "list", item.ID)
		var labels struct {
			Labels []string `json:"labels"`
		}
		require.NoError(t, json.Unmarshal([]byte(labelsRaw), &labels), labelsRaw)
		commentsRaw := runPasture(t, bin, db, "--format", "json", "task", "comments", item.ID)
		var comments []json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(commentsRaw), &comments), commentsRaw)
		treeRaw := runPasture(t, bin, db, "--format", "json", "task", "dep", "tree", item.ID, "--kind", "all")
		var tree struct {
			Edges []struct {
				TargetID string `json:"targetId"`
				Kind     string `json:"kind"`
			} `json:"edges"`
		}
		require.NoError(t, json.Unmarshal([]byte(treeRaw), &tree), treeRaw)
		edges := make([]string, 0, len(tree.Edges))
		for _, edge := range tree.Edges {
			edges = append(edges, edge.Kind+">"+edge.TargetID)
		}
		sort.Strings(labels.Labels)
		sort.Strings(edges)
		out = append(out, parityTask{
			ID: item.ID, Title: item.Title, Status: item.Status,
			Labels: labels.Labels, Edges: edges, Comments: len(comments),
		})
	}
	return out
}

func runPasture(t *testing.T, bin, db string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--db", db}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "pasture %v: %s", args, out)
	return string(out)
}

func projectLedger(t *testing.T, dir string) []parityTask {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	require.NoError(t, err)
	byID := map[string]*parityTask{}
	order := []string{}
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			var row map[string]string
			require.NoError(t, json.Unmarshal([]byte(line), &row), line)
			id := row["id"]
			task, ok := byID[id]
			if !ok {
				task = &parityTask{ID: id}
				byID[id] = task
				order = append(order, id)
			}
			switch row["op"] {
			case "create":
				task.Title = row["title"]
				if task.Status == "" {
					task.Status = row["status"]
				}
			case "status", "close":
				if row["status"] != "" {
					task.Status = row["status"]
				}
			case "label":
				if row["label"] != "" {
					task.Labels = append(task.Labels, row["label"])
				}
			case "block":
				task.Edges = append(task.Edges, row["edge_kind"]+">"+row["child"])
			case "note":
				task.Comments++
			}
		}
	}
	out := make([]parityTask, 0, len(order))
	for _, id := range order {
		task := *byID[id]
		sort.Strings(task.Labels)
		sort.Strings(task.Edges)
		out = append(out, task)
	}
	return out
}

func requireEquivalent(t *testing.T, pasture, ledger []parityTask) {
	t.Helper()
	require.Equal(t, len(pasture), len(ledger))
	byTitle := map[string]parityTask{}
	ids := map[string]string{}
	for _, task := range pasture {
		require.NotEmpty(t, task.ID)
		byTitle[task.Title] = task
		ids[task.ID] = task.Title
	}
	for _, task := range ledger {
		require.NotEmpty(t, task.ID)
		want, ok := byTitle[task.Title]
		require.Truef(t, ok, "ledger task %q has no pasture peer", task.Title)
		require.Equal(t, want.Status, task.Status, task.Title)
		require.Equal(t, nonempty(want.Labels), nonempty(task.Labels), task.Title)
		require.Equal(t, want.Comments, task.Comments, task.Title)
		wantEdges := make([]string, len(want.Edges))
		for i, edge := range want.Edges {
			kind, target, _ := strings.Cut(edge, ">")
			wantEdges[i] = kind + ">" + ids[target]
		}
		gotEdges := make([]string, len(task.Edges))
		ledgerTitle := map[string]string{}
		for _, peer := range ledger {
			ledgerTitle[peer.ID] = peer.Title
		}
		for i, edge := range task.Edges {
			kind, target, _ := strings.Cut(edge, ">")
			gotEdges[i] = kind + ">" + ledgerTitle[target]
		}
		sort.Strings(wantEdges)
		sort.Strings(gotEdges)
		require.Equal(t, nonempty(wantEdges), nonempty(gotEdges), task.Title)
	}
}

func nonempty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func ledgerQueryTitles(t *testing.T, header string, extra ...string) []string {
	t.Helper()
	args := append([]string{"query", "--format", "text"}, extra...)
	out := runOne(t, header, "", "", args...)
	var titles []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 2 {
			titles = append(titles, fields[1])
		}
	}
	return titles
}

// countUnregisteredTrackerCalls counts entry-point occurrences whose next
// token is not a registered neutral op. A placeholder or a prose continuation
// (no leading word) is not a call and is skipped.
func countUnregisteredTrackerCalls(text, entryPoint string, callOp *regexp.Regexp) int {
	bad := 0
	rest := text
	for {
		idx := strings.Index(rest, entryPoint)
		if idx < 0 {
			return bad
		}
		rest = rest[idx+len(entryPoint):]
		match := callOp.FindStringSubmatch(rest)
		if match == nil {
			continue
		}
		if !TrackerOpId(match[1]).isRegistered() {
			bad++
		}
	}
}
