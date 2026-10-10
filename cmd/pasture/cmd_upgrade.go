package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Durable endpoints cited in user-facing output and used by the production
// wiring. The API base URL is injectable through upgradeDeps so tests can point
// the flow at an httptest server.
const (
	upgradeAPIBaseURL     = "https://api.github.com/repos/dayvidpham/pasture"
	upgradeReleasePageURL = "https://github.com/dayvidpham/pasture/releases"
	upgradeInstallerURL   = "https://raw.githubusercontent.com/dayvidpham/pasture/main/install.sh"
	upgradeChecksumsName  = "checksums.txt"

	upgradeHTTPTimeout    = 90 * time.Second
	upgradeProbeTimeout   = 2 * time.Second
	upgradeChecksumsLimit = 4 << 20
	upgradeBinaryLimit    = 128 << 20

	upgradeMainBinaryName   = "pasture"
	upgradeDaemonBinaryName = "pastured"
)

// upgradeInstallKind classifies how a planned destination reached the machine.
// Only the raw kind is replaceable; every managed kind is advised and left
// untouched so package-manager ownership metadata stays correct.
type upgradeInstallKind int

const (
	upgradeInstallRaw upgradeInstallKind = iota
	upgradeInstallNix
	upgradeInstallHomebrew
	upgradeInstallDPKG
	upgradeInstallRPM
	upgradeInstallPacman
)

func (k upgradeInstallKind) String() string {
	switch k {
	case upgradeInstallRaw:
		return "raw"
	case upgradeInstallNix:
		return "nix"
	case upgradeInstallHomebrew:
		return "homebrew"
	case upgradeInstallDPKG:
		return "dpkg"
	case upgradeInstallRPM:
		return "rpm"
	case upgradeInstallPacman:
		return "pacman"
	default:
		return "unknown"
	}
}

// upgradeOptions carries the parsed command flags into the core. VersionSet
// distinguishes an explicit --version (which permits --allow-downgrade and an
// exact tag) from the absent flag.
type upgradeOptions struct {
	Version        string
	VersionSet     bool
	DryRun         bool
	Yes            bool
	AllowDowngrade bool
}

// upgradeDeps is the injected boundary around every external effect the core
// needs: the running executable, symlink resolution, the filesystem stat used to
// find a co-located sibling, package-manager probes, the release HTTP client,
// the host platform, the current version stamp, the verified-binary install
// primitive, and terminal detection. Production wiring is defaultUpgradeDeps.
type upgradeDeps struct {
	Executable      func() (string, error)
	EvalSymlinks    func(string) (string, error)
	Stat            func(string) (fs.FileInfo, error)
	CommandOutput   func(context.Context, string, ...string) ([]byte, error)
	HTTPClient      *http.Client
	APIBaseURL      string
	GOOS            string
	GOARCH          string
	CurrentVersion  string
	InstallBinary   func(string, []byte, fs.FileMode) error
	StdinIsTerminal func(io.Reader) bool
}

// upgradeAsset is one published release asset.
type upgradeAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// upgradeRelease is the subset of the GitHub release JSON the flow consumes.
type upgradeRelease struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Assets  []upgradeAsset `json:"assets"`
}

func (r upgradeRelease) asset(name string) (upgradeAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return upgradeAsset{}, false
}

// upgradeDestination is a binary the flow may replace: its published asset name
// and the resolved path to write.
type upgradeDestination struct {
	Name string
	Path string
}

// upgradeClassification is a planned destination paired with its detected
// channel. Raw is a valid classification, not a failure.
type upgradeClassification struct {
	Destination upgradeDestination
	Kind        upgradeInstallKind
	Package     string
}

// upgradePlanItem pairs a destination with the release asset that will replace
// it, after verification.
type upgradePlanItem struct {
	Destination upgradeDestination
	Asset       upgradeAsset
}

// upgradePlan is the assembled, verified set of replacements for one release.
type upgradePlan struct {
	Checksums upgradeAsset
	Items     []upgradePlanItem
}

// verifiedBinary is a downloaded asset whose SHA-256 matched checksums.txt,
// ready to be renamed over its destination.
type verifiedBinary struct {
	Destination upgradeDestination
	Bytes       []byte
	Mode        fs.FileMode
}

// upgradeError is the local six-part failure type. It carries what/why/where/
// when/impact/fix, an optional wrapped cause, and the process exit code the
// adapter translates. The core returns it; only the production adapter exits.
type upgradeError struct {
	what   string
	why    string
	where  string
	when   string
	impact string
	fix    string
	cause  error
	exit   int
}

func (e *upgradeError) Error() string {
	var b strings.Builder
	b.WriteString("upgrade failed\n\n")
	writeUpgradeErrorField(&b, "what", e.what)
	writeUpgradeErrorField(&b, "why", e.why)
	if e.cause != nil {
		writeUpgradeErrorField(&b, "details", e.cause.Error())
	}
	writeUpgradeErrorField(&b, "where", e.where)
	writeUpgradeErrorField(&b, "when", e.when)
	writeUpgradeErrorField(&b, "means", e.impact)
	writeUpgradeErrorField(&b, "fix", e.fix)
	return strings.TrimRight(b.String(), "\n")
}

func (e *upgradeError) Unwrap() error { return e.cause }

// code is the process exit code the adapter translates: 1 for validation and
// local-failure classes, 2 for release-query and download failures.
func (e *upgradeError) code() int {
	if e.exit == 0 {
		return 1
	}
	return e.exit
}

func writeUpgradeErrorField(b *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	lines := strings.Split(value, "\n")
	fmt.Fprintf(b, "%-8s %s\n", label+":", lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(b, "%-8s %s\n", "", line)
	}
}

