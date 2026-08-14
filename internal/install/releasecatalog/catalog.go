// Package releasecatalog selects and completely verifies immutable aggregate GitHub Releases.
package releasecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
)

// PrereleasePolicy controls whether release candidates may be selected.
type PrereleasePolicy uint8

const (
	FinalsOnly PrereleasePolicy = iota + 1
	IncludePrereleases
)

// Asset is one validated immutable GitHub Release asset boundary.
type Asset struct {
	name, downloadURL string
	size              int64
}

func (a Asset) Name() string        { return a.name }
func (a Asset) DownloadURL() string { return a.downloadURL }
func (a Asset) Size() int64         { return a.size }

// NewAsset validates an immutable alternate-source DTO through the production asset trust rules.
func NewAsset(name, downloadURL string, size int64) (Asset, error) {
	u, err := url.Parse(downloadURL)
	if err != nil || name == "" || strings.Contains(name, "/") || strings.Contains(name, "pasture-stable") || size < 0 || !validAssetURL(u) {
		return Asset{}, invalid("asset construction", name, "asset metadata is malformed or outside approved GitHub hosts", "alternate source cannot supply trusted bytes", "use a unique non-alias name, nonnegative size, and approved HTTPS GitHub URL", fs.ErrInvalid)
	}
	return Asset{name: name, downloadURL: downloadURL, size: size}, nil
}

// Release is one validated, non-draft GitHub Release.
type Release struct {
	version    artifact.Version
	prerelease bool
	assets     map[string]Asset
}

func (r Release) Version() artifact.Version { return r.version }
func (r Release) Prerelease() bool          { return r.prerelease }

// NewRelease validates a constructible immutable alternate-source release DTO.
func NewRelease(version artifact.Version, prerelease bool, assets []Asset) (Release, error) {
	if version.String() == "" || version.IsPrerelease() != prerelease {
		return Release{}, invalid("release construction", "version", "version and prerelease flag disagree or are zero", "release channel is ambiguous", "construct Version with ParseVersion and match its prerelease state", fs.ErrInvalid)
	}
	owned := map[string]Asset{}
	for _, asset := range assets {
		if asset.name == "" || owned[asset.name].name != "" {
			return Release{}, invalid("release construction", "assets", "asset is invalid or duplicated", "release inventory is ambiguous", "construct unique assets with NewAsset", fs.ErrInvalid)
		}
		owned[asset.name] = asset
	}
	return Release{version: version, prerelease: prerelease, assets: owned}, nil
}

// Source injects the GitHub catalog and asset network boundary.
type Source interface {
	ListReleases(context.Context) ([]Release, error)
	OpenURL(context.Context, string) (io.ReadCloser, error)
}

// Selection fixes trusted constraints before any release bytes are consumed.
type Selection struct {
	Installer                 artifact.Version
	PastureRevision           artifact.Revision
	AuraRevision              artifact.Revision
	Policy                    PrereleasePolicy
	MaxAssetBytes             int64
	MaxVerificationCandidates int
	MaxVerificationBytes      int64
}

// Mutator is called only with a completely verified immutable aggregate.
type Mutator interface {
	ApplyVerifiedAggregate(context.Context, artifact.VerifiedAggregate) error
}

// Catalog owns release selection, complete verification, then optional mutation.
type DiscoveryLimits struct {
	MaxCandidates int
	MaxBytes      int64
}
type Catalog struct {
	source    Source
	discovery DiscoveryLimits
}

// Candidate is one compatible checksum-verified manifest choice bound to its catalog.
type Candidate struct {
	catalog        *Catalog
	release        Release
	manifest       artifact.AggregateManifest
	manifestDigest artifact.Digest
	installer      artifact.Version
}

