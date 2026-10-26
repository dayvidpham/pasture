package definitions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// EpochDocumentVersion is the explicit semantic revision of the epoch
// implementation document. Version 1 shipped in v0.0.21 and remains decodable;
// its published body digest is immutable. Version 2 is the current
// source-inventory refresh. A later generated source-inventory change is
// admitted at the same version by CompatibleSourceInventoryUpdate; any
// semantic change requires a new version. It is tracked independently of the
// DBOS recovery cohort the document carries: ordinary manifest changes never
// bump the cohort, and a cohort bump never silently rides a manifest change.
const EpochDocumentVersion uint32 = 2

// EpochDocumentID is the stable definition identity of the epoch
// implementation document. It does not change across versions and never
// carries an actor, epoch/task ID, timestamp, local path, or assignment.
const EpochDocumentID model.DefinitionID = "pasture.epoch.implementation"

// EpochCohort is the DBOS recovery cohort marker this binary executes. It is
// stated as a literal so the epoch document never imports the durable engine;
// the cohort agreement check proves it still equals the engine owner.
const EpochCohort = "1"

type epochDocument struct {
	Kind          model.DefinitionKind  `json:"kind"`
	ID            model.DefinitionID    `json:"id"`
	Version       uint32                `json:"version"`
	Codec         model.DefinitionCodec `json:"codec"`
	Specification EpochSpecification    `json:"specification"`
}

// EpochPhase records the execution shape of one protocol phase: its identity,
// number, owner roles, and valid transitions with their gate conditions. prose
// such as phase names and narratives is not execution shape and is excluded.
type EpochPhase struct {
	ID          string            `json:"id"`
	Number      int               `json:"number"`
	OwnerRoles  []string          `json:"owner_roles"`
	Transitions []EpochTransition `json:"transitions"`
}

// EpochTransition is one valid phase transition with its gate condition and
// optional action on transition.
type EpochTransition struct {
	To        string `json:"to"`
	Condition string `json:"condition"`
	Action    string `json:"action,omitempty"`
}

// EpochRole records one protocol role by its wire identity.
type EpochRole struct {
	ID string `json:"id"`
}

// EpochWorkflow records the execution shape of one protocol workflow: its
// identity, owning role, and stages with their execution mode and phase
// mapping. Stage prose and action detail are not execution shape.
type EpochWorkflow struct {
	ID     string       `json:"id"`
	Role   string       `json:"role"`
	Stages []EpochStage `json:"stages"`
}

// EpochStage is one workflow stage with its execution mode and optional phase
// mapping.
type EpochStage struct {
	ID        string `json:"id"`
	Order     int    `json:"order"`
	Execution string `json:"execution"`
	Phase     string `json:"phase,omitempty"`
}

// EpochSpecification is the shared pure descriptor of the binary's compiled
// epoch implementation: the typed protocol state machine plus the generated
// engine execution source manifest. It describes the binary, not any epoch
// instance.
type EpochSpecification struct {
	// Cohort is the DBOS recovery cohort marker. Recovery filters in-flight
	// epochs by executor and cohort, so a rebuilt binary with this marker
	// still recovers epochs an earlier build left in flight.
	Cohort string `json:"cohort"`
	// ProtocolTransitions are the canonical protocol reachability table:
	// every phase with the phases directly reachable from it.
	ProtocolTransitions map[string][]string `json:"protocol_transitions"`
	// ConsensusGated are the protocol transitions requiring all-ACCEPT
	// consensus, as from/to pairs.
	ConsensusGated [][2]string `json:"consensus_gated"`
	// BlockerGated are the protocol transitions blocked while blockers stand.
	BlockerGated [][2]string `json:"blocker_gated"`
	// ReviseDrivesBack are the review phases where a REVISE vote forces only
	// the backward transition.
	ReviseDrivesBack []string `json:"revise_drives_back"`
	// Phases are the codegen phase inventory with owner roles and gated
	// transitions, sorted by phase number.
	Phases []EpochPhase `json:"phases"`
	// Roles are the codegen role inventory by wire identity, sorted.
	Roles []EpochRole `json:"roles"`
	// Workflows are the codegen workflow inventory with execution stages,
	// sorted by workflow identity.
	Workflows []EpochWorkflow `json:"workflows"`
	// WriteClasses are the closed lifecycle write classes the gate admits,
	// in class order.
	WriteClasses []string `json:"write_classes"`
	// SignalTopics are the durable epoch signal topics, sorted.
	SignalTopics []string `json:"signal_topics"`
	// QueryNames are the read-only epoch state queries, sorted.
	QueryNames []string `json:"query_names"`
	// Queues are the durable work queues with their concurrency discipline.
	Queues []EpochQueue `json:"queues"`
	// Sources is the engine execution source manifest: every production epoch
	// execution source with its generation-time content hash, sorted by path.
	Sources []SourceEntry `json:"sources"`
}

