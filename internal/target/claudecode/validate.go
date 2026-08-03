package claudecode

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// removedTeamLifecycleCalls are the Claude Code team-lifecycle tools the pinned
// 2.1.210 profile deliberately never lowers to. They belong to the orchestration
// vocabulary — so an agent that grants one is naming a removed native call — but
// the pinned contract classifies none of the core operations to them.
var removedTeamLifecycleCalls = []string{"TeamCreate", "TeamDelete"}

// ReviewedNativeCalls returns the sorted set of native host call names the pinned
// contract actually lowers core orchestration operations to. It is derived from
// the contract's bindings, never hardcoded, so it always reflects exactly what
// the reviewed runtime profile declares.
func ReviewedNativeCalls(contract runtime.RuntimeContract) ([]string, error) {
	if !contract.IsValid() {
		return nil, fmt.Errorf(
			"claudecode.ReviewedNativeCalls: the runtime contract is zero or invalid — " +
				"the reviewed native vocabulary is read from a contract's bindings; " +
				"pass a contract built by runtime.ClaudeCode2_1_210 or NewRuntimeContract",
		)
	}
	seen := make(map[string]struct{})
	for _, kind := range ir.AllOperationKinds() {
		descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
		if !ok {
			continue
		}
		binding, err := runtime.LookupOperationBinding(contract, descriptor)
		if err != nil {
			// Unsupported operations yield an actionable error and no binding;
			// mediated and semantic operations yield a binding whose Native is
			// absent. Neither contributes a native call name.
			continue
		}
		if call, isNative := binding.Native(); isNative {
			seen[call.CallName()] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// orchestrationVocabulary is the closed set of Claude Code orchestration tool
// names the pinned profile reasons about: the reviewed native calls it lowers to
// plus the removed team-lifecycle calls it never names. A tool name outside this
// set (Read, Bash, Task, Edit, Write, Glob, Grep, …) is a general host
// capability, not an orchestration lowering, and is not this validator's concern
// — so legitimate skill prose that discusses a deprecated tool never trips it.
func orchestrationVocabulary(reviewed []string) map[string]struct{} {
	vocab := make(map[string]struct{}, len(reviewed)+len(removedTeamLifecycleCalls))
	for _, name := range reviewed {
		vocab[name] = struct{}{}
	}
	for _, name := range removedTeamLifecycleCalls {
		vocab[name] = struct{}{}
	}
	return vocab
}

// ValidateAgentFidelity proves the target's agent artifacts grant only native
// orchestration tools the pinned contract declares. It parses each agent's
// `tools:` frontmatter and rejects any granted tool that is in the orchestration
// vocabulary but is not a reviewed native call — in particular a removed
// team-lifecycle call. General host tools are ignored, so the check operates on
// the contract's binding vocabulary rather than scanning free-text guidance.
func ValidateAgentFidelity(d TargetDescriptor) error {
	if !d.IsValid() {
		return fmt.Errorf(
			"claudecode.ValidateAgentFidelity: the target descriptor is zero or invalid — " +
				"validation reads the agents bundle from a constructed descriptor; " +
				"build it with claudecode.Descriptor",
		)
	}
	contract := runtime.ClaudeCode2_1_210()
	reviewed, err := ReviewedNativeCalls(contract)
	if err != nil {
		return fmt.Errorf("claudecode.ValidateAgentFidelity: %w", err)
	}
	reviewedSet := make(map[string]struct{}, len(reviewed))
	for _, name := range reviewed {
		reviewedSet[name] = struct{}{}
	}
	vocab := orchestrationVocabulary(reviewed)

	bundle := d.Agents().Bundle()
	for _, entry := range bundle.Manifest().Entries() {
		name := entry.Path().String()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		granted, err := agentToolsFrontmatter(bundle, name)
		if err != nil {
			return err
		}
		for _, tool := range granted {
			if _, isOrchestration := vocab[tool]; !isOrchestration {
				continue
			}
			if _, reviewedOK := reviewedSet[tool]; !reviewedOK {
				return fmt.Errorf(
					"claudecode.ValidateAgentFidelity: agent artifact %q grants orchestration tool %q, "+
						"which the pinned contract %q does not lower any core operation to — "+
						"the reviewed native calls are [%s]; %q is a removed or unmodeled native call and must not be granted — "+
						"remove it from the agent's tools frontmatter or target a contract that declares it",
					name, tool, d.RuntimeContractID(), strings.Join(reviewed, ", "), tool,
				)
			}
		}
	}
	return nil
}

// agentToolsFrontmatter reads the comma-separated tool names on the `tools:`
// line of an agent artifact's YAML frontmatter. An agent without a tools line
// grants no tools and yields an empty slice.
func agentToolsFrontmatter(source fs.FS, name string) ([]string, error) {
	content, err := fs.ReadFile(source, name)
	if err != nil {
		return nil, fmt.Errorf(
			"claudecode.ValidateAgentFidelity: cannot read agent artifact %q from its bundle — "+
				"the agents bundle is missing a declared leaf, which must never happen for a validated bundle; "+
				"rebuild the descriptor with claudecode.Descriptor: %w",
			name, err,
		)
	}
	inFrontmatter := false
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break
		}
		if !inFrontmatter {
			continue
		}
		if !strings.HasPrefix(trimmed, "tools:") {
			continue
		}
		list := strings.TrimSpace(strings.TrimPrefix(trimmed, "tools:"))
		if list == "" {
			return nil, nil
		}
		parts := strings.Split(list, ",")
		tools := make([]string, 0, len(parts))
		for _, part := range parts {
			if tool := strings.TrimSpace(part); tool != "" {
				tools = append(tools, tool)
			}
		}
		return tools, nil
	}
	return nil, nil
}
