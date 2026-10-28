package execution_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

type boundary struct {
	Name   string `yaml:"name"`
	Value  string `yaml:"value"`
	Repeat int    `yaml:"repeat"`
	Suffix string `yaml:"suffix"`
	Bytes  []byte `yaml:"bytes"`
	Valid  bool   `yaml:"valid"`
}

type roleCase struct {
	Name         string           `yaml:"name"`
	Native       *string          `yaml:"native"`
	Environment  *string          `yaml:"environment"`
	Registry     *string          `yaml:"registry"`
	WantNative   execution.Status `yaml:"want_native"`
	WantProtocol execution.Status `yaml:"want_protocol"`
	Source       execution.Source `yaml:"source"`
	Value        string           `yaml:"value"`
	Reason       execution.Reason `yaml:"reason"`
}

type validationCorpus struct {
	Boundaries []boundary         `yaml:"boundaries"`
	Roles      []roleCase         `yaml:"roles"`
	Kinds      []execution.Kind   `yaml:"kinds"`
	Members    []execution.Member `yaml:"members"`
}

func TestValidationFixtures(t *testing.T) {
	t.Parallel()
	var corpus validationCorpus
	testutil.LoadFixtures(t, testutil.ExecutionValidation, &corpus)
	for _, tc := range corpus.Boundaries {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			value := strings.Repeat(tc.Value, tc.Repeat) + tc.Suffix
			if tc.Bytes != nil {
				value = string(tc.Bytes)
			}
			if execution.ValidValue(value) != tc.Valid {
				t.Fatal("value boundary classification differs")
			}
			for source := 0; source < execution.MaxCandidates; source++ {
				var models [3]*string
				models[source] = &value
				in := input(models, [3]*string{})
				got, err := execution.Normalize(in)
				if err != nil {
					t.Fatal(err)
				}
				if tc.Valid {
					if got.Model.Status != execution.Observed || got.Model.Value != value {
						t.Fatal("valid evidence lost")
					}
				} else {
					if got.Model.Status != execution.Invalid || got.Model.Value != "" || len(got.Model.Candidates) != 0 {
						t.Fatalf("invalid raw retained: %#v", got.Model)
					}
					encoded, err := json.Marshal(got.Model)
					if err != nil {
						t.Fatal(err)
					}
					// Exact content-free output proves no prefix, hash, marker or
					// rejected raw text was added, including supplied empty values.
					want, _ := json.Marshal(execution.AttributionValue{Status: execution.Invalid, Source: got.Model.Source, Field: got.Model.Field, Reason: execution.RejectedInput})
					if string(encoded) != string(want) {
						t.Fatal("invalid output includes unexpected evidence")
					}
				}
			}
			in := execution.Inputs{Kind: execution.Primary, Host: execution.Declaration{
				Provider: execution.Raw{Present: true, Member: execution.HostProvider, Value: value},
				Model:    execution.Raw{Present: true, Member: execution.HostModelID, Value: "M"},
				Variant:  execution.Raw{Present: true, Member: execution.HostVariant, Value: value},
				Role:     execution.Raw{Present: true, Member: execution.HostAgent, Value: value},
			}}
			got, err := execution.Normalize(in)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := execution.Invalid
			if tc.Valid {
				wantStatus = execution.Observed
			}
			for _, field := range []execution.AttributionValue{got.Provider, got.Variant, got.Role} {
				if field.Status != wantStatus {
					t.Fatalf("independent field validation differs: %#v", field)
				}
				if !tc.Valid && (field.Value != "" || len(field.Candidates) != 0) {
					t.Fatal("invalid field retained raw input")
				}
			}
			in.Environment.ProtocolRole = execution.Raw{Present: true, Member: execution.EnvironmentRole, Value: value}
			got, err = execution.Normalize(in)
			if err != nil || got.ProtocolRole.Status != execution.Invalid || got.ProtocolRole.Value != "" || len(got.ProtocolRole.Candidates) != 0 {
				t.Fatal("non-enum or malformed protocol role admitted")
			}
		})
	}
	for _, tc := range corpus.Roles {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			in := execution.Inputs{Kind: execution.Selection}
			if tc.Native != nil {
				in.Host.Role = execution.Raw{Present: true, Member: execution.HostAgentType, Value: *tc.Native}
			}
			if tc.Environment != nil {
				in.Environment.ProtocolRole = execution.Raw{Present: true, Member: execution.EnvironmentRole, Value: *tc.Environment}
			}
			if tc.Registry != nil {
				in.ActorRegistry.ProtocolRole = execution.Raw{Present: true, Member: execution.RegistryRole, Value: *tc.Registry}
			}
			got, err := execution.Normalize(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Role.Status != tc.WantNative || got.ProtocolRole.Status != tc.WantProtocol || got.Role.Vocabulary != execution.NativeAgent || got.ProtocolRole.Vocabulary != execution.PastureRole {
				t.Fatalf("role vocabularies/statuses differ: %#v", got)
			}
			if tc.Native != nil && got.Role.Value != *tc.Native {
				t.Fatal("native role rewritten")
			}
			if tc.Source != "" && (got.ProtocolRole.Source != tc.Source || got.ProtocolRole.Value != tc.Value || got.ProtocolRole.Reason != tc.Reason) {
				t.Fatalf("protocol provenance differs: %#v", got.ProtocolRole)
			}
			if tc.WantProtocol == execution.Conflict && (len(got.ProtocolRole.Candidates) != 2 || got.ProtocolRole.Candidates[0].Source != execution.Environment || got.ProtocolRole.Candidates[1].Source != execution.ActorRegistry) {
				t.Fatal("protocol conflict candidate provenance lost")
			}
			if tc.Name == "invalid-plus-valid-role" && (len(got.ProtocolRole.Candidates) != 1 || got.ProtocolRole.Candidates[0].Value != "worker") {
				t.Fatal("valid protocol candidate lost")
			}
		})
	}
	for _, kind := range corpus.Kinds {
		t.Run(string(kind), func(t *testing.T) {
			in := execution.Inputs{Kind: kind, Scope: execution.Scope{AgentID: "child-1", TurnID: "turn-1", RequestID: "request-1"}}
			got, err := execution.Normalize(in)
			if err != nil || got.Kind != kind || got.Scope != in.Scope {
				t.Fatalf("kind/scope lost: %#v %v", got, err)
			}
		})
	}
	for _, member := range corpus.Members {
		t.Run(string(member), func(t *testing.T) {
			got, err := execution.Normalize(execution.Inputs{Kind: execution.Selection, Host: execution.Declaration{Model: execution.Raw{Present: true, Member: member, Value: "model-with-variant-suffix"}}})
			if err != nil || got.Model.Field != member || got.Model.Value != "model-with-variant-suffix" || got.Variant.Status != execution.Unavailable {
				t.Fatalf("member rewritten or suffix guessed: %#v %v", got, err)
			}
		})
	}
}

