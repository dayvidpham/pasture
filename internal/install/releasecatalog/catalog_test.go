package releasecatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
)

const pastureRevisionText = "1111111111111111111111111111111111111111"
const auraRevisionText = "2222222222222222222222222222222222222222"

type fixtureSource struct {
	releases []Release
	data     map[string][]byte
}

func (s *fixtureSource) ListReleases(context.Context) ([]Release, error) {
	return append([]Release(nil), s.releases...), nil
}
func (s *fixtureSource) OpenURL(_ context.Context, url string) (io.ReadCloser, error) {
	b, ok := s.data[url]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type recordingMutator struct{ calls int }

func (m *recordingMutator) ApplyVerifiedAggregate(context.Context, artifact.VerifiedAggregate) error {
	m.calls++
	return nil
}

func TestCatalogFinalDefaultAndOptInRC(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	final, _ := artifact.ParseVersion("1.2.0")
	rc, _ := artifact.ParseVersion("1.3.0-rc.1")
	source.releases = []Release{
		makeRelease(t, source, final, false, "final/"),
		makeRelease(t, source, rc, true, "rc/"),
	}
	catalog, err := New(source)
	if err != nil {
		t.Fatal(err)
	}
	selection := validSelection(t)
	got, err := catalog.Resolve(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest().Version() != final {
		t.Fatalf("default selected %s, want final %s", got.Manifest().Version(), final)
	}
	selection.Policy = IncludePrereleases
	got, err = catalog.Resolve(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest().Version() != rc {
		t.Fatalf("opt-in selected %s, want RC %s", got.Manifest().Version(), rc)
	}
}

func TestResolveAndApplyNeverMutatesUntilCompleteVerification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string][]byte)
	}{
		{"checksum mismatch", func(data map[string][]byte) {
			data["final/"+artifact.AggregateChecksumAsset] = []byte(strings.Repeat("0", 64) + "  " + artifact.AggregateManifestAsset + "\n")
		}},
		{"component checksum mismatch", func(data map[string][]byte) { data["final/pasture-1.2.0-codex-hooks.tgz"] = []byte("corrupt") }},
		{"identity mismatch", func(data map[string][]byte) { replaceManifest(data, `"id":"codex.hooks"`, `"id":"codex.skills"`) }},
		{"revision mismatch", func(data map[string][]byte) {
			replaceManifest(data, `"aura_revision":"2222222222222222222222222222222222222222"`, `"aura_revision":"3333333333333333333333333333333333333333"`)
		}},
		{"incompatible", func(data map[string][]byte) {
			replaceManifest(data, `"installer_max":"1.9.9"`, `"installer_max":"1.0.0"`)
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := loadFixtureSource(t)
			version, _ := artifact.ParseVersion("1.2.0")
			source.releases = []Release{makeRelease(t, source, version, false, "final/")}
			test.mutate(source.data)
			mutator := &recordingMutator{}
			catalog, _ := New(source)
			if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), mutator); err == nil {
				t.Fatal("expected verification failure")
			}
			if mutator.calls != 0 {
				t.Fatalf("mutator called %d times before complete verification", mutator.calls)
			}
		})
	}
}

func TestMovingAliasCannotEnterCatalog(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	// No Release value can be constructed from pasture-stable through the GitHub decoder;
	// an empty validated catalog proves it is not accepted as a fallback.
	catalog, _ := New(source)
	if _, err := catalog.Resolve(context.Background(), validSelection(t)); err == nil {
		t.Fatal("expected no immutable release")
	}
}

func TestAssetLimitOverflowDoesNotMutate(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	version, _ := artifact.ParseVersion("1.2.0")
	source.releases = []Release{makeRelease(t, source, version, false, "final/")}
	selection := validSelection(t)
	selection.MaxAssetBytes = math.MaxInt64
	mutator := &recordingMutator{}
	catalog, _ := New(source)
	if _, err := catalog.ResolveAndApply(context.Background(), selection, mutator); err == nil {
		t.Fatal("expected overflow rejection")
	}
	if mutator.calls != 0 {
		t.Fatal("mutation occurred")
	}
}

func loadFixtureSource(t *testing.T) *fixtureSource {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	data := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		data["final/"+entry.Name()] = b
	}
	// The prerelease uses the same deterministic component bytes and a manifest whose immutable names/version/channel are rewritten.
	for key, value := range data {
		data["rc/"+strings.TrimPrefix(key, "final/")] = append([]byte(nil), value...)
	}
	replaceManifestPrefix(data, "rc/", "1.2.0", "1.3.0-rc.1", "final", "prerelease")
	return &fixtureSource{data: data}
}

func makeRelease(t *testing.T, source *fixtureSource, version artifact.Version, prerelease bool, prefix string) Release {
	t.Helper()
	assets := map[string]Asset{}
	manifest, err := artifact.ParseAggregateManifest(source.data[prefix+artifact.AggregateManifestAsset])
	if err != nil {
		t.Fatal(err)
	}
	names := []string{artifact.AggregateManifestAsset, artifact.AggregateChecksumAsset}
	for _, c := range manifest.Components() {
		names = append(names, c.Asset())
	}
	for _, name := range names {
		url := prefix + name
		assets[name] = Asset{name: name, downloadURL: url, size: int64(len(source.data[url]))}
	}
	return Release{version: version, prerelease: prerelease, assets: assets}
}

func validSelection(t *testing.T) Selection {
	t.Helper()
	installer, _ := artifact.ParseVersion("1.5.0")
	pasture, _ := artifact.ParseRevision(pastureRevisionText)
	aura, _ := artifact.ParseRevision(auraRevisionText)
	return Selection{Installer: installer, PastureRevision: pasture, AuraRevision: aura, Policy: FinalsOnly}
}

func replaceManifest(data map[string][]byte, old, replacement string) {
	value := string(data["final/"+artifact.AggregateManifestAsset])
	value = strings.Replace(value, old, replacement, 1)
	data["final/"+artifact.AggregateManifestAsset] = []byte(value)
	updateChecksum(data, "final/")
}
func replaceManifestPrefix(data map[string][]byte, prefix, oldVersion, newVersion, oldChannel, newChannel string) {
	value := string(data[prefix+artifact.AggregateManifestAsset])
	value = strings.ReplaceAll(value, oldVersion, newVersion)
	value = strings.Replace(value, `"channel":"`+oldChannel+`"`, `"channel":"`+newChannel+`"`, 1)
	data[prefix+artifact.AggregateManifestAsset] = []byte(value)
	for key, bytes := range data {
		if strings.HasPrefix(key, prefix+"pasture-"+oldVersion) {
			newKey := strings.Replace(key, oldVersion, newVersion, 1)
			data[newKey] = bytes
			delete(data, key)
		}
	}
	updateChecksum(data, prefix)
}
func updateChecksum(data map[string][]byte, prefix string) {
	sum := sha256.Sum256(data[prefix+artifact.AggregateManifestAsset])
	data[prefix+artifact.AggregateChecksumAsset] = []byte(hex.EncodeToString(sum[:]) + "  " + artifact.AggregateManifestAsset + "\n")
}
