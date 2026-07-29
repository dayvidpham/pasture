package lifecycle

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// MaxNativePayloadBytes is the largest native payload any frontend will
// accept, and the bound the process boundary applies before it buffers
// anything.
//
// It lives at the waist rather than in each frontend so there is one number to
// reason about: whatever a host sends, Pasture's exposure is the same. A hook
// payload describes one occurrence; a megabyte is already far more than any
// supported harness needs, and anything larger is payload content being
// smuggled onto a control channel.
const MaxNativePayloadBytes = 1 << 20

// Frontend translates one harness's native lifecycle payload into the
// target-agnostic Level-2 IR.
//
// A frontend is part of Pasture, in the same sense that a C parser is part of
// a C compiler: it is written in Pasture's own language, linked into Pasture,
// and its output is IR. The fact that it reads a host's JSON no more makes
// that JSON a Pasture API than parsing C makes arbitrary text a compiler API.
//
// Everything on the far side of the boundary — the generated hook
// registration and its trampoline — stays trivial: it forwards the native
// payload unchanged and does nothing else. All parsing, validation, and
// correlation live here, once per harness, in Go. Nothing here selects a
// Pasture operation; that is a later pass's decision, made once for all
// harnesses rather than once per harness.
//
// Implementations must be safe for concurrent use: one process may parse
// several occurrences, and a frontend holds no per-occurrence state.
type Frontend interface {
	// Harness returns the exact native harness this frontend is bound to.
	// A frontend serves exactly one harness; there is no runtime switch.
	Harness() ir.HarnessID

	// Parse decodes one native payload into the Level-2 lifecycle IR.
	//
	// requested is the event the caller believes fired, taken from the hook
	// registration that invoked Pasture. When it is non-empty the payload must
	// agree with it, so a misregistered hook is caught instead of silently
	// recording the wrong occurrence. The empty NativeEventName means
	// "unspecified": the frontend resolves the event from the payload alone.
	//
	// Parse performs no effects. A returned Event has been verified against
	// the pinned runtime contract; a returned error is actionable and nothing
	// has been read from or written to any store.
	Parse(payload []byte, requested NativeEventName) (Event, error)
}

// UnsupportedHarnessError reports that no frontend is built into this binary
// for the requested harness.
//
// This is a legalization failure in the compiler sense, and it is deliberately
// a first-class, named result rather than a silent no-op: an integration that
// quietly does nothing when it is not understood is indistinguishable from one
// that is working, which is exactly the failure mode this design exists to
// remove.
func UnsupportedHarnessError(requested ir.HarnessID, supported []ir.HarnessID, where string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("There is no lifecycle frontend for harness %q in this build.", requested),
		Why:      "Each harness needs its own reviewed parser bound to a pinned host contract; guessing a payload shape would invent semantics nobody approved.",
		Where:    where,
		Impact:   "The native event was not translated and nothing was recorded.",
		Fix:      "Use one of the harnesses this build supports: " + describeHarnesses(supported) + ".",
	}
}

func describeHarnesses(supported []ir.HarnessID) string {
	if len(supported) == 0 {
		return "(this build has no lifecycle frontends)"
	}
	out := ""
	for index, harness := range supported {
		if index > 0 {
			out += ", "
		}
		out += string(harness)
	}
	return out
}