// EpochQueue names one durable work queue and its concurrency bound.
type EpochQueue struct {
	Name        string `json:"name"`
	Concurrency string `json:"concurrency"`
}

// EpochSpecificationForBinary is the pure owner descriptor used by both the
// document generator and the epoch agreement checks. The source manifest comes
// from the committed generated inventory, never from a runtime filesystem
// read; every other literal names a value the protocol, gate, engine, or
// codegen owners enforce.
func EpochSpecificationForBinary() (EpochSpecification, error) {
	sources, err := EpochSourceInventory()
	if err != nil {
		return EpochSpecification{}, err
	}
	return AssembleEpochSpecification(sources)
}

// AssembleEpochSpecification builds the epoch specification around one source
// manifest. Explicit generation supplies the freshly hashed manifest; the
// binary variant supplies the committed embedded one. Both paths share this
// assembler so the committed bytes always match the hashed sources.
func AssembleEpochSpecification(sources []SourceEntry) (EpochSpecification, error) {
	return EpochSpecification{
		Cohort: EpochCohort,
		ProtocolTransitions: map[string][]string{
			"request": {"elicit"}, "elicit": {"propose"}, "propose": {"review"},
			"review": {"plan-review", "propose"}, "plan-review": {"ratify"}, "ratify": {"handoff"},
			"handoff": {"impl-plan"}, "impl-plan": {"worker-slices"}, "worker-slices": {"code-review"},
			"code-review": {"impl-uat", "worker-slices"}, "impl-uat": {"landing"}, "landing": {"complete"},
		},
		ConsensusGated:   [][2]string{{"review", "plan-review"}, {"code-review", "impl-uat"}},
		BlockerGated:     [][2]string{{"code-review", "impl-uat"}},
		ReviseDrivesBack: []string{"review", "code-review"},
		Phases: []EpochPhase{
			{ID: "request", Number: 1, OwnerRoles: []string{"epoch", "architect"}, Transitions: []EpochTransition{{To: "elicit", Condition: "classification confirmed, research and explore complete"}}},
			{ID: "elicit", Number: 2, OwnerRoles: []string{"epoch", "architect"}, Transitions: []EpochTransition{{To: "propose", Condition: "URD created with structured requirements"}}},
			{ID: "propose", Number: 3, OwnerRoles: []string{"epoch", "architect"}, Transitions: []EpochTransition{{To: "review", Condition: "proposal created"}}},
			{ID: "review", Number: 4, OwnerRoles: []string{"epoch", "architect", "reviewer"}, Transitions: []EpochTransition{{To: "plan-review", Condition: "all 3 reviewers vote ACCEPT"}, {To: "propose", Condition: "any reviewer votes REVISE", Action: "create PROPOSAL-{N+1}, mark current pasture:superseded"}}},
			{ID: "plan-review", Number: 5, OwnerRoles: []string{"epoch", "architect"}, Transitions: []EpochTransition{{To: "ratify", Condition: "user accepts plan"}, {To: "propose", Condition: "user requests changes", Action: "create PROPOSAL-{N+1}"}}},
			{ID: "ratify", Number: 6, OwnerRoles: []string{"epoch", "architect"}, Transitions: []EpochTransition{{To: "handoff", Condition: "proposal ratified, IMPL_PLAN placeholder created"}}},
			{ID: "handoff", Number: 7, OwnerRoles: []string{"epoch", "architect", "supervisor"}, Transitions: []EpochTransition{{To: "impl-plan", Condition: "handoff authored in the HANDOFF Pasture task body"}}},
			{ID: "impl-plan", Number: 8, OwnerRoles: []string{"epoch", "supervisor"}, Transitions: []EpochTransition{{To: "worker-slices", Condition: "all slices created with leaf tasks, assigned, and dependency-chained"}}},
			{ID: "worker-slices", Number: 9, OwnerRoles: []string{"epoch", "supervisor", "worker"}, Transitions: []EpochTransition{{To: "code-review", Condition: "all slices complete, quality gates pass"}}},
			{ID: "code-review", Number: 10, OwnerRoles: []string{"epoch", "supervisor", "reviewer"}, Transitions: []EpochTransition{{To: "impl-uat", Condition: "all 3 reviewers ACCEPT, all BLOCKERs resolved"}, {To: "worker-slices", Condition: "any reviewer votes REVISE", Action: "fix BLOCKERs in affected slices, then re-review"}}},
			{ID: "impl-uat", Number: 11, OwnerRoles: []string{"epoch", "supervisor"}, Transitions: []EpochTransition{{To: "landing", Condition: "user accepts implementation"}, {To: "worker-slices", Condition: "user requests changes", Action: "create fix tasks in affected slices"}}},
			{ID: "landing", Number: 12, OwnerRoles: []string{"epoch", "supervisor"}, Transitions: []EpochTransition{{To: "complete", Condition: "git push succeeds, all tasks closed or dependency-resolved"}}},
		},
		Roles: []EpochRole{{ID: "architect"}, {ID: "epoch"}, {ID: "reviewer"}, {ID: "supervisor"}, {ID: "worker"}},
		Workflows: []EpochWorkflow{
			{ID: "architect-state-flow", Role: "architect", Stages: []EpochStage{{ID: "asf-request", Order: 1, Execution: "sequential", Phase: "request"}, {ID: "asf-elicit", Order: 2, Execution: "sequential", Phase: "elicit"}, {ID: "asf-propose", Order: 3, Execution: "sequential", Phase: "propose"}, {ID: "asf-review", Order: 4, Execution: "conditional-loop", Phase: "review"}, {ID: "asf-uat", Order: 5, Execution: "sequential", Phase: "plan-review"}, {ID: "asf-ratify", Order: 6, Execution: "sequential", Phase: "ratify"}, {ID: "asf-handoff", Order: 7, Execution: "sequential", Phase: "handoff"}}},
			{ID: "layer-cake", Role: "worker", Stages: []EpochStage{{ID: "lc-types", Order: 1, Execution: "sequential", Phase: "worker-slices"}, {ID: "lc-tests", Order: 2, Execution: "sequential", Phase: "worker-slices"}, {ID: "lc-impl", Order: 3, Execution: "sequential", Phase: "worker-slices"}}},
			{ID: "ride-the-wave", Role: "supervisor", Stages: []EpochStage{{ID: "rtw-plan", Order: 1, Execution: "sequential", Phase: "impl-plan"}, {ID: "rtw-build", Order: 2, Execution: "parallel", Phase: "worker-slices"}, {ID: "rtw-review-fix", Order: 3, Execution: "conditional-loop", Phase: "code-review"}}},
		},
		WriteClasses: []string{"delivery-receipt", "definition-activation", "lineage-links", "disclosure"},
		SignalTopics: []string{"advance_phase", "complete_slice", "register_session", "slice_progress", "start_slice", "submit_vote"},
		QueryNames:   []string{"active_sessions", "available_transitions", "current_state", "full_state", "slice_progress_state"},
		Queues: []EpochQueue{
			{Name: "pasture-slice-queue", Concurrency: "configurable slice budget shared by slices and reviews"},
			{Name: "pasture-control-queue", Concurrency: "one workflow at a time by design"},
		},
		Sources: sources,
	}, nil
}

