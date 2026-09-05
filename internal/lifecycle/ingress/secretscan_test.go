package ingress_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
)

// plantedSecrets holds one sample per committed secret shape. The samples are
// shaped like real credentials and are not credentials. Each pattern must
// have a sample and each sample must be found by its pattern, so removing a
// pattern from the committed set turns that shape's case RED here, and a
// sample that stops matching turns RED as well.
var plantedSecrets = map[string]string{
	"private key block":                "-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----",
	"AWS access key id":                "AKIAIOSFODNN7EXAMPLE",
	"AWS secret access key assignment": "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	"GCP API key":                      "AIzaSyA1234567890abcdefghijklmnopqrstuv",
	"GCP OAuth access token":           "ya29.a0AfH6SMBxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
	"GitHub token":                     "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij",
	"GitHub fine-grained token":        "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz",
	"Anthropic API key":                "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123",
	"JSON web token":                   "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
}

func TestSecretScanIsRedOnEachPlantedShape(t *testing.T) {
	t.Parallel()
	patterns := ingress.SecretPatterns()
	byName := map[string]ingress.SecretPattern{}
	for _, pattern := range patterns {
		byName[pattern.Name] = pattern
	}
	// Every planted sample must have its committed shape and be found by it.
	// A shape removed from the committed set turns its sample's case RED
	// here, named by shape.
	for name, sample := range plantedSecrets {
		name, sample := name, sample
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, known := byName[name]
			require.True(t, known, "the planted %s sample has no committed shape; the shape was removed from the committed set, so this token shape would reach the repository unseen", name)
			document := []byte(`{"session_id":"s","note":"` + strings.ReplaceAll(sample, "\n", `\n`) + `"}`)
			found := false
			for _, hit := range ingress.ScanSecrets(document) {
				if hit.Pattern == name {
					found = true
					assert.Positive(t, hit.Length)
					assert.Less(t, hit.Offset, len(document))
				}
			}
			assert.True(t, found, "the planted %s token was not detected; the corpus scan would let this shape into the repository", name)
		})
	}
	// Every committed shape must have a planted sample, so a shape cannot be
	// added without proof that it detects anything.
	for _, pattern := range patterns {
		_, planted := plantedSecrets[pattern.Name]
		assert.True(t, planted, "the committed shape %q has no planted sample", pattern.Name)
	}
	assert.Empty(t, ingress.ScanSecrets([]byte(`{"session_id":"5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68","hook_event_name":"PreToolUse","cwd":"/home/user/p"}`)),
		"an ordinary payload must produce no hit")
}

// moduleRoot walks up from the package directory to the module root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "no go.mod above %s", dir)
		dir = parent
	}
}

// TestNoCommittedTestdataCarriesASecretShape scans EVERY file beneath every
// testdata directory in the module, raw bytes and not JSON, so a token in a
// payload, a sidecar, a YAML corpus or a golden is found the same way. The
// population is derived by walking the module, and the three harness fixture
// directories must be among the files walked, so a moved directory cannot
// silently leave the scan.
func TestNoCommittedTestdataCarriesASecretShape(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	var scanned []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "worktree", "result":
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !strings.Contains(relative, string(filepath.Separator)+"testdata"+string(filepath.Separator)) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned = append(scanned, relative)
		for _, hit := range ingress.ScanSecrets(raw) {
			assert.Fail(t, "secret shape in committed test data",
				"%s carries a %s at byte %d (%d bytes); a committed fixture must never carry a credential, so remove or substitute it before committing", relative, hit.Pattern, hit.Offset, hit.Length)
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, scanned, "no testdata file was scanned")
	for _, harness := range []string{"claude", "codex", "opencode"} {
		want := filepath.Join("internal", "lifecycle", "ingress", harness, "testdata", "fixtures")
		found := false
		for _, path := range scanned {
			found = found || strings.HasPrefix(path, want)
		}
		assert.True(t, found, "the %s fixture directory was not among the %d files scanned", harness, len(scanned))
	}
}
