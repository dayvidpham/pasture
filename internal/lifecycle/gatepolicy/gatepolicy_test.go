package gatepolicy_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/gatepolicy"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func policyInput(episodes ...gateauthority.Episode) gatepolicy.Input {
	return gatepolicy.Input{
		Event:      registration.EventWorktreeCreate,
		Action:     gateauthority.ActionWorktree,
		Semantic:   runtime.SemanticGateConsultation,
		StopLoop:   runtime.StopLoopNotApplicable,
		Capability: runtime.CapabilityDeny,
		Claim:      gateauthority.SessionClaim{Bound: true, Known: true},
		Authority:  gateauthority.ActorAuthority{Episodes: episodes},
	}
}

func episode(role gateauthority.AssignmentRole, phase gateauthority.TaskPhase) gateauthority.Episode {
	return gateauthority.Episode{Role: role, Phase: phase}
}

func assertDecision(t *testing.T, in gatepolicy.Input, kind backend.DecisionKind, reason backend.DecisionReason) {
	t.Helper()
	got, err := gatepolicy.Decide(in)
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	d, ok := got.Decision()
	if !ok || d.Kind() != kind || d.Reason() != reason {
		t.Fatalf("decision = (%v, %s, %s), want (%s, %s)", ok, d.Kind(), d.Reason(), kind, reason)
	}
	if r, ok := got.Refusal(); ok || r != nil {
		t.Fatalf("decision also exposes refusal: %v", r)
	}
}

func assertRefusal(t *testing.T, in gatepolicy.Input, kind gatepolicy.RefusalKind) {
	t.Helper()
	got, err := gatepolicy.Decide(in)
	if err != nil {
		t.Fatalf("Decide returned error instead of typed refusal: %v", err)
	}
	r, ok := got.Refusal()
	if !ok || r == nil || r.Kind() != kind {
		t.Fatalf("refusal = (%v, %v), want kind %d", ok, r, kind)
	}
	if d, ok := got.Decision(); ok || d.IsValid() {
		t.Fatalf("fault exposes a policy decision: %v", d)
	}
}

