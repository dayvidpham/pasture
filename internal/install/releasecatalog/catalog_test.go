package releasecatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
)

type fixtureSource struct {
	releases     []release
	data         map[string][]byte
	listErr      error
	openErr      error
	readErr      error
	closeErr     error
	cancelURL    string
	cancelOnOpen string
	cancel       context.CancelFunc
	closeCalls   *int
	listCalls    int
}

func (s *fixtureSource) listReleases(context.Context) ([]release, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]release(nil), s.releases...), nil
}

func (s *fixtureSource) openAsset(_ context.Context, item asset) (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	name := item.downloadURL
	content, ok := s.data[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	reader := &faultReader{data: bytes.NewReader(content), err: s.readErr, closeErr: s.closeErr, cancel: s.cancel, cancelOnRead: name == s.cancelURL, closeCalls: s.closeCalls}
	if name == s.cancelOnOpen && s.cancel != nil {
		s.cancel()
	}
	return reader, nil
}

type faultReader struct {
	data         *bytes.Reader
	err          error
	closeErr     error
	cancel       context.CancelFunc
	cancelOnRead bool
	fired        bool
	closeCalls   *int
}

func (r *faultReader) Read(p []byte) (int, error) {
	if !r.fired {
		r.fired = true
		if r.cancelOnRead && r.cancel != nil {
			r.cancel()
		}
		if r.err != nil {
			return 0, r.err
		}
	}
	return r.data.Read(p)
}
func (r *faultReader) Close() error {
	if r.closeCalls != nil {
		(*r.closeCalls)++
	}
	return r.closeErr
}

func TestListCompatiblePolicyAndExactNonNewestResolution(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	for key, value := range source.data {
		if strings.HasPrefix(key, "final/") {
			source.data["old/"+strings.TrimPrefix(key, "final/")] = append([]byte(nil), value...)
		}
	}
	replaceManifestPrefix(source.data, "old/", "1.2.0", "1.1.0", "final", "final")
	latest := mustVersion(t, "1.2.0")
	old := mustVersion(t, "1.1.0")
	rc := mustVersion(t, "1.3.0-rc.1")
	source.releases = []release{makeRelease(t, source, latest, false, "final/"), makeRelease(t, source, old, false, "old/"), makeRelease(t, source, rc, true, "rc/")}
	catalog, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)

	finals, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
	if err != nil || len(finals) != 2 || finals[0].Version() != latest || finals[1].Version() != old {
		t.Fatalf("finals=%v err=%v", finals, err)
	}
	all, err := catalog.ListCompatible(context.Background(), installerVersion(t), IncludePrereleases)
	if err != nil || len(all) != 3 || all[0].Version() != rc {
		t.Fatalf("all=%v err=%v", all, err)
	}
	verified, err := catalog.ResolveCandidate(context.Background(), finals[1], 0)
	if err != nil || verified.Manifest().Version() != old {
		t.Fatalf("version=%s err=%v", verified.Manifest().Version(), err)
	}
	missing := mustVersion(t, "1.0.0")
	for _, candidate := range finals {
		if candidate.Version() == missing {
			t.Fatal("an absent exact version fell back")
		}
	}
}

