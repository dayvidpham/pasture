// Package codegen — Codex standalone agent TOML generation.
//
// The Codex harness (Codex 0.144.1) has no per-file "subagent" or "skill
// invocation" runtime: its pinned runtime contract (runtime.Codex0_144_1)
// lowers InvokeSkill as a semantic instruction and every delegation/collection/
// stop operation as parent-mediated, so the only NATIVE Codex function a
// generated agent may reference is `request-input`.
//
// This file emits one standalone TOML profile per protocol role that carries
// tools (the same role set the Claude Code and OpenCode agent emitters cover)
// at `.codex/agents/<role>.toml`. Each profile is a self-describing Codex agent
// descriptor: identity, description, model, orchestration role-class, and the
// contract-derived set of native Codex functions it is permitted to call.
//
// The emitted `functions` list is NOT hand-authored: it is derived at emit time
// from the pinned Codex runtime contract's native operation bindings (see
// codexNativeFunctions), so the generated output can never name a Codex
// function the pinned contract does not classify as native.
package codegen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// codexAgentSchema is the schema tag stamped into every emitted agent TOML so a
// consumer can distinguish the Pasture Codex agent contract from an unrelated
// Codex profile and reject an incompatible shape.
const codexAgentSchema = "pasture.codex.agent.v1"

// codexRoleClass is the orchestration position a role occupies on the Codex
// harness. Codex 0.144.1 exposes no self-service spawn function, so a
// "delegated" role is driven by the parent orchestrator (parent-mediated in the
// pinned contract) rather than invoked natively by a sibling agent.
type codexRoleClass string

const (
	codexRoleClassPrimary   codexRoleClass = "primary"
	codexRoleClassDelegated codexRoleClass = "delegated"
)

// codexRoleClasses maps each protocol role to its Codex orchestration class.
// Primary roles are user-driven top-level orchestration/planning roles;
// delegated roles run under a primary's direction and, on Codex, are
// parent-mediated because the harness has no native spawn function.
var codexRoleClasses = map[protocol.RoleId]codexRoleClass{
	protocol.RoleEpoch:      codexRoleClassPrimary,
	protocol.RoleSupervisor: codexRoleClassPrimary,
	protocol.RoleArchitect:  codexRoleClassPrimary,
	protocol.RoleWorker:     codexRoleClassDelegated,
	protocol.RoleReviewer:   codexRoleClassDelegated,
}

// codexModel maps the harness-neutral RoleSpec.Model nickname to the Codex
// harness model profile.
//
// Source: the epoch's Codex model policy (recorded in the Proposal 50
// implementation plan) — high-complexity orchestration/planning roles run on
// the top Codex tier and ordinary implementation/review roles run on the
// standard tier. Unlike the OpenCode model table there is no committed live
// catalog fixture for Codex here: these are the documented Codex dispatch-tier
// profile names, not entries asserted against a third-party catalog, so the
// mapping is intentionally conservative and easy to re-point when a Codex model
// catalog is pinned.
var codexModel = map[string]string{
	"opus":   "gpt-5.6-sol",
	"sonnet": "gpt-5.6-terra",
}

// codexAgentEmitter emits `.codex/agents/<role>.toml` for every role that has
// tools. It implements AgentEmitter and is wired into CodexTarget.Agents.
type codexAgentEmitter struct{}

// Emit renders one standalone agent TOML per tool-bearing role, sorted by
// output path for deterministic, byte-stable emission.
func (codexAgentEmitter) Emit(root string, figuresDir string, opts GenerateOptions) ([]GeneratedFile, error) {
	functions := codexNativeFunctions()
	var out []GeneratedFile
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		content, err := renderCodexAgent(roleID, functions)
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.codexAgentEmitter.Emit: render Codex agent for role %q failed: %w",
				roleID, err,
			)
		}
		path := filepath.Join(root, ".codex", "agents", fmt.Sprintf("%s.toml", roleID))
		generated, err := writeFullGeneratedFile(path, content, opts)
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.codexAgentEmitter.Emit: write Codex agent for role %q to %q failed: %w",
				roleID, path, err,
			)
		}
		out = append(out, generated)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// renderCodexAgent builds the complete `.codex/agents/<role>.toml` content for
// one role. functions is the pre-computed, contract-derived native Codex
// function list shared across every emitted agent (see codexNativeFunctions).
func renderCodexAgent(roleID protocol.RoleId, functions []string) (string, error) {
	roleSpec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderCodexAgent: role %q not found in RoleSpecs — "+
				"where: codexAgentEmitter.Emit iterating a role absent from specs_data.go — "+
				"fix: define the role in RoleSpecs",
			roleID,
		)
	}

	class, ok := codexRoleClasses[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderCodexAgent: role %q has no Codex role-class mapping — "+
				"where: codexRoleClasses in internal/codegen/codex_agent.go — "+
				"fix: add a codexRoleClassPrimary/codexRoleClassDelegated entry for role %q",
			roleID, roleID,
		)
	}

	model, ok := codexModel[roleSpec.Model]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderCodexAgent: role %q model nickname %q has no Codex model mapping — "+
				"where: codexModel in internal/codegen/codex_agent.go — "+
				"fix: map the neutral nickname %q to a Codex model profile",
			roleID, roleSpec.Model, roleSpec.Model,
		)
	}

	var b strings.Builder
	b.WriteString("# Code-generated by Pasture; DO NOT EDIT.\n")
	fmt.Fprintf(&b, "# Standalone Codex agent profile for the %q protocol role.\n", string(roleID))
	fmt.Fprintf(&b, "# Runtime contract: %s\n", CodexRuntimeContractID().String())
	b.WriteString("#\n")
	b.WriteString("# `functions` lists the native Codex functions this agent may call. Codex\n")
	b.WriteString("# 0.144.1 exposes no skill or self-service spawn function, so it is derived\n")
	b.WriteString("# from the pinned runtime contract's native operation bindings.\n")
	fmt.Fprintf(&b, "schema = %s\n", tomlString(codexAgentSchema))
	fmt.Fprintf(&b, "name = %s\n", tomlString(string(roleID)))
	fmt.Fprintf(&b, "description = %s\n", tomlString(roleSpec.Description))
	fmt.Fprintf(&b, "model = %s\n", tomlString(model))
	fmt.Fprintf(&b, "role_class = %s\n", tomlString(string(class)))
	fmt.Fprintf(&b, "functions = %s\n", tomlStringArray(functions))
	return b.String(), nil
}

// tomlString renders a Go string as a TOML basic string with the minimal
// escaping TOML requires, so a description containing a quote or backslash can
// never corrupt the emitted profile.
func tomlString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlStringArray renders a slice as a single-line TOML array of basic strings
// with deterministic spacing.
func tomlStringArray(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, tomlString(value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
