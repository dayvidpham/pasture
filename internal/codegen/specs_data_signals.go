// The one authored home of the signal-capture component.
//
// Capture records the user's friction or approval so a later project-level
// skill can distill it. It is a side effect of work already happening: it never
// prompts the user and never blocks a phase. One feedback task per epoch holds
// one line per signal on the pasture backend; the ledger backend appends the
// same line to a file. The kind is a typed closed set and the line fields are a
// registry, so a guard reads both instead of matching prose.
package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SignalKind is the closed set of signal kinds. A kind is registered in
// SignalKindSpecs to be valid.
type SignalKind string

const (
	// SignalFrustration is annoyance, a repeated complaint, or a rejected
	// interaction.
	SignalFrustration SignalKind = "frustration"
	// SignalPositive is an early endorsement of a direction or a result.
	SignalPositive SignalKind = "positive-signal"
	// SignalSatisfaction is a gate accepted with the result the user wanted.
	SignalSatisfaction SignalKind = "satisfaction"
	// SignalConfusion is a term question, a misread question, or an answer to
	// a different question.
	SignalConfusion SignalKind = "confusion"
	// SignalCorrection is a corrected fact, requirement, or earlier answer.
	SignalCorrection SignalKind = "correction"
)

var canonicalSignalKinds = []SignalKind{
	SignalFrustration,
	SignalPositive,
	SignalSatisfaction,
	SignalConfusion,
	SignalCorrection,
}

// AllSignalKinds returns a fresh copy of the closed kind set in registry order.
func AllSignalKinds() []SignalKind {
	return append([]SignalKind(nil), canonicalSignalKinds...)
}

// IsValid reports whether the value is a registered signal kind.
func (k SignalKind) IsValid() bool {
	for _, candidate := range canonicalSignalKinds {
		if candidate == k {
			return true
		}
	}
	return false
}

// SignalKindSpec is one row of the kind registry.
type SignalKindSpec struct {
	Kind       SignalKind
	Definition string
}

// SignalKindSpecs is the one kind registry. Every canonical kind carries its
// definition here; the fragment renders the list from it.
var SignalKindSpecs = map[SignalKind]SignalKindSpec{
	SignalFrustration: {
		Kind:       SignalFrustration,
		Definition: "the user shows annoyance, repeats a complaint, or rejects the interaction itself.",
	},
	SignalPositive: {
		Kind:       SignalPositive,
		Definition: "the user endorses a direction or a result before a gate asks.",
	},
	SignalSatisfaction: {
		Kind:       SignalSatisfaction,
		Definition: "the user accepts a gate and says the result is what they wanted.",
	},
	SignalConfusion: {
		Kind:       SignalConfusion,
		Definition: "the user asks what a term means, misreads a question, or answers a different question.",
	},
	SignalCorrection: {
		Kind:       SignalCorrection,
		Definition: "the user corrects a fact, a requirement, or an earlier answer.",
	},
}

// ValidateSignalKinds proves the kind registry is closed and complete. It runs
// on the generation path, so an unregistered kind fails the build rather than
// rendering.
func ValidateSignalKinds() error {
	return validateSignalKinds(canonicalSignalKinds, SignalKindSpecs)
}

// validateSignalKinds is the pure core of the registry guard. It accepts any
// pair of set and rows, so a sixth kind can prove the guard fires.
func validateSignalKinds(kinds []SignalKind, specs map[SignalKind]SignalKindSpec) error {
	if len(specs) != len(kinds) {
		return fmt.Errorf("codegen.ValidateSignalKinds: the registry has %d rows but the closed set has %d kinds — why: a row with no kind, or a kind with no row, would ship an undefined or unrenderable kind; where: internal/codegen/specs_data_signals.go SignalKindSpecs; fix: add the missing row or remove the extra one", len(specs), len(kinds))
	}
	seen := make(map[SignalKind]bool, len(kinds))
	for _, kind := range kinds {
		if seen[kind] {
			return fmt.Errorf("codegen.ValidateSignalKinds: kind %q appears twice — why: one kind has one definition; where: internal/codegen/specs_data_signals.go canonicalSignalKinds; fix: remove the duplicate", kind)
		}
		seen[kind] = true
		spec, ok := specs[kind]
		if !ok {
			return fmt.Errorf("codegen.ValidateSignalKinds: kind %q is in the closed set but has no registry row — why: the fragment renders from the rows, so the kind would never appear; where: internal/codegen/specs_data_signals.go SignalKindSpecs; fix: add the row", kind)
		}
		if spec.Kind != kind {
			return fmt.Errorf("codegen.ValidateSignalKinds: row key %q carries Kind %q — why: the registry would render a different kind than the key names; where: internal/codegen/specs_data_signals.go SignalKindSpecs; fix: make the row key and Kind agree", kind, spec.Kind)
		}
		if strings.TrimSpace(spec.Definition) == "" {
			return fmt.Errorf("codegen.ValidateSignalKinds: kind %q has no definition — why: a reader cannot tell when to use it; where: internal/codegen/specs_data_signals.go SignalKindSpecs; fix: add the definition", kind)
		}
	}
	return nil
}