func TestListCompatibleRejectsUnconstructedInstallerBeforeSourceIO(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	catalog, err := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := catalog.ListCompatible(context.Background(), artifact.Version{}, FinalsOnly)
	var typed *Error
	if candidates != nil || !errors.As(err, &typed) || typed.Stage != "candidate listing" || typed.Location != "installer version" || source.listCalls != 0 {
		t.Fatalf("candidates=%v typed=%v err=%v list calls=%d", candidates, typed, err, source.listCalls)
	}

	zero, err := artifact.ParseVersion("0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	candidates, err = catalog.ListCompatible(context.Background(), zero, FinalsOnly)
	if err != nil || candidates == nil || source.listCalls != 1 {
		t.Fatalf("parsed zero candidates=%v err=%v list calls=%d", candidates, err, source.listCalls)
	}
}

func TestMalformedNewestDoesNotHideValidOlderCandidate(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	for key, value := range source.data {
		if strings.HasPrefix(key, "final/") {
			source.data["old/"+strings.TrimPrefix(key, "final/")] = append([]byte(nil), value...)
		}
	}
	replaceManifestPrefix(source.data, "old/", "1.2.0", "1.1.0", "final", "final")
	newest := makeRelease(t, source, mustVersion(t, "1.2.0"), false, "final/")
	delete(newest.assets, artifact.AggregateManifestAsset)
	older := makeRelease(t, source, mustVersion(t, "1.1.0"), false, "old/")
	source.releases = []release{newest, older}
	catalog, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
	candidates, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
	if err != nil || len(candidates) != 1 || candidates[0].Version() != older.version {
		t.Fatalf("candidates=%v err=%v", candidates, err)
	}
}

func TestExactCandidateTamperAndVerificationFaultsReturnNoVerifiedOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string][]byte)
	}{
		{"checksum", func(data map[string][]byte) {
			data["final/"+artifact.AggregateChecksumAsset] = []byte(strings.Repeat("0", 64) + "  " + artifact.AggregateManifestAsset + "\n")
		}},
		{"component digest", func(data map[string][]byte) { data["final/pasture-1.2.0-codex-hooks.tgz"] = []byte("corrupt") }},
		{"identity", func(data map[string][]byte) { replaceManifest(data, `"id":"codex/hooks"`, `"id":"codex/skills"`) }},
		{"version changed after listing", func(data map[string][]byte) {
			replaceManifestPrefix(data, "final/", "1.2.0", "1.1.0", "final", "final")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := loadFixtureSource(t)
			version := mustVersion(t, "1.2.0")
			source.releases = []release{makeRelease(t, source, version, false, "final/")}
			catalog, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
			candidates, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
			if err != nil || len(candidates) != 1 {
				t.Fatalf("candidates=%d err=%v", len(candidates), err)
			}
			test.mutate(source.data)
			if verified, err := catalog.ResolveCandidate(context.Background(), candidates[0], 0); err == nil || verified.Manifest().Version().String() != "" {
				t.Fatalf("verified=%v err=%v", verified, err)
			}
		})
	}
}

