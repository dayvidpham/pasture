package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// roundTripFunc adapts a function to http.RoundTripper so a test can fail loudly
// if the flow reaches the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// upgradeTestServer serves the GitHub release endpoints and raw asset bytes the
// flow consumes. It records API and per-asset request counts so a test can prove
// that a path made no request at all.
type upgradeTestServer struct {
	t             *testing.T
	server        *httptest.Server
	mu            sync.Mutex
	latest        *upgradeRelease
	tags          map[string]upgradeRelease
	assets        map[string][]byte
	apiRequests   int
	assetRequests map[string]int
}

func newUpgradeTestServer(t *testing.T) *upgradeTestServer {
	t.Helper()
	s := &upgradeTestServer{
		t:             t,
		tags:          map[string]upgradeRelease{},
		assets:        map[string][]byte{},
		assetRequests: map[string]int{},
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *upgradeTestServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := r.URL.Path
	switch {
	case path == "/releases/latest":
		s.apiRequests++
		if s.latest == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeUpgradeJSON(w, *s.latest)
	case strings.HasPrefix(path, "/releases/tags/"):
		s.apiRequests++
		release, ok := s.tags[strings.TrimPrefix(path, "/releases/tags/")]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeUpgradeJSON(w, release)
	case strings.HasPrefix(path, "/assets/"):
		name := strings.TrimPrefix(path, "/assets/")
		s.assetRequests[name]++
		data, ok := s.assets[name]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// release builds a release whose assets point back at this server.
func (s *upgradeTestServer) release(tag string, assetNames ...string) upgradeRelease {
	release := upgradeRelease{TagName: tag, HTMLURL: "https://example.test/" + tag}
	for _, name := range assetNames {
		release.Assets = append(release.Assets, upgradeAsset{
			Name:               name,
			BrowserDownloadURL: s.server.URL + "/assets/" + name,
		})
	}
	return release
}

func (s *upgradeTestServer) setLatest(release upgradeRelease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = &release
}

func (s *upgradeTestServer) setTag(release upgradeRelease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[release.TagName] = release
}

func (s *upgradeTestServer) setAsset(name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assets[name] = data
}

func (s *upgradeTestServer) assetRequestCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assetRequests[name]
}

func (s *upgradeTestServer) totalAssetRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, count := range s.assetRequests {
		total += count
	}
	return total
}

func writeUpgradeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// checksumsFor renders a checksums.txt body in sha256sum's "<hex>  <name>" form.
func checksumsFor(assets map[string][]byte) []byte {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	var b bytes.Buffer
	for _, name := range names {
		fmt.Fprintf(&b, "%x  %s\n", sha256.Sum256(assets[name]), name)
	}
	return b.Bytes()
}

func upgradeDepsForServer(t *testing.T, s *upgradeTestServer, executable string) upgradeDeps {
	t.Helper()
	return upgradeDeps{
		Executable:      func() (string, error) { return executable, nil },
		EvalSymlinks:    func(path string) (string, error) { return path, nil },
		Stat:            os.Stat,
		CommandOutput:   func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not managed") },
		HTTPClient:      s.server.Client(),
		APIBaseURL:      s.server.URL,
		GOOS:            "linux",
		GOARCH:          "amd64",
		CurrentVersion:  "v0.0.15",
		InstallBinary:   installPastureBinary,
		StdinIsTerminal: func(io.Reader) bool { return false },
	}
}

// upgradeDepsNoNetwork returns deps whose HTTP client fails the test if used.
func upgradeDepsNoNetwork(t *testing.T, executable string) upgradeDeps {
	t.Helper()
	return upgradeDeps{
		Executable:   func() (string, error) { return executable, nil },
		EvalSymlinks: func(path string) (string, error) { return path, nil },
		Stat:         os.Stat,
		CommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("not managed")
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("this path must not call the network")
			return nil, errors.New("unexpected request")
		})},
		GOOS:            "linux",
		GOARCH:          "amd64",
		CurrentVersion:  "v0.0.15",
		InstallBinary:   func(string, []byte, os.FileMode) error { t.Fatal("this path must not replace the binary"); return nil },
		StdinIsTerminal: func(io.Reader) bool { return false },
	}
}

func runUpgradeForTest(opts upgradeOptions, deps upgradeDeps, in io.Reader) (string, error) {
	var out bytes.Buffer
	err := runUpgradeCommand(context.Background(), &out, in, opts, deps)
	return out.String(), err
}

func requireUpgradeError(t *testing.T, err error, code int) *upgradeError {
	t.Helper()
	var ue *upgradeError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v, want *upgradeError", err)
	}
	if ue.code() != code {
		t.Fatalf("exit code = %d, want %d\n%s", ue.code(), code, ue.Error())
	}
	return ue
}

func writeTestBinary(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// upgradeTempExecutable returns a temp-dir path holding a plausible installed
// main binary, so the destination planner finds no co-located daemon sibling.
func upgradeTempExecutable(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), upgradeMainBinaryName)
	writeTestBinary(t, target, "installed-bytes", 0o755)
	return target
}

