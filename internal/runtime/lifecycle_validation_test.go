package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

func TestLifecycleContractRejectsInvalidUnresolvedIdentityMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mapping    func() LifecycleEventMapping
		wantDetail string
	}{
		{
			name: "invalid kind",
			mapping: func() LifecycleEventMapping {
				mapping := validSessionStartMapping(t)
				mapping.unresolved = []NativeIdentityKind{0}
				return mapping
			},
			wantDetail: "invalid unresolved identity kind",
		},
		{
			name: "duplicate kind",
			mapping: func() LifecycleEventMapping {
				mapping := validSessionStartMapping(t)
				mapping.unresolved = []NativeIdentityKind{IdentityToolCall, IdentityToolCall}
				return mapping
			},
			wantDetail: "repeats unresolved identity kind",
		},
		{
			name: "resolved and unresolved kind",
			mapping: func() LifecycleEventMapping {
				mapping := validSessionStartMapping(t)
				mapping.unresolved = []NativeIdentityKind{IdentitySession}
				return mapping
			},
			wantDetail: "both resolved and unresolved",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := newLifecycleContract(
				ClaudeCode2_1_261(),
				[]ClaudeLifecycleEvent{ClaudeEventSessionStart},
				map[ClaudeLifecycleEvent]LifecycleEventMapping{
					ClaudeEventSessionStart: test.mapping(),
				},
			)
			if err == nil {
				t.Fatal("newLifecycleContract() error = nil, want validation failure")
			}

			var diagnostic *ir.Diagnostic
			if !errors.As(err, &diagnostic) {
				t.Fatalf("newLifecycleContract() error type = %T, want *ir.Diagnostic", err)
			}
			if diagnostic.Phase != "runtime contract validation" {
				t.Fatalf("error phase = %q, want runtime contract validation", diagnostic.Phase)
			}
			if !strings.Contains(diagnostic.What, test.wantDetail) {
				t.Fatalf("error problem = %q, want cause %q", diagnostic.What, test.wantDetail)
			}
			if diagnostic.Why == "" || diagnostic.Where == "" || diagnostic.Impact == "" || diagnostic.Fix == "" {
				t.Fatalf("validation error is not actionable: %#v", diagnostic)
			}
		})
	}
}

func TestLifecycleContractRejectsOptionalRequestForHumanResponse(t *testing.T) {
	t.Parallel()
	mapping := claudeLifecycleMappings()[ClaudeEventElicitationResult]
	mapping.identities = []NativeIdentityField{
		nativeIdentity(IdentitySession, "session_id", true),
		nativeIdentity(IdentityRequest, "request_id", false),
	}
	_, err := newLifecycleContract(
		ClaudeCode2_1_261(),
		[]ClaudeLifecycleEvent{ClaudeEventElicitationResult},
		map[ClaudeLifecycleEvent]LifecycleEventMapping{
			ClaudeEventElicitationResult: mapping,
		},
	)
	if err == nil {
		t.Fatal("newLifecycleContract() error = nil, want validation failure")
	}

	var diagnostic *ir.Diagnostic
	if !errors.As(err, &diagnostic) {
		t.Fatalf("newLifecycleContract() error type = %T, want *ir.Diagnostic", err)
	}
	if diagnostic.Phase != "runtime contract validation" {
		t.Fatalf("error phase = %q, want runtime contract validation", diagnostic.Phase)
	}
	if !strings.Contains(diagnostic.What, "no required native request identity") {
		t.Fatalf("error problem = %q, want required-request cause", diagnostic.What)
	}
	if diagnostic.Why == "" || diagnostic.Where == "" || diagnostic.Impact == "" || diagnostic.Fix == "" {
		t.Fatalf("validation error is not actionable: %#v", diagnostic)
	}
}

func validSessionStartMapping(t *testing.T) LifecycleEventMapping {
	t.Helper()
	mapping, ok := claudeLifecycleMappings()[ClaudeEventSessionStart]
	if !ok {
		t.Fatal("Claude lifecycle mappings do not contain SessionStart")
	}
	return mapping
}

