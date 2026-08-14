// Package releasecatalog selects and completely verifies immutable aggregate GitHub Releases.
package releasecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
)

var errCandidateMetadata = errors.New("candidate metadata is absent or malformed")

func isCandidateMetadataDefect(err error) bool { return errors.Is(err, errCandidateMetadata) }

// PrereleasePolicy controls whether release candidates may be selected.
type PrereleasePolicy uint8

const (
	FinalsOnly PrereleasePolicy = iota + 1
	IncludePrereleases
)

type asset struct {
	name, downloadURL string
	size              int64
	identity          assetIdentity
}

type release struct {
	version    artifact.Version
	prerelease bool
	assets     map[string]asset
}

type catalogSource interface {
	listReleases(context.Context) ([]release, error)
	openAsset(context.Context, asset) (io.ReadCloser, error)
}

type aggregateVerifier func(context.Context, artifact.AggregateAssetSource, artifact.AggregateRequirements) (artifact.VerifiedAggregate, error)

// Catalog owns compatible candidate discovery and exact immutable verification.
type DiscoveryLimits struct {
	MaxCandidates int
	MaxBytes      int64
}
type Catalog struct {
	source    catalogSource
	discovery DiscoveryLimits
	verify    aggregateVerifier
}

// Candidate is one compatible checksum-verified manifest choice bound to its catalog.
type Candidate struct {
	catalog        *Catalog
	release        release
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

func New(source *GitHubSource) (*Catalog, error) {
	return NewWithDiscoveryLimits(source, DiscoveryLimits{})
}

// NewWithDiscoveryLimits configures bounded manifest discovery for listing and exact selection.
func NewWithDiscoveryLimits(source *GitHubSource, limits DiscoveryLimits) (*Catalog, error) {
	return newCatalog(source, limits, artifact.VerifyAggregate)
}

func newCatalog(source catalogSource, limits DiscoveryLimits, verifier aggregateVerifier) (*Catalog, error) {
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
	if verifier == nil {
		return nil, invalid("catalog construction", "verifier", "aggregate verifier is nil", "exact resolution cannot establish immutable output", "use the production constructor", fs.ErrInvalid)
	}
	return &Catalog{source: source, discovery: limits, verify: verifier}, nil
}

// ListCompatible returns typed checksum-verified choices in descending SemVer order.
func (c *Catalog) ListCompatible(ctx context.Context, installer artifact.Version, policy PrereleasePolicy) ([]Candidate, error) {
	if installer.String() == "" {
		return nil, invalid("candidate listing", "installer version", "the installer version is zero or was not constructed", "release compatibility cannot be established", "parse the running installer version with artifact.ParseVersion before listing candidates", fs.ErrInvalid)
	}
	if policy != FinalsOnly && policy != IncludePrereleases {
		return nil, invalid("candidate listing", "policy", "unsupported prerelease policy", "candidate channel cannot be filtered", "use FinalsOnly or IncludePrereleases", fs.ErrInvalid)
	}
	releases, err := c.source.listReleases(ctx)
	if err != nil {
		return nil, invalid("release listing", "GitHub Releases", fmt.Sprintf("catalog could not be loaded: %v", err), "no candidates are available", "repair GitHub access and retry", err)
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].version.Compare(releases[j].version) > 0 })
	result := make([]Candidate, 0, len(releases))
	defects := make([]string, 0, 8)
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
		if !ok {
			if len(defects) < cap(defects) {
				defects = append(defects, fmt.Sprintf("%s is missing manifest or checksum metadata", release.version))
			}
			continue
		}
		if manifestCost > c.discovery.MaxBytes-discoveryBytes {
			return nil, invalid("candidate listing", "byte limit", fmt.Sprintf("manifest discovery byte limit %d exhausted", c.discovery.MaxBytes), "candidate listing is explicitly incomplete", "construct the Catalog with a larger reviewed DiscoveryLimits value", fs.ErrInvalid)
		}
		discoveryBytes += manifestCost
		source := releaseAssetSource{source: c.source, assets: release.assets}
		manifestBytes, e := readCatalogAsset(ctx, source, artifact.AggregateManifestAsset, 4<<20)
		if e != nil {
			if isCandidateMetadataDefect(e) {
				if len(defects) < cap(defects) {
					defects = append(defects, fmt.Sprintf("%s manifest: %v", release.version, e))
				}
				continue
			}
			return nil, e
		}
		checksumBytes, e := readCatalogAsset(ctx, source, artifact.AggregateChecksumAsset, 4096)
		if e != nil {
			if isCandidateMetadataDefect(e) {
				if len(defects) < cap(defects) {
					defects = append(defects, fmt.Sprintf("%s checksum: %v", release.version, e))
				}
				continue
			}
			return nil, e
		}
		manifest, e := artifact.VerifyAggregateManifest(manifestBytes, checksumBytes)
		if e != nil {
			if len(defects) < cap(defects) {
				defects = append(defects, fmt.Sprintf("%s verification: %v", release.version, e))
			}
			continue
		}
		if manifest.Version() != release.version {
			if len(defects) < cap(defects) {
				defects = append(defects, fmt.Sprintf("%s version differs from manifest %s", release.version, manifest.Version()))
			}
			continue
		}
		if !manifest.Compatible(installer) {
			continue
		}
		sum := sha256.Sum256(manifestBytes)
		digest, _ := artifact.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
		result = append(result, Candidate{catalog: c, release: release, manifest: manifest, manifestDigest: digest, installer: installer})
	}
	if len(result) == 0 && len(defects) > 0 {
		return nil, invalid("candidate listing", "catalog", "all otherwise eligible candidates had invalid immutable metadata: "+strings.Join(defects, " | "), "no verified candidate choice is returned", "repair the listed manifest, checksum, or immutable release metadata and retry", errCandidateMetadata)
	}
	return result, nil
}

