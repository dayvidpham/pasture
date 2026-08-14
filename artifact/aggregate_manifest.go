package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
)

// AggregateValidationError is an actionable aggregate release failure.
type AggregateValidationError struct {
	Stage, Field, Reason, Impact, Fix string
	Cause                             error
}

func (e *AggregateValidationError) Error() string {
	return fmt.Sprintf("aggregate release: %s failed at %s because %s; impact: %s; fix: %s", e.Stage, e.Field, e.Reason, e.Impact, e.Fix)
}
func (e *AggregateValidationError) Unwrap() error { return e.Cause }
func aggregateInvalid(stage, field, reason, impact, fix string, cause error) error {
	return &AggregateValidationError{stage, field, reason, impact, fix, cause}
}

// AggregateComponent is one immutable member of the closed three-by-three set.
type AggregateComponent struct {
	id              ComponentID
	harness         Harness
	extension       Extension
	asset           string
	digest          Digest
	bundle          BundleID
	runtimeContract RuntimeContractID
	pasture, aura   Revision
}

func (c AggregateComponent) ID() ComponentID                      { return c.id }
func (c AggregateComponent) Harness() Harness                     { return c.harness }
func (c AggregateComponent) Extension() Extension                 { return c.extension }
func (c AggregateComponent) Asset() string                        { return c.asset }
func (c AggregateComponent) Digest() Digest                       { return c.digest }
func (c AggregateComponent) BundleID() BundleID                   { return c.bundle }
func (c AggregateComponent) RuntimeContractID() RuntimeContractID { return c.runtimeContract }
func (c AggregateComponent) PastureRevision() Revision            { return c.pasture }
func (c AggregateComponent) AuraRevision() Revision               { return c.aura }

// AggregateManifest is a completely validated immutable aggregate release.
type AggregateManifest struct {
	version, minInstaller, maxInstaller Version
	channel                             ReleaseChannel
	pasture, aura                       Revision
	components                          []AggregateComponent
}

// AggregateComponentSpec is the typed producer input for one component.
type AggregateComponentSpec struct {
	Harness           Harness
	Extension         Extension
	Asset             string
	Digest            Digest
	BundleID          BundleID
	RuntimeContractID RuntimeContractID
	PastureRevision   Revision
	AuraRevision      Revision
}

// AggregateManifestSpec is the typed producer input for a complete release.
type AggregateManifestSpec struct {
	Version         Version
	Channel         ReleaseChannel
	InstallerMin    Version
	InstallerMax    Version
	PastureRevision Revision
	AuraRevision    Revision
	Components      []AggregateComponentSpec
}

func (m AggregateManifest) Version() Version          { return m.version }
func (m AggregateManifest) Channel() ReleaseChannel   { return m.channel }
func (m AggregateManifest) PastureRevision() Revision { return m.pasture }
func (m AggregateManifest) AuraRevision() Revision    { return m.aura }
func (m AggregateManifest) InstallerMin() Version     { return m.minInstaller }
func (m AggregateManifest) InstallerMax() Version     { return m.maxInstaller }
func (m AggregateManifest) Components() []AggregateComponent {
	return append([]AggregateComponent(nil), m.components...)
}
func (m AggregateManifest) Compatible(installer Version) bool {
	return installer.Compare(m.minInstaller) >= 0 && installer.Compare(m.maxInstaller) <= 0
}

type aggregateWire struct {
	Schema        string            `json:"schema"`
	Version       string            `json:"version"`
	Channel       string            `json:"channel"`
	Compatibility compatibilityWire `json:"compatibility"`
	Revisions     revisionsWire     `json:"revisions"`
	Components    []componentWire   `json:"components"`
}
type compatibilityWire struct {
	InstallerMin string `json:"installer_min"`
	InstallerMax string `json:"installer_max"`
}
type revisionsWire struct {
	Pasture string `json:"pasture"`
	Aura    string `json:"aura"`
}
type componentWire struct {
	ID              string `json:"id"`
	Harness         string `json:"harness"`
	Extension       string `json:"extension"`
	Asset           string `json:"asset"`
	Digest          string `json:"digest"`
	BundleID        string `json:"bundle_id"`
	RuntimeContract string `json:"runtime_contract"`
	PastureRevision string `json:"pasture_revision"`
	AuraRevision    string `json:"aura_revision"`
}

