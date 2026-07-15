// Package artifact defines immutable, target-independent generated artifacts.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

const (
	digestPrefix   = "sha256:"
	bundleIDPrefix = "artifact.bundle.v1:sha256:"
)

// ValidationError describes a rejected artifact value or filesystem boundary.
// Its fields are stable so callers can present or inspect the actionable
// failure without parsing Error's text.
type ValidationError struct {
	Stage  string
	Bundle string
	Entry  string
	Rule   string
	Reason string
	Impact string
	Fix    string
	Cause  error
}

func (e *ValidationError) Error() string {
	bundle := e.Bundle
	if bundle == "" {
		bundle = "<unidentified>"
	}
	entry := e.Entry
	if entry == "" {
		entry = "<none>"
	}
	return fmt.Sprintf(
		"artifact: validation failed during %s for bundle %q, entry %q: rule %q failed because %s; impact: %s; fix: %s",
		e.Stage,
		bundle,
		entry,
		e.Rule,
		e.Reason,
		e.Impact,
		e.Fix,
	)
}

// Unwrap returns the lower-level filesystem or codec error, when one exists.
func (e *ValidationError) Unwrap() error { return e.Cause }

func invalid(stage, bundle, entry, rule, reason, impact, fix string, cause error) error {
	return &ValidationError{
		Stage:  stage,
		Bundle: bundle,
		Entry:  entry,
		Rule:   rule,
		Reason: reason,
		Impact: impact,
		Fix:    fix,
		Cause:  cause,
	}
}

// Path is a canonical, slash-separated relative artifact path.
type Path struct{ value string }

// NewPath validates and owns a canonical artifact path.
func NewPath(value string) (Path, error) {
	if value == "" {
		return Path{}, invalid(
			"path construction", "", value, "non-empty relative path",
			"the path is empty", "the entry cannot be addressed safely",
			"provide a non-empty slash-separated path relative to the bundle root", fs.ErrInvalid,
		)
	}
	if value == "." || !fs.ValidPath(value) || strings.ContainsAny(value, "\\\x00") {
		return Path{}, invalid(
			"path construction", "", value, "canonical clean relative path",
			"the path is absolute, traversing, unclean, contains a backslash or NUL, or names the bundle root",
			"the entry could escape or collide at an installation boundary",
			"use a clean slash-separated relative path without '.', '..', empty segments, backslashes, or NUL bytes", fs.ErrInvalid,
		)
	}
	return Path{value: value}, nil
}

func (p Path) String() string { return p.value }

// MarshalText returns the canonical path spelling.
func (p Path) MarshalText() ([]byte, error) {
	if _, err := NewPath(p.value); err != nil {
		return nil, err
	}
	return []byte(p.value), nil
}

// UnmarshalText replaces p only after the complete input validates.
func (p *Path) UnmarshalText(text []byte) error {
	if p == nil {
		return invalid(
			"path decoding", "", string(text), "non-nil decode target",
			"the destination Path pointer is nil", "the decoded path cannot be returned",
			"decode into a non-nil *artifact.Path", nil,
		)
	}
	parsed, err := NewPath(string(text))
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

// Mode is an explicit set of Unix permission bits.
type Mode struct {
	bits  uint32
	valid bool
}

// NewMode accepts permission bits in the inclusive range 0000 through 0777.
func NewMode(bits uint32) (Mode, error) {
	if bits&^uint32(fs.ModePerm) != 0 {
		return Mode{}, invalid(
			"mode construction", "", "", "permission bits only",
			fmt.Sprintf("mode %#o contains bits outside 0777", bits),
			"the artifact mode would be ambiguous or include a filesystem type",
			"provide explicit Unix permission bits from 0000 through 0777", fs.ErrInvalid,
		)
	}
	return Mode{bits: bits, valid: true}, nil
}

// Bits returns the validated permission bits.
func (m Mode) Bits() uint32 { return m.bits }

func (m Mode) String() string {
	if !m.valid {
		return ""
	}
	return fmt.Sprintf("%04o", m.bits)
}

// ParseMode decodes the canonical four-digit octal form, such as 0644.
func ParseMode(value string) (Mode, error) {
	if len(value) != 4 || value[0] != '0' {
		return Mode{}, invalid(
			"mode decoding", "", "", "canonical octal mode",
			fmt.Sprintf("mode %q is not four octal digits beginning with 0", value),
			"the artifact mode cannot be decoded deterministically",
			"encode the mode as exactly four octal digits from 0000 through 0777", fs.ErrInvalid,
		)
	}
	bits, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return Mode{}, invalid(
			"mode decoding", "", "", "canonical octal mode",
			fmt.Sprintf("mode %q contains a non-octal digit", value),
			"the artifact mode cannot be decoded deterministically",
			"encode the mode as exactly four octal digits from 0000 through 0777", err,
		)
	}
	return NewMode(uint32(bits))
}

