package codegen_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── errReader ──────────────────────────────────────────────────────────────────

// errReader is an io.Reader that always returns an error, used to test the
// io.Reader failure branch of ValidateSchema.
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }

// ── TestValidateSchema_IOError ────────────────────────────────────────────────

// TestValidateSchema_IOError uses an errReader and expects:
//   - (nil, non-nil error) — the Go error is propagated, no ValidationErrors.
func TestValidateSchema_IOError(t *testing.T) {
	sentinel := errors.New("disk read failure")
	errs, err := codegen.ValidateSchema(&errReader{err: sentinel})
	assert.Error(t, err, "ValidateSchema must propagate io.Reader errors")
	assert.Nil(t, errs, "no ValidationErrors should be returned when read fails")
	assert.ErrorIs(t, err, sentinel, "wrapped error must chain back to the sentinel")
}

// ── TestXMLNode_Helpers ───────────────────────────────────────────────────────

// TestXMLNode_Helpers verifies Iter, Find, FindAll, and Attr on a minimal
// parsed tree.
func TestXMLNode_Helpers(t *testing.T) {
	const xmlDoc = `<root id="r1">
  <child id="c1" value="hello"/>
  <child id="c2" value="world"/>
  <other id="o1"/>
</root>`

	var root codegen.XMLNode
	require.NoError(t, codegen.ParseXMLNode(strings.NewReader(xmlDoc), &root))

	t.Run("Attr", func(t *testing.T) {
		assert.Equal(t, "r1", root.Attr("id"))
		assert.Equal(t, "", root.Attr("nonexistent"))
	})

	t.Run("Find", func(t *testing.T) {
		child := root.Find("child")
		require.NotNil(t, child, "Find must locate first child with matching tag")
		assert.Equal(t, "c1", child.Attr("id"))

		assert.Nil(t, root.Find("missing"), "Find must return nil for absent tag")
	})

	t.Run("FindAll", func(t *testing.T) {
		children := root.FindAll("child")
		require.Len(t, children, 2, "FindAll must return all direct children with matching tag")
		assert.Equal(t, "c1", children[0].Attr("id"))
		assert.Equal(t, "c2", children[1].Attr("id"))

		assert.Empty(t, root.FindAll("missing"), "FindAll must return empty for absent tag")
	})

	t.Run("Iter", func(t *testing.T) {
		// Iter searches recursively including self.
		all := root.Iter("child")
		require.Len(t, all, 2, "Iter must find all matching nodes recursively")

		// Iter on root matches root itself.
		roots := root.Iter("root")
		require.Len(t, roots, 1)
		assert.Equal(t, "r1", roots[0].Attr("id"))

		assert.Empty(t, root.Iter("missing"), "Iter must return empty for absent tag")
	})
}

// ── TestValidateTree_EmptyRoot ────────────────────────────────────────────────

// TestValidateTree_EmptyRoot exercises ValidateTree with a completely empty
// XMLNode (zero value). The validator must not panic and may return findings
// but must not crash.
func TestValidateTree_EmptyRoot(t *testing.T) {
	var root codegen.XMLNode
	// Must not panic.
	errs := codegen.ValidateTree(&root)
	// An empty root has no phases — that's handled gracefully.
	assert.Empty(t, errs, "empty root should produce no validation errors")
}

// ── TestValidateSchema_StructuralErrors ──────────────────────────────────────