func (c Candidate) Version() artifact.Version          { return c.manifest.Version() }
func (c Candidate) Channel() artifact.ReleaseChannel   { return c.manifest.Channel() }
func (c Candidate) InstallerMin() artifact.Version     { return c.manifest.InstallerMin() }
func (c Candidate) InstallerMax() artifact.Version     { return c.manifest.InstallerMax() }
func (c Candidate) PastureRevision() artifact.Revision { return c.manifest.PastureRevision() }
func (c Candidate) AuraRevision() artifact.Revision    { return c.manifest.AuraRevision() }
func (c Candidate) ManifestDigest() artifact.Digest    { return c.manifestDigest }

func New(source Source) (*Catalog, error) {
	return NewWithDiscoveryLimits(source, DiscoveryLimits{})
}

// NewWithDiscoveryLimits configures bounded manifest discovery for listing and exact selection.
func NewWithDiscoveryLimits(source Source, limits DiscoveryLimits) (*Catalog, error) {
	if source == nil {
		return nil, invalid("catalog construction", "source", "the GitHub release source is nil", "no release can be selected or verified", "inject a configured GitHub source", fs.ErrInvalid)
	}
	if limits.MaxCandidates == 0 {
		limits.MaxCandidates = 64
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = 64 << 20
	}
	if limits.MaxCandidates < 1 || limits.MaxBytes < 1 {
		return nil, invalid("catalog construction", "discovery limits", "candidate and byte limits must be positive", "candidate discovery cannot be bounded", "use zero defaults or positive reviewed limits", fs.ErrInvalid)
	}
	return &Catalog{source: source, discovery: limits}, nil
}

// Resolve returns the newest compatible fully verified final, or prerelease when explicitly opted in.
func (c *Catalog) Resolve(ctx context.Context, selection Selection) (artifact.VerifiedAggregate, error) {
	if selection.Policy != FinalsOnly && selection.Policy != IncludePrereleases {
		return artifact.VerifiedAggregate{}, invalid("release selection", "prerelease policy", fmt.Sprintf("policy value %d is unsupported", selection.Policy), "the catalog cannot decide whether prereleases are authorized", "use FinalsOnly or explicitly use IncludePrereleases", fs.ErrInvalid)
	}
	if _, err := artifact.ParseVersion(selection.Installer.String()); err != nil {
		return artifact.VerifiedAggregate{}, invalid("release selection", "installer version", "the installer version was not constructed", "compatibility cannot be evaluated", "construct it with artifact.ParseVersion", err)
	}
	if _, err := artifact.ParseRevision(selection.PastureRevision.String()); err != nil {
		return artifact.VerifiedAggregate{}, invalid("release selection", "Pasture revision", "the required revision was not constructed", "source identity cannot be verified", "construct it with artifact.ParseRevision", err)
	}
	if _, err := artifact.ParseRevision(selection.AuraRevision.String()); err != nil {
		return artifact.VerifiedAggregate{}, invalid("release selection", "Aura revision", "the required revision was not constructed", "source identity cannot be verified", "construct it with artifact.ParseRevision", err)
	}
	candidates, err := c.ListCompatible(ctx, selection.Installer, selection.Policy)
	if err != nil {
		return artifact.VerifiedAggregate{}, err
	}
	var failures []string
	maxCandidates := selection.MaxVerificationCandidates
	if maxCandidates == 0 {
		maxCandidates = 16
	}
	maxBytes := selection.MaxVerificationBytes
	if maxBytes == 0 {
		maxBytes = 512 << 20
	}
	if maxCandidates < 1 || maxBytes < 1 {
		return artifact.VerifiedAggregate{}, invalid("release selection", "verification limits", "candidate and byte limits must be positive", "candidate verification cannot be bounded", "use zero defaults or positive reviewed limits", fs.ErrInvalid)
	}
	var attempted int
	var transferred int64
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return artifact.VerifiedAggregate{}, err
		}
		if selection.PastureRevision.String() != "" && selection.PastureRevision != candidate.PastureRevision() {
			continue
		}
		if selection.AuraRevision.String() != "" && selection.AuraRevision != candidate.AuraRevision() {
			continue
		}
		attempted++
		if attempted > maxCandidates {
			return artifact.VerifiedAggregate{}, invalid("release selection", "candidate limit", fmt.Sprintf("verification candidate limit %d exhausted", maxCandidates), "the catalog was not silently truncated and no mutation began", "choose an exact listed candidate or raise the reviewed limit", fs.ErrInvalid)
		}
		candidateBytes, ok := candidate.release.totalAssetBytes()
		if !ok || candidateBytes > maxBytes-transferred {
			return artifact.VerifiedAggregate{}, invalid("release selection", "byte limit", fmt.Sprintf("verification byte limit %d exhausted", maxBytes), "cross-candidate downloads stopped before mutation", "choose an exact candidate or raise the reviewed limit", fs.ErrInvalid)
		}
		transferred += candidateBytes
		verified, verifyErr := c.ResolveCandidate(ctx, candidate, selection.MaxAssetBytes)
		if verifyErr == nil {
			return verified, nil
		}
		if len(failures) < 8 {
			failures = append(failures, fmt.Sprintf("%s: %v", candidate.Version(), verifyErr))
		}
	}
	reason := "no final release satisfied compatibility and verification"
	if selection.Policy == IncludePrereleases {
		reason = "no final or opted-in prerelease satisfied compatibility and verification"
	}
	if len(failures) > 0 {
		reason += "; candidates failed: " + strings.Join(failures, " | ")
	}
	return artifact.VerifiedAggregate{}, invalid("release selection", "catalog", reason, "no aggregate was returned and mutation must not begin", "publish one complete compatible immutable aggregate, or explicitly opt into a compatible prerelease", fs.ErrNotExist)
}

