package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// baselineUnsetOriginPayload is the FROZEN occurrence.v1 evidence payload the
// production encoder committed at baseline 8bfdf4c for validDelivery() — a
// native delivery carrying NO origin member, in the canonical key-sorted form
// the journal stores. Since DEFS-SLICE-3 the common receipt boundary emits
// occurrence.v2 with the complete four-reference envelope; the committed-bytes
// pins for the new writer live in the production suite
// (envelope_production_test.go), which pins the v2 origin omission against a
// real store. What stays HERE is the legacy side of the guarantee: the frozen
// v1 bytes must still decode exactly — zero refs, no origin member — so
// pre-origin records remain faithful historical read data after the cutover.
var baselineUnsetOriginPayload = fmt.Sprintf(`{"bindings":null,"body_digest":"sha256:f352f3001468c4e837240ca5714239468d614ec48ca47016bd69f2c64ad5fd8d","capture":1,"contract":"%s","envelope":{"HostVersion":"","Implementation":{"Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}},"Retention":{"Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}},"Runtime":{"Contract":"%s","Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}},"Schema":{"Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}}},"event":1}`, productionRegistrationContract(), productionRegistrationContract())

// receiveOnce and the committed-payload pins moved to
// envelope_production_test.go: every success commit now ensures definition
// refs through the real journal, which the in-memory fake cannot back.

// TestFrozenV1BaselineStillDecodes pins the legacy read half of the origin
// guarantee: the frozen pre-origin v1 bytes decode with zero envelope refs
// and no origin, so the v2 writer cutover never rewrites what old rows mean.
func TestFrozenV1BaselineStillDecodes(t *testing.T) {
	t.Parallel()
	var decoded occurrencePayload
	if err := json.Unmarshal([]byte(baselineUnsetOriginPayload), &decoded); err != nil {
		t.Fatalf("frozen v1 baseline rejected: %v", err)
	}
	if decoded.Contract.String() != productionRegistrationContract() || decoded.Event != 1 || decoded.Capture != model.CaptureValid {
		t.Fatalf("frozen baseline fields = %#v, want the recorded contract, event 1, valid capture", decoded)
	}
	for _, ref := range []model.DefinitionRef{decoded.Envelope.Runtime.Definition, decoded.Envelope.Schema.Definition, decoded.Envelope.Implementation.Definition, decoded.Envelope.Retention.Definition} {
		if ref != (model.DefinitionRef{}) {
			t.Fatalf("frozen baseline envelope ref = %#v, want zero (legacy zero-ref encoding)", ref)
		}
	}
	if bytes.Contains([]byte(baselineUnsetOriginPayload), []byte("origin")) {
		t.Fatal("frozen baseline contains an origin member")
	}
}

// productionRegistrationContract is the registration contract spelling
// ("claude-code/<version>") the occurrence payload carries, read from the one
// root so the frozen baseline follows the recorded host version.
func productionRegistrationContract() string {
	root, err := artifact.ProductionRuntimeContract(artifact.HarnessClaudeCode)
	if err != nil {
		panic(err)
	}
	_, version, _ := strings.Cut(root.String(), "@")
	return "claude-code/" + version
}
