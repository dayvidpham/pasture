package codegen_test

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Fixture types ─────────────────────────────────────────────────────────────

// schemaConstraintCheck mirrors one entry in testdata/schema.yaml constraint_checks.
type schemaConstraintCheck struct {
	ConstraintID           string   `yaml:"constraint_id"`
	IsGeneral              bool     `yaml:"is_general"`
	RoleRefMustContain     []string `yaml:"role_ref_must_contain"`
	RoleRefMustNotContain  []string `yaml:"role_ref_must_not_contain"`
	PhaseRefMustContain    []string `yaml:"phase_ref_must_contain"`
	PhaseRefMustNotContain []string `yaml:"phase_ref_must_not_contain"`
}

// schemaRoleCheck mirrors one entry in testdata/schema.yaml role_checks.
type schemaRoleCheck struct {
	RoleID                 string   `yaml:"role_id"`
	MustHaveOwnedPhases    []string `yaml:"must_have_owned_phases"`
	MustNotHaveOwnedPhases []string `yaml:"must_not_have_owned_phases"`
}

// schemaPhaseCheck mirrors one entry in testdata/schema.yaml phase_checks.
type schemaPhaseCheck struct {
	PhaseID                 string   `yaml:"phase_id"`
	MustContainElements     []string `yaml:"must_contain_elements"`
	MustContainTransitionTo string   `yaml:"must_contain_transition_to"`
	MustHaveSkillInvocation bool     `yaml:"must_have_skill_invocation"`
}

// schemaSuite is the top-level structure of testdata/schema.yaml.
type schemaSuite struct {
	ConstraintChecks  []schemaConstraintCheck `yaml:"constraint_checks"`
	RoleChecks        []schemaRoleCheck       `yaml:"role_checks"`
	PhaseChecks       []schemaPhaseCheck      `yaml:"phase_checks"`
	MustHaveCDATACode bool                    `yaml:"must_have_cdata_code"`
}

// ─── Helper: generate and parse ───────────────────────────────────────────────

// generateXML calls GenerateSchema and returns the output as a string.
// Fails the test immediately if generation fails.
func generateXML(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	err := codegen.GenerateSchema(&buf)
	require.NoError(t, err, "GenerateSchema must not return an error")
	return buf.String()
}

// ─── TestGenerateSchema_ValidXML ──────────────────────────────────────────────

// TestGenerateSchema_ValidXML verifies that GenerateSchema produces well-formed
// XML that can be parsed without errors. This is the fundamental structural gate.
func TestGenerateSchema_ValidXML(t *testing.T) {
	output := generateXML(t)
	require.NotEmpty(t, output, "GenerateSchema must produce non-empty output")

	// xml.Unmarshal into a generic interface is the simplest valid-XML check.
	var doc interface{}
	decoder := xml.NewDecoder(strings.NewReader(output))
	err := decoder.Decode(&doc)
	assert.NoError(t, err, "GenerateSchema output must be valid XML")
}

// TestGenerateSchema_HasXMLDeclaration verifies the output starts with the XML
// declaration (required for schema.xml consumers).
func TestGenerateSchema_HasXMLDeclaration(t *testing.T) {
	output := generateXML(t)
	assert.True(t,
		strings.HasPrefix(output, "<?xml version='1.0' encoding='UTF-8'?>"),
		"output must start with XML declaration",
	)
}

// TestGenerateSchema_RootElement verifies the root element is <aura-protocol version="2.0">.
func TestGenerateSchema_RootElement(t *testing.T) {
	output := generateXML(t)
	assert.Contains(t, output, `<aura-protocol`, "output must have <aura-protocol> root")
	assert.Contains(t, output, `version="2.0"`, "root must have version=2.0 attribute")
}

// ─── TestGenerateSchema_ContainsAllRoles ──────────────────────────────────────

// TestGenerateSchema_ContainsAllRoles verifies that all 5 role IDs appear in
// the generated output. This guards against missing role entries.
func TestGenerateSchema_ContainsAllRoles(t *testing.T) {
	output := generateXML(t)

	expectedRoles := []string{"epoch", "architect", "reviewer", "supervisor", "worker"}
	for _, role := range expectedRoles {
		assert.Contains(t, output, `id="`+role+`"`,
			"output must contain role with id=%q", role)
	}
}

// ─── TestGenerateSchema_ContainsAllPhases ─────────────────────────────────────

