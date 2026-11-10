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
	{"quote", `"<short verbatim quote>"`},
}

func renderSignalLine(value func(SignalLineField) string) string {
	parts := []string{"signal:"}
	for _, field := range SignalLineFields {
		parts = append(parts, field.Field+"="+value(field))
	}
	return strings.Join(parts, " ")
}

// SignalLineTemplate is the authored line shape shared by both backends.
func SignalLineTemplate() string {
	return renderSignalLine(func(field SignalLineField) string { return field.Placeholder })
}

// SignalLineExample is one filled line. The same text is the note body on
// pasture and the appended line on the ledger.
func SignalLineExample() string {
	values := map[string]string{
		"kind":  "confusion",
		"phase": "elicit",
		"skill": "user-elicit",
		"entry": "<task URI>#end-vision",
		"quote": `"what does arm mean here"`,
	}
	return renderSignalLine(func(field SignalLineField) string { return values[field.Field] })
}

// signalPhaseVocabulary is the registered phase list, rendered from the
// protocol registry. The terminal phase is not a place a signal can appear.
func signalPhaseVocabulary() string {
	names := make([]string, 0, len(protocol.AllPhaseIds))
	for _, phase := range protocol.AllPhaseIds {
		if phase == protocol.PhaseComplete {
			continue
		}
		names = append(names, string(phase))
	}
	return strings.Join(names, ", ")
}

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

// signalTaskContent pins one feedback task per epoch and how a second run finds
// it. Creation is epoch-only, so two skills cannot both create it.
func signalTaskContent() string {
	return strings.Join([]string{
		"Use one feedback task per epoch, and create it once. The epoch creates it at epoch start with:",
		"",
		"~~~bash",
		"FEEDBACK_ID_URI=$([[rec:create]] --kind task --title 'FEEDBACK: epoch signals' --phase unscoped)",
		"~~~",
		"",
		"Write `feedback: <task URI>` into `.agents.local/pasture/EPOCH.md` in the same step. That line is how a second run finds the task: every skill reads it before it records, and no skill creates a second task when the line is present. If the line is absent, treat the epoch as one that started before this rule: create the task once with exactly this title, write the line, then continue. Never create a second feedback task in the same epoch.",
		"",
		"This subsection applies to the pasture backend. A ledger epoch has no feedback task: use the ledger recording target below.",
	}, "\n")
}

// signalLineContent defines the stable line and the two recording targets. The
// line is identical on both backends; only its destination differs.
func signalLineContent() string {
	return strings.Join([]string{
		"Write one line per signal. The same fields and order apply on both backends:",
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
		"Recording target, by backend:",
		"",
		"- `backend: pasture`: one note on the feedback task that the `feedback:` line names. The note body is the whole line and nothing else. Never run the note with an empty id: an empty id fails with `need_id` and records nothing.",
		"- `backend: ledger`: no task and no note. Append the line to `.agents.local/pasture/signals.jsonl`, one line per signal.",
		"",
		"Pasture target:",
		"",
		"~~~bash",
		"FEEDBACK_ID_URI=$(sed -n 's/^feedback: //p' .agents.local/pasture/EPOCH.md | tail -1)",
		"[[rec:note]] \"$FEEDBACK_ID_URI\" --author pasture-system/00000000-0000-0000-0000-000000000000 --body '" + SignalLineExample() + "'",
		"~~~",
		"",
		"Ledger target:",
		"",
		"~~~bash",
		"printf '%s\\n' '" + SignalLineExample() + "' >> .agents.local/pasture/signals.jsonl",
		"~~~",
		"",
		"The recorded text is identical on both backends. Only the destination differs: a note body on pasture, one appended line on the ledger. Write it in the same turn as the work. Do not ask permission to record. Do not wait for the write before you continue the phase.",
		"",
		"If the write fails, follow the tracker write-failure rule: retry, keep the recorded backend, and surface the pending line at the next gate. A failed record never blocks the phase and never prompts the user.",
	}, "\n")
}