func TestUpgradeCommandRegisteredWithUpdateAlias(t *testing.T) {
	// SERIAL: this test reads the shared rootCmd, which other serial tests
	// execute, so it must not use t.Parallel.
	for _, name := range []string{"upgrade", "update"} {
		if _, _, err := rootCmd.Find([]string{name}); err != nil {
			t.Fatalf("find %q command: %v", name, err)
		}
	}
	if rootCmd.Flags().Lookup("upgrade") == nil {
		t.Fatal("root command must register --upgrade")
	}
	cmd, _, err := rootCmd.Find([]string{"upgrade"})
	if err != nil {
		t.Fatalf("find upgrade: %v", err)
	}
	if cmd.Flags().Lookup("prerelease") != nil {
		t.Fatal("upgrade must not carry a --prerelease flag")
	}
	for _, forbidden := range []string{"PROPOSAL", "SLICE-", "beads://", "RATIFIED"} {
		if strings.Contains(cmd.Short+cmd.Long, forbidden) {
			t.Fatalf("upgrade help leaks internal identifier %q", forbidden)
		}
	}
}

func TestUpgradeManagedAdvice(t *testing.T) {
	t.Parallel()

	type probe struct {
		name   string
		args   string
		stdout string
	}
	cases := []struct {
		name         string
		path         string
		probes       []probe
		wantContains []string
	}{
		{
			name:         "nix",
			path:         "/nix/store/abc-pasture/bin/pasture",
			wantContains: []string{"managed by nix", "nix profile upgrade pasture", "nix profile install github:dayvidpham/pasture#pasture"},
		},
		{
			name: "dpkg",
			path: "/usr/bin/pasture",
			probes: []probe{
				{name: "dpkg-query", args: "-S /usr/bin/pasture", stdout: "pasture: /usr/bin/pasture\n"},
			},
			wantContains: []string{"managed by dpkg", "sudo apt update && sudo apt install --only-upgrade pasture"},
		},
		{
			name: "rpm",
			path: "/usr/bin/pasture",
			probes: []probe{
				{name: "rpm", args: "-qf /usr/bin/pasture", stdout: "pasture-0.0.15-1.x86_64\n"},
			},
			wantContains: []string{"managed by rpm", "sudo dnf upgrade pasture-0.0.15-1.x86_64"},
		},
		{
			name: "pacman",
			path: "/usr/bin/pasture",
			probes: []probe{
				{name: "pacman", args: "-Qo /usr/bin/pasture", stdout: "/usr/bin/pasture is owned by pasture 0.0.15-1\n"},
			},
			wantContains: []string{"managed by pacman", "sudo pacman -Syu pasture"},
		},
		{
			name: "homebrew",
			path: "/opt/homebrew/bin/pasture",
			probes: []probe{
				{name: "brew", args: "list --cask pasture", stdout: "/opt/homebrew/bin/pasture\n"},
			},
			wantContains: []string{"managed by homebrew", "brew update && brew upgrade pasture"},
		},
		{
			// The nix path check wins over a matching dpkg probe.
			name: "nix-precedence-over-dpkg",
			path: "/nix/store/abc-pasture/bin/pasture",
			probes: []probe{
				{name: "dpkg-query", args: "-S /nix/store/abc-pasture/bin/pasture", stdout: "pasture: /nix/store/abc-pasture/bin/pasture\n"},
			},
			wantContains: []string{"managed by nix"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps := upgradeDepsNoNetwork(t, tc.path)
			deps.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				for _, p := range tc.probes {
					if p.name == name && p.args == joined {
						return []byte(p.stdout), nil
					}
				}
				return nil, errors.New("not managed by this test command")
			}

			output, err := runUpgradeForTest(upgradeOptions{}, deps, nil)
			if err != nil {
				t.Fatalf("managed advice returned error: %v\noutput:\n%s", err, output)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(output, want) {
					t.Fatalf("advice missing %q:\n%s", want, output)
				}
			}
			if !strings.Contains(output, "No files were changed") {
				t.Fatalf("advice must state no files were changed:\n%s", output)
			}
			if !strings.Contains(output, upgradeReleasePageURL) {
				t.Fatalf("advice must cite the release page:\n%s", output)
			}
		})
	}
}

func TestUpgradeMixedChannelsRefused(t *testing.T) {
	t.Parallel()
	deps := upgradeDepsNoNetwork(t, "/usr/bin/pasture")
	deps.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "dpkg-query" && strings.Join(args, " ") == "-S /usr/bin/pasture" {
			return []byte("pasture: /usr/bin/pasture\n"), nil
		}
		return nil, errors.New("not managed by this test command")
	}
	destinations := []upgradeDestination{
		{Name: upgradeMainBinaryName, Path: "/usr/bin/pasture"},
		{Name: upgradeDaemonBinaryName, Path: "/usr/local/bin/pastured"},
	}
	classifications := classifyUpgradeDestinations(context.Background(), deps, destinations)

	var out bytes.Buffer
	advised, err := preflightUpgradeDestinations(&out, classifications)
	if advised {
		t.Fatal("mixed channels must not be advised")
	}
	ue := requireUpgradeError(t, err, 1)
	for _, want := range []string{"/usr/bin/pasture", "dpkg", "/usr/local/bin/pastured", "raw"} {
		if !strings.Contains(ue.Error(), want) {
			t.Fatalf("refusal missing %q:\n%s", want, ue.Error())
		}
	}
	if out.Len() != 0 {
		t.Fatalf("refusal must not print advice, got:\n%s", out.String())
	}
}

