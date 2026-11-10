package codegen

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelPolicyRepoRoot returns the module root from this test file's location.
// The package's black-box tests live in codegen_test and cannot share a helper,
// so this white-box test file carries its own.
func modelPolicyRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("modelPolicyRepoRoot: runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// TestCapabilityTierSetIsClosed proves the capability-tier and effort sets are
// closed and defensive: every registered value validates, an unregistered value
// does not, and mutating a caller's copy cannot affect the next caller.
func TestCapabilityTierSetIsClosed(t *testing.T) {
	t.Parallel()

	tiers := AllCapabilityTiers()
	require.Equal(t, []CapabilityTier{TierFrontier, TierStandard, TierMini}, tiers,
		"the capability tiers must be T1 frontier, T2 standard, T3 mini in order")
	for _, tier := range tiers {
		assert.True(t, tier.IsValid(), "tier %q must be registered", tier)
	}
	assert.False(t, CapabilityTier("T4").IsValid(), "an unregistered tier must not validate")
	tiers[0] = CapabilityTier("T9")
	assert.Equal(t, TierFrontier, AllCapabilityTiers()[0], "AllCapabilityTiers must return a fresh copy")

	efforts := AllEffortLevels()
	require.NotEmpty(t, efforts)
	for _, effort := range efforts {
		assert.True(t, effort.IsValid(), "effort %q must be registered", effort)
	}
	assert.False(t, EffortLevel("maximum").IsValid(), "an unregistered effort must not validate")
}

// TestCapabilityTierRoleMappingIsComplete proves every role resolves to exactly
// one registered tier, the tier registry maps each role once, and the reviewer
// tier is the reviewer role's override rather than a separate budget type.
func TestCapabilityTierRoleMappingIsComplete(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateCapabilityTiers())

	seen := make(map[protocol.RoleId]CapabilityTier)
	for _, tier := range AllCapabilityTiers() {
		spec := mustCapabilityTierSpec(tier)
		require.NotEmpty(t, spec.Name, "tier %q needs a name", tier)
		require.NotEmpty(t, spec.DefaultModel, "tier %q needs a default model nickname", tier)
		require.True(t, spec.Effort.IsValid(), "tier %q needs a registered effort", tier)
		for _, roleID := range spec.Roles {
			_, duplicate := seen[roleID]
			assert.False(t, duplicate, "role %q must be mapped by exactly one tier", roleID)
			seen[roleID] = tier
		}
	}

	for roleID := range RoleSpecs {
		effective, err := TierForRole(roleID)
		require.NoError(t, err, "role %q must resolve to a registered tier", roleID)
		assert.True(t, effective.IsValid(), "role %q effective tier %q must be registered", roleID, effective)
	}

	reviewer := RoleSpecs[protocol.RoleReviewer]
	assert.Equal(t, TierStandard, reviewer.TierOverride,
		"the reviewer tier must be the reviewer role's TierOverride (standard full review by default)")
	assert.Equal(t, TierMini, reviewer.Tier,
		"the reviewer's mapped checker tier is mini; the override lifts it to standard by default")
}

// TestModelForTierCoversEveryHarness proves every harness resolves every
// capability tier to a non-empty native model id and fails closed on a miss.
func TestModelForTierCoversEveryHarness(t *testing.T) {
	t.Parallel()

	harnesses := []HarnessName{HarnessClaudeCode, HarnessOpenCode, HarnessCodex}
	for _, harness := range harnesses {
		for _, tier := range AllCapabilityTiers() {
			model, err := ModelForTier(harness, tier)
			require.NoError(t, err, "harness %q tier %q", harness, tier)
			assert.NotEmpty(t, model, "harness %q tier %q must resolve a native model id", harness, tier)
		}
	}

	if _, err := ModelForTier(HarnessName("no-such-harness"), TierFrontier); err == nil {
		t.Fatal("an unregistered harness must fail closed")
	} else {
		assert.Contains(t, err.Error(), "no model tier table")
	}
	if _, err := ModelForTier(HarnessClaudeCode, CapabilityTier("T9")); err == nil {
		t.Fatal("an unregistered tier must fail closed")
	} else {
		assert.Contains(t, err.Error(), "no ")
	}
}

// TestModelChainAtP1AndP8 proves the fallback-chain ask renders at phase 1 and
// phase 8 only: one gate, two asks, not five. It also pins the concurrency cap
// and the reviewer override wording.
func TestModelChainAtP1AndP8(t *testing.T) {
	t.Parallel()

	root := modelPolicyRepoRoot(t)
	p1 := filepath.Join(root, "skills", "user-request", "SKILL.md")
	p8 := filepath.Join(root, "skills", "supervisor-plan-tasks", "SKILL.md")

	const needle = "The three form one fallback chain"
	for _, path := range []string{p1, p8} {
		content, err := os.ReadFile(path)
		require.NoError(t, err, "read rendered skill %s", path)
		text := string(content)
		assert.Contains(t, text, "ask the user for three models: a primary, a secondary, and a tertiary",
			"%s must carry the three-model ask", path)
		assert.Contains(t, text, needle, "%s must state the fallback chain", path)
		assert.Contains(t, text, "when a provider is exhausted, use the next model in the chain",
			"%s must say how the chain is consumed on exhaustion", path)
		assert.Contains(t, text, "at most 4 delegated agents",
			"%s must carry the concurrency cap", path)
		assert.Contains(t, text, "reviewer role's TierOverride",
			"%s must name the reviewer override, not a budget type", path)
	}

	matches, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	require.NoError(t, err)
	var owners []string
	for _, path := range matches {
		rel, err := filepath.Rel(root, path)
		require.NoError(t, err)
		if strings.HasPrefix(filepath.ToSlash(rel), "skills/protocol/") {
			continue
		}
		content, err := os.ReadFile(path)
		require.NoError(t, err, "read rendered skill %s", path)
		if strings.Contains(string(content), needle) {
			owners = append(owners, filepath.ToSlash(rel))
		}
	}
	assert.ElementsMatch(t, []string{
		"skills/supervisor-plan-tasks/SKILL.md",
		"skills/user-request/SKILL.md",
	}, owners, "the model-chain ask must render at one gate in phase 1 and one gate in phase 8, not five places")

	assert.Equal(t, 4, MaxConcurrentAgents, "the concurrency cap is the observed 4-agent bound")
}

