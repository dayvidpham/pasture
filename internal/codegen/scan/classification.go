package scan

// Classification is the closed, exhaustive disposition a maintainer assigns
// to one discovered Candidate through the checked-in ClassificationManifest.
//
// There is deliberately no "unclassified" member of this type: unclassified
// is not a value a candidate can be classified *as*, it is the absence of a
// matching classification-manifest entry (see Inventory.UnclassifiedCount
// and ClassifiedCandidate.Classified). Modeling it as a value would let a
// zero Classification silently mean "no fallback assigns meaning" one day
// and "explicitly reviewed, no meaning applies" the next — the two states
// this package must keep visibly distinct per pasture#47's acceptance
// criteria ("every candidate is explicitly classified or visibly
// unclassified; no fallback assigns meaning").
type Classification string

const (
	// ClassificationOrchestration is native team/assignment orchestration
	// syntax (e.g. TeamCreate, SendMessage, Skill invocation) that #38's
	// IR represents as a typed orchestration SemanticOperation.
	ClassificationOrchestration Classification = "orchestration"
	// ClassificationUserDecision is native user-interaction syntax (e.g.
	// AskUserQuestion) that #38's IR represents as RequestUserDecision.
	ClassificationUserDecision Classification = "user_decision"
	// ClassificationTaskEffect is a Beads/task-tracker invocation that #43
	// will represent as a typed task effect.
	ClassificationTaskEffect Classification = "task_effect"
	// ClassificationProcessEffect is a process/Git/filesystem invocation
	// that #46 will represent as a typed process/Git/filesystem effect.
	ClassificationProcessEffect Classification = "process_effect"
	// ClassificationPortableVerbatim is native-looking text that is
	// intentionally preserved exactly (e.g. a documentation example
	// showing legacy syntax on purpose) and maps to #38's Verbatim part.
	ClassificationPortableVerbatim Classification = "portable_verbatim"
	// ClassificationTargetLiteral is a reviewed, harness-bound raw escape
	// that maps to #38's exhaustive TargetLiteral part.
	ClassificationTargetLiteral Classification = "target_literal"
	// ClassificationNeutralFalsePositive is a pattern match that, on
	// review, carries no operational meaning at all (e.g. the construct
	// name mentioned in prose without being invoked).
	ClassificationNeutralFalsePositive Classification = "neutral_false_positive"
)

// canonicalClassifications is the single source of truth for the closed
// Classification sum. Classifications returns a defensive copy of it — this
// is the only enumeration accessor, mirroring ir.EnabledHarnessIDs's
// documented rationale: a package-level mutable slice would let one caller's
// mutation corrupt every later reader.
var canonicalClassifications = [...]Classification{
	ClassificationOrchestration,
	ClassificationUserDecision,
	ClassificationTaskEffect,
	ClassificationProcessEffect,
	ClassificationPortableVerbatim,
	ClassificationTargetLiteral,
	ClassificationNeutralFalsePositive,
}

// Classifications returns a fresh defensive copy of the closed, exhaustive
// classification set.
func Classifications() []Classification {
	return append([]Classification(nil), canonicalClassifications[:]...)
}

// IsValid reports whether c is one of the closed Classification values.
func (c Classification) IsValid() bool {
	for _, candidate := range canonicalClassifications {
		if c == candidate {
			return true
		}
	}
	return false
}

// OwnerDisposition is the closed, exhaustive whole-file disposition recorded
// in the checked-in OwnerManifest.
type OwnerDisposition string

const (
	// OwnerActive means the owner is parsed and scanned for candidates.
	OwnerActive OwnerDisposition = "active"
	// OwnerDead means the owner is discovered and reconciled (it still
	// must be byte-for-byte hashed and present in the manifest) but is
	// explicitly disposed as historically inactive, so it is not parsed
	// for candidates. A dead disposition always requires a non-empty
	// Reason (see OwnerEntry) — pasture#47 requires "explicit dead-owner
	// dispositions", not a silent skip.
	OwnerDead OwnerDisposition = "dead"
)

var canonicalOwnerDispositions = [...]OwnerDisposition{OwnerActive, OwnerDead}

// OwnerDispositions returns a fresh defensive copy of the closed, exhaustive
// owner-disposition set.
func OwnerDispositions() []OwnerDisposition {
	return append([]OwnerDisposition(nil), canonicalOwnerDispositions[:]...)
}

// IsValid reports whether d is one of the closed OwnerDisposition values.
func (d OwnerDisposition) IsValid() bool {
	return d == OwnerActive || d == OwnerDead
}

// PatternID identifies one closed, code-owned lexical pattern the scanner
// looks for inside every active owner's Goldmark AST. The registry is
// intentionally small and precise (exact call-like syntax, not every prose
// mention of a construct's name) — see patternRegistry in pattern.go for the
// exact regular expressions and internal/codegen/scan/manifest for the real
// classification of every match this registry currently produces against
// this repository's canonical roots.
type PatternID string

const (
	// PatternTeamCreate matches a literal TeamCreate( call prefix — native
	// parallel-team spawning syntax (orchestration).
	PatternTeamCreate PatternID = "team_create"
	// PatternSendMessage matches a literal SendMessage( call prefix —
	// native assignment-messaging syntax (orchestration).
	PatternSendMessage PatternID = "send_message"
	// PatternSkillInvocation matches a literal Skill(/ call prefix —
	// native skill-invocation syntax (orchestration).
	PatternSkillInvocation PatternID = "skill_invocation"
	// PatternAskUserQuestion matches a literal AskUserQuestion( call
	// prefix — native user-decision syntax (user decision).
	PatternAskUserQuestion PatternID = "ask_user_question"
)

var canonicalPatternIDs = [...]PatternID{
	PatternTeamCreate,
	PatternSendMessage,
	PatternSkillInvocation,
	PatternAskUserQuestion,
}

// PatternIDs returns a fresh defensive copy of the closed, exhaustive
// pattern-registry identity set.
func PatternIDs() []PatternID {
	return append([]PatternID(nil), canonicalPatternIDs[:]...)
}

// IsValid reports whether id is one of the closed PatternID values.
func (id PatternID) IsValid() bool {
	for _, candidate := range canonicalPatternIDs {
		if id == candidate {
			return true
		}
	}
	return false
}
