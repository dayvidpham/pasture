package promotion_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
)

const canonicalPastureRemote = "https://github.com/dayvidpham/pasture.git"

type promotionRunner struct {
	t           *testing.T
	bare        string
	gateCalls   [][]string
	failPattern string
	failCleanup bool
	mutateRepo  string
	pushes      int
	cleanups    int
	unsafePush  bool
}

func (r *promotionRunner) run(dir, executable string, args ...string) (string, error) {
	r.t.Helper()
	if filepath.Base(executable) == "go" {
		r.gateCalls = append(r.gateCalls, slices.Clone(args))
		if r.mutateRepo != "" && len(r.gateCalls) == 1 {
			git(r.t, r.mutateRepo, "remote", "set-url", "origin", "https://github.com/example/redirected.git")
		}
		if slices.Contains(args, r.failPattern) {
			return "", errors.New("injected mandatory gate failure")
		}
		return "ok", nil
	}
	gitArgs := slices.Clone(args)
	for i, arg := range gitArgs {
		if arg == canonicalPastureRemote && (gitArgs[0] == "push" || gitArgs[0] == "ls-remote") {
			gitArgs[i] = r.bare
		}
	}
	if len(gitArgs) > 0 && gitArgs[0] == "push" {
		r.pushes++
		r.unsafePush = r.cleanups != 2
	}
	if r.failCleanup && len(gitArgs) >= 2 && gitArgs[0] == "worktree" && gitArgs[1] == "remove" {
		_, _ = effects.DefaultCommandRunner(dir, executable, gitArgs...)
		r.cleanups++
		r.failCleanup = false
		return "", errors.New("injected worktree cleanup failure")
	}
	output, err := effects.DefaultCommandRunner(dir, executable, gitArgs...)
	if err == nil && len(gitArgs) >= 2 && gitArgs[0] == "worktree" && gitArgs[1] == "remove" {
		r.cleanups++
	}
	return output, err
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repository(t *testing.T, remote string, files map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main", ".")
	git(t, dir, "config", "commit.gpgsign", "false")
	git(t, dir, "remote", "add", "origin", remote)
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "candidate")
	return dir, git(t, dir, "rev-parse", "HEAD")
}