// TestLifecycleContractBindsBlockingExitCodesToEvidence is the row-validation
// table for the failure-evidence rule. A blocking exit code refuses the user's
// prompt or tool call, so the row must cite where the host's blocking behavior
// was read. Without a citation, validation refuses the row and NAMES it, so a
// maintainer sees which row to fix.
//
// The table covers {blocking exit code, non-blocking mode} x {evidence, none},
// plus a padded citation, which is refused because a reader could not resolve
// it.
func TestLifecycleContractBindsBlockingExitCodesToEvidence(t *testing.T) {
	t.Parallel()

	const cited = "https://docs.claude.com/en/docs/claude-code/hooks"

	tests := []struct {
		name       string
		blocking   BlockingMode
		semantic   EventSemantic
		failure    FailureMode
		evidence   FailureEvidence
		wantDetail string
	}{
		{
			name:     "blocking exit code with evidence is accepted",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureExitTwoBlocks, evidence: FailureEvidence{Source: cited},
		},
		{
			name:     "strict blocking exit code with evidence is accepted",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureStrictExitTwoBlocks, evidence: FailureEvidence{Source: cited},
		},
		{
			name:     "blocking exit code with no evidence is refused",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureExitTwoBlocks, evidence: FailureEvidence{},
			wantDetail: "no failure evidence",
		},
		{
			name:     "strict blocking exit code with no evidence is refused",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureStrictExitTwoBlocks, evidence: FailureEvidence{},
			wantDetail: "no failure evidence",
		},
		{
			name:     "blocking exit code with a blank citation is refused",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureExitTwoBlocks, evidence: FailureEvidence{Source: "   "},
			wantDetail: "no failure evidence",
		},
		{
			name:     "report-and-continue with no evidence is accepted",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureReportAndContinue, evidence: FailureEvidence{},
		},
		{
			name:     "strict hook failure with no evidence is accepted",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureStrictHook, evidence: FailureEvidence{},
		},
		{
			name:     "observation with no evidence is accepted",
			blocking: NonBlocking, semantic: SemanticObservation,
			failure: FailureReportAndContinue, evidence: FailureEvidence{},
		},
		{
			name:     "observation with evidence is accepted",
			blocking: NonBlocking, semantic: SemanticObservation,
			failure: FailureReportAndContinue, evidence: FailureEvidence{Source: cited},
		},
		{
			name:     "a padded citation is refused",
			blocking: Blocking, semantic: SemanticGateConsultation,
			failure: FailureExitTwoBlocks, evidence: FailureEvidence{Source: cited + " "},
			wantDetail: "leading or trailing space",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			mapping := validSessionStartMapping(t)
			mapping.blocking = test.blocking
			mapping.semantic = test.semantic
			mapping.failure = test.failure
			mapping.evidence = test.evidence
			if test.semantic != SemanticObservation {
				mapping.reconciliation = ReconcileHostNative
			}

			_, err := newLifecycleContract(
				ClaudeCode2_1_261(),
				[]ClaudeLifecycleEvent{ClaudeEventSessionStart},
				map[ClaudeLifecycleEvent]LifecycleEventMapping{
					ClaudeEventSessionStart: mapping,
				},
			)

			if test.wantDetail == "" {
				if err != nil {
					t.Fatalf("newLifecycleContract() error = %v, want acceptance", err)
				}
				return
			}
			if err == nil {
				t.Fatal("newLifecycleContract() error = nil, want an evidence refusal")
			}
			var diagnostic *ir.Diagnostic
			if !errors.As(err, &diagnostic) {
				t.Fatalf("newLifecycleContract() error type = %T, want *ir.Diagnostic", err)
			}
			if !strings.Contains(diagnostic.What, test.wantDetail) {
				t.Fatalf("error problem = %q, want cause %q", diagnostic.What, test.wantDetail)
			}
			if !strings.Contains(diagnostic.What, mapping.nativeName) {
				t.Fatalf("error problem = %q does not NAME the row %q", diagnostic.What, mapping.nativeName)
			}
			if !strings.Contains(diagnostic.Fix, mapping.nativeName) {
				t.Fatalf("error fix = %q does not NAME the row %q", diagnostic.Fix, mapping.nativeName)
			}
			if diagnostic.Why == "" || diagnostic.Where == "" || diagnostic.Impact == "" || diagnostic.Fix == "" {
				t.Fatalf("evidence refusal is not actionable: %#v", diagnostic)
			}
		})
	}
}

