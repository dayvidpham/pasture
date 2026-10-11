package codegen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/require"
)

// A narrow completeness contract, not the publication validator: active
// hand-authored instructions must satisfy the same argv vocabulary as bodies.
// Historical evidence is checked separately against exact quoted windows.
func TestVerbatimOperationalTaskContract(t *testing.T) {
	t.Parallel()
	root := testModuleRoot(t)
	var fixture canonicalTaskFixture
	testutil.LoadFixtures(t, testutil.CanonicalTaskContract, &fixture)
	historical := map[string]map[string]int{}
	for _, exception := range fixture.Historical {
		name := filepath.Join(root, filepath.FromSlash(exception.Owner))
		content, err := os.ReadFile(name)
		require.NoError(t, err)
		require.Contains(t, string(content), exception.Banner, "historical quotations must be visibly non-operational")
		windows := map[string]int{}
		for _, window := range exception.Windows {
			windows[window]++
		}
		historical[name] = windows
	}
	for _, dir := range []string{"protocol", "install-cli"} {
		err := filepath.WalkDir(filepath.Join(root, "skills", dir), func(name string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(name, ".md") {
				return nil
			}
			content, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			// Remove only exact, counted quotation lines; a new recipe in the
			// same historical document still fails below. No whole-file escape.
			var active []string
			for _, line := range strings.Split(string(content), "\n") {
				if historical[name][line] > 0 {
					historical[name][line]--
					continue
				}
				active = append(active, line)
			}
			for window, count := range historical[name] {
				require.Zero(t, count, "historical classification window changed or disappeared in %s: %q", name, window)
			}
			content = []byte(strings.Join(active, "\n"))
			for _, rule := range fixture.Forbidden {
				pattern, err := regexp.Compile(rule.Pattern)
				require.NoError(t, err)
				if match := pattern.Find(content); match != nil {
					t.Errorf("%s: %s: unsupported recipe %q; repair the hand-authored source and regenerate", name, rule.Name, match)
				}
			}
			return nil
		})
		require.NoError(t, err)
	}
}