// renderSignalKinds renders the closed-kind bullet list from the registry. An
// unregistered kind is an error, so a sixth kind cannot reach the shipped text.
func renderSignalKinds(kinds []SignalKind, specs map[SignalKind]SignalKindSpec) (string, error) {
	lines := []string{"The kind is a closed list. Use exactly one of:", ""}
	for _, kind := range kinds {
		if !kind.IsValid() {
			return "", fmt.Errorf("codegen.renderSignalKinds: kind %q is not registered — why: the kind set is closed, so an unregistered kind would ship undefined; where: internal/codegen/specs_data_signals.go canonicalSignalKinds; fix: register the kind in canonicalSignalKinds and SignalKindSpecs", kind)
		}
		spec, ok := specs[kind]
		if !ok {
			return "", fmt.Errorf("codegen.renderSignalKinds: kind %q has no registry row — why: the bullet would render with no definition; where: internal/codegen/specs_data_signals.go SignalKindSpecs; fix: add the row", kind)
		}
		lines = append(lines, fmt.Sprintf("- `%s`: %s", kind, spec.Definition))
	}
	return strings.Join(lines, "\n"), nil
}

// signalKindsContent renders the kind bullets. ValidateSignalKinds runs before
// generation, so reaching the error here means a caller skipped the gate.
func signalKindsContent() string {
	content, err := renderSignalKinds(canonicalSignalKinds, SignalKindSpecs)
	if err != nil {
		panic("codegen.signalKindsContent: " + err.Error())
	}
	return content
}

// SignalLineField is one field of the signal line, in the fixed order the
// registry states. Placeholder is the authored form; an example substitutes a
// value for the same field.
type SignalLineField struct {
	Field       string
	Placeholder string
}

// SignalLineFields is the one field-order registry. The template, the note
// example and the ledger example all render from it.
var SignalLineFields = []SignalLineField{
	{"kind", "<kind>"},
	{"phase", "<phase>"},
	{"skill", "<skill>"},
	{"entry", "<entry-ref>"},
	{"quote", "<short verbatim quote>"},
}

// signalLine is one JSON object. Field order is the struct order, which is
// SignalLineFields order, so a reader can parse and count later.
type signalLine struct {
	Kind  string `json:"kind"`
	Phase string `json:"phase"`
	Skill string `json:"skill"`
	Entry string `json:"entry"`
	Quote string `json:"quote"`
}

func marshalSignalLine(line signalLine) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(line); err != nil {
		panic("codegen.marshalSignalLine: " + err.Error())
	}
	return strings.TrimRight(buf.String(), "\n")
}

func signalLineFromValues(values map[string]string) signalLine {
	return signalLine{
		Kind:  values["kind"],
		Phase: values["phase"],
		Skill: values["skill"],
		Entry: values["entry"],
		Quote: values["quote"],
	}
}

// SignalLineTemplate is the authored JSON object. Both backends record this
// shape: one object, one line, fields in registry order.
func SignalLineTemplate() string {
	values := map[string]string{}
	for _, field := range SignalLineFields {
		values[field.Field] = field.Placeholder
	}
	return marshalSignalLine(signalLineFromValues(values))
}

// SignalLineExample is one filled JSON object. The adapter writes this shape;
// a test parses every appended line against it.
func SignalLineExample() string {
	return marshalSignalLine(signalLineFromValues(map[string]string{
		"kind":  "confusion",
		"phase": "elicit",
		"skill": "user-elicit",
		"entry": "<task URI>#end-vision",
		"quote": "what does arm mean here",
	}))
}