// ListCompatible returns typed checksum-verified choices in descending SemVer order.
func (c *Catalog) ListCompatible(ctx context.Context, installer artifact.Version, policy PrereleasePolicy) ([]Candidate, error) {
	if policy != FinalsOnly && policy != IncludePrereleases {
		return nil, invalid("candidate listing", "policy", "unsupported prerelease policy", "candidate channel cannot be filtered", "use FinalsOnly or IncludePrereleases", fs.ErrInvalid)
	}
	releases, err := c.source.ListReleases(ctx)
	if err != nil {
		return nil, invalid("release listing", "GitHub Releases", fmt.Sprintf("catalog could not be loaded: %v", err), "no candidates are available", "repair GitHub access and retry", err)
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].version.Compare(releases[j].version) > 0 })
	result := make([]Candidate, 0, len(releases))
	discovered := 0
	var discoveryBytes int64
	for _, release := range releases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if release.prerelease && policy != IncludePrereleases || release.prerelease != release.version.IsPrerelease() {
			continue
		}
		discovered++
		if discovered > c.discovery.MaxCandidates {
			return nil, invalid("candidate listing", "candidate limit", fmt.Sprintf("manifest discovery limit %d exhausted", c.discovery.MaxCandidates), "candidate listing is explicitly incomplete", "construct the Catalog with a larger reviewed DiscoveryLimits value", fs.ErrInvalid)
		}
		manifestCost, ok := release.manifestAssetBytes()
		if !ok || manifestCost > c.discovery.MaxBytes-discoveryBytes {
			return nil, invalid("candidate listing", "byte limit", fmt.Sprintf("manifest discovery byte limit %d exhausted", c.discovery.MaxBytes), "candidate listing is explicitly incomplete", "construct the Catalog with a larger reviewed DiscoveryLimits value", fs.ErrInvalid)
		}
		discoveryBytes += manifestCost
		source := releaseAssetSource{source: c.source, assets: release.assets}
		manifestBytes, e := readCatalogAsset(ctx, source, artifact.AggregateManifestAsset, 4<<20)
		if e != nil {
			continue
		}
		checksumBytes, e := readCatalogAsset(ctx, source, artifact.AggregateChecksumAsset, 4096)
		if e != nil {
			continue
		}
		manifest, e := artifact.VerifyAggregateManifest(manifestBytes, checksumBytes)
		if e != nil || manifest.Version() != release.version || !manifest.Compatible(installer) {
			continue
		}
		sum := sha256.Sum256(manifestBytes)
		digest, _ := artifact.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
		result = append(result, Candidate{catalog: c, release: release, manifest: manifest, manifestDigest: digest, installer: installer})
	}
	return result, nil
}

