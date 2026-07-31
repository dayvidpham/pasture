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
				ClaudeCode2_1_210(),
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

func validSessionStartMapping(t *testing.T) LifecycleEventMapping {
	t.Helper()
	mapping, ok := claudeLifecycleMappings()[ClaudeEventSessionStart]
	if !ok {
		t.Fatal("Claude lifecycle mappings do not contain SessionStart")
	}
	return mapping
}
