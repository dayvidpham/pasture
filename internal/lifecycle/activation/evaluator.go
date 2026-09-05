package activation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	digest "github.com/opencontainers/go-digest"
)

// Evaluator evaluates corpus cases against ONE harness's pinned contract: its
// host-version admission, its registration manifest (native event name to
// generated event kind) and its activation target set. Build one with
// ClaudeCodeEvaluator, CodexEvaluator or OpenCodeEvaluator, or select one by
// harness with EvaluatorFor. A fixture whose sidecar names another harness is
// refused by name, never evaluated against the wrong contract. The zero value
// is invalid and evaluates nothing.
//
// VERSION ADMISSION follows each harness's declared contract, and the shape
// differs per harness: Claude Code admits a RANGE of host versions (the pinned
// contract is a range contract, so a patch release inside it is admitted),
// while Codex and OpenCode admit EXACTLY their pinned version. The admission
// is judged here, on a fixture whose sidecar records the host version as a
// fact, and nowhere on the live hook path, where a host may pass no usable
// version at all.
type Evaluator struct {
	harness     acceptance.HarnessKind
	contract    ir.RuntimeContractID
	admission   runtime.VersionConstraint
	events      []registration.Event
	targets     []model.ContractEventKind
	constructed bool
}

func newEvaluator[E comparable](harness acceptance.HarnessKind, contract runtime.LifecycleContract[E], manifest registration.Manifest, declarations []targetEventDeclaration) Evaluator {
	return Evaluator{
		harness:     harness,
		contract:    contract.ID(),
		admission:   contract.Versions(),
		events:      manifest.Entries(),
		targets:     targetEvents(declarations),
		constructed: true,
	}
}

// Admission returns the host-version constraint the evaluator admits.
func (e Evaluator) Admission() runtime.VersionConstraint { return e.admission }

// AdmitsExactly reports whether the admission is one exact host version (Codex,
// OpenCode) rather than a range (Claude Code).
func (e Evaluator) AdmitsExactly() bool {
	return runtime.ComparePrecedence(e.admission.Min(), e.admission.Max()) == 0
}

// describeAdmission spells a constraint for a reader: "exactly X" for a pinned
// point, "from X through Y" for a range.
func describeAdmission(c runtime.VersionConstraint) string {
	if runtime.ComparePrecedence(c.Min(), c.Max()) == 0 {
		return fmt.Sprintf("exactly %s", c.Min())
	}
	return fmt.Sprintf("from %s through %s", c.Min(), c.Max())
}

// ClaudeCodeEvaluator evaluates against the pinned Claude Code contract.
func ClaudeCodeEvaluator() Evaluator {
	return newEvaluator(acceptance.HarnessClaudeCode, runtime.ClaudeCode2_1_210Lifecycle(), registration.ClaudeCode2_1_210(), claudeTargetEventDeclarations[:])
}

// CodexEvaluator evaluates against the pinned Codex contract.
func CodexEvaluator() Evaluator {
	return newEvaluator(acceptance.HarnessCodexCLI, runtime.Codex0_146_0Lifecycle(), registration.Codex0_146_0(), codexTargetEventDeclarations[:])
}

// OpenCodeEvaluator evaluates against the pinned OpenCode contract.
func OpenCodeEvaluator() Evaluator {
	return newEvaluator(acceptance.HarnessOpenCode, runtime.OpenCode1_18_10Lifecycle(), registration.OpenCode1_18_10(), openCodeTargetEventDeclarations[:])
}

// EvaluatorFor selects the evaluator of one harness. The set is closed: a
// harness without a pinned lifecycle contract has no evaluator.
func EvaluatorFor(harness acceptance.HarnessKind) (Evaluator, error) {
	switch harness {
	case acceptance.HarnessClaudeCode:
		return ClaudeCodeEvaluator(), nil
	case acceptance.HarnessCodexCLI:
		return CodexEvaluator(), nil
	case acceptance.HarnessOpenCode:
		return OpenCodeEvaluator(), nil
	default:
		return Evaluator{}, fmt.Errorf("activation.EvaluatorFor: harness %q has no activation evaluator; evaluators exist for the pinned contracts of %q, %q and %q only", harness, acceptance.HarnessClaudeCode, acceptance.HarnessCodexCLI, acceptance.HarnessOpenCode)
	}
}

func (e Evaluator) IsValid() bool                   { return e.constructed }
func (e Evaluator) Harness() acceptance.HarnessKind { return e.harness }
func (e Evaluator) Contract() ir.RuntimeContractID  { return e.contract }

// TargetEvents returns the harness's activation target set as a fresh slice.
func (e Evaluator) TargetEvents() []model.ContractEventKind {
	out := make([]model.ContractEventKind, len(e.targets))
	copy(out, e.targets)
	return out
}

