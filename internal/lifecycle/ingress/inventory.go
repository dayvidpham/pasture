package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// This file holds the mechanical half of the clearance procedure: the value
// classifier that inventories a captured payload, the value-only substitution
// rule for free text, the payload classes that are refused whatever the
// substitution, and the secret shapes the corpus scan refuses. Every rule
// here is value-only: a field name, the nesting, a type and a null survive
// untouched, so a cleared fixture keeps its power to falsify the host
// contract it was captured from.

// ValueClass is the class of one leaf value in a captured payload.
type ValueClass uint8

const (
	ClassInvalid ValueClass = iota
	// ClassIdentifier is a string with no whitespace and a bounded length:
	// identifiers, enum values, timestamps, digests, versions, URLs.
	ClassIdentifier
	// ClassPath is a string that names a filesystem location. It is cleared
	// by the home-path rule, never by free-text substitution.
	ClassPath
	// ClassFreeText is a string that carries whitespace or exceeds
	// FreeTextLengthLimit: prose, commands, file contents, titles. It is the
	// class the user's prompt and tool text land in.
	ClassFreeText
	ClassNumber
	ClassBool
	ClassNull
)

func (c ValueClass) IsValid() bool { return c >= ClassIdentifier && c <= ClassNull }

func (c ValueClass) String() string {
	switch c {
	case ClassIdentifier:
		return "identifier"
	case ClassPath:
		return "path"
	case ClassFreeText:
		return "free-text"
	case ClassNumber:
		return "number"
	case ClassBool:
		return "bool"
	case ClassNull:
		return "null"
	default:
		return ""
	}
}

// FreeTextLengthLimit is the byte length above which a string without
// whitespace is still classed as free text: no host identifier is that long,
// while a base64 blob or a minified document is.
const FreeTextLengthLimit = 128

// FreeTextRule names the value-only substitution that replaces a free-text
// string by same-length placeholder text. HomePathRule names the existing
// substitution that rewrites the capturing user's home directory. A fixture's
// provenance lists the rules applied, in order.
const (
	FreeTextRule = "free-text-v1"
	HomePathRule = "home-path-v1"
)

// MaxToolResponseBytes bounds a tool-response value a fixture may carry. Raw
// file contents from a tool response above it are refused whatever the
// substitution, because a fixture is evidence of a payload SHAPE and a large
// response is content the corpus has no reason to hold.
const MaxToolResponseBytes = 4096

// Field is one leaf of a captured payload: its JSON path, its class, and the
// raw byte span of its literal in the document.
type Field struct {
	Path  string
	Class ValueClass
	// Value is the decoded string for a string field, empty otherwise.
	Value string
	start int
	end   int
}

// pathLike matches a value that names a filesystem location: an absolute
// POSIX path, a home-relative path, a Windows drive path, or the dash-encoded
// project path the Claude transcript path carries.
var pathLike = regexp.MustCompile(`^(/|~/|[A-Za-z]:\\|-home-)`)

// Classify returns the class of one decoded string value.
func Classify(value string) ValueClass {
	if pathLike.MatchString(value) {
		return ClassPath
	}
	for _, r := range value {
		if unicode.IsSpace(r) {
			return ClassFreeText
		}
	}
	if len(value) > FreeTextLengthLimit {
		return ClassFreeText
	}
	return ClassIdentifier
}

// Inventory lists every leaf value of one JSON object payload with its class.
// It refuses a payload that is not one JSON object, because that is what the
// shared refusals admit and a fixture that fails them is not a fixture.
func Inventory(body []byte) ([]Field, error) {
	validation := Validate(body)
	if validation.Disposition != validDisposition() {
		return nil, fmt.Errorf("the payload cannot be inventoried because it is not one well-formed JSON object (disposition %v); this happened in Inventory (internal/lifecycle/ingress/inventory.go); a payload the shared refusals do not admit cannot become a fixture, so nothing was classified; capture the exact bytes the host sent and check they parse as one object", validation.Disposition)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var fields []Field
	var path []string
	// A key token is followed by its value; inside an object the tokens
	// alternate key, value, key, value. expectKey tracks that per level.
	type level struct {
		object    bool
		expectKey bool
		index     int
	}
	var levels []level
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the payload could not be walked after it was admitted: %w; this happened in Inventory (internal/lifecycle/ingress/inventory.go); report this, because the shared refusals admit only what this walk can read", err)
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				// A container that is an array element takes its index
				// segment here; one that is an object member already has its
				// key on the path; the root has no segment.
				if len(levels) > 0 {
					parent := &levels[len(levels)-1]
					if !parent.object {
						path = append(path, "["+strconv.Itoa(parent.index)+"]")
						parent.index++
					}
				}
				levels = append(levels, level{object: delim == '{', expectKey: delim == '{'})
			case '}', ']':
				levels = levels[:len(levels)-1]
				if len(levels) > 0 {
					path = path[:len(path)-1]
					if levels[len(levels)-1].object {
						levels[len(levels)-1].expectKey = true
					}
				}
			}
			continue
		}
		top := &levels[len(levels)-1]
		if top.object && top.expectKey {
			path = append(path, "."+token.(string))
			top.expectKey = false
			continue
		}
		if !top.object {
			path = append(path, "["+strconv.Itoa(top.index)+"]")
			top.index++
		}
		end := int(decoder.InputOffset())
		field := Field{Path: strings.Join(path, ""), end: end}
		switch value := token.(type) {
		case string:
			field.Class = Classify(value)
			field.Value = value
			field.start = stringLiteralStart(body, end)
		case float64, json.Number:
			field.Class = ClassNumber
		case bool:
			field.Class = ClassBool
		case nil:
			field.Class = ClassNull
		}
		fields = append(fields, field)
		path = path[:len(path)-1]
		if top.object {
			top.expectKey = true
		}
	}
	return fields, nil
}

