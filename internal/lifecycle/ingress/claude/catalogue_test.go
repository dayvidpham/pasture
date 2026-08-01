package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/dayvidpham/pasture/internal/acceptance"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestIndependentCatalogueCoversGeneratedManifest(t *testing.T) {
	t.Parallel()
	type row struct {
		Name     string   `yaml:"name"`
		Fields   []string `yaml:"fields"`
		Behavior string   `yaml:"behavior"`
	}
	var catalogue struct {
		Events []row `yaml:"events"`
	}
	raw, err := os.ReadFile("testdata/corpora/claude_2_1_210_catalogue.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &catalogue))
	manifest := registration.ClaudeCode2_1_210()
	require.Len(t, catalogue.Events, len(manifest.Events))
	for i, expected := range catalogue.Events {
		require.Equal(t, expected.Name, manifest.Events[i].NativeName, "independent catalogue order %d", i)
		actualFields := make([]string, 0)
		identityFields := make(map[string]struct{})
		for _, identity := range manifest.Events[i].Identities {
			identityFields[fieldNames[identity.Field]] = struct{}{}
		}
		for _, field := range manifest.Events[i].AllowedFields {
			name := fieldNames[field]
			_, identity := identityFields[name]
			if !isCommonField(name) || (identity && name != "session_id") {
				actualFields = append(actualFields, name)
			}
		}
		sort.Strings(actualFields)
		sort.Strings(expected.Fields)
		require.Equal(t, expected.Fields, actualFields, "independent field catalogue for %s", expected.Name)
		require.Equal(t, expected.Behavior, behaviorName(manifest.Events[i]), "independent behavior catalogue for %s", expected.Name)
	}
}

func TestClaudeCatalogueMatchesRuntimeMappingForAllOrdinals(t *testing.T) {
	t.Parallel()

	manifest := registration.ClaudeCode2_1_210()
	runtimeContract := runtime.ClaudeCode2_1_210Lifecycle()
	runtimeEvents := runtime.ClaudeLifecycleEvents()
	require.Len(t, manifest.Events, 30)
	require.Len(t, runtimeEvents, 30)

	for index, registered := range manifest.Events {
		runtimeEvent := runtimeEvents[index]
		require.Equal(t, model.ContractEventKind(index+1), registered.Kind, "registration ordinal %d", index+1)
		require.Equal(t, runtime.ClaudeLifecycleEvent(index+1), runtimeEvent, "runtime ordinal %d", index+1)
		require.Equal(t, registered.NativeName, runtimeEvent.NativeName(), "native event name ordinal %d", index+1)

		runtimeMapping, err := runtimeContract.Mapping(runtimeEvent)
		require.NoError(t, err)
		require.Equal(t, registered.NativeName, runtimeMapping.NativeName(), "mapping native name ordinal %d", index+1)
		require.Len(t, registered.Identities, len(runtimeMapping.Identities()), "identity count ordinal %d", index+1)
		bindings := make([]model.NativeBinding, 0, len(registered.Identities))
		for identityIndex, registeredIdentity := range registered.Identities {
			runtimeIdentity := runtimeMapping.Identities()[identityIndex]
			require.Equal(t, fieldNames[registeredIdentity.Field], runtimeIdentity.NativeName(), "identity native name ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, registeredIdentity.Required, runtimeIdentity.Required(), "identity required flag ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, uint8(registeredIdentity.Binding), uint8(runtimeIdentity.Kind()), "identity kind ordinal %d index %d", index+1, identityIndex)
			bindings = append(bindings, model.NativeBinding{
				Kind:       registeredIdentity.Binding,
				NativeName: fieldNames[registeredIdentity.Field],
				Value:      fmt.Sprintf("catalogue-%d-%d", index, identityIndex),
			})
		}
		bound, identities, err := claudefrontend.Bind(registered.Kind, bindings)
		require.NoError(t, err, "frontend mapping ordinal %d", index+1)
		require.True(t, bound.IsValid(), "frontend L1 ordinal %d", index+1)
		require.Len(t, identities, len(runtimeMapping.Identities()), "frontend identities ordinal %d", index+1)
		for identityIndex, identity := range bound.DeclaredIdentities() {
			runtimeIdentity := runtimeMapping.Identities()[identityIndex]
			require.Equal(t, runtimeIdentity.NativeName(), identity.NativeName(), "frontend native name ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, runtimeIdentity.Kind(), identity.Kind(), "frontend kind ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, runtimeIdentity.Required(), identity.Required(), "frontend required flag ordinal %d index %d", index+1, identityIndex)
		}
	}
}

