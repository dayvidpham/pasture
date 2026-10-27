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