// TestGenerateSchema_ContainsAllPhases verifies that all 12 phase elements
// appear in the generated output, identified by their phase IDs (p1–p12).
func TestGenerateSchema_ContainsAllPhases(t *testing.T) {
	output := generateXML(t)

	// All 12 phases should appear as <phase id="p{N}" ...> elements.
	expectedPhaseIDs := []string{
		"p1", "p2", "p3", "p4", "p5", "p6",
		"p7", "p8", "p9", "p10", "p11", "p12",
	}
	for _, pid := range expectedPhaseIDs {
		assert.Contains(t, output, `id="`+pid+`"`,
			"output must contain phase with id=%q", pid)
	}
}

// ─── TestGenerateSchema_ConstraintRoleRefs ────────────────────────────────────

// TestGenerateSchema_ConstraintRoleRefs verifies that constraint elements have
// correct role-ref attributes from the YAML fixture.
func TestGenerateSchema_ConstraintRoleRefs(t *testing.T) {
	output := generateXML(t)

	var suite schemaSuite
	testutil.LoadFixtures(t, testutil.CodegenSchema, &suite)
	require.NotEmpty(t, suite.ConstraintChecks, "schema.yaml must have constraint_checks")

	for _, check := range suite.ConstraintChecks {
		check := check
		t.Run(check.ConstraintID, func(t *testing.T) {
			// Find the constraint element in the XML output.
			// Look for the constraint id= attribute to locate the constraint.
			constraintTag := `id="` + check.ConstraintID + `"`
			assert.Contains(t, output, constraintTag,
				"output must contain constraint %q", check.ConstraintID)

			if check.IsGeneral {
				// General constraints must NOT have a role-ref attribute.
				// Find the constraint line(s) and verify role-ref is absent.
				// We check that the constraint's id appears, and near it there's no role-ref.
				// A simpler approach: general constraints are in generalConstraints → role-ref omitted.
				// We verify by checking the output doesn't have both the constraint ID and role-ref
				// on the same constraint element. We do this by finding the constraint block.
				constraintBlock := extractConstraintBlock(output, check.ConstraintID)
				assert.NotContains(t, constraintBlock, `role-ref`,
					"general constraint %q must not have role-ref", check.ConstraintID)
				return
			}

			constraintBlock := extractConstraintBlock(output, check.ConstraintID)

			for _, role := range check.RoleRefMustContain {
				assert.Contains(t, constraintBlock, role,
					"constraint %q role-ref must contain %q", check.ConstraintID, role)
			}
			for _, role := range check.RoleRefMustNotContain {
				// The role ID must not appear in role-ref attribute value.
				// It may appear in other attributes, so we check for role-ref specifically.
				if strings.Contains(constraintBlock, `role-ref=`) {
					roleRefVal := extractAttrValue(constraintBlock, "role-ref")
					assert.NotContains(t, roleRefVal, role,
						"constraint %q role-ref must not contain %q", check.ConstraintID, role)
				}
			}
		})
	}
}

// ─── TestGenerateSchema_ConstraintPhaseRefs ───────────────────────────────────

// TestGenerateSchema_ConstraintPhaseRefs verifies that constraints with phase
// scope have correct phase-ref attributes.
func TestGenerateSchema_ConstraintPhaseRefs(t *testing.T) {
	output := generateXML(t)

	var suite schemaSuite
	testutil.LoadFixtures(t, testutil.CodegenSchema, &suite)

	for _, check := range suite.ConstraintChecks {
		check := check
		if len(check.PhaseRefMustContain) == 0 {
			continue
		}
		t.Run(check.ConstraintID+"_phase_ref", func(t *testing.T) {
			constraintBlock := extractConstraintBlock(output, check.ConstraintID)
			phaseRefVal := extractAttrValue(constraintBlock, "phase-ref")

			for _, phase := range check.PhaseRefMustContain {
				// Use comma-delimited exact match to avoid "p1" matching "p10".
				assert.True(t, containsPhaseToken(phaseRefVal, phase),
					"constraint %q phase-ref must contain %q (got %q)", check.ConstraintID, phase, phaseRefVal)
			}
			for _, phase := range check.PhaseRefMustNotContain {
				assert.False(t, containsPhaseToken(phaseRefVal, phase),
					"constraint %q phase-ref must not contain %q (got %q)", check.ConstraintID, phase, phaseRefVal)
			}
		})
	}
}

// ─── TestGenerateSchema_RoleChecks ────────────────────────────────────────────

