// Package context builds the Pasture-side context-disclosure projection: a
// bounded, canonical summary of committed lifecycle records, per-host chain
// summaries derived from committed links, and the codebook coordinates present
// in the interpreted evidence.
//
// The package is named context but imports NO standard library context: the
// projection is PURE. Project takes already-read committed inputs and returns a
// constructor-owned ContextProjection; it holds no clock, store, or I/O and does
// not consult std context, so importers alias this package as lifecyclecontext
// to avoid colliding with the standard library.
//
// Disclosure is the heaviest surface per unit of payoff, so this package is
// deliberately LEAN (Stage-3 M5 axis-C directive). It exposes exactly one
// projection shape under a single static policy — disclose committed lifecycle
// records, links, and codebook coordinates only. There is no alternate policy to
// select (ContextPolicyDefinitionRef stays a staked seam) and there are no
// speculative accessors: the disclosure command reads what Project returns, and
// the durable plan fact fingerprints it.
package context

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// MaxProjectionRecords bounds the number of record summaries a single
// disclosure projection carries. A selection wider than this is disclosed
// truncated (with an explicit marker) rather than releasing an unbounded
// projection — the same "reads stay bounded" discipline the lifecycle readers
// use.
const MaxProjectionRecords = model.MaxPageSize

// ContextInput selects the bounded committed state a disclosure projects. Scope
// is the OccurrenceQuery whose fingerprint becomes the durable plan scope; the
// records and links Project summarizes are read against this same selection by
// the disclosure command before Project is called.
//
// The single static M5 policy — disclose committed lifecycle records, links, and
// codebook coordinates only — is implicit: ContextInput carries no policy
// selector because no alternate policy exists to choose.
type ContextInput struct {
	Scope model.OccurrenceQuery
}

// recordSummary is the bounded disclosure view of one committed lifecycle
// record: its occurrence journal identity, typed event kind, host, capture
// disposition, and — when the interpretation is codebook-resolved (interpreted
// .v2) — the hex content identity of the codebook it was interpreted against.
// A pre-M5 (interpreted.v1) record has an empty Codebook, disclosing "codebook
// unresolved" rather than inventing one.
type recordSummary struct {
	Occurrence int64  `json:"occurrence"`
	Event      uint16 `json:"event"`
	Harness    string `json:"harness"`
	Capture    uint8  `json:"capture"`
	Codebook   string `json:"codebook"`
}

