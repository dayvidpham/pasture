package activation

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"gopkg.in/yaml.v3"
)

// Classification identifies whether a corpus row is expected to exercise the
// admitted path or one of its guarded rejection paths. Its zero value is
// reserved for an unconstructed value.
type Classification uint8

const (
	ClassificationInvalid Classification = iota
	ClassificationMustPass
	ClassificationMustFail
)

func (c Classification) IsValid() bool {
	return c == ClassificationMustPass || c == ClassificationMustFail
}

func (c Classification) String() string {
	switch c {
	case ClassificationMustPass:
		return "must-pass"
	case ClassificationMustFail:
		return "must-fail"
	default:
		return ""
	}
}

func (c *Classification) UnmarshalText(text []byte) error {
	switch string(text) {
	case "must-pass":
		*c = ClassificationMustPass
	case "must-fail":
		*c = ClassificationMustFail
	default:
		return fmt.Errorf("activation corpus: unknown classification %q; use must-pass or must-fail", text)
	}
	return nil
}

// Decision is the closed activation result expected from a corpus case. Its
// zero value is invalid.
type Decision uint8

const (
	DecisionInvalid Decision = iota
	DecisionEnabled
	DecisionWithheld
)

func (d Decision) IsValid() bool { return d == DecisionEnabled || d == DecisionWithheld }

func (d Decision) String() string {
	switch d {
	case DecisionEnabled:
		return "enabled"
	case DecisionWithheld:
		return "withheld"
	default:
		return ""
	}
}

func (d *Decision) UnmarshalText(text []byte) error {
	switch string(text) {
	case "enabled":
		*d = DecisionEnabled
	case "withheld":
		*d = DecisionWithheld
	default:
		return fmt.Errorf("activation corpus: unknown decision %q; use enabled or withheld", text)
	}
	return nil
}

// CorpusReason is the closed set of expected evidence-gate bypass reasons.
// CorpusReasonNone is the zero value and is valid only with DecisionEnabled on
// a constructed Case or Evaluation.
type CorpusReason uint8

const (
	CorpusReasonNone CorpusReason = iota
	CorpusReasonNonAuthenticOrigin
	CorpusReasonDigestMismatch
	CorpusReasonVersionOutOfRange
	CorpusReasonPathEscape
)

func (r CorpusReason) IsValid() bool {
	return r <= CorpusReasonPathEscape
}

func (r CorpusReason) IsWithheldReason() bool {
	return r >= CorpusReasonNonAuthenticOrigin && r <= CorpusReasonPathEscape
}

func (r CorpusReason) String() string {
	switch r {
	case CorpusReasonNone:
		return ""
	case CorpusReasonNonAuthenticOrigin:
		return "non-authentic-origin"
	case CorpusReasonDigestMismatch:
		return "digest-mismatch"
	case CorpusReasonVersionOutOfRange:
		return "version-out-of-range"
	case CorpusReasonPathEscape:
		return "path-escape"
	default:
		return ""
	}
}

func (r *CorpusReason) UnmarshalText(text []byte) error {
	switch string(text) {
	case "":
		*r = CorpusReasonNone
	case "non-authentic-origin":
		*r = CorpusReasonNonAuthenticOrigin
	case "digest-mismatch":
		*r = CorpusReasonDigestMismatch
	case "version-out-of-range":
		*r = CorpusReasonVersionOutOfRange
	case "path-escape":
		*r = CorpusReasonPathEscape
	default:
		return fmt.Errorf("activation corpus: unknown reason %q; use an enabled empty reason or one declared withheld reason", text)
	}
	return nil
}

// ProvenanceSource identifies the reviewed source category for a corpus row.
// It is metadata only; Evaluate never trusts it as evidence.
type ProvenanceSource uint8

const (
	ProvenanceSourceInvalid ProvenanceSource = iota
	ProvenanceSourceRequirement
	ProvenanceSourceBug
	ProvenanceSourceEnum
	ProvenanceSourceBoundary
)

func (s ProvenanceSource) IsValid() bool {
	return s >= ProvenanceSourceRequirement && s <= ProvenanceSourceBoundary
}

func (s ProvenanceSource) String() string {
	switch s {
	case ProvenanceSourceRequirement:
		return "requirement"
	case ProvenanceSourceBug:
		return "bug"
	case ProvenanceSourceEnum:
		return "enum"
	case ProvenanceSourceBoundary:
		return "boundary"
	default:
		return ""
	}
}

