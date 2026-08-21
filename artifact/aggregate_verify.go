package artifact

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"math"
	"strings"
)

const AggregateManifestAsset = "pasture-aggregate-manifest.json"
const AggregateChecksumAsset = "pasture-aggregate-manifest.json.sha256"
const defaultMaxAggregateAssetBytes int64 = 64 << 20
const defaultMaxAggregateTotalBytes int64 = 256 << 20

// AggregateManifestChecksum returns the canonical checksum sidecar bytes.
func AggregateManifestChecksum(manifest []byte) []byte {
	sum := sha256.Sum256(manifest)
	return []byte(hex.EncodeToString(sum[:]) + "  " + AggregateManifestAsset + "\n")
}

// AggregateAssetSource injects release asset I/O for verification and testing.
type AggregateAssetSource interface {
	OpenAsset(context.Context, string) (io.ReadCloser, error)
}

// AggregateRequirements are trusted installer-side constraints.
type AggregateRequirements struct {
	Version                Version
	Installer              Version
	PastureRevision        Revision
	AuraRevision           Revision
	MaxAssetBytes          int64
	MaxTotalBytes          int64
	ExpectedManifestDigest Digest
}

// VerifiedAggregate is produced only after all manifest and component bytes verify.
type VerifiedAggregate struct {
	manifest AggregateManifest
	assets   map[ComponentID][]byte
}

func (v VerifiedAggregate) Manifest() AggregateManifest { return v.manifest }
func (v VerifiedAggregate) Asset(id ComponentID) ([]byte, bool) {
	b, ok := v.assets[id]
	return append([]byte(nil), b...), ok
}

// VerifyAggregate verifies the checksum, identity, compatibility, revisions, and every component before returning a value usable by a mutator.
func VerifyAggregate(ctx context.Context, source AggregateAssetSource, requirements AggregateRequirements) (VerifiedAggregate, error) {
	if source == nil {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "asset source", "the asset source is nil", "release bytes cannot be read", "inject a GitHub release or filesystem asset source", fs.ErrInvalid)
	}
	if requirements.Installer.String() == "" {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "installer version", "the installer version is zero or was not constructed", "release compatibility cannot be established", "parse the running installer version with ParseVersion before verification", fs.ErrInvalid)
	}
	manifestBytes, err := readBounded(ctx, source, AggregateManifestAsset, 4<<20)
	if err != nil {
		return VerifiedAggregate{}, err
	}
	if requirements.ExpectedManifestDigest.String() != "" && DigestBytes(manifestBytes) != requirements.ExpectedManifestDigest {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "manifest identity", fmt.Sprintf("manifest digest changed from selected %s to %s", requirements.ExpectedManifestDigest, DigestBytes(manifestBytes)), "the selected candidate no longer identifies these bytes", "refresh the candidate list and reselect", fs.ErrInvalid)
	}
	checksumBytes, err := readBounded(ctx, source, AggregateChecksumAsset, 4096)
	if err != nil {
		return VerifiedAggregate{}, err
	}
	manifest, err := VerifyAggregateManifest(manifestBytes, checksumBytes)
	if err != nil {
		return VerifiedAggregate{}, err
	}
	if manifest.version != requirements.Version {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "version", fmt.Sprintf("manifest version %s differs from selected release %s", manifest.version, requirements.Version), "assets from different aggregate releases could be mixed", "publish a manifest whose version exactly matches the immutable release tag", fs.ErrInvalid)
	}
	if !manifest.Compatible(requirements.Installer) {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "compatibility", fmt.Sprintf("installer %s is outside inclusive range %s through %s", requirements.Installer, manifest.minInstaller, manifest.maxInstaller), "this installer cannot safely consume the release", "select a compatible release or upgrade the installer", fs.ErrInvalid)
	}
	if manifest.pasture != requirements.PastureRevision || manifest.aura != requirements.AuraRevision {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "revisions", fmt.Sprintf("manifest revisions pasture=%s aura=%s differ from required pasture=%s aura=%s", manifest.pasture, manifest.aura, requirements.PastureRevision, requirements.AuraRevision), "unreviewed source revisions could be installed", "select the aggregate built from the exact required source commits", fs.ErrInvalid)
	}
	limit := requirements.MaxAssetBytes
	if limit < 0 || limit == math.MaxInt64 {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "MaxAssetBytes", "asset limit is negative or would overflow the bounded reader", "complete byte verification cannot be guaranteed", "use zero for the default or a positive value below MaxInt64", fs.ErrInvalid)
	}
	if limit == 0 {
		limit = defaultMaxAggregateAssetBytes
	}
	totalLimit := requirements.MaxTotalBytes
	if totalLimit < 0 {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "MaxTotalBytes", "total limit is negative", "bounded verification cannot be guaranteed", "use zero for the default or a positive value", fs.ErrInvalid)
	}
	if totalLimit == 0 {
		totalLimit = defaultMaxAggregateTotalBytes
	}
	assets := make(map[ComponentID][]byte, 9)
	var total int64
	for _, component := range manifest.components {
		content, err := readBounded(ctx, source, component.asset, limit)
		if err != nil {
			return VerifiedAggregate{}, err
		}
		actual := DigestBytes(content)
		if actual != component.digest {
			return VerifiedAggregate{}, aggregateInvalid("component verification", component.id.String(), fmt.Sprintf("asset %q digest is %s but manifest requires %s", component.asset, actual, component.digest), "a corrupt or substituted component would be installed", "replace the release asset with the exact bytes named by the signed-off manifest", fs.ErrInvalid)
		}
		if int64(len(content)) > totalLimit-total {
			return VerifiedAggregate{}, aggregateInvalid("component verification", component.id.String(), "aggregate assets exceed the configured total-byte limit", "the complete release was rejected before mutation", "publish a bounded aggregate or configure an explicitly reviewed larger total limit", fs.ErrInvalid)
		}
		total += int64(len(content))
		assets[component.id] = content
	}
	if err := ctx.Err(); err != nil {
		return VerifiedAggregate{}, aggregateInvalid("aggregate verification", "verified output", fmt.Sprintf("operation canceled after the final component verification: %v", err), "no verified aggregate is returned to an installer service", "retry with a live context", err)
	}
	return VerifiedAggregate{manifest: manifest, assets: assets}, nil
}

