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

// deriveOpenCodeNativeToolNames returns, sorted and de-duplicated, exactly the
// native tool names the OpenCode runtime contract declares — and
// nothing else. It resolves every core orchestration operation against the
// contract via the delivered runtime lookup and collects only the operations the
// contract classifies as native. Semantic-instruction, parent-mediated, and
// unsupported operations contribute no tool name, so a generated artifact can
// never reference an invented OpenCode tool: the allow-list is the contract's
// own declared surface, computed here rather than hand-copied.
func deriveOpenCodeNativeToolNames() ([]string, error) {
	contract := runtime.OpenCode1_18_29()
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
	contract := runtime.OpenCode1_18_29Lifecycle()
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			return "", err
		}
		surfaces[mapping.NativeName()] = mapping.Surface()
	}
	return generateOpenCodeHooksModule(registration.OpenCode1_18_29().Entries(), activationEntries, surfaces)
}

// generateOpenCodeHooksModule consumes the validated activation result from the
// public generator. Constructed inputs exercise emission mechanics only; this
// function neither grants activation nor evaluates capture evidence.
func generateOpenCodeHooksModule(manifest []registration.Event, activationEntries []activation.Entry, surfaces map[string]runtime.HookSurface) (string, error) {
	enabled := make(map[model.ContractEventKind]bool)
	for _, entry := range activationEntries {
		if _, duplicate := enabled[entry.Event]; duplicate {
			return "", fmt.Errorf("codegen.generateOpenCodeHooksModule: duplicate activation kind %d during emission; restore one classification per registration", entry.Event)
		}
		enabled[entry.Event] = entry.State == activation.Enabled
	}
	callbacks, factory, err := openCodeCallbacks(manifest, enabled, surfaces)
	if err != nil {
		return "", err
	}
	toolNames, err := deriveOpenCodeNativeToolNames()
	if err != nil {
		return "", fmt.Errorf("codegen.GenerateOpenCodeHooksModule: derive native tool allow-list: %w", err)
	}
	metadata, err := lifecycleMetadata(
		runtime.OpenCode1_18_29Lifecycle(),
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
// This plugin is self-contained and requires only Bun plus the built pasture binary.

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

async function invokeLifecycle(command, event, value) {
  const binary = process.env.%s ?? "pasture";
  const child = Bun.spawn({
    cmd: [binary, ...command],
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
  if (stderr.trim() !== "") await forwardDiagnostic(stderr.endsWith("\n") ? stderr : stderr + "\n", event);
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
  // THE STREAM IT NAMES IS REACHED BECAUSE invokeLifecycle FORWARDS IT. That is
  // the whole reason the imperative is allowed to stand: for one round this
  // line named standard error while the spawn above piped fd 2 and dropped
  // everything it caught on the exit-0 route, so the operator was sent to a
  // stream this callback had emptied. Evidence named to a reader who cannot
  // reach it is the same defect as evidence that does not exist.
  if (stdout.trim() === "") {
    console.error("Pasture did not evaluate " + event + " and returned no decision; the host continues unevaluated. Read the pasture diagnostic on standard error first: this plugin forwards it there, and pasture reports every such fault there, including the case where it could not write a durable record. A line may also have been appended to lifecycle-faults.jsonl beside the pasture database, but a fault whose record could not be placed or written leaves none, and the diagnostic then quotes the path it tried.");
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
export const PastureLifecycle = async ({ client }) => ({
%s});

// OpenCode reads the default export first. When it is an object whose server()
// is the plugin function, the loader uses it and reads nothing else. A bare
// function default falls back to a scan of every export, which throws on the
// first export that is not a function (PASTURE_NATIVE_TOOLS above).
export default { id: "pasture-lifecycle", server: PastureLifecycle };
`

	return fmt.Sprintf(
		module,
		metadata.Contract,
		allowList,
		metadata.Contract,
		string(metadataJSON),
		openCodeHostVersion(),
		adapterBinaryEnv,
		callbacks,
		factory,
	), nil
}

func openCodeCallbacks(manifest []registration.Event, enabled map[model.ContractEventKind]bool, surfaces map[string]runtime.HookSurface) (string, string, error) {
	var callbacks, observations, named strings.Builder
	seen := make(map[model.ContractEventKind]bool)
	names := make(map[string]bool)
	for _, event := range manifest {
		active, classified := enabled[event.Kind]
		if !classified || seen[event.Kind] {
			return "", "", fmt.Errorf("codegen.openCodeCallbacks: registration %q has missing or duplicate activation join during emission; restore one classification per kind", event.NativeName)
		}
		seen[event.Kind] = true
		if !active {
			continue
		}
		// Event names become identifiers as well as quoted host keys. Refuse
		// malformed or colliding names rather than emit ambiguous JavaScript.
		parts := strings.Split(event.NativeName, ".")
		for _, part := range parts {
			if part == "" || part[0] < 'a' || part[0] > 'z' || strings.IndexFunc(part, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') }) >= 0 {
				return "", "", fmt.Errorf("codegen.openCodeCallbacks: unsupported callback name %q during emission; use the source-proven dotted native name", event.NativeName)
			}
		}
		helper := parts[0]
		for _, part := range parts[1:] {
			helper += strings.ToUpper(part[:1]) + part[1:]
		}
		helper = strings.ReplaceAll(helper, "-", "_")
		if names[helper] || helper == "event" {
			return "", "", fmt.Errorf("codegen.openCodeCallbacks: callback %q collides with another generated key during emission; correct the native registration", event.NativeName)
		}
		names[helper] = true
		command := fmt.Sprintf(`["hook", "lifecycle", "--harness", "opencode", "--event", %q, "--host-version", %q]`, event.NativeName, openCodeHostVersion())
		switch surfaces[event.NativeName] {
		case runtime.SurfaceOpenCodeCatchAllSSE:
			fmt.Fprintf(&callbacks, `export async function %s(callback) {
  try {
    await invokeLifecycle(%s, %q, callback);
  } catch (error) {
    // Observation is never a gate and cannot terminate the native event bus.
    console.error(%q + error);
  }
}

`, helper, command, event.NativeName, "Pasture lifecycle observation failed for "+event.NativeName+": ")
			fmt.Fprintf(&observations, "    if (callback.event?.type === %q) {\n      await %s(callback);\n      return;\n    }\n", event.NativeName, helper)
		case runtime.SurfaceOpenCodeNamedOutput:
			// The pinned plugin Hooks API supplies two objects to named hooks.
			// tool.execute.before retains its cleared, args-only projection.
			payload := "{ input, output }"
			if event.NativeName == "tool.execute.before" {
				payload = "{ input, output: { args: output.args } }"
			}
			fmt.Fprintf(&callbacks, `export async function %s(input, output) {
  if (input === null || typeof input !== "object" || Array.isArray(input) ||
      output === null || typeof output !== "object" || Array.isArray(output)) {
    throw new Error(%q + CONFIGURATION_ADVICE);
  }
  const stdout = await invokeLifecycle(%s, %q, %s);
  const response = parseResponse(stdout, %q);
  if (response?.decision === "deny") throw new Error(response.reason);
  // Proceed is a decision, not a mutation. Never write host-owned objects.
}

`, helper, "pasture hook lifecycle callback "+event.NativeName+" requires input and output objects; check the host plugin API", command, event.NativeName, payload, event.NativeName)
			fmt.Fprintf(&named, "  async %q(input, output) {\n    await %s(input, output);\n  },\n", event.NativeName, helper)
		default:
			return "", "", fmt.Errorf("codegen.openCodeCallbacks: enabled callback %q has unsupported runtime surface %v during emission; supply its source-proven OpenCode surface before generation", event.NativeName, surfaces[event.NativeName])
		}
	}
	if len(seen) != len(enabled) {
		return "", "", fmt.Errorf("codegen.openCodeCallbacks: activation contains an unregistered kind during emission; regenerate matching activation and registration tables")
	}
	var factory strings.Builder
	if observations.Len() > 0 {
		factory.WriteString("  async event(callback) {\n")
		factory.WriteString(observations.String())
		factory.WriteString("    void client;\n  },\n")
	}
	factory.WriteString(named.String())
	return callbacks.String(), factory.String(), nil
}

func openCodeEventByKind(kind model.ContractEventKind) (registration.Event, error) {
	for _, event := range registration.OpenCode1_18_29().Entries() {
		if event.Kind == kind {
			return event, nil
		}
	}
	return registration.Event{}, fmt.Errorf("codegen.openCodeEventByKind: generated OpenCode %s manifest lacks event kind %d; regenerate the host contract before activation", registration.OpenCode1_18_29().Version, kind)
}

func openCodeActivationEntries() ([]activation.Entry, error) {
	entries, err := activation.OpenCode1_18_29()
	if err != nil {
		return nil, err
	}
	manifest := registration.OpenCode1_18_29().Entries()
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

// openCodeHostVersion is the OpenCode host version this target generates for,
// read from the OpenCode runtime contract, the one root. The generated plugin
// passes it to every hook invocation and the target manifest records it, so
// neither restates a number that could drift from the contract.
func openCodeHostVersion() string {
	return runtime.OpenCode1_18_29().Versions().Min().String()
}