func (s *ProvenanceSource) UnmarshalText(text []byte) error {
	switch string(text) {
	case "requirement":
		*s = ProvenanceSourceRequirement
	case "bug":
		*s = ProvenanceSourceBug
	case "enum":
		*s = ProvenanceSourceEnum
	case "boundary":
		*s = ProvenanceSourceBoundary
	default:
		return fmt.Errorf("activation corpus: unknown provenance source %q; use requirement, bug, enum, or boundary", text)
	}
	return nil
}

const (
	// MaxCorpusBytes bounds one YAML document before decoding allocates case
	// values. MaxCorpusCases bounds the retained ordered metadata rows.
	MaxCorpusBytes     = 8 << 20
	MaxCorpusCases     = 1024
	MaxFieldBytes      = 1 << 20
	MaxProvenanceBytes = 1 << 20
	MaxFixtureBytes    = acceptance.MaxCaptureFixtureBytes
)

// Case is an immutable, scalar-backed corpus row. Fixture, provenance, and
// mutation fields intentionally remain private: callers compare only this
// public oracle surface and pass the Case back to Evaluate.
type Case struct {
	name                string
	fixture             string
	expectedDecision    Decision
	expectedReason      CorpusReason
	classification      Classification
	provenanceSource    ProvenanceSource
	provenanceReference string
	mutationDescription string
	constructed         bool
}

func (c Case) IsValid() bool {
	return c.constructed && c.name != "" && c.fixture != "" &&
		c.classification.IsValid() && c.provenanceSource.IsValid() &&
		c.provenanceReference != "" && c.mutationDescription != "" &&
		validDecisionReason(c.expectedDecision, c.expectedReason) &&
		validClassificationDecision(c.classification, c.expectedDecision)
}

func (c Case) Name() string                   { return c.name }
func (c Case) Classification() Classification { return c.classification }
func (c Case) ExpectedDecision() Decision     { return c.expectedDecision }
func (c Case) ExpectedReason() CorpusReason   { return c.expectedReason }

// Corpus owns an ordered set of immutable Case values. The zero value is an
// empty corpus; LoadCorpus is the constructor that establishes coverage.
type Corpus struct {
	cases       []Case
	constructed bool
}

func (c Corpus) Cases() []Case {
	if len(c.cases) == 0 {
		return nil
	}
	out := make([]Case, len(c.cases))
	copy(out, c.cases)
	return out
}

// Evaluation is the immutable result of one evidence evaluation. The zero
// value is intentionally invalid and is returned with every actionable error.
type Evaluation struct {
	caseName     string
	event        model.ContractEventKind
	decision     Decision
	reason       CorpusReason
	detail       string
	eventPresent bool
	constructed  bool
}

func (e Evaluation) IsValid() bool {
	if !e.constructed || e.caseName == "" || !validDecisionReason(e.decision, e.reason) {
		return false
	}
	if e.decision == DecisionEnabled {
		return e.eventPresent && e.event != 0
	}
	return !e.eventPresent && e.event == 0
}

func (e Evaluation) CaseName() string     { return e.caseName }
func (e Evaluation) Decision() Decision   { return e.decision }
func (e Evaluation) Reason() CorpusReason { return e.reason }

// Detail is the sentence that explains a withheld reason to a reader, when
// the reason has one; a version-out-of-range evaluation names the observed
// host version and the admitted versions. It is empty for an enabled
// evaluation.
func (e Evaluation) Detail() string { return e.detail }
func (e Evaluation) Event() (model.ContractEventKind, bool) {
	if !e.eventPresent {
		return 0, false
	}
	return e.event, true
}

// MissingCoverage is a bitset describing the non-vacuity requirements absent
// from a corpus. The zero value means that no coverage is missing.
type MissingCoverage uint8

const (
	MissingCoverageNone     MissingCoverage = 0
	MissingCoverageMustPass MissingCoverage = 1 << (iota - 1)
	MissingCoverageMustFail
	MissingCoverageNonAuthenticOrigin
	MissingCoverageDigestMismatch
	MissingCoverageVersionOutOfRange
	MissingCoveragePathEscape
)

func (m MissingCoverage) Has(bit MissingCoverage) bool { return m&bit == bit }

// CoverageError reports a syntactically valid corpus that is vacuous or lacks
// one or more required rejection categories.
type CoverageError struct {
	MissingCoverage MissingCoverage
}