// chainSummary is the disclosure view of one per-host native-identity chain: the
// harness, identity kind, identity value, and the number of committed
// predecessor edges (LinkRecords) that thread occurrences inside the disclosed
// selection. Chains are derived from committed links only; Project never
// materializes an edge.
type chainSummary struct {
	Harness string `json:"harness"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	Edges   int    `json:"edges"`
}

// codebookSummary is the disclosure view of one codebook coordinate present in
// the disclosed interpreted evidence, deduplicated across records.
type codebookSummary struct {
	ID      string `json:"id"`
	Version uint32 `json:"version"`
	Content string `json:"content"`
}

// projectionWire is the canonical, deterministically-ordered JSON shape of a
// ContextProjection. Its sha256 is the projection content digest that the
// durable plan fact records and the gate intent fingerprints, so its field set
// and ordering are load-bearing: any change alters every disclosure digest.
type projectionWire struct {
	Scope     string            `json:"scope"`
	Records   []recordSummary   `json:"records"`
	Chains    []chainSummary    `json:"chains"`
	Codebooks []codebookSummary `json:"codebooks"`
	Truncated bool              `json:"truncated"`
}

// ContextProjection is the constructor-owned, bounded disclosure projection. Its
// contents are unexported and exposed only through the canonical MarshalJSON
// (whose digest is the released content), ScopeFingerprint, and Digest — there
// are no field accessors, keeping the surface lean. Only Project constructs a
// valid projection; the zero value marshals to nothing and every disclosure
// surface refuses it.
type ContextProjection struct {
	scope       model.ContentIdentity
	records     []recordSummary
	chains      []chainSummary
	codebooks   []codebookSummary
	truncated   bool
	constructed bool
}

// IsValid reports whether the projection was produced by Project. A zero
// projection is never valid, so it can never be disclosed or committed.
func (p ContextProjection) IsValid() bool { return p.constructed }

// ScopeFingerprint returns the fingerprint of the disclosed selection (the
// OccurrenceQuery), which the durable plan fact records as its scope. It is
// stable across disclosures of the same selection and independent of the
// committed contents.
func (p ContextProjection) ScopeFingerprint() model.ContentIdentity { return p.scope }

// canonicalWire renders the projection to its canonical JSON bytes: escape-free,
// trailing-newline-free, with every slice already in a total order fixed by
// Project. Identical projections always render identical bytes, so the digest is
// stable.
func (p ContextProjection) canonicalWire() ([]byte, error) {
	wire := projectionWire{
		Scope:     hex.EncodeToString(p.scope[:]),
		Records:   p.records,
		Chains:    p.chains,
		Codebooks: p.codebooks,
		Truncated: p.truncated,
	}
	if wire.Records == nil {
		wire.Records = []recordSummary{}
	}
	if wire.Chains == nil {
		wire.Chains = []chainSummary{}
	}
	if wire.Codebooks == nil {
		wire.Codebooks = []codebookSummary{}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(wire); err != nil {
		return nil, fmt.Errorf("encode context disclosure projection: %w", err)
	}
	return append([]byte(nil), bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})...), nil
}

// MarshalJSON returns the canonical projection bytes. It is the sole content
// accessor: the disclosure command prints these bytes and the durable plan fact
// records their digest, so "what is released" and "what is fingerprinted" are by
// construction the same bytes.
func (p ContextProjection) MarshalJSON() ([]byte, error) {
	if !p.constructed {
		return nil, fmt.Errorf("marshal context disclosure projection: the projection is not constructed; build it with context.Project")
	}
	return p.canonicalWire()
}

// Digest returns the sha256 content identity of the canonical projection — the
// projection content digest recorded in the durable plan fact and presented to
// the write gate as the disclosure scope.
func (p ContextProjection) Digest() (model.ContentIdentity, error) {
	wire, err := p.canonicalWire()
	if err != nil {
		return model.ContentIdentity{}, err
	}
	return model.ContentIdentity(sha256.Sum256(wire)), nil
}

// chainKey is the per-host native-identity chain address a committed link
// belongs to.
type chainKey struct {
	harness string
	kind    string
	value   string
}

// Project is the PURE context-disclosure projection. Given the disclosed
// selection and the already-read committed records and links, it returns a
// bounded ContextProjection: a canonical, deterministically-ordered summary of
// the records, the per-host identity chains (edge counts derived from committed
// links whose endpoints fall inside the disclosed record set), and the codebook
// coordinates present in the interpreted evidence.
//
// It performs no I/O, holds no clock or store, and consults no std context, so
// re-running it over identical inputs yields a byte-identical projection and a
// stable digest. Records beyond MaxProjectionRecords are dropped and the
// projection is explicitly marked truncated rather than released unbounded.
func Project(input ContextInput, records []model.LifecycleRecord, links []model.LinkRecord) (ContextProjection, error) {
	scope := scopeFingerprint(input.Scope)

	ordered := append([]model.LifecycleRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Occurrence.JournalID() < ordered[j].Occurrence.JournalID()
	})

	truncated := false
	if len(ordered) > MaxProjectionRecords {
		ordered = ordered[:MaxProjectionRecords]
		truncated = true
	}

	inScope := make(map[int64]struct{}, len(ordered))
	summaries := make([]recordSummary, 0, len(ordered))
	codebookSeen := make(map[codebookSummary]struct{})
	var codebooks []codebookSummary
	for _, record := range ordered {
		occurrence := record.Occurrence
		id := int64(occurrence.JournalID())
		inScope[id] = struct{}{}

		codebookHex := ""
		for _, interpreted := range record.Interpreted() {
			if coordinate, ok := interpreted.Codebook(); ok && coordinate.IsValid() {
				codebookHex = hex.EncodeToString(coordinate.Content[:])
				summary := codebookSummary{
					ID:      string(coordinate.ID),
					Version: coordinate.Version,
					Content: codebookHex,
				}
				if _, dup := codebookSeen[summary]; !dup {
					codebookSeen[summary] = struct{}{}
					codebooks = append(codebooks, summary)
				}
			}
		}
		summaries = append(summaries, recordSummary{
			Occurrence: id,
			Event:      uint16(occurrence.Kind),
			Harness:    string(occurrence.RuntimeContract.Harness()),
			Capture:    uint8(occurrence.Capture),
			Codebook:   codebookHex,
		})
	}

	edges := make(map[chainKey]int)
	for _, link := range links {
		_, from := inScope[int64(link.From.JournalID())]
		_, to := inScope[int64(link.To.JournalID())]
		if !from && !to {
			continue
		}
		edges[chainKey{harness: string(link.Harness), kind: kindString(link.Kind), value: link.Value}]++
	}
	chains := make([]chainSummary, 0, len(edges))
	for key, count := range edges {
		chains = append(chains, chainSummary{Harness: key.harness, Kind: key.kind, Value: key.value, Edges: count})
	}

	sort.SliceStable(chains, func(i, j int) bool { return lessChain(chains[i], chains[j]) })
	sort.SliceStable(codebooks, func(i, j int) bool { return lessCodebook(codebooks[i], codebooks[j]) })

	return ContextProjection{
		scope:       scope,
		records:     summaries,
		chains:      chains,
		codebooks:   codebooks,
		truncated:   truncated,
		constructed: true,
	}, nil
}

// scopeFingerprint hashes the disclosed selection into a stable fingerprint. The
// encoding is length-prefixed per field so distinct selections can never
// collide; it is self-contained (no dependency on the projection reader) to keep
// this pure package lean.
func scopeFingerprint(query model.OccurrenceQuery) model.ContentIdentity {
	h := sha256.New()
	writeField := func(b []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	for _, contract := range query.ContractFilter() {
		writeField([]byte(contract.String()))
	}
	for _, event := range query.EventFilter() {
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(event))
		writeField(b[:])
	}
	for _, binding := range query.BindingFilter() {
		var kind [1]byte
		kind[0] = byte(binding.Kind)
		writeField(kind[:])
		writeField([]byte(binding.NativeName))
		writeField([]byte(binding.Value))
	}
	var out model.ContentIdentity
	copy(out[:], h.Sum(nil))
	return out
}

func kindString(kind runtime.NativeIdentityKind) string {
	if s := kind.String(); s != "" {
		return s
	}
	return fmt.Sprintf("kind-%d", uint8(kind))
}

func lessChain(a, b chainSummary) bool {
	if a.Harness != b.Harness {
		return a.Harness < b.Harness
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Value != b.Value {
		return a.Value < b.Value
	}
	return a.Edges < b.Edges
}

func lessCodebook(a, b codebookSummary) bool {
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	return a.Content < b.Content
}
