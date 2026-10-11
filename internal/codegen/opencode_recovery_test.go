package codegen

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeRecoveryComposition(t *testing.T) {
	t.Parallel()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun is required to verify the generated recovery callback; enter the flake dev shell")
	}
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`const TASK_RECOVERY_TEXT = ("[^\n]*");`).FindStringSubmatch(module)
	if len(match) != 2 {
		t.Fatal("generated plugin must embed exactly one escaped canonical recovery constant")
	}
	var recovery string
	if err := json.Unmarshal([]byte(match[1]), &recovery); err != nil {
		t.Fatal(err)
	}
	want := SharedFragmentSpecs[FragTaskRecovery].Prose.Content + "\n\n" + SharedFragmentSpecs[FragTaskAuthor].Prose.Content
	if recovery != want {
		t.Fatal("plugin recovery drifted from the shared skill-body fragments")
	}
	name := filepath.Join(t.TempDir(), "pasture-hooks.ts")
	if err := os.WriteFile(name, []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bun, "testdata/opencode_recovery.ts", name).CombinedOutput()
	if err != nil {
		t.Fatalf("generated recovery runtime: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "recovery composition assertions passed") {
		t.Fatalf("recovery runner did not finish: %s", out)
	}
}