func TestGatePolicyDecisionOrder(t *testing.T) {
	t.Parallel()
	allow := episode(gateauthority.RoleOwnerResponsibility, gateauthority.PhaseWorkerSlices)
	cases := []struct {
		name   string
		change func(*gatepolicy.Input)
		kind   backend.DecisionKind
		reason backend.DecisionReason
		fault  gatepolicy.RefusalKind
	}{
		{
			name: "observation_before_all_later_rules",
			change: func(in *gatepolicy.Input) {
				in.Semantic = runtime.SemanticObservation
				in.StopLoop = runtime.StopLoopConsultWhenInactive
				in.Claim.Bound = false
				in.Claim.Known = false
				in.Authority.Episodes = nil
				in.Action = gateauthority.ActionUnset
			},
			kind: backend.DecisionProceed, reason: backend.ReasonLegal,
		},
		{
			name: "stop_before_unbound_unknown_and_policy",
			change: func(in *gatepolicy.Input) {
				in.StopLoop = runtime.StopLoopConsultWhenInactive
				in.Claim.Bound = false
				in.Claim.Known = false
				in.Authority.Episodes = nil
				in.Action = gateauthority.ActionUnset
			},
			kind: backend.DecisionProceed, reason: backend.ReasonStopLoopGuard,
		},
		{
			name: "unbound_before_unknown_empty_and_policy",
			change: func(in *gatepolicy.Input) {
				in.Claim.Bound = false
				in.Claim.Known = false
				in.Authority.Episodes = nil
				in.Action = gateauthority.ActionUnset
			},
			kind: backend.DecisionProceed, reason: backend.ReasonUnboundSession,
		},
		{
			name: "unknown_before_empty_and_phase",
			change: func(in *gatepolicy.Input) {
				in.Claim.Known = false
				in.Authority.Episodes = nil
			},
			kind: backend.DecisionDeny, reason: backend.ReasonUnknownActor,
		},
		{
			name: "empty_before_unlisted_action",
			change: func(in *gatepolicy.Input) {
				in.Authority.Episodes = nil
				in.Action = gateauthority.ActionUnset
			},
			kind: backend.DecisionDeny, reason: backend.ReasonNoActiveAssignment,
		},
		{
			name: "phase_unknown_before_unlisted_cell",
			change: func(in *gatepolicy.Input) {
				in.Authority.Episodes = []gateauthority.Episode{
					episode(gateauthority.RoleUnset, gateauthority.PhaseWorkerSlices),
					episode(gateauthority.RoleOwnerResponsibility, gateauthority.TaskPhaseUnset),
				}
			},
			fault: gatepolicy.RefusalPhaseUnknown,
		},
		{
			name: "unlisted_cell_before_allow",
			change: func(in *gatepolicy.Input) {
				in.Authority.Episodes = []gateauthority.Episode{
					episode(gateauthority.RoleUnset, gateauthority.PhaseWorkerSlices),
					allow,
				}
			},
			fault: gatepolicy.RefusalPolicyUnlisted,
		},
		{
			name: "role_forbids_action",
			change: func(in *gatepolicy.Input) {
				in.Authority.Episodes[0].Role = gateauthority.RoleAxisReviewer
			},
			kind: backend.DecisionDeny, reason: backend.ReasonRoleForbidsAction,
		},
		{
			name: "phase_forbids_action",
			change: func(in *gatepolicy.Input) {
				in.Authority.Episodes[0].Phase = gateauthority.PhasePlanUAT
			},
			kind: backend.DecisionDeny, reason: backend.ReasonPhaseForbidsAction,
		},
		{
			name:   "one_episode_allows_role_and_phase",
			change: func(in *gatepolicy.Input) {},
			kind:   backend.DecisionProceed, reason: backend.ReasonLegal,
		},
		{
			name: "record_only_deny_after_policy",
			change: func(in *gatepolicy.Input) {
				in.Capability = runtime.CapabilityNone
				in.Authority.Episodes[0].Phase = gateauthority.PhasePlanUAT
			},
			kind: backend.DecisionProceed, reason: backend.ReasonUnenforcedDeny,
		},
		{
			name: "normal_deny_after_policy",
			change: func(in *gatepolicy.Input) {
				in.Authority.Episodes[0].Role = gateauthority.RoleAxisReviewer
			},
			kind: backend.DecisionDeny, reason: backend.ReasonRoleForbidsAction,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := policyInput(allow)
			tc.change(&in)
			if tc.fault.IsValid() {
				assertRefusal(t, in, tc.fault)
			} else {
				assertDecision(t, in, tc.kind, tc.reason)
			}
		})
	}
}

func TestDecideSameEpisodeAndReasonOrder(t *testing.T) {
	t.Parallel()
	roleDenied := episode(gateauthority.RoleAxisReviewer, gateauthority.PhaseWorkerSlices)
	phaseDenied := episode(gateauthority.RoleOwnerResponsibility, gateauthority.PhasePlanUAT)
	for _, reverse := range []bool{false, true} {
		name := "forward"
		if reverse {
			name = "reverse"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			episodes := []gateauthority.Episode{roleDenied, phaseDenied}
			if reverse {
				episodes[0], episodes[1] = episodes[1], episodes[0]
			}
			assertDecision(t, policyInput(episodes...), backend.DecisionDeny, backend.ReasonPhaseForbidsAction)
			recordOnly := policyInput(episodes...)
			recordOnly.Capability = runtime.CapabilityNone
			assertDecision(t, recordOnly, backend.DecisionProceed, backend.ReasonUnenforcedDeny)
			episodes = append(episodes, episode(gateauthority.RoleGoverningSupervisor, gateauthority.PhaseWorkerSlices))
			assertDecision(t, policyInput(episodes...), backend.DecisionProceed, backend.ReasonLegal)
		})
	}
	assertDecision(t, policyInput(roleDenied, episode(gateauthority.RoleAxisReviewer, gateauthority.PhasePlanUAT)), backend.DecisionDeny, backend.ReasonRoleForbidsAction)
}