// signalPhaseNames is the registered phase list a signal may name. The
// terminal phase is not a place a signal can appear. The prose list and the
// adapter's runtime check both read this function.
func signalPhaseNames() []string {
	names := make([]string, 0, len(protocol.AllPhaseIds))
	for _, phase := range protocol.AllPhaseIds {
		if phase == protocol.PhaseComplete {
			continue
		}
		names = append(names, string(phase))
	}
	return names
}

// signalKindNames is the closed kind list in registry order. The adapter's
// runtime check reads this function, not a second list.
func signalKindNames() []string {
	names := make([]string, len(canonicalSignalKinds))
	for i, kind := range canonicalSignalKinds {
		names[i] = string(kind)
	}
	return names
}

// signalPhaseVocabulary is the registered phase list, rendered from the
// protocol registry.
func signalPhaseVocabulary() string {
	return strings.Join(signalPhaseNames(), ", ")
}

// feedbackLookupRule is the one home of how a second run finds the feedback
// task. The epoch start step and the adapter point at Signal Capture; they
// do not restate this sentence.
const feedbackLookupRule = "A second run reads the `feedback:` line and does not create another task."

// signalCaptureFragment builds the shared signal-capture fragment.
func signalCaptureFragment() SharedFragment {
	prose := ProseSection{
		Id:    string(FragSignalCapture),
		Title: "Signal Capture",
		Content: "Record the user's friction or approval when it appears, so a later " +
			"project-level skill can distill it. Capture is a side effect of work already " +
			"happening: it never prompts the user and never blocks a phase.",
		Subsections: []ProseSection{
			{
				Id:      "frag-signal-capture-kinds",
				Title:   "Closed kind list",
				Content: signalKindsContent(),
			},
			{
				Id:      "frag-signal-capture-task",
				Title:   "One feedback task per epoch",
				Content: signalTaskContent(),
			},
			{
				Id:      "frag-signal-capture-line",
				Title:   "Line format and recording",
				Content: signalLineContent(),
			},
		},
	}
	return SharedFragment{
		Id:    FragSignalCapture,
		Kind:  FragmentKindProse,
		Prose: &prose,
	}
}

// signalTaskContent names the one creator, the backend gate, and the one
// lookup rule. The create command lives in the epoch start step, not here, so
// a consumer skill cannot create a second task.
func signalTaskContent() string {
	return strings.Join([]string{
		"On the pasture backend, the epoch creates one feedback task at epoch start and writes `feedback: <task URI>` into `.agents.local/pasture/EPOCH.md`. " + feedbackLookupRule + " On the ledger backend, create nothing and record nothing: there is no feedback task and no `feedback:` line. No other skill creates a feedback task. If the pasture line is absent, the signal operation fails and records nothing; do not create a task to fill the gap.",
	}, "\n")
}

// signalLineContent is the one call a skill body ships. Both backend recipes
// live on the signal operation, not in this section.
func signalLineContent() string {
	return strings.Join([]string{
		"Write one JSON object per signal. The fields, in this order, are kind, phase, skill, entry and quote.",
		"",
		"~~~",
		SignalLineTemplate(),
		"~~~",
		"",
		"- `kind`: one of the registered kinds above, exactly.",
		"- `phase`: one of " + signalPhaseVocabulary() + ".",
		"- `skill`: the skill that recorded it, named exactly as the skill directory.",
		"- `entry`: the record that holds the interaction, such as a task URI plus the question or section.",
		"- `quote`: a short verbatim fragment, max 20 words. Never paraphrase or tidy it.",
		"",
		"Record the signal with one call. Do not name a backend command in this section.",
		"",
		"~~~bash",
		"[[rec:signal]] --kind confusion --phase elicit --skill user-elicit --entry '<task URI>#end-vision' --quote 'what does arm mean here'",
		"~~~",
		"",
		"The adapter records that JSON object. An empty feedback id fails in the adapter and records nothing. Write the call in the same turn as the work. Do not ask permission to record. Do not wait for the write before you continue the phase.",
		"",
		"If the write fails, follow the tracker write-failure rule: retry, keep the recorded backend, and surface the pending line at the next gate. A failed record never blocks the phase and never prompts the user.",
	}, "\n")
}

// signalBackendRecipeMarkers are executable backend recipes. A skill body that
// carries one of them has named a recipe the tracker clause forbids.
var signalBackendRecipeMarkers = []string{
	"Pasture target",
	"Ledger target",
	"printf ",
	"signals.jsonl",
	"pasture task",
	"backend: pasture",
	"backend: ledger",
	"sed -n",
	">> ",
}