func TestUpgradeDevelRefused(t *testing.T) {
	t.Parallel()
	deps := upgradeDepsNoNetwork(t, "/usr/local/bin/pasture")
	deps.CurrentVersion = "devel"

	output, err := runUpgradeForTest(upgradeOptions{}, deps, nil)
	ue := requireUpgradeError(t, err, 1)
	for _, want := range []string{"devel", upgradeInstallerURL, upgradeReleasePageURL} {
		if !strings.Contains(ue.Error(), want) {
			t.Fatalf("devel refusal missing %q:\n%s", want, ue.Error())
		}
	}
	if output != "" {
		t.Fatalf("devel refusal must print nothing on stdout, got:\n%s", output)
	}
}

func TestUpgradeManagedDevelAdvisesInsteadOfRefusing(t *testing.T) {
	t.Parallel()
	deps := upgradeDepsNoNetwork(t, "/nix/store/abc-pasture/bin/pasture")
	deps.CurrentVersion = "devel"

	output, err := runUpgradeForTest(upgradeOptions{}, deps, nil)
	if err != nil {
		t.Fatalf("managed devel must advise, got error: %v", err)
	}
	if !strings.Contains(output, "managed by nix") {
		t.Fatalf("managed devel must print manager advice:\n%s", output)
	}
}

func TestUpgradeRejectsBadFlagsBeforeNetwork(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		opts         upgradeOptions
		wantContains string
	}{
		{
			name:         "unsafe-version",
			opts:         upgradeOptions{Version: "0.0.16;rm", VersionSet: true},
			wantContains: "unsafe character",
		},
		{
			name:         "empty-version",
			opts:         upgradeOptions{Version: "", VersionSet: true},
			wantContains: "empty",
		},
		{
			name:         "allow-downgrade-without-version",
			opts:         upgradeOptions{AllowDowngrade: true},
			wantContains: "--version <tag> --allow-downgrade",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps := upgradeDepsNoNetwork(t, "/usr/local/bin/pasture")
			_, err := runUpgradeForTest(tc.opts, deps, nil)
			ue := requireUpgradeError(t, err, 1)
			if !strings.Contains(ue.Error(), tc.wantContains) {
				t.Fatalf("error missing %q:\n%s", tc.wantContains, ue.Error())
			}
		})
	}
}

func TestUpgradeVersionOrder(t *testing.T) {
	t.Parallel()
	const (
		pastureAsset  = "pasture-linux-amd64"
		checksumsName = "checksums.txt"
	)

	t.Run("newer-target-dry-run", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		s.setLatest(s.release("v0.0.16", checksumsName, pastureAsset))
		deps := upgradeDepsForServer(t, s, upgradeTempExecutable(t))

		output, err := runUpgradeForTest(upgradeOptions{DryRun: true}, deps, nil)
		if err != nil {
			t.Fatalf("dry run returned error: %v\n%s", err, output)
		}
		for _, want := range []string{"Upgrade plan", "target version:  v0.0.16", "dry run: no files were changed"} {
			if !strings.Contains(output, want) {
				t.Fatalf("plan missing %q:\n%s", want, output)
			}
		}
		if s.totalAssetRequests() != 0 {
			t.Fatalf("dry run must make zero asset requests, got %d", s.totalAssetRequests())
		}
	})

	t.Run("older-target-refused", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		s.setLatest(s.release("v0.0.14", checksumsName, pastureAsset))
		deps := upgradeDepsForServer(t, s, upgradeTempExecutable(t))

		_, err := runUpgradeForTest(upgradeOptions{}, deps, nil)
		ue := requireUpgradeError(t, err, 1)
		if !strings.Contains(ue.Error(), "--version v0.0.14 --allow-downgrade") {
			t.Fatalf("downgrade refusal missing retry shape:\n%s", ue.Error())
		}
		if s.totalAssetRequests() != 0 {
			t.Fatalf("downgrade refusal must make zero asset requests, got %d", s.totalAssetRequests())
		}
	})

	t.Run("older-target-allowed", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		release := s.release("v0.0.14", checksumsName, pastureAsset)
		s.setTag(release)
		deps := upgradeDepsForServer(t, s, upgradeTempExecutable(t))

		output, err := runUpgradeForTest(upgradeOptions{Version: "0.0.14", VersionSet: true, AllowDowngrade: true, DryRun: true}, deps, nil)
		if err != nil {
			t.Fatalf("allowed downgrade returned error: %v\n%s", err, output)
		}
		if !strings.Contains(output, "downgrade override") {
			t.Fatalf("missing downgrade override line:\n%s", output)
		}
		if !strings.Contains(output, "target version:  v0.0.14") {
			t.Fatalf("plan missing target:\n%s", output)
		}
	})

	t.Run("same-target-already-at", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		target := filepath.Join(t.TempDir(), upgradeMainBinaryName)
		writeTestBinary(t, target, "installed-bytes", 0o755)
		s.setAsset(checksumsName, checksumsFor(map[string][]byte{pastureAsset: []byte("installed-bytes")}))
		s.setLatest(s.release("v0.0.15", checksumsName, pastureAsset))
		deps := upgradeDepsForServer(t, s, target)

		output, err := runUpgradeForTest(upgradeOptions{}, deps, nil)
		if err != nil {
			t.Fatalf("already-at returned error: %v\n%s", err, output)
		}
		if !strings.Contains(output, "already at v0.0.15; no files were changed.") {
			t.Fatalf("missing already-at message:\n%s", output)
		}
		if s.assetRequestCount(pastureAsset) != 0 {
			t.Fatalf("already-at must not request the binary asset, got %d", s.assetRequestCount(pastureAsset))
		}
		if s.assetRequestCount(checksumsName) != 1 {
			t.Fatalf("already-at must fetch checksums.txt exactly once, got %d", s.assetRequestCount(checksumsName))
		}
	})
}

