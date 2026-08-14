// Package releasecatalog selects and completely verifies immutable aggregate GitHub Releases.
package releasecatalog

import (
	"context"
	"fmt"
	"io"
	"io/fs"
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

// Release is one validated, non-draft GitHub Release.
type Release struct {
	version    artifact.Version
	prerelease bool
	assets     map[string]Asset
}

func (r Release) Version() artifact.Version { return r.version }
func (r Release) Prerelease() bool          { return r.prerelease }

// Source injects the GitHub catalog and asset network boundary.
type Source interface {
	ListReleases(context.Context) ([]Release, error)
	OpenURL(context.Context, string) (io.ReadCloser, error)
}

// Selection fixes trusted constraints before any release bytes are consumed.
type Selection struct {
	Installer       artifact.Version
	PastureRevision artifact.Revision
	AuraRevision    artifact.Revision
	Policy          PrereleasePolicy
	MaxAssetBytes   int64
}

// Mutator is called only with a completely verified immutable aggregate.
type Mutator interface {
	ApplyVerifiedAggregate(context.Context, artifact.VerifiedAggregate) error
}

// Catalog owns release selection, complete verification, then optional mutation.
type Catalog struct{ source Source }

func New(source Source) (*Catalog, error) {
	if source == nil {
		return nil, invalid("catalog construction", "source", "the GitHub release source is nil", "no release can be selected or verified", "inject a configured GitHub source", fs.ErrInvalid)
	}
	return &Catalog{source: source}, nil
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
	releases, err := c.source.ListReleases(ctx)
	if err != nil {
		return artifact.VerifiedAggregate{}, invalid("release listing", "GitHub Releases", fmt.Sprintf("the release catalog could not be loaded: %v", err), "no release was selected and mutation must not begin", "repair GitHub access or use a deterministic injected catalog and retry", err)
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].version.Compare(releases[j].version) > 0 })
	var failures []string
	for _, release := range releases {
		if release.prerelease && selection.Policy != IncludePrereleases {
			continue
		}
		if release.prerelease != release.version.IsPrerelease() {
			continue
		}
		assets := releaseAssetSource{source: c.source, assets: release.assets}
		verified, verifyErr := artifact.VerifyAggregate(ctx, assets, artifact.AggregateRequirements{Version: release.version, Installer: selection.Installer, PastureRevision: selection.PastureRevision, AuraRevision: selection.AuraRevision, MaxAssetBytes: selection.MaxAssetBytes})
		if verifyErr == nil {
			return verified, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", release.version, verifyErr))
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