type captureCorpus struct {
	Cases []captureCase `yaml:"cases"`
}

type captureCase struct {
	Name           string                `yaml:"name"`
	Input          captureInput          `yaml:"input"`
	Expected       captureExpected       `yaml:"expected"`
	Classification string                `yaml:"classification"`
	Provenance     captureCaseProvenance `yaml:"provenance"`
	Mutation       captureMutation       `yaml:"mutation"`
}

type captureInput struct {
	Fixture string `yaml:"fixture"`
}

type captureExpected struct {
	Decision string `yaml:"decision"`
	Reason   string `yaml:"reason"`
}

type captureCaseProvenance struct {
	Source string `yaml:"source"`
	Ref    string `yaml:"ref"`
}

type captureMutation struct {
	Description string `yaml:"description"`
}

func TestCaptureCorpusHasFrozenFiveCaseShape(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/captures.yaml")
	require.NoError(t, err)
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var corpus captureCorpus
	require.NoError(t, decoder.Decode(&corpus))
	require.Len(t, corpus.Cases, 5)

	wantReasons := map[string]struct{}{
		"non-authentic-origin": {},
		"digest-mismatch":      {},
		"version-out-of-range": {},
		"path-escape":          {},
	}
	wantSources := map[string]struct{}{
		"requirement": {},
		"bug":         {},
		"enum":        {},
		"boundary":    {},
		"manual":      {},
	}
	seenReasons := make(map[string]struct{}, len(wantReasons))
	seenNames := make(map[string]struct{}, len(corpus.Cases))
	passCount, failCount := 0, 0
	for _, testCase := range corpus.Cases {
		require.NotEmpty(t, testCase.Name)
		require.NotEmpty(t, testCase.Input.Fixture)
		require.False(t, filepath.IsAbs(testCase.Input.Fixture), "fixture paths must be relative to testdata")
		require.NotEmpty(t, testCase.Provenance.Source)
		require.NotEmpty(t, testCase.Provenance.Ref)
		require.NotEmpty(t, testCase.Mutation.Description)
		require.Contains(t, wantSources, testCase.Provenance.Source)
		_, duplicate := seenNames[testCase.Name]
		require.False(t, duplicate, "corpus case names must be unique")
		seenNames[testCase.Name] = struct{}{}

		switch testCase.Classification {
		case "must-pass":
			passCount++
			require.Equal(t, "enabled", testCase.Expected.Decision)
			require.Empty(t, testCase.Expected.Reason)
		case "must-fail":
			failCount++
			require.Equal(t, "withheld", testCase.Expected.Decision)
			require.Contains(t, wantReasons, testCase.Expected.Reason)
			_, duplicate = seenReasons[testCase.Expected.Reason]
			require.False(t, duplicate, "each closed withheld reason must have one case")
			seenReasons[testCase.Expected.Reason] = struct{}{}
		default:
			t.Fatalf("case %q has unknown classification %q", testCase.Name, testCase.Classification)
		}
	}
	require.Equal(t, 1, passCount)
	require.Equal(t, 4, failCount)
	require.Equal(t, wantReasons, seenReasons)
}

func TestAuthenticProvenanceValidatesUnchangedFixture(t *testing.T) {
	t.Parallel()

	provenanceBytes, err := os.ReadFile("testdata/fixtures/session_start_2_1_210.provenance.json")
	require.NoError(t, err)
	var provenanceRecord acceptance.CaptureProvenance
	// Decode through encoding/json without DisallowUnknownFields: redaction and
	// event are intentional provenance metadata outside CaptureProvenance.
	require.NoError(t, json.Unmarshal(provenanceBytes, &provenanceRecord))
	require.NoError(t, provenanceRecord.ValidateFixture("testdata", "fixtures/session_start_2_1_210.json"))
}