func newUpgradeError(exit int, what, why, where, when, impact, fix string) *upgradeError {
	return &upgradeError{
		what:   what,
		why:    why,
		where:  where,
		when:   when,
		impact: impact,
		fix:    fix,
		exit:   exit,
	}
}

func (e *upgradeError) withCause(err error) *upgradeError {
	e.cause = err
	return e
}

// upgradeCommandLong is the user-facing help. It describes the raw replacement
// of the co-located pair after checksums.txt verification, the managed-channel
// advice, and the four flags. No internal process identifier appears here.
const upgradeCommandLong = `Upgrade pasture to the latest release.

The upgrade replaces the running pasture binary, and the pastured daemon when
it is co-located, with the bytes published for this machine after verifying
them against the release's checksums.txt. Nothing is downloaded or replaced
before the plan is confirmed, and no artifact whose hash does not match is ever
installed. Package-manager installs (nix, homebrew, apt/dpkg, dnf/rpm, pacman)
are detected and advised, never modified.

Flags:
  --version TAG       install an exact release (X.Y.Z or vX.Y.Z)
  --dry-run           print the plan and change nothing
  --yes               skip the interactive confirmation prompt
  --allow-downgrade   allow an older target, only together with --version`

// buildUpgradeCommand assembles the production command from injected deps. The
// RunE is the production adapter: it prints the returned failure and translates
// its exit code. In-process tests call runUpgradeCommand directly.
func buildUpgradeCommand(deps upgradeDeps) *cobra.Command {
	deps = normalizeUpgradeDeps(deps)
	opts := upgradeOptions{}
	cmd := &cobra.Command{
		Use:          "upgrade",
		Aliases:      []string{"update"},
		Short:        "Upgrade pasture to the latest release",
		Long:         upgradeCommandLong,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			runOpts := opts
			runOpts.VersionSet = cmd.Flags().Changed("version")
			return runUpgradeAdapter(ctx, cmd.OutOrStdout(), cmd.InOrStdin(), runOpts, deps)
		},
	}
	cmd.Flags().StringVar(&opts.Version, "version", "", "Install an exact release tag (X.Y.Z or vX.Y.Z)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Print the upgrade plan without changing files")
	cmd.Flags().BoolVar(&opts.Yes, "yes", false, "Accept the replacement plan without an interactive prompt")
	cmd.Flags().BoolVar(&opts.AllowDowngrade, "allow-downgrade", false, "Allow installing an older release when paired with --version")
	return cmd
}

func defaultUpgradeDeps() upgradeDeps {
	return upgradeDeps{
		Executable:      os.Executable,
		EvalSymlinks:    filepath.EvalSymlinks,
		Stat:            os.Stat,
		CommandOutput:   defaultUpgradeCommandOutput,
		HTTPClient:      &http.Client{Timeout: upgradeHTTPTimeout},
		APIBaseURL:      upgradeAPIBaseURL,
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		CurrentVersion:  version,
		InstallBinary:   installPastureBinary,
		StdinIsTerminal: upgradeInputIsTerminal,
	}
}

func normalizeUpgradeDeps(deps upgradeDeps) upgradeDeps {
	defaults := defaultUpgradeDeps()
	if deps.Executable == nil {
		deps.Executable = defaults.Executable
	}
	if deps.EvalSymlinks == nil {
		deps.EvalSymlinks = defaults.EvalSymlinks
	}
	if deps.Stat == nil {
		deps.Stat = defaults.Stat
	}
	if deps.CommandOutput == nil {
		deps.CommandOutput = defaults.CommandOutput
	}
	if deps.HTTPClient == nil {
		deps.HTTPClient = defaults.HTTPClient
	}
	if strings.TrimSpace(deps.APIBaseURL) == "" {
		deps.APIBaseURL = defaults.APIBaseURL
	}
	if deps.GOOS == "" {
		deps.GOOS = defaults.GOOS
	}
	if deps.GOARCH == "" {
		deps.GOARCH = defaults.GOARCH
	}
	if deps.CurrentVersion == "" {
		deps.CurrentVersion = defaults.CurrentVersion
	}
	if deps.InstallBinary == nil {
		deps.InstallBinary = defaults.InstallBinary
	}
	if deps.StdinIsTerminal == nil {
		deps.StdinIsTerminal = defaults.StdinIsTerminal
	}
	return deps
}

// runUpgradeAdapter is the production adapter: it renders the returned typed
// failure with the shared error printer and exits with the failure's code. The
// core never exits the process, which is what makes it in-process testable.
func runUpgradeAdapter(ctx context.Context, out io.Writer, in io.Reader, opts upgradeOptions, deps upgradeDeps) error {
	if err := runUpgradeCommand(ctx, out, in, opts, deps); err != nil {
		printError(err)
		exitWithCode(upgradeExitCode(err))
	}
	return nil
}

// runUpgradeRoot is the root --upgrade branch: the default flow with no flags.
func runUpgradeRoot(cmd *cobra.Command) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return runUpgradeAdapter(ctx, cmd.OutOrStdout(), cmd.InOrStdin(), upgradeOptions{}, defaultUpgradeDeps())
}

func upgradeExitCode(err error) int {
	var ue *upgradeError
	if errors.As(err, &ue) {
		return ue.code()
	}
	return 1
}