// trackerClauseForbidsBackendRecipe is the sentence the tracker adapter uses
// to forbid a skill body from naming a backend recipe.
const trackerClauseForbidsBackendRecipe = "no skill body names a backend recipe"

func proseTreeText(section ProseSection) string {
	parts := []string{section.Title, section.Content}
	for _, sub := range section.Subsections {
		parts = append(parts, proseTreeText(sub))
	}
	return strings.Join(parts, "\n")
}

// SignalCaptureSection extracts one rendered Signal Capture section. Recipes
// in a later or earlier section, including the tracker adapter, are not part
// of it.
func SignalCaptureSection(rendered string) (string, bool) {
	const heading = "## Signal Capture"
	start := strings.Index(rendered, heading)
	if start < 0 {
		return "", false
	}
	rest := rendered[start+len(heading):]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		return rendered[start : start+len(heading)+next], true
	}
	return rendered[start:], true
}

// findSignalBackendRecipes reports backend recipes inside a Signal Capture
// section while the tracker clause still forbids them. An amended clause that
// no longer forbids recipes produces no finding.
func findSignalBackendRecipes(section, clause string) []string {
	if !strings.Contains(clause, trackerClauseForbidsBackendRecipe) {
		return nil
	}
	var found []string
	for _, marker := range signalBackendRecipeMarkers {
		if strings.Contains(section, marker) {
			found = append(found, marker)
		}
	}
	return found
}

// ValidateSignalCaptureHasNoBackendRecipe fails generation when a rendered
// Signal Capture section names a backend recipe while the tracker clause
// forbids one.
func ValidateSignalCaptureHasNoBackendRecipe() error {
	clause := buildTrackerAdapterContent()
	if !strings.Contains(clause, trackerClauseForbidsBackendRecipe) {
		return nil
	}
	fragment := signalCaptureFragment()
	rendered, err := ResolveBodyMarkers(proseTreeText(*fragment.Prose), HarnessClaudeCode)
	if err != nil {
		return fmt.Errorf("codegen.ValidateSignalCaptureHasNoBackendRecipe: cannot render the signal section — why: %s; where: internal/codegen/specs_data_signals.go signalCaptureFragment; fix: close every marker and use a registered payload", err.Error())
	}
	section := "## Signal Capture\n" + rendered
	found := findSignalBackendRecipes(section, clause)
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("codegen.ValidateSignalCaptureHasNoBackendRecipe: the rendered Signal Capture section names a backend recipe (%s) — why: the tracker clause forbids a skill body from naming a backend recipe, and TrackerOpSignal already declares both recipes; where: internal/codegen/specs_data_signals.go signalLineContent; fix: ship one signal call through the tracker entry point and leave the recipes on TrackerOpSignal", strings.Join(found, ", "))
}

// QuestionRecordField is one field a recorded question carries.
type QuestionRecordField struct {
	Name   string
	Phrase string
}

// QuestionRecordFields is the one declaration of what a recorded question
// carries. The guidance renders it, so a dataset can be derived from what the
// skills capture without changing the skills.
var QuestionRecordFields = []QuestionRecordField{
	{Name: "header", Phrase: "the header"},
	{Name: "question", Phrase: "the exact question text"},
	{Name: "options", Phrase: "every option label and description"},
	{Name: "multi", Phrase: "the multi-select flag"},
	{Name: "preview", Phrase: "the preview when the harness showed one"},
	{Name: "response", Phrase: "the user's verbatim response"},
}

// questionRecordSentence renders the recorded-field list from the registry.
func questionRecordSentence() string {
	phrases := make([]string, 0, len(QuestionRecordFields))
	for _, field := range QuestionRecordFields {
		phrases = append(phrases, field.Phrase)
	}
	return "Each question must\ninclude " + strings.Join(phrases[:len(phrases)-1], ", ") + ", and " + phrases[len(phrases)-1] + "."
}

// questionRecordFieldsPhrase renders the field phrases as a comma list, so a
// constraint that names them reads from the registry rather than copying it.
func questionRecordFieldsPhrase() string {
	phrases := make([]string, 0, len(QuestionRecordFields))
	for _, field := range QuestionRecordFields {
		phrases = append(phrases, field.Phrase)
	}
	return strings.Join(phrases, ", ")
}