// GenerateEpochBody is the production owner codec used by explicit
// generation. It never reads source files or a store at runtime: the source
// manifest travels in the committed generated inventory.
func GenerateEpochBody() ([]byte, error) {
	spec, err := EpochSpecificationForBinary()
	if err != nil {
		return nil, err
	}
	return AssembleEpochBody(spec)
}

// AssembleEpochBody encodes one epoch specification as the canonical owner
// body. The generator and the round-trip checks share this assembler.
func AssembleEpochBody(spec EpochSpecification) ([]byte, error) {
	raw, err := marshalDocument(epochDocument{model.DefinitionEpochImplementation, EpochDocumentID, EpochDocumentVersion, model.DefinitionCanonicalJSON, spec})
	if err != nil {
		return nil, failure("AssembleEpochBody", "The typed epoch specification could not be encoded.", err)
	}
	return NormalizePayload(EvidenceKind, raw)
}

// NewEpochImplementation returns the compiled epoch owner document from its
// generated body. Historical bodies remain decodable; only this constructor
// provides a document the activation writer accepts.
func NewEpochImplementation() (Document, error) {
	body, err := generatedBody("generated/epoch.json")
	if err != nil {
		return Document{}, err
	}
	document, err := decodeEpoch(body)
	if err != nil {
		return Document{}, err
	}
	if document.ID() != EpochDocumentID {
		return Document{}, failure("NewEpochImplementation", "The embedded document does not identify the epoch implementation; regenerate the epoch document.", nil)
	}
	document.ownerBuilt = true
	return document, nil
}