// TestGenerateSchema_RoleChecks verifies roles have the correct owned phases
// as specified in the fixture.
func TestGenerateSchema_RoleChecks(t *testing.T) {
	output := generateXML(t)

	var suite schemaSuite
	testutil.LoadFixtures(t, testutil.CodegenSchema, &suite)
	require.NotEmpty(t, suite.RoleChecks, "schema.yaml must have role_checks")

	for _, check := range suite.RoleChecks {
		check := check
		t.Run(check.RoleID, func(t *testing.T) {
			roleBlock := extractElementBlock(output, "role", check.RoleID)
			require.NotEmpty(t, roleBlock, "role %q must appear in output", check.RoleID)

			ownsBlock := extractSubBlock(roleBlock, "owns-phases")
			for _, phase := range check.MustHaveOwnedPhases {
				assert.Contains(t, ownsBlock, `ref="`+phase+`"`,
					"role %q must own phase %q", check.RoleID, phase)
			}
			for _, phase := range check.MustNotHaveOwnedPhases {
				assert.NotContains(t, ownsBlock, `ref="`+phase+`"`,
					"role %q must NOT own phase %q", check.RoleID, phase)
			}
		})
	}
}

// ─── TestGenerateSchema_PhaseChecks ───────────────────────────────────────────

// TestGenerateSchema_PhaseChecks verifies phases contain expected elements
// and transitions.
func TestGenerateSchema_PhaseChecks(t *testing.T) {
	output := generateXML(t)

	var suite schemaSuite
	testutil.LoadFixtures(t, testutil.CodegenSchema, &suite)
	require.NotEmpty(t, suite.PhaseChecks, "schema.yaml must have phase_checks")

	for _, check := range suite.PhaseChecks {
		check := check
		t.Run(check.PhaseID, func(t *testing.T) {
			phaseBlock := extractElementBlock(output, "phase", check.PhaseID)
			require.NotEmpty(t, phaseBlock, "phase %q must appear in output", check.PhaseID)

			for _, elem := range check.MustContainElements {
				assert.Contains(t, phaseBlock, "<"+elem,
					"phase %q must contain element <%s>", check.PhaseID, elem)
			}

			if check.MustContainTransitionTo != "" {
				assert.Contains(t, phaseBlock,
					`to-phase="`+check.MustContainTransitionTo+`"`,
					"phase %q must have transition to %q", check.PhaseID, check.MustContainTransitionTo)
			}

			if check.MustHaveSkillInvocation {
				assert.Contains(t, phaseBlock, "skill-invocation",
					"phase %q must have a skill-invocation element", check.PhaseID)
			}
		})
	}
}

// ─── TestGenerateSchema_CDATAInCodeElements ───────────────────────────────────

// TestGenerateSchema_CDATAInCodeElements verifies that <code> elements use
// CDATA sections to wrap their content, preserving angle brackets raw.
//
// buildConstraints always emits CDATA-wrapped code blocks, so the assertion
// is unconditional — the conditional guard is unnecessary.
func TestGenerateSchema_CDATAInCodeElements(t *testing.T) {
	output := generateXML(t)

	assert.Contains(t, output, "<![CDATA[",
		"generated schema must contain CDATA sections: buildConstraints always emits CDATA-wrapped <code> blocks")
}

// ─── TestGenerateSchema_AllConstraintsPresent ─────────────────────────────────

// TestGenerateSchema_AllConstraintsPresent verifies that every constraint ID in
// ConstraintSpecs appears in the generated schema.xml.
func TestGenerateSchema_AllConstraintsPresent(t *testing.T) {
	output := generateXML(t)

	for cid := range codegen.ConstraintSpecs {
		assert.Contains(t, output, `id="`+cid+`"`,
			"output must contain constraint with id=%q", cid)
	}
}

// ─── TestGenerateSchema_AllHandoffsPresent ────────────────────────────────────

// TestGenerateSchema_AllHandoffsPresent verifies that all 6 handoff IDs
// appear in the generated output.
func TestGenerateSchema_AllHandoffsPresent(t *testing.T) {
	output := generateXML(t)

	for _, hid := range []string{"h1", "h2", "h3", "h4", "h5", "h6"} {
		assert.Contains(t, output, `id="`+hid+`"`,
			"output must contain handoff with id=%q", hid)
	}
}

// ─── TestGenerateSchema_AllSectionsPresent ────────────────────────────────────

