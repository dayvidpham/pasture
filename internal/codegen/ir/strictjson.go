package ir

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// strictJSONWithPresence decodes exactly one JSON value from data into
// target, and:
//  1. rejects any duplicate JSON object member anywhere in data (see
//     rejectDuplicateJSONMembers) — encoding/json's decoder silently applies
//     "last member wins" for a repeated key, which would let two different
//     byte-identical-looking readers of the same wire bytes disagree about
//     the effective value;
//  2. rejects unknown fields (json.Decoder.DisallowUnknownFields);
//  3. rejects a decode that omits any of requiredFields — a required field's
//     JSON zero value (e.g. an empty string, 0, or an empty array) is a
//     legitimate value, so presence must be checked independently of the
//     decoded Go value, not inferred from it;
//  4. rejects trailing content after the first JSON value.
//
// requiredFields names the top-level JSON object members target must
// contain; it is the caller's exhaustive omission matrix for that wire type.
func strictJSONWithPresence(data []byte, requiredFields []string, target any) error {
	if err := rejectDuplicateJSONMembers(data); err != nil {
		return err
	}
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	for _, field := range requiredFields {
		if _, ok := presence[field]; !ok {
			return fmt.Errorf("required field %q is omitted", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("additional JSON value follows the first")
		}
		return err
	}
	return nil
}

// duplicateJSONMemberError reports a genuine repeated object member found by
// rejectDuplicateJSONMembers. It is distinct from every other error the walk
// can return (empty input, truncated input, a JSON syntax error, …), which
// are all forms of "this is not one well-formed JSON value" rather than "this
// JSON value has two members with the same key". A caller uses errors.As to
// tell the two situations apart before choosing a diagnostic — see
// decision.go's DecodeReportedResult and continuation.go's canonicalJSON,
// which previously mislabeled every rejectDuplicateJSONMembers error
// (including a plain empty reader, i.e. io.EOF) as a duplicate member.
type duplicateJSONMemberError struct{ key string }

func (e *duplicateJSONMemberError) Error() string {
	return fmt.Sprintf("duplicate JSON member %q", e.key)
}

// rejectDuplicateJSONMembers walks data as a single JSON value (of any
// shape, at any nesting depth) and errors if any JSON object repeats a
// member name. encoding/json does not do this itself: for `{"a":1,"a":2}`,
// Decode silently keeps the last occurrence. Calling this once on a
// top-level wire document also covers every object nested inside it (an
// envelope's "data", a prompt's "stimulus", …), because they are all
// substrings of the same walk.
//
// Only a genuine repeated member returns a *duplicateJSONMemberError; empty
// input, truncated input, and JSON syntax errors are returned exactly as
// encoding/json reports them (io.EOF, io.ErrUnexpectedEOF, *json.SyntaxError,
// …) so a caller can distinguish "malformed/empty/truncated" from
// "duplicate member" instead of conflating every failure into one message.
func rejectDuplicateJSONMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkNoDuplicateMembers(decoder); err != nil {
		return err
	}
	return nil
}

// walkNoDuplicateMembers consumes exactly one JSON value (object, array, or
// scalar) from decoder, recursing into nested objects/arrays, and returns an
// error on the first duplicate object member it finds.
func walkNoDuplicateMembers(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key %v is not a string", keyToken)
			}
			if _, duplicate := seen[key]; duplicate {
				return &duplicateJSONMemberError{key: key}
			}
			seen[key] = struct{}{}
			if err := walkNoDuplicateMembers(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token() // consume closing '}'
		return err
	case '[':
		for decoder.More() {
			if err := walkNoDuplicateMembers(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token() // consume closing ']'
		return err
	default:
		return nil
	}
}

// isDuplicateJSONMember reports whether err (or something it wraps) is a
// genuine duplicate-member failure from rejectDuplicateJSONMembers, as
// opposed to empty/truncated/malformed input.
func isDuplicateJSONMember(err error) bool {
	var duplicate *duplicateJSONMemberError
	return errors.As(err, &duplicate)
}