func TestUpgradeVersionCompare(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		current       string
		target        string
		wantOrder     string
		wantErrSubstr string
	}{
		{name: "equal", current: "v1.2.3", target: "1.2.3", wantOrder: "same"},
		{name: "v-prefix-equal", current: "1.2.3", target: "v1.2.3", wantOrder: "same"},
		{name: "patch-bump", current: "v1.2.3", target: "v1.2.4", wantOrder: "before"},
		{name: "minor-bump", current: "v1.2.3", target: "v1.3.0", wantOrder: "before"},
		{name: "major-bump", current: "v1.2.3", target: "v2.0.0", wantOrder: "before"},
		{name: "newer-current", current: "v2.0.0", target: "v1.2.3", wantOrder: "after"},
		{name: "prerelease-before-release", current: "v1.0.0-rc.1", target: "v1.0.0", wantOrder: "before"},
		{name: "release-after-prerelease", current: "v1.0.0", target: "v1.0.0-rc.1", wantOrder: "after"},
		{name: "rc-number-order", current: "v1.0.0-rc.1", target: "v1.0.0-rc.2", wantOrder: "before"},
		{name: "numeric-before-alphanumeric", current: "v1.0.0-1", target: "v1.0.0-alpha", wantOrder: "before"},
		{name: "shorter-identifier-set-first", current: "v1.0.0-alpha", target: "v1.0.0-alpha.1", wantOrder: "before"},
		{name: "build-metadata-ignored", current: "v1.0.0+build1", target: "v1.0.0+build2", wantOrder: "same"},
		{name: "malformed-current", current: "not-a-version", target: "v1.0.0", wantErrSubstr: "could not be ordered"},
		{name: "leading-zero", current: "v01.0.0", target: "v1.0.0", wantErrSubstr: "leading zero"},
		{name: "two-part", current: "v1.0", target: "v1.0.0", wantErrSubstr: "MAJOR.MINOR.PATCH"},
		{name: "empty", current: "", target: "v1.0.0", wantErrSubstr: "empty"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := compareUpgradeVersions(tc.current, tc.target)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("compareUpgradeVersions(%q, %q) succeeded, want error", tc.current, tc.target)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("error missing %q:\n%s", tc.wantErrSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compareUpgradeVersions(%q, %q) error: %v", tc.current, tc.target, err)
			}
			if got.String() != tc.wantOrder {
				t.Fatalf("compareUpgradeVersions(%q, %q) = %s, want %s", tc.current, tc.target, got, tc.wantOrder)
			}
		})
	}
}