func TestDecideAnyUnlistedCellBeforeAllowOrDeny(t *testing.T) {
	t.Parallel()
	for _, bad := range []gateauthority.Episode{
		episode(gateauthority.RoleUnset, gateauthority.PhaseWorkerSlices),
		episode(gateauthority.AssignmentRole(255), gateauthority.PhaseWorkerSlices),
		episode(gateauthority.RoleOwnerResponsibility, gateauthority.TaskPhase(255)),
	} {
		for _, other := range []gateauthority.Episode{
			episode(gateauthority.RoleOwnerResponsibility, gateauthority.PhaseWorkerSlices),
			episode(gateauthority.RoleAxisReviewer, gateauthority.PhasePlanUAT),
		} {
			for _, episodes := range [][]gateauthority.Episode{{bad, other}, {other, bad}} {
				in := policyInput(episodes...)
				in.Capability = runtime.CapabilityNone
				assertRefusal(t, in, gatepolicy.RefusalPolicyUnlisted)
			}
		}
	}
	in := policyInput(episode(gateauthority.RoleOwnerResponsibility, gateauthority.PhaseWorkerSlices))
	in.Action = gateauthority.ActionClass(255)
	assertRefusal(t, in, gatepolicy.RefusalPolicyUnlisted)
}

func TestDecideUnenforcedDeny(t *testing.T) {
	t.Parallel()
	for _, reason := range []backend.DecisionReason{
		backend.ReasonUnknownActor,
		backend.ReasonNoActiveAssignment,
		backend.ReasonRoleForbidsAction,
		backend.ReasonPhaseForbidsAction,
	} {
		t.Run(reason.String(), func(t *testing.T) {
			t.Parallel()
			in := policyInput()
			switch reason {
			case backend.ReasonUnknownActor:
				in.Claim.Known = false
			case backend.ReasonRoleForbidsAction:
				in.Authority.Episodes = []gateauthority.Episode{episode(gateauthority.RoleAxisReviewer, gateauthority.PhaseWorkerSlices)}
			case backend.ReasonPhaseForbidsAction:
				in.Authority.Episodes = []gateauthority.Episode{episode(gateauthority.RoleOwnerResponsibility, gateauthority.PhasePlanUAT)}
			}
			for _, capability := range []runtime.ResponseCapability{runtime.CapabilityNone, runtime.CapabilityDeny, runtime.CapabilityDenyAsk} {
				in.Capability = capability
				if capability == runtime.CapabilityNone {
					assertDecision(t, in, backend.DecisionProceed, backend.ReasonUnenforcedDeny)
				} else {
					assertDecision(t, in, backend.DecisionDeny, reason)
				}
			}
		})
	}
}

func TestDecideRejectsInvalidRuntimeVocabulary(t *testing.T) {
	t.Parallel()
	for _, value := range []uint8{0, 255} {
		for _, field := range []string{"semantic", "stop-loop", "capability"} {
			in := policyInput()
			switch field {
			case "semantic":
				in.Semantic = runtime.EventSemantic(value)
			case "stop-loop":
				in.StopLoop = runtime.StopLoopPolicy(value)
			case "capability":
				in.Capability = runtime.ResponseCapability(value)
			}
			got, err := gatepolicy.Decide(in)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("invalid %s %d: error = %v", field, value, err)
			}
			assertInvalidResult(t, got)
		}
	}
}