// runUpgradeCommand is the production core. It returns typed *upgradeError
// values and never exits the process.
func runUpgradeCommand(ctx context.Context, out io.Writer, in io.Reader, opts upgradeOptions, deps upgradeDeps) error {
	if out == nil {
		out = io.Discard
	}
	deps = normalizeUpgradeDeps(deps)

	// 1. Flag validation, before any probe or network.
	if opts.AllowDowngrade && strings.TrimSpace(opts.Version) == "" {
		return allowDowngradeWithoutVersionError()
	}
	if opts.VersionSet || strings.TrimSpace(opts.Version) != "" {
		cleaned, err := cleanUpgradeRequestedVersion(opts.Version)
		if err != nil {
			return err
		}
		opts.Version = cleaned
	}

	// 2. Executable resolution.
	executablePath, err := deps.Executable()
	if err != nil {
		return executableResolutionError(err)
	}
	resolvedPath := executablePath
	if evaluated, evalErr := deps.EvalSymlinks(executablePath); evalErr == nil && evaluated != "" {
		resolvedPath = evaluated
	}

	// 3. Destination preflight, before any release query or modification.
	destinations := planUpgradeDestinations(deps, resolvedPath)
	classifications := classifyUpgradeDestinations(ctx, deps, destinations)
	advised, err := preflightUpgradeDestinations(out, classifications)
	if err != nil {
		return err
	}
	if advised {
		return nil
	}

	// 4. Devel refusal, before any GitHub call.
	if isDevelVersion(deps.CurrentVersion) {
		return develRefusalError()
	}

	// 5. Release resolution.
	release, err := resolveUpgradeRelease(ctx, deps, opts)
	if err != nil {
		return err
	}

	// 6. Version order.
	order, err := compareUpgradeVersions(deps.CurrentVersion, release.TagName)
	if err != nil {
		return err
	}
	switch order {
	case upgradeVersionSame:
		printUpgradeAlreadyAt(out, destinations, release.TagName)
		return nil
	case upgradeVersionAfter:
		if opts.Version == "" || !opts.AllowDowngrade {
			return downgradeRefusalError(deps.CurrentVersion, release.TagName)
		}
		fmt.Fprintf(out, "downgrade override: current %s sorts after target %s; continuing because --allow-downgrade was set.\n", deps.CurrentVersion, release.TagName)
	}

	// 7. Plan assembly.
	plan, err := assembleUpgradePlan(deps, release, destinations)
	if err != nil {
		return err
	}

	// 8. Print the plan; a dry run stops here with no asset request.
	printUpgradePlan(out, deps, release, plan)
	if opts.DryRun {
		fmt.Fprintln(out, "dry run: no files were changed")
		return nil
	}

	// 9. Confirm.
	confirmed, err := confirmUpgradePlan(in, out, opts, deps)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}

	// 10. Download and verify every planned asset before any replacement.
	verified, err := downloadAndVerifyUpgradeAssets(ctx, deps, plan)
	if err != nil {
		return err
	}

	// 11. Replace in order.
	return replaceUpgradeBinaries(out, deps, verified)
}

// planUpgradeDestinations resolves the binaries this run may replace. The main
// binary is always the resolved running executable.
func planUpgradeDestinations(_ upgradeDeps, resolvedPath string) []upgradeDestination {
	return []upgradeDestination{
		{Name: upgradeMainBinaryName, Path: resolvedPath},
	}
}

// classifyUpgradeDestinations classifies every planned destination. A probe that
// errors is simply not a match, so an unclassifiable path is raw.
func classifyUpgradeDestinations(ctx context.Context, deps upgradeDeps, destinations []upgradeDestination) []upgradeClassification {
	classifications := make([]upgradeClassification, 0, len(destinations))
	for _, destination := range destinations {
		classifications = append(classifications, classifyUpgradeDestination(ctx, deps, destination))
	}
	return classifications
}

func classifyUpgradeDestination(ctx context.Context, deps upgradeDeps, destination upgradeDestination) upgradeClassification {
	path := destination.Path
	if strings.HasPrefix(filepath.Clean(path), "/nix/store/") {
		return upgradeClassification{Destination: destination, Kind: upgradeInstallNix}
	}
	if install, ok := detectHomebrewInstall(ctx, deps, path); ok {
		return upgradeClassification{Destination: destination, Kind: upgradeInstallHomebrew, Package: install}
	}
	if pkg, ok := probeUpgradePackage(ctx, deps, "dpkg-query", []string{"-S", path}, func(out string) string {
		return strings.TrimSpace(strings.SplitN(out, ":", 2)[0])
	}); ok {
		return upgradeClassification{Destination: destination, Kind: upgradeInstallDPKG, Package: pkg}
	}
	if pkg, ok := probeUpgradePackage(ctx, deps, "rpm", []string{"-qf", path}, func(out string) string {
		return strings.TrimSpace(out)
	}); ok {
		return upgradeClassification{Destination: destination, Kind: upgradeInstallRPM, Package: pkg}
	}
	if pkg, ok := probeUpgradePackage(ctx, deps, "pacman", []string{"-Qo", path}, parsePacmanOwner); ok {
		return upgradeClassification{Destination: destination, Kind: upgradeInstallPacman, Package: pkg}
	}
	return upgradeClassification{Destination: destination, Kind: upgradeInstallRaw}
}

func probeUpgradePackage(ctx context.Context, deps upgradeDeps, name string, args []string, parse func(string) string) (string, bool) {
	out, err := deps.CommandOutput(ctx, name, args...)
	if err != nil {
		return "", false
	}
	pkg := parse(string(out))
	if pkg == "" {
		pkg = upgradeMainBinaryName
	}
	return pkg, true
}