func TestUpgradeAssetSelection(t *testing.T) {
	t.Parallel()

	t.Run("asset-names", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			goos, goarch string
			want         string
		}{
			{"linux", "amd64", "pasture-linux-amd64"},
			{"linux", "arm64", "pasture-linux-arm64"},
			{"darwin", "amd64", "pasture-darwin-amd64"},
			{"darwin", "arm64", "pasture-darwin-arm64"},
		}
		for _, tc := range cases {
			if got := upgradeAssetName(upgradeMainBinaryName, tc.goos, tc.goarch); got != tc.want {
				t.Fatalf("upgradeAssetName(%s,%s) = %s, want %s", tc.goos, tc.goarch, got, tc.want)
			}
		}
	})

	t.Run("unsupported-platform", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		release := s.release("v0.0.16", "checksums.txt", "pasture-windows-amd64")
		s.setLatest(release)
		deps := upgradeDepsForServer(t, s, upgradeTempExecutable(t))
		deps.GOOS = "windows"

		_, err := runUpgradeForTest(upgradeOptions{DryRun: true}, deps, nil)
		ue := requireUpgradeError(t, err, 1)
		if !strings.Contains(ue.Error(), "windows") {
			t.Fatalf("unsupported-platform refusal missing platform:\n%s", ue.Error())
		}
	})

	t.Run("unsupported-arch", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		release := s.release("v0.0.16", "checksums.txt", "pasture-linux-386")
		s.setLatest(release)
		deps := upgradeDepsForServer(t, s, upgradeTempExecutable(t))
		deps.GOARCH = "386"

		_, err := runUpgradeForTest(upgradeOptions{DryRun: true}, deps, nil)
		ue := requireUpgradeError(t, err, 1)
		if !strings.Contains(ue.Error(), "386") {
			t.Fatalf("unsupported-arch refusal missing arch:\n%s", ue.Error())
		}
	})

	t.Run("missing-checksums-asset", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		s.setLatest(s.release("v0.0.16", "pasture-linux-amd64"))
		deps := upgradeDepsForServer(t, s, upgradeTempExecutable(t))

		_, err := runUpgradeForTest(upgradeOptions{DryRun: true}, deps, nil)
		ue := requireUpgradeError(t, err, 1)
		if !strings.Contains(ue.Error(), "checksums.txt") {
			t.Fatalf("missing-checksums refusal missing name:\n%s", ue.Error())
		}
	})

	t.Run("missing-planned-asset", func(t *testing.T) {
		t.Parallel()
		s := newUpgradeTestServer(t)
		s.setLatest(s.release("v0.0.16", "checksums.txt"))
		deps := upgradeDepsForServer(t, s, upgradeTempExecutable(t))

		_, err := runUpgradeForTest(upgradeOptions{DryRun: true}, deps, nil)
		ue := requireUpgradeError(t, err, 1)
		if !strings.Contains(ue.Error(), "pasture-linux-amd64") {
			t.Fatalf("missing-asset refusal missing name:\n%s", ue.Error())
		}
	})
}

func TestUpgradeSingleBinaryInstall(t *testing.T) {
	t.Parallel()
	s := newUpgradeTestServer(t)
	dir := t.TempDir()
	target := filepath.Join(dir, upgradeMainBinaryName)
	writeTestBinary(t, target, "old-bytes", 0o755)

	assetBytes := []byte("verified-new-bytes")
	s.setAsset(upgradeMainBinaryName+"-linux-amd64", assetBytes)
	s.setAsset("checksums.txt", checksumsFor(map[string][]byte{upgradeMainBinaryName + "-linux-amd64": assetBytes}))
	s.setLatest(s.release("v0.0.16", "checksums.txt", upgradeMainBinaryName+"-linux-amd64"))

	deps := upgradeDepsForServer(t, s, target)
	output, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
	if err != nil {
		t.Fatalf("install returned error: %v\n%s", err, output)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read replaced binary: %v", err)
	}
	if string(got) != string(assetBytes) {
		t.Fatalf("target bytes = %q, want %q", got, assetBytes)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat replaced binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	if !strings.Contains(output, "installed pasture at "+target) {
		t.Fatalf("output missing installed line:\n%s", output)
	}
	if s.assetRequestCount(upgradeDaemonBinaryName+"-linux-amd64") != 0 {
		t.Fatal("a single-binary install must not request the daemon asset")
	}
	if s.assetRequestCount("pasture-release-linux-amd64") != 0 {
		t.Fatal("the internal release tool must never be requested")
	}
}

func TestUpgradeNonExecutableTargetGetsFallbackMode(t *testing.T) {
	t.Parallel()
	s := newUpgradeTestServer(t)
	dir := t.TempDir()
	target := filepath.Join(dir, upgradeMainBinaryName)
	writeTestBinary(t, target, "old-bytes", 0o644)

	assetBytes := []byte("verified-new-bytes")
	s.setAsset(upgradeMainBinaryName+"-linux-amd64", assetBytes)
	s.setAsset("checksums.txt", checksumsFor(map[string][]byte{upgradeMainBinaryName + "-linux-amd64": assetBytes}))
	s.setLatest(s.release("v0.0.16", "checksums.txt", upgradeMainBinaryName+"-linux-amd64"))

	deps := upgradeDepsForServer(t, s, target)
	if _, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil); err != nil {
		t.Fatalf("install returned error: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat replaced binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755 fallback", info.Mode().Perm())
	}
}

