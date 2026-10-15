package definitions_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDefinitionCodecControls(t *testing.T) {
	t.Parallel()
	var fixtures struct {
		Cases []struct {
			Name       string `yaml:"name"`
			Input      string `yaml:"input"`
			Equivalent string `yaml:"equivalent"`
			Invalid    bool   `yaml:"invalid"`
			Distinct   bool   `yaml:"distinct"`
		} `yaml:"cases"`
		DocumentMutations []string `yaml:"document_mutations"`
	}
	testutil.LoadFixtures(t, testutil.DefinitionCodec, &fixtures)
	for _, tc := range fixtures.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			first, err := definitions.NormalizePayload(definitions.EvidenceKind, []byte(tc.Input))
			if tc.Invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			second, err := definitions.NormalizePayload(definitions.EvidenceKind, []byte(tc.Equivalent))
			require.NoError(t, err)
			if tc.Distinct {
				require.NotEqual(t, first, second, "the pinned journal codec preserves numeric spelling")
			} else {
				require.Equal(t, first, second)
			}
			again, err := definitions.NormalizePayload(definitions.EvidenceKind, first)
			require.NoError(t, err)
			require.Equal(t, first, again)
		})
	}
}

func TestDefinitionKindAdmission(t *testing.T) {
	t.Parallel()
	var fixtures struct {
		Cases []struct {
			Kind     model.DefinitionKind `yaml:"kind"`
			Admitted bool                 `yaml:"admitted"`
		} `yaml:"cases"`
	}
	testutil.LoadFixtures(t, testutil.DefinitionKinds, &fixtures)
	for _, tc := range fixtures.Cases {
		t.Run(fmt.Sprint(tc.Kind), func(t *testing.T) {
			t.Parallel()
			intent, refusal := gate.NewEnvelopeDefinitionActivationIntent(tc.Kind, model.ContentIdentity{1})
			require.Equal(t, tc.Admitted, refusal == nil)
			if tc.Admitted {
				require.Equal(t, gate.WriteDefinitionActivation, intent.Class())
			}
			_, refusal = gate.NewEnvelopeDefinitionActivationIntent(tc.Kind, model.ContentIdentity{})
			require.NotNil(t, refusal)
		})
	}
}

func TestRuntimeGeneratedBodiesAgreeWithCompiledOwners(t *testing.T) {
	t.Parallel()
	var ledger struct {
		Versions []struct {
			Contract string `yaml:"contract"`
			Version  uint32 `yaml:"version"`
			SHA256   string `yaml:"sha256"`
		} `yaml:"versions"`
	}
	testutil.LoadFixtures(t, testutil.RuntimeDocumentVersions, &ledger)
	for _, contract := range definitions.RuntimeContracts() {
		t.Run(contract.String(), func(t *testing.T) {
			t.Parallel()
			doc, err := definitions.NewRuntimeContract(contract)
			require.NoError(t, err)
			generated, err := definitions.GenerateRuntimeBody(contract)
			require.NoError(t, err)
			require.Equal(t, generated, doc.Body(), "compiled owners changed: bump document version and regenerate")
			require.Equal(t, model.ContentIdentity(sha256.Sum256(generated)), doc.Content())
			matched := false
			for _, entry := range ledger.Versions {
				if entry.Contract == contract.String() && entry.Version == doc.Version() {
					content := doc.Content()
					require.Equal(t, entry.SHA256, hex.EncodeToString(content[:]), "a body change requires a document version bump")
					matched = true
				}
			}
			require.True(t, matched, "document coordinate must have a version ledger entry")
			body := doc.Body()
			body[0] ^= 1
			require.Equal(t, generated, doc.Body())
			decoded, err := definitions.Decode(generated)
			require.NoError(t, err)
			_, err = receipt.NewEnvelopeDefinitionActivation(decoded)
			require.Error(t, err, "read-only historical decoding must not provide an arbitrary new-writer JSON escape hatch")
		})
	}
}

func TestRuntimeOwnerRejectsUnknownFieldsAndUnsupportedCoordinate(t *testing.T) {
	t.Parallel()
	doc, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
	require.NoError(t, err)
	var fixtures struct {
		Cases []struct {
			Name       string `yaml:"name"`
			Input      string `yaml:"input"`
			Equivalent string `yaml:"equivalent"`
			Invalid    bool   `yaml:"invalid"`
			Distinct   bool   `yaml:"distinct"`
		} `yaml:"cases"`
		DocumentMutations []string `yaml:"document_mutations"`
	}
	testutil.LoadFixtures(t, testutil.DefinitionCodec, &fixtures)
	for _, name := range fixtures.DocumentMutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var fields map[string]any
			require.NoError(t, json.Unmarshal(doc.Body(), &fields))
			switch name {
			case "unknown":
				fields["unknown"] = true
			case "version":
				fields["version"] = 999
			case "codec":
				fields["codec"] = 255
			case "kind":
				fields["kind"] = 3
			case "contract":
				fields["specification"].(map[string]any)["contract"] = "codex/codex@0.153.0"
			case "unknown-self-consistent-contract":
				fields["id"] = "pasture.lifecycle.runtime-contract.claude-code/evil@9.9.9"
				fields["specification"].(map[string]any)["contract"] = "claude-code/evil@9.9.9"
			case "registration-contract":
				fields["specification"].(map[string]any)["registration"].(map[string]any)["Contract"] = "claude-code/tampered"
			case "capability":
				binding := make(map[string]any)
				for key, value := range fields["specification"].(map[string]any)["operations"].([]any)[0].(map[string]any) {
					binding[key] = value
				}
				binding["ID"] = "synthetic.inverted-capability"
				fields["specification"].(map[string]any)["capabilities"] = []any{map[string]any{"ID": "synthetic.inverted-capability", "Minimum": "99.0.0", "Maximum": "1.0.0", "Version": "1.0.0", "Binding": binding}}
			case "effect":
				binding := make(map[string]any)
				for key, value := range fields["specification"].(map[string]any)["operations"].([]any)[0].(map[string]any) {
					binding[key] = value
				}
				binding["ID"], binding["Class"], binding["NativeCall"] = "synthetic.broken-native-effect", "native", ""
				fields["specification"].(map[string]any)["effects"] = []any{binding}
			}
			raw, err := json.Marshal(fields)
			require.NoError(t, err)
			_, err = definitions.Decode(raw)
			require.Error(t, err)
			if name == "capability" {
				require.ErrorContains(t, err, "inverted or out-of-range version")
			}
			if name == "effect" {
				require.ErrorContains(t, err, "empty call name")
			}
		})
	}
}
