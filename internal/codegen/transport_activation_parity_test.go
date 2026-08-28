package codegen_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTransportWiresOnlyActivatedEvents asserts, for every harness Pasture
// generates, that the committed transport artifact wires EXACTLY the events the
// harness activation manifest marks as enabled.
//
// The transport is the only path by which a host can reach the lifecycle
// handler. A wired event that activation withholds is a host-visible defect: the
// host spawns a process for each occurrence, the handler refuses the event, and
// the host gets a refusal diagnostic instead of a decision. The wiring also
// contradicts the harness activation report, which records the same event as
// withheld. A withheld event that is never wired can never reach the handler.
//
// The comparison uses the committed artifacts, not the emitters, so the test
// fails on a stale regeneration as well as on an emitter defect.
func TestTransportWiresOnlyActivatedEvents(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	t.Run("claude-code", func(t *testing.T) {
		t.Parallel()
		enabled := enabledEventsFromActivationReport(t, filepath.Join(root, "hooks", "pasture-activation.json"))
		wired := claudeWiredLifecycleEvents(t, filepath.Join(root, "hooks", "hooks.json"))
		requireSameEvents(t, "hooks/hooks.json", enabled, wired)
	})

	t.Run("codex", func(t *testing.T) {
		t.Parallel()
		enabled := enabledEventsFromActivationReport(t, filepath.Join(root, ".codex", "pasture-codex-activation.json"))
		wired := codexWiredLifecycleEvents(t, filepath.Join(root, ".codex", "hooks.json"))
		requireSameEvents(t, ".codex/hooks.json", enabled, wired)

		runners := codexEventRunnerNames(t, filepath.Join(root, ".codex", "hooks", "events"))
		requireSameEvents(t, ".codex/hooks/events", enabled, runners)
	})

	t.Run("opencode", func(t *testing.T) {
		t.Parallel()
		enabled := enabledEventsFromOpenCodeManifest(t, filepath.Join(root, ".opencode", "pasture-opencode.json"))
		wired := openCodeWiredLifecycleEvents(t, filepath.Join(root, ".opencode", "plugins", "pasture-lifecycle.ts"))
		requireSameEvents(t, ".opencode/plugins/pasture-lifecycle.ts", enabled, wired)
	})
}

// activationReportFile mirrors the committed Claude and Codex activation audit
// reports. Only the fields this parity check reads are declared.
type activationReportFile struct {
	Events []struct {
		Event string `json:"event"`
		State string `json:"state"`
	} `json:"events"`
}

// openCodeTargetManifestFile mirrors the committed OpenCode target manifest.
// Its activation array carries the same event/state shape as the two report
// files, nested one level down.
type openCodeTargetManifestFile struct {
	Activation []struct {
		Event string `json:"event"`
		State string `json:"state"`
	} `json:"activation"`
}

func enabledEventsFromActivationReport(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var report activationReportFile
	readGeneratedJSON(t, path, &report)
	enabled := make(map[string]struct{})
	for _, entry := range report.Events {
		if entry.State == "enabled" {
			enabled[entry.Event] = struct{}{}
		}
	}
	require.NotEmpty(t, enabled,
		"activation report %q enables no event; a harness with an empty enabled set cannot be checked for transport parity — regenerate the report or remove the harness from this test", path)
	return enabled
}

func enabledEventsFromOpenCodeManifest(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var manifest openCodeTargetManifestFile
	readGeneratedJSON(t, path, &manifest)
	enabled := make(map[string]struct{})
	for _, entry := range manifest.Activation {
		if entry.State == "enabled" {
			enabled[entry.Event] = struct{}{}
		}
	}
	require.NotEmpty(t, enabled,
		"OpenCode target manifest %q enables no event; regenerate the manifest before running the transport parity check", path)
	return enabled
}

// claudeHooksFile mirrors the committed Claude hooks configuration. Claude
// groups carry non-lifecycle commands too (git discipline, task priming), so the
// parity check counts only groups that invoke `pasture hook lifecycle`.
type claudeHooksFile struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func claudeWiredLifecycleEvents(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var config claudeHooksFile
	readGeneratedJSON(t, path, &config)
	wired := make(map[string]struct{})
	for event, groups := range config.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if lifecycleCommand.MatchString(hook.Command) {
					wired[event] = struct{}{}
				}
			}
		}
	}
	return wired
}

// codexHooksFile mirrors the committed Codex host hook configuration.
type codexHooksFile struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func codexWiredLifecycleEvents(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var config codexHooksFile
	readGeneratedJSON(t, path, &config)
	wired := make(map[string]struct{})
	for event, groups := range config.Hooks {
		require.NotEmpty(t, groups,
			"%s: event %q has an empty hook group; every wired Codex event must invoke exactly one runner", path, event)
		wired[event] = struct{}{}
	}
	return wired
}

func codexEventRunnerNames(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoErrorf(t, err,
		"read Codex runner directory %q: %v — the generated Codex hooks package must contain one runner per enabled event; run `make generate`", dir, err)
	runners := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sh" {
			continue
		}
		runners[entry.Name()[:len(entry.Name())-len(".sh")]] = struct{}{}
	}
	return runners
}

