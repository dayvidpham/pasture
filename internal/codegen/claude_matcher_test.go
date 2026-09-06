package codegen

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestClaudeMatcherReportIsSourceDerivedAndOptional(t *testing.T) {
	t.Parallel()
	files, err := (claudeHooksEmitter{}).Emit(t.TempDir(), GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range files {
		if filepath.Base(file.Path) != "pasture-activation.json" {
			continue
		}
		found = true
		var report struct {
			Events []map[string]json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal([]byte(file.Content), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Events) != len(registration.ClaudeCode2_1_261().Entries()) {
			t.Fatal("report population differs from registration")
		}
		matched := 0
		for _, row := range report.Events {
			var name string
			if err := json.Unmarshal(row["event"], &name); err != nil {
				t.Fatal(err)
			}
			value, present := row["matcher"]
			if name == "FileChanged" {
				matched++
				if string(value) != `".envrc|.env"` {
					t.Errorf("FileChanged report matcher = %s", value)
				}
			} else if present {
				t.Errorf("%s has undeclared matcher %s", name, value)
			}
		}
		if matched != 1 {
			t.Fatalf("FileChanged report rows = %d", matched)
		}
	}
	if !found {
		t.Fatal("emitter returned no activation report")
	}
	encoded, err := json.Marshal(activationSupportEntry{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"matcher"`) {
		t.Fatal("empty shared matcher must be omitted for every harness")
	}
}

func TestClaudeLifecycleMatchersFollowTargetDeclarations(t *testing.T) {
	t.Parallel()
	events := registration.ClaudeCode2_1_261().Entries()
	if len(events) == 0 {
		t.Fatal("Claude registration is empty; matcher coverage would check no event")
	}
	for _, event := range events {
		t.Run(event.NativeName, func(t *testing.T) {
			t.Parallel()
			want := ""
			if event.Kind == registration.EventFileChanged {
				// Claude Code 2.1.261's embedded FileChanged help gives this
				// pipe-delimited filename list; its watcher splits on "|".
				want = ".envrc|.env"
			}
			if got := activation.ClaudeCode2_1_261Matcher(event.Kind); got != want {
				t.Errorf("%s declared matcher = %q, want %q", event.NativeName, got, want)
			}
			// This is the row renderer used by emitClaudeHooks, including for
			// a future proven FileChanged row. No capture proof is invented.
			encoded, err := json.Marshal(claudeLifecycleHookGroup(event))
			if err != nil {
				t.Fatal(err)
			}
			var group struct {
				Matcher *string             `json:"matcher"`
				Hooks   []claudeHookCommand `json:"hooks"`
			}
			if err := json.Unmarshal(encoded, &group); err != nil {
				t.Fatalf("decode %s generated row: %v", event.NativeName, err)
			}
			if group.Matcher == nil || *group.Matcher != want {
				t.Errorf("%s generated row = %s, want matcher string %q", event.NativeName, encoded, want)
			}
			wantCommand := `${PASTURE_BIN:-pasture} hook lifecycle --harness claude-code --event ` + event.NativeName + ` --host-version "${CLAUDE_CODE_VERSION:-unknown}"`
			if len(group.Hooks) != 1 || group.Hooks[0].Command != wantCommand || group.Hooks[0].Type != "command" || group.Hooks[0].Timeout != 10 {
				t.Errorf("%s lifecycle command changed: %s", event.NativeName, encoded)
			}
		})
	}
}

func TestClaudeMatcherMetadataDoesNotEnableFileChanged(t *testing.T) {
	t.Parallel()
	states, err := activation.ClaudeCode2_1_261()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(states, func(entry activation.Entry) bool { return entry.Event == registration.EventFileChanged })
	if index < 0 {
		t.Fatal("FileChanged is absent from the exhaustive Claude activation manifest")
	}
	entry := states[index]
	if entry.State != activation.Withheld || entry.Reason != activation.WithheldOutsideTargetSet || entry.CaptureProof != 0 || entry.ProductionProof != 0 {
		t.Fatalf("matcher metadata must not change FileChanged admission: %+v", entry)
	}
	if slices.Contains(activation.ClaudeCode2_1_261TargetEvents(), registration.EventFileChanged) {
		t.Fatal("matcher metadata selected FileChanged without accepted capture and production proofs")
	}
	files, err := (claudeHooksEmitter{}).Emit(t.TempDir(), GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var config claudeHooksConfig
	found := false
	for _, file := range files {
		if filepath.Base(file.Path) != "hooks.json" {
			continue
		}
		found = true
		if err := json.Unmarshal([]byte(file.Content), &config); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("Claude emitter returned no hooks.json; admission check would read no transport")
	}
	if _, present := config.Hooks["FileChanged"]; present {
		t.Fatal("unproven FileChanged row reached the generated transport")
	}
	for name, groups := range config.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if strings.Contains(hook.Command, "hook lifecycle") && group.Matcher != "" {
					t.Errorf("%s existing lifecycle matcher changed from empty to %q", name, group.Matcher)
				}
			}
		}
	}
}
