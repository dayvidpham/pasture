// Command codegen is the go:generate entry point for the pasture codegen system.
//
// It is invoked by:
//
//	go generate ./internal/codegen/...
//
// which runs:
//
//	go run ../../tools/codegen
//
// from the internal/codegen/ package directory.
//
// This binary wires all three generators:
//  1. GenerateSchemaToFile — writes schema.xml
//  2. GenerateSkill — writes skills/{role}/SKILL.md headers (marker-bounded)
//  3. GenerateSubSkill — writes skills/{dir}/SKILL.md sub-skill headers
//  4. GenerateAgent — writes agents/{role}.md definitions (fully generated)
//
// Exits non-zero if any generator returns an error.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// moduleRoot walks upward from the current working directory until it finds go.mod,
// then returns that directory as the module (repo) root.
//
// When go:generate runs from internal/codegen/, the cwd is that package directory.
// Walking upward finds the repo root regardless of the exact invocation method.
func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("codegen: os.Getwd failed: %w", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"codegen: could not find go.mod walking up from %q — "+
					"ensure the tool is run from inside the pasture module",
				wd,
			)
		}
		dir = parent
	}
}

// roleSkillDirs maps each role to its skill directory name (relative to skills/).
// Mirrors Python _ROLE_SKILL_DIRS in gen_skills.py.
var roleSkillDirs = map[protocol.RoleId]string{
	protocol.RoleSupervisor: "supervisor",
	protocol.RoleWorker:     "worker",
	protocol.RoleReviewer:   "reviewer",
	protocol.RoleArchitect:  "architect",
	protocol.RoleEpoch:      "epoch",
}

// commandSkillDirs maps each command ID to its skill directory name (relative to skills/).
// Only commands whose FigureSpecs entries have CommandRefs pointing at them need
// sub-skill header generation. Mirrors Python _COMMAND_SKILL_DIRS in gen_skills.py.
var commandSkillDirs = map[string]string{
	"cmd-sup-plan":    "supervisor-plan-tasks",
	"cmd-sup-spawn":   "supervisor-spawn-worker",
	"cmd-impl-review": "impl-review",
	// Newly-ported commands (22 skills from aura-plugins/skills/).
	"cmd-arch-handoff":  "architect-handoff",
	"cmd-arch-propose":  "architect-propose-plan",
	"cmd-arch-ratify":   "architect-ratify",
	"cmd-arch-review":   "architect-request-review",
	"cmd-explore":       "explore",
	"cmd-impl-slice":    "impl-slice",
	"cmd-research":      "research",
	"cmd-rev-comment":   "reviewer-comment",
	"cmd-rev-code":      "reviewer-review-code",
	"cmd-rev-plan":      "reviewer-review-plan",
	"cmd-rev-vote":      "reviewer-vote",
	"cmd-status":        "status",
	"cmd-sup-commit":    "supervisor-commit",
	"cmd-sup-track":     "supervisor-track-progress",
	"cmd-swarm":         "swarm",
	"cmd-user-elicit":   "user-elicit",
	"cmd-user-request":  "user-request",
	"cmd-user-uat":      "user-uat",
	"cmd-work-blocked":  "worker-blocked",
	"cmd-work-complete": "worker-complete",
	"cmd-work-impl":     "worker-implement",
}

func main() {
	outputRoot := flag.String("output", "", "output root directory (default: module root, found by walking up from cwd to go.mod)")
	flag.Parse()

	var root string
	if *outputRoot != "" {
		root = *outputRoot
	} else {
		var err error
		root, err = moduleRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
	}

	opts := codegen.DefaultOptions // Diff: true, Write: true

	var errors []error

	// ── 1. Generate schema.xml ────────────────────────────────────────────────
	schemaPath := filepath.Join(root, "schema.xml")
	if _, err := codegen.GenerateSchemaToFile(schemaPath, opts); err != nil {
		errors = append(errors, fmt.Errorf("schema: %w", err))
	} else {
		fmt.Printf("Generated %s\n", schemaPath)
	}

	// ── 2. Generate SKILL.md headers for each role ────────────────────────────
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	for roleId, dirName := range roleSkillDirs {
		skillPath := filepath.Join(root, "skills", dirName, "SKILL.md")
		if _, err := os.Stat(skillPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Skipping %s (not found)\n", skillPath)
			continue
		}
		if _, err := codegen.GenerateSkill(roleId, skillPath, figuresDir, opts); err != nil {
			errors = append(errors, fmt.Errorf("skill %s: %w", dirName, err))
		} else {
			fmt.Printf("Generated %s\n", skillPath)
		}
	}

	// ── 3. Generate sub-skill headers (commands with figures) ─────────────────
	for commandId, dirName := range commandSkillDirs {
		skillPath := filepath.Join(root, "skills", dirName, "SKILL.md")
		if _, err := os.Stat(skillPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Skipping sub-skill %s (not found)\n", skillPath)
			continue
		}
		if _, err := codegen.GenerateSubSkill(commandId, skillPath, figuresDir, opts); err != nil {
			errors = append(errors, fmt.Errorf("sub-skill %s: %w", dirName, err))
		} else {
			fmt.Printf("Generated sub-skill %s\n", skillPath)
		}
	}

	// ── 4. Generate agent definitions for roles with tools ────────────────────
	for roleId, roleSpec := range codegen.RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		agentPath := filepath.Join(root, "agents", fmt.Sprintf("%s.md", roleId))
		if _, err := codegen.GenerateAgent(roleId, agentPath, figuresDir, opts); err != nil {
			errors = append(errors, fmt.Errorf("agent %s: %w", roleId, err))
		} else {
			fmt.Printf("Generated %s\n", agentPath)
		}
	}

	// ── 5. Global-ID uniqueness enforcement (SLICE-2, URD R5+R7) ─────────────
	// Must run AFTER all generators so the full registry is assembled, and
	// BEFORE exit so a violation causes go generate to fail immediately.
	if err := codegen.ValidateGlobalIds(); err != nil {
		errors = append(errors, fmt.Errorf("global-id validation: %w", err))
	}

	// ── Report errors and exit ────────────────────────────────────────────────
	if len(errors) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d error(s) encountered:\n", len(errors))
		for _, e := range errors {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", e)
		}
		os.Exit(1)
	}
}