// VerifyAggregateManifest validates the immutable checksum sidecar and strict manifest without opening components.
func VerifyAggregateManifest(manifestBytes, checksumBytes []byte) (AggregateManifest, error) {
	if err := verifyManifestChecksum(manifestBytes, checksumBytes); err != nil {
		return AggregateManifest{}, err
	}
	return ParseAggregateManifest(manifestBytes)
}

func readBounded(ctx context.Context, source AggregateAssetSource, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, aggregateInvalid("asset read", name, fmt.Sprintf("the operation was canceled before reading: %v", err), "verification is incomplete and mutation must not begin", "retry with a live context", err)
	}
	r, err := source.OpenAsset(ctx, name)
	if err != nil {
		if r != nil {
			_ = r.Close()
		}
		return nil, aggregateInvalid("asset open", name, fmt.Sprintf("the release asset could not be opened: %v", err), "verification is incomplete and mutation must not begin", "publish the named asset and ensure it is readable", err)
	}
	if r == nil {
		return nil, aggregateInvalid("asset open", name, "the asset source returned no reader and no error", "verification cannot read the release asset and mutation must not begin", "repair AggregateAssetSource.OpenAsset to return a non-nil reader on success", fs.ErrInvalid)
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	closeErr := r.Close()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, aggregateInvalid("asset read", name, fmt.Sprintf("the operation was canceled while reading: %v", ctxErr), "verification is incomplete and no verified aggregate is returned", "retry with a live context", ctxErr)
	}
	if err != nil {
		return nil, aggregateInvalid("asset read", name, fmt.Sprintf("the release asset could not be read completely: %v", err), "verification is incomplete and mutation must not begin", "repair the asset transport and retry", err)
	}
	if closeErr != nil {
		return nil, aggregateInvalid("asset close", name, fmt.Sprintf("the release asset could not be closed cleanly: %v", closeErr), "verification resource ownership is incomplete", "repair the asset transport and retry", closeErr)
	}
	if int64(len(b)) > limit {
		return nil, aggregateInvalid("asset read", name, fmt.Sprintf("asset exceeds the %d-byte verification limit", limit), "unbounded release data was rejected before mutation", "publish a bounded component or configure an explicitly reviewed larger limit", fs.ErrInvalid)
	}
	return b, nil
}

func verifyManifestChecksum(manifest, sidecar []byte) error {
	scanner := bufio.NewScanner(strings.NewReader(string(sidecar)))
	if !scanner.Scan() {
		return aggregateInvalid("manifest checksum verification", AggregateChecksumAsset, "checksum file must contain exactly one line", "manifest identity cannot be established", "publish '<64 lowercase hex>  pasture-aggregate-manifest.json' followed by a newline", fs.ErrInvalid)
	}
	line := scanner.Text()
	if scanner.Scan() || scanner.Err() != nil || string(sidecar) != line+"\n" {
		return aggregateInvalid("manifest checksum verification", AggregateChecksumAsset, "checksum file must contain exactly one line", "manifest identity cannot be established", "publish '<64 lowercase hex>  pasture-aggregate-manifest.json' followed by a newline", fs.ErrInvalid)
	}
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[1] != AggregateManifestAsset || len(fields[0]) != 64 || fields[0] != strings.ToLower(fields[0]) {
		return aggregateInvalid("manifest checksum verification", AggregateChecksumAsset, "checksum line has a malformed digest or filename", "manifest identity cannot be established", "publish the exact lowercase SHA-256 and immutable manifest filename", fs.ErrInvalid)
	}
	if line != fields[0]+"  "+AggregateManifestAsset {
		return aggregateInvalid("manifest checksum verification", AggregateChecksumAsset, "checksum spacing is not canonical", "manifest identity could be parsed differently by another consumer", "separate the digest and filename with exactly two spaces", fs.ErrInvalid)
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return aggregateInvalid("manifest checksum verification", AggregateChecksumAsset, "checksum is not lowercase hexadecimal", "manifest identity cannot be established", "publish a 64-character lowercase hexadecimal SHA-256", err)
	}
	sum := sha256.Sum256(manifest)
	actual := hex.EncodeToString(sum[:])
	if actual != fields[0] {
		return aggregateInvalid("manifest checksum verification", AggregateManifestAsset, fmt.Sprintf("manifest hashes to %s but checksum requires %s", actual, fields[0]), "a corrupt or substituted manifest was rejected before component reads", "replace the manifest or checksum with the matching reviewed pair", fs.ErrInvalid)
	}
	return nil
}