func detectHomebrewInstall(ctx context.Context, deps upgradeDeps, path string) (string, bool) {
	if out, err := deps.CommandOutput(ctx, "brew", "list", "--cask", upgradeMainBinaryName); err == nil && strings.Contains(string(out), path) {
		return upgradeMainBinaryName, true
	}
	if out, err := deps.CommandOutput(ctx, "brew", "list", upgradeMainBinaryName); err == nil && strings.Contains(string(out), path) {
		return upgradeMainBinaryName, true
	}
	if _, err := deps.CommandOutput(ctx, "brew", "list", "--cask", "--versions", upgradeMainBinaryName); err != nil {
		return "", false
	}
	prefixOut, err := deps.CommandOutput(ctx, "brew", "--prefix")
	if err != nil {
		return "", false
	}
	prefix := strings.TrimSpace(string(prefixOut))
	if prefix == "" {
		return "", false
	}
	clean := filepath.Clean(path)
	if clean == prefix || strings.HasPrefix(clean, prefix+string(os.PathSeparator)) {
		return upgradeMainBinaryName, true
	}
	return "", false
}

// preflightUpgradeDestinations decides whether the run may proceed. It returns
// true when managed advice was printed (the caller stops with exit 0). A mixed
// layout returns a refusal.
func preflightUpgradeDestinations(out io.Writer, classifications []upgradeClassification) (bool, error) {
	managed := make([]upgradeClassification, 0, len(classifications))
	for _, classification := range classifications {
		if classification.Kind != upgradeInstallRaw {
			managed = append(managed, classification)
		}
	}
	if len(managed) == 0 {
		return false, nil
	}
	if len(managed) == len(classifications) && sameUpgradeKind(managed) {
		printManagedUpgradeAdvice(out, classifications, managed)
		return true, nil
	}
	return false, mixedChannelRefusalError(classifications)
}

func sameUpgradeKind(classifications []upgradeClassification) bool {
	for _, classification := range classifications[1:] {
		if classification.Kind != classifications[0].Kind {
			return false
		}
	}
	return true
}

func printManagedUpgradeAdvice(out io.Writer, classifications, managed []upgradeClassification) {
	kind := managed[0].Kind
	destinations := make([]upgradeDestination, 0, len(classifications))
	for _, classification := range classifications {
		destinations = append(destinations, classification.Destination)
	}
	names := destinationPhrase(destinations)
	fmt.Fprintf(out, "%s appear to be managed by %s.\n", names, kind)
	for _, classification := range managed {
		fmt.Fprintf(out, "  %s (%s)\n", classification.Destination.Path, classification.Kind)
	}
	fmt.Fprintln(out, "No files were changed because direct replacement would leave package-manager ownership metadata stale.")
	fmt.Fprintln(out, "Use the manager-owned upgrade path instead:")
	switch kind {
	case upgradeInstallNix:
		fmt.Fprintf(out, "  nix profile upgrade %s\n", upgradeMainBinaryName)
		fmt.Fprintf(out, "  # If no profile entry exists: nix profile install github:dayvidpham/pasture#%s\n", upgradeMainBinaryName)
	case upgradeInstallHomebrew:
		fmt.Fprintf(out, "  brew update && brew upgrade %s\n", upgradeMainBinaryName)
	case upgradeInstallDPKG:
		fmt.Fprintf(out, "  sudo apt update && sudo apt install --only-upgrade %s\n", managed[0].Package)
	case upgradeInstallRPM:
		fmt.Fprintf(out, "  sudo dnf upgrade %s\n", managed[0].Package)
	case upgradeInstallPacman:
		fmt.Fprintf(out, "  sudo pacman -Syu %s\n", managed[0].Package)
	default:
		fmt.Fprintf(out, "  Open %s and follow the install guide for this channel.\n", upgradeReleasePageURL)
	}
	fmt.Fprintf(out, "See %s\n", upgradeReleasePageURL)
}

func mixedChannelRefusalError(classifications []upgradeClassification) *upgradeError {
	lines := make([]string, 0, len(classifications))
	for _, classification := range classifications {
		lines = append(lines, fmt.Sprintf("%s is %s", classification.Destination.Path, classification.Kind))
	}
	return newUpgradeError(
		1,
		"the pasture and pastured binaries are installed through different channels",
		strings.Join(lines, "; "),
		"pasture upgrade",
		"before querying releases or changing any file",
		"the pair cannot move independently without creating version skew, so no files were changed",
		"align both binaries on one channel: upgrade the managed one through its manager and reinstall the raw one through the same manager or the installer, or remove the stray raw copy",
	)
}

// resolveUpgradeRelease queries exactly one release: the requested tag, or the
// stable-only latest endpoint.
func resolveUpgradeRelease(ctx context.Context, deps upgradeDeps, opts upgradeOptions) (upgradeRelease, error) {
	base := strings.TrimRight(deps.APIBaseURL, "/")
	var release upgradeRelease
	if opts.Version != "" {
		tag := normalizeUpgradeTag(opts.Version)
		if err := getUpgradeJSON(ctx, deps.HTTPClient, base+"/releases/tags/"+url.PathEscape(tag), &release); err != nil {
			return upgradeRelease{}, releaseQueryError(tag, err)
		}
		return release, nil
	}
	if err := getUpgradeJSON(ctx, deps.HTTPClient, base+"/releases/latest", &release); err != nil {
		return upgradeRelease{}, releaseQueryError("latest", err)
	}
	return release, nil
}

func getUpgradeJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pasture-upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", endpoint, err)
	}
	return nil
}

func downloadUpgradeBytes(ctx context.Context, client *http.Client, endpoint string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pasture-upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET %s returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download from %s exceeded %d bytes", endpoint, limit)
	}
	return data, nil
}