func (e *CoverageError) Error() string {
	if e == nil {
		return "activation corpus coverage is missing"
	}
	missing := make([]string, 0, 6)
	for _, item := range []struct {
		bit  MissingCoverage
		name string
	}{
		{MissingCoverageMustPass, "must-pass class"},
		{MissingCoverageMustFail, "must-fail class"},
		{MissingCoverageNonAuthenticOrigin, CorpusReasonNonAuthenticOrigin.String()},
		{MissingCoverageDigestMismatch, CorpusReasonDigestMismatch.String()},
		{MissingCoverageVersionOutOfRange, CorpusReasonVersionOutOfRange.String()},
		{MissingCoveragePathEscape, CorpusReasonPathEscape.String()},
	} {
		if e.MissingCoverage.Has(item.bit) {
			missing = append(missing, item.name)
		}
	}
	return fmt.Sprintf("activation corpus coverage is incomplete: missing %v; add cases for each named class or bypass category", missing)
}

func validDecisionReason(decision Decision, reason CorpusReason) bool {
	switch decision {
	case DecisionEnabled:
		return reason == CorpusReasonNone
	case DecisionWithheld:
		return reason.IsWithheldReason()
	default:
		return false
	}
}

func validClassificationDecision(classification Classification, decision Decision) bool {
	switch classification {
	case ClassificationMustPass:
		return decision == DecisionEnabled
	case ClassificationMustFail:
		return decision == DecisionWithheld
	default:
		return false
	}
}

// LoadCorpus reads and validates one bounded activation corpus. Its
// implementation is intentionally kept separate from the static activation
// manifest; it is test-time evidence tooling and never runs on hook ingress.
func LoadCorpus(path string) (Corpus, error) {
	f, err := os.Open(path)
	if err != nil {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: open %q: %w; ensure the corpus exists and is readable", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxCorpusBytes+1))
	if err != nil {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: read %q: %w", path, err)
	}
	if len(raw) > MaxCorpusBytes {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: corpus %q exceeds the %d-byte bound; split or reduce the corpus", path, MaxCorpusBytes)
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: decode strict YAML document %q: %w", path, err)
	}
	if err := rejectYAMLFeatures(&doc); err != nil {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: corpus %q is not plain strict YAML: %w", path, err)
	}
	var trailing yaml.Node
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: corpus %q must contain exactly one YAML document; remove trailing content", path)
	}
	type row struct {
		Name  *string `yaml:"name"`
		Input *struct {
			Fixture *string `yaml:"fixture"`
		} `yaml:"input"`
		Expected *struct {
			Decision *Decision     `yaml:"decision"`
			Reason   *CorpusReason `yaml:"reason"`
		} `yaml:"expected"`
		Classification *Classification `yaml:"classification"`
		Provenance     *struct {
			Source *ProvenanceSource `yaml:"source"`
			Ref    *string           `yaml:"ref"`
		} `yaml:"provenance"`
		Mutation *struct {
			Description *string `yaml:"description"`
		} `yaml:"mutation"`
	}
	var wire struct {
		Cases []row `yaml:"cases"`
	}
	dec = yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&wire); err != nil {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: decode known fields in %q: %w", path, err)
	}
	if len(wire.Cases) == 0 || len(wire.Cases) > MaxCorpusCases {
		return Corpus{}, fmt.Errorf("activation.LoadCorpus: corpus %q has %d cases; require 1..%d", path, len(wire.Cases), MaxCorpusCases)
	}
	seen := make(map[string]struct{}, len(wire.Cases))
	cases := make([]Case, 0, len(wire.Cases))
	missing := MissingCoverageMustPass | MissingCoverageMustFail | MissingCoverageNonAuthenticOrigin | MissingCoverageDigestMismatch | MissingCoverageVersionOutOfRange | MissingCoveragePathEscape
	for i, r := range wire.Cases {
		missingPath := ""
		switch {
		case r.Name == nil:
			missingPath = "name"
		case r.Input == nil:
			missingPath = "input"
		case r.Input.Fixture == nil:
			missingPath = "input.fixture"
		case r.Expected == nil:
			missingPath = "expected"
		case r.Expected.Decision == nil:
			missingPath = "expected.decision"
		case r.Expected.Reason == nil:
			missingPath = "expected.reason"
		case r.Classification == nil:
			missingPath = "classification"
		case r.Provenance == nil:
			missingPath = "provenance"
		case r.Provenance.Source == nil:
			missingPath = "provenance.source"
		case r.Provenance.Ref == nil:
			missingPath = "provenance.ref"
		case r.Mutation == nil:
			missingPath = "mutation"
		case r.Mutation.Description == nil:
			missingPath = "mutation.description"
		}
		if missingPath != "" {
			return Corpus{}, fmt.Errorf("activation.LoadCorpus: case %d is missing required key %s; add that key explicitly (enabled expected.reason must be reason: \"\")", i, missingPath)
		}
		for _, field := range []struct{ path, value string }{{"name", *r.Name}, {"input.fixture", *r.Input.Fixture}, {"provenance.ref", *r.Provenance.Ref}, {"mutation.description", *r.Mutation.Description}} {
			if strings.TrimSpace(field.value) == "" {
				return Corpus{}, fmt.Errorf("activation.LoadCorpus: case %d required field %s is empty; provide a non-empty string", i, field.path)
			}
			if len(field.value) > MaxFieldBytes {
				return Corpus{}, fmt.Errorf("activation.LoadCorpus: case %d required field %s exceeds the %d-byte bound; shorten that field", i, field.path, MaxFieldBytes)
			}
		}
		if _, ok := seen[*r.Name]; ok {
			return Corpus{}, fmt.Errorf("activation.LoadCorpus: duplicate case name %q at case %d; names must be unique", *r.Name, i)
		}
		seen[*r.Name] = struct{}{}
		c := Case{name: *r.Name, fixture: *r.Input.Fixture, expectedDecision: *r.Expected.Decision, expectedReason: *r.Expected.Reason, classification: *r.Classification, provenanceSource: *r.Provenance.Source, provenanceReference: *r.Provenance.Ref, mutationDescription: *r.Mutation.Description, constructed: true}
		if !c.IsValid() {
			return Corpus{}, fmt.Errorf("activation.LoadCorpus: case %q violates classification/decision/reason combination: classification=%q decision=%q reason=%q; must-pass requires enabled with explicit empty reason and must-fail requires withheld with one bypass reason", *r.Name, r.Classification.String(), r.Expected.Decision.String(), r.Expected.Reason.String())
		}
		if c.classification == ClassificationMustPass {
			missing &^= MissingCoverageMustPass
		} else {
			missing &^= MissingCoverageMustFail
		}
		switch c.expectedReason {
		case CorpusReasonNonAuthenticOrigin:
			missing &^= MissingCoverageNonAuthenticOrigin
		case CorpusReasonDigestMismatch:
			missing &^= MissingCoverageDigestMismatch
		case CorpusReasonVersionOutOfRange:
			missing &^= MissingCoverageVersionOutOfRange
		case CorpusReasonPathEscape:
			missing &^= MissingCoveragePathEscape
		}
		cases = append(cases, c)
	}
	if missing != 0 {
		return Corpus{}, &CoverageError{MissingCoverage: missing}
	}
	return Corpus{cases: cases, constructed: true}, nil
}

