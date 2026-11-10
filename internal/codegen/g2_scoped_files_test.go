package codegen

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// G2ScopedFiles returns the full surface swept by the handoff-storage guard and
// reused by every guard that must cover the shipped corpus: the root
// schema.xml, every agents/*.md (SLICE-1), every skills/*/SKILL.md (SLICE-5
// widening), and every skills/protocol/*.md (UAT-2 widening).
//
// SLICE-1 authored this corpus for the G2 guard against the schema.xml +
// agents/*.md it owned and documented the widening point (A3: "G2 widened
// [SLICE-1 guard; SLICE-5 final sweep]"). SLICE-5 widened the scope to
// skills/*/SKILL.md once SLICE-3 (bodies) and SLICE-4 (protocol docs) dropped
// their handoff-path prose.
//
// UAT-2 dropped the LAST exclusion: the hand-authored skills/protocol/*.md docs
// (CLAUDE.md, CONSTRAINTS.md, AGENTS.md, PROCESS.md, HANDOFF_TEMPLATE.md) were
// previously left out because they carried legitimate retired-pattern migration
// callouts (e.g. "R8/A3 retired the .git/.aura/handoff pattern"). The user chose
// absolute cleanliness (UAT-2 FIX-NOW): the path must appear NOWHERE, so those
// callouts were stripped and the protocol docs are now swept too. There is NO
// allowlist and NO exclusion anywhere — zero-tolerance is durable across the
// entire generated + hand-authored surface.
//
// It is exported because the external package codegen_test keeps the G2 guard
// and the in-package review-model guards must sweep the same corpus; one list,
// not two (tenet 4).
func G2ScopedFiles(t *testing.T, root string) []string {
	t.Helper()

	agentGlob := filepath.Join(root, "agents", "*.md")
	agentFiles, err := filepath.Glob(agentGlob)
	require.NoError(t, err, "G2: globbing %q failed", agentGlob)
	require.NotEmpty(t, agentFiles,
		"G2: no agents/*.md found under %q — the guard would vacuously pass", root)

	skillGlob := filepath.Join(root, "skills", "*", "SKILL.md")
	skillFiles, err := filepath.Glob(skillGlob)
	require.NoError(t, err, "G2: globbing %q failed", skillGlob)
	require.NotEmpty(t, skillFiles,
		"G2: no skills/*/SKILL.md found under %q — the SLICE-5 widening would vacuously pass", root)

	// UAT-2 widening: the hand-authored protocol docs (skills/protocol/*.md) were
	// previously excluded because they carried legitimate retired-pattern migration
	// callouts. The user chose absolute cleanliness (UAT-2 FIX-NOW): the path must
	// appear NOWHERE, so the exclusion is dropped and these are now swept too.
	protocolGlob := filepath.Join(root, "skills", "protocol", "*.md")
	protocolFiles, err := filepath.Glob(protocolGlob)
	require.NoError(t, err, "G2: globbing %q failed", protocolGlob)
	require.NotEmpty(t, protocolFiles,
		"G2: no skills/protocol/*.md found under %q — the UAT-2 widening would vacuously pass", root)

	files := append([]string{}, agentFiles...)
	files = append(files, skillFiles...)
	files = append(files, protocolFiles...)
	files = append(files, filepath.Join(root, "schema.xml"))
	return files
}