// TestValidateSchema_StructuralErrors feeds a minimal document with a missing
// required attribute and expects a Structural error.
func TestValidateSchema_StructuralErrors(t *testing.T) {
	// A phase element without the required "number" attribute.
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L-p1s1"/>
    </phase>
  </phases>
  <labels>
    <label id="L-p1s1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
</aura-protocol>`

	errs, err := codegen.ValidateSchema(strings.NewReader(xmlDoc))
	assert.NoError(t, err)
	require.NotEmpty(t, errs)
	// At least one Structural error for missing "number" attribute.
	hasStructural := false
	for _, e := range errs {
		if e.Layer == codegen.LayerStructural {
			hasStructural = true
			break
		}
	}
	assert.True(t, hasStructural, "expected at least one Structural error for missing 'number' attribute")
}

// ── TestValidateSchema_ReferentialErrors ─────────────────────────────────────

// TestValidateSchema_ReferentialErrors feeds a document where a substep
// label-ref points to a non-existent label ID.
func TestValidateSchema_ReferentialErrors(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="NONEXISTENT"/>
    </phase>
  </phases>
  <labels/>
</aura-protocol>`

	errs, err := codegen.ValidateSchema(strings.NewReader(xmlDoc))
	assert.NoError(t, err)
	require.NotEmpty(t, errs)

	hasRef := false
	for _, e := range errs {
		if e.Layer == codegen.LayerReferential {
			hasRef = true
			break
		}
	}
	assert.True(t, hasRef, "expected at least one Referential error for dangling label-ref")
}

// ── TestValidateSchema_SemanticErrors ────────────────────────────────────────

// TestValidateSchema_SemanticErrors verifies that non-sequential phase numbers
// produce a Semantic error.
func TestValidateSchema_SemanticErrors(t *testing.T) {
	// Two phases with numbers 1 and 3 (missing 2).
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1_1" type="request" execution="sequential" order="1" label-ref="L-p1s1"/>
    </phase>
    <phase id="p3" number="3" domain="plan" name="Propose">
      <substep id="s3_1" type="propose" execution="sequential" order="1" label-ref="L-p3s1"/>
    </phase>
  </phases>
  <labels>
    <label id="L-p1s1" value="aura:p1-user:s1-request" special="true"/>
    <label id="L-p3s1" value="aura:p3-plan:s1-propose" special="true"/>
  </labels>
</aura-protocol>`

	errs, err := codegen.ValidateSchema(strings.NewReader(xmlDoc))
	assert.NoError(t, err)
	require.NotEmpty(t, errs)

	hasSemantic := false
	for _, e := range errs {
		if e.Layer == codegen.LayerSemantic && e.ElementPath == "phases" {
			hasSemantic = true
			break
		}
	}
	assert.True(t, hasSemantic, "expected Semantic error for non-sequential phase numbers")
}

// ── TestValidateSchema_DuplicateId ────────────────────────────────────────────

// TestValidateSchema_DuplicateId verifies that the structural validation layer
// (checkIDUnique inside buildIndex) catches duplicate element IDs and reports
// a LayerStructural ValidationError naming the duplicated id.
func TestValidateSchema_DuplicateId(t *testing.T) {
	// Two phases with the same id "p1" — checkIDUnique must fire on the second.
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request"/>
    <phase id="p1" number="2" domain="plan" name="Duplicate"/>
  </phases>
</aura-protocol>`

	errs, err := codegen.ValidateSchema(strings.NewReader(xmlDoc))
	assert.NoError(t, err, "ValidateSchema must not return a Go error for valid XML with duplicate IDs")
	require.NotEmpty(t, errs, "ValidateSchema must return ValidationErrors for duplicate phase IDs")

	hasDuplicate := false
	for _, e := range errs {
		if e.Layer == codegen.LayerStructural && strings.Contains(e.Message, "duplicate") && strings.Contains(e.Message, "p1") {
			hasDuplicate = true
			break
		}
	}
	assert.True(t, hasDuplicate,
		"expected a LayerStructural error with 'duplicate' and 'p1' in the message; got: %v", errs)
}

// ── Compile-time assertion ────────────────────────────────────────────────────

// Verify ParseXMLNode is exported with correct signature.
var _ func(r io.Reader, out *codegen.XMLNode) error = codegen.ParseXMLNode

// ── Per-rule semantic tests (B-I1) ────────────────────────────────────────────
//
// Each sub-test creates a minimal synthetic XML document that triggers exactly
// one semantic rule violation. Real schema.xml is never used; inputs are
// hand-crafted to exercise one rule in isolation.

