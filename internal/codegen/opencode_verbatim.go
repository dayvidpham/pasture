package codegen

// portableVerbatimDirs names the hand-authored skill directories the OpenCode
// and Codex harnesses copy VERBATIM (no template rendering). The copy is
// RECURSIVE (see copyVerbatimSkill in harness.go): the entire source tree is
// reproduced byte-for-byte.
//
// Why these two:
//   - "protocol" is the shared documentation skill (PROCESS.md, AGENTS.md,
//     CONSTRAINTS.md, CLAUDE.md, SKILLS.md, README.md, the HANDOFF_*/MR_*/UAT_*
//     templates, and figures/). The generated per-role OpenCode skills under
//     .opencode/skill/<role>/SKILL.md link to siblings under ../protocol/
//     (e.g. ../protocol/PROCESS.md, ../protocol/CONSTRAINTS.md), so emitting
//     .opencode/skill/protocol/ in full is what makes those links resolve. A
//     SKILL.md-only copy would ship dangling links — hence the recursive copy.
//   - "install-cli" is the Pasture binary installer skill (hand-authored, no
//     generated counterpart).
//
// Both are wired into OpenCodeTarget.Verbatim and CodexTarget.Verbatim. The
// Claude Code target does not copy them because they already live under skills/.
var portableVerbatimDirs = []string{
	"protocol",
	"install-cli",
}
