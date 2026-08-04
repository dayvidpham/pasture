package hostcontract

import "github.com/dayvidpham/pasture/internal/lifecycle/model"

const (
	openCodeSessionID model.NativeFieldID = 1001 + iota
	openCodeCallID
)

var openCodeFields = []Field{
	{openCodeSessionID, "FieldOpenCodeSessionID", "sessionID"},
	{openCodeCallID, "FieldOpenCodeCallID", "callID"},
}

// OpenCode1_18_10 is derived from the Event union and Hooks interface at
// OpenCode source revision 7902e04c3a67f7c69726bc955efb46e29214c797.
// The 32 catch-all entries and 15 named hooks are source metadata. They do not
// claim that an event was observed at runtime.
func OpenCode1_18_10() Contract {
	catchAll := []string{
		"command.executed", "file.edited", "file.watcher.updated", "installation.updated",
		"installation.update_available", "lsp.client.diagnostics", "lsp.updated", "message.updated",
		"message.removed", "message.part.updated", "message.part.removed", "permission.updated",
		"permission.replied", "server.connected", "server.instance.disposed", "session.created",
		"session.updated", "session.deleted", "session.compacted", "session.diff", "session.error",
		"session.idle", "session.status", "todo.updated", "tui.prompt.append", "tui.command.execute",
		"tui.toast.show", "pty.created", "pty.updated", "pty.exited", "pty.deleted", "vcs.branch.updated",
	}
	named := []string{
		"chat.message", "chat.params", "chat.headers", "permission.ask", "command.execute.before",
		"tool.execute.before", "shell.env", "tool.execute.after", "experimental.chat.messages.transform",
		"experimental.chat.system.transform", "experimental.provider.small_model",
		"experimental.session.compacting", "experimental.compaction.autocontinue",
		"experimental.text.complete", "tool.definition",
	}
	events := make([]Event, 0, len(catchAll)+len(named))
	for _, name := range catchAll {
		events = append(events, Event{
			Symbol: symbol("EventOpenCode", name), Name: name, Blocking: NonBlocking,
			Mutation: MutationNone, Failure: FailureReportAndContinue, StopLoop: StopLoopNotApplicable,
		})
	}
	for _, name := range named {
		events = append(events, Event{
			Symbol: symbol("EventOpenCode", name), Name: name, Blocking: Blocking,
			Mutation: MutationNone, Failure: FailureExitTwoBlocks, StopLoop: StopLoopNotApplicable,
		})
	}
	for i := range events {
		events[i].Kind = model.ContractEventKind(31 + i)
		switch events[i].Name {
		case "session.created":
			events[i].Fields = []model.NativeFieldID{openCodeSessionID}
			events[i].Identities = []Identity{{Field: openCodeSessionID, Binding: model.BindingSession, Required: true}}
		case "tool.execute.before":
			events[i].Fields = []model.NativeFieldID{openCodeSessionID, openCodeCallID}
			events[i].Identities = []Identity{
				{Field: openCodeSessionID, Binding: model.BindingSession, Required: true},
				{Field: openCodeCallID, Binding: model.BindingToolCall, Required: true},
			}
		}
	}
	return Contract{Version: "1.18.10", Fields: append([]Field(nil), openCodeFields...), Events: events}
}

func symbol(prefix, name string) string {
	out := []byte(prefix)
	upper := true
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			upper = true
			continue
		}
		c := name[i]
		if upper && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper = false
		out = append(out, c)
	}
	return string(out)
}