// stringLiteralStart finds the opening quote of the string literal that ends
// at end-1, skipping an escaped quote inside the literal.
func stringLiteralStart(body []byte, end int) int {
	for index := end - 2; index >= 0; index-- {
		if body[index] != '"' {
			continue
		}
		backslashes := 0
		for probe := index - 1; probe >= 0 && body[probe] == '\\'; probe-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return index
		}
	}
	return -1
}

// SubstituteFreeText applies the free-text rule: every free-text string
// literal is replaced, in place, by a literal of the SAME RAW LENGTH made of
// the letter x. Keys, nesting, types, nulls, numbers, identifiers and paths
// are untouched byte for byte, so the document keeps its length and its
// shape. It returns the substituted document and the paths substituted, in
// document order. A document with no free text comes back as an unchanged
// copy with no paths.
func SubstituteFreeText(body []byte) ([]byte, []string, error) {
	fields, err := Inventory(body)
	if err != nil {
		return nil, nil, err
	}
	out := append([]byte(nil), body...)
	var paths []string
	for _, field := range fields {
		if field.Class != ClassFreeText {
			continue
		}
		if field.start < 0 || field.end <= field.start+1 {
			return nil, nil, fmt.Errorf("the free-text field %s could not be located in the document; this happened in SubstituteFreeText (internal/lifecycle/ingress/inventory.go); nothing was substituted; report this with the payload", field.Path)
		}
		placeholder := []byte(`"` + strings.Repeat("x", field.end-field.start-2) + `"`)
		copy(out[field.start:field.end], placeholder)
		paths = append(paths, field.Path)
	}
	return out, paths, nil
}

// RefusalClass is one payload class that is never committed, whatever the
// substitution.
type RefusalClass uint8

const (
	RefusalInvalid RefusalClass = iota
	// RefusalToolResponseOverLimit is a tool-response value above
	// MaxToolResponseBytes: raw file contents a fixture has no reason to hold.
	RefusalToolResponseOverLimit
	// RefusalEnvironmentDump is an object whose members read as environment
	// variables, or a string that lists them line by line.
	RefusalEnvironmentDump
)

func (c RefusalClass) String() string {
	switch c {
	case RefusalToolResponseOverLimit:
		return "tool-response-over-limit"
	case RefusalEnvironmentDump:
		return "environment-dump"
	default:
		return ""
	}
}

// Refusal names one refused field in a payload.
type Refusal struct {
	Class RefusalClass
	Path  string
	Bytes int
}

// responseSegment matches a path segment that carries a tool's response.
var responseSegment = regexp.MustCompile(`(?i)^\.(tool_?response|tool_?result|response|result|output|stdout|stderr|content)$`)

// environmentLine matches one line of an environment listing.
var environmentLine = regexp.MustCompile(`(?m)^[A-Z_][A-Z0-9_]*=`)

// environmentName matches a member name shaped like an environment variable.
var environmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// environmentDumpMembers is the smallest number of environment-shaped members
// that makes an object an environment dump rather than a constant or two.
const environmentDumpMembers = 3

