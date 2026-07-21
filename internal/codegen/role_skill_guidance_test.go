package codegen

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

func TestRoleSkillInstructionSourcesRemainCanonicalAndAgentIntroductionsNeutral(t *testing.T) {
	t.Parallel()

	figuresDir := filepath.Join(testModuleRoot(t), "skills", "protocol", "figures")
	for roleID, guidance := range roleSkillInstructionSources {
		if strings.TrimSpace(guidance) == "" {
			t.Errorf("role %s has empty canonical skill instruction-source guidance", roleID)
			continue
		}
		skill, err := renderSkill(roleID, figuresDir, TemplateSkill)
		if err != nil {
			t.Fatalf("render role skill %s: %v", roleID, err)
		}
		if !strings.Contains(skill, "### Instruction Sources\n\n"+guidance) {
			t.Errorf("role skill %s omitted canonical instruction-source guidance", roleID)
		}
		introduction := RoleSpecs[roleID].Introduction
		for _, forbidden := range []string{"Claude Code", "OpenCode", "~/.claude", "CLAUDE.md"} {
			if strings.Contains(introduction, forbidden) {
				t.Errorf("role %s semantic introduction contains harness wording %q", roleID, forbidden)
			}
		}
	}

	for _, requiredRole := range []protocol.RoleId{protocol.RoleArchitect, protocol.RoleReviewer, protocol.RoleSupervisor, protocol.RoleWorker} {
		if _, ok := roleSkillInstructionSources[requiredRole]; !ok {
			t.Errorf("canonical role-skill guidance inventory is missing %s", requiredRole)
		}
	}
}