func TestUpgradeChecksumMismatchPreservesTarget(t *testing.T) {
	t.Parallel()
	s := newUpgradeTestServer(t)
	dir := t.TempDir()
	target := filepath.Join(dir, upgradeMainBinaryName)
	writeTestBinary(t, target, "original-bytes", 0o755)

	name := upgradeMainBinaryName + "-linux-amd64"
	s.setAsset("checksums.txt", checksumsFor(map[string][]byte{name: []byte("expected-good-bytes")}))
	s.setAsset(name, []byte("tampered-bytes"))
	s.setLatest(s.release("v0.0.16", "checksums.txt", name))

	deps := upgradeDepsForServer(t, s, target)
	_, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
	ue := requireUpgradeError(t, err, 1)
	if !strings.Contains(ue.Error(), "checksum did not match") {
		t.Fatalf("mismatch error missing wording:\n%s", ue.Error())
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(got) != "original-bytes" {
		t.Fatalf("target changed on mismatch: %q", got)
	}
	entries, dirErr := os.ReadDir(dir)
	if dirErr != nil {
		t.Fatalf("read dir: %v", dirErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pasture-upgrade-") {
			t.Fatalf("staging temp left behind: %s", entry.Name())
		}
	}
}

func TestUpgradeConfirmationMatrix(t *testing.T) {
	t.Parallel()

	newInstallServer := func(t *testing.T) (*upgradeTestServer, string, []byte) {
		t.Helper()
		s := newUpgradeTestServer(t)
		dir := t.TempDir()
		target := filepath.Join(dir, upgradeMainBinaryName)
		writeTestBinary(t, target, "old-bytes", 0o755)
		assetBytes := []byte("verified-new-bytes")
		name := upgradeMainBinaryName + "-linux-amd64"
		s.setAsset(name, assetBytes)
		s.setAsset("checksums.txt", checksumsFor(map[string][]byte{name: assetBytes}))
		s.setLatest(s.release("v0.0.16", "checksums.txt", name))
		return s, target, assetBytes
	}

	t.Run("yes-skips-prompt", func(t *testing.T) {
		t.Parallel()
		s, target, assetBytes := newInstallServer(t)
		deps := upgradeDepsForServer(t, s, target)
		output, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
		if err != nil {
			t.Fatalf("--yes returned error: %v\n%s", err, output)
		}
		got, _ := os.ReadFile(target)
		if string(got) != string(assetBytes) {
			t.Fatalf("target not replaced: %q", got)
		}
	})

	t.Run("no-cancels", func(t *testing.T) {
		t.Parallel()
		s, target, _ := newInstallServer(t)
		deps := upgradeDepsForServer(t, s, target)
		deps.StdinIsTerminal = func(io.Reader) bool { return true }
		output, err := runUpgradeForTest(upgradeOptions{}, deps, strings.NewReader("n\n"))
		if err != nil {
			t.Fatalf("cancel returned error: %v\n%s", err, output)
		}
		if !strings.Contains(output, "upgrade cancelled: no files were changed") {
			t.Fatalf("missing cancellation message:\n%s", output)
		}
		if got, _ := os.ReadFile(target); string(got) != "old-bytes" {
			t.Fatalf("target changed on cancel: %q", got)
		}
		if s.totalAssetRequests() != 0 {
			t.Fatalf("cancel must make zero asset requests, got %d", s.totalAssetRequests())
		}
	})

	t.Run("non-terminal-refused", func(t *testing.T) {
		t.Parallel()
		s, target, _ := newInstallServer(t)
		deps := upgradeDepsForServer(t, s, target)
		deps.StdinIsTerminal = func(io.Reader) bool { return false }
		_, err := runUpgradeForTest(upgradeOptions{}, deps, strings.NewReader(""))
		ue := requireUpgradeError(t, err, 1)
		if !strings.Contains(ue.Error(), "interactive confirmation") {
			t.Fatalf("non-terminal refusal missing wording:\n%s", ue.Error())
		}
		if s.totalAssetRequests() != 0 {
			t.Fatalf("non-terminal refusal must make zero asset requests, got %d", s.totalAssetRequests())
		}
	})

	t.Run("terminal-yes-proceeds", func(t *testing.T) {
		t.Parallel()
		s, target, assetBytes := newInstallServer(t)
		deps := upgradeDepsForServer(t, s, target)
		deps.StdinIsTerminal = func(io.Reader) bool { return true }
		output, err := runUpgradeForTest(upgradeOptions{}, deps, strings.NewReader("yes\n"))
		if err != nil {
			t.Fatalf("interactive yes returned error: %v\n%s", err, output)
		}
		if got, _ := os.ReadFile(target); string(got) != string(assetBytes) {
			t.Fatalf("target not replaced: %q", got)
		}
	})

	t.Run("dry-run-no-asset-requests-no-stdin", func(t *testing.T) {
		t.Parallel()
		s, target, _ := newInstallServer(t)
		deps := upgradeDepsForServer(t, s, target)
		deps.StdinIsTerminal = func(io.Reader) bool { return true }
		output, err := runUpgradeForTest(upgradeOptions{DryRun: true}, deps, upgradeFailingReader{t: t})
		if err != nil {
			t.Fatalf("dry run returned error: %v\n%s", err, output)
		}
		if s.totalAssetRequests() != 0 {
			t.Fatalf("dry run must make zero asset requests, got %d", s.totalAssetRequests())
		}
		if got, _ := os.ReadFile(target); string(got) != "old-bytes" {
			t.Fatalf("dry run changed target: %q", got)
		}
	})
}

type upgradeFailingReader struct{ t *testing.T }

func (r upgradeFailingReader) Read([]byte) (int, error) {
	r.t.Fatal("stdin must not be read")
	return 0, io.EOF
}

func TestInstallPastureBinaryCleansTempOnRenameFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A directory stands where the target file would go, so the rename fails.
	target := filepath.Join(dir, upgradeMainBinaryName)
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := installPastureBinary(target, []byte("bytes"), 0o755); err == nil {
		t.Fatal("installPastureBinary succeeded over a directory, want error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pasture-upgrade-") {
			t.Fatalf("staging temp left behind: %s", entry.Name())
		}
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func requireFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %v, want %v", path, info.Mode().Perm(), want)
	}
}

