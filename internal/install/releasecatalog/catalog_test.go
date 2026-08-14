package releasecatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	releases     []Release
	data         map[string][]byte
	listErr      error
	openErr      error
	readErr      error
	cancelOnRead context.CancelFunc
	closeErr     error
}

func (s *fixtureSource) ListReleases(context.Context) ([]Release, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]Release(nil), s.releases...), nil
}
func (s *fixtureSource) OpenURL(_ context.Context, url string) (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	b, ok := s.data[url]
	if !ok {
		return nil, os.ErrNotExist
	}
	if s.readErr != nil || s.cancelOnRead != nil {
		return &faultReader{data: bytes.NewReader(b), err: s.readErr, cancel: s.cancelOnRead, closeErr: s.closeErr}, nil
	}
	if s.closeErr != nil {
		return &faultReader{data: bytes.NewReader(b), closeErr: s.closeErr}, nil
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type faultReader struct {
	data     *bytes.Reader
	err      error
	cancel   context.CancelFunc
	fired    bool
	closeErr error
}

func (r *faultReader) Read(p []byte) (int, error) {
	if !r.fired {
		r.fired = true
		if r.cancel != nil {
			r.cancel()
		}
		if r.err != nil {
			return 0, r.err
		}
	}
	return r.data.Read(p)
}
func (r *faultReader) Close() error { return r.closeErr }

type recordingMutator struct{ calls int }

type failingMutator struct{ calls int }

func (m *failingMutator) ApplyVerifiedAggregate(context.Context, artifact.VerifiedAggregate) error {
	m.calls++
	return errors.New("mutation failed")
}

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
		{"identity mismatch", func(data map[string][]byte) { replaceManifest(data, `"id":"codex/hooks"`, `"id":"codex/skills"`) }},
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

func TestCandidateListingAndExactNonNewestResolution(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	for key, value := range source.data {
		if strings.HasPrefix(key, "final/") {
			source.data["old/"+strings.TrimPrefix(key, "final/")] = append([]byte(nil), value...)
		}
	}
	replaceManifestPrefix(source.data, "old/", "1.2.0", "1.1.0", "final", "final")
	latest, _ := artifact.ParseVersion("1.2.0")
	old, _ := artifact.ParseVersion("1.1.0")
	source.releases = []Release{makeRelease(t, source, latest, false, "final/"), makeRelease(t, source, old, false, "old/")}
	catalog, _ := New(source)
	selection := validSelection(t)
	candidates, err := catalog.ListCompatible(context.Background(), selection.Installer, FinalsOnly)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates=%d err=%v", len(candidates), err)
	}
	got, err := catalog.ResolveVersion(context.Background(), selection.Installer, FinalsOnly, old, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest().Version() != old {
		t.Fatalf("got %s", got.Manifest().Version())
	}
	missing, _ := artifact.ParseVersion("1.0.0")
	if _, err = catalog.ResolveVersion(context.Background(), selection.Installer, FinalsOnly, missing, 0); err == nil {
		t.Fatal("missing exact version fell back")
	}
}

func TestChosenCandidateTamperNeverFallsBackOrMutates(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	version, _ := artifact.ParseVersion("1.2.0")
	source.releases = []Release{makeRelease(t, source, version, false, "final/")}
	catalog, _ := New(source)
	selection := validSelection(t)
	candidates, err := catalog.ListCompatible(context.Background(), selection.Installer, FinalsOnly)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("err=%v candidates=%d", err, len(candidates))
	}
	source.data["final/"+artifact.AggregateManifestAsset] = append(source.data["final/"+artifact.AggregateManifestAsset], byte(' '))
	updateChecksum(source.data, "final/")
	mutator := &recordingMutator{}
	if _, err = catalog.ResolveCandidateAndApply(context.Background(), candidates[0], 0, mutator); err == nil {
		t.Fatal("expected selected identity mismatch")
	}
	if mutator.calls != 0 {
		t.Fatal("mutation occurred")
	}
}

func TestTrustedRevisionMismatchesAreIndependent(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"pasture", "aura"} {
		which := which
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			source := loadFixtureSource(t)
			version, _ := artifact.ParseVersion("1.2.0")
			source.releases = []Release{makeRelease(t, source, version, false, "final/")}
			selection := validSelection(t)
			wrong, _ := artifact.ParseRevision("3333333333333333333333333333333333333333")
			if which == "pasture" {
				selection.PastureRevision = wrong
			} else {
				selection.AuraRevision = wrong
			}
			mutator := &recordingMutator{}
			catalog, _ := New(source)
			if _, err := catalog.ResolveAndApply(context.Background(), selection, mutator); err == nil {
				t.Fatal("expected revision mismatch")
			}
			if mutator.calls != 0 {
				t.Fatal("mutation occurred")
			}
		})
	}
}

