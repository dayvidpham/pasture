package promotion_test

import (
	"errors"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
)

// fakeResolver records whether it was called and returns fixed operands.
type fakeResolver struct {
	called bool
	commit effects.CommitOID
	tree   effects.TreeDigest
}

func (f *fakeResolver) ResolveCommit(effects.RepositoryID, string) (effects.CommitOID, error) {
	f.called = true
	return f.commit, nil
}

func (f *fakeResolver) ResolveTree(effects.RepositoryID, effects.CommitOID) (effects.TreeDigest, error) {
	return f.tree, nil
}

// fakePusher records whether any mutating primitive was called.
type fakePusher struct{ pushed bool }

func (f *fakePusher) VerifyLocalObject(effects.RepositoryID, effects.CommitOID, effects.TreeDigest) error {
	return nil
}

func (f *fakePusher) PushExact(effects.RepositoryID, effects.CommitOID, effects.RemoteRef, effects.ExpectedOldOID) error {
	f.pushed = true
	return nil
}

func (f *fakePusher) ReadRemote(effects.RepositoryID, effects.RemoteRef) (effects.RemoteState, error) {
	return effects.AbsentRemoteState(), nil
}

func TestNewPromoterValidation(t *testing.T) {
	if _, err := promotion.NewPromoter(nil, &fakePusher{}); err == nil {
		t.Error("expected nil resolver to be rejected")
	}
	if _, err := promotion.NewPromoter(&fakeResolver{}, nil); err == nil {
		t.Error("expected nil pusher to be rejected")
	}
}

func TestPromoteResolvesBeforeGateAndDoesNotPushWhenGateFails(t *testing.T) {
	commit, _ := effects.NewCommitOID(testPastureCommit)
	tree, _ := effects.NewTreeDigest("abcdef0123456789abcdef0123456789abcdef01")
	resolver := &fakeResolver{commit: commit, tree: tree}
	pusher := &fakePusher{}
	p, err := promotion.NewPromoter(resolver, pusher)
	if err != nil {
		t.Fatalf("promoter: %v", err)
	}
	repo, _ := effects.NewRepositoryID("/repo")
	ref, _ := effects.NewRemoteRef(promotion.DefaultStableRef)
	req, err := promotion.NewPromotionRequest(repo, testPastureCommit, repo, testAuraCommit, "origin", ref, effects.ExpectAbsentRemote())
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	failing, _ := promotion.NewFuncGate("gate", func() error { return errors.New("nope") })

	if _, err := p.Promote(req, []promotion.Gate{failing}); err == nil {
		t.Fatal("expected gate failure")
	}
	if !resolver.called {
		t.Error("resolver was not called before the expensive gate")
	}
	if pusher.pushed {
		t.Error("pusher was called despite gate failure — no ref may be touched after a gate failure")
	}
}

func TestPromoteRejectsZeroPromoterAndRequest(t *testing.T) {
	var zero promotion.Promoter
	repo, _ := effects.NewRepositoryID("/repo")
	ref, _ := effects.NewRemoteRef(promotion.DefaultStableRef)
	req, _ := promotion.NewPromotionRequest(repo, testPastureCommit, repo, testAuraCommit, "origin", ref, effects.ExpectAbsentRemote())
	if _, err := zero.Promote(req, nil); err == nil {
		t.Error("expected zero promoter to be rejected")
	}

	p, _ := promotion.NewPromoter(&fakeResolver{}, &fakePusher{})
	if _, err := p.Promote(promotion.PromotionRequest{}, nil); err == nil {
		t.Error("expected zero request to be rejected")
	}
}