func TestUpgradePairHappyPath(t *testing.T) {
	t.Parallel()
	s := newUpgradeTestServer(t)
	dir := t.TempDir()
	mainPath := filepath.Join(dir, upgradeMainBinaryName)
	daemonPath := filepath.Join(dir, upgradeDaemonBinaryName)
	releaseToolPath := filepath.Join(dir, "pasture-release")
	writeTestBinary(t, mainPath, "old-pasture", 0o755)
	writeTestBinary(t, daemonPath, "old-pastured", 0o700)
	writeTestBinary(t, releaseToolPath, "internal-tool-bytes", 0o755)

	mainBytes := []byte("new-pasture-bytes")
	daemonBytes := []byte("new-pastured-bytes")
	mainAsset := upgradeAssetName(upgradeMainBinaryName, "linux", "amd64")
	daemonAsset := upgradeAssetName(upgradeDaemonBinaryName, "linux", "amd64")
	s.setAsset(mainAsset, mainBytes)
	s.setAsset(daemonAsset, daemonBytes)
	s.setAsset("checksums.txt", checksumsFor(map[string][]byte{mainAsset: mainBytes, daemonAsset: daemonBytes}))
	s.setLatest(s.release("v0.0.16", "checksums.txt", mainAsset, daemonAsset))

	deps := upgradeDepsForServer(t, s, mainPath)
	output, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
	if err != nil {
		t.Fatalf("pair install returned error: %v\n%s", err, output)
	}
	assertFileContent(t, mainPath, string(mainBytes))
	assertFileContent(t, daemonPath, string(daemonBytes))
	requireFileMode(t, daemonPath, 0o700)
	assertFileContent(t, releaseToolPath, "internal-tool-bytes")
	for _, want := range []string{
		"installed pasture at " + mainPath,
		"installed pastured at " + daemonPath,
		"restart pastured when convenient",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("pair output missing %q:\n%s", want, output)
		}
	}
	if s.assetRequestCount("pasture-release-linux-amd64") != 0 {
		t.Fatal("the internal release tool must never be requested")
	}
}

func TestUpgradePartialFailureRerun(t *testing.T) {
	t.Parallel()
	s := newUpgradeTestServer(t)
	dir := t.TempDir()
	mainPath := filepath.Join(dir, upgradeMainBinaryName)
	daemonPath := filepath.Join(dir, upgradeDaemonBinaryName)
	writeTestBinary(t, mainPath, "old-pasture", 0o755)
	writeTestBinary(t, daemonPath, "old-pastured", 0o755)

	mainBytes := []byte("new-pasture-bytes")
	daemonBytes := []byte("new-pastured-bytes")
	mainAsset := upgradeAssetName(upgradeMainBinaryName, "linux", "amd64")
	daemonAsset := upgradeAssetName(upgradeDaemonBinaryName, "linux", "amd64")
	s.setAsset(mainAsset, mainBytes)
	s.setAsset(daemonAsset, daemonBytes)
	s.setAsset("checksums.txt", checksumsFor(map[string][]byte{mainAsset: mainBytes, daemonAsset: daemonBytes}))
	s.setLatest(s.release("v0.0.16", "checksums.txt", mainAsset, daemonAsset))

	failDaemon := true
	deps := upgradeDepsForServer(t, s, mainPath)
	deps.InstallBinary = func(path string, bytes []byte, mode os.FileMode) error {
		if filepath.Base(path) == upgradeDaemonBinaryName && failDaemon {
			return errors.New("simulated daemon replacement failure")
		}
		return installPastureBinary(path, bytes, mode)
	}

	// Run 1: the main binary is replaced, the daemon rename fails.
	_, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
	ue := requireUpgradeError(t, err, 1)
	if !strings.Contains(ue.Error(), "pasture was replaced") {
		t.Fatalf("partial failure must name the replaced binary:\n%s", ue.Error())
	}
	assertFileContent(t, mainPath, string(mainBytes))
	assertFileContent(t, daemonPath, "old-pastured")
	mainRequestsAfterRun1 := s.assetRequestCount(mainAsset)

	// Run 2: the new CLI at the same tag repairs only the differing daemon.
	failDaemon = false
	deps.CurrentVersion = "v0.0.16"
	output, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
	if err != nil {
		t.Fatalf("rerun returned error: %v\n%s", err, output)
	}
	assertFileContent(t, mainPath, string(mainBytes))
	assertFileContent(t, daemonPath, string(daemonBytes))
	if got := s.assetRequestCount(mainAsset); got != mainRequestsAfterRun1 {
		t.Fatalf("repair must not re-download the matching binary: requests %d -> %d", mainRequestsAfterRun1, got)
	}
	if got := s.assetRequestCount(daemonAsset); got < 2 {
		t.Fatalf("repair must download the differing daemon asset: requests %d", got)
	}
}