func TestRejectStructuralInputsWithoutEchoingContent(t *testing.T) {
	t.Parallel()
	secret := "never-echo-this-sensitive-input"
	in := execution.Inputs{Kind: execution.Kind(secret)}
	_, err := execution.Normalize(in)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("unsupported kind was accepted or echoed")
	}
	in.Kind = execution.Primary
	in.Scope.AgentID = " " + secret
	_, err = execution.Normalize(in)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("invalid scope was accepted or echoed")
	}
	in.Scope = execution.Scope{}
	in.Host.Model = execution.Raw{Present: true, Member: execution.Member(secret), Value: secret}
	_, err = execution.Normalize(in)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("unsupported member was accepted or echoed")
	}
	in.Host.Model = execution.Raw{Value: secret}
	_, err = execution.Normalize(in)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("absent data was accepted or echoed")
	}
}

func TestClosedEnums(t *testing.T) {
	t.Parallel()
	if execution.Status("future").IsValid() || execution.Source("future").IsValid() || execution.Kind("future").IsValid() || execution.Vocabulary("future").IsValid() || execution.Reason("future").IsValid() {
		t.Fatal("unknown enum admitted")
	}
	if !execution.Observed.IsValid() || !execution.Unavailable.IsValid() || !execution.Invalid.IsValid() || !execution.Conflict.IsValid() || !execution.None.IsValid() || !execution.Host.IsValid() || !execution.Environment.IsValid() || !execution.ActorRegistry.IsValid() || !execution.NativeAgent.IsValid() || !execution.PastureRole.IsValid() || !execution.NoEvidence.IsValid() || !execution.RejectedInput.IsValid() || !execution.ValidDisagreement.IsValid() || !execution.ValidDisagreementWithRejectedInput.IsValid() || !execution.UnpairedProvider.IsValid() {
		t.Fatal("known enum refused")
	}
}

func TestVariantAndRolesCannotSupplyModelAnchor(t *testing.T) {
	t.Parallel()
	in := execution.Inputs{Kind: execution.Compaction, Host: execution.Declaration{
		Model:    execution.Raw{Present: true, Member: execution.HostModelID, Value: " invalid"},
		Provider: execution.Raw{Present: true, Member: execution.HostProvider, Value: "route"},
		Variant:  execution.Raw{Present: true, Member: execution.HostVariant, Value: "high"},
		Role:     execution.Raw{Present: true, Member: execution.HostAgent, Value: "worker"},
	}}
	for _, role := range protocol.AllRoleIds {
		in.Environment.ProtocolRole = execution.Raw{Present: true, Member: execution.EnvironmentRole, Value: string(role)}
		got, err := execution.Normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		if got.Model.Status != execution.Invalid || got.Provider.Status != execution.Unavailable || got.Provider.Reason != execution.UnpairedProvider || got.Variant.Status != execution.Observed || got.Variant.Value != "high" || got.ProtocolRole.Status != execution.Observed || got.ProtocolRole.Value != string(role) {
			t.Fatal("variant or role changed model/provider anchoring, or valid protocol role lost")
		}
	}
}
