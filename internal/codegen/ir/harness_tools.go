package ir

// HarnessTool is the closed set of native file and shell tools a protocol role
// may hold. It is deliberately separate from OperationKind: an operation names
// an orchestration intent (invoke a skill, delegate an assignment), while a
// harness tool names a host capability the agent uses directly on files or the
// shell.
//
// The spelling here is neutral. The native name for one harness comes from that
// harness's lowering table in internal/runtime/profiles.go, so a tool name has
// exactly one home (tenet 4). A role spec stores HarnessTool values; a raw
// string tool name is not assignable to RoleSpec.Tools without an explicit
// conversion.
type HarnessTool string

const (
	HarnessToolRead     HarnessTool = "read"
	HarnessToolGlob     HarnessTool = "glob"
	HarnessToolGrep     HarnessTool = "grep"
	HarnessToolBash     HarnessTool = "bash"
	HarnessToolEdit     HarnessTool = "edit"
	HarnessToolWrite    HarnessTool = "write"
	HarnessToolTask     HarnessTool = "task"
	HarnessToolWebFetch HarnessTool = "webfetch"
)

var canonicalHarnessTools = [...]HarnessTool{
	HarnessToolRead,
	HarnessToolGlob,
	HarnessToolGrep,
	HarnessToolBash,
	HarnessToolEdit,
	HarnessToolWrite,
	HarnessToolTask,
	HarnessToolWebFetch,
}

// AllHarnessTools returns a fresh defensive copy of the closed harness-tool
// set in canonical order. It is a function, not an exported mutable slice, so
// one caller cannot corrupt every later reader for the process lifetime.
func AllHarnessTools() []HarnessTool {
	return append([]HarnessTool(nil), canonicalHarnessTools[:]...)
}

// IsValid reports whether the value is a registered member of the closed set.
func (t HarnessTool) IsValid() bool {
	for _, candidate := range canonicalHarnessTools {
		if candidate == t {
			return true
		}
	}
	return false
}
