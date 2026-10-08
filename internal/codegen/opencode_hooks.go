package codegen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// OpenCodeHooksModulePath is the canonical generated path of the OpenCode
// lifecycle plugin. OpenCode loads project plugins from .opencode/plugins in
// deterministic configuration order.
const OpenCodeHooksModulePath = ".opencode/plugins/pasture-lifecycle.ts"

// openCodeV2ThrowingHook is the one 2.0.20 hook whose callback may throw. The
// host failure model types exactly one failure channel: only tool
// execute.before may fail (packages/core/src/plugin/hooks.ts at the 2.0.20
// tag: "Only tool execute.before may fail"). The promise adapter runs the
// callback inside Effect.promise (packages/plugin/src/promise/adapter.ts at
// the 2.0.20 tag), so a throw stops the call through the Effect defect path
// rather than the typed Tool.Error failure the host types for its own
// rejections. Every other hook's failure channel is never, so a throw there
// is a host-flow defect rather than a refusal, and those callbacks report
// and continue. A named row added later defaults to report-and-continue here
// until the host types a channel for it.
const openCodeV2ThrowingHook = "tool.execute.before"

// openCodeV2DenyHook is the one 2.0.20 hook that enforces a pasture Denial
// through a host-effect mutation instead of a throw. The host's permission
// assertion carries the operation under test as action plus resources: a
// tool-sourced assertion names the tool operation (for example "edit" from
// packages/core/src/tool/plugin/edit.ts, or "shell" from
// packages/core/src/tool/plugin/shell.ts, which carries each parsed command
// text in resources[]) with the target paths or command resources in
// resources[], and source { type: "tool", messageID, id }. The
// plugin forwards that assertion verbatim to the gate and enforces the
// returned decision only: on a Denial it assigns hookEvent.effect = "deny"
// and hookEvent.message to the durable reason, and on any other answer it
// leaves the host evaluation untouched. The assignment reaches the caller
// because the host passes this same object through
// hooks.trigger("permission", "evaluate", ...) and returns
// { effect: event.effect, message: event.message, rules } from evaluateInput
// (packages/core/src/permission.ts at the 2.0.20 tag); the evaluation type
// declares effect and message mutable while sessionID stays readonly
// (packages/plugin/src/promise/permission.ts at the 2.0.20 tag). The hook's
// failure channel is never (the promise Hooks type defaults it), so a throw
// here would be a host-flow defect rather than a refusal: the callback never
// throws, it only assigns. Where a saved or configured host rule already
// denied, evaluateInput returns early without firing the hook, so this
// mutation runs only on the not-already-denied path.
const openCodeV2DenyHook = "permission.evaluate"

// deriveOpenCodeNativeToolNames returns, sorted and de-duplicated, exactly the
// native tool names the OpenCode runtime contract declares — and
// nothing else. It resolves every core orchestration operation against the
// contract via the delivered runtime lookup and collects only the operations the
// contract classifies as native. Semantic-instruction, parent-mediated, and
// unsupported operations contribute no tool name, so a generated artifact can
// never reference an invented OpenCode tool: the allow-list is the contract's
// own declared surface, computed here rather than hand-copied.
func deriveOpenCodeNativeToolNames() ([]string, error) {
	contract := runtime.OpenCode2_0_20()
	seen := make(map[string]struct{})
	var names []string
	for _, kind := range ir.AllOperationKinds() {
		descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
		if !ok {
			// A core operation kind with no shared descriptor would mean the
			// delivered runtime descriptor table drifted from the operation
			// vocabulary; fail closed rather than silently narrow the allow-list.
			return nil, fmt.Errorf(
				"codegen.deriveOpenCodeNativeToolNames: core operation %q has no shared descriptor in the delivered runtime table — "+
					"the runtime descriptor set drifted from ir.AllOperationKinds; regenerate against a runtime revision whose CoreOperationDescriptorFor covers every core kind",
				kind)
		}
		binding, err := runtime.LookupOperationBinding(contract, descriptor)
		if err != nil {
			// Unsupported operations (for OpenCode at the recorded version, stopping an
			// assignment) return a lookup error by contract; they are correctly
			// absent from a native allow-list, so skip rather than fail.
			continue
		}
		call, isNative := binding.Native()
		if !isNative {
			continue
		}
		name := call.CallName()
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// GenerateOpenCodeHooksModule returns the deterministic self-contained Bun
// plugin for the pinned OpenCode lifecycle contract. Lifecycle commands are
// statically bound here; the foreign adapter only serializes callback values and
// mechanically validates the canonical response returned by Pasture.
func GenerateOpenCodeHooksModule() (string, error) {
	activationEntries, err := openCodeActivationEntries()
	if err != nil {
		return "", fmt.Errorf("codegen.GenerateOpenCodeHooksModule: activation: %w", err)
	}
	surfaces := make(map[string]runtime.HookSurface)
	contract := runtime.OpenCode2_0_20Lifecycle()
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			return "", err
		}
		surfaces[mapping.NativeName()] = mapping.Surface()
	}
	return generateOpenCodeHooksModule(registration.OpenCode2_0_20().Entries(), activationEntries, surfaces)
}