func TestMutationCallCountsAndErrors(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	version, _ := artifact.ParseVersion("1.2.0")
	source.releases = []Release{makeRelease(t, source, version, false, "final/")}
	catalog, _ := New(source)
	success := &recordingMutator{}
	if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), success); err != nil || success.calls != 1 {
		t.Fatalf("calls=%d err=%v", success.calls, err)
	}
	failure := &failingMutator{}
	if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), failure); err == nil || failure.calls != 1 {
		t.Fatalf("calls=%d err=%v", failure.calls, err)
	}
}

func TestCancellationListOpenAndMissingAssetNeverMutate(t *testing.T) {
	t.Parallel()
	version, _ := artifact.ParseVersion("1.2.0")
	for _, tc := range []struct {
		name    string
		prepare func(*fixtureSource)
	}{{"list", func(s *fixtureSource) { s.listErr = errors.New("list") }}, {"open", func(s *fixtureSource) { s.openErr = errors.New("open") }}, {"missing", func(s *fixtureSource) { delete(s.data, "final/"+artifact.AggregateManifestAsset) }}} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := loadFixtureSource(t)
			source.releases = []Release{makeRelease(t, source, version, false, "final/")}
			tc.prepare(source)
			mutator := &recordingMutator{}
			catalog, _ := New(source)
			if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), mutator); err == nil {
				t.Fatal("expected error")
			}
			if mutator.calls != 0 {
				t.Fatal("mutation occurred")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := loadFixtureSource(t)
	source.releases = []Release{makeRelease(t, source, version, false, "final/")}
	mutator := &recordingMutator{}
	catalog, _ := New(source)
	if _, err := catalog.ResolveAndApply(ctx, validSelection(t), mutator); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if mutator.calls != 0 {
		t.Fatal("mutation occurred")
	}
}

func TestDeclaredAssetSizeMismatchNeverMutates(t *testing.T) {
	t.Parallel()
	for _, delta := range []int64{-1, 1} {
		delta := delta
		t.Run(fmt.Sprintf("delta_%d", delta), func(t *testing.T) {
			t.Parallel()
			source := loadFixtureSource(t)
			version, _ := artifact.ParseVersion("1.2.0")
			release := makeRelease(t, source, version, false, "final/")
			asset := release.assets[artifact.AggregateManifestAsset]
			asset.size += delta
			release.assets[artifact.AggregateManifestAsset] = asset
			source.releases = []Release{release}
			mutator := &recordingMutator{}
			catalog, _ := New(source)
			if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), mutator); err == nil {
				t.Fatal("expected size failure")
			}
			if mutator.calls != 0 {
				t.Fatal("mutation occurred")
			}
		})
	}
}

func TestReadErrorAndMidReadCancellationNeverMutate(t *testing.T) {
	t.Parallel()
	version, _ := artifact.ParseVersion("1.2.0")
	t.Run("read error", func(t *testing.T) {
		source := loadFixtureSource(t)
		source.releases = []Release{makeRelease(t, source, version, false, "final/")}
		source.readErr = errors.New("read")
		mutator := &recordingMutator{}
		catalog, _ := New(source)
		if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), mutator); err == nil {
			t.Fatal("expected read error")
		}
		if mutator.calls != 0 {
			t.Fatal("mutation occurred")
		}
	})
	t.Run("mid read cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		source := loadFixtureSource(t)
		source.releases = []Release{makeRelease(t, source, version, false, "final/")}
		source.cancelOnRead = cancel
		mutator := &recordingMutator{}
		catalog, _ := New(source)
		if _, err := catalog.ResolveAndApply(ctx, validSelection(t), mutator); err == nil {
			t.Fatal("expected cancellation")
		}
		if mutator.calls != 0 {
			t.Fatal("mutation occurred")
		}
	})
	t.Run("close error", func(t *testing.T) {
		source := loadFixtureSource(t)
		source.releases = []Release{makeRelease(t, source, version, false, "final/")}
		source.closeErr = errors.New("close")
		mutator := &recordingMutator{}
		catalog, _ := New(source)
		if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), mutator); err == nil {
			t.Fatal("expected close error")
		}
		if mutator.calls != 0 {
			t.Fatal("mutation occurred")
		}
	})
}

func TestOversizedCandidateManifestNeverMutates(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	version, _ := artifact.ParseVersion("1.2.0")
	release := makeRelease(t, source, version, false, "final/")
	source.data["final/"+artifact.AggregateManifestAsset] = bytes.Repeat([]byte(" "), 4<<20+1)
	asset := release.assets[artifact.AggregateManifestAsset]
	asset.size = int64(len(source.data["final/"+artifact.AggregateManifestAsset]))
	release.assets[artifact.AggregateManifestAsset] = asset
	source.releases = []Release{release}
	mutator := &recordingMutator{}
	catalog, _ := New(source)
	if _, err := catalog.ResolveAndApply(context.Background(), validSelection(t), mutator); err == nil {
		t.Fatal("expected oversized manifest rejection")
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
