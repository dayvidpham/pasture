package audit

// ─── Module-Level Trail Singleton ─────────────────────────────────────────────

// trail is the module-level singleton injected before the Temporal worker starts.
// Activities delegate all persistence calls to this value. Access to this
// variable is not protected by a mutex because it is written exactly once at
// worker startup (InitTrail) and read-only thereafter — the same pattern used
// in the Python aura-protocol reference implementation.
var trail Trail

// InitTrail injects the Trail implementation used by the activity wrappers.
//
// Must be called once before the Temporal worker starts. Passing nil resets
// the singleton (useful in tests to isolate state between test cases).
//
// This function is not concurrency-safe with activity execution; call it
// during worker startup, before any activities can run.
func InitTrail(t Trail) {
	trail = t
}