func rejectYAMLFeatures(n *yaml.Node) error {
	return validateYAMLNode(n, "document")
}

func validateYAMLNode(n *yaml.Node, path string) error {
	if n.Alias != nil {
		return fmt.Errorf("alias at %s line %d column %d is forbidden; spell out the value", path, n.Line, n.Column)
	}
	if n.Tag != "" && n.Tag != "!!map" && n.Tag != "!!seq" && n.Tag != "!!str" && n.Tag != "!!null" {
		return fmt.Errorf("custom tag %q at %s line %d column %d is forbidden; use plain YAML values", n.Tag, path, n.Line, n.Column)
	}
	if n.Kind == yaml.MappingNode {
		if len(n.Content)%2 != 0 {
			return fmt.Errorf("mapping at %s line %d column %d has an unmatched key; provide key/value pairs", path, n.Line, n.Column)
		}
		seen := make(map[string]*yaml.Node, len(n.Content)/2)
		for index := 0; index < len(n.Content); index += 2 {
			key, value := n.Content[index], n.Content[index+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("mapping key at %s line %d column %d must be a scalar string", path, key.Line, key.Column)
			}
			childPath := path + "." + key.Value
			if first, duplicate := seen[key.Value]; duplicate {
				return fmt.Errorf("duplicate YAML key %q at %s line %d column %d; first declared at line %d column %d; remove the duplicate so one value is authoritative", key.Value, path, key.Line, key.Column, first.Line, first.Column)
			}
			seen[key.Value] = key
			if err := validateYAMLNode(key, childPath+".<key>"); err != nil {
				return err
			}
			if err := validateYAMLNode(value, childPath); err != nil {
				return err
			}
		}
		return nil
	}
	for index, c := range n.Content {
		if err := validateYAMLNode(c, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
	}
	return nil
}
