package ingress_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/testutil"
)

func TestSessionTransitionConstructedCases(t *testing.T) {
	t.Parallel()
	const corpus testutil.FixtureName = "session_transitions"
	var cases []struct {
		Name    string                  `yaml:"name"`
		Harness string                  `yaml:"harness"`
		Event   string                  `yaml:"event"`
		Body    string                  `yaml:"body"`
		Session string                  `yaml:"session"`
		Invalid bool                    `yaml:"invalid"`
		Want    model.SessionTransition `yaml:"want"`
	}
	testutil.LoadFixtures(t, corpus, &cases)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			manifest := registration.ClaudeCode__2_1_261()
			if tc.Harness == "codex" {
				manifest = registration.Codex__0_153_0()
			}
			if tc.Harness == "opencode" {
				manifest = registration.OpenCode__2_0_20()
			}
			event, err := ingress.EventByNativeName(manifest, tc.Event)
			if err != nil {
				t.Fatal(err)
			}
			capture := model.CaptureValid
			if tc.Invalid {
				capture = model.CaptureUnsupportedSchema
			}
			var bindings []model.NativeBinding
			if tc.Session != "" {
				bindings = []model.NativeBinding{{Kind: model.BindingSession, Value: tc.Session}}
			}
			got := ingress.ExtractSessionTransition(manifest.Contract, event.Kind, []byte(tc.Body), capture, bindings)
			if got != tc.Want {
				t.Fatalf("transition = %d, want %d", got, tc.Want)
			}
		})
	}
}