func (r Release) totalAssetBytes() (int64, bool) {
	var total int64
	for _, asset := range r.assets {
		if asset.size < 0 || asset.size > int64(^uint64(0)>>1)-total {
			return 0, false
		}
		total += asset.size
	}
	return total, true
}
func (r Release) manifestAssetBytes() (int64, bool) {
	manifest, ok := r.assets[artifact.AggregateManifestAsset]
	if !ok {
		return 0, false
	}
	checksum, ok := r.assets[artifact.AggregateChecksumAsset]
	if !ok {
		return 0, false
	}
	if manifest.size < 0 || checksum.size < 0 || manifest.size > int64(^uint64(0)>>1)-checksum.size {
		return 0, false
	}
	return manifest.size + checksum.size, true
}

// ResolveCandidate verifies exactly the selected candidate and never falls back.
func (c *Catalog) ResolveCandidate(ctx context.Context, candidate Candidate, maxAssetBytes int64) (artifact.VerifiedAggregate, error) {
	if candidate.catalog != c || candidate.Version().String() == "" {
		return artifact.VerifiedAggregate{}, invalid("candidate resolution", "candidate", "candidate is zero or belongs to another catalog", "exact selection cannot be trusted", "select a value returned by this Catalog.ListCompatible", fs.ErrInvalid)
	}
	source := releaseAssetSource{source: c.source, assets: candidate.release.assets}
	return artifact.VerifyAggregate(ctx, source, artifact.AggregateRequirements{Version: candidate.Version(), Installer: candidate.installer, PastureRevision: candidate.PastureRevision(), AuraRevision: candidate.AuraRevision(), MaxAssetBytes: maxAssetBytes, ExpectedManifestDigest: candidate.manifestDigest})
}

// ResolveVersion lists then resolves exactly version; absence never selects another candidate.
func (c *Catalog) ResolveVersion(ctx context.Context, installer artifact.Version, policy PrereleasePolicy, version artifact.Version, maxAssetBytes int64) (artifact.VerifiedAggregate, error) {
	candidates, err := c.ListCompatible(ctx, installer, policy)
	if err != nil {
		return artifact.VerifiedAggregate{}, err
	}
	for _, candidate := range candidates {
		if candidate.Version() == version {
			return c.ResolveCandidate(ctx, candidate, maxAssetBytes)
		}
	}
	return artifact.VerifiedAggregate{}, invalid("candidate resolution", version.String(), "the exact selected version is absent or incompatible", "no fallback release was selected and mutation must not begin", "refresh candidates or choose an exact listed version", fs.ErrNotExist)
}

// ResolveCandidateAndApply preserves exact selection and verify-before-mutation.
func (c *Catalog) ResolveCandidateAndApply(ctx context.Context, candidate Candidate, maxAssetBytes int64, mutator Mutator) (artifact.VerifiedAggregate, error) {
	if mutator == nil {
		return artifact.VerifiedAggregate{}, invalid("mutation preparation", "mutator", "mutator is nil", "the selected release cannot be installed", "inject the installer mutation boundary", fs.ErrInvalid)
	}
	verified, err := c.ResolveCandidate(ctx, candidate, maxAssetBytes)
	if err != nil {
		return artifact.VerifiedAggregate{}, err
	}
	if err = mutator.ApplyVerifiedAggregate(ctx, verified); err != nil {
		return artifact.VerifiedAggregate{}, invalid("aggregate mutation", candidate.Version().String(), fmt.Sprintf("mutation failed after complete verification: %v", err), "the caller must inspect factual mutation state", "repair the mutation failure and retry the same exact candidate", err)
	}
	return verified, nil
}

