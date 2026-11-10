package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
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

	cmd := exec.Command("sh", "-c", TrackerProbeScript)
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
func writeFakePasture(t *testing.T, dir string) (binPath, logPath string) {
	t.Helper()

	binPath = filepath.Join(dir, "pasture")
	logPath = filepath.Join(dir, "pasture.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$PROBE_FAKE_LOG"
mode="${PROBE_FAKE_MODE:-ok}"
case "$1" in
  --version) printf 'pasture version v9.9.9\n'; exit 0 ;;
  --help)
    [ "$mode" = "help-failed" ] && exit 1
    exit 0 ;;
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
  exit 0
fi
exit 0
`
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))
	return binPath, logPath
}