// NewAggregateManifest validates typed producer input through the public codec.
func NewAggregateManifest(spec AggregateManifestSpec) (AggregateManifest, error) {
	wire := aggregateWire{Schema: AggregateManifestSchema, Version: spec.Version.String(), Channel: string(spec.Channel), Compatibility: compatibilityWire{InstallerMin: spec.InstallerMin.String(), InstallerMax: spec.InstallerMax.String()}, Revisions: revisionsWire{Pasture: spec.PastureRevision.String(), Aura: spec.AuraRevision.String()}, Components: make([]componentWire, 0, len(spec.Components))}
	for _, c := range spec.Components {
		wire.Components = append(wire.Components, componentWire{ID: string(canonicalComponentID(c.Harness, c.Extension)), Harness: string(c.Harness), Extension: c.Extension.String(), Asset: c.Asset, Digest: c.Digest.String(), BundleID: c.BundleID.String(), RuntimeContract: c.RuntimeContractID.String(), PastureRevision: c.PastureRevision.String(), AuraRevision: c.AuraRevision.String()})
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return AggregateManifest{}, aggregateInvalid("manifest construction", "manifest", fmt.Sprintf("typed input could not be encoded: %v", err), "the aggregate cannot be constructed", "provide valid typed component inputs", err)
	}
	return ParseAggregateManifest(encoded)
}

// MarshalJSON emits the canonical aggregate manifest representation.
func (m AggregateManifest) MarshalJSON() ([]byte, error) {
	if m.version.String() == "" {
		return nil, aggregateInvalid("manifest encoding", "manifest", "the zero aggregate manifest was not constructed", "an invalid release cannot be published", "construct it with NewAggregateManifest or ParseAggregateManifest", fs.ErrInvalid)
	}
	wire := aggregateWire{Schema: AggregateManifestSchema, Version: m.version.String(), Channel: string(m.channel), Compatibility: compatibilityWire{InstallerMin: m.minInstaller.String(), InstallerMax: m.maxInstaller.String()}, Revisions: revisionsWire{Pasture: m.pasture.String(), Aura: m.aura.String()}, Components: make([]componentWire, 0, len(m.components))}
	for _, c := range m.components {
		wire.Components = append(wire.Components, componentWire{ID: string(c.id), Harness: string(c.harness), Extension: c.extension.String(), Asset: c.asset, Digest: c.digest.String(), BundleID: c.bundle.String(), RuntimeContract: c.runtimeContract.String(), PastureRevision: c.pasture.String(), AuraRevision: c.aura.String()})
	}
	return json.Marshal(wire)
}

