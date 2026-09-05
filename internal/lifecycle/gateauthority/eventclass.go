package gateauthority

// eventclass.go answers one question for the whole product: given a registered
// native lifecycle event of a known harness, WHAT CLASS OF ACTION is it about?
//
// It is the ONE source of that fact. The decision reads the class of the fired
// event, the handler obtains the class to build its input, and the reachability
// column below asks which events can reach a denied cell. A second mapping
// anywhere would be a second answer to one question.
//
// The table is TOTAL: every registered event of every harness has a class. A
// registered event with no class is a fault this package refuses at process
// start, naming the harness and the event, so a host event added to a manifest
// cannot reach a gate unclassified.
//
// WHY THE REACHABILITY COLUMN READS ONLY THE REGISTRATION MANIFESTS. Whether an
// event is ENABLED today lives in the activation package, and that package
// reaches pasture's task store, which imports THIS package. Reading it here
// would close the very cycle this package exists to break. So the published
// column states REGISTERED reachability, which is a property of the manifests
// alone, and the test alongside cross-checks which of those events are enabled
// today. Both words reach the reader; neither is presented as the other.

import (
	"fmt"
	"sort"
	"sync"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// harnessManifests returns the registration manifest of every harness pasture
// supports, in canonical order. It is the population every derivation in this
// file walks.
func harnessManifests() []registration.Manifest {
	return []registration.Manifest{
		registration.ClaudeCode2_1_261(),
		registration.Codex0_153_0(),
		registration.OpenCode1_18_29(),
	}
}

// eventClasses is the whole mapping, keyed by harness and then by the native
// event name the manifest carries.
//
// Reading the table: an entry is the class of action the host is asking about,
// or ActionObservation when the host is reporting rather than asking. An
// observation that reports a COMPLETED action carries ActionObservation and not
// the class of the action it reports, because the decision exits on the
// semantic before the class is read; a post-hoc CONSULTATION the host does ask
// carries ActionPostHoc.
var eventClasses = map[ir.HarnessID]map[string]ActionClass{
	ir.HarnessClaudeCode: {
		// Session and setup: the host reports where it is, it asks nothing.
		"SessionStart":       ActionObservation,
		"Setup":              ActionObservation,
		"SessionEnd":         ActionObservation,
		"InstructionsLoaded": ActionObservation,
		"Notification":       ActionObservation,
		"MessageDisplay":     ActionObservation,
		"TeammateIdle":       ActionObservation,
		"FileChanged":        ActionObservation,
		"CwdChanged":         ActionObservation,
		"DirectoryAdded":     ActionObservation,
		"Elicitation":        ActionObservation,
		"ElicitationResult":  ActionObservation,
		"PreModelSwitch":     ActionObservation,
		"PostModelSwitch":    ActionObservation,
		"StopFailure":        ActionObservation,
		"PostCompact":        ActionObservation,
		"WorktreeRemove":     ActionObservation,
		"SubagentStart":      ActionObservation,
		"PostToolUse":        ActionObservation,
		"PostToolUseFailure": ActionObservation,
		"PermissionDenied":   ActionObservation,

		// The host asks.
		"UserPromptSubmit":    ActionPromptSubmit,
		"UserPromptExpansion": ActionPromptSubmit,
		"PreToolUse":          ActionToolUse,
		"PermissionRequest":   ActionPermission,
		"PostToolBatch":       ActionPostHoc,
		"Stop":                ActionStop,
		"SubagentStop":        ActionSubagent,
		"PreCompact":          ActionCompact,
		"ConfigChange":        ActionConfigChange,
		"WorktreeCreate":      ActionWorktree,
		"TaskCreated":         ActionTaskLifecycle,
		"TaskCompleted":       ActionTaskLifecycle,
	},
	ir.HarnessCodex: {
		"SessionStart":  ActionObservation,
		"SessionEnd":    ActionObservation,
		"SubagentStart": ActionObservation,
		"Interrupt":     ActionObservation,

		"UserPromptSubmit":  ActionPromptSubmit,
		"PreToolUse":        ActionToolUse,
		"PermissionRequest": ActionPermission,
		"PostToolUse":       ActionPostHoc,
		"PreCompact":        ActionCompact,
		"PostCompact":       ActionPostHoc,
		"SubagentStop":      ActionSubagent,
		"Stop":              ActionStop,
	},
	ir.HarnessOpenCode: {
		// OpenCode publishes a wide event surface, and almost all of it is the
		// server telling a plugin what happened.
		"command.executed":                     ActionObservation,
		"file.edited":                          ActionObservation,
		"file.watcher.updated":                 ActionObservation,
		"installation.updated":                 ActionObservation,
		"installation.update-available":        ActionObservation,
		"lsp.client.diagnostics":               ActionObservation,
		"lsp.updated":                          ActionObservation,
		"message.updated":                      ActionObservation,
		"message.removed":                      ActionObservation,
		"message.part.updated":                 ActionObservation,
		"message.part.removed":                 ActionObservation,
		"permission.updated":                   ActionObservation,
		"permission.replied":                   ActionObservation,
		"server.connected":                     ActionObservation,
		"server.instance.disposed":             ActionObservation,
		"session.created":                      ActionObservation,
		"session.updated":                      ActionObservation,
		"session.deleted":                      ActionObservation,
		"session.compacted":                    ActionObservation,
		"session.diff":                         ActionObservation,
		"session.error":                        ActionObservation,
		"session.idle":                         ActionObservation,
		"session.status":                       ActionObservation,
		"todo.updated":                         ActionObservation,
		"tui.prompt.append":                    ActionObservation,
		"tui.command.execute":                  ActionObservation,
		"tui.toast.show":                       ActionObservation,
		"pty.created":                          ActionObservation,
		"pty.updated":                          ActionObservation,
		"pty.exited":                           ActionObservation,
		"pty.deleted":                          ActionObservation,
		"vcs.branch.updated":                   ActionObservation,
		"chat.message":                         ActionObservation,
		"chat.params":                          ActionObservation,
		"chat.headers":                         ActionObservation,
		"shell.env":                            ActionObservation,
		"experimental.chat.messages.transform": ActionObservation,
		"experimental.chat.system.transform":   ActionObservation,
		"experimental.provider.small_model":    ActionObservation,
		"experimental.session.compacting":      ActionObservation,
		"experimental.compaction.autocontinue": ActionObservation,
		"experimental.text.complete":           ActionObservation,
		"tool.definition":                      ActionObservation,

		// The server asks.
		"permission.ask":         ActionPermission,
		"command.execute.before": ActionToolUse,
		"tool.execute.before":    ActionToolUse,
		"tool.execute.after":     ActionPostHoc,
	}, //nolint:gofmt // alignment is gofmt's
}

func init() {
	if err := checkEventClassesAreTotal(); err != nil {
		panic("gateauthority: " + err.Error())
	}
}

// checkEventClassesAreTotal walks every registered event of every harness and
// refuses any event with no class or with the invalid Unset class. It is called
// at process start, so a manifest that grows an event pasture has not
// classified stops the binary instead of reaching a gate.
//
// It also refuses an entry for an event NO manifest registers, because such an
// entry is a class for something that cannot happen, and it would sit in the
// reachability column as a rule the host can never apply.
func checkEventClassesAreTotal() error {
	manifests := harnessManifests()
	if len(manifests) == 0 {
		return fmt.Errorf("the harness manifest list is empty, so no event was classified and the check proves nothing")
	}

	var unclassified []string
	registered := map[ir.HarnessID]map[string]bool{}
	events := 0
	for _, manifest := range manifests {
		names := map[string]bool{}
		for _, event := range manifest.Entries() {
			events++
			names[event.NativeName] = true
			class, ok := eventClasses[manifest.Harness][event.NativeName]
			if !ok || !class.IsValid() {
				unclassified = append(unclassified, fmt.Sprintf("%s %s", manifest.Harness, event.NativeName))
			}
		}
		registered[manifest.Harness] = names
	}
	if events == 0 {
		return fmt.Errorf("the harness manifests registered no event at all, so the class check walked nothing")
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		return fmt.Errorf("%d registered event(s) carry no action class: %v; every registered event needs one, because the gate reads the class of the event that fired", len(unclassified), unclassified)
	}

	var unknown []string
	for harness, byName := range eventClasses {
		for name := range byName {
			if !registered[harness][name] {
				unknown = append(unknown, fmt.Sprintf("%s %s", harness, name))
			}
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%d action-class entr(y/ies) name an event no manifest registers: %v; remove the entry or register the event", len(unknown), unknown)
	}
	return nil
}

// ClassForEvent returns the action class of one registered native event. ok is
// false for an event this build does not register, so a name that arrived from
// outside the manifests is refused rather than read as some class.
func ClassForEvent(harness ir.HarnessID, nativeName string) (ActionClass, bool) {
	class, ok := eventClasses[harness][nativeName]
	if !ok || !class.IsValid() {
		return ActionUnset, false
	}
	return class, true
}

// ─── the reachability column ─────────────────────────────────────────────────

// TableName says which of the two legality tables a cell belongs to.
type TableName uint8

const (
	// TableUnset is the invalid zero.
	TableUnset TableName = iota
	TableRole
	TablePhase
	tableNameCeiling
)

// IsValid reports whether the table name is a real arm.
func (t TableName) IsValid() bool { return t > TableUnset && t < tableNameCeiling }

func (t TableName) String() string {
	switch t {
	case TableRole:
		return "role"
	case TablePhase:
		return "phase"
	case TableUnset:
		return "table-unset"
	default:
		return fmt.Sprintf("TableName(%d)", uint8(t))
	}
}

// HarnessReach is the registered reachability of one denied cell on one
// harness: the native events that can carry that class of action to a gate.
// An empty Events list means the harness cannot reach the cell.
type HarnessReach struct {
	Harness ir.HarnessID
	Events  []string
}

// CellReachability is the published reachability entry of one denied cell.
//
// Reachable is REGISTERED reachability: some harness registers an event of the
// cell's action class, that event can block, and it is not short-circuited by
// the observation rule or by the stop-loop guard. It does NOT say the event is
// enabled today; that is stated beside it in the report, and is checked by the
// test rather than read here.
type CellReachability struct {
	Table     TableName
	Role      AssignmentRole
	Phase     TaskPhase
	Action    ActionClass
	Harnesses []HarnessReach
	Reachable bool
}

// deniedCellReachability is built ON FIRST USE, not in an init function.
//
// The reason is worth writing down, because building it eagerly looked correct
// and was not: this file sorts before tables.go, so its init runs FIRST, and an
// eager build read the legality tables before they existed. It found no denied
// cell and published an EMPTY column, silently. A once-guarded build reads the
// tables when a caller asks, by which time every init has run.
var deniedCellReachabilityOnce = sync.OnceValue(buildDeniedCellReachability)

// DeniedCellReachability returns the published reachability entry of every
// denied cell of both tables, in a stable order. It is derived from the
// registration manifests and the tables themselves, so a new denial or a new
// host event changes it with no edit here.
func DeniedCellReachability() []CellReachability {
	built := deniedCellReachabilityOnce()
	out := make([]CellReachability, len(built))
	copy(out, built)
	return out
}

// reachingEvents returns the native events of one harness that can carry an
// action class to a gate: the event's class matches, it can block, and neither
// short-circuit applies.
//
// The two short-circuits are the ones the decision applies before it reads a
// table. An event the host does not block on is an observation, and the
// decision exits on the semantic. An event carrying the stop-loop guard is
// consulted only while the session is inactive, and the guard answers before a
// table is read.
func reachingEvents(manifest registration.Manifest, action ActionClass) []string {
	var names []string
	for _, event := range manifest.Entries() {
		class, ok := ClassForEvent(manifest.Harness, event.NativeName)
		if !ok || class != action {
			continue
		}
		if event.Blocking != registration.Blocking && event.Blocking != registration.ConditionallyBlocking {
			continue
		}
		if event.StopLoop != registration.StopLoopNotApplicable {
			continue
		}
		names = append(names, event.NativeName)
	}
	sort.Strings(names)
	return names
}

func buildDeniedCellReachability() []CellReachability {
	manifests := harnessManifests()
	var entries []CellReachability

	add := func(entry CellReachability) {
		for _, manifest := range manifests {
			entry.Harnesses = append(entry.Harnesses, HarnessReach{
				Harness: manifest.Harness,
				Events:  reachingEvents(manifest, entry.Action),
			})
		}
		for _, reach := range entry.Harnesses {
			if len(reach.Events) > 0 {
				entry.Reachable = true
				break
			}
		}
		entries = append(entries, entry)
	}

	for _, role := range AssignmentRoles() {
		for _, action := range ActionClasses() {
			if RoleVerdict(role, action) != VerdictDeny {
				continue
			}
			add(CellReachability{Table: TableRole, Role: role, Action: action})
		}
	}
	for _, phase := range TaskPhases() {
		for _, action := range ActionClasses() {
			if PhaseVerdict(phase, action) != VerdictDeny {
				continue
			}
			add(CellReachability{Table: TablePhase, Phase: phase, Action: action})
		}
	}
	return entries
}