// assembleUpgradePlan validates the platform and requires every planned asset
// plus checksums.txt to be present before any download.
func assembleUpgradePlan(deps upgradeDeps, release upgradeRelease, destinations []upgradeDestination) (upgradePlan, error) {
	if !upgradePlatformSupported(deps.GOOS, deps.GOARCH) {
		return upgradePlan{}, unsupportedPlatformError(deps.GOOS, deps.GOARCH)
	}
	checksums, ok := release.asset(upgradeChecksumsName)
	if !ok {
		return upgradePlan{}, missingChecksumsAssetError(release.TagName)
	}
	plan := upgradePlan{Checksums: checksums}
	for _, destination := range destinations {
		name := upgradeAssetName(destination.Name, deps.GOOS, deps.GOARCH)
		asset, ok := release.asset(name)
		if !ok {
			return upgradePlan{}, missingAssetError(release.TagName, name)
		}
		plan.Items = append(plan.Items, upgradePlanItem{Destination: destination, Asset: asset})
	}
	return plan, nil
}

func upgradeAssetName(binary, goos, goarch string) string {
	return fmt.Sprintf("%s-%s-%s", binary, goos, goarch)
}

func upgradePlatformSupported(goos, goarch string) bool {
	if goos != "linux" && goos != "darwin" {
		return false
	}
	return goarch == "amd64" || goarch == "arm64"
}

func printUpgradePlan(out io.Writer, deps upgradeDeps, release upgradeRelease, plan upgradePlan) {
	names := make([]string, 0, len(plan.Items))
	for _, item := range plan.Items {
		names = append(names, item.Destination.Name)
	}
	fmt.Fprintln(out, "Upgrade plan:")
	fmt.Fprintf(out, "  current version: %s\n", deps.CurrentVersion)
	fmt.Fprintf(out, "  target version:  %s\n", release.TagName)
	fmt.Fprintf(out, "  machine:         %s/%s\n", deps.GOOS, deps.GOARCH)
	fmt.Fprintf(out, "  binaries:        %s\n", strings.Join(names, ", "))
	fmt.Fprintf(out, "  checksums source: %s\n", plan.Checksums.BrowserDownloadURL)
	for _, item := range plan.Items {
		fmt.Fprintf(out, "  download source:  %s\n", item.Asset.BrowserDownloadURL)
	}
	fmt.Fprintln(out, "  temporary replacement files: staged beside each target and removed if the step fails")
	for _, item := range plan.Items {
		fmt.Fprintf(out, "  binary to replace: %s\n", item.Destination.Path)
	}
}

func confirmUpgradePlan(in io.Reader, out io.Writer, opts upgradeOptions, deps upgradeDeps) (bool, error) {
	if opts.Yes {
		fmt.Fprintln(out, "confirmation: --yes set; continuing without prompt")
		return true, nil
	}
	if in == nil || !deps.StdinIsTerminal(in) {
		return false, nonInteractiveConfirmationError()
	}
	fmt.Fprint(out, "continue? [y/N] ")
	response, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, confirmationReadError(err)
	}
	response = strings.ToLower(strings.TrimSpace(response))
	if response != "y" && response != "yes" {
		fmt.Fprintln(out, "upgrade cancelled: no files were changed")
		return false, nil
	}
	return true, nil
}

// downloadAndVerifyUpgradeAssets fetches checksums.txt, then downloads and
// SHA-256-verifies every planned asset before the first rename. A failure at any
// point leaves every target byte-identical to its prior content.
func downloadAndVerifyUpgradeAssets(ctx context.Context, deps upgradeDeps, plan upgradePlan) ([]verifiedBinary, error) {
	checksums, err := downloadUpgradeBytes(ctx, deps.HTTPClient, plan.Checksums.BrowserDownloadURL, upgradeChecksumsLimit)
	if err != nil {
		return nil, checksumsDownloadError(err)
	}
	verified := make([]verifiedBinary, 0, len(plan.Items))
	for _, item := range plan.Items {
		expected, err := checksumForUpgradeAsset(checksums, item.Asset.Name)
		if err != nil {
			return nil, err
		}
		assetBytes, err := downloadUpgradeBytes(ctx, deps.HTTPClient, item.Asset.BrowserDownloadURL, upgradeBinaryLimit)
		if err != nil {
			return nil, binaryDownloadError(item.Asset.Name, err)
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(assetBytes))
		if !strings.EqualFold(actual, expected) {
			return nil, checksumMismatchError(item.Asset.Name, expected, actual)
		}
		mode := upgradeInstallMode(deps, item.Destination.Path)
		verified = append(verified, verifiedBinary{Destination: item.Destination, Bytes: assetBytes, Mode: mode})
	}
	return verified, nil
}

func checksumForUpgradeAsset(checksums []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || filepath.Base(fields[len(fields)-1]) != assetName {
			continue
		}
		hash := strings.TrimSpace(fields[0])
		if len(hash) != sha256.Size*2 {
			return "", invalidChecksumLengthError(assetName, hash)
		}
		return strings.ToLower(hash), nil
	}
	return "", missingChecksumEntryError(assetName)
}

// replaceUpgradeBinaries renames the verified binaries over their destinations
// in order (main first). A mid-sequence failure names exactly which binaries
// were replaced; the same-tag path repairs the partial state on the next run.
func replaceUpgradeBinaries(out io.Writer, deps upgradeDeps, verified []verifiedBinary) error {
	replaced := make([]string, 0, len(verified))
	for _, item := range verified {
		if err := deps.InstallBinary(item.Destination.Path, item.Bytes, item.Mode); err != nil {
			return replaceFailureError(item.Destination, replaced, err)
		}
		replaced = append(replaced, item.Destination.Name)
		fmt.Fprintf(out, "installed %s at %s\n", item.Destination.Name, item.Destination.Path)
	}
	fmt.Fprintln(out, "Run `pasture --version` to verify the new binary.")
	if containsUpgradeName(replaced, upgradeDaemonBinaryName) {
		fmt.Fprintln(out, "restart pastured when convenient so the daemon matches the new CLI.")
	}
	return nil
}