// ParseAggregateManifest strictly decodes the public aggregate manifest codec.
func ParseAggregateManifest(data []byte) (AggregateManifest, error) {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return AggregateManifest{}, err
	}
	var wire aggregateWire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return AggregateManifest{}, aggregateInvalid("manifest decoding", "JSON", fmt.Sprintf("the manifest is malformed or has unknown fields: %v", err), "no release artifact can be trusted", "publish one strict pasture.aggregate-release/v1 JSON object", err)
	}
	if err := ensureEOF(dec); err != nil {
		return AggregateManifest{}, err
	}
	if wire.Schema != AggregateManifestSchema {
		return AggregateManifest{}, aggregateInvalid("manifest decoding", "schema", fmt.Sprintf("%q is not %q", wire.Schema, AggregateManifestSchema), "manifest semantics may be incompatible", "publish the supported aggregate schema", fs.ErrInvalid)
	}
	version, err := ParseVersion(wire.Version)
	if err != nil {
		return AggregateManifest{}, err
	}
	channel := ReleaseChannel(wire.Channel)
	if channel != ReleaseFinal && channel != ReleasePrerelease {
		return AggregateManifest{}, aggregateInvalid("manifest decoding", "channel", fmt.Sprintf("unsupported channel %q", wire.Channel), "final/prerelease policy cannot be enforced", "use final or prerelease", fs.ErrInvalid)
	}
	if (channel == ReleaseFinal) == version.IsPrerelease() {
		return AggregateManifest{}, aggregateInvalid("manifest decoding", "channel", "channel disagrees with version prerelease state", "release policy could select the wrong channel", "mark stable SemVer final and prerelease SemVer prerelease", fs.ErrInvalid)
	}
	min, err := ParseVersion(wire.Compatibility.InstallerMin)
	if err != nil {
		return AggregateManifest{}, err
	}
	max, err := ParseVersion(wire.Compatibility.InstallerMax)
	if err != nil {
		return AggregateManifest{}, err
	}
	if min.Compare(max) > 0 {
		return AggregateManifest{}, aggregateInvalid("manifest decoding", "compatibility", "installer_min is greater than installer_max", "no deterministic compatible range exists", "publish an inclusive non-empty installer range", fs.ErrInvalid)
	}
	pasture, err := parseRevision(wire.Revisions.Pasture, "revisions.pasture")
	if err != nil {
		return AggregateManifest{}, err
	}
	aura, err := parseRevision(wire.Revisions.Aura, "revisions.aura")
	if err != nil {
		return AggregateManifest{}, err
	}
	if len(wire.Components) != 9 {
		return AggregateManifest{}, aggregateInvalid("manifest decoding", "components", fmt.Sprintf("found %d components instead of exactly 9", len(wire.Components)), "the aggregate does not cover the complete installation matrix", "publish exactly skills, agents, and hooks for all three harnesses", fs.ErrInvalid)
	}
	components := make([]AggregateComponent, 0, 9)
	seen := map[ComponentID]bool{}
	assets := map[string]bool{}
	for i, item := range wire.Components {
		h, e := parseHarness(item.Harness)
		if e != nil {
			return AggregateManifest{}, e
		}
		x, e := parseExtension(item.Extension)
		if e != nil {
			return AggregateManifest{}, e
		}
		id := canonicalComponentID(h, x)
		if item.ID != string(id) || seen[id] {
			return AggregateManifest{}, aggregateInvalid("manifest decoding", fmt.Sprintf("components[%d].id", i), fmt.Sprintf("identity %q is mismatched or duplicated; expected target identity %q", item.ID, id), "component identity cannot be proven", "publish each exact target descriptor harness/extension identity once", fs.ErrInvalid)
		}
		seen[id] = true
		stem := string(h)
		if h == HarnessClaudeCode {
			stem = "claude"
		}
		expectedAsset := fmt.Sprintf("pasture-%s-%s-%s.tgz", version, stem, x.String())
		if item.Asset != expectedAsset || strings.Contains(item.Asset, "pasture-stable") || assets[item.Asset] {
			return AggregateManifest{}, aggregateInvalid("manifest decoding", fmt.Sprintf("components[%d].asset", i), fmt.Sprintf("asset name %q is duplicated, moving, or differs from canonical %q", item.Asset, expectedAsset), "the component is not tied to one immutable aggregate version", "use the exact canonical immutable component basename; never use pasture-stable", fs.ErrInvalid)
		}
		assets[item.Asset] = true
		digest, e := ParseDigest(item.Digest)
		if e != nil {
			return AggregateManifest{}, e
		}
		bundle, e := ParseBundleID(item.BundleID)
		if e != nil {
			return AggregateManifest{}, e
		}
		pr, e := parseRevision(item.PastureRevision, "component.pasture_revision")
		if e != nil {
			return AggregateManifest{}, e
		}
		ar, e := parseRevision(item.AuraRevision, "component.aura_revision")
		if e != nil {
			return AggregateManifest{}, e
		}
		if pr != pasture || ar != aura {
			return AggregateManifest{}, aggregateInvalid("manifest decoding", fmt.Sprintf("components[%d].revisions", i), "component revisions differ from aggregate revisions", "components from different source revisions could be mixed", "rebuild every component from the manifest's exact Pasture and Aura commits", fs.ErrInvalid)
		}
		runtimeContract, e := ParseRuntimeContractID(item.RuntimeContract)
		if e != nil || runtimeContract.Harness() != h {
			return AggregateManifest{}, aggregateInvalid("manifest decoding", fmt.Sprintf("components[%d].runtime_contract", i), fmt.Sprintf("runtime contract %q is not bound to harness %q", item.RuntimeContract, h), "runtime compatibility could be tied to the wrong target", "use the exact target descriptor RuntimeContractID", e)
		}
		productionContract, _ := ProductionRuntimeContract(h)
		if runtimeContract != productionContract {
			return AggregateManifest{}, aggregateInvalid("manifest decoding", fmt.Sprintf("components[%d].runtime_contract", i), fmt.Sprintf("runtime contract %q is not registered production profile %q", runtimeContract, productionContract), "unknown or stale target bytes cannot enter an aggregate", "use ProductionRuntimeContract for the component harness", fs.ErrInvalid)
		}
		components = append(components, AggregateComponent{id, h, x, item.Asset, digest, bundle, runtimeContract, pr, ar})
	}
	sort.Slice(components, func(i, j int) bool { return components[i].id < components[j].id })
	return AggregateManifest{version, min, max, channel, pasture, aura, components}, nil
}