// ParseEpochSpecification strictly decodes one schema owner body and
// returns its shared specification. Readers and agreement checks consume the
// specification through this parser, never by reimplementing the codec.
func ParseEpochSpecification(body []byte) (EpochSpecification, error) {
	var wire epochDocument
	if err := strictDecode(body, &wire); err != nil {
		return EpochSpecification{}, failure("ParseEpochSpecification", "The typed epoch document contains malformed, duplicate or unknown fields.", err)
	}
	doc, err := decodeEpoch(body)
	if err != nil {
		return EpochSpecification{}, err
	}
	if doc.ID() != EpochDocumentID {
		return EpochSpecification{}, failure("ParseEpochSpecification", "The document does not identify the epoch owner.", nil)
	}
	return wire.Specification, nil
}

func decodeEpoch(payload []byte) (Document, error) {
	var wire epochDocument
	if err := strictDecode(payload, &wire); err != nil {
		return Document{}, failure("decodeEpoch", "The typed epoch document contains malformed, duplicate or unknown fields.", err)
	}
	// Version 1 shipped in v0.0.21 and remains a historical codec arm.
	// Version 2 is the current source-inventory refresh. Add a new arm
	// rather than replacing a released decoder.
	switch wire.Version {
	case 1:
	case 2:
	default:
		return Document{}, failure("decodeEpoch", "The epoch document version has no supported historical codec.", nil)
	}
	if wire.Kind != model.DefinitionEpochImplementation || (wire.Version != 1 && wire.Version != 2) || wire.Codec != model.DefinitionCanonicalJSON || wire.ID != EpochDocumentID {
		return Document{}, failure("decodeEpoch", "Epoch document kind, owner identity, codec or version is invalid.", fmt.Errorf("kind=%d version=%d codec=%d id=%q", wire.Kind, wire.Version, wire.Codec, wire.ID))
	}
	if err := validateEpochSpecification(wire.Specification); err != nil {
		return Document{}, failure("decodeEpoch", "The epoch descriptor violates its structural contract.", err)
	}
	normalized, err := NormalizePayload(EvidenceKind, payload)
	if err != nil {
		return Document{}, err
	}
	second, err := NormalizePayload(EvidenceKind, normalized)
	if err != nil || !bytes.Equal(normalized, second) {
		return Document{}, failure("decodeEpoch", "Journal normalization is not idempotent.", err)
	}
	return Document{kind: wire.Kind, id: wire.ID, version: wire.Version, codec: wire.Codec, body: normalized, content: model.ContentIdentity(sha256.Sum256(normalized))}, nil
}