func TestInvalidCapabilityNeverBecomesEnforcementOrRecordOnly(t *testing.T) {
	t.Parallel()
	// Walk the underlying vocabulary, including every unsupported value, on
	// early proceeds, policy denies, legal episodes and authority faults.
	for value := 0; value <= 255; value++ {
		capability := runtime.ResponseCapability(value)
		if capability.IsValid() {
			continue
		}
		for _, scenario := range []string{"observation", "stop", "unbound", "unknown", "no-episode", "legal", "phase-unset", "unlisted"} {
			in := policyInput()
			in.Capability = capability
			switch scenario {
			case "observation":
				in.Semantic = runtime.SemanticObservation
			case "stop":
				in.StopLoop = runtime.StopLoopConsultWhenInactive
			case "unbound":
				in.Claim.Bound = false
			case "unknown":
				in.Claim.Known = false
			case "legal":
				in.Authority.Episodes = []gateauthority.Episode{episode(gateauthority.RoleOwnerResponsibility, gateauthority.PhaseWorkerSlices)}
			case "phase-unset":
				in.Authority.Episodes = []gateauthority.Episode{episode(gateauthority.RoleOwnerResponsibility, gateauthority.TaskPhaseUnset)}
			case "unlisted":
				in.Authority.Episodes = []gateauthority.Episode{episode(gateauthority.RoleUnset, gateauthority.PhaseWorkerSlices)}
			}
			got, err := gatepolicy.Decide(in)
			if err == nil || !strings.Contains(err.Error(), "capability") {
				t.Fatalf("capability %d on %s: error = %v", value, scenario, err)
			}
			assertInvalidResult(t, got)
		}
	}
}

func TestDecideConsumesPublishedTablesWithoutMutationOrRequireHuman(t *testing.T) {
	t.Parallel()
	for _, role := range gateauthority.AssignmentRoles() {
		for _, phase := range gateauthority.TaskPhases() {
			for _, action := range gateauthority.ActionClasses() {
				in := policyInput(episode(role, phase))
				in.Action = action
				in.Capability = runtime.CapabilityDenyAsk
				in.Semantic = runtime.SemanticExplicitHumanResponse
				before := in
				before.Authority.Episodes = append([]gateauthority.Episode(nil), in.Authority.Episodes...)
				roleVerdict := gateauthority.RoleVerdict(role, action)
				phaseVerdict := gateauthority.PhaseVerdict(phase, action)
				switch {
				case roleVerdict == gateauthority.VerdictRefuse || phaseVerdict == gateauthority.VerdictRefuse:
					assertRefusal(t, in, gatepolicy.RefusalPolicyUnlisted)
				case roleVerdict == gateauthority.VerdictDeny:
					assertDecision(t, in, backend.DecisionDeny, backend.ReasonRoleForbidsAction)
				case phaseVerdict == gateauthority.VerdictDeny:
					assertDecision(t, in, backend.DecisionDeny, backend.ReasonPhaseForbidsAction)
				default:
					assertDecision(t, in, backend.DecisionProceed, backend.ReasonLegal)
				}
				if !reflect.DeepEqual(before, in) {
					t.Fatalf("Decide changed caller facts for %s/%s/%s", role, phase, action)
				}
				if gateauthority.RoleVerdict(role, action) != roleVerdict || gateauthority.PhaseVerdict(phase, action) != phaseVerdict {
					t.Fatalf("Decide changed a published table for %s/%s/%s", role, phase, action)
				}
			}
		}
	}
}

func assertInvalidResult(t *testing.T, got gatepolicy.Result) {
	t.Helper()
	if d, ok := got.Decision(); ok || d.IsValid() {
		t.Fatalf("invalid result exposes decision %v", d)
	}
	if r, ok := got.Refusal(); ok || r != nil {
		t.Fatalf("invalid result exposes refusal %v", r)
	}
}

