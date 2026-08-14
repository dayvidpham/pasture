package artifact_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

// These declarations only compile while the public canonical extensions are
// constants rather than mutable package variables.
const (
	immutableExtensionSkills artifact.Extension = artifact.ExtensionSkills
	immutableExtensionAgents artifact.Extension = artifact.ExtensionAgents
	immutableExtensionHooks  artifact.Extension = artifact.ExtensionHooks
)

type memoryAssets map[string][]byte

func TestCanonicalExtensionsAreImmutableConstants(t *testing.T) {
	t.Parallel()
	want := []string{"skills", "agents", "hooks"}
	got := []string{immutableExtensionSkills.String(), immutableExtensionAgents.String(), immutableExtensionHooks.String()}
	if !slices.Equal(got, want) {
		t.Fatalf("canonical extensions = %v, want %v", got, want)
	}
}

func (m memoryAssets) OpenAsset(_ context.Context, name string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m[name])), nil
}

func TestClaudeProductionDescriptorUsesPublicRuntimeIdentity(t *testing.T) {
	t.Parallel()
	descriptor, err := claudecode.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := artifact.ParseRuntimeContractID("claude-code/claude-code@2.1.210")
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.RuntimeContractID() != parsed || parsed.Harness() != artifact.HarnessClaudeCode {
		t.Fatalf("descriptor=%s parsed=%s", descriptor.RuntimeContractID(), parsed)
	}
}

func TestAggregateProducerRoundTripAndVerifiedCopies(t *testing.T) {
	t.Parallel()
	version, _ := artifact.ParseVersion("3.2.1")
	min, _ := artifact.ParseVersion("1.0.0")
	max, _ := artifact.ParseVersion("9.0.0")
	pasture, _ := artifact.ParseRevision("1111111111111111111111111111111111111111")
	aura, _ := artifact.ParseRevision("2222222222222222222222222222222222222222")
	harnesses := []artifact.Harness{artifact.HarnessClaudeCode, artifact.HarnessOpenCode, artifact.HarnessCodex}
	extensions := []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks}
	assets := memoryAssets{}
	specs := []artifact.AggregateComponentSpec{}
	for _, h := range harnesses {
		contract, _ := artifact.ProductionRuntimeContract(h)
		for _, e := range extensions {
			content := []byte(string(h) + "/" + e.String())
			stem := string(h)
			if h == artifact.HarnessClaudeCode {
				stem = "claude"
			}
			name := "pasture-3.2.1-" + stem + "-" + e.String() + ".tgz"
			assets[name] = content
			bundle, _ := artifact.ParseBundleID("artifact.bundle.v1:sha256:" + repeatHex(byte(len(specs)+1)))
			specs = append(specs, artifact.AggregateComponentSpec{Harness: h, Extension: e, Asset: name, Digest: artifact.DigestBytes(content), BundleID: bundle, RuntimeContractID: contract, PastureRevision: pasture, AuraRevision: aura})
		}
	}
	manifest, err := artifact.NewAggregateManifest(artifact.AggregateManifestSpec{Version: version, Channel: artifact.ReleaseFinal, InstallerMin: min, InstallerMax: max, PastureRevision: pasture, AuraRevision: aura, Components: specs})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := artifact.ParseAggregateManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Version() != version || parsed.Channel() != artifact.ReleaseFinal || parsed.InstallerMin() != min || parsed.InstallerMax() != max || parsed.PastureRevision() != pasture || parsed.AuraRevision() != aura || len(parsed.Components()) != 9 || parsed.Components()[0].RuntimeContractID().Harness() != parsed.Components()[0].Harness() {
		t.Fatal("typed component round trip failed")
	}
	expected := map[artifact.ComponentID]artifact.AggregateComponentSpec{}
	for _, spec := range specs {
		id, _ := artifact.ParseComponentID(string(spec.Harness) + "/" + spec.Extension.String())
		expected[id] = spec
	}
	for _, component := range parsed.Components() {
		spec, ok := expected[component.ID()]
		if !ok || component.Harness() != spec.Harness || component.Extension() != spec.Extension || component.Asset() != spec.Asset || component.Digest() != spec.Digest || component.BundleID() != spec.BundleID || component.RuntimeContractID() != spec.RuntimeContractID || component.PastureRevision() != spec.PastureRevision || component.AuraRevision() != spec.AuraRevision {
			t.Fatalf("component %s lost fidelity", component.ID())
		}
	}
	assets[artifact.AggregateManifestAsset] = encoded
	assets[artifact.AggregateChecksumAsset] = artifact.AggregateManifestChecksum(encoded)
	verified, err := artifact.VerifyAggregate(context.Background(), assets, artifact.AggregateRequirements{Version: version, Installer: min, PastureRevision: pasture, AuraRevision: aura})
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range parsed.Components() {
		first, ok := verified.Asset(component.ID())
		if !ok {
			t.Fatalf("missing %s", component.ID())
		}
		if !bytes.Equal(first, assets[component.Asset()]) {
			t.Fatalf("asset %s bytes differ", component.ID())
		}
		first[0] ^= 0xff
		second, _ := verified.Asset(component.ID())
		if bytes.Equal(first, second) {
			t.Fatalf("asset %s was not defensively copied", component.ID())
		}
	}
}

func repeatHex(value byte) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 64)
	for i := range b {
		b[i] = digits[value%16]
	}
	return string(b)
}
