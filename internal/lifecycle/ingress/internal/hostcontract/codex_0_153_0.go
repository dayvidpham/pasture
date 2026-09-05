package hostcontract

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// Codex native identity field IDs. Only the correlation fields consumed by the
// two authentically observed Codex 0.153.0 events are declared here; all other
// payload content is preserved byte-exact in the retained evidence body rather
// than being lifted into typed native fields.
const (
	fCodexSessionID model.NativeFieldID = 5001 + iota
	fCodexTurnID
	fCodexToolUseID
)

var codexFields = []Field{
	{fCodexSessionID, "FieldCodexSessionID", "session_id"},
	{fCodexTurnID, "FieldCodexTurnID", "turn_id"},
	{fCodexToolUseID, "FieldCodexToolUseID", "tool_use_id"},
}

// codexReaderRefusalCost is the blast radius both arms of codexFailureReader
// report, and it is measured from the import graph rather than assumed.
//
// WHAT MAY BE CLAIMED HERE. This package has exactly one caller outside its own
// tests: the generator internal/lifecycle/ingress/cmd/hostcontractgen. No
// pasture binary links it (it is in the dependency set of none of the five
// commands under cmd/). The Codex hook path admits with the COMMITTED
// internal/lifecycle/registration/codex_0_153_0.gen.go, which is plain Go data
// and calls nothing here. So a refusal here stops REGENERATION and never
// admission, and the message must say the smaller, true thing: a maintainer who
// is told the product is down looks for an outage that is not there.
const codexReaderRefusalCost = "WHAT IT COSTS: code generation stops here and admission does not. " +
	"The generator internal/lifecycle/ingress/cmd/hostcontractgen is the only caller of this catalog outside this package's own tests, " +
	"and it renders every harness in one pass, so make generate writes no file for any harness and the committed generated files keep the bytes they have. " +
	"A running pasture is unaffected: no pasture binary links this package, and a Codex hook is still admitted from the committed internal/lifecycle/registration/codex_0_153_0.gen.go. " +
	"HOW TO REPAIR: "

// codexFailureReader returns the failure mode of one Codex row, by native name,
// as the runtime Codex profile holds it.
//
// WHY THE CATALOG DOES NOT DECLARE THIS FIELD ITSELF. The runtime profile
// applies the failure-evidence rule: a row keeps a blocking exit code only
// while it cites where that behavior was read from the host, and a row with no
// citation runs as report-and-continue. A failure mode written in two places
// can be demoted in one of them and kept in the other, and the reader of this
// file then learns a blocking claim that no code path holds. Reading the field
// makes that state unreachable: a later citation that promotes a row, and a
// later demotion that lowers one, move both artefacts together.
//
// WHAT THE READ COVERS. One field of one row. It does not read the profile's
// gate-or-observation semantic, its mutation mode, its stop-loop policy, its
// ordering or its identities, so a row that the two artefacts describe
// differently on any other axis keeps both descriptions.
func codexFailureReader() func(name string) pastureruntime.FailureMode {
	contract := pastureruntime.Codex0_153_0Lifecycle()
	events := pastureruntime.CodexLifecycleEvents()
	modes := make(map[string]pastureruntime.FailureMode, len(events))
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		if err != nil {
			panic(fmt.Sprintf(
				"the Codex host contract cannot be built: the runtime Codex profile holds no mapping for its own event %d (%v). "+
					"This happened in codexFailureReader in internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go, "+
					"while reading the failure mode of every Codex row from internal/runtime/lifecycle_profiles_codex.go. "+
					codexReaderRefusalCost+
					"Add the missing mapping to codexLifecycleMappings in internal/runtime/lifecycle_profiles_codex.go, then run make generate: %v",
				uint8(event), event, err))
		}
		modes[mapping.NativeName()] = mapping.Failure()
	}
	return func(name string) pastureruntime.FailureMode {
		mode, present := modes[name]
		if !present {
			panic(fmt.Sprintf(
				"the Codex host contract cannot be built: this catalog declares the event %q, and the runtime Codex profile declares no row of that native name, "+
					"so there is no failure mode to read for it. "+
					"This happened in codexFailureReader in internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go, while building the Codex 0.153.0 contract. "+
					codexReaderRefusalCost+
					"Add the row to codexLifecycleMappings in internal/runtime/lifecycle_profiles_codex.go, or spell the name here as the profile spells it, then run make generate",
				name))
		}
		return mode
	}
}

