// The one authored home of the signal-capture component for the epoch,
// architect, supervisor, user-elicit, and user-uat skills.
//
// Capture records the user's friction or approval so a later project-level
// skill can distill it. It is a side effect of work already happening: it never
// prompts the user and never blocks a phase. One feedback task per epoch holds
// one line per signal, with a fixed field order and a closed kind list.
package codegen

import "strings"

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

// signalKindsContent is the closed kind list. The five kinds are exactly the
// ones the requirement names.
func signalKindsContent() string {
	return strings.Join([]string{
		"The kind is a closed list. Use exactly one of:",
		"",
		"- `frustration`: the user shows annoyance, repeats a complaint, or rejects the interaction itself.",
		"- `positive-signal`: the user endorses a direction or a result before a gate asks.",
		"- `satisfaction`: the user accepts a gate and says the result is what they wanted.",
		"- `confusion`: the user asks what a term means, misreads a question, or answers a different question.",
		"- `correction`: the user corrects a fact, a requirement, or an earlier answer.",
	}, "\n")
}

// signalTaskContent pins one feedback task per epoch and its location in the
// active backend.
func signalTaskContent() string {
	return strings.Join([]string{
		"Use one feedback task per epoch. The epoch skill creates it once:",
		"",
		"~~~bash",
		"FEEDBACK_ID_URI=$([[rec:create]] --kind task --title 'FEEDBACK: epoch signals' --phase unscoped)",
		"~~~",
		"",
		"Then write `feedback: <task URI>` into `.agents.local/pasture/EPOCH.md`. Every skill reads that line before it records a signal. If the line is absent, create the task, write the line, and continue. Never create a second feedback task in the same epoch.",
		"",
		"On the ledger backend there is no task. Append the same line to `.agents.local/pasture/signals.jsonl` instead.",
	}, "\n")
}

// signalLineContent defines the stable line and how to record it without
// prompting or blocking.
func signalLineContent() string {
	return strings.Join([]string{
		"Write one line per signal. Field order is fixed: kind, phase, skill, entry, quote.",
		"",
		"~~~",
		"signal: kind=<kind> phase=<phase> skill=<skill> entry=<entry-ref> quote=\"<short verbatim quote>\"",
		"~~~",
		"",
		"- `kind`: one of the five kinds, exactly.",
		"- `phase`: the phase where the signal appeared.",
		"- `skill`: the skill that recorded it: epoch, architect, supervisor, user-elicit, or user-uat.",
		"- `entry`: the record that holds the interaction, such as a task URI plus the question or section.",
		"- `quote`: a short verbatim fragment, max 20 words. Never paraphrase or tidy it.",
		"",
		"Record a signal with one note on the feedback task:",
		"",
		"~~~bash",
		"FEEDBACK_ID_URI=$(sed -n 's/^feedback: //p' .agents.local/pasture/EPOCH.md | tail -1)",
		"[[rec:note]] \"$FEEDBACK_ID_URI\" --author pasture-system/00000000-0000-0000-0000-000000000000 --body 'signal: kind=confusion phase=elicit skill=user-elicit entry=<task URI>#end-vision quote=\"what does arm mean here\"'",
		"~~~",
		"",
		"The note body is the one line and nothing else. Write it in the same turn as the work. Do not ask permission to record. Do not wait for the write before you continue the phase.",
		"",
		"If the write fails, follow the tracker write-failure rule: retry, keep the recorded backend, and surface the pending line at the next gate. A failed record never blocks the phase and never prompts the user.",
	}, "\n")
}