// generateOpenCodeHooksModule consumes the validated activation result from the
// public generator. Constructed inputs exercise emission mechanics only; this
// function neither grants activation nor evaluates capture evidence.
//
// The emitted plugin registers the whole manifest surface rather than the
// enabled subset: admission is enforced by the lifecycle handler, and the
// fail-open continuation bytes make an unadmitted row safe — the handler
// answers it with the host's continue bytes, exit 0, and a diagnostic, so the
// host continues unevaluated. Transport completeness is load-bearing before
// any row carries proofs: the capture sitting that proves a row needs the
// plugin to forward that row's hook, and an activation-filtered emitter would
// generate an empty plugin while every row awaits its capture. The activation
// join below still validates total — every manifest kind classified exactly
// once — so a drifted table fails generation loudly.
func generateOpenCodeHooksModule(manifest []registration.Event, activationEntries []activation.Entry, surfaces map[string]runtime.HookSurface) (string, error) {
	enabled := make(map[model.ContractEventKind]bool)
	for _, entry := range activationEntries {
		if _, duplicate := enabled[entry.Event]; duplicate {
			return "", fmt.Errorf("codegen.generateOpenCodeHooksModule: duplicate activation kind %d during emission; restore one classification per registration", entry.Event)
		}
		enabled[entry.Event] = entry.State == activation.Enabled
	}
	helpers, setup, err := openCodeCallbacks(manifest, enabled, surfaces)
	if err != nil {
		return "", err
	}
	toolNames, err := deriveOpenCodeNativeToolNames()
	if err != nil {
		return "", fmt.Errorf("codegen.GenerateOpenCodeHooksModule: derive native tool allow-list: %w", err)
	}
	metadata, err := lifecycleMetadata(
		runtime.OpenCode2_0_20Lifecycle(),
		openCodeHostVersion(),
		func(string) []string { return nil },
	)
	if err != nil {
		return "", fmt.Errorf("codegen.GenerateOpenCodeHooksModule: lifecycle metadata: %w", err)
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("codegen.GenerateOpenCodeHooksModule: marshal lifecycle metadata: %w", err)
	}

	quoted := make([]string, len(toolNames))
	for i, n := range toolNames {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	allowList := strings.Join(quoted, ", ")

	const module = `// Code generated by Pasture for OpenCode. DO NOT EDIT.
// Runtime contract: %s
// This plugin performs no runtime imports and requires only Bun plus the built pasture binary.

export const PASTURE_NATIVE_TOOLS = Object.freeze([%s]);
export const PASTURE_RUNTIME_CONTRACT = %q;
const METADATA = %s;
type LifecycleResponse =
  | { decision: "proceed" }
  | { decision: "deny"; reason: string };

const CHILD_WAIT_MS = 8000;
const CONFIGURATION_ADVICE = "; verify PASTURE_BIN and the generated OpenCode %s configuration";

async function forwardDiagnostic(stderr: string, event: string): Promise<void> {
  const sink = process.stderr;
  try {
    await new Promise<void>((resolve, reject) => {
      let settled = false;
      const finish = (error?: Error | null) => {
        if (settled) return;
        settled = true;
        // Writable calls its write callback BEFORE emitting the error event.
        // Keep the listener through that turn, then remove both listeners.
        setImmediate(() => {
          sink.off("error", onError);
          sink.off("close", onClose);
          if (error) reject(error);
          else resolve();
        });
      };
      const onError = (error: Error) => finish(error);
      const onClose = () => finish(new Error("standard error closed before the diagnostic was written"));
      sink.on("error", onError);
      sink.on("close", onClose);
      if (sink.destroyed || sink.closed || sink.writableEnded) {
        finish(new Error("standard error is not writable"));
        return;
      }
      try {
        // One write, no further chunks while backpressured. The callback means
        // the entire chunk was handled, even when write() returned false.
        sink.write(stderr, "utf8", finish);
      } catch (error) {
        finish(error instanceof Error ? error : new Error(String(error)));
      }
    });
  } catch (error) {
    throw new Error("pasture hook lifecycle diagnostic forwarding for " + event + " failed: " + error + "; restore the OpenCode standard-error sink and retry" + CONFIGURATION_ADVICE);
  }
}

async function drain(reader: ReadableStreamDefaultReader<Uint8Array>): Promise<string> {
  const decoder = new TextDecoder();
  let text = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) return text + decoder.decode();
    text += decoder.decode(value, { stream: true });
  }
}

// The running host reports its own release on the setup context as
// ctx.app.version (packages/plugin/src/app.ts: App.version, exposed on the
// promise Context at packages/plugin/src/promise/plugin.ts). setup captures
// it once below; every helper runs outside setup's scope, so the captured
// flags wait here. Empty means absent: no version is ever invented, and an
// absent version leaves each command unflagged for the binary to resolve.
// setup assigns on every run, so a setup without a usable version clears a
// previous capture instead of reusing it.
let pastureHostVersionArgs = [];

// A host self-report is carried only when it is release-shaped: an optional
// leading "v", a MAJOR.MINOR.PATCH triple, and optional suffix metadata. The
// running host reports "local" for source builds and "unknown" when its
// metadata is unconfigured, and neither is a release: forwarding one would
// bypass the version probe with a value the binary cannot route, silently
// selecting the oldest contract row for a newer host's payloads. This is a
// coarse pre-filter, not the authority — the binary still parses strictly and
// faults honestly on anything it cannot admit. Anything not release-shaped is
// cleared so the probe resolves.
function pastureReleaseShaped(value) {
  return typeof value === "string" && /^v?\d+\.\d+\.\d+(?:[-+].*)?$/.test(value.trim());
}

async function invokeLifecycle(command, event, value) {
  const binary = process.env.%s ?? "pasture";
  // Carry the setup-observed host version unless this invocation already
  // names its own (the session.created observation does, from its bus data).
  // Both sources are release-shape-gated where captured, so an unflagged
  // command means no usable report existed and the binary resolves the
  // version itself. A later host stays admissible: the binary admits its
  // recorded baseline and every later release, so forwarding the running
  // release never narrows it.
  const effective = command.includes("--host-version") || pastureHostVersionArgs.length === 0 ? command : [...command, ...pastureHostVersionArgs];
  const child = Bun.spawn({
    cmd: [binary, ...effective],
    stdin: new Blob([JSON.stringify(value)]),
    stdout: "pipe",
    stderr: "pipe",
  });
  const stdoutReader = child.stdout.getReader();
  const stderrReader = child.stderr.getReader();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let timedOut = false;
  let result: [string, string, number];
  try {
    // This outer bound includes BOTH pipe drains and exit. It sits above the
    // binary's 5s cancellation budget; it is not a measured universal host limit.
    // Killing the process can interrupt settlement after cancellation. This
    // outer bound cannot promise delivery of a committed response afterward.
    result = await Promise.race([
      Promise.all([drain(stdoutReader), drain(stderrReader), child.exited]),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          timedOut = true;
          child.kill("SIGKILL");
          reject(new Error("timed out after " + CHILD_WAIT_MS + " ms"));
        }, CHILD_WAIT_MS);
      }),
    ]);
  } catch (error) {
    if (!timedOut && child.exitCode === null) child.kill("SIGKILL");
    // Cancel pending reads as well: inherited pipes can remain open even after
    // the direct child exits. Always wait for the direct child to be reaped.
    await Promise.allSettled([stdoutReader.cancel(), stderrReader.cancel(), child.exited]);
    throw new Error("pasture hook lifecycle for " + event + " " + (error instanceof Error ? error.message : String(error)) + CONFIGURATION_ADVICE);
  } finally {
    clearTimeout(timer);
    stdoutReader.releaseLock();
    stderrReader.releaseLock();
  }
  const [stdout, stderr, exitCode] = result;
  if (exitCode !== 0) throw new Error("pasture hook lifecycle for " + event + " exited " + exitCode + ": " + (stderr.trim() || "no diagnostic was returned") + CONFIGURATION_ADVICE);
  // fd 2 is piped, not inherited. Preserve the child's diagnostic and the
  // existing terminal-newline policy without console.error decoration.
  // Bun.write(Bun.stderr, ...) stalled after a partial write in the generated
  // plugin's spawned-child/pipe context under Bun 1.3.13. Writable completion
  // is exercised in that same context; Bun's internal cause is not established.
  if (stderr.trim() !== "") {
    // A successful lifecycle decision must survive a host that has closed or
    // rejected its diagnostic stream. The child's stdout remains authoritative.
    await forwardDiagnostic(stderr.endsWith("\n") ? stderr : stderr + "\n", event).catch(() => {});
  }
  return stdout;
}

function parseResponse(stdout: string, event: string): LifecycleResponse | undefined {
  // An EMPTY body at exit 0 means pasture could not evaluate the event and let
  // this host continue. It is not a decision and it is not a fault of this
  // callback, so the callback continues and reports on the console. This belt
  // exists so that an OLD pasture binary, which emitted nothing on a fail-open
  // fault, still cannot abort the user's action.
  //
  // THE CONSOLE LINE SENDS THE OPERATOR TO STANDARD ERROR AND ONLY OFFERS THE
  // RECORD. It used to promise both unconditionally. Standard error is where
  // pasture reports every such fault, and it is also where it reports that it
  // could not write a record at all; the durable line is the thing that may be
  // missing, because a fault whose record cannot be placed or written leaves
  // none. The old wording sent an operator who had just lost a gate evaluation
  // to hunt for a file that is not there on exactly the routes it fires on.
  //
  // THE STREAM IT NAMES IS ATTEMPTED, NOT GUARANTEED. invokeLifecycle forwards
  // the child's diagnostic best effort: a healthy sink receives it, while a
  // sink the host has closed or that rejects the write neither fails the hook
  // nor delivers the bytes. For one round this line named standard error while
  // the spawn above piped fd 2 and dropped everything it caught on the exit-0
  // route, so the operator was sent to a stream this callback had emptied.
  // Evidence named to a reader who cannot reach it is the same defect as
  // evidence that does not exist; best-effort forwarding must therefore say so
  // rather than promise delivery.
  if (stdout.trim() === "") {
    console.error("Pasture did not evaluate " + event + " and returned no decision; the host continues unevaluated. Read the pasture diagnostic on standard error first: pasture writes every such fault there, including the case where it could not write a durable record, and this plugin forwards it there best effort — so it may be missing when the host has closed that stream. A line may also have been appended to lifecycle-faults.jsonl beside the pasture database, but a fault whose record could not be placed or written leaves none, and the diagnostic then quotes the path it tried.");
    return;
  }
  let response: unknown;
  try {
    response = JSON.parse(stdout);
  } catch (error) {
    throw new Error("pasture hook lifecycle response is not JSON for " + event + ": " + error + CONFIGURATION_ADVICE);
  }
  if (response !== null && typeof response === "object" && !Array.isArray(response)) {
    const record = response as Record<string, unknown>;
    const keys = Object.keys(record);
    if (keys.length === 1 && record.decision === "proceed") {
      return { decision: "proceed" };
    }
    if (keys.length === 2 && record.decision === "deny" &&
        typeof record.reason === "string" && record.reason.length > 0) {
      return { decision: "deny", reason: record.reason };
    }
  }
  throw new Error('pasture hook lifecycle response must be exactly {"decision":"proceed"} or {"decision":"deny","reason":<nonempty string>} for ' + event + CONFIGURATION_ADVICE);
}

%s
export default {
  id: "pasture-lifecycle",
  setup: async (ctx) => {
%s  },
};
`

	return fmt.Sprintf(
		module,
		metadata.Contract,
		allowList,
		metadata.Contract,
		string(metadataJSON),
		openCodeHostVersion(),
		adapterBinaryEnv,
		helpers,
		setup,
	), nil
}

// openCodeHookTarget splits one dotted 2.0.20 native coordinate into the
// plugin domain that owns it and the hook name registered through that
// domain: "session.prompt" registers through ctx.session.hook("prompt"), and
// "tool.execute.before" through ctx.tool.hook("execute.before"). The bus
// event session.created is not a hook coordinate and never reaches here.
func openCodeHookTarget(nativeName string) (domain, hook string, err error) {
	domain, hook, found := strings.Cut(nativeName, ".")
	if !found || domain == "" || hook == "" {
		return "", "", fmt.Errorf("codegen.openCodeHookTarget: native coordinate %q has no domain.hook shape during emission; use the dotted coordinate from the 2.0.20 plugin types", nativeName)
	}
	switch domain {
	case "session", "tool", "permission", "shell":
		return domain, hook, nil
	default:
		return "", "", fmt.Errorf("codegen.openCodeHookTarget: native coordinate %q names unknown plugin domain %q during emission; the 2.0.20 plugin exposes session, tool, permission and shell hook domains", nativeName, domain)
	}
}

// openCodeHelperName mints the exported helper identifier for one dotted
// native name: "session.model.request" becomes sessionModelRequest. It
// refuses malformed or colliding names rather than emit ambiguous JavaScript.
func openCodeHelperName(nativeName string, names map[string]bool) (string, error) {
	parts := strings.Split(nativeName, ".")
	for _, part := range parts {
		if part == "" || part[0] < 'a' || part[0] > 'z' || strings.IndexFunc(part, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') }) >= 0 {
			return "", fmt.Errorf("codegen.openCodeCallbacks: unsupported callback name %q during emission; use the source-proven dotted native name", nativeName)
		}
	}
	helper := parts[0]
	for _, part := range parts[1:] {
		helper += strings.ToUpper(part[:1]) + part[1:]
	}
	helper = strings.ReplaceAll(helper, "-", "_")
	if names[helper] || helper == "event" {
		return "", fmt.Errorf("codegen.openCodeCallbacks: callback %q collides with another generated key during emission; correct the native registration", nativeName)
	}
	names[helper] = true
	return helper, nil
}

func openCodeCallbacks(manifest []registration.Event, enabled map[model.ContractEventKind]bool, surfaces map[string]runtime.HookSurface) (string, string, error) {
	var helpers, setup strings.Builder
	seen := make(map[model.ContractEventKind]bool)
	names := make(map[string]bool)
	helperByKind := make(map[model.ContractEventKind]string)
	hookByKind := make(map[model.ContractEventKind][2]string)
	var observations []registration.Event
	for _, event := range manifest {
		if _, classified := enabled[event.Kind]; !classified || seen[event.Kind] {
			return "", "", fmt.Errorf("codegen.openCodeCallbacks: registration %q has missing or duplicate activation join during emission; restore one classification per kind", event.NativeName)
		}
		seen[event.Kind] = true
		helper, err := openCodeHelperName(event.NativeName, names)
		if err != nil {
			return "", "", err
		}
		helperByKind[event.Kind] = helper
		switch surfaces[event.NativeName] {
		case runtime.SurfaceOpenCodeCatchAllSSE:
			observations = append(observations, event)
		case runtime.SurfaceOpenCodeNamedOutput:
			if event.NativeName == "session.created" {
				return "", "", fmt.Errorf("codegen.openCodeCallbacks: %q rides the named surface but arrives on the event bus during emission; classify it as an observation", event.NativeName)
			}
			domain, hook, err := openCodeHookTarget(event.NativeName)
			if err != nil {
				return "", "", err
			}
			hookByKind[event.Kind] = [2]string{domain, hook}
		default:
			return "", "", fmt.Errorf("codegen.openCodeCallbacks: enabled callback %q has unsupported runtime surface %v during emission; supply its source-proven OpenCode surface before generation", event.NativeName, surfaces[event.NativeName])
		}
	}
	if len(seen) != len(enabled) {
		return "", "", fmt.Errorf("codegen.openCodeCallbacks: activation contains an unregistered kind during emission; regenerate matching activation and registration tables")
	}
	commandFor := func(nativeName string) string {
		return fmt.Sprintf(`["hook", "lifecycle", "--harness", "opencode", "--event", %q]`, nativeName)
	}
	for _, event := range manifest {
		helper := helperByKind[event.Kind]
		if _, isHook := hookByKind[event.Kind]; !isHook {
			continue
		}
		if event.NativeName == openCodeV2DenyHook {
			fmt.Fprintf(&helpers, `export async function %s(hookEvent) {
  try {
    const stdout = await invokeLifecycle(%s, %q, hookEvent);
    const response = parseResponse(stdout, %q);
    if (response?.decision === "deny") {
      // ENFORCED through the host's typed permission channel, on the path
      // where no saved or configured host rule already denied (that path
      // returns early without firing this hook). The host passes this same
      // evaluation object through its hook trigger and returns the mutated
      // effect and message to the permission caller, so assigning both here
      // refuses the guarded action with the durable reason the gate
      // recorded. The reason travels verbatim: it names the policy fact
      // (for example that the session actor is unknown, or that the actor
      // holds no active assignment), and this transport neither rewrites it
      // nor invents one. Any deny-shaped body is a policy refusal here, so
      // the binary must never answer a fault in that shape: its encoder
      // rejects every non-proceed response (see EncodeOpenCode in
      // internal/lifecycle/nativeresponse), and a fault travels the fault
      // continuation instead. The hook's failure channel is never, so a
      // throw would be a host-flow defect rather than a refusal: assign,
      // never throw.
      hookEvent.effect = "deny";
      hookEvent.message = response.reason;
    }
    // Proceed (and the empty-body unevaluated belt) is a decision, not a
    // mutation. Never write host-owned objects.
  } catch (error) {
    // No failure channel exists on this hook: report and continue without
    // touching the host evaluation.
    console.error("Pasture lifecycle gate consultation failed for %s: " + error);
  }
}

`, helper, commandFor(event.NativeName), event.NativeName, event.NativeName, event.NativeName)
			continue
		}
		if event.NativeName == openCodeV2ThrowingHook {
			fmt.Fprintf(&helpers, `export async function %s(hookEvent) {
  const stdout = await invokeLifecycle(%s, %q, hookEvent);
  const response = parseResponse(stdout, %q);
  // A deny response is a future capable-binary path, not the enforceable
  // deny: every 2.0.20 row derives CapabilityNone, so the shipped binary
  // downgrades denials to proceed with the unenforced reason before this
  // plugin ever sees one. The permission hook enforces through its own
  // typed channel instead, assigning the evaluation's effect and message.
  // Throwing here stops the call
  // because the host types this hook's failure channel — only tool
  // execute.before may fail — and the promise adapter runs the callback
  // inside Effect.promise (packages/plugin/src/promise/adapter.ts at the
  // 2.0.20 tag), so the stop travels the Effect defect path rather than the
  // typed Tool.Error failure the host types for its own rejections.
  if (response?.decision === "deny") throw new Error(response.reason);
  // Proceed (and the empty-body unevaluated belt) is a decision, not a
  // mutation. Never write host-owned objects.
}

`, helper, commandFor(event.NativeName), event.NativeName, event.NativeName)
			continue
		}
		fmt.Fprintf(&helpers, `export async function %s(hookEvent) {
  try {
    const stdout = await invokeLifecycle(%s, %q, hookEvent);
    const response = parseResponse(stdout, %q);
    if (response?.decision === "deny") {
      // UNENFORCED on this hook: its failure channel is never, so a throw
      // would be a host-flow defect rather than a refusal, and the
      // host-effect mutation that enforces a denial belongs to the
      // permission hook, not this one. Log the
      // reason the binary recorded and continue without touching host
      // objects; the receipt names the unenforced denial.
      console.error("Pasture returned a denial for %s that this transport does not enforce; the host continues. Reason: " + response.reason);
    }
    // Proceed (and the empty-body unevaluated belt) is a decision, not a
    // mutation. Never write host-owned objects.
  } catch (error) {
    // No failure channel exists on this hook: report and continue.
    console.error("Pasture lifecycle gate consultation failed for %s: " + error);
  }
}

`, helper, commandFor(event.NativeName), event.NativeName, event.NativeName, event.NativeName, event.NativeName)
	}
	for _, event := range observations {
		helper := helperByKind[event.Kind]
		versionSelection := ""
		if event.NativeName == "session.created" {
			versionSelection = `    // The 2.0.20 bus event wraps its payload in data: version rides
    // beside sessionID there (packages/schema/src/session-event.ts for the
    // Created data shape and packages/schema/src/event.ts for the durable
    // envelope, both at the 2.0.20 tag). Do not cache it for later callbacks
    // or change the original payload. A non-release bus version (a source
    // build reports "local") is not carried: the setup-observed version is
    // the fallback, and the probe resolves when neither is release-shaped.
    const busVersion = typeof busEvent?.data?.version === "string" ? busEvent.data.version.trim() : "";
    if (pastureReleaseShaped(busVersion)) command.push("--host-version", busVersion);
`
		}
		fmt.Fprintf(&helpers, `export async function %s(busEvent) {
  try {
    const command = %s;
%s    await invokeLifecycle(command, %q, busEvent);
  } catch (error) {
    // Observation is never a gate and cannot terminate the native event bus.
    console.error(%q + error);
  }
}

`, helper, commandFor(event.NativeName), versionSelection, event.NativeName, "Pasture lifecycle observation failed for "+event.NativeName+": ")
	}
	fmt.Fprintf(&setup, `    // The v2 plugin context carries the running host's own release as
    // ctx.app.version (packages/plugin/src/app.ts: App.version, exposed on
    // the promise Context at packages/plugin/src/promise/plugin.ts). Capture
    // it once and carry it on every lifecycle invocation, so no hook depends
    // on spawning the host for --version. The value is the host's
    // self-report, never an invented default: only a release-shaped report is
    // captured (a source build reports "local", which is not a release), and
    // otherwise the capture is cleared and the binary resolves the version
    // itself.
    const observedVersion = typeof ctx?.app?.version === "string" ? ctx.app.version.trim() : "";
    pastureHostVersionArgs = pastureReleaseShaped(observedVersion) ? ["--host-version", observedVersion] : [];
    const registrations = [];
`)
	for _, event := range manifest {
		hook, isHook := hookByKind[event.Kind]
		if !isHook {
			continue
		}
		fmt.Fprintf(&setup, "    registrations.push(await ctx.%s.hook(%q, %s));\n", hook[0], hook[1], helperByKind[event.Kind])
	}
	if len(observations) > 0 {
		var guards strings.Builder
		for i, event := range observations {
			keyword := "if"
			if i > 0 {
				keyword = "else if"
			}
			fmt.Fprintf(&guards, "          %s (busEvent !== null && typeof busEvent === \"object\" && busEvent.type === %q) {\n            await %s(busEvent);\n          }\n", keyword, event.NativeName, helperByKind[event.Kind])
		}
		fmt.Fprintf(&setup, `    // Observation rows arrive on the connected server's public event
    // stream, not through a hook: filter the subscription to the bus events
    // the lifecycle needs and ignore the rest.
    const controller = new AbortController();
    const subscription = (async () => {
      try {
        for await (const busEvent of ctx.event.subscribe({ signal: controller.signal })) {
%s        }
      } catch (error) {
        if (!controller.signal.aborted) console.error("Pasture lifecycle event subscription failed: " + error);
      }
    })();
`, guards.String())
	}
	fmt.Fprintf(&setup, `    return async () => {
`)
	if len(observations) > 0 {
		fmt.Fprintf(&setup, `      controller.abort();
      await subscription;
`)
	}
	fmt.Fprintf(&setup, `      for (const registration of registrations) await registration.dispose();
    };
`)
	return helpers.String(), setup.String(), nil
}

func openCodeEventByKind(kind model.ContractEventKind) (registration.Event, error) {
	for _, event := range registration.OpenCode2_0_20().Entries() {
		if event.Kind == kind {
			return event, nil
		}
	}
	return registration.Event{}, fmt.Errorf("codegen.openCodeEventByKind: generated OpenCode %s manifest lacks event kind %d; regenerate the host contract before activation", registration.OpenCode2_0_20().Version, kind)
}

func openCodeActivationEntries() ([]activation.Entry, error) {
	entries, err := activation.OpenCode2_0_20()
	if err != nil {
		return nil, err
	}
	manifest := registration.OpenCode2_0_20().Entries()
	if len(entries) != len(manifest) {
		return nil, fmt.Errorf("activation has %d entries but generated OpenCode manifest has %d; activation must exhaustively classify every generated event", len(entries), len(manifest))
	}
	for index, entry := range entries {
		if !entry.IsValid() || entry.Event != manifest[index].Kind {
			return nil, fmt.Errorf("activation entry %d does not validly classify generated event %q; preserve generated manifest order and fail closed", index, manifest[index].NativeName)
		}
	}
	return entries, nil
}

// openCodeHostVersion is the build baseline recorded in target metadata, read
// from the runtime contract. It is not an invocation-time host observation.
func openCodeHostVersion() string {
	return runtime.OpenCode2_0_20().Versions().Min().String()
}
