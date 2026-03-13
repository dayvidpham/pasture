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

// ── Compile-time assertion ────────────────────────────────────────────────────

// Verify ParseXMLNode is exported with correct signature.
var _ func(r io.Reader, out *codegen.XMLNode) error = codegen.ParseXMLNode