func TestCaptureCorpusMetadataControlsHaveSiblingProvenance(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/captures.yaml")
	require.NoError(t, err)
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var corpus captureCorpus
	require.NoError(t, decoder.Decode(&corpus))
	for _, testCase := range corpus.Cases {
		if strings.HasPrefix(filepath.Clean(testCase.Input.Fixture), "..") {
			_, err := os.Stat(filepath.Join("testdata", testCase.Input.Fixture))
			require.Error(t, err, "path-escape control must not add a fake host capture")
			continue
		}

		provenancePath := strings.TrimSuffix(testCase.Input.Fixture, ".json") + ".provenance.json"
		provenanceBytes, err := os.ReadFile(filepath.Join("testdata", provenancePath))
		require.NoError(t, err, "sibling provenance for %s", testCase.Input.Fixture)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(provenanceBytes, &fields))
		require.Len(t, fields, 8, "provenance for %s", testCase.Input.Fixture)
		for _, field := range []string{"origin", "harness", "harnessVersion", "captureSource", "rawFileDigest", "capturedAt", "redaction", "event"} {
			_, present := fields[field]
			require.True(t, present, "provenance %s has %s", provenancePath, field)
		}

		var provenanceRecord acceptance.CaptureProvenance
		require.NoError(t, json.Unmarshal(provenanceBytes, &provenanceRecord))
		switch testCase.Expected.Reason {
		case "":
			require.Equal(t, acceptance.OriginAuthenticCapture, provenanceRecord.Origin)
		case "non-authentic-origin":
			require.Equal(t, acceptance.OriginAuthored, provenanceRecord.Origin)
		case "digest-mismatch":
			require.NotEqual(t, "sha256:30d524e5d2cb22d486faad05adbaa1a4b7e0d72cd6301f38fe18ca5e3f167003", provenanceRecord.RawFileDigest)
		case "version-out-of-range":
			require.Equal(t, "2.2.0", provenanceRecord.HarnessVersion)
		default:
			t.Fatalf("unexpected corpus reason %q", testCase.Expected.Reason)
		}
	}
}

func TestDerivedCorpusFixturesAreByteIdenticalMetadataControls(t *testing.T) {
	t.Parallel()

	base, err := os.ReadFile("testdata/fixtures/session_start_2_1_210.json")
	require.NoError(t, err)
	for _, name := range []string{
		"session_start_2_1_210_origin_authored.json",
		"session_start_2_1_210_digest_mismatch.json",
		"session_start_2_1_210_version_out_of_range.json",
	} {
		derived, err := os.ReadFile(filepath.Join("testdata/fixtures", name))
		require.NoError(t, err)
		require.Equal(t, base, derived, "derived control %s must not claim a new host capture", name)
	}
}

func TestCaptureScriptEmitsMatchingDeterministicSiblings(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is unavailable in the development shell")
	}

	out := t.TempDir()
	home := "/home/capture-user"
	script := filepath.Join("..", "..", "..", "..", "tools", "capture-claude-hook.sh")
	body := `{"session_id":"session-1","cwd":"/home/capture-user/project","hook_event_name":"SessionStart"}`
	command := exec.Command("bash", script)
	command.Env = append(os.Environ(),
		"PASTURE_CAPTURE_DIR="+out,
		"HOME="+home,
		"CLAUDE_CODE_VERSION=2.1.220",
	)
	command.Stdin = strings.NewReader(body)
	require.NoError(t, command.Run())

	fixture := filepath.Join(out, "session_start_2_1_220.json")
	provenance := filepath.Join(out, "session_start_2_1_220.provenance.json")
	fixtureBytes, err := os.ReadFile(fixture)
	require.NoError(t, err)
	provenanceBytes, err := os.ReadFile(provenance)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(out, "SessionStart.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.True(t, json.Valid(fixtureBytes))
	require.Contains(t, string(fixtureBytes), `"cwd":"/home/user/project"`)
	var provenanceFields map[string]any
	require.NoError(t, json.Unmarshal(provenanceBytes, &provenanceFields))
	require.Equal(t, "2.1.220", provenanceFields["harnessVersion"])
}

func isCommonField(name string) bool {
	for _, common := range []string{"session_id", "transcript_path", "cwd", "permission_mode", "hook_event_name", "effort", "agent_id", "agent_type"} {
		if name == common {
			return true
		}
	}
	return false
}

func behaviorName(event registration.Event) string {
	if event.NativeName == "ElicitationResult" {
		return "human-response"
	}
	if event.StopLoop == registration.StopLoopConsultWhenInactive {
		return "stop-gate"
	}
	if event.Blocking == registration.ConditionallyBlocking {
		return "conditional-gate"
	}
	if event.Blocking == registration.NonBlocking {
		return "observation"
	}
	return "gate"
}