func candidatePair(t *testing.T) (pastureRepo, pastureCommit, auraRepo, auraCommit, bare string) {
	t.Helper()
	pastureFiles := map[string]string{"go.mod": "module example.invalid/candidate\n\ngo 1.25\n"}
	for _, name := range []string{"pasture-agents", "pasture-hooks", "pasture-skills"} {
		data, err := os.ReadFile(filepath.Join("..", "target", "claudecode", "assets", name, ".claude-plugin", "plugin.json"))
		if err != nil {
			t.Fatal(err)
		}
		pastureFiles[filepath.Join("internal", "target", "claudecode", "assets", name, ".claude-plugin", "plugin.json")] = string(data)
	}
	pastureRepo, pastureCommit = repository(t, canonicalPastureRemote, pastureFiles)
	projection, err := promotion.ProjectClaudeCodeTree(pastureRepo, "aura-plugins", pastureCommit)
	if err != nil {
		t.Fatal(err)
	}
	catalog := projectedCatalog(projection)
	data, err := jsonMarshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	auraRepo, auraCommit = repository(t, "https://github.com/dayvidpham/aura-plugins.git", map[string]string{".claude-plugin/marketplace.json": string(data)})
	bare = t.TempDir()
	git(t, bare, "init", "--bare", "--initial-branch=main", ".")
	return
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func request(t *testing.T, pastureRepo, pastureCommit, auraRepo, auraCommit string, expected effects.ExpectedOldOID) promotion.PromotionRequest {
	t.Helper()
	pastureID, _ := effects.NewRepositoryID(pastureRepo)
	auraID, _ := effects.NewRepositoryID(auraRepo)
	ref, _ := effects.NewRemoteRef(promotion.DefaultStableRef)
	request, err := promotion.NewPromotionRequest(pastureID, pastureCommit, auraID, auraCommit, "origin", ref, expected)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func remoteRef(t *testing.T, bare string) string {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", promotion.DefaultStableRef)
	cmd.Dir = bare
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.Fields(string(out))[0]
}

func TestCoordinatorRunsExactMandatoryGateSetAndPublishes(t *testing.T) {
	pastureRepo, pastureCommit, auraRepo, auraCommit, bare := candidatePair(t)
	runner := &promotionRunner{t: t, bare: bare}
	coordinator, _ := promotion.NewCoordinator(exec.LookPath, runner.run)
	result, err := coordinator.Promote(request(t, pastureRepo, pastureCommit, auraRepo, auraCommit, effects.ExpectAbsentRemote()))
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	wantGates := [][]string{{"test", "-race", "./..."}, {"test", "-race", "./internal/install/..."}}
	if !slices.EqualFunc(runner.gateCalls, wantGates, func(a, b []string) bool { return slices.Equal(a, b) }) {
		t.Fatalf("command gates = %v, want exact mandatory set %v", runner.gateCalls, wantGates)
	}
	if result.Commit != pastureCommit || remoteRef(t, bare) != pastureCommit || runner.pushes != 1 || runner.cleanups != 2 || runner.unsafePush {
		t.Fatalf("publication result=%+v remote=%q pushes=%d cleanups=%d unsafe=%v", result, remoteRef(t, bare), runner.pushes, runner.cleanups, runner.unsafePush)
	}
}

func TestCoordinatorIgnoresRemoteNameMutationAfterPreparation(t *testing.T) {
	pastureRepo, pastureCommit, auraRepo, auraCommit, bare := candidatePair(t)
	runner := &promotionRunner{t: t, bare: bare, mutateRepo: pastureRepo}
	coordinator, _ := promotion.NewCoordinator(exec.LookPath, runner.run)
	if _, err := coordinator.Promote(request(t, pastureRepo, pastureCommit, auraRepo, auraCommit, effects.ExpectAbsentRemote())); err != nil {
		t.Fatalf("exact verified URL publication failed after config mutation: %v", err)
	}
	if remoteRef(t, bare) != pastureCommit {
		t.Fatalf("canonical endpoint was not updated: %q", remoteRef(t, bare))
	}
}

func TestCoordinatorGateAndCleanupFailuresPreserveRef(t *testing.T) {
	for _, test := range []struct {
		name        string
		failPattern string
		cleanup     bool
	}{
		{"package race", "./...", false},
		{"activation race", "./internal/install/...", false},
		{"cleanup", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			pastureRepo, pastureCommit, auraRepo, auraCommit, bare := candidatePair(t)
			runner := &promotionRunner{t: t, bare: bare, failPattern: test.failPattern, failCleanup: test.cleanup}
			coordinator, _ := promotion.NewCoordinator(exec.LookPath, runner.run)
			_, err := coordinator.Promote(request(t, pastureRepo, pastureCommit, auraRepo, auraCommit, effects.ExpectAbsentRemote()))
			if err == nil {
				t.Fatal("expected pre-publication failure")
			}
			if test.cleanup && !strings.Contains(err.Error(), "cleanup failed") {
				t.Fatalf("cleanup failure is not actionable: %v", err)
			}
			if got := remoteRef(t, bare); got != "" || runner.pushes != 0 {
				t.Fatalf("failure changed ref=%q pushes=%d", got, runner.pushes)
			}
		})
	}
}

func TestCoordinatorCleansPastureWhenAuraPreparationFails(t *testing.T) {
	pastureRepo, pastureCommit, auraRepo, _, bare := candidatePair(t)
	runner := &promotionRunner{t: t, bare: bare}
	coordinator, _ := promotion.NewCoordinator(exec.LookPath, runner.run)
	_, err := coordinator.Promote(request(t, pastureRepo, pastureCommit, auraRepo, testAuraCommit, effects.ExpectAbsentRemote()))
	if err == nil {
		t.Fatal("expected unavailable Aura commit to fail preparation")
	}
	if runner.cleanups != 1 || runner.pushes != 0 || remoteRef(t, bare) != "" {
		t.Fatalf("partial preparation cleanup: cleanups=%d pushes=%d ref=%q", runner.cleanups, runner.pushes, remoteRef(t, bare))
	}
}

func TestCoordinatorPreservesRacingRef(t *testing.T) {
	pastureRepo, pastureCommit, auraRepo, auraCommit, bare := candidatePair(t)
	runner := &promotionRunner{t: t, bare: bare}
	coordinator, _ := promotion.NewCoordinator(exec.LookPath, runner.run)
	if _, err := coordinator.Promote(request(t, pastureRepo, pastureCommit, auraRepo, auraCommit, effects.ExpectAbsentRemote())); err != nil {
		t.Fatal(err)
	}
	first := pastureCommit
	file := filepath.Join(pastureRepo, "second")
	if err := os.WriteFile(file, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, pastureRepo, "add", "second")
	git(t, pastureRepo, "commit", "-m", "second")
	second := git(t, pastureRepo, "rev-parse", "HEAD")
	projection, err := promotion.ProjectClaudeCodeTree(pastureRepo, "aura-plugins", second)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := json.Marshal(projectedCatalog(projection))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(auraRepo, ".claude-plugin", "marketplace.json"), catalog, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, auraRepo, "add", ".claude-plugin/marketplace.json")
	git(t, auraRepo, "commit", "-m", "second projection")
	auraCommit = git(t, auraRepo, "rev-parse", "HEAD")
	_, err = coordinator.Promote(request(t, pastureRepo, second, auraRepo, auraCommit, effects.ExpectAbsentRemote()))
	if err == nil {
		t.Fatal("expected stale absent lease to fail")
	}
	if got := remoteRef(t, bare); got != first {
		t.Fatalf("racing ref changed to %q, want %q", got, first)
	}
}
