package audit

// ─── Module-Level Trail Singleton ─────────────────────────────────────────────

// trail is the module-level singleton injected before the durable runtime
// starts. Callers that cannot be handed a Trail directly delegate their
// persistence calls to this value. Access to this variable is not protected by
// a mutex because it is written exactly once at daemon startup (InitTrail) and
// is read-only thereafter.
var trail Trail

// InitTrail injects the Trail implementation used by the activity wrappers.
//
// Must be called once before the durable runtime starts. Passing nil resets
// the singleton (useful in tests to isolate state between test cases).
//
// This function is not concurrency-safe with workflow execution; call it
// during daemon startup, before any workflow can run.
func InitTrail(t Trail) {
	trail = t
}
