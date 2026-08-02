package activation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	digest "github.com/opencontainers/go-digest"
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
		Name  string `yaml:"name"`
		Input struct {
			Fixture string `yaml:"fixture"`
		} `yaml:"input"`
		Expected struct {
			Decision Decision     `yaml:"decision"`
			Reason   CorpusReason `yaml:"reason"`
		} `yaml:"expected"`
		Classification Classification `yaml:"classification"`
		Provenance     struct {
			Source ProvenanceSource `yaml:"source"`
			Ref    string           `yaml:"ref"`
		} `yaml:"provenance"`
		Mutation struct {
			Description string `yaml:"description"`
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
		fields := []string{r.Name, r.Input.Fixture, r.Provenance.Ref, r.Mutation.Description}
		for _, v := range fields {
			if strings.TrimSpace(v) == "" || len(v) > MaxFieldBytes {
				return Corpus{}, fmt.Errorf("activation.LoadCorpus: case %d has a missing or oversized required string; provide non-empty fields no larger than %d bytes", i, MaxFieldBytes)
			}
		}
		if _, ok := seen[r.Name]; ok {
			return Corpus{}, fmt.Errorf("activation.LoadCorpus: duplicate case name %q at case %d; names must be unique", r.Name, i)
		}
		seen[r.Name] = struct{}{}
		c := Case{name: r.Name, fixture: r.Input.Fixture, expectedDecision: r.Expected.Decision, expectedReason: r.Expected.Reason, classification: r.Classification, provenanceSource: r.Provenance.Source, provenanceReference: r.Provenance.Ref, mutationDescription: r.Mutation.Description, constructed: true}
		if !c.IsValid() {
			return Corpus{}, fmt.Errorf("activation.LoadCorpus: case %q has inconsistent classification, decision, reason, or enum values; must-pass requires enabled/no reason and must-fail requires withheld/a bypass reason", r.Name)
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

// Evaluate evaluates one immutable case against contained fixture evidence.
func Evaluate(root string, c Case) (Evaluation, error) {
	if !c.IsValid() {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: case is not constructed by LoadCorpus; load and select a valid corpus case")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: resolve root %q: %w", root, err)
	}
	fixture, escaped, err := containedPath(rootAbs, c.fixture)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: resolve fixture for case %q: %w", c.name, err)
	}
	if escaped {
		return withheldEvaluation(c.name, CorpusReasonPathEscape), nil
	}
	provenance := strings.TrimSuffix(fixture, filepath.Ext(fixture)) + ".provenance.json"
	provenance, escaped, err = containedPath(rootAbs, provenance)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: resolve provenance for case %q: %w", c.name, err)
	}
	if escaped {
		return withheldEvaluation(c.name, CorpusReasonPathEscape), nil
	}
	body, err := os.ReadFile(fixture)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: read contained fixture %q for case %q: %w", fixture, c.name, err)
	}
	praw, err := os.ReadFile(provenance)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: read contained provenance %q for case %q: %w", provenance, c.name, err)
	}
	if len(praw) > MaxProvenanceBytes {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q exceeds %d bytes; reduce it", provenance, MaxProvenanceBytes)
	}
	type envelope struct {
		acceptance.CaptureProvenance
		Event string `json:"event"`
	}
	var p envelope
	jd := json.NewDecoder(bytes.NewReader(praw))
	if err := jd.Decode(&p); err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: decode provenance %q: %w", provenance, err)
	}
	var extra any
	if err := jd.Decode(&extra); !errors.Is(err, io.EOF) {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q must contain exactly one JSON object", provenance)
	}
	if p.Origin != acceptance.OriginAuthenticCapture {
		return withheldEvaluation(c.name, CorpusReasonNonAuthenticOrigin), nil
	}
	want, err := digest.Parse(p.RawFileDigest)
	if err != nil || want.Algorithm() != digest.SHA256 {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q has malformed SHA-256 digest %q; record sha256:<hex>", provenance, p.RawFileDigest)
	}
	sum := sha256.Sum256(body)
	if want.Encoded() != hex.EncodeToString(sum[:]) {
		return withheldEvaluation(c.name, CorpusReasonDigestMismatch), nil
	}
	version, err := runtime.ParseHostVersion(p.HarnessVersion)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q has malformed host version %q: %w", provenance, p.HarnessVersion, err)
	}
	if !runtime.ClaudeCode2_1_210Lifecycle().Supports(version) {
		return withheldEvaluation(c.name, CorpusReasonVersionOutOfRange), nil
	}
	if p.Harness != acceptance.HarnessClaudeCode {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q names harness %q, not claude-code; capture with the pinned Claude harness", provenance, p.Harness)
	}
	var event model.ContractEventKind
	found := false
	for _, entry := range registration.ClaudeCode2_1_210().Entries() {
		if entry.NativeName == p.Event {
			event = entry.Kind
			found = true
			break
		}
	}
	if !found {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q names empty or unknown event %q; use an exact generated Claude native event", provenance, p.Event)
	}
	target := false
	for _, candidate := range ClaudeCode2_1_210TargetEvents() {
		if candidate == event {
			target = true
			break
		}
	}
	if !target {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance event %q is generated but outside the activation target set; capture one of the ten declared targets", p.Event)
	}
	if err := p.CaptureProvenance.ValidateFixture(rootAbs, c.fixture); err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: final fixture validation failed for case %q: %w", c.name, err)
	}
	return Evaluation{caseName: c.name, event: event, decision: DecisionEnabled, reason: CorpusReasonNone, eventPresent: true, constructed: true}, nil
}

func withheldEvaluation(name string, reason CorpusReason) Evaluation {
	return Evaluation{caseName: name, decision: DecisionWithheld, reason: reason, constructed: true}
}

func containedPath(root, candidate string) (string, bool, error) {
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", false, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs, true, nil
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false, err
	}
	rel, err = filepath.Rel(root, resolved)
	if err != nil {
		return "", false, err
	}
	return resolved, rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

func rejectYAMLFeatures(n *yaml.Node) error {
	if n.Alias != nil {
		return fmt.Errorf("aliases are forbidden")
	}
	if n.Tag != "" && n.Tag != "!!map" && n.Tag != "!!seq" && n.Tag != "!!str" && n.Tag != "!!null" {
		return fmt.Errorf("explicit/custom tag %q is forbidden", n.Tag)
	}
	for _, c := range n.Content {
		if err := rejectYAMLFeatures(c); err != nil {
			return err
		}
	}
	return nil
}
