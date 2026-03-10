package hooks

import (
	"context"
)

// ─── Module-level singleton ───────────────────────────────────────────────────

// hooksMgr is the process-level hooks Manager singleton.
//
// Nil until InitHooksManager is called. All DispatchHook calls are no-ops when
// nil, so hooks remain optional — the protocol continues to function without them.
var hooksMgr *Manager

// InitHooksManager injects the hooks Manager singleton for this worker process.
//
// Must be called once before the Temporal worker starts, after all handlers
// have been registered on mgr. Passing nil resets the singleton (useful in
// tests to isolate state between test cases).
//
// Thread safety: Safe to call multiple times (e.g. between test cases).
// Must NOT be called concurrently with DispatchHook.
func InitHooksManager(mgr *Manager) {
	hooksMgr = mgr
}

// GetManager returns the module-level Manager singleton.
//
// Returns nil if InitHooksManager has not been called or was called with nil.
// Callers that need to check whether hooks are active should compare the result
// against nil before calling methods on it.
func GetManager() *Manager {
	return hooksMgr
}

// DispatchHook is a Temporal activity that dispatches a HookPayload to all
// registered handlers via the singleton Manager.
//
// Temporal activity signature: (ctx context.Context, payload HookPayload) error.
// Register with the Temporal worker via RegisterWorkflows before starting.
//
// Behaviour:
//   - If the singleton has not been initialized (hooksMgr == nil), returns nil
//     immediately. Hooks are optional — their absence must not fail workflows.
//   - Otherwise, delegates to hooksMgr.Dispatch(ctx, payload) and returns any
//     combined handler errors so the caller can log them.
//
// Callers in activities and workflows should treat a non-nil return as a
// best-effort log signal, not as a hard failure.
func DispatchHook(ctx context.Context, payload HookPayload) error {
	if hooksMgr == nil {
		return nil
	}
	return hooksMgr.Dispatch(ctx, payload)
}
