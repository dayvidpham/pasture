package promotion_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
)

const (
	testPastureCommit = "0123456789abcdef0123456789abcdef01234567"
	testAuraCommit    = "89abcdef0123456789abcdef0123456789abcdef"
)

func validRefAndRepo(t *testing.T) (effects.RepositoryID, effects.RemoteRef) {
	t.Helper()
	repo, err := effects.NewRepositoryID("/repo")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	ref, err := effects.NewRemoteRef(promotion.DefaultStableRef)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	return repo, ref
}

func TestParseExpectedOldAbsent(t *testing.T) {
	for _, v := range []string{"absent", "ABSENT", "Absent"} {
		e, err := promotion.ParseExpectedOld(v)
		if err != nil {
			t.Fatalf("ParseExpectedOld(%q): %v", v, err)
		}
		if !e.Absent() {
			t.Fatalf("ParseExpectedOld(%q) is not absent", v)
		}
	}
}

func TestParseExpectedOldCommit(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	e, err := promotion.ParseExpectedOld(sha)
	if err != nil {
		t.Fatalf("ParseExpectedOld: %v", err)
	}
	c, present := e.Commit()
	if !present {
		t.Fatal("expected a present commit expectation")
	}
	if c.String() != sha {
		t.Fatalf("commit = %q, want %q", c, sha)
	}
}

func TestParseExpectedOldRejectsGarbage(t *testing.T) {
	for _, v := range []string{"", "   ", "not-a-sha", "12345"} {
		if _, err := promotion.ParseExpectedOld(v); err == nil {
			t.Fatalf("expected ParseExpectedOld(%q) to fail", v)
		}
	}
}

func TestNewPromotionRequestValidation(t *testing.T) {
	repo, ref := validRefAndRepo(t)
	exp := effects.ExpectAbsentRemote()

	// Happy path.
	if _, err := promotion.NewPromotionRequest(repo, testPastureCommit, repo, testAuraCommit, "origin", ref, exp); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	cases := []struct {
		name                        string
		pastureRepo, auraRepo       effects.RepositoryID
		pastureRev, auraRev, remote string
		ref                         effects.RemoteRef
		exp                         effects.ExpectedOldOID
	}{
		{"invalid pasture repo", effects.RepositoryID{}, repo, testPastureCommit, testAuraCommit, "origin", ref, exp},
		{"empty pasture rev", repo, repo, "", testAuraCommit, "origin", ref, exp},
		{"padded pasture rev", repo, repo, " " + testPastureCommit + " ", testAuraCommit, "origin", ref, exp},
		{"symbolic pasture rev", repo, repo, "HEAD", testAuraCommit, "origin", ref, exp},
		{"invalid aura repo", repo, effects.RepositoryID{}, testPastureCommit, testAuraCommit, "origin", ref, exp},
		{"empty aura rev", repo, repo, testPastureCommit, "", "origin", ref, exp},
		{"symbolic aura rev", repo, repo, testPastureCommit, "main", "origin", ref, exp},
		{"empty remote", repo, repo, testPastureCommit, testAuraCommit, "", ref, exp},
		{"invalid ref", repo, repo, testPastureCommit, testAuraCommit, "origin", effects.RemoteRef{}, exp},
		{"unspecified expected-old", repo, repo, testPastureCommit, testAuraCommit, "origin", ref, effects.ExpectedOldOID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := promotion.NewPromotionRequest(tc.pastureRepo, tc.pastureRev, tc.auraRepo, tc.auraRev, tc.remote, tc.ref, tc.exp)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}
