package ingress_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// homePathSpellings are the three spellings of a home directory a committed
// corpus file can carry: the absolute path, the relative spelling without the
// leading slash, and the directory slug a host derives from a path. Each
// captures the user segment. A tilde path (`~/...`) carries no segment and is
// not matched.
var homePathSpellings = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"absolute /home/<segment>", regexp.MustCompile(`/home/([A-Za-z0-9._-]+)`)},
	{"relative home/<segment>/", regexp.MustCompile(`(?:^|[^/A-Za-z0-9])home/([A-Za-z0-9._-]+)/`)},
	{"slug -home-<segment>-", regexp.MustCompile(`-home-([A-Za-z0-9._]+)-`)},
}

// homePathPlaceholder is the only user segment a committed corpus file may
// carry under home-path-v1.
const homePathPlaceholder = "user"

// TestCommittedCorpusCarriesNoHomeSegmentButThePlaceholder is the structural
// form of home-path-v1: over every file under every harness testdata
// directory (payloads, sidecars, corpora, the clearance record), every home
// path spelling names the placeholder segment and no other. It carries no
// real user name, so it holds on any machine; RED names the file and the
// spelling. Non-vacuity: at least one file per harness directory was read and
// at least one home path was matched, so an empty walk or a dead pattern
// cannot pass.
func TestCommittedCorpusCarriesNoHomeSegmentButThePlaceholder(t *testing.T) {
	t.Parallel()
	for _, harness := range []string{"claude", "codex", "opencode"} {
		harness := harness
		t.Run(harness, func(t *testing.T) {
			t.Parallel()
			files, matched := assertNoHomeSegmentButThePlaceholder(t, filepath.Join(harness, "testdata"))
			require.Positive(t, files, "the %s testdata directory contributed no file; the walk is broken or the directory moved", harness)
			require.Positive(t, matched, "no home path spelling matched under %s/testdata, so the guard checked nothing", harness)
		})
	}
}

// assertNoHomeSegmentButThePlaceholder walks root and returns how many files
// it read and how many home path spellings it matched.
func assertNoHomeSegmentButThePlaceholder(t *testing.T, root string) (files, matched int) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files++
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		for _, spelling := range homePathSpellings {
			for _, match := range spelling.pattern.FindAllStringSubmatch(text, -1) {
				matched++
				require.Equal(t, homePathPlaceholder, match[1],
					"%s carries the home path spelling %q with the user segment %q; home-path-v1 writes the placeholder %q in every spelling, so the file is not cleared",
					path, spelling.name, match[1], homePathPlaceholder)
			}
		}
		return nil
	})
	require.NoError(t, err)
	return files, matched
}
