package codegen

// openCodeVerbatimDirs names the hand-authored skill directories the OpenCode
// harness copies VERBATIM (no template rendering) from skills/<dir>/ into
// .opencode/skill/<dir>/. The copy is RECURSIVE (see copyVerbatimSkill in
// harness.go): the entire source tree — SKILL.md plus every sibling .md doc and
// the figures/ subdirectory — is reproduced byte-for-byte.
//
// Why this directory:
//   - "protocol" is the shared documentation skill (PROCESS.md, AGENTS.md,
//     CONSTRAINTS.md, CLAUDE.md, SKILLS.md, README.md, the HANDOFF_*/MR_*/UAT_*
//     templates, and figures/). The generated per-role OpenCode skills under
//     .opencode/skill/<role>/SKILL.md link to siblings under ../protocol/
//     (e.g. ../protocol/PROCESS.md, ../protocol/CONSTRAINTS.md), so emitting
//     .opencode/skill/protocol/ in full is what makes those links resolve. A
//     SKILL.md-only copy would ship dangling links — hence the recursive copy.
//
// install-cli is intentionally absent: it has target-specific identity and
// invocation guidance and is emitted from its canonical target-aware renderer.
// The Claude Code target does not copy protocol (it already lives under skills/).
var openCodeVerbatimDirs = []string{
	"protocol",
}
