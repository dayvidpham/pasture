package metamodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dayvidpham/pasture/internal/runtime"
)

// metamodelDoc is the canonical on-the-wire shape of the codebook body. Field
// order is fixed by struct declaration order, harnesses are sorted by runtime
// contract id, and events keep their pinned native catalog order, so the
// encoded bytes are deterministic.
type metamodelDoc struct {
	ID        string             `json:"id"`
	Version   uint32             `json:"version"`
	Harnesses []metamodelHarness `json:"harnesses"`
}

type metamodelHarness struct {
	Harness string           `json:"harness"`
	Events  []metamodelEvent `json:"events"`
}

type metamodelEvent struct {
	NativeName string   `json:"native_name"`
	Semantic   string   `json:"semantic"`
	Blocking   string   `json:"blocking"`
	Mutation   string   `json:"mutation"`
	Failure    string   `json:"failure"`
	StopLoop   string   `json:"stop_loop"`
	Identities []string `json:"identities"`
	Unresolved []string `json:"unresolved"`
}

// GenerateLifecycleMetamodel derives the canonical codebook body from the three pinned
// runtime lifecycle profiles (F17). It is deterministic: the same profiles
// always produce byte-identical output, which is what makes `make generate`
// idempotent and the content identity stable. It is used by the go:generate
// program that writes metamodel.gen.go and by the agreement tests that prove the
// committed generated body still matches the profiles.
func GenerateLifecycleMetamodel() ([]byte, error) {
	sections := make([]metamodelHarness, 0, 3)
	for _, build := range []func() (metamodelHarness, error){claudeSection, codexSection, openCodeSection} {
		section, err := build()
		if err != nil {
			return nil, err
		}
		sections = append(sections, section)
	}
	sort.Slice(sections, func(i, j int) bool { return sections[i].Harness < sections[j].Harness })

	doc := metamodelDoc{ID: string(LifecycleMetamodelID), Version: LifecycleMetamodelVersion, Harnesses: sections}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode canonical codebook body: %w", err)
	}
	return append([]byte(nil), bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})...), nil
}

func claudeSection() (metamodelHarness, error) {
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	events := make([]metamodelEvent, 0, len(contract.Events()))
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			return metamodelHarness{}, fmt.Errorf("codebook claude mapping for %v: %w", event, err)
		}
		events = append(events, eventFromMapping(mapping))
	}
	return metamodelHarness{Harness: contract.ID().String(), Events: events}, nil
}

func codexSection() (metamodelHarness, error) {
	contract := runtime.Codex0_146_0Lifecycle()
	events := make([]metamodelEvent, 0, len(contract.Events()))
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			return metamodelHarness{}, fmt.Errorf("codebook codex mapping for %v: %w", event, err)
		}
		events = append(events, eventFromMapping(mapping))
	}
	return metamodelHarness{Harness: contract.ID().String(), Events: events}, nil
}

func openCodeSection() (metamodelHarness, error) {
	contract := runtime.OpenCode1_18_10Lifecycle()
	events := make([]metamodelEvent, 0, len(contract.Events()))
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			return metamodelHarness{}, fmt.Errorf("codebook opencode mapping for %v: %w", event, err)
		}
		events = append(events, eventFromMapping(mapping))
	}
	return metamodelHarness{Harness: contract.ID().String(), Events: events}, nil
}

func eventFromMapping(mapping runtime.LifecycleEventMapping) metamodelEvent {
	identities := make([]string, 0, len(mapping.Identities()))
	for _, identity := range mapping.Identities() {
		identities = append(identities, identity.Kind().String())
	}
	unresolved := make([]string, 0, len(mapping.UnresolvedIdentities()))
	for _, kind := range mapping.UnresolvedIdentities() {
		unresolved = append(unresolved, kind.String())
	}
	return metamodelEvent{
		NativeName: mapping.NativeName(),
		Semantic:   mapping.Semantic().String(),
		Blocking:   mapping.Blocking().String(),
		Mutation:   mapping.Mutation().String(),
		Failure:    mapping.Failure().String(),
		StopLoop:   mapping.StopLoop().String(),
		Identities: identities,
		Unresolved: unresolved,
	}
}