// lifecycleEventFlag captures the native event name a generated OpenCode
// handler passes to `pasture hook lifecycle`. It is applied ONLY to
// comment-stripped source, so prose can never contribute an event name.
var lifecycleEventFlag = regexp.MustCompile(`"--event",\s*"([^"]+)"`)

// lifecycleCommand recognizes a command string that invokes the Pasture
// lifecycle handler, in either the Claude or the Codex spelling.
var lifecycleCommand = regexp.MustCompile(`hook lifecycle`)

// openCodePluginExport is the exported plugin factory the OpenCode host loads.
// Only the handlers inside its returned object are registered with the host.
const openCodePluginExport = "export const PastureLifecycle"

// openCodeCatchAllHandler is the single OpenCode handler that receives the whole
// server-sent observation stream. It is not one native event: it dispatches on
// the event type, so its registered event set comes from its dispatch guards.
const openCodeCatchAllHandler = "event"

// openCodeDispatchGuard captures the native event name each catch-all dispatch
// guard selects, e.g. `if (callback.event?.type !== "session.created") return;`.
var openCodeDispatchGuard = regexp.MustCompile(`callback\.event\?\.type\s*!==\s*"([^"]+)"`)

// openCodeHandlerKey captures the leading property key of one object-literal
// member: an optional `async`, then a quoted or bare key, then its parameter
// list. A named-output handler's key IS the native event name; the catch-all
// handler's key is openCodeCatchAllHandler.
var openCodeHandlerKey = regexp.MustCompile(`^\s*(?:async\s+)?(?:"([^"]+)"|'([^']+)'|([A-Za-z_$][A-Za-z0-9_$]*))\s*\(`)

// openCodeWiredLifecycleEvents returns the events the generated OpenCode plugin
// actually REGISTERS with the host.
//
// The set is derived from the structure of the exported plugin object, not from
// free text. Only a handler inside that object can ever be invoked by the host,
// so a handler function that is emitted but not registered is dead code, and an
// event named only in a comment is not wired at all. A free-text scan cannot
// tell either case apart from real wiring, so it would pass a plugin that
// silently observes nothing.
//
// The check has two parts. First it parses the registered set. Then it requires
// the registered set to equal the set of events the file emits to
// `pasture hook lifecycle`, so an emitted-but-unregistered handler and a
// registered-but-silent handler both fail here with a precise message.
func openCodeWiredLifecycleEvents(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err,
		"read generated OpenCode plugin %q: %v — run `make generate` to restore the committed transport artifact", path, err)
	source := stripScriptComments(string(raw))

	registered := openCodeRegisteredEvents(t, path, source)
	emitted := make(map[string]struct{})
	for _, match := range lifecycleEventFlag.FindAllStringSubmatch(source, -1) {
		emitted[match[1]] = struct{}{}
	}
	requireEqualEventSets(t,
		fmt.Sprintf("%s: handlers registered in %s", path, openCodePluginExport),
		"registered", registered,
		fmt.Sprintf("%s: events emitted to `pasture hook lifecycle`", path),
		"emitted", emitted,
		"An emitted handler that no registered handler reaches is dead code: the host never calls it, so the event never reaches the lifecycle handler. A registered handler that emits nothing observes nothing. Fix the OpenCode plugin emitter and run `make generate`.")
	return registered
}

// openCodeRegisteredEvents parses the object literal the exported plugin factory
// returns and reports one native event name per registered handler. The
// catch-all handler contributes the event names of its dispatch guards; every
// other handler key IS a native event name.
func openCodeRegisteredEvents(t *testing.T, path, source string) map[string]struct{} {
	t.Helper()
	body := openCodePluginObjectBody(t, path, source)
	registered := make(map[string]struct{})
	add := func(event, origin string) {
		_, duplicate := registered[event]
		require.Falsef(t, duplicate,
			"%s registers event %q twice (%s); two handlers for one event double every lifecycle invocation — emit exactly one handler per event and run `make generate`",
			path, event, origin)
		registered[event] = struct{}{}
	}
	for _, member := range splitTopLevelMembers(body) {
		match := openCodeHandlerKey.FindStringSubmatch(member)
		require.NotNilf(t, match,
			"%s: cannot read a handler key from the member %q of the %s object; the parity check derives the wired set from that object's structure, so an unrecognized member shape must fail rather than be skipped — update this parser together with the OpenCode plugin emitter",
			path, strings.TrimSpace(member), openCodePluginExport)
		key := match[1] + match[2] + match[3]
		if key != openCodeCatchAllHandler {
			add(key, "as a named-output handler")
			continue
		}
		guards := openCodeDispatchGuard.FindAllStringSubmatch(member, -1)
		require.NotEmptyf(t, guards,
			"%s: the %q handler of %s dispatches on no event type; a catch-all handler with no guard either observes nothing or forwards every native event — fix the OpenCode plugin emitter and run `make generate`",
			path, openCodeCatchAllHandler, openCodePluginExport)
		for _, guard := range guards {
			add(guard[1], "in the catch-all dispatch")
		}
	}
	require.NotEmptyf(t, registered,
		"%s registers no lifecycle handler in %s; the plugin would load and observe nothing — run `make generate`",
		path, openCodePluginExport)
	return registered
}