func containsUpgradeName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// installPastureBinary is the production replacement primitive: a temp file
// beside the target, write, chmod, then a same-directory rename. Renaming over
// a running executable is atomic on unix and leaves the running process on its
// own inode.
func installPastureBinary(path string, binary []byte, mode fs.FileMode) error {
	if mode.Perm() == 0 {
		mode = 0o755
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".pasture-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temporary binary beside %s: %w", path, err)
	}
	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(binary); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary binary %s: %w", tempPath, err)
	}
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod temporary binary %s: %w", tempPath, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary binary %s: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("rename verified binary over %s: %w", path, err)
	}
	keepTemp = true
	return nil
}

// upgradeInstallMode preserves an existing executable mode, else 0755. Release
// assets are raw binaries and carry no archive mode.
func upgradeInstallMode(deps upgradeDeps, path string) fs.FileMode {
	if info, err := deps.Stat(path); err == nil {
		mode := info.Mode().Perm()
		if mode&0o111 != 0 {
			return mode
		}
	}
	return 0o755
}

func defaultUpgradeCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, upgradeProbeTimeout)
	defer cancel()
	return exec.CommandContext(probeCtx, name, args...).CombinedOutput()
}

func upgradeInputIsTerminal(in io.Reader) bool {
	file, ok := in.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func parsePacmanOwner(output string) string {
	const marker = " is owned by "
	idx := strings.Index(output, marker)
	if idx < 0 {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(output[idx+len(marker):]))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func normalizeUpgradeTag(version string) string {
	version = strings.TrimSpace(version)
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func cleanUpgradeRequestedVersion(version string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "" || version == "v" {
		return "", emptyVersionError()
	}
	for _, r := range version {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			continue
		}
		return "", unsafeVersionError(version, r)
	}
	return version, nil
}

func isDevelVersion(version string) bool {
	return strings.TrimSpace(version) == "devel"
}

// destinationPhrase names the planned binaries for user-facing messages.
func destinationPhrase(destinations []upgradeDestination) string {
	names := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		names = append(names, destination.Name)
	}
	switch len(names) {
	case 0:
		return "pasture"
	case 1:
		return names[0] + " is"
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " are"
	}
}

func printUpgradeAlreadyAt(out io.Writer, destinations []upgradeDestination, tag string) {
	fmt.Fprintf(out, "%s already at %s; no files were changed.\n", destinationPhrase(destinations), tag)
}

// --- version parsing and comparison ---

type upgradeVersionOrder int

const (
	upgradeVersionBefore upgradeVersionOrder = -1
	upgradeVersionSame   upgradeVersionOrder = 0
	upgradeVersionAfter  upgradeVersionOrder = 1
)

func (o upgradeVersionOrder) String() string {
	switch o {
	case upgradeVersionBefore:
		return "before"
	case upgradeVersionSame:
		return "same"
	case upgradeVersionAfter:
		return "after"
	default:
		return "unknown"
	}
}

type parsedUpgradeVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
}

func compareUpgradeVersions(current, target string) (upgradeVersionOrder, error) {
	currentVersion, err := parseUpgradeVersion(current, "current")
	if err != nil {
		return upgradeVersionSame, err
	}
	targetVersion, err := parseUpgradeVersion(target, "target")
	if err != nil {
		return upgradeVersionSame, err
	}
	return currentVersion.compare(targetVersion), nil
}

func parseUpgradeVersion(version, label string) (parsedUpgradeVersion, error) {
	original := strings.TrimSpace(version)
	if original == "" {
		return parsedUpgradeVersion{}, unorderedVersionError(label, version, "version is empty")
	}
	withoutPrefix := strings.TrimPrefix(original, "v")
	withoutBuild, _, _ := strings.Cut(withoutPrefix, "+")
	core, prerelease, hasPrerelease := strings.Cut(withoutBuild, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return parsedUpgradeVersion{}, unorderedVersionError(label, version, fmt.Sprintf("version core %q must be MAJOR.MINOR.PATCH", core))
	}
	major, err := parseVersionComponent(parts[0], "MAJOR")
	if err != nil {
		return parsedUpgradeVersion{}, unorderedVersionError(label, version, err.Error())
	}
	minor, err := parseVersionComponent(parts[1], "MINOR")
	if err != nil {
		return parsedUpgradeVersion{}, unorderedVersionError(label, version, err.Error())
	}
	patch, err := parseVersionComponent(parts[2], "PATCH")
	if err != nil {
		return parsedUpgradeVersion{}, unorderedVersionError(label, version, err.Error())
	}
	out := parsedUpgradeVersion{major: major, minor: minor, patch: patch}
	if !hasPrerelease {
		return out, nil
	}
	if prerelease == "" {
		return parsedUpgradeVersion{}, unorderedVersionError(label, version, "prerelease is empty")
	}
	identifiers := strings.Split(prerelease, ".")
	for _, identifier := range identifiers {
		if identifier == "" {
			return parsedUpgradeVersion{}, unorderedVersionError(label, version, "prerelease identifier is empty")
		}
		for _, r := range identifier {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
				continue
			}
			return parsedUpgradeVersion{}, unorderedVersionError(label, version, fmt.Sprintf("prerelease identifier %q contains %q", identifier, r))
		}
	}
	out.prerelease = identifiers
	return out, nil
}

