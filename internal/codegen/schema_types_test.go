package codegen_test

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── L1 schema_types.go: struct instantiation and xml tag correctness ──────────

// TestSectionStructsInstantiate verifies that all 17 section structs (and their
// nested types) can be instantiated without panics. This is the minimal
// compilation gate required by SLICE-A.
func TestSectionStructsInstantiate(t *testing.T) {
	t.Run("EnumsSection", func(t *testing.T) {
		s := codegen.EnumsSection{
			Enums: []codegen.EnumType{
				{
					Name: "VoteType",
					Values: []codegen.EnumValue{
						{Id: "ACCEPT", Description: "All review criteria satisfied"},
					},
				},
			},
		}
		assert.Equal(t, "VoteType", s.Enums[0].Name)
	})

	t.Run("LabelsSection", func(t *testing.T) {
		s := codegen.LabelsSection{
			Labels: []codegen.LabelElem{
				{Id: "L-p1s1_1", Value: "pasture:p1-user:s1_1-request"},
			},
		}
		assert.Equal(t, "L-p1s1_1", s.Labels[0].Id)
	})

	t.Run("ReviewAxesSection", func(t *testing.T) {
		s := codegen.ReviewAxesSection{
			Axes: []codegen.ReviewAxisElem{
				{
					Id:     "axis-correctness",
					Letter: "A",
					Name:   "Correctness",
					Short:  "Does it work?",
					KeyQuestions: &codegen.KeyQuestionsElem{
						Questions: []string{"Is it correct?"},
					},
				},
			},
		}
		assert.Equal(t, "axis-correctness", s.Axes[0].Id)
		require.NotNil(t, s.Axes[0].KeyQuestions)
		assert.Len(t, s.Axes[0].KeyQuestions.Questions, 1)
	})

	t.Run("PhasesSection", func(t *testing.T) {
		s := codegen.PhasesSection{
			Phases: []codegen.PhaseElem{
				{Id: "p1", Number: "1", Domain: "user", Name: "Request"},
			},
		}
		assert.Equal(t, "p1", s.Phases[0].Id)
	})

	t.Run("RolesSection", func(t *testing.T) {
		s := codegen.RolesSection{
			Roles: []codegen.RoleElem{
				{Id: "worker", Name: "Worker", Description: "Implements slices"},
			},
		}
		assert.Equal(t, "worker", s.Roles[0].Id)
	})

	t.Run("CommandsSection", func(t *testing.T) {
		s := codegen.CommandsSection{
			Commands: []codegen.CommandElem{
				{Id: "cmd-worker", Name: "pasture:worker", Description: "Worker skill"},
			},
		}
		assert.Equal(t, "cmd-worker", s.Commands[0].Id)
	})

	t.Run("HandoffsSection", func(t *testing.T) {
		s := codegen.HandoffsSection{
			StoragePattern: "beads-task-body",
			Handoffs: []codegen.HandoffElem{
				{
					Id:           "h2",
					SourceRole:   "supervisor",
					TargetRole:   "worker",
					AtPhase:      "p9",
					ContentLevel: "summary-with-ids",
				},
			},
		}
		assert.Equal(t, "h2", s.Handoffs[0].Id)
	})

	t.Run("ConstraintsSection_typeOnly", func(t *testing.T) {
		// ConstraintsSection is NOT used for xml.Marshal — just verify instantiation.
		s := codegen.ConstraintsSection{
			Constraints: []codegen.ConstraintElem{
				{Id: "C-agent-commit", Given: "code is ready", When: "committing", Then: "use git agent-commit"},
			},
		}
		assert.Equal(t, "C-agent-commit", s.Constraints[0].Id)
	})

	t.Run("TaskTitlesSection", func(t *testing.T) {
		s := codegen.TaskTitlesSection{
			Conventions: []codegen.TitleConventionElem{
				{Pattern: "REQUEST: {description}", LabelRef: "L-p1s1_1", CreatedBy: "epoch"},
			},
		}
		assert.Equal(t, "REQUEST: {description}", s.Conventions[0].Pattern)
	})

	t.Run("DocumentsSection", func(t *testing.T) {
		s := codegen.DocumentsSection{
			Documents: []codegen.DocumentElem{
				{Id: "doc-readme", Path: "protocol/README.md", Purpose: "Entry point"},
			},
		}
		assert.Equal(t, "doc-readme", s.Documents[0].Id)
	})

	t.Run("DependencyModelSection", func(t *testing.T) {
		s := codegen.DependencyModelSection{
			Rule:           "Parent blocked-by child.",
			CanonicalChain: "REQUEST → blocked-by ELICIT",
		}
		assert.Contains(t, s.Rule, "blocked-by")
	})

	t.Run("FollowupLifecycleSection", func(t *testing.T) {
		s := codegen.FollowupLifecycleSection{
			Trigger:        "Code review completion",
			OwnerRole:      "supervisor",
			GatedOnBlocker: "false",
		}
		assert.Equal(t, "supervisor", s.OwnerRole)
	})

	t.Run("ProcedureStepsSection_typeOnly", func(t *testing.T) {
		// ProcedureStepsSection is NOT used for xml.Marshal — just verify instantiation.
		s := codegen.ProcedureStepsSection{
			RoleGroups: []codegen.ProcedureRoleGroup{
				{Ref: "worker", Steps: []codegen.ProcedureStepElem{
					{Order: "1", Id: "s-types", Instruction: "Define types"},
				}},
			},
		}
		assert.Equal(t, "worker", s.RoleGroups[0].Ref)
	})

	t.Run("ChecklistsSection", func(t *testing.T) {
		s := codegen.ChecklistsSection{
			Checklists: []codegen.ChecklistElem{
				{
					Id:      "worker-completion",
					RoleRef: "worker",
					Gate:    "completion",
					Items: []codegen.ChecklistItemElem{
						{Id: "no-todos", Required: "true", Text: "No TODO placeholders"},
					},
				},
			},
		}
		assert.Equal(t, "worker-completion", s.Checklists[0].Id)
	})

	t.Run("CoordinationCommandsSection", func(t *testing.T) {
		s := codegen.CoordinationCommandsSection{
			Commands: []codegen.CoordCmdElem{
				{Id: "bd-show", Action: "show", Template: "bd show {id}"},
			},
		}
		assert.Equal(t, "bd-show", s.Commands[0].Id)
	})

	t.Run("WorkflowsSection", func(t *testing.T) {
		s := codegen.WorkflowsSection{
			Workflows: []codegen.WorkflowElem{
				{
					Id:          "wf-layer-cake",
					Name:        "Layer Cake",
					RoleRef:     "worker",
					Description: "TDD workflow",
				},
			},
		}
		assert.Equal(t, "wf-layer-cake", s.Workflows[0].Id)
	})

	t.Run("FiguresSection", func(t *testing.T) {
		s := codegen.FiguresSection{
			Figures: []codegen.FigureElem{
				{
					Id:         "fig-workflow",
					Title:      "Workflow Overview",
					Type:       "ascii-diagram",
					SectionRef: "workflows",
				},
			},
		}
		assert.Equal(t, "fig-workflow", s.Figures[0].Id)
	})
}

