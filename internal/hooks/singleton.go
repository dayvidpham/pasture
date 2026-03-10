// Package hooks — singleton.go previously held the module-level hooksMgr
// singleton, InitHooksManager, GetManager, and DispatchHook.
//
// These have been removed as part of the Activity struct DI refactor (Revision 3).
// Hook dispatch now flows through Activities.DispatchHook, which receives the
// hooks.Manager via constructor injection (Activities.HooksMgr field).
//
// This file is intentionally empty except for the package declaration.
package hooks