func parseVersionComponent(value, label string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("%s component is empty", label)
	}
	if len(value) > 1 && strings.HasPrefix(value, "0") {
		return 0, fmt.Errorf("%s component %q has a leading zero", label, value)
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%s component %q is not numeric", label, value)
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s component %q is not numeric: %w", label, value, err)
	}
	return parsed, nil
}

func (v parsedUpgradeVersion) compare(other parsedUpgradeVersion) upgradeVersionOrder {
	if c := compareVersionInts(v.major, other.major); c != upgradeVersionSame {
		return c
	}
	if c := compareVersionInts(v.minor, other.minor); c != upgradeVersionSame {
		return c
	}
	if c := compareVersionInts(v.patch, other.patch); c != upgradeVersionSame {
		return c
	}
	// A release sorts after the same core with a prerelease.
	if len(v.prerelease) == 0 && len(other.prerelease) == 0 {
		return upgradeVersionSame
	}
	if len(v.prerelease) == 0 {
		return upgradeVersionAfter
	}
	if len(other.prerelease) == 0 {
		return upgradeVersionBefore
	}
	for i := 0; i < len(v.prerelease) && i < len(other.prerelease); i++ {
		if c := comparePrereleaseIdentifiers(v.prerelease[i], other.prerelease[i]); c != upgradeVersionSame {
			return c
		}
	}
	// A shorter identifier set sorts first when it is a prefix.
	if len(v.prerelease) < len(other.prerelease) {
		return upgradeVersionBefore
	}
	if len(v.prerelease) > len(other.prerelease) {
		return upgradeVersionAfter
	}
	return upgradeVersionSame
}

func comparePrereleaseIdentifiers(a, b string) upgradeVersionOrder {
	aNum, aNumeric := parseNumericIdentifier(a)
	bNum, bNumeric := parseNumericIdentifier(b)
	switch {
	case aNumeric && bNumeric:
		return compareVersionInts(aNum, bNum)
	case aNumeric:
		return upgradeVersionBefore
	case bNumeric:
		return upgradeVersionAfter
	default:
		return upgradeVersionOrder(strings.Compare(a, b))
	}
}

