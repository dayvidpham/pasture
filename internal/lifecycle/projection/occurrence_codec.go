package projection

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/provenance"
)

// decodeOccurrence keeps historical codecs tolerant while validating the new
// constructor-owned metadata before any replay consumer can observe it.
func decodeOccurrence(kind provenance.EvidenceKind, raw, contentDigest []byte, out *occurrencePayload) error {
	switch kind {
	case occurrenceEvidenceKind, occurrenceEvidenceKindV2, occurrenceEvidenceKindV3:
	default:
		return fmt.Errorf("unsupported lifecycle occurrence kind %q; upgrade the reader before replaying this journal", kind)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	if kind != occurrenceEvidenceKindV3 {
		// A legacy member is not attribution evidence, even if an unknown JSON
		// member happens to have the same name.
		out.Execution = nil
		out.SessionTransition = ""
		return nil
	}
	sum := sha256.Sum256(raw)
	if !bytes.Equal(sum[:], contentDigest) {
		return fmt.Errorf("lifecycle occurrence digest does not match its payload; restore the journal row from a verified backup")
	}
	if err := model.ValidateOccurrenceEnvelope(out.Envelope); err != nil {
		return err
	}
	contract, err := parseOccurrenceContract(out.Contract)
	if err != nil || contract != out.Envelope.Runtime.Contract {
		return fmt.Errorf("lifecycle occurrence contract does not match its runtime envelope; restore the journal row from a verified backup")
	}
	if out.Execution != nil {
		if err := execution.ValidateMetadata(*out.Execution); err != nil {
			return err
		}
	}
	if out.SessionTransition != "" && out.SessionTransition != "resume" {
		return fmt.Errorf("lifecycle occurrence carries an unsupported session transition; restore the journal row from a verified backup")
	}
	if out.SessionTransition == "resume" {
		eligible := false
		for _, manifest := range []registration.Manifest{registration.ClaudeCode__2_1_261(), registration.Codex__0_153_0(), registration.OpenCode__1_18_29(), registration.OpenCode__2_0_20()} {
			if manifest.Contract == contract {
				for _, event := range manifest.Events {
					eligible = eligible || (event.Kind == out.Event && event.SessionTransition == model.SessionTransitionResume)
				}
			}
		}
		sessions := 0
		for _, binding := range out.Bindings {
			if binding.Kind == model.BindingSession && model.ValidateBindingText("session", binding.Value) == nil && binding.Value != "" {
				sessions++
			}
		}
		if !eligible || out.Capture != model.CaptureValid || sessions != 1 {
			return fmt.Errorf("lifecycle resume transition lacks a valid session-start coordinate; restore the journal row from a verified backup")
		}
	}
	return nil
}

func storedTransition(value string) model.SessionTransition {
	if value == "resume" {
		return model.SessionTransitionResume
	}
	return model.SessionTransitionNone
}