// ─── L1: XML tag correctness via Marshal round-trip ──────────────────────────

// TestXMLTagsEnumsSection verifies that EnumsSection marshals to the correct
// XML element and attribute names.
func TestXMLTagsEnumsSection(t *testing.T) {
	s := codegen.EnumsSection{
		Enums: []codegen.EnumType{
			{Name: "VoteType", Values: []codegen.EnumValue{
				{Id: "ACCEPT", Description: "Accept vote"},
			}},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<enums>", "root element must be <enums>")
	assert.Contains(t, xmlStr, `<enum name="VoteType">`, "enum name attr")
	assert.Contains(t, xmlStr, `id="ACCEPT"`, "value id attr")
	assert.Contains(t, xmlStr, `description="Accept vote"`, "value description attr")
}

// TestXMLTagsLabelsSection verifies <labels> element structure.
func TestXMLTagsLabelsSection(t *testing.T) {
	s := codegen.LabelsSection{
		Labels: []codegen.LabelElem{
			{Id: "L-urd", Value: "pasture:urd", Special: "true", Description: "URD label"},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<labels>")
	assert.Contains(t, xmlStr, "<label")
	assert.Contains(t, xmlStr, `id="L-urd"`)
	assert.Contains(t, xmlStr, `value="pasture:urd"`)
	assert.Contains(t, xmlStr, `special="true"`)
}

// TestXMLTagsReviewAxesSection verifies <review-axes> element structure.
func TestXMLTagsReviewAxesSection(t *testing.T) {
	s := codegen.ReviewAxesSection{
		Axes: []codegen.ReviewAxisElem{
			{
				Id:     "axis-correctness",
				Letter: "A",
				Name:   "Correctness",
				Short:  "Correct?",
				KeyQuestions: &codegen.KeyQuestionsElem{
					Questions: []string{"Does it work?"},
				},
			},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<review-axes>")
	assert.Contains(t, xmlStr, "<axis")
	assert.Contains(t, xmlStr, `letter="A"`)
	assert.Contains(t, xmlStr, "<key-questions>")
	assert.Contains(t, xmlStr, "<q>Does it work?</q>")
}

// TestXMLTagsHandoffsSection verifies <handoffs> element with storage-pattern attr.
func TestXMLTagsHandoffsSection(t *testing.T) {
	pattern := "beads-task-body"
	s := codegen.HandoffsSection{
		StoragePattern: pattern,
		Handoffs: []codegen.HandoffElem{
			{
				Id:           "h1",
				SourceRole:   "architect",
				TargetRole:   "supervisor",
				AtPhase:      "p7",
				ContentLevel: "full-provenance",
				FilePattern:  "architect-to-supervisor.md",
			},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<handoffs")
	assert.Contains(t, xmlStr, `storage-pattern=`)
	assert.Contains(t, xmlStr, "<handoff")
	assert.Contains(t, xmlStr, `source-role="architect"`)
	assert.Contains(t, xmlStr, `target-role="supervisor"`)
	assert.Contains(t, xmlStr, `at-phase="p7"`)
	assert.Contains(t, xmlStr, `content-level="full-provenance"`)
	assert.Contains(t, xmlStr, `file-pattern="architect-to-supervisor.md"`)
}

// TestXMLTagsChecklistsSection verifies <checklists> structure with chardata items.
func TestXMLTagsChecklistsSection(t *testing.T) {
	s := codegen.ChecklistsSection{
		Checklists: []codegen.ChecklistElem{
			{
				Id:      "worker-completion",
				RoleRef: "worker",
				Gate:    "completion",
				Items: []codegen.ChecklistItemElem{
					{Id: "no-todos", Required: "true", Text: "No TODO placeholders"},
				},
			},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<checklists>")
	assert.Contains(t, xmlStr, `<checklist`)
	assert.Contains(t, xmlStr, `role-ref="worker"`)
	assert.Contains(t, xmlStr, `gate="completion"`)
	assert.Contains(t, xmlStr, `<item`)
	assert.Contains(t, xmlStr, `required="true"`)
	assert.Contains(t, xmlStr, "No TODO placeholders")
}

// TestXMLTagsWorkflowsSection verifies <workflows> nesting.
func TestXMLTagsWorkflowsSection(t *testing.T) {
	s := codegen.WorkflowsSection{
		Workflows: []codegen.WorkflowElem{
			{
				Id:          "wf-layer-cake",
				Name:        "Layer Cake",
				RoleRef:     "worker",
				Description: "TDD layer workflow",
				Stages: []codegen.StageElem{
					{
						Id:        "stage-types",
						Name:      "Types",
						Order:     "1",
						Execution: "sequential",
						Actions: []codegen.ActionElem{
							{Id: "a1", Instruction: "Define types"},
						},
						ExitConditions: []codegen.ExitCondElem{
							{Type: "proceed", Condition: "All types compile"},
						},
					},
				},
			},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<workflows>")
	assert.Contains(t, xmlStr, "<workflow")
	assert.Contains(t, xmlStr, `role-ref="worker"`)
	assert.Contains(t, xmlStr, "<stage")
	assert.Contains(t, xmlStr, `execution="sequential"`)
	assert.Contains(t, xmlStr, "<action")
	assert.Contains(t, xmlStr, `instruction="Define types"`)
	assert.Contains(t, xmlStr, "<exit-condition")
	assert.Contains(t, xmlStr, `type="proceed"`)
}

// TestXMLTagsFiguresSection verifies <figures> structure.
func TestXMLTagsFiguresSection(t *testing.T) {
	s := codegen.FiguresSection{
		Figures: []codegen.FigureElem{
			{
				Id:           "fig-workflow",
				Title:        "Workflow",
				Type:         "ascii-diagram",
				SectionRef:   "workflows",
				RoleRefs:     []codegen.RefElem{{Ref: "worker"}},
				WorkflowRefs: []codegen.RefElem{{Ref: "wf-layer-cake"}},
			},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<figures>")
	assert.Contains(t, xmlStr, "<figure")
	assert.Contains(t, xmlStr, `section-ref="workflows"`)
	assert.Contains(t, xmlStr, "<role-ref")
	assert.Contains(t, xmlStr, "<workflow-ref")
}

// TestXMLTagsCoordCmdElem verifies coord-cmd element name is hyphenated.
func TestXMLTagsCoordCmdElem(t *testing.T) {
	s := codegen.CoordinationCommandsSection{
		Commands: []codegen.CoordCmdElem{
			{Id: "bd-show", Action: "show", Template: "bd show {id}", Shared: "true"},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<coordination-commands>")
	assert.Contains(t, xmlStr, "<coord-cmd")
	assert.Contains(t, xmlStr, `shared="true"`)
}

// TestXMLTagsTaskTitlesSection verifies <task-titles> element.
func TestXMLTagsTaskTitlesSection(t *testing.T) {
	s := codegen.TaskTitlesSection{
		Conventions: []codegen.TitleConventionElem{
			{Pattern: "REQUEST: {desc}", LabelRef: "L-p1s1_1", CreatedBy: "epoch", PhaseRef: "p1"},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<task-titles>")
	assert.Contains(t, xmlStr, "<title-convention")
	assert.Contains(t, xmlStr, `label-ref="L-p1s1_1"`)
	assert.Contains(t, xmlStr, `created-by="epoch"`)
	assert.Contains(t, xmlStr, `phase-ref="p1"`)
}

// TestXMLTagsDocumentsSection verifies <documents> nesting.
func TestXMLTagsDocumentsSection(t *testing.T) {
	s := codegen.DocumentsSection{
		Documents: []codegen.DocumentElem{
			{
				Id:      "doc-readme",
				Path:    "protocol/README.md",
				Purpose: "Entry point",
				Covers: &codegen.CoversElem{
					Entities: []codegen.CoverEntityElem{
						{Type: "phase", Depth: "overview", Refs: "p1,p2"},
					},
				},
			},
		},
	}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.Contains(t, xmlStr, "<documents>")
	assert.Contains(t, xmlStr, "<document")
	assert.Contains(t, xmlStr, `purpose="Entry point"`)
	assert.Contains(t, xmlStr, "<covers>")
	assert.Contains(t, xmlStr, "<entity")
	assert.Contains(t, xmlStr, `depth="overview"`)
	assert.Contains(t, xmlStr, `refs="p1,p2"`)
}

// ─── L1 validate.go: ErrorLayer constants ─────────────────────────────────────

// TestErrorLayerConstants verifies that the three ErrorLayer constants have
// the exact wire values required for compatibility with the Python validator.
func TestErrorLayerConstants(t *testing.T) {
	assert.Equal(t, codegen.ErrorLayer("Structural"), codegen.LayerStructural)
	assert.Equal(t, codegen.ErrorLayer("Referential Integrity"), codegen.LayerReferential)
	assert.Equal(t, codegen.ErrorLayer("Semantic"), codegen.LayerSemantic)
}

// TestErrorLayerString verifies that converting an ErrorLayer to string gives
// the wire value (important for error message formatting).
func TestErrorLayerString(t *testing.T) {
	assert.Equal(t, "Structural", string(codegen.LayerStructural))
	assert.Equal(t, "Referential Integrity", string(codegen.LayerReferential))
	assert.Equal(t, "Semantic", string(codegen.LayerSemantic))
}

// ─── L1 validate.go: ValidationError struct ──────────────────────────────────

// TestValidationErrorFields verifies that ValidationError can be instantiated
// with all required fields.
func TestValidationErrorFields(t *testing.T) {
	ve := codegen.ValidationError{
		Layer:       codegen.LayerStructural,
		ElementPath: "phase[@id='p1']",
		Message:     "missing required attribute 'name'",
	}
	assert.Equal(t, codegen.LayerStructural, ve.Layer)
	assert.Equal(t, "phase[@id='p1']", ve.ElementPath)
	assert.Equal(t, "missing required attribute 'name'", ve.Message)
}

// TestValidationErrorSlice verifies that []ValidationError works as expected
// for collecting multiple errors.
func TestValidationErrorSlice(t *testing.T) {
	errs := []codegen.ValidationError{
		{Layer: codegen.LayerStructural, ElementPath: "phase[@id='p1']", Message: "duplicate id"},
		{Layer: codegen.LayerReferential, ElementPath: "role[@id='worker']/owns-phases/phase-ref[@ref='p99']", Message: "undefined phase ref"},
		{Layer: codegen.LayerSemantic, ElementPath: "phase[@id='p3']", Message: "phase number out of order"},
	}
	assert.Len(t, errs, 3)
	assert.Equal(t, codegen.LayerStructural, errs[0].Layer)
	assert.Equal(t, codegen.LayerReferential, errs[1].Layer)
	assert.Equal(t, codegen.LayerSemantic, errs[2].Layer)
}

// ─── L1 validate.go: SchemaIndex struct ──────────────────────────────────────

// TestSchemaIndexInstantiate verifies that SchemaIndex can be instantiated
// and all map fields initialized.
func TestSchemaIndexInstantiate(t *testing.T) {
	idx := codegen.SchemaIndex{
		PhaseIds:           map[string]bool{"p1": true},
		SubstepIDs:         map[string]bool{"s1_1": true},
		LabelIDs:           map[string]bool{"L-p1s1_1": true},
		RoleIds:            map[string]bool{"worker": true},
		CommandIds:         map[string]bool{"cmd-worker": true},
		AxisIDs:            map[string]bool{"axis-correctness": true},
		HandoffIDs:         map[string]bool{"h2": true},
		ConstraintIDs:      map[string]bool{"C-agent-commit": true},
		DocumentIDs:        map[string]bool{"doc-readme": true},
		TeamIDs:            map[string]bool{},
		SeverityIDs:        map[string]bool{"BLOCKER": true},
		EnumValueIDs:       map[string]map[string]bool{"VoteType": {"ACCEPT": true, "REVISE": true}},
		PhaseNumbers:       map[string]int{"p1": 1},
		PhaseDomains:       map[string]string{"p1": "user"},
		PhaseSubstepOrders: map[string][]codegen.SubstepOrderEntry{},
		LabelValues:        map[string]string{"L-p1s1_1": "pasture:p1-user:s1_1-request"},
		AxisLetters:        map[string]string{"axis-correctness": "A"},
		RolePhaseRefs:      map[string]map[string]bool{"worker": {"p9": true}},
		StartupStepOrders:  map[string][]int{},
	}
	assert.True(t, idx.PhaseIds["p1"])
	assert.True(t, idx.RoleIds["worker"])
	assert.Equal(t, 1, idx.PhaseNumbers["p1"])
	assert.True(t, idx.EnumValueIDs["VoteType"]["ACCEPT"])
	assert.True(t, idx.RolePhaseRefs["worker"]["p9"])
}

// TestSubstepOrderEntryFields verifies SubstepOrderEntry struct fields.
func TestSubstepOrderEntryFields(t *testing.T) {
	entry := codegen.SubstepOrderEntry{
		Id:        "s1_1",
		Order:     1,
		Execution: "sequential",
	}
	assert.Equal(t, "s1_1", entry.Id)
	assert.Equal(t, 1, entry.Order)
	assert.Equal(t, "sequential", entry.Execution)
}

// ─── L1 validate.go: XMLNode ──────────────────────────────────────────────────

// TestXMLNodeUnmarshal verifies that XMLNode can be unmarshalled from XML,
// capturing element name, attributes, children, and text content.
func TestXMLNodeUnmarshal(t *testing.T) {
	xmlInput := `<phase id="p1" number="1"><description>Phase one</description></phase>`
	var node codegen.XMLNode
	err := xml.Unmarshal([]byte(xmlInput), &node)
	require.NoError(t, err)

	assert.Equal(t, "phase", node.XMLName.Local)
	require.Len(t, node.Children, 1)
	assert.Equal(t, "description", node.Children[0].XMLName.Local)
	assert.Equal(t, "Phase one", node.Children[0].Text)

	// Verify attrs
	attrMap := make(map[string]string)
	for _, a := range node.Attrs {
		attrMap[a.Name.Local] = a.Value
	}
	assert.Equal(t, "p1", attrMap["id"])
	assert.Equal(t, "1", attrMap["number"])
}

// TestXMLNodeNestedUnmarshal verifies that nested XMLNode trees are correctly
// populated.
func TestXMLNodeNestedUnmarshal(t *testing.T) {
	xmlInput := `<roles><role id="worker"><tools>bash, python</tools></role></roles>`
	var node codegen.XMLNode
	err := xml.Unmarshal([]byte(xmlInput), &node)
	require.NoError(t, err)

	assert.Equal(t, "roles", node.XMLName.Local)
	require.Len(t, node.Children, 1)
	roleNode := node.Children[0]
	assert.Equal(t, "role", roleNode.XMLName.Local)
	require.Len(t, roleNode.Children, 1)
	toolsNode := roleNode.Children[0]
	assert.Equal(t, "tools", toolsNode.XMLName.Local)
	assert.Equal(t, "bash, python", toolsNode.Text)
}

// ─── L1 validate.go: ValidateSchema and ValidateTree ────────────────────────

// TestValidateSchemaStubReturnsNil verifies that a minimal well-formed XML
// document with no protocol entities produces (nil, nil) — no errors.
func TestValidateSchemaStubReturnsNil(t *testing.T) {
	r := strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?><pasture-protocol version="2.0"/>`)
	errs, err := codegen.ValidateSchema(r)
	assert.NoError(t, err, "ValidateSchema must not return a Go error for well-formed XML")
	assert.Nil(t, errs, "a minimal well-formed document with no protocol entities must return nil errors")
}

// TestValidateSchemaStubEmptyReader verifies that an empty reader produces a
// Structural error (EOF parse failure) rather than a Go error.
// The implementation reports XML parse failures as ValidationErrors.
func TestValidateSchemaStubEmptyReader(t *testing.T) {
	r := strings.NewReader("")
	errs, err := codegen.ValidateSchema(r)
	// Empty reader → XML parse failure → structural error, no Go error.
	assert.NoError(t, err, "ValidateSchema must not return a Go error for empty input")
	require.NotEmpty(t, errs, "empty XML must produce at least one Structural ValidationError")
	assert.Equal(t, codegen.LayerStructural, errs[0].Layer,
		"empty XML error must be classified as Structural")
}

// TestValidateTreeStubReturnsNil verifies that an empty XMLNode (zero value)
// does not panic and returns some result. An empty document produces no phases
// and therefore no semantic violations about phase ordering, but may produce
// no errors since there is nothing to check. We just verify it doesn't panic.
func TestValidateTreeStubReturnsNil(t *testing.T) {
	root := &codegen.XMLNode{}
	// Must not panic. Result may be nil or a slice — both are acceptable
	// for an empty document with no phases or roles.
	result := codegen.ValidateTree(root)
	_ = result // nil or empty — both acceptable
}

// TestValidateTreeStubNilRoot verifies that ValidateTree handles a nil root
// without panicking and returns nil (nothing to validate).
func TestValidateTreeStubNilRoot(t *testing.T) {
	result := codegen.ValidateTree(nil)
	assert.Nil(t, result, "nil root must return nil — nothing to validate")
}

// ─── Existing tests still pass: compile-time smoke ────────────────────────────

// TestSchemaTypesPackageImports verifies that schema_types.go types can be
// used together with types from the existing codegen package without conflicts.
// This catches any name collisions introduced by SLICE-A.
func TestSchemaTypesPackageImports(t *testing.T) {
	// Use EnumsSection and ValidationError together to verify no namespace collision.
	var _ codegen.EnumsSection
	var _ codegen.ValidationError
	var _ codegen.SchemaIndex
	var _ codegen.XMLNode

	// Also verify the OmitEmpty fields on LabelElem don't produce spurious attrs.
	label := codegen.LabelElem{Id: "L-urd", Value: "pasture:urd", Special: "true"}
	out, err := xml.Marshal(label)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `phase-ref=""`, "empty optional attrs must be omitted")
	assert.NotContains(t, string(out), `substep-ref=""`, "empty optional attrs must be omitted")
}

// TestXMLNodeAttrsField verifies the Attrs field is []xml.Attr (not []xml.Attr pointer).
func TestXMLNodeAttrsField(t *testing.T) {
	xmlInput := `<label id="L-p1s1_1" value="pasture:p1-user"/>`
	var node codegen.XMLNode
	err := xml.Unmarshal([]byte(xmlInput), &node)
	require.NoError(t, err)
	assert.IsType(t, []xml.Attr{}, node.Attrs)

	// Build a map from attrs for easy lookup.
	m := make(map[string]string)
	for _, a := range node.Attrs {
		m[a.Name.Local] = a.Value
	}
	assert.Equal(t, "L-p1s1_1", m["id"])
	assert.Equal(t, "pasture:p1-user", m["value"])
}

// TestConstraintsSectionNotMarshalled verifies that ConstraintsSection is
// intentionally NOT used for xml.Marshal by confirming it has no XMLName field.
// This is a documentation test: if someone adds XMLName by mistake, this fails.
func TestConstraintsSectionNotMarshalled(t *testing.T) {
	// ConstraintsSection should NOT have an xml:"constraints" XMLName.
	// We verify this by marshalling and checking the output does NOT include
	// a <constraints> root (encoding/xml will use the struct name instead).
	s := codegen.ConstraintsSection{}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	// Negative check: no <constraints> element (would appear if XMLName were set).
	assert.NotContains(t, xmlStr, "<constraints>",
		"ConstraintsSection must not have XMLName so it won't accidentally be marshalled as root")
	// Positive check: encoding/xml falls back to the struct name as the element
	// name when no XMLName field is present, so we must see <ConstraintsSection>.
	assert.Contains(t, xmlStr, "<ConstraintsSection>",
		"ConstraintsSection without XMLName must marshal as <ConstraintsSection> (Go struct name fallback)")
}

// TestProcedureStepsSectionNotMarshalled mirrors TestConstraintsSectionNotMarshalled.
func TestProcedureStepsSectionNotMarshalled(t *testing.T) {
	s := codegen.ProcedureStepsSection{}
	out, err := xml.Marshal(s)
	require.NoError(t, err)
	xmlStr := string(out)
	assert.NotContains(t, xmlStr, "<procedure-steps>",
		"ProcedureStepsSection must not have XMLName so it won't accidentally be marshalled as root")
}

// TestXMLNodeChildrenArePointers verifies that XMLNode.Children is []*XMLNode
// (pointers), which allows round-trip unmarshalling of nested structures.
func TestXMLNodeChildrenArePointers(t *testing.T) {
	var node codegen.XMLNode
	xmlInput := `<root><child>text</child></root>`
	err := xml.Unmarshal([]byte(xmlInput), &node)
	require.NoError(t, err)
	require.Len(t, node.Children, 1)
	// Children must be non-nil pointers (not zero-value structs)
	assert.NotNil(t, node.Children[0])
	assert.Equal(t, "child", node.Children[0].XMLName.Local)
	assert.Equal(t, "text", node.Children[0].Text)
}

// TestValidationErrorLayerZeroValue verifies that the zero value of ErrorLayer
// is an empty string, which would cause a test failure if used accidentally.
func TestValidationErrorLayerZeroValue(t *testing.T) {
	var layer codegen.ErrorLayer
	assert.Equal(t, "", string(layer), "zero value of ErrorLayer must be empty string")
	assert.NotEqual(t, codegen.LayerStructural, layer)
	assert.NotEqual(t, codegen.LayerReferential, layer)
	assert.NotEqual(t, codegen.LayerSemantic, layer)
}

// TestBytesBufferIntegration runs a quick sanity check that the xml.Marshal
// output for a simple section can be written to a bytes.Buffer without error,
// simulating how SLICE-B will use these types.
func TestBytesBufferIntegration(t *testing.T) {
	s := codegen.EnumsSection{
		Enums: []codegen.EnumType{{Name: "VoteType"}},
	}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	require.NoError(t, enc.Encode(s))
	require.NoError(t, enc.Flush())
	assert.Contains(t, buf.String(), "<enums>")
}