// captureFixtureSuffix is the fixture spelling of a callback-object capture
// (OpenCode); its sidecar sits beside it under the plain name.
const captureFixtureSuffix = ".capture.json"

// ProvenancePath returns the sidecar path of a fixture: the fixture's path
// with ".capture.json" or ".json" replaced by ".provenance.json".
func ProvenancePath(fixture string) string {
	if strings.HasSuffix(fixture, captureFixtureSuffix) {
		return strings.TrimSuffix(fixture, captureFixtureSuffix) + ".provenance.json"
	}
	return strings.TrimSuffix(fixture, filepath.Ext(fixture)) + ".provenance.json"
}

// Evaluate evaluates one immutable case against contained fixture evidence,
// under this evaluator's harness contract. Every rejection category reports
// as a withheld Evaluation; a fixture the evaluator cannot read or that
// belongs to another harness is an error.
func (e Evaluator) Evaluate(root string, c Case) (Evaluation, error) {
	if !e.constructed {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: evaluator is not constructed; build it with ClaudeCodeEvaluator, CodexEvaluator, OpenCodeEvaluator or EvaluatorFor")
	}
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
	provenance, escaped, err := containedPath(rootAbs, ProvenancePath(fixture))
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: resolve provenance for case %q: %w", c.name, err)
	}
	if escaped {
		return withheldEvaluation(c.name, CorpusReasonPathEscape), nil
	}
	body, err := readBounded(fixture, MaxFixtureBytes)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: fixture for case %q: %w", c.name, err)
	}
	praw, err := readBounded(provenance, MaxProvenanceBytes)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance for case %q: %w", c.name, err)
	}
	var p acceptance.CaptureProvenance
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
	if digest.FromBytes(body) != want {
		return withheldEvaluation(c.name, CorpusReasonDigestMismatch), nil
	}
	// The harness is checked before the version: a sidecar from another
	// harness is a category error, and a version verdict against the wrong
	// contract would name a problem the fixture does not have.
	if p.Harness != e.harness {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q names harness %q, not %q; this evaluator is bound to the %s contract %s, so evaluate the fixture with the %q evaluator or move it to that harness's corpus", provenance, p.Harness, e.harness, e.harness, e.contract, p.Harness)
	}
	version, err := runtime.ParseHostVersion(p.HarnessVersion)
	if err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q has malformed host version %q: %w", provenance, p.HarnessVersion, err)
	}
	if !e.admission.Allows(version) {
		return withheldEvaluationWithDetail(c.name, CorpusReasonVersionOutOfRange,
			fmt.Sprintf("observed host version %q is outside the admitted %s versions, %s", p.HarnessVersion, e.harness, describeAdmission(e.admission))), nil
	}
	var event model.ContractEventKind
	found := false
	for _, entry := range e.events {
		if entry.NativeName == p.Event {
			event = entry.Kind
			found = true
			break
		}
	}
	if !found {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance %q names empty or unknown event %q; use an exact generated %s native event of contract %s", provenance, p.Event, e.harness, e.contract)
	}
	target := false
	for _, candidate := range e.targets {
		if candidate == event {
			target = true
			break
		}
	}
	if !target {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: provenance event %q is generated but outside the %s activation target set; capture one of the %d declared %s targets", p.Event, e.harness, len(e.targets), e.harness)
	}
	if err := p.ValidateCommittedFixtureBytes(fixture, body); err != nil {
		return Evaluation{}, fmt.Errorf("activation.Evaluate: final fixture validation failed for case %q: %w", c.name, err)
	}
	return Evaluation{caseName: c.name, event: event, decision: DecisionEnabled, reason: CorpusReasonNone, eventPresent: true, constructed: true}, nil
}

func withheldEvaluation(name string, reason CorpusReason) Evaluation {
	return Evaluation{caseName: name, decision: DecisionWithheld, reason: reason, constructed: true}
}

// withheldEvaluationWithDetail is a withheld evaluation that carries the
// sentence a reader needs to act on the reason.
func withheldEvaluationWithDetail(name string, reason CorpusReason, detail string) Evaluation {
	out := withheldEvaluation(name, reason)
	out.detail = detail
	return out
}

// readBounded reads one contained file up to bound bytes and refuses a longer
// one, so an oversized capture can never be admitted by truncation.
func readBounded(path string, bound int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open contained file %q: %w", path, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, int64(bound)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read bounded file %q: %w", path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close file %q after bounded read: %w", path, closeErr)
	}
	if len(body) > bound {
		return nil, fmt.Errorf("file %q exceeds the %d-byte native payload bound; reduce or reject the capture", path, bound)
	}
	return body, nil
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