// openCodePluginObjectBody returns the text between the braces of the object
// literal the exported plugin factory returns. It fails with an actionable
// message when the export or the literal is absent, so a renamed or restructured
// export can never be read as "no handler is registered".
func openCodePluginObjectBody(t *testing.T, path, source string) string {
	t.Helper()
	start := strings.Index(source, openCodePluginExport)
	require.GreaterOrEqualf(t, start, 0,
		"%s does not export %s; the OpenCode host loads that export, so a plugin without it registers no handler at all — run `make generate`",
		path, openCodePluginExport)
	arrow := strings.Index(source[start:], "=> ({")
	require.GreaterOrEqualf(t, arrow, 0,
		"%s: %s is not an arrow factory returning an object literal; this parity check reads the registered handler set from that literal — update this parser together with the OpenCode plugin emitter",
		path, openCodePluginExport)
	open := start + arrow + len("=> (")
	end := matchBrace(source, open)
	require.GreaterOrEqualf(t, end, 0,
		"%s: the object literal returned by %s has no matching closing brace; the committed artifact is malformed — run `make generate`",
		path, openCodePluginExport)
	return source[open+1 : end]
}

// matchBrace returns the index of the brace that closes the one at open, or -1
// when the source ends first. It skips braces inside string literals.
func matchBrace(source string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(source); i++ {
		c := source[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevelMembers splits an object-literal body into its members, cutting
// only on commas that sit at the literal's own nesting depth and outside every
// string. Blank members (a trailing comma) are dropped.
func splitTopLevelMembers(body string) []string {
	var members []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(body); i++ {
		c := body[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			depth--
		case ',':
			if depth == 0 {
				members = append(members, body[start:i])
				start = i + 1
			}
		}
	}
	members = append(members, body[start:])
	kept := members[:0]
	for _, member := range members {
		if strings.TrimSpace(member) != "" {
			kept = append(kept, member)
		}
	}
	return kept
}

// stripScriptComments blanks every line and block comment while preserving the
// source length, so prose can never satisfy a structural or textual match. The
// generated OpenCode plugin contains no regular-expression literal, so a `/` is
// always either division or a comment opener.
func stripScriptComments(source string) string {
	out := []byte(source)
	var quote byte
	for i := 0; i < len(out); i++ {
		c := out[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			for ; i < len(out) && out[i] != '\n'; i++ {
				out[i] = ' '
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			for ; i < len(out); i++ {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
		}
	}
	return string(out)
}

// requireEqualEventSets compares two derived event sets and names both sides in
// the failure, so the reader learns which artifact to change.
func requireEqualEventSets(t *testing.T, leftLabel, leftRole string, left map[string]struct{}, rightLabel, rightRole string, right map[string]struct{}, fix string) {
	t.Helper()
	var onlyLeft, onlyRight []string
	for event := range left {
		if _, ok := right[event]; !ok {
			onlyLeft = append(onlyLeft, event)
		}
	}
	for event := range right {
		if _, ok := left[event]; !ok {
			onlyRight = append(onlyRight, event)
		}
	}
	sort.Strings(onlyLeft)
	sort.Strings(onlyRight)
	require.Emptyf(t, onlyLeft, "%s: %v is %s but not %s (%s). %s", leftLabel, onlyLeft, leftRole, rightRole, rightLabel, fix)
	require.Emptyf(t, onlyRight, "%s: %v is %s but not %s (%s). %s", rightLabel, onlyRight, rightRole, leftRole, leftLabel, fix)
}

func readGeneratedJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err,
		"read generated artifact %q: %v — run `make generate` to restore the committed output", path, err)
	require.NoErrorf(t, json.Unmarshal(raw, target),
		"parse generated artifact %q as JSON — the committed artifact is malformed; run `make generate`", path)
}

func requireSameEvents(t *testing.T, artifact string, enabled, wired map[string]struct{}) {
	t.Helper()
	var extra, missing []string
	for event := range wired {
		if _, ok := enabled[event]; !ok {
			extra = append(extra, event)
		}
	}
	for event := range enabled {
		if _, ok := wired[event]; !ok {
			missing = append(missing, event)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	require.Emptyf(t, extra,
		"%s wires %v, which the activation manifest withholds. The host would spawn a process for each occurrence and get a refusal from the lifecycle handler instead of a decision, and the transport would contradict the activation audit report. Filter the emitter on the activation manifest and run `make generate`.",
		artifact, extra)
	require.Emptyf(t, missing,
		"%s does not wire %v, which the activation manifest enables. An enabled event with no transport entry never reaches the handler. Regenerate with `make generate`.",
		artifact, missing)
}