func TestDiscoveryPreservesOperationalErrorsAndCancellation(t *testing.T) {
	t.Parallel()
	causes := []struct {
		name  string
		set   func(*fixtureSource)
		cause error
	}{
		{"list", func(s *fixtureSource) { s.listErr = io.ErrUnexpectedEOF }, io.ErrUnexpectedEOF},
		{"open", func(s *fixtureSource) { s.openErr = os.ErrPermission }, os.ErrPermission},
		{"read", func(s *fixtureSource) { s.readErr = io.ErrUnexpectedEOF }, io.ErrUnexpectedEOF},
		{"close", func(s *fixtureSource) { s.closeErr = os.ErrClosed }, os.ErrClosed},
	}
	for _, test := range causes {
		t.Run(test.name, func(t *testing.T) {
			source := loadFixtureSource(t)
			source.releases = []release{makeRelease(t, source, mustVersion(t, "1.2.0"), false, "final/")}
			test.set(source)
			catalog, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
			candidates, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
			if candidates != nil || !errors.Is(err, test.cause) {
				t.Fatalf("candidates=%v err=%v", candidates, err)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	source := loadFixtureSource(t)
	source.releases = []release{makeRelease(t, source, mustVersion(t, "1.2.0"), false, "final/")}
	source.cancel, source.cancelURL = cancel, "final/"+artifact.AggregateManifestAsset
	catalog, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
	if candidates, err := catalog.ListCompatible(ctx, installerVersion(t), FinalsOnly); candidates != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("candidates=%v err=%v", candidates, err)
	}
}

func TestCancellationAfterAssetOpenClosesReaderExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	source := loadFixtureSource(t)
	source.releases = []release{makeRelease(t, source, mustVersion(t, "1.2.0"), false, "final/")}
	closeCalls := 0
	source.cancel = cancel
	source.cancelOnOpen = "final/" + artifact.AggregateManifestAsset
	source.closeCalls = &closeCalls
	source.closeErr = os.ErrClosed
	catalog, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
	candidates, err := catalog.ListCompatible(ctx, installerVersion(t), FinalsOnly)
	if candidates != nil || !errors.Is(err, context.Canceled) || closeCalls != 1 {
		t.Fatalf("candidates=%v err=%v close calls=%d", candidates, err, closeCalls)
	}
}

func TestFinalComponentAndPostVerificationCancellationReturnNoVerifiedOutput(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"final component", "post verification"} {
		t.Run(stage, func(t *testing.T) {
			source := loadFixtureSource(t)
			source.releases = []release{makeRelease(t, source, mustVersion(t, "1.2.0"), false, "final/")}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			verifier := aggregateVerifier(artifact.VerifyAggregate)
			if stage == "post verification" {
				verifier = func(ctx context.Context, source artifact.AggregateAssetSource, requirements artifact.AggregateRequirements) (artifact.VerifiedAggregate, error) {
					verified, err := artifact.VerifyAggregate(ctx, source, requirements)
					if err == nil {
						cancel()
					}
					return verified, err
				}
			}
			catalog, _ := newCatalog(source, DiscoveryLimits{}, verifier)
			candidates, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
			if err != nil || len(candidates) != 1 {
				t.Fatalf("candidates=%d err=%v", len(candidates), err)
			}
			if stage == "final component" {
				components := candidates[0].manifest.Components()
				source.cancelURL = "final/" + components[len(components)-1].Asset()
				source.cancel = cancel
			}
			verified, err := catalog.ResolveCandidate(ctx, candidates[0], 0)
			if !errors.Is(err, context.Canceled) || verified.Manifest().Version().String() != "" {
				t.Fatalf("verified=%v err=%v", verified, err)
			}
		})
	}
}

func TestCandidateOwnershipAndLimits(t *testing.T) {
	t.Parallel()
	source := loadFixtureSource(t)
	source.releases = []release{makeRelease(t, source, mustVersion(t, "1.2.0"), false, "final/")}
	first, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
	second, _ := newCatalog(source, DiscoveryLimits{}, artifact.VerifyAggregate)
	candidates, _ := first.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
	if _, err := second.ResolveCandidate(context.Background(), candidates[0], 0); err == nil {
		t.Fatal("foreign candidate accepted")
	}
	if _, err := first.ResolveCandidate(context.Background(), candidates[0], math.MaxInt64); err == nil {
		t.Fatal("overflowing limit accepted")
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
		content, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		data["final/"+entry.Name()] = content
	}
	for key, value := range data {
		data["rc/"+strings.TrimPrefix(key, "final/")] = append([]byte(nil), value...)
	}
	replaceManifestPrefix(data, "rc/", "1.2.0", "1.3.0-rc.1", "final", "prerelease")
	return &fixtureSource{data: data}
}

func makeRelease(t *testing.T, source *fixtureSource, version artifact.Version, prerelease bool, prefix string) release {
	t.Helper()
	manifest, err := artifact.ParseAggregateManifest(source.data[prefix+artifact.AggregateManifestAsset])
	if err != nil {
		t.Fatal(err)
	}
	names := []string{artifact.AggregateManifestAsset, artifact.AggregateChecksumAsset}
	for _, component := range manifest.Components() {
		names = append(names, component.Asset())
	}
	assets := map[string]asset{}
	for _, name := range names {
		location := prefix + name
		assets[name] = asset{name: name, downloadURL: location, size: int64(len(source.data[location]))}
	}
	return release{version: version, prerelease: prerelease, assets: assets}
}

func installerVersion(t *testing.T) artifact.Version { return mustVersion(t, "1.5.0") }
func mustVersion(t *testing.T, value string) artifact.Version {
	t.Helper()
	version, err := artifact.ParseVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
func replaceManifest(data map[string][]byte, old, replacement string) {
	data["final/"+artifact.AggregateManifestAsset] = []byte(strings.Replace(string(data["final/"+artifact.AggregateManifestAsset]), old, replacement, 1))
	updateChecksum(data, "final/")
}
func replaceManifestPrefix(data map[string][]byte, prefix, oldVersion, newVersion, oldChannel, newChannel string) {
	value := strings.ReplaceAll(string(data[prefix+artifact.AggregateManifestAsset]), oldVersion, newVersion)
	value = strings.Replace(value, `"channel":"`+oldChannel+`"`, `"channel":"`+newChannel+`"`, 1)
	data[prefix+artifact.AggregateManifestAsset] = []byte(value)
	for key, content := range data {
		if strings.HasPrefix(key, prefix+"pasture-"+oldVersion) {
			data[strings.Replace(key, oldVersion, newVersion, 1)] = content
			delete(data, key)
		}
	}
	updateChecksum(data, prefix)
}
func updateChecksum(data map[string][]byte, prefix string) {
	sum := sha256.Sum256(data[prefix+artifact.AggregateManifestAsset])
	data[prefix+artifact.AggregateChecksumAsset] = []byte(hex.EncodeToString(sum[:]) + "  " + artifact.AggregateManifestAsset + "\n")
}