// vendorModelToken matches vendor model identifiers, not harness names. "Claude
// Code" is a harness and stays allowed; "claude-opus-4-8" is a model id.
var vendorModelToken = regexp.MustCompile(`(?i)\b(opus|sonnet|haiku)\b|claude-(opus|sonnet|haiku)[a-z0-9.-]*|gpt-[0-9][a-z0-9.-]*|(anthropic|openai|google|deepseek|meta|mistral|xai)/[a-z0-9._-]+`)

// TestVendorIdOnlyInAgentFiles proves both directions of the model boundary:
// emitted prose never names a vendor model, and every emitted agent file carries
// the native id its effective capability tier maps to.
func TestVendorIdOnlyInAgentFiles(t *testing.T) {
	t.Parallel()

	root := modelPolicyRepoRoot(t)

	agentFiles := map[string]HarnessName{
		"agents":          HarnessClaudeCode,
		".opencode/agent": HarnessOpenCode,
		".codex/agents":   HarnessCodex,
	}
	agentTokens := make(map[string]struct{})
	for dir, harness := range agentFiles {
		paths, err := filepath.Glob(filepath.Join(root, dir, "*"))
		require.NoError(t, err)
		require.NotEmpty(t, paths, "no agent files under %s", dir)
		for _, path := range paths {
			if info, err := os.Stat(path); err != nil || info.IsDir() {
				continue
			}
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			roleID, model, ok := emittedAgentModel(path, string(content), harness)
			require.True(t, ok, "%s has no readable model declaration", path)
			tier, err := TierForRole(roleID)
			require.NoError(t, err, "%s names role %q", path, roleID)
			want, err := ModelForTier(harness, tier)
			require.NoError(t, err)
			assert.Equal(t, want, model,
				"%s must carry the native model id for role %q tier %q", path, roleID, tier)
			for _, token := range vendorModelToken.FindAllString(model, -1) {
				agentTokens[strings.ToLower(token)] = struct{}{}
			}
		}
	}
	require.NotEmpty(t, agentTokens, "agent files must carry the vendor-qualified native model ids")

	proseGlobs := []string{
		filepath.Join(root, "skills", "*", "SKILL.md"),
		filepath.Join(root, "skills", "protocol", "*.md"),
		filepath.Join(root, ".opencode", "skill", "*", "SKILL.md"),
		filepath.Join(root, ".opencode", "skill", "protocol", "*.md"),
		filepath.Join(root, ".agents", "skills", "*", "SKILL.md"),
		filepath.Join(root, ".agents", "skills", "protocol", "*.md"),
	}
	for _, glob := range proseGlobs {
		paths, err := filepath.Glob(glob)
		require.NoError(t, err)
		for _, path := range paths {
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			hits := vendorModelToken.FindAllString(string(content), -1)
			assert.Empty(t, hits, "%s names a vendor model %v; prose must name the capability tier instead", path, hits)
		}
	}

	schema, err := os.ReadFile(filepath.Join(root, "schema.xml"))
	require.NoError(t, err)
	assert.Empty(t, vendorModelToken.FindAllString(string(schema), -1),
		"the harness-neutral schema must name capability tiers, not vendor model ids")
}

// emittedAgentModel extracts the role id and model id from one generated agent
// file for the target harness.
func emittedAgentModel(path, content string, harness HarnessName) (protocol.RoleId, string, bool) {
	base := filepath.Base(path)
	switch harness {
	case HarnessCodex:
		role := strings.TrimSuffix(strings.TrimPrefix(base, "pasture-"), ".toml")
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "model = ") {
				return protocol.RoleId(role), strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "model = ")), `"`), true
			}
		}
	case HarnessClaudeCode, HarnessOpenCode:
		role := strings.TrimSuffix(base, ".md")
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "model: ") {
				return protocol.RoleId(role), strings.TrimSpace(strings.TrimPrefix(line, "model: ")), true
			}
		}
	}
	return "", "", false
}

// TestCodexProfileConformanceDocumented proves the Codex developer_instructions
// question is recorded in the emitter, as the harness research requires, without
// redesigning the profile format in this change.
func TestCodexProfileConformanceDocumented(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join("codex_agent.go"))
	require.NoError(t, err)
	text := string(source)
	assert.Contains(t, text, "developer_instructions",
		"the Codex emitter must document the developer_instructions conformance question")
	assert.Contains(t, text, "docs/research/epoch-workflow-harness-tools.md",
		"the Codex emitter must cite the harness research that answers the question")
}