// RefusedFields lists the fields of a payload that belong to a refused class.
// A tool-response string above MaxToolResponseBytes is refused, and an
// environment dump is refused whether it arrives as an object of
// SCREAMING_CASE members or as a string of NAME=value lines.
func RefusedFields(body []byte) ([]Refusal, error) {
	fields, err := Inventory(body)
	if err != nil {
		return nil, err
	}
	var refusals []Refusal
	members := map[string]int{}
	for _, field := range fields {
		if field.Class == ClassFreeText || field.Class == ClassIdentifier || field.Class == ClassPath {
			size := len(field.Value)
			if size > MaxToolResponseBytes && pathHasResponseSegment(field.Path) {
				refusals = append(refusals, Refusal{Class: RefusalToolResponseOverLimit, Path: field.Path, Bytes: size})
			}
			if len(environmentLine.FindAllStringIndex(field.Value, -1)) >= environmentDumpMembers {
				refusals = append(refusals, Refusal{Class: RefusalEnvironmentDump, Path: field.Path, Bytes: size})
			}
			parent, name := splitLastSegment(field.Path)
			if strings.HasPrefix(name, ".") && environmentName.MatchString(name[1:]) {
				members[parent]++
			}
		}
	}
	for parent, count := range members {
		if count >= environmentDumpMembers {
			refusals = append(refusals, Refusal{Class: RefusalEnvironmentDump, Path: parent, Bytes: count})
		}
	}
	return refusals, nil
}

func pathHasResponseSegment(path string) bool {
	for _, segment := range strings.Split(strings.ReplaceAll(path, "[", ".["), ".") {
		if segment == "" {
			continue
		}
		if responseSegment.MatchString("." + segment) {
			return true
		}
	}
	return false
}

func splitLastSegment(path string) (parent, last string) {
	dot := strings.LastIndex(path, ".")
	bracket := strings.LastIndex(path, "[")
	cut := dot
	if bracket > cut {
		cut = bracket
	}
	if cut <= 0 {
		return "", path
	}
	return path[:cut], path[cut:]
}

// SecretPattern is one committed secret shape the corpus scan refuses.
type SecretPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// secretPatterns is the committed regex set. Each shape has a planted sample
// in the scan test, so a pattern removed here turns that sample's case RED.
var secretPatterns = []SecretPattern{
	{"private key block", regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----`)},
	{"AWS access key id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"AWS secret access key assignment", regexp.MustCompile(`(?i)aws_secret_access_key\s*[=:]\s*["']?[A-Za-z0-9/+=]{40}`)},
	{"GCP API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"GCP OAuth access token", regexp.MustCompile(`\bya29\.[0-9A-Za-z_-]{20,}`)},
	{"GitHub token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b`)},
	{"GitHub fine-grained token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`)},
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)},
	{"JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
}

// SecretPatterns returns a copy of the committed secret shapes.
func SecretPatterns() []SecretPattern {
	return append([]SecretPattern(nil), secretPatterns...)
}

// SecretHit is one match of a secret shape in a document.
type SecretHit struct {
	Pattern string
	Offset  int
	Length  int
}

// ScanSecrets reports every secret shape found anywhere in the raw bytes. It
// reads bytes and not JSON, so a token in a sidecar, a YAML corpus or a
// comment is found the same way as one in a payload value.
func ScanSecrets(body []byte) []SecretHit {
	var hits []SecretHit
	for _, pattern := range secretPatterns {
		for _, span := range pattern.Pattern.FindAllIndex(body, -1) {
			hits = append(hits, SecretHit{Pattern: pattern.Name, Offset: span[0], Length: span[1] - span[0]})
		}
	}
	return hits
}

// Unclearable lists the reasons a payload cannot be cleared by value
// substitution alone: a refused field, or a secret shape that survives the
// free-text substitution because it sits in an identifier or path value that
// substitution must not touch. A payload with no reasons is clearable by the
// listed rules; a payload with any reason needs a user decision, and its event
// stays withheld.
func Unclearable(body []byte) ([]string, error) {
	refusals, err := RefusedFields(body)
	if err != nil {
		return nil, err
	}
	var reasons []string
	for _, refusal := range refusals {
		reasons = append(reasons, fmt.Sprintf("field %s is refused as %s (%d bytes)", refusal.Path, refusal.Class, refusal.Bytes))
	}
	substituted, _, err := SubstituteFreeText(body)
	if err != nil {
		return nil, err
	}
	fields, err := Inventory(substituted)
	if err != nil {
		return nil, err
	}
	for _, hit := range ScanSecrets(substituted) {
		path := "outside any field"
		for _, field := range fields {
			if field.start >= 0 && hit.Offset >= field.start && hit.Offset < field.end {
				path = "field " + field.Path
				break
			}
		}
		reasons = append(reasons, fmt.Sprintf("%s carries a %s, which value substitution cannot clear because the value is not free text", path, hit.Pattern))
	}
	return reasons, nil
}
