package model

import (
	"encoding/json"
	"fmt"
)

// CaptureCause is transient parser evidence, not a journal field. Its zero
// value means the adapter supplied no further diagnosis. No host value is held.
type CaptureCause struct {
	nonJournalValue
	kind        CaptureCauseKind
	field       string
	required    JSONKind
	disposition CaptureDisposition
}

type CaptureCauseKind uint8

const (
	CauseUnknown CaptureCauseKind = iota
	CauseMalformedJSON
	CauseNonObject
	CauseUndeclaredMember
	CauseMissingMember
	CauseWrongKind
	CauseEmptyIdentity
	CauseOverlengthIdentity
	CauseUnusableIdentity
	CauseEventMismatch
)

type JSONKind uint8

const (
	JSONKindUnknown JSONKind = iota
	JSONString
	JSONObject
)

func (k JSONKind) String() string {
	switch k {
	case JSONString:
		return "string"
	case JSONObject:
		return "object"
	default:
		return "unknown"
	}
}

// NewCaptureCause rejects invalid metadata rather than rendering a guessed
// explanation. The disposition stays the adapter's existing admission result.
func NewCaptureCause(kind CaptureCauseKind, field string, required JSONKind, disposition CaptureDisposition) CaptureCause {
	c := CaptureCause{kind: kind, field: field, required: required, disposition: disposition}
	if !c.valid() {
		panic("capture cause: inconsistent parser diagnosis; correct the cause, field, kind and disposition at the refusal site")
	}
	return c
}

func (c CaptureCause) Kind() CaptureCauseKind { return c.kind }
func (c CaptureCause) Field() string          { return c.field }
func (c CaptureCause) RequiredKind() JSONKind { return c.required }

func (c CaptureCause) valid() bool {
	switch c.kind {
	case CauseUnknown:
		return c == (CaptureCause{})
	case CauseMalformedJSON, CauseNonObject:
		return c.disposition == CaptureMalformed && c.field == "" && c.required == JSONKindUnknown
	case CauseUndeclaredMember:
		return c.disposition == CaptureUnsupportedSchema && c.required == JSONKindUnknown
	case CauseMissingMember, CauseWrongKind:
		return (c.disposition == CaptureUnsupportedSchema || c.disposition == CaptureEventMismatch || c.disposition == CaptureMalformed) && c.field != "" && (c.required == JSONString || c.required == JSONObject)
	case CauseEmptyIdentity, CauseOverlengthIdentity, CauseUnusableIdentity:
		return c.disposition == CaptureUnsupportedSchema && c.field != "" && c.required == JSONString
	case CauseEventMismatch:
		return (c.disposition == CaptureEventMismatch || c.disposition == CaptureUnsupportedSchema) && c.field != "" && c.required == JSONString
	default:
		return false
	}
}

// Check also guards the boundary where an adapter's cause meets its result.
// An unchanged adapter can omit a cause, including on a valid capture.
func (c CaptureCause) Check(disposition CaptureDisposition) error {
	if !c.valid() || (c.kind != CauseUnknown && c.disposition != disposition) {
		return fmt.Errorf("capture diagnosis disagrees with its disposition; no specific advice is safe; report the parser result and correct its cause/disposition pairing")
	}
	return nil
}

// JSONCaptureCause refines only the shared malformed disposition. Duplicate
// members and invalid UTF-8 retain their own earlier validation priority.
func JSONCaptureCause(raw []byte, disposition CaptureDisposition) CaptureCause {
	if disposition != CaptureMalformed {
		return CaptureCause{}
	}
	kind := CauseMalformedJSON
	if json.Valid(raw) {
		kind = CauseNonObject
	}
	return NewCaptureCause(kind, "", JSONKindUnknown, disposition)
}

// Advice is the sole cause-to-prose mapping. Field names are quoted so a host
// key cannot inject terminal lines. Values and decoder error text never enter it.
func (c CaptureCause) Advice(disposition CaptureDisposition) (reason, fix string, err error) {
	if err = c.Check(disposition); err != nil {
		return "", "", err
	}
	field := fmt.Sprintf("%q", c.field)
	switch c.kind {
	case CauseUnknown:
		return "", "", nil
	case CauseMalformedJSON:
		return "the payload is not one complete, well-formed JSON value", "Send one complete JSON object with no trailing value or bytes; check the hook's stdin serialization.", nil
	case CauseNonObject:
		return "the payload is valid JSON but its top level is not an object", "Send a JSON object at the top level, not an array, scalar or null.", nil
	case CauseUndeclaredMember:
		return "member " + field + " is not declared by this event's registration", "Compare that added member with the matching host contract and update the pinned registration before admitting it; do not rename or remove valid identities.", nil
	case CauseMissingMember:
		return "required member " + field + " is absent", "Supply " + field + " as a JSON " + c.required.String() + " at that path; check for a dropped or renamed member in the host contract.", nil
	case CauseWrongKind:
		return "member " + field + " has the wrong JSON kind; required kind is " + c.required.String(), "Send " + field + " as a JSON " + c.required.String() + " at that path; correct the wrapper or serializer, not the top-level object.", nil
	case CauseEmptyIdentity:
		return "identity " + field + " is an empty string", "Supply the nonempty host identity at " + field + "; do not synthesize a replacement identity.", nil
	case CauseOverlengthIdentity:
		return "identity " + field + " exceeds the 512-byte identity bound", "Check why " + field + " exceeds 512 bytes and align the host contract with the identity bound; do not truncate an identity.", nil
	case CauseUnusableIdentity:
		return "identity " + field + " contains padding or a control character", "Supply " + field + " without leading or trailing whitespace, NUL or control characters; fix serialization without changing the host identity.", nil
	case CauseEventMismatch:
		return "member " + field + " does not name the invoked event", "Send the payload for the command's event, or invoke the command for the event that payload describes; do not reinterpret it as another event.", nil
	default:
		panic("unreachable capture cause")
	}
}