func validateEpochSpecification(spec EpochSpecification) error {
	if spec.Cohort == "" {
		return fmt.Errorf("epoch descriptor names no recovery cohort")
	}
	if len(spec.ProtocolTransitions) == 0 || len(spec.Phases) == 0 || len(spec.Roles) == 0 || len(spec.Workflows) == 0 {
		return fmt.Errorf("epoch descriptor protocol inventory is empty")
	}
	known := make(map[string]bool, len(spec.Phases))
	var numbers []int
	for _, phase := range spec.Phases {
		if phase.ID == "" || phase.Number <= 0 || known[phase.ID] {
			return fmt.Errorf("epoch descriptor phase identity is invalid or duplicated")
		}
		known[phase.ID] = true
		numbers = append(numbers, phase.Number)
		if len(phase.OwnerRoles) == 0 {
			return fmt.Errorf("epoch descriptor phase %q names no owner role", phase.ID)
		}
		for _, transition := range phase.Transitions {
			if transition.To == "" || transition.Condition == "" {
				return fmt.Errorf("epoch descriptor transition from %q is missing its target or gate condition", phase.ID)
			}
		}
	}
	slices.Sort(numbers)
	for index, number := range numbers {
		if number != index+1 {
			return fmt.Errorf("epoch descriptor phase numbers are not the dense 1..%d sequence", len(numbers))
		}
	}
	// Complete is the terminal protocol phase: transitions may reach it, but
	// it owns no phase inventory entry because nothing executes there.
	const terminalPhase = "complete"
	for from, tos := range spec.ProtocolTransitions {
		if !known[from] {
			return fmt.Errorf("protocol transition table leaves phase %q", from)
		}
		for _, to := range tos {
			if to != terminalPhase && !known[to] {
				return fmt.Errorf("protocol transition table reaches unknown phase %q", to)
			}
		}
	}
	for _, pair := range spec.ConsensusGated {
		if !known[pair[0]] || !known[pair[1]] {
			return fmt.Errorf("consensus-gated transition %q names an unknown phase", pair)
		}
	}
	for _, pair := range spec.BlockerGated {
		if !known[pair[0]] || !known[pair[1]] {
			return fmt.Errorf("blocker-gated transition %q names an unknown phase", pair)
		}
	}
	for _, phase := range spec.ReviseDrivesBack {
		if !known[phase] {
			return fmt.Errorf("revise-drives-back phase %q is unknown", phase)
		}
	}
	// Every signal topic and query name must parse against the protocol's own
	// closed sets; a renamed topic fails here, not at delivery time.
	for _, topic := range spec.SignalTopics {
		if !protocol.SignalTopic(topic).IsValid() {
			return fmt.Errorf("epoch descriptor signal topic %q is not a protocol topic", topic)
		}
	}
	for _, name := range spec.QueryNames {
		if _, ok := protocol.ParseQueryName(name); !ok {
			return fmt.Errorf("epoch descriptor query name %q is not a protocol query", name)
		}
	}
	// Every write class must be an admitted gate class, and together they must
	// name the complete closed set the gate admits today; the gate owns the
	// set, so a renamed or added class fails here instead of rotting.
	seenClasses := make(map[gate.WriteClass]bool, len(spec.WriteClasses))
	for _, class := range spec.WriteClasses {
		parsed, ok := gate.ParseWriteClass(class)
		if !ok || seenClasses[parsed] {
			return fmt.Errorf("epoch descriptor write class %q is not admitted by the gate", class)
		}
		seenClasses[parsed] = true
	}
	for class := gate.WriteDeliveryReceipt; class <= gate.WriteDisclosure; class++ {
		if !class.IsValid() || !seenClasses[class] {
			return fmt.Errorf("epoch descriptor does not name the complete closed write class set")
		}
	}
	if len(spec.Queues) == 0 {
		return fmt.Errorf("epoch descriptor names no durable work queues")
	}
	return validateSources("epoch", spec.Sources)
}

// SourceEntry is one generation-time engine/interpreter source with its
// content hash. Paths are repository-relative with forward slashes; hashes
// are lowercase hex over the exact file bytes the generator read.
type SourceEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// validateSources enforces the generation contract every source manifest
// obeys: repository-relative sorted unique paths with well-formed hashes and
// no absolute paths, test files, or checkout dirt.
func validateSources(owner string, entries []SourceEntry) error {
	if len(entries) == 0 {
		return fmt.Errorf("%s descriptor source manifest is empty", owner)
	}
	seen := make(map[string]bool, len(entries))
	previous := ""
	for _, entry := range entries {
		if entry.Path == "" || seen[entry.Path] {
			return fmt.Errorf("%s descriptor source path %q is empty or duplicated", owner, entry.Path)
		}
		seen[entry.Path] = true
		if previous != "" && entry.Path <= previous {
			return fmt.Errorf("%s descriptor source manifest is not sorted at %q", owner, entry.Path)
		}
		previous = entry.Path
		if !isRepositoryRelative(entry.Path) {
			return fmt.Errorf("%s descriptor source path %q is not repository-relative", owner, entry.Path)
		}
		digest, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("%s descriptor source hash for %q is not a sha256 hex digest", owner, entry.Path)
		}
		if strings.ToLower(entry.SHA256) != entry.SHA256 {
			return fmt.Errorf("%s descriptor source hash for %q is not lowercase hex", owner, entry.Path)
		}
	}
	return nil
}

func isRepositoryRelative(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.Contains(path, "..") {
		return false
	}
	if strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/testdata/") {
		return false
	}
	return strings.HasSuffix(path, ".go")
}

// EpochSourceInventory returns the committed generation-time engine execution
// source manifest. It reads the checked-in inventory, never the filesystem.
func EpochSourceInventory() ([]SourceEntry, error) {
	return readSourceInventory("generated/epoch-sources.json")
}

func readSourceInventory(name string) ([]SourceEntry, error) {
	raw, err := generatedBody(name)
	if err != nil {
		return nil, err
	}
	var entries []SourceEntry
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, failure("readSourceInventory", fmt.Sprintf("The committed %s inventory is malformed.", name), err)
	}
	return entries, nil
}