// UnmarshalJSON replaces m only after complete strict validation.
func (m *AggregateManifest) UnmarshalJSON(data []byte) error {
	if m == nil {
		return aggregateInvalid("manifest decoding", "manifest", "decode target is nil", "the validated manifest cannot be returned", "decode into a non-nil *AggregateManifest", fs.ErrInvalid)
	}
	parsed, err := ParseAggregateManifest(data)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func(string) error
	walk = func(location string) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			allowed := allowedAggregateFields(location)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fs.ErrInvalid
				}
				if !allowed[key] {
					return aggregateInvalid("manifest decoding", location+"."+key, fmt.Sprintf("field %q is unknown or not canonically spelled", key), "case-folded or unknown JSON keys are ambiguous across consumers", "use only the exact lowercase schema keys", fs.ErrInvalid)
				}
				if seen[key] {
					return aggregateInvalid("manifest decoding", location+"."+key, fmt.Sprintf("field %q appears more than once", key), "duplicate JSON fields make the release ambiguous", "publish every object field exactly once", fs.ErrInvalid)
				}
				seen[key] = true
				if err := walk(location + "." + key); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			index := 0
			for decoder.More() {
				if err := walk(fmt.Sprintf("%s[%d]", location, index)); err != nil {
					return err
				}
				index++
			}
			_, err = decoder.Token()
			return err
		default:
			return fs.ErrInvalid
		}
	}
	if err := walk("manifest"); err != nil {
		var validation *AggregateValidationError
		if errors.As(err, &validation) {
			return err
		}
		return aggregateInvalid("manifest decoding", "JSON", fmt.Sprintf("canonical field validation failed: %v", err), "the manifest cannot be interpreted canonically", "publish one strict JSON object without duplicate fields", err)
	}
	return nil
}

func allowedAggregateFields(location string) map[string]bool {
	fields := []string{}
	switch {
	case location == "manifest":
		fields = []string{"schema", "version", "channel", "compatibility", "revisions", "components"}
	case location == "manifest.compatibility":
		fields = []string{"installer_min", "installer_max"}
	case location == "manifest.revisions":
		fields = []string{"pasture", "aura"}
	case strings.HasPrefix(location, "manifest.components["):
		fields = []string{"id", "harness", "extension", "asset", "digest", "bundle_id", "runtime_contract", "pasture_revision", "aura_revision"}
	}
	result := make(map[string]bool, len(fields))
	for _, field := range fields {
		result[field] = true
	}
	return result
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return aggregateInvalid("manifest decoding", "JSON", "trailing data follows the manifest object", "the manifest has more than one interpretation", "remove all bytes after the single JSON object", fs.ErrInvalid)
	}
	return nil
}
