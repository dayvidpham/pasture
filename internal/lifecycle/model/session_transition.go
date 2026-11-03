package model

// SessionTransition is validated host evidence, independent of binding,
// actor attribution and authority. Zero means no prompt/end/resume evidence.
type SessionTransition uint8

const (
	SessionTransitionNone SessionTransition = iota
	SessionTransitionPrompt
	SessionTransitionEnd
	SessionTransitionResume
)

// SessionTransitionResumeToken is the ONE occurrence.v3 wire token for a proven
// resume. Prompt and end are derived from the admitted event coordinate and are
// not persisted as a member.
const SessionTransitionResumeToken = "resume"