// ResolveVersionAndApply never substitutes another version when the exact choice fails.
func (c *Catalog) ResolveVersionAndApply(ctx context.Context, installer artifact.Version, policy PrereleasePolicy, version artifact.Version, maxAssetBytes int64, mutator Mutator) (artifact.VerifiedAggregate, error) {
	candidates, err := c.ListCompatible(ctx, installer, policy)
	if err != nil {
		return artifact.VerifiedAggregate{}, err
	}
	for _, candidate := range candidates {
		if candidate.Version() == version {
			return c.ResolveCandidateAndApply(ctx, candidate, maxAssetBytes, mutator)
		}
	}
	return artifact.VerifiedAggregate{}, invalid("candidate resolution", version.String(), "the exact selected version is absent or incompatible", "no fallback release was selected and mutation did not begin", "refresh candidates or choose an exact listed version", fs.ErrNotExist)
}

func readCatalogAsset(ctx context.Context, source releaseAssetSource, name string, limit int64) ([]byte, error) {
	reader, err := source.OpenAsset(ctx, name)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, invalid("candidate manifest read", name, fmt.Sprintf("asset read failed: %v", readErr), "candidate cannot be listed safely", "repair the asset transport and retry", readErr)
	}
	if closeErr != nil {
		return nil, invalid("candidate manifest close", name, fmt.Sprintf("asset close failed: %v", closeErr), "candidate resource ownership is incomplete", "repair the asset transport and retry", closeErr)
	}
	if len(content) > int(limit) {
		return nil, invalid("candidate manifest read", name, fmt.Sprintf("asset exceeds %d-byte limit", limit), "oversized candidate metadata was rejected", "publish a bounded manifest or sidecar", fs.ErrInvalid)
	}
	return content, nil
}

// ResolveAndApply preserves the verify-before-mutate production ordering.
func (c *Catalog) ResolveAndApply(ctx context.Context, selection Selection, mutator Mutator) (artifact.VerifiedAggregate, error) {
	if mutator == nil {
		return artifact.VerifiedAggregate{}, invalid("mutation preparation", "mutator", "the aggregate mutator is nil", "the verified release cannot be installed", "inject the installer mutation boundary", fs.ErrInvalid)
	}
	verified, err := c.Resolve(ctx, selection)
	if err != nil {
		return artifact.VerifiedAggregate{}, err
	}
	if err := mutator.ApplyVerifiedAggregate(ctx, verified); err != nil {
		return artifact.VerifiedAggregate{}, invalid("aggregate mutation", verified.Manifest().Version().String(), fmt.Sprintf("mutation failed after complete verification: %v", err), "the caller must inspect the mutator's factual result before retrying", "repair the reported mutation failure and retry the same immutable version", err)
	}
	return verified, nil
}

type releaseAssetSource struct {
	source Source
	assets map[string]Asset
}

func (s releaseAssetSource) OpenAsset(ctx context.Context, name string) (io.ReadCloser, error) {
	asset, ok := s.assets[name]
	if !ok {
		return nil, fmt.Errorf("release asset %q is missing", name)
	}
	reader, err := s.source.OpenURL(ctx, asset.downloadURL)
	if err != nil {
		return nil, err
	}
	return &exactSizeReadCloser{ReadCloser: reader, remaining: asset.size, name: name}, nil
}

type exactSizeReadCloser struct {
	io.ReadCloser
	remaining int64
	name      string
}

func (r *exactSizeReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		return n, fmt.Errorf("asset %q contains more bytes than GitHub declared", r.name)
	}
	if err == io.EOF && r.remaining != 0 {
		return n, fmt.Errorf("asset %q ended with %d declared bytes missing", r.name, r.remaining)
	}
	return n, err
}

// Error provides stable actionable release-catalog failure fields.
type Error struct {
	Stage, Location, Reason, Impact, Fix string
	Cause                                error
}

func (e *Error) Error() string {
	return fmt.Sprintf("release catalog: %s failed at %s because %s; impact: %s; fix: %s", e.Stage, e.Location, e.Reason, e.Impact, e.Fix)
}
func (e *Error) Unwrap() error { return e.Cause }
func invalid(stage, location, reason, impact, fix string, cause error) error {
	return &Error{stage, location, reason, impact, fix, cause}
}