// TestGenerateSchema_AllSectionsPresent verifies that all expected top-level
// sections appear in the generated output. This guards against accidentally
// dropping a section.
func TestGenerateSchema_AllSectionsPresent(t *testing.T) {
	output := generateXML(t)

	sections := []string{
		"<enums>",
		"<labels>",
		"<review-axes>",
		"<phases>",
		"<roles>",
		"<commands>",
		"<handoffs",
		"<constraints>",
		"<task-titles>",
		"<documents>",
		"<dependency-model>",
		"<followup-lifecycle>",
		"<procedure-steps>",
		"<checklists>",
		"<coordination-commands>",
		"<workflows>",
		"<figures>",
	}
	for _, sec := range sections {
		assert.Contains(t, output, sec,
			"output must contain section %q", sec)
	}
}

// ─── TestGenerateSchema_CDATACodeFixture ─────────────────────────────────────

// TestGenerateSchema_CDATACodeFixture verifies CDATA sections are present in
// the generated output when must_have_cdata_code is true in the fixture.
func TestGenerateSchema_CDATACodeFixture(t *testing.T) {
	var suite schemaSuite
	testutil.LoadFixtures(t, testutil.CodegenSchema, &suite)

	if !suite.MustHaveCDATACode {
		t.Skip("must_have_cdata_code not set in schema.yaml fixture")
	}

	output := generateXML(t)
	assert.Contains(t, output, "<![CDATA[",
		"generated schema.xml must contain CDATA sections (must_have_cdata_code=true in fixture)")
}

// ─── TestValidateSchema_MalformedXML ─────────────────────────────────────────

// TestValidateSchema_MalformedXML verifies that passing malformed XML to
// ValidateSchema returns a Structural-layer ValidationError rather than an
// error return value. Parse failures are reported as ValidationErrors so that
// callers receive a consistent result type regardless of failure mode.
func TestValidateSchema_MalformedXML(t *testing.T) {
	r := strings.NewReader("<aura-protocol><unclosed")
	errs, err := codegen.ValidateSchema(r)
	assert.NoError(t, err,
		"ValidateSchema must return nil error for malformed XML — parse failures go to ValidationError")
	require.GreaterOrEqual(t, len(errs), 1,
		"ValidateSchema must return at least one ValidationError for malformed XML")
	assert.Equal(t, codegen.LayerStructural, errs[0].Layer,
		"malformed XML error must be classified as LayerStructural")
}

// ─── TestGenerateSchema_RoundTripValidation ───────────────────────────────────

// TestGenerateSchema_RoundTripValidation verifies that the schema produced by
// GenerateSchema is valid according to ValidateSchema. Each layer is checked
// separately so that a single violation produces an actionable error message
// naming the layer and listing the first 3 violations.
//
// If ValidateSchema is still a stub (returns nil, nil), this test trivially
// passes with 0 errors in all layers. It will tighten once SLICE-C lands.
func TestGenerateSchema_RoundTripValidation(t *testing.T) {
	var buf bytes.Buffer
	err := codegen.GenerateSchema(&buf)
	require.NoError(t, err, "GenerateSchema must not error")

	errs, validErr := codegen.ValidateSchema(strings.NewReader(buf.String()))
	require.NoError(t, validErr,
		"ValidateSchema must not return an error for well-formed generated XML")

	// Group by layer.
	byLayer := map[codegen.ErrorLayer][]codegen.ValidationError{}
	for _, e := range errs {
		byLayer[e.Layer] = append(byLayer[e.Layer], e)
	}

	// Assert per-layer counts independently for clear failure messages.
	for _, layer := range []codegen.ErrorLayer{
		codegen.LayerStructural,
		codegen.LayerReferential,
		codegen.LayerSemantic,
	} {
		layerErrs := byLayer[layer]
		if len(layerErrs) > 0 {
			msgs := make([]string, 0, 3)
			for i, e := range layerErrs {
				if i >= 3 {
					break
				}
				msgs = append(msgs, "  "+e.ElementPath+": "+e.Message)
			}
			t.Errorf("%s layer errors: %d\n%s", layer, len(layerErrs), strings.Join(msgs, "\n"))
		}
	}
}

// ─── TestGenerateSchemaToFile_WriteMode ────────────────────────────────────────

