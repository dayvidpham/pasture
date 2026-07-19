package promotion_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
)

// These tests exercise the full production promotion path against a temporary
// bare git remote: the real GitRevisionResolver and the real
// effects.GitRepositoryPusher, driven by the on-disk git binary. They falsify
// every guarded-update failure mode and prove the old ref is preserved in every
// failure case, per the aura-plugins#9 acceptance criteria.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// bareRemoteFixture stands up a bare remote and a work repo with one commit on
// the pasture-stable-eligible branch, and returns their paths plus the head sha.
func bareRemoteFixture(t *testing.T) (workDir, bareDir, head string) {
	t.Helper()
	bareDir = t.TempDir()
	git(t, bareDir, "init", "--bare", "--initial-branch=main", ".")

	workDir = t.TempDir()
	git(t, workDir, "init", "--initial-branch=main", ".")
	git(t, workDir, "config", "commit.gpgsign", "false")
	git(t, workDir, "remote", "add", "origin", bareDir)
	// One real commit to publish.
	writeFile(t, workDir, "README.md", "pasture\n")
	git(t, workDir, "add", "README.md")
	git(t, workDir, "commit", "-m", "initial")
	head = git(t, workDir, "rev-parse", "HEAD")
	return workDir, bareDir, head
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := writeFileAtomic(dir+"/"+name, content); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func writeFileAtomic(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// remoteRef reads the bare remote's value for ref, or "" if absent.
func remoteRef(t *testing.T, bareDir, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", ref)
	cmd.Dir = bareDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func productionPromoter(t *testing.T) promotion.Promoter {
	t.Helper()
	resolver, err := promotion.NewGitRevisionResolver(exec.LookPath, effects.DefaultCommandRunner)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	pusher, err := effects.NewGitRepositoryPusher(exec.LookPath, effects.DefaultCommandRunner, "origin")
	if err != nil {
		t.Fatalf("pusher: %v", err)
	}
	p, err := promotion.NewPromoter(resolver, pusher)
	if err != nil {
		t.Fatalf("promoter: %v", err)
	}
	return p
}

func mustRequest(t *testing.T, workDir, head string, expectedOld effects.ExpectedOldOID) promotion.PromotionRequest {
	t.Helper()
	repo, err := effects.NewRepositoryID(workDir)
	if err != nil {
		t.Fatalf("repo id: %v", err)
	}
	ref, err := effects.NewRemoteRef(promotion.DefaultStableRef)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	req, err := promotion.NewPromotionRequest(repo, head, repo, head, "origin", ref, expectedOld)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return req
}

func passGates(t *testing.T) []promotion.Gate {
	t.Helper()
	g, err := promotion.NewFuncGate("test-gate", func() error { return nil })
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	return []promotion.Gate{g}
}

// Fixture 1: initial absent creation.
func TestPromoteCreatesAbsentChannel(t *testing.T) {
	workDir, bareDir, head := bareRemoteFixture(t)
	p := productionPromoter(t)
	req := mustRequest(t, workDir, head, effects.ExpectAbsentRemote())

	res, err := p.Promote(req, passGates(t))
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if res.Outcome != effects.GuardedPushPushed {
		t.Fatalf("outcome = %q, want pushed", res.Outcome)
	}
	if got := remoteRef(t, bareDir, promotion.DefaultStableRef); got != head {
		t.Fatalf("remote ref = %q, want %q", got, head)
	}
}

// Fixture 2: matching expected-old promotion (advance the channel).
func TestPromoteAdvancesChannelOnMatchingExpectedOld(t *testing.T) {
	workDir, bareDir, first := bareRemoteFixture(t)
	p := productionPromoter(t)

	// Create the channel at the first commit.
	if _, err := p.Promote(mustRequest(t, workDir, first, effects.ExpectAbsentRemote()), passGates(t)); err != nil {
		t.Fatalf("initial promote: %v", err)
	}

	// A second reviewed commit.
	writeFile(t, workDir, "CHANGELOG.md", "v2\n")
	git(t, workDir, "add", "CHANGELOG.md")
	git(t, workDir, "commit", "-m", "second")
	second := git(t, workDir, "rev-parse", "HEAD")

	firstCommit, err := effects.NewCommitOID(strings.ToLower(first))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	expectAt, err := effects.ExpectRemoteAt(firstCommit)
	if err != nil {
		t.Fatalf("expect: %v", err)
	}

	res, err := p.Promote(mustRequest(t, workDir, second, expectAt), passGates(t))
	if err != nil {
		t.Fatalf("advance promote: %v", err)
	}
	if res.Outcome != effects.GuardedPushPushed {
		t.Fatalf("outcome = %q, want pushed", res.Outcome)
	}
	if got := remoteRef(t, bareDir, promotion.DefaultStableRef); got != second {
		t.Fatalf("remote ref = %q, want %q", got, second)
	}
}

// Fixture 3 + racing-advance preservation: a stale expected-old is rejected and
// the racing publisher's advance is preserved unchanged.
func TestPromoteRejectsStaleExpectedOldAndPreservesRacingAdvance(t *testing.T) {
	workDir, bareDir, first := bareRemoteFixture(t)
	p := productionPromoter(t)

	// A racing publisher already advanced the channel to `first`.
	if _, err := p.Promote(mustRequest(t, workDir, first, effects.ExpectAbsentRemote()), passGates(t)); err != nil {
		t.Fatalf("racing publish: %v", err)
	}

	// Our candidate commit.
	writeFile(t, workDir, "ours.md", "ours\n")
	git(t, workDir, "add", "ours.md")
	git(t, workDir, "commit", "-m", "ours")
	ours := git(t, workDir, "rev-parse", "HEAD")

	// We believed the channel was still absent (stale expectation).
	res, err := p.Promote(mustRequest(t, workDir, ours, effects.ExpectAbsentRemote()), passGates(t))
	if err == nil {
		t.Fatalf("expected stale expected-old to be rejected, got result %+v", res)
	}
	// The racing publisher's advance is preserved: the ref still holds `first`.
	if got := remoteRef(t, bareDir, promotion.DefaultStableRef); got != first {
		t.Fatalf("racing advance not preserved: remote ref = %q, want %q", got, first)
	}
}

// Fixture 4: idempotent retry — re-running the same landed promotion is a
// verified replay, and the remote is unchanged.
func TestPromoteIdempotentRetry(t *testing.T) {
	workDir, bareDir, head := bareRemoteFixture(t)
	p := productionPromoter(t)

	if _, err := p.Promote(mustRequest(t, workDir, head, effects.ExpectAbsentRemote()), passGates(t)); err != nil {
		t.Fatalf("first promote: %v", err)
	}

	headCommit, err := effects.NewCommitOID(strings.ToLower(head))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	expectAt, err := effects.ExpectRemoteAt(headCommit)
	if err != nil {
		t.Fatalf("expect: %v", err)
	}

	// Retry landing the same commit; the remote already holds it.
	res, err := p.Promote(mustRequest(t, workDir, head, expectAt), passGates(t))
	if err != nil {
		t.Fatalf("retry promote: %v", err)
	}
	if res.Outcome != effects.GuardedPushIdempotentReplay {
		t.Fatalf("outcome = %q, want idempotent-replay", res.Outcome)
	}
	if got := remoteRef(t, bareDir, promotion.DefaultStableRef); got != head {
		t.Fatalf("remote ref = %q, want %q", got, head)
	}
}

// A failing gate aborts before any ref update: the channel is never created.
func TestPromoteFailingGateLeavesRefUnchanged(t *testing.T) {
	workDir, bareDir, head := bareRemoteFixture(t)
	p := productionPromoter(t)

	failing, err := promotion.NewFuncGate("failing-gate", func() error {
		return errors.New("simulated gate failure")
	})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	res, err := p.Promote(mustRequest(t, workDir, head, effects.ExpectAbsentRemote()), []promotion.Gate{failing})
	if err == nil {
		t.Fatalf("expected gate failure, got %+v", res)
	}
	if !strings.Contains(err.Error(), "failing-gate") {
		t.Fatalf("error does not name the failing gate: %v", err)
	}
	if got := remoteRef(t, bareDir, promotion.DefaultStableRef); got != "" {
		t.Fatalf("ref was created despite gate failure: %q", got)
	}
}
