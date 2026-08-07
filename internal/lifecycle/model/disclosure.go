package model

import "time"

// DisclosureDisposition is the closed set of context-disclosure result
// dispositions.
//
// DisclosureReleased is the MAXIMAL honest disposition (Stage-3 M5, ratified UAT
// resolution 6): the pure projection was durably recorded and released to the
// stdout boundary. Pasture commits before it prints, so the durable trail is
// intact once the projection reaches stdout, but Pasture CANNOT observe host or
// operator consumption of the released bytes — no "consumed" or "acknowledged"
// disposition is representable, because none could be honestly proven. The zero
// value is deliberately invalid so an unset disposition can never be committed.
type DisclosureDisposition uint8

const (
	// DisclosureReleased records that the disclosure projection was durably
	// committed and then released to the stdout boundary. It is the only
	// disposition M5 can honestly claim.
	DisclosureReleased DisclosureDisposition = iota + 1
)

// IsValid reports whether d is one of the enumerated dispositions.
func (d DisclosureDisposition) IsValid() bool { return d == DisclosureReleased }

// String returns the stable lowercase spelling used in durable evidence and
// diagnostics. An out-of-range value renders its numeric form so a diagnostic
// never silently drops it.
func (d DisclosureDisposition) String() string {
	switch d {
	case DisclosureReleased:
		return "released"
	default:
		return "unknown-disposition"
	}
}

// DisclosurePlanFact is the durable record of WHAT a context disclosure released
// (Stage-3 M5, D7). It is one of the three facts a single gated disclosure
// operation commits, classified ImmutableFact in the lifecycle guard map (the
// classification entry is pre-landed by the write-gate slice per IP-4): a plan
// is committed once per disclosure invocation and never mutated.
//
// Scope is the fingerprint of the bounded selection (the OccurrenceQuery the
// operator disclosed), so two disclosures over the same selection share one
// scope. Projection is the sha256 content digest of the exact canonical
// projection released to stdout, so the durable record binds the released bytes
// without retaining them. Policy names the single static M5 disclosure policy
// (there is no alternate policy to select; ContextPolicyDefinitionRef stays a
// staked seam).
type DisclosurePlanFact struct {
	PlanID     DisclosurePlanID
	Scope      ContentIdentity
	Projection ContentIdentity
	Policy     string
}

// IsValid reports whether the plan fact is well-formed: a nonzero scope
// fingerprint, a nonzero projection content digest, and a non-empty policy note.
// The PlanID is assigned only when the fact is committed, so it is not required
// for a constructor-side plan.
func (f DisclosurePlanFact) IsValid() bool {
	return f.Scope != (ContentIdentity{}) && f.Projection != (ContentIdentity{}) && f.Policy != ""
}

// DisclosureAttemptFact records WHEN a context disclosure was recorded (D7). It
// is committed together with its plan and result in one gated operation and is
// classified ImmutableFact: the recorded-at instant is fixed at commit and never
// changes.
type DisclosureAttemptFact struct {
	AttemptID  DisclosureAttemptID
	RecordedAt time.Time
}

// IsValid reports whether the attempt fact carries a real recorded-at instant.
func (f DisclosureAttemptFact) IsValid() bool { return !f.RecordedAt.IsZero() }

// DisclosureResultFact records the DISPOSITION of a context disclosure (D7). It
// is committed with its plan and attempt in one gated operation and classified
// ImmutableFact. At M5 the only honest disposition is DisclosureReleased.
type DisclosureResultFact struct {
	ResultID    DisclosureResultID
	Disposition DisclosureDisposition
}

// IsValid reports whether the result fact carries an enumerated disposition.
func (f DisclosureResultFact) IsValid() bool { return f.Disposition.IsValid() }