// TestBlocksByExitCodeCoversOnlyTheTwoExitCodeArms pins which failure arms may
// claim a blocking exit code. The predicate decides which rows the evidence
// rule gates, so widening it silently would demand evidence from a plugin-throw
// row, and narrowing it silently would let an undocumented row block a user.
func TestBlocksByExitCodeCoversOnlyTheTwoExitCodeArms(t *testing.T) {
	t.Parallel()

	blocks := map[FailureMode]bool{
		FailureReportAndContinue:   false,
		FailureExitTwoBlocks:       true,
		FailureStrictHook:          false,
		FailureStrictExitTwoBlocks: true,
		FailureThrowFailFast:       false,
		FailureObserveOnly:         false,
	}
	for mode, want := range blocks {
		if got := mode.BlocksByExitCode(); got != want {
			t.Errorf("FailureMode %q BlocksByExitCode() = %t, want %t", mode, got, want)
		}
	}
	var unset FailureMode
	if unset.BlocksByExitCode() {
		t.Error("the zero FailureMode must never claim a blocking exit code")
	}
}

// TestPinnedProfilesCiteEvidenceForEveryBlockingExitCode pins the exact set of
// rows that keep a blocking exit code in the shipped profiles. Every other gate
// row runs as report-and-continue until its harness supplies a citation, so
// this list is the handover list for the harness slices.
func TestPinnedProfilesCiteEvidenceForEveryBlockingExitCode(t *testing.T) {
	t.Parallel()

	blocking := map[string]string{}
	for event, mapping := range claudeLifecycleMappings() {
		_ = event
		if mapping.failure.BlocksByExitCode() {
			blocking["claude:"+mapping.nativeName] = mapping.evidence.Source
		}
		if mapping.evidence.IsPresent() && !mapping.failure.BlocksByExitCode() {
			t.Errorf("Claude row %q cites evidence but claims no blocking exit code", mapping.nativeName)
		}
	}
	for _, mapping := range codexLifecycleMappings() {
		if mapping.failure.BlocksByExitCode() {
			blocking["codex:"+mapping.nativeName] = mapping.evidence.Source
		}
	}
	for _, mapping := range openCodeLifecycleMappings() {
		if mapping.failure.BlocksByExitCode() {
			blocking["opencode:"+mapping.nativeName] = mapping.evidence.Source
		}
	}

	want := map[string]string{
		"claude:UserPromptSubmit": claudeHooksReference,
		"claude:Stop":             claudeHooksReference,
		"claude:PreToolUse":       claudeHooksReference,
		"claude:SubagentStop":     claudeHooksReference,
		// Read from the installed binary's own hook-event table, not from the
		// hook reference: the reference does not name this event.
		"claude:PreModelSwitch": claudeInstalledBinaryHookTable2_1_261,
	}
	if len(blocking) != len(want) {
		t.Fatalf("rows claiming a blocking exit code = %v, want exactly %v", blocking, want)
	}
	for row, source := range want {
		got, found := blocking[row]
		if !found {
			t.Errorf("row %q lost its blocking exit code", row)
			continue
		}
		if got != source {
			t.Errorf("row %q cites %q, want %q", row, got, source)
		}
	}
}