func TestResultOwnsItsUnion(t *testing.T) {
	t.Parallel()
	assertInvalidResult(t, gatepolicy.Result{})
	assertInvalidResult(t, gatepolicy.Decided(backend.Decision{}))
	assertInvalidResult(t, gatepolicy.Refused(gatepolicy.Refusal{}))
	for _, typ := range []reflect.Type{reflect.TypeOf(gatepolicy.Result{}), reflect.TypeOf(gatepolicy.Refusal{})} {
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				t.Fatalf("%s exposes writable union field %s", typ, typ.Field(i).Name)
			}
		}
	}
	d, err := backend.NewDecision(backend.DecisionProceed, backend.ReasonLegal)
	if err != nil {
		t.Fatal(err)
	}
	decided := gatepolicy.Decided(d)
	d = backend.Decision{}
	if got, ok := decided.Decision(); !ok || got.Reason() != backend.ReasonLegal {
		t.Fatal("Decided did not retain its valid decision value")
	}
	if got, ok := decided.Refusal(); ok || got != nil {
		t.Fatal("Decided exposes a refusal")
	}
	for _, kind := range []gatepolicy.RefusalKind{gatepolicy.RefusalPhaseUnknown, gatepolicy.RefusalPolicyUnlisted} {
		r, err := gatepolicy.NewRefusal(kind)
		if err != nil || !r.IsValid() {
			t.Fatalf("NewRefusal(%d): %v", kind, err)
		}
		result := gatepolicy.Refused(r)
		r = gatepolicy.Refusal{}
		copy, ok := result.Refusal()
		if !ok || copy == nil || copy.Kind() != kind {
			t.Fatalf("Refused did not retain kind %d", kind)
		}
		*copy = gatepolicy.Refusal{}
		again, ok := result.Refusal()
		if !ok || again == nil || again.Kind() != kind {
			t.Fatal("caller changed Result through returned refusal pointer")
		}
		if got, ok := result.Decision(); ok || got.IsValid() {
			t.Fatal("Refused exposes a decision")
		}
		if !strings.Contains(again.Error(), "no policy decision") || !strings.Contains(again.Error(), "gatepolicy.Decide") {
			t.Fatalf("fault diagnostic misrepresents policy: %s", again.Error())
		}
	}
	if gatepolicy.RefusalPhaseUnknown != 1 || gatepolicy.RefusalPolicyUnlisted != 2 {
		t.Fatalf("refusal kinds are phase=%d and policy=%d; want the retired arm to leave phase=1 and policy=2", gatepolicy.RefusalPhaseUnknown, gatepolicy.RefusalPolicyUnlisted)
	}
	for _, kind := range []gatepolicy.RefusalKind{gatepolicy.RefusalUnset, 255} {
		if r, err := gatepolicy.NewRefusal(kind); err == nil || r.IsValid() {
			t.Fatalf("invalid refusal kind %d accepted", kind)
		}
	}
}

func TestInputHasOnlyPolicyFacts(t *testing.T) {
	t.Parallel()
	// Pin the whole boundary, not a substring such as "complete": a renamed
	// watermark or a nested snapshot must not become a policy condition either.
	want := map[string]reflect.Type{
		"Event":      reflect.TypeOf(model.ContractEventKind(0)),
		"Action":     reflect.TypeOf(gateauthority.ActionClass(0)),
		"Semantic":   reflect.TypeOf(runtime.EventSemantic(0)),
		"StopLoop":   reflect.TypeOf(runtime.StopLoopPolicy(0)),
		"Capability": reflect.TypeOf(runtime.ResponseCapability(0)),
		"Claim":      reflect.TypeOf(gateauthority.SessionClaim{}),
		"Authority":  reflect.TypeOf(gateauthority.ActorAuthority{}),
	}
	typ := reflect.TypeOf(gatepolicy.Input{})
	if typ.NumField() != len(want) {
		t.Fatalf("Input has %d fields, want only %d policy facts", typ.NumField(), len(want))
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if want[field.Name] != field.Type {
			t.Fatalf("unexpected input fact %s %s", field.Name, field.Type)
		}
	}
	// The imported values must not hide a completeness fact inside Input.
	for typ, names := range map[reflect.Type][]string{
		reflect.TypeOf(gateauthority.ActorAuthority{}): {"Actor", "Episodes"},
		reflect.TypeOf(gateauthority.Episode{}):        {"Assignment", "Task", "Role", "Phase"},
		reflect.TypeOf(gateauthority.SessionClaim{}):   {"Bound", "Actor", "Session", "Known"},
	} {
		if typ.NumField() != len(names) {
			t.Fatalf("%s adds facts beyond the policy boundary", typ)
		}
		for i, name := range names {
			if typ.Field(i).Name != name {
				t.Fatalf("%s field %d = %s, want %s", typ, i, typ.Field(i).Name, name)
			}
		}
	}
}