// semanticErrors is a helper that parses xmlDoc and returns only LayerSemantic
// errors from ValidateTree.
func semanticErrors(t *testing.T, xmlDoc string) []codegen.ValidationError {
	t.Helper()
	var root codegen.XMLNode
	require.NoError(t, codegen.ParseXMLNode(strings.NewReader(xmlDoc), &root))
	all := codegen.ValidateTree(&root)
	var sem []codegen.ValidationError
	for _, e := range all {
		if e.Layer == codegen.LayerSemantic {
			sem = append(sem, e)
		}
	}
	return sem
}

// TestSemanticRule1_PhaseNumbersNotSequential verifies that non-contiguous
// phase numbers (e.g. 1, 3 — missing 2) produce a Semantic error at "phases".
func TestSemanticRule1_PhaseNumbersNotSequential(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="A">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
    <phase id="p3" number="3" domain="plan" name="C">
      <substep id="s3" type="propose" execution="sequential" order="1" label-ref="L3"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
    <label id="L3" value="aura:p3-plan:s1-propose" special="true"/>
  </labels>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for non-sequential phase numbers")
	found := false
	for _, e := range errs {
		if e.ElementPath == "phases" && strings.Contains(e.Message, "not sequential") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error at ElementPath='phases' with 'not sequential' in message; got %+v", errs)
}

// TestSemanticRule2_PhaseDomainInconsistency verifies that a phase whose
// domain attribute does not match the expected domain for that phase number
// produces a Semantic error.
func TestSemanticRule2_PhaseDomainInconsistency(t *testing.T) {
	// Phase 1 should be domain "user" but we supply "plan".
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="plan" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for domain inconsistency")
	found := false
	for _, e := range errs {
		if e.ElementPath == "phase[@id='p1']" && strings.Contains(e.Message, "should be 'user'") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about wrong domain for phase 1; got %+v", errs)
}

// TestSemanticRule3_PhaseHasNoSubsteps verifies that a phase with no substeps
// produces a Semantic error.
func TestSemanticRule3_PhaseHasNoSubsteps(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
    </phase>
  </phases>
  <labels/>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for phase with no substeps")
	found := false
	for _, e := range errs {
		if e.ElementPath == "phase[@id='p1']" && strings.Contains(e.Message, "no substeps") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error 'phase has no substeps'; got %+v", errs)
}

// TestSemanticRule4_SubstepOrdersNotSequential verifies that non-sequential
// substep order values within a phase produce a Semantic error.
func TestSemanticRule4_SubstepOrdersNotSequential(t *testing.T) {
	// Orders 1 and 3 — missing order 2.
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
      <substep id="s3" type="review"  execution="sequential" order="3" label-ref="L3"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
    <label id="L3" value="aura:p1-user:s3-review"  special="true"/>
  </labels>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for non-sequential substep orders")
	found := false
	for _, e := range errs {
		if e.ElementPath == "phase[@id='p1']" && strings.Contains(e.Message, "substep orders not sequential") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about substep orders; got %+v", errs)
}

// TestSemanticRule5_ParallelSubstepMissingGroup verifies that a parallel
// substep without parallel-group or an <instances> child produces a Semantic
// error.
func TestSemanticRule5_ParallelSubstepMissingGroup(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="review" execution="parallel" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-review" special="true"/>
  </labels>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for parallel substep without parallel-group")
	found := false
	for _, e := range errs {
		if strings.Contains(e.ElementPath, "p1") && strings.Contains(e.Message, "parallel-group") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about missing parallel-group; got %+v", errs)
}

// TestSemanticRule6_DuplicateLabelValue verifies that two labels with the
// same value attribute produce a Semantic error.
func TestSemanticRule6_DuplicateLabelValue(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
    <label id="L2" value="aura:p1-user:s1-request" special="true"/>
  </labels>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for duplicate label value")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "duplicate value") && strings.Contains(e.Message, "aura:p1-user:s1-request") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about duplicate label value; got %+v", errs)
}

// TestSemanticRule6_DuplicateLabelValue_Deterministic verifies that rule 6
// produces the same error order on repeated runs (map iteration must not
// affect output order).
func TestSemanticRule6_DuplicateLabelValue_Deterministic(t *testing.T) {
	// Four labels: L1 and L3 share "val-a", L2 and L4 share "val-b".
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="val-a" special="true"/>
    <label id="L2" value="val-b" special="true"/>
    <label id="L3" value="val-a" special="true"/>
    <label id="L4" value="val-b" special="true"/>
  </labels>
</aura-protocol>`

	// Run 20 times and assert identical results each time.
	var reference []codegen.ValidationError
	for i := 0; i < 20; i++ {
		got := semanticErrors(t, xmlDoc)
		if i == 0 {
			reference = got
		} else {
			require.Equal(t, reference, got, "rule 6 error order must be deterministic (iteration %d differs)", i+1)
		}
	}
	// Sanity: at least the two duplicate errors are present.
	require.GreaterOrEqual(t, len(reference), 2, "expected at least 2 duplicate-value errors")
}

// TestSemanticRule9_RoleOwnsNoPhases verifies that a role with an empty
// <owns-phases> block produces a Semantic error.
func TestSemanticRule9_RoleOwnsNoPhases(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
  <roles>
    <role id="r1" name="Worker">
      <owns-phases/>
    </role>
  </roles>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for role owning no phases")
	found := false
	for _, e := range errs {
		if e.ElementPath == "role[@id='r1']" && strings.Contains(e.Message, "no phases") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error 'role owns no phases'; got %+v", errs)
}

// TestSemanticRule10_CommandHasPhasesButNoFile verifies that a command element
// with a <phases> child but no <file> child produces a Semantic error.
func TestSemanticRule10_CommandHasPhasesButNoFile(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
  <commands>
    <command id="cmd1" name="do-thing">
      <phases/>
    </command>
  </commands>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for command with phases but no file")
	found := false
	for _, e := range errs {
		if strings.Contains(e.ElementPath, "cmd1") && strings.Contains(e.Message, "no <file> child") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about missing <file> child; got %+v", errs)
}

// TestSemanticRule11_DuplicateAxisLetter verifies that two review axes
// sharing the same letter produce a Semantic error.
func TestSemanticRule11_DuplicateAxisLetter(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
  <review-framework>
    <axis id="axis-a" letter="A" name="First"/>
    <axis id="axis-b" letter="A" name="Second"/>
  </review-framework>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for duplicate axis letter")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "duplicate letter 'A'") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about duplicate axis letter 'A'; got %+v", errs)
}

// TestSemanticRule12_StartupSequenceNotSequential verifies that a startup
// sequence with non-sequential step orders produces a Semantic error.
func TestSemanticRule12_StartupSequenceNotSequential(t *testing.T) {
	// Step orders 1 and 3 — missing 2.
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1">
        <startup-sequence>
          <step order="1" description="First"/>
          <step order="3" description="Third"/>
        </startup-sequence>
      </substep>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for non-sequential startup sequence steps")
	found := false
	for _, e := range errs {
		if strings.Contains(e.ElementPath, "startup-sequence") && strings.Contains(e.Message, "not sequential") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about startup-sequence step orders; got %+v", errs)
}

// TestSemanticRule13_AgentTemplateMinCountExceedsMax verifies that an
// agent-template with min-count > max-count produces a Semantic error.
func TestSemanticRule13_AgentTemplateMinCountExceedsMax(t *testing.T) {
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="user" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
  <roles>
    <role id="r1" name="Worker">
      <owns-phases>
        <phase-ref ref="p1"/>
      </owns-phases>
      <standing-teams>
        <team id="t1">
          <agent-template role="worker" skill-ref="cmd1" invocation="auto" min-count="5" max-count="2"/>
        </team>
      </standing-teams>
    </role>
  </roles>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for min-count > max-count")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "min-count") && strings.Contains(e.Message, "max-count") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about min-count > max-count; got %+v", errs)
}

// TestSemanticRule14_DomainNotInEnum verifies that a phase with a domain
// value absent from the DomainType enum produces a Semantic error.
func TestSemanticRule14_DomainNotInEnum(t *testing.T) {
	// DomainType enum defines "user","plan","impl". "unknown" is not in it.
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="unknown" name="Request">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="aura:p1-user:s1-request" special="true"/>
  </labels>
  <enums>
    <enum name="DomainType">
      <value id="user" description="User domain"/>
      <value id="plan" description="Plan domain"/>
      <value id="impl" description="Impl domain"/>
    </enum>
  </enums>
</aura-protocol>`

	errs := semanticErrors(t, xmlDoc)
	require.NotEmpty(t, errs, "expected a Semantic error for domain not in DomainType enum")
	found := false
	for _, e := range errs {
		if e.ElementPath == "phase[@id='p1']" && strings.Contains(e.Message, "'unknown'") && strings.Contains(e.Message, "DomainType") {
			found = true
		}
	}
	assert.True(t, found, "expected Semantic error about domain not in DomainType enum; got %+v", errs)
}

// TestSemanticRule14_DomainNotInEnum_Deterministic verifies that rule 14
// produces the same error order on repeated runs and that the rule 14 errors
// are emitted in sorted phase-id order.
func TestSemanticRule14_DomainNotInEnum_Deterministic(t *testing.T) {
	// Three phases whose domains are absent from the DomainType enum.
	// The document also triggers rule 2 (domain inconsistency) for each phase,
	// so we filter to only rule 14 errors (containing "DomainType") when
	// checking sort order.
	const xmlDoc = `<aura-protocol>
  <phases>
    <phase id="p1" number="1" domain="bogus" name="A">
      <substep id="s1" type="request" execution="sequential" order="1" label-ref="L1"/>
    </phase>
    <phase id="p2" number="2" domain="bogus" name="B">
      <substep id="s2" type="review" execution="sequential" order="1" label-ref="L2"/>
    </phase>
    <phase id="p3" number="3" domain="bogus" name="C">
      <substep id="s3" type="propose" execution="sequential" order="1" label-ref="L3"/>
    </phase>
  </phases>
  <labels>
    <label id="L1" value="v1" special="true"/>
    <label id="L2" value="v2" special="true"/>
    <label id="L3" value="v3" special="true"/>
  </labels>
  <enums>
    <enum name="DomainType">
      <value id="user" description="User domain"/>
      <value id="plan" description="Plan domain"/>
      <value id="impl" description="Impl domain"/>
    </enum>
  </enums>
</aura-protocol>`

	// rule14Only filters semantic errors to those produced by rule 14
	// (domain not in DomainType enum).
	rule14Only := func(errs []codegen.ValidationError) []codegen.ValidationError {
		var out []codegen.ValidationError
		for _, e := range errs {
			if strings.Contains(e.Message, "DomainType") {
				out = append(out, e)
			}
		}
		return out
	}

	var reference []codegen.ValidationError
	for i := 0; i < 20; i++ {
		got := rule14Only(semanticErrors(t, xmlDoc))
		if i == 0 {
			reference = got
		} else {
			require.Equal(t, reference, got, "rule 14 error order must be deterministic (iteration %d differs)", i+1)
		}
	}
	require.Len(t, reference, 3, "expected exactly 3 domain-not-in-enum errors (one per phase)")
	// Verify they are in ascending ElementPath order.
	for k := 1; k < len(reference); k++ {
		assert.LessOrEqual(t, reference[k-1].ElementPath, reference[k].ElementPath,
			"rule 14 errors must be sorted by ElementPath")
	}
}