func (m Mode) MarshalText() ([]byte, error) {
	if !m.valid {
		return nil, invalid(
			"mode encoding", "", "", "validated mode",
			"the zero Mode value was not constructed", "an invalid mode cannot be serialized",
			"construct the mode with artifact.NewMode or artifact.ParseMode", fs.ErrInvalid,
		)
	}
	return []byte(m.String()), nil
}

func (m *Mode) UnmarshalText(text []byte) error {
	if m == nil {
		return invalid(
			"mode decoding", "", "", "non-nil decode target",
			"the destination Mode pointer is nil", "the decoded mode cannot be returned",
			"decode into a non-nil *artifact.Mode", nil,
		)
	}
	parsed, err := ParseMode(string(text))
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// Digest is a validated lowercase SHA-256 content digest.
type Digest struct{ value string }

// DigestBytes returns the SHA-256 digest of the exact supplied bytes.
func DigestBytes(content []byte) Digest {
	sum := sha256.Sum256(content)
	return Digest{value: digestPrefix + hex.EncodeToString(sum[:])}
}

// ParseDigest validates the canonical sha256:<64 lowercase hex> form.
func ParseDigest(value string) (Digest, error) {
	if !validLowerHexValue(value, digestPrefix) {
		return Digest{}, invalid(
			"digest decoding", "", "", "canonical SHA-256 digest",
			fmt.Sprintf("digest %q is not sha256 followed by 64 lowercase hexadecimal digits", value),
			"content integrity cannot be established",
			"provide the exact form sha256:<64 lowercase hex digits> computed over the file bytes", fs.ErrInvalid,
		)
	}
	return Digest{value: value}, nil
}

func (d Digest) String() string { return d.value }

func (d Digest) MarshalText() ([]byte, error) {
	if _, err := ParseDigest(d.value); err != nil {
		return nil, err
	}
	return []byte(d.value), nil
}

func (d *Digest) UnmarshalText(text []byte) error {
	if d == nil {
		return invalid(
			"digest decoding", "", "", "non-nil decode target",
			"the destination Digest pointer is nil", "the decoded digest cannot be returned",
			"decode into a non-nil *artifact.Digest", nil,
		)
	}
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// BundleID is the validated content address of a complete artifact manifest.
type BundleID struct{ value string }

// ParseBundleID validates the canonical artifact bundle identifier.
func ParseBundleID(value string) (BundleID, error) {
	if !validLowerHexValue(value, bundleIDPrefix) {
		return BundleID{}, invalid(
			"bundle ID decoding", value, "", "canonical content-addressed bundle ID",
			fmt.Sprintf("bundle ID %q is not artifact.bundle.v1:sha256 followed by 64 lowercase hexadecimal digits", value),
			"the bundle identity cannot be trusted or compared",
			"provide the exact form artifact.bundle.v1:sha256:<64 lowercase hex digits>", fs.ErrInvalid,
		)
	}
	return BundleID{value: value}, nil
}

func (id BundleID) String() string { return id.value }

func (id BundleID) MarshalText() ([]byte, error) {
	if _, err := ParseBundleID(id.value); err != nil {
		return nil, err
	}
	return []byte(id.value), nil
}

func (id *BundleID) UnmarshalText(text []byte) error {
	if id == nil {
		return invalid(
			"bundle ID decoding", string(text), "", "non-nil decode target",
			"the destination BundleID pointer is nil", "the decoded ID cannot be returned",
			"decode into a non-nil *artifact.BundleID", nil,
		)
	}
	parsed, err := ParseBundleID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func validLowerHexValue(value, prefix string) bool {
	hexValue := strings.TrimPrefix(value, prefix)
	if hexValue == value || len(hexValue) != sha256.Size*2 {
		return false
	}
	for _, r := range hexValue {
		if !('0' <= r && r <= '9') && !('a' <= r && r <= 'f') {
			return false
		}
	}
	return true
}