func (r release) manifestAssetBytes() (int64, bool) {
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
	verified, err := c.verify(ctx, source, artifact.AggregateRequirements{Version: candidate.Version(), Installer: candidate.installer, PastureRevision: candidate.PastureRevision(), AuraRevision: candidate.AuraRevision(), MaxAssetBytes: maxAssetBytes, ExpectedManifestDigest: candidate.manifestDigest})
	if err != nil {
		return artifact.VerifiedAggregate{}, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return artifact.VerifiedAggregate{}, invalid("candidate resolution", candidate.Version().String(), fmt.Sprintf("operation canceled after verification: %v", ctxErr), "no verified aggregate is returned to the installer service", "retry exact resolution with a live context", ctxErr)
	}
	return verified, nil
}

func readCatalogAsset(ctx context.Context, source releaseAssetSource, name string, limit int64) ([]byte, error) {
	reader, err := source.OpenAsset(ctx, name)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, invalid("candidate manifest open", name, fmt.Sprintf("operation canceled while opening candidate metadata: %v", ctxErr), "candidate discovery is incomplete", "retry with a live context", ctxErr)
	}
	if err != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, invalid("candidate manifest read", name, fmt.Sprintf("operation canceled while reading candidate metadata: %v", ctxErr), "candidate discovery is incomplete", "retry with a live context", ctxErr)
	}
	if readErr != nil {
		return nil, invalid("candidate manifest read", name, fmt.Sprintf("asset read failed: %v", readErr), "candidate cannot be listed safely", "repair the asset transport and retry", readErr)
	}
	if closeErr != nil {
		return nil, invalid("candidate manifest close", name, fmt.Sprintf("asset close failed: %v", closeErr), "candidate resource ownership is incomplete", "repair the asset transport and retry", closeErr)
	}
	if len(content) > int(limit) {
		return nil, invalid("candidate metadata validation", name, fmt.Sprintf("asset exceeds %d-byte limit", limit), "oversized candidate metadata was rejected", "publish a bounded manifest or sidecar", errCandidateMetadata)
	}
	return content, nil
}

type releaseAssetSource struct {
	source catalogSource
	assets map[string]asset
}

func (s releaseAssetSource) OpenAsset(ctx context.Context, name string) (io.ReadCloser, error) {
	asset, ok := s.assets[name]
	if !ok {
		return nil, invalid("candidate metadata validation", name, fmt.Sprintf("release asset %q is missing", name), "the candidate is incomplete", "publish the required immutable asset", errCandidateMetadata)
	}
	reader, err := s.source.openAsset(ctx, asset)
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