func parseNumericIdentifier(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func compareVersionInts(a, b int) upgradeVersionOrder {
	switch {
	case a < b:
		return upgradeVersionBefore
	case a > b:
		return upgradeVersionAfter
	default:
		return upgradeVersionSame
	}
}

// --- typed failures ---

func allowDowngradeWithoutVersionError() *upgradeError {
	return newUpgradeError(
		1,
		"the downgrade override needs an exact target version",
		"--allow-downgrade was not paired with --version <tag>",
		"pasture upgrade --allow-downgrade",
		"before checking the installation channel or selecting a release",
		"pasture refused to continue and no files were changed",
		"rerun with --version <tag> --allow-downgrade if you intentionally need to roll back to an older release",
	)
}

func emptyVersionError() *upgradeError {
	return newUpgradeError(
		1,
		"the requested release version is empty",
		"--version must name a release such as 0.0.16",
		"pasture upgrade --version",
		"before using the version in release URLs",
		"pasture cannot select a target release",
		"pass --version 0.0.16 or omit --version to use the latest stable release",
	)
}

func unsafeVersionError(version string, offending rune) *upgradeError {
	return newUpgradeError(
		1,
		"the requested release version contains an unsafe character",
		fmt.Sprintf("--version %q contains %q; only letters, numbers, dots, hyphens, and underscores are allowed", version, offending),
		"pasture upgrade --version",
		"before using the version in release URLs",
		"pasture will not request a release path with shell metacharacters",
		"pass a release version such as 0.0.16 or v0.0.16",
	)
}

func executableResolutionError(cause error) *upgradeError {
	return newUpgradeError(
		1,
		"the current pasture executable path could not be resolved",
		"the running binary path is unknown",
		"pasture upgrade",
		"before checking the installation channel",
		"pasture cannot safely replace or advise for an unknown binary path",
		"run `pasture --version` from the binary you want to upgrade, then retry from that same shell",
	).withCause(cause)
}

func develRefusalError() *upgradeError {
	return newUpgradeError(
		1,
		"this pasture build reports \"devel\" and cannot upgrade itself",
		"the running binary was built without a release version stamp",
		"pasture upgrade",
		"before querying GitHub releases",
		"pasture cannot compare \"devel\" against a release tag, so no files were changed",
		"install a stamped release first, then rerun:\n  curl -fsSL "+upgradeInstallerURL+" | bash\nor download a release from "+upgradeReleasePageURL+"\nwith make: make build VERSION=vX.Y.Z",
	)
}

func releaseQueryError(tag string, cause error) *upgradeError {
	return newUpgradeError(
		2,
		"the target pasture release could not be resolved",
		fmt.Sprintf("the GitHub release %s could not be read", tag),
		"pasture upgrade",
		"while reading the GitHub release",
		"pasture cannot choose an asset or checksum for an unknown release, so no files were changed",
		"check network access to the GitHub API and that the release exists, then retry; open "+upgradeReleasePageURL,
	).withCause(cause)
}

func downgradeRefusalError(current, target string) *upgradeError {
	return newUpgradeError(
		1,
		fmt.Sprintf("the current pasture version %s is newer than the target %s", current, target),
		"the target release sorts before the running version",
		"pasture upgrade",
		"before downloading any asset",
		"pasture refused to continue and no files were changed",
		fmt.Sprintf("to roll back intentionally, rerun with --version %s --allow-downgrade", target),
	)
}

func unsupportedPlatformError(goos, goarch string) *upgradeError {
	return newUpgradeError(
		1,
		"this platform is not supported by pasture release assets",
		fmt.Sprintf("%s/%s is not one of linux or darwin on amd64 or arm64", goos, goarch),
		"pasture upgrade",
		"while choosing the release asset",
		"pasture cannot select a published asset for this host",
		"open "+upgradeReleasePageURL+" and follow the install guide for this platform",
	)
}

func missingChecksumsAssetError(tag string) *upgradeError {
	return newUpgradeError(
		1,
		"the target release does not contain checksums.txt",
		fmt.Sprintf("release %s has no %s asset", tag, upgradeChecksumsName),
		"pasture upgrade",
		"before downloading any binary",
		"pasture will not install a binary it cannot verify, so no files were changed",
		"open "+upgradeReleasePageURL+" and choose a release that includes "+upgradeChecksumsName,
	)
}

func missingAssetError(tag, name string) *upgradeError {
	return newUpgradeError(
		1,
		"the target release does not contain a binary for this platform",
		fmt.Sprintf("release %s is missing asset %s", tag, name),
		"pasture upgrade",
		"after selecting the release artifact",
		"pasture cannot install a binary whose published checksum and asset are absent",
		"open "+upgradeReleasePageURL+" and choose a release that includes "+name,
	)
}

func nonInteractiveConfirmationError() *upgradeError {
	return newUpgradeError(
		1,
		"the replacement plan needs interactive confirmation",
		"standard input is not an interactive terminal and --yes was not set",
		"pasture upgrade",
		"after printing the replacement plan and before downloading files",
		"pasture refused to replace the current binary and no files were changed",
		"rerun from an interactive terminal and answer yes, or rerun with --yes only if automation has already reviewed the printed plan",
	)
}

func confirmationReadError(cause error) *upgradeError {
	return newUpgradeError(
		1,
		"the upgrade confirmation response could not be read",
		"the confirmation prompt could not be answered",
		"pasture upgrade",
		"after printing the replacement plan and before downloading files",
		"pasture refused to replace the current binary and no files were changed",
		"rerun from an interactive terminal and answer yes, or rerun with --yes",
	).withCause(cause)
}

func checksumsDownloadError(cause error) *upgradeError {
	return newUpgradeError(
		2,
		"checksums.txt could not be downloaded",
		"the release checksums could not be fetched",
		"pasture upgrade",
		"before downloading the binaries",
		"pasture cannot verify the release artifact, so no files were changed",
		"check network access to GitHub releases, then retry",
	).withCause(cause)
}

func binaryDownloadError(name string, cause error) *upgradeError {
	return newUpgradeError(
		2,
		fmt.Sprintf("the %s binary could not be downloaded", name),
		"the release asset could not be fetched",
		"pasture upgrade",
		"after checksums.txt was downloaded and before any replacement",
		"the existing binaries were left untouched",
		"check network access to GitHub releases, then retry",
	).withCause(cause)
}

func checksumMismatchError(name, expected, actual string) *upgradeError {
	return newUpgradeError(
		1,
		"the downloaded binary checksum did not match checksums.txt",
		fmt.Sprintf("%s expected %s but got %s", name, expected, actual),
		"pasture upgrade",
		"before replacing any local binary",
		"the existing binaries were left untouched because the release artifact could be corrupt or tampered with",
		"delete any local partial downloads, check the release page, and retry when GitHub serves matching bytes",
	)
}

func invalidChecksumLengthError(name, hash string) *upgradeError {
	return newUpgradeError(
		1,
		"checksums.txt contains an invalid checksum length",
		fmt.Sprintf("%s has checksum %q", name, hash),
		"pasture upgrade",
		"while verifying release metadata",
		"pasture will not install a binary without a valid SHA-256 checksum, so no files were changed",
		"check the release checksums.txt and retry after the release is repaired",
	)
}

func missingChecksumEntryError(name string) *upgradeError {
	return newUpgradeError(
		1,
		"checksums.txt does not name the selected binary",
		fmt.Sprintf("no checksum entry matched %s", name),
		"pasture upgrade",
		"while verifying release metadata",
		"pasture will not install a binary it cannot verify, so no files were changed",
		"check the release checksums.txt and retry after the release is repaired",
	)
}

func replaceFailureError(destination upgradeDestination, replaced []string, cause error) *upgradeError {
	means := "no binary was replaced"
	if len(replaced) > 0 {
		means = fmt.Sprintf("%s was replaced, but %s was not; rerunning at the same release repairs the partial state", strings.Join(replaced, " and "), destination.Name)
	}
	return newUpgradeError(
		1,
		fmt.Sprintf("the verified %s binary could not replace the current executable", destination.Name),
		"the replacement file could not be written or renamed into place",
		"pasture upgrade",
		"after the release asset passed checksum verification",
		means,
		"if this is a raw install in a user directory, check that the directory is writable; reinstall through the installer to ~/.local/bin as the last resort, warning that root-owned files in a user bin directory are a hazard",
	).withCause(cause)
}

func unorderedVersionError(label, version, why string) *upgradeError {
	return newUpgradeError(
		1,
		fmt.Sprintf("the %s pasture version could not be ordered", label),
		fmt.Sprintf("%s version %q is not a supported release: %s", label, version, why),
		"pasture upgrade",
		"before comparing the current and target versions",
		"pasture cannot prove whether the target is an upgrade or a downgrade, so no files were changed",
		"use a pasture binary stamped with a release version, and pass a release tag such as v0.0.16",
	)
}

func init() {
	rootCmd.AddCommand(buildUpgradeCommand(defaultUpgradeDeps()))
}
