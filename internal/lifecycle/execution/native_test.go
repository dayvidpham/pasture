package execution_test

import (
	"os"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
)

func TestNativeOpenCodeCapturedContextAndTitle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file, event, model string
		kind               execution.Kind
	}{
		{"opencode_session_context_2_0_20.1.json", "session.context", "deepseek-v4.1-flash", execution.Primary},
		{"opencode_session_model_request_2_0_20.2.json", "session.model.request", "gpt-6-luna", execution.Title},
	} {
		t.Run(tc.event, func(t *testing.T) {
			body, err := os.ReadFile("../ingress/opencode/testdata/fixtures/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			in, ok := execution.Native("opencode", tc.event, body)
			if !ok {
				t.Fatal("capture not recognized")
			}
			got, err := execution.Normalize(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.kind || got.Model.Value != tc.model || got.Provider.Value != "opencode-go" {
				t.Fatalf("unexpected execution metadata: %+v", got)
			}
		})
	}
}
