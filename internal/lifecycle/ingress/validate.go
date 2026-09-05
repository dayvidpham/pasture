// Package ingress holds what every harness ingress shares: the refusals
// applied to one raw host payload before any harness-specific field is read,
// and the validating lookup of a native event name in a generated registration
// manifest. The per-harness packages beneath it keep only what differs per
// host: which fields are read and how they bind.
package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// Validation is the outcome of the shared refusals over one raw payload. Body
// is a defensive copy of the exact bytes, whatever the disposition: a refused
// payload is still retained byte-exact as the evidence of what the host sent.
// Members is the decoded top-level object, present only when the disposition
// is valid.
type Validation struct {
	Body        []byte
	Digest      digest.Digest
	Members     map[string]json.RawMessage
	Disposition model.CaptureDisposition
}

// Validate applies, in this order, the refusals every harness shares: the
// payload must be valid UTF-8; it must be exactly one JSON object with no
// trailing value; no member name may repeat. The order is what the Claude
// parser established and the other parsers now follow, so the same malformed
// input is refused with the same disposition on every harness. Nothing here
// reads a field by name; that is the per-harness parser's work.
func Validate(raw []byte) Validation {
	body := append([]byte(nil), raw...)
	result := Validation{Body: body, Digest: digest.FromBytes(body), Disposition: model.CaptureValid}
	if !utf8.Valid(body) {
		result.Disposition = model.CaptureInvalidUTF8
		return result
	}
	members, duplicate, err := strictMembers(body)
	switch {
	case duplicate:
		result.Disposition = model.CaptureDuplicateField
	case err != nil:
		result.Disposition = model.CaptureMalformed
	default:
		result.Members = members
	}
	return result
}

// strictMembers decodes exactly one JSON object and reports a repeated member
// name separately from every other decode failure, because the two are
// refused with different dispositions.
func strictMembers(raw []byte) (map[string]json.RawMessage, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false, fmt.Errorf("payload is not a JSON object: %w", err)
	}
	members := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false, fmt.Errorf("object member name is not a string")
		}
		if _, exists := members[key]; exists {
			return nil, true, nil
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false, err
		}
		members[key] = append(json.RawMessage(nil), value...)
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false, fmt.Errorf("payload object is not terminated: %w", err)
	}
	if token, err = decoder.Token(); err != io.EOF {
		return nil, false, fmt.Errorf("payload has trailing JSON value or bytes")
	}
	return members, false, nil
}

// EventByNativeName resolves one exact native event name against a generated
// registration manifest. It is the validating reverse lookup: ingress trusts
// the generated registration coordinate and never a payload's claim about
// itself, so a name the manifest does not declare is refused with the harness
// and the name in the refusal. The spelling is exact; a case variant of a
// declared name is not that name.
func EventByNativeName(manifest registration.Manifest, nativeName string) (registration.Event, error) {
	for _, candidate := range manifest.Events {
		if candidate.NativeName == nativeName {
			return candidate, nil
		}
	}
	return registration.Event{}, fmt.Errorf(
		"the %s registration at host version %s declares no native event named %q; "+
			"this happened in EventByNativeName (internal/lifecycle/ingress/validate.go) while resolving the event a hook was invoked for, before any payload was read; "+
			"ingress trusts the generated registration coordinate and never a payload's claim about itself, so nothing was read or recorded for this invocation; "+
			"invoke the hook with one of the native event names present in the %s support report, spelled exactly",
		manifest.Harness, manifest.Version, nativeName, manifest.Harness)
}