func TestUpgradeSameTagRepair(t *testing.T) {
	t.Parallel()
	mainAsset := upgradeAssetName(upgradeMainBinaryName, "linux", "amd64")
	daemonAsset := upgradeAssetName(upgradeDaemonBinaryName, "linux", "amd64")
	mainBytes := []byte("new-pasture-bytes")
	daemonBytes := []byte("new-pastured-bytes")

	setup := func(t *testing.T, mainContent, daemonContent string) (*upgradeTestServer, string, string) {
		t.Helper()
		s := newUpgradeTestServer(t)
		dir := t.TempDir()
		mainPath := filepath.Join(dir, upgradeMainBinaryName)
		daemonPath := filepath.Join(dir, upgradeDaemonBinaryName)
		writeTestBinary(t, mainPath, mainContent, 0o755)
		writeTestBinary(t, daemonPath, daemonContent, 0o755)
		s.setAsset(mainAsset, mainBytes)
		s.setAsset(daemonAsset, daemonBytes)
		s.setAsset("checksums.txt", checksumsFor(map[string][]byte{mainAsset: mainBytes, daemonAsset: daemonBytes}))
		s.setLatest(s.release("v0.0.16", "checksums.txt", mainAsset, daemonAsset))
		return s, mainPath, daemonPath
	}

	t.Run("main-differs-only", func(t *testing.T) {
		t.Parallel()
		s, mainPath, daemonPath := setup(t, "stale-pasture", string(daemonBytes))
		deps := upgradeDepsForServer(t, s, mainPath)
		deps.CurrentVersion = "v0.0.16"

		output, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
		if err != nil {
			t.Fatalf("repair returned error: %v\n%s", err, output)
		}
		assertFileContent(t, mainPath, string(mainBytes))
		assertFileContent(t, daemonPath, string(daemonBytes))
		if !strings.Contains(output, "Repair plan") || !strings.Contains(output, mainPath) {
			t.Fatalf("repair plan must name the differing binary:\n%s", output)
		}
		if strings.Contains(output, daemonPath) {
			t.Fatalf("repair plan must not name the matching binary:\n%s", output)
		}
		if s.assetRequestCount(daemonAsset) != 0 {
			t.Fatal("a matching binary's asset must not be downloaded")
		}
	})

	t.Run("sibling-differs-only", func(t *testing.T) {
		t.Parallel()
		s, mainPath, daemonPath := setup(t, string(mainBytes), "stale-pastured")
		deps := upgradeDepsForServer(t, s, mainPath)
		deps.CurrentVersion = "v0.0.16"

		output, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
		if err != nil {
			t.Fatalf("repair returned error: %v\n%s", err, output)
		}
		assertFileContent(t, mainPath, string(mainBytes))
		assertFileContent(t, daemonPath, string(daemonBytes))
		daemonLine := "binary to verify and repair if it differs: " + daemonPath + "\n"
		mainLine := "binary to verify and repair if it differs: " + mainPath + "\n"
		if !strings.Contains(output, daemonLine) || strings.Contains(output, mainLine) {
			t.Fatalf("repair plan must name only the differing daemon:\n%s", output)
		}
		if s.assetRequestCount(mainAsset) != 0 {
			t.Fatal("a matching binary's asset must not be downloaded")
		}
	})

	t.Run("checksums-entry-missing", func(t *testing.T) {
		t.Parallel()
		s, mainPath, daemonPath := setup(t, "stale-pasture", "stale-pastured")
		s.setAsset("checksums.txt", checksumsFor(map[string][]byte{mainAsset: mainBytes}))
		deps := upgradeDepsForServer(t, s, mainPath)
		deps.CurrentVersion = "v0.0.16"

		_, err := runUpgradeForTest(upgradeOptions{Yes: true}, deps, nil)
		ue := requireUpgradeError(t, err, 1)
		if !strings.Contains(ue.Error(), daemonAsset) {
			t.Fatalf("missing-entry failure must name the asset:\n%s", ue.Error())
		}
		assertFileContent(t, mainPath, "stale-pasture")
		assertFileContent(t, daemonPath, "stale-pastured")
	})

	t.Run("dry-run-no-fetch", func(t *testing.T) {
		t.Parallel()
		s, mainPath, daemonPath := setup(t, "stale-pasture", "stale-pastured")
		deps := upgradeDepsForServer(t, s, mainPath)
		deps.CurrentVersion = "v0.0.16"

		output, err := runUpgradeForTest(upgradeOptions{DryRun: true}, deps, nil)
		if err != nil {
			t.Fatalf("repair dry run returned error: %v\n%s", err, output)
		}
		if !strings.Contains(output, "Repair plan") || !strings.Contains(output, "dry run: no files were changed") {
			t.Fatalf("repair dry run missing plan or dry-run line:\n%s", output)
		}
		if s.totalAssetRequests() != 0 {
			t.Fatalf("repair dry run must make zero asset requests, got %d", s.totalAssetRequests())
		}
		assertFileContent(t, mainPath, "stale-pasture")
		assertFileContent(t, daemonPath, "stale-pastured")
	})
}