// Codex0_153_0 is the source-reprofiled selected-contract for Codex 0.153.0.
//
// It mirrors the self-contained Claude host-contract shape (a closed native
// catalog defined in source) rather than the runtime-derived OpenCode shape.
//
// Only SessionStart and PreToolUse carry native identities and are eligible for
// authentic-evidence proof; the remaining closed-catalog entries are
// source-derived metadata and declare no identities, exactly as the OpenCode
// catalog leaves unproven events identity-free. SessionStart is a
// configured-hook ingress smoke observation and is never treated as
// semantically identical to the OpenCode session.created aggregate.
//
// THE FAILURE MODE IS READ, NOT DECLARED HERE. The failure mode of every one of
// the 12 rows is read from the runtime Codex profile
// (internal/runtime/lifecycle_profiles_codex.go) by codexFailureReader above,
// so the two artefacts disagree on the failure mode of 0 of the 12 rows. Before
// that read existed, this source declared a blocking exit code on each of its
// gate rows while the profile, which every behavior obeys, ran all of them as
// report-and-continue for want of a host citation. A reader of this file
// learned a refusal the product could not perform.
//
// THE COINCIDENCE THIS DOES NOT HIDE. PostCompact is an observation here and a
// gate in the runtime profile. The two land on the same failure arm only
// because the profile's gate cites no evidence and is demoted to the arm an
// observation already carries. The read above copies the arm and never the
// semantic, so a citation for PostCompact would move both arms together and
// would leave the disagreement about what the event IS exactly where it is,
// until a reader of the host emission site settles it deliberately.
//
// Of the 12 registered Codex events, 10 have no authentic capture, and this
// source declares no identities for any of those 10.
//
// The diverging metadata never reaches ingest, and the mechanism that stops it
// is the ACTIVATION TABLE, which withholds every event without a capture proof
// before Bind is reached. It is not the frontend: the Codex frontend mapping
// (internal/lifecycle/frontend/codex) is complete over all 12 registered
// events. The runtime profile is the authority for non-ingress event semantics,
// and it is the one to believe when the two disagree.
//
// The counts above are read back from the tree by
// internal/lifecycle/registration/failure_divergence_test.go, so a new
// registration or a new capture turns that test RED instead of leaving a stale
// number here.
//
// The identities are the field still written twice. Give a row its identities
// here once an authentic capture shows what the host writes on the wire for it.
func Codex0_153_0() Contract {
	failure := codexFailureReader()
	// observe builds a report-and-continue catalog event with no declared
	// identities (source-derived metadata only). Its failure mode is read.
	observe := func(kind model.ContractEventKind, symbol, name string) Event {
		return Event{
			Kind: kind, Symbol: symbol, Name: name,
			Blocking: NonBlocking, Mutation: MutationNone,
			Failure: failure(name), StopLoop: StopLoopNotApplicable,
		}
	}
	// gate builds a blocking catalog event with no declared identities
	// (source-derived metadata only). Its failure mode is read, so a gate whose
	// blocking exit code cites nothing carries the demoted arm here too.
	gate := func(kind model.ContractEventKind, symbol, name string, mutation MutationMode, stop StopLoopPolicy) Event {
		return Event{
			Kind: kind, Symbol: symbol, Name: name,
			Blocking: Blocking, Mutation: mutation,
			Failure: failure(name), StopLoop: stop,
		}
	}

	sessionStart := observe(1, "EventCodexSessionStart", "SessionStart")
	sessionStart.Fields = []model.NativeFieldID{fCodexSessionID}
	sessionStart.Identities = []Identity{
		{Field: fCodexSessionID, Binding: model.BindingSession, Required: true},
	}

	preToolUse := gate(3, "EventCodexPreToolUse", "PreToolUse", MutationInput, StopLoopNotApplicable)
	preToolUse.Fields = []model.NativeFieldID{fCodexSessionID, fCodexTurnID, fCodexToolUseID}
	preToolUse.Identities = []Identity{
		{Field: fCodexSessionID, Binding: model.BindingSession, Required: true},
		{Field: fCodexTurnID, Binding: model.BindingTurn, Required: true},
		{Field: fCodexToolUseID, Binding: model.BindingToolCall, Required: true},
	}

	// SessionEnd was emitted by the host before this catalogue held it, and BOTH
	// pinned versions emit it from the SAME function, run_session_end_hooks:
	// codex-rs/core/src/hook_runtime.rs:369 at rust-v0.146.0 (root session only at
	// :378-382) and :455 at rust-v0.153.0 (root session only at :464-468). The
	// emitter serializes session_id, transcript_path, cwd, hook_event_name and
	// reason (codex-rs/hooks/src/events/session_end.rs:64-68 at rust-v0.153.0):
	// that is the cited payload SHAPE, not a declared identity. Like every other
	// unproven Codex row, this row declares no identity and no payload field until
	// an authentic capture proves what the host writes on the wire.
	sessionEnd := observe(11, "EventCodexSessionEnd", "SessionEnd")
	// Interrupt arrived BETWEEN the two pinned versions. At rust-v0.146.0 there is
	// no codex-rs/hooks/src/events/interrupt.rs and no interrupt emitter in
	// codex-rs/core/src/hook_runtime.rs; at rust-v0.153.0 both exist, and the
	// emitter is run_turn_interrupt_hooks at :486. It serializes session_id,
	// turn_id, transcript_path, cwd, hook_event_name, model and permission_mode
	// (codex-rs/hooks/src/events/interrupt.rs:76-82 at rust-v0.153.0): the cited
	// shape, not declared identities, for the same reason as SessionEnd above.
	interrupt := observe(12, "EventCodexInterrupt", "Interrupt")

	// WHERE EACH ROW WAS READ IN THE HOST, at tag rust-v0.153.0 of
	// github.com/openai/codex. The first path is the emission site, which is the
	// function that builds the hook request; the second is the payload the host
	// serializes on the wire, which is the cited SHAPE and never a declared
	// identity here.
	//
	//   SessionStart      core/src/hook_runtime.rs:124  hooks/src/events/session_start.rs:130
	//   UserPromptSubmit  core/src/hook_runtime.rs:661  hooks/src/events/user_prompt_submit.rs:83
	//   PreToolUse        core/src/hook_runtime.rs:184  hooks/src/events/pre_tool_use.rs:177
	//   PermissionRequest core/src/hook_runtime.rs:246  hooks/src/events/permission_request.rs:171
	//   PostToolUse       core/src/hook_runtime.rs:285  hooks/src/events/post_tool_use.rs:152
	//   PreCompact        core/src/hook_runtime.rs:528  hooks/src/events/compact.rs:126
	//   PostCompact       core/src/hook_runtime.rs:565  hooks/src/events/compact.rs:207
	//   SubagentStart     core/src/hook_runtime.rs:124  hooks/src/events/session_start.rs:156
	//   SubagentStop      core/src/hook_runtime.rs:376  hooks/src/events/stop.rs:183
	//   Stop              core/src/hook_runtime.rs:376  hooks/src/events/stop.rs:152
	//   SessionEnd        core/src/hook_runtime.rs:455  hooks/src/events/session_end.rs:63
	//   Interrupt         core/src/hook_runtime.rs:486  hooks/src/events/interrupt.rs:75
	//
	// SessionStart and SubagentStart share one emission site: the host dispatches
	// a thread-spawn subagent start through the pending session-start path. Stop
	// and SubagentStop share one for the same reason, by the stop-hook target.
	events := []Event{
		sessionStart,
		gate(2, "EventCodexUserPromptSubmit", "UserPromptSubmit", MutationNone, StopLoopNotApplicable),
		preToolUse,
		gate(4, "EventCodexPermissionRequest", "PermissionRequest", MutationNone, StopLoopNotApplicable),
		gate(5, "EventCodexPostToolUse", "PostToolUse", MutationNone, StopLoopNotApplicable),
		gate(6, "EventCodexPreCompact", "PreCompact", MutationNone, StopLoopNotApplicable),
		observe(7, "EventCodexPostCompact", "PostCompact"),
		observe(8, "EventCodexSubagentStart", "SubagentStart"),
		gate(9, "EventCodexSubagentStop", "SubagentStop", MutationNone, StopLoopConsultWhenInactive),
		gate(10, "EventCodexStop", "Stop", MutationNone, StopLoopConsultWhenInactive),
		sessionEnd,
		interrupt,
	}
	return Contract{Version: "0.153.0", Fields: append([]Field(nil), codexFields...), Events: events}
}
