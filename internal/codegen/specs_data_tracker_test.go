package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// binary and proves every neutral op lowers to its backend recipe. It exercises
// the production constant, not a hand-written imitation.
func TestRecAssetServesTheTenOps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"create", []string{"create", "--kind", "task", "--title", "T", "--phase", "request", "--body", "B"}, "task create T --type task --phase request --namespace https://example.test/repo --description B"},
		{"label", []string{"label", "ID", "pasture:urd"}, "task label add ID pasture:urd"},
		{"block", []string{"block", "P", "--by", "C"}, "task dep add P --blocked-by C"},
		{"block-typed", []string{"block", "P", "--by", "C", "--kind", "supersedes"}, "task dep add P --target C --kind supersedes"},
		{"note", []string{"note", "ID", "--author", "actor/x", "--body", "hello"}, "task comment add ID --author actor/x -- hello"},
		{"show", []string{"show", "ID"}, "task show ID"},
		{"show-notes", []string{"show", "ID", "--notes"}, "task comments ID"},
		{"show-history", []string{"show", "ID", "--history"}, "task timeline ID"},
		{"query", []string{"query", "--label", "pasture:urd", "--status", "open"}, "task list --namespace https://example.test/repo --status open --label pasture:urd"},
		{"query-blocked", []string{"query", "--blocked", "--label", "x"}, "task blocked --label x"},
		{"query-tree", []string{"query", "--tree", "ID"}, "task dep tree ID"},
		{"ready", []string{"ready", "--label", "x"}, "task ready --label x"},
		{"status", []string{"status", "ID", "--set", "in_progress"}, "task update ID --status in_progress"},
		{"close", []string{"close", "ID", "--reason", "done"}, "task close ID --reason done"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			bin, log := writeFakePasture(t, dir)
			header := filepath.Join(dir, "EPOCH.md")
			require.NoError(t, os.WriteFile(header, []byte("backend: pasture\ndb: "+filepath.Join(dir, "store.db")+"\nnamespace: https://example.test/repo\nauthor: actor/x\nrec: "+header+"\n"), 0o600))
			out, code := runRec(t, dir, map[string]string{
				"HOME":            dir,
				"PASTURE_BIN":     bin,
				"PASTURE_DB_PATH": filepath.Join(dir, "store.db"),
				"EPOCH_FILE":      header,
				"PROBE_FAKE_LOG":  log,
			}, tc.args...)
			require.Zero(t, code, out)
			invocations, err := os.ReadFile(log)
			require.NoError(t, err)
			require.Contains(t, string(invocations), tc.want)
		})
	}
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
