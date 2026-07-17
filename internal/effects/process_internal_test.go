package effects

import "testing"

// TestShellMetaCharacterConstructIsTotalOverShellMetaCharacters proves
// shellMetaCharacterConstruct (process.go), which ClassifyShellConstruct's
// fallback scan is derived from, classifies every rune in shellMetaCharacters
// (refs.go, the set ExecutableRef rejects). This is the mechanical guarantee
// that the two sets cannot drift apart: any future metacharacter added to
// shellMetaCharacters without a corresponding classification here fails this
// test immediately, rather than silently reintroducing the gap the
// derived-scan fix closed.
func TestShellMetaCharacterConstructIsTotalOverShellMetaCharacters(t *testing.T) {
	t.Parallel()

	for _, r := range shellMetaCharacters {
		construct, ok := shellMetaCharacterConstruct[r]
		if !ok {
			t.Fatalf("shellMetaCharacters rune %q has no entry in shellMetaCharacterConstruct — the classifier scan has drifted from the rejected set", r)
		}
		if construct == "" {
			t.Fatalf("shellMetaCharacters rune %q maps to an empty ShellConstruct", r)
		}
	}

	// And the reverse: every mapped rune must actually be a rejected
	// metacharacter, so the map carries no dead or accidental entries.
	for r := range shellMetaCharacterConstruct {
		found := false
		for _, meta := range shellMetaCharacters {
			if r == meta {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("shellMetaCharacterConstruct has an entry for rune %q which is not in shellMetaCharacters", r)
		}
	}
}

// TestClassifyShellConstructRejectsEveryShellMetaCharacter proves the public
// ClassifyShellConstruct entry point — not just the internal map — rejects
// every individual shellMetaCharacters rune as some classified construct.
func TestClassifyShellConstructRejectsEveryShellMetaCharacter(t *testing.T) {
	t.Parallel()

	for _, r := range shellMetaCharacters {
		fragment := "cmd " + string(r) + " arg"
		construct, err := ClassifyShellConstruct(fragment)
		if err == nil {
			t.Fatalf("fragment containing metacharacter %q classified clean", r)
		}
		if construct == "" {
			t.Fatalf("fragment containing metacharacter %q returned an empty construct alongside a non-nil error", r)
		}
	}
}
