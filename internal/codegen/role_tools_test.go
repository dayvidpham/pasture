package codegen

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessToolAndPolicySetsAreClosed proves both new sets are closed and
// defensive: every registered value validates, an unregistered value does not,
// and mutating one caller's copy cannot affect the next caller.
func TestHarnessToolAndPolicySetsAreClosed(t *testing.T) {
	t.Parallel()

	tools := ir.AllHarnessTools()
	require.NotEmpty(t, tools, "the harness-tool set must not be empty")
	for _, tool := range tools {
		assert.True(t, tool.IsValid(), "harness tool %q must be registered", tool)
	}
	assert.False(t, ir.HarnessTool("Shell").IsValid(), "an unregistered harness tool must not validate")
	tools[0] = ir.HarnessTool("forged")
	assert.NotEqual(t, ir.HarnessTool("forged"), ir.AllHarnessTools()[0], "AllHarnessTools must return a fresh copy")

	policies := AllRolePolicies()
	require.NotEmpty(t, policies, "the role-policy set must not be empty")
	for _, policy := range policies {
		assert.True(t, policy.IsValid(), "role policy %q must be registered", policy)
	}
	assert.False(t, RolePolicy("model-tier").IsValid(), "an unregistered role policy must not validate")
	policies[0] = RolePolicy("forged")
	assert.NotEqual(t, RolePolicy("forged"), AllRolePolicies()[0], "AllRolePolicies must return a fresh copy")
}

// TestRoleSpecToolFieldsAreTypedClosedSets proves that Tools is exactly
// []ir.HarnessTool and Operations is exactly []ir.OperationKind, and that no
// RoleSpec field is exactly []string (the shape that previously carried raw
// tool names). It does not prove that no field could hold a native name: a
// plain string field could still hold one.
func TestRoleSpecToolFieldsAreTypedClosedSets(t *testing.T) {
	t.Parallel()

	roleType := reflect.TypeOf(RoleSpec{})
	toolsField, ok := roleType.FieldByName("Tools")
	require.True(t, ok, "RoleSpec must expose a Tools field")
	assert.Equal(t, reflect.TypeOf([]ir.HarnessTool{}), toolsField.Type,
		"Tools must be []ir.HarnessTool, not a string or []string")

	operationsField, ok := roleType.FieldByName("Operations")
	require.True(t, ok, "RoleSpec must expose an Operations field")
	assert.Equal(t, reflect.TypeOf([]ir.OperationKind{}), operationsField.Type,
		"Operations must be []ir.OperationKind, not a string or []string")

	stringSlice := reflect.TypeOf([]string{})
	for index := 0; index < roleType.NumField(); index++ {
		field := roleType.Field(index)
		assert.NotEqual(t, stringSlice, field.Type,
			"RoleSpec.%s is []string; a raw tool name must not be representable in a role", field.Name)
	}
}

// TestRoleToolNamesPreserveNativeSurface pins the emitter's resolution for the
// three roles whose native surface is asserted by the existing OpenCode and
// Claude agent tests. It proves the typed fields still produce the same names,
// in the same order, through the lowering tables.
func TestRoleToolNamesPreserveNativeSurface(t *testing.T) {
	t.Parallel()

	cases := map[protocol.RoleId][]string{
		protocol.RoleEpoch:    {"Read", "Glob", "Grep", "Bash", "Skill", "Agent", "Task", "SendMessage"},
		protocol.RoleReviewer: {"Read", "Glob", "Grep", "Bash", "Skill", "SendMessage"},
		protocol.RoleWorker:   {"Read", "Glob", "Grep", "Bash", "Skill", "Edit", "Write", "SendMessage"},
	}
	for roleID, want := range cases {
		got, err := RoleToolNames(HarnessClaudeCode, RoleSpecs[roleID])
		require.NoError(t, err, "role %q", roleID)
		assert.Equal(t, want, got, "role %q Claude tool surface", roleID)
	}
}