// TestGenerateSchemaToFile_WriteMode verifies that GenerateSchemaToFile with
// Write=true creates the file on disk with valid XML content matching the
// returned string.
func TestGenerateSchemaToFile_WriteMode(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "schema.xml")
	opts := codegen.GenerateOptions{Diff: false, Write: true}

	content, err := codegen.GenerateSchemaToFile(outPath, opts)
	require.NoError(t, err, "GenerateSchemaToFile should not error")
	require.NotEmpty(t, content, "GenerateSchemaToFile should return non-empty content")

	// Read back from disk and verify it matches the returned content.
	diskContent, err := os.ReadFile(outPath)
	require.NoError(t, err, "should be able to read written schema.xml")
	assert.Equal(t, content, string(diskContent),
		"written file content should match returned content")

	// Verify the written file is valid XML.
	var doc interface{}
	decoder := xml.NewDecoder(strings.NewReader(string(diskContent)))
	err = decoder.Decode(&doc)
	assert.NoError(t, err, "written file must be valid XML")
}

// ─── TestGenerateSchema_PrettyPrinted ─────────────────────────────────────────

// TestGenerateSchema_PrettyPrinted verifies the output is indented (pretty-printed),
// not a single-line minified XML string.
func TestGenerateSchema_PrettyPrinted(t *testing.T) {
	output := generateXML(t)

	lines := strings.Split(output, "\n")
	assert.Greater(t, len(lines), 100,
		"output must be pretty-printed (many lines), got %d lines", len(lines))

	// At least some lines should start with whitespace (indentation).
	indented := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") {
			indented++
		}
	}
	assert.Greater(t, indented, 50,
		"output must have at least 50 indented lines, got %d", indented)
}

// ─── XML extraction helpers ───────────────────────────────────────────────────

// containsPhaseToken returns true if the comma-separated phaseRef string contains
// the given phase token as an exact word (not a substring).
// e.g. containsPhaseToken("p9,p10,p11", "p1") → false
//
//	containsPhaseToken("p9,p10,p11", "p10") → true
func containsPhaseToken(phaseRef, token string) bool {
	for _, part := range strings.Split(phaseRef, ",") {
		if strings.TrimSpace(part) == token {
			return true
		}
	}
	return false
}

// extractConstraintBlock returns the XML fragment containing the constraint with
// the given ID. It searches for the first line containing id="<cid>" and returns
// enough context to check role-ref and phase-ref attributes.
func extractConstraintBlock(xml, cid string) string {
	lines := strings.Split(xml, "\n")
	for i, line := range lines {
		if strings.Contains(line, `id="`+cid+`"`) &&
			strings.Contains(line, "constraint") {
			// Return this line and the next ~5 lines (for multi-line elements).
			end := i + 6
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[i:end], "\n")
		}
	}
	return ""
}

// extractAttrValue extracts the value of an XML attribute from a string fragment.
// e.g. extractAttrValue(`role-ref="epoch,supervisor"`, "role-ref") → "epoch,supervisor"
func extractAttrValue(fragment, attrName string) string {
	prefix := attrName + `="`
	start := strings.Index(fragment, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(fragment[start:], `"`)
	if end == -1 {
		return ""
	}
	return fragment[start : start+end]
}

// extractElementBlock returns the XML block for an element with the given tag
// and id attribute value. It collects lines from the opening tag until the
// matching closing tag.
func extractElementBlock(xmlContent, tag, id string) string {
	lines := strings.Split(xmlContent, "\n")
	startMarker := `<` + tag + ` `
	idAttr := `id="` + id + `"`
	closeTag := "</" + tag + ">"

	start := -1
	for i, line := range lines {
		if strings.Contains(line, startMarker) && strings.Contains(line, idAttr) {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}

	var sb strings.Builder
	depth := 0
	for i := start; i < len(lines); i++ {
		line := lines[i]
		sb.WriteString(line)
		sb.WriteString("\n")

		// Count open/close tags to find the matching closing tag.
		opens := strings.Count(line, "<"+tag)
		closes := strings.Count(line, closeTag)
		depth += opens - closes
		if depth <= 0 && i > start {
			break
		}
	}
	return sb.String()
}

// extractSubBlock returns the content of a named sub-element from a parent block.
// It returns everything between the opening and closing tags of the first
// occurrence of <subTag> within parentBlock.
func extractSubBlock(parentBlock, subTag string) string {
	openTag := "<" + subTag
	closeTag := "</" + subTag + ">"

	start := strings.Index(parentBlock, openTag)
	if start == -1 {
		return ""
	}
	end := strings.Index(parentBlock[start:], closeTag)
	if end == -1 {
		return parentBlock[start:]
	}
	return parentBlock[start : start+end+len(closeTag)]
}