// TestForeignToolNameScan proves the target-aware scan with two samples: a
// clean declaration passes, and a declaration carrying another target's native
// name fails in the same call. Matching is case-insensitive.
func TestForeignToolNameScan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clean := []GeneratedFile{{
		Path:    filepath.Join(root, "agents", "reviewer.md"),
		Content: "---\nname: reviewer\ntools: Read, Glob\n---\n\n# Reviewer Agent\n",
	}}
	require.NoError(t, ValidateOperationalFiles(root, clean, "foreign tool scan", HarnessClaudeCode),
		"a declaration that names only its own target's tools must pass")

	for _, planted := range []string{"question", "QUESTION", "request-input"} {
		defective := []GeneratedFile{{
			Path:    filepath.Join(root, "agents", "reviewer.md"),
			Content: "---\nname: reviewer\ntools: Read, " + planted + "\n---\n\n# Reviewer Agent\n",
		}}
		err := ValidateOperationalFiles(root, defective, "foreign tool scan", HarnessClaudeCode)
		require.Error(t, err, "planted foreign tool %q must fail", planted)
		assert.Contains(t, err.Error(), "foreign tool name", "planted foreign tool %q", planted)
		assert.Contains(t, err.Error(), "fix:", "the failure must be actionable")
	}

	openCodeClean := []GeneratedFile{{
		Path:    filepath.Join(root, ".opencode", "agent", "reviewer.md"),
		Content: "---\ndescription: Reviewer\npermission:\n  \"*\": deny\n  read: allow\n  skill: allow\n---\n\n# Reviewer Agent\n",
	}}
	require.NoError(t, ValidateOperationalFiles(root, openCodeClean, "foreign tool scan", HarnessOpenCode))

	openCodeDefective := []GeneratedFile{{
		Path:    filepath.Join(root, ".opencode", "agent", "reviewer.md"),
		Content: "---\ndescription: Reviewer\npermission:\n  \"*\": deny\n  Agent: allow\n---\n\n# Reviewer Agent\n",
	}}
	err := ValidateOperationalFiles(root, openCodeDefective, "foreign tool scan", HarnessOpenCode)
	require.ErrorContains(t, err, "foreign tool name")

	codexClean := []GeneratedFile{{
		Path:    filepath.Join(root, ".codex", "agents", "pasture-reviewer.toml"),
		Content: "name = \"reviewer\"\nfunctions = [\"request-input\"]\n",
	}}
	require.NoError(t, ValidateOperationalFiles(root, codexClean, "foreign tool scan", HarnessCodex))

	codexDefective := []GeneratedFile{{
		Path:    filepath.Join(root, ".codex", "agents", "pasture-reviewer.toml"),
		Content: "name = \"reviewer\"\nfunctions = [\"Read\"]\n",
	}}
	err = ValidateOperationalFiles(root, codexDefective, "foreign tool scan", HarnessCodex)
	require.ErrorContains(t, err, "foreign tool name")

	// An unrecognised declaration shape must fail closed. If it parsed to an
	// empty list, the guard would pass vacuously on the very template change it
	// exists to catch.
	markdownShapes := []string{
		"---\nname: reviewer\ntools:\n  - Read\n---\n\n# Reviewer Agent\n",
		"---\nname: reviewer\nmodel: opus\n---\n\n# Reviewer Agent\n",
		"name: reviewer\ntools: Read\n",
	}
	for _, shape := range markdownShapes {
		defective := []GeneratedFile{{
			Path:    filepath.Join(root, "agents", "reviewer.md"),
			Content: shape,
		}}
		err := ValidateOperationalFiles(root, defective, "foreign tool scan", HarnessClaudeCode)
		require.ErrorContains(t, err, "foreign tool scan could not read", "unrecognised shape %q", shape)
	}

	codexShape := []GeneratedFile{{
		Path:    filepath.Join(root, ".codex", "agents", "pasture-reviewer.toml"),
		Content: "name = \"reviewer\"\n",
	}}
	err = ValidateOperationalFiles(root, codexShape, "foreign tool scan", HarnessCodex)
	require.ErrorContains(t, err, "foreign tool scan could not read")
}

// TestForeignToolNamesAreDerived pins the derivation direction: a target's
// foreign set is the other targets' native names minus its own, so a contract
// change updates the guard without editing a list here.
func TestForeignToolNamesAreDerived(t *testing.T) {
	t.Parallel()

	claude, err := foreignToolNames(HarnessClaudeCode)
	require.NoError(t, err)
	assert.Contains(t, claude, "question", "OpenCode's question tool is foreign to Claude Code")
	assert.Contains(t, claude, "request-input", "Codex's request-input is foreign to Claude Code")
	assert.NotContains(t, claude, "skill", "Claude Code declares Skill itself")
	assert.NotContains(t, claude, "task", "Claude Code declares Task itself")

	openCode, err := foreignToolNames(HarnessOpenCode)
	require.NoError(t, err)
	assert.Contains(t, openCode, "agent", "Claude Code's Agent is foreign to OpenCode")
	assert.Contains(t, openCode, "askuserquestion", "Claude Code's AskUserQuestion is foreign to OpenCode")
	assert.NotContains(t, openCode, "task", "OpenCode declares task itself")
	assert.NotContains(t, openCode, "question", "OpenCode declares question itself")
}

// TestValidateRoleToolSurfaceHasTwoSamples proves each validation branch has a
// clean sample that passes and a defective sample that fails: the read-only
// policy, an unregistered tool (the only way a raw string can become a tool
// after an explicit conversion), an unregistered policy, an unregistered
// operation kind, and a registered operation the role-tool sequence cannot
// render.
func TestValidateRoleToolSurfaceHasTwoSamples(t *testing.T) {
	t.Parallel()

	clean := RoleSpec{
		Id:          protocol.RoleReviewer,
		Tools:       []ir.HarnessTool{ir.HarnessToolRead, ir.HarnessToolGrep},
		Operations:  []ir.OperationKind{ir.OperationInvokeSkill},
		Policies:    []RolePolicy{RolePolicyReadOnly},
		OwnedPhases: []protocol.PhaseId{protocol.PhaseReview},
	}
	require.NoError(t, validateRoleSpec(protocol.RoleReviewer, clean))

	defective := clean
	defective.Tools = append(append([]ir.HarnessTool(nil), clean.Tools...), ir.HarnessToolEdit)
	require.ErrorContains(t, validateRoleSpec(protocol.RoleReviewer, defective), "read-only")

	unregisteredTool := clean
	unregisteredTool.Tools = []ir.HarnessTool{ir.HarnessTool("Shell")}
	require.ErrorContains(t, validateRoleSpec(protocol.RoleReviewer, unregisteredTool), "unregistered harness tool")

	unregisteredPolicy := clean
	unregisteredPolicy.Policies = []RolePolicy{RolePolicy("forged")}
	require.ErrorContains(t, validateRoleSpec(protocol.RoleReviewer, unregisteredPolicy), "unregistered policy")

	unregisteredOperation := clean
	unregisteredOperation.Operations = []ir.OperationKind{ir.OperationKind("forged")}
	require.ErrorContains(t, validateRoleSpec(protocol.RoleReviewer, unregisteredOperation), "unregistered operation")

	unrenderableOperation := clean
	unrenderableOperation.Operations = []ir.OperationKind{ir.OperationContinueAssignment}
	require.ErrorContains(t, validateRoleSpec(protocol.RoleReviewer, unrenderableOperation), "no rendering slot")
}
