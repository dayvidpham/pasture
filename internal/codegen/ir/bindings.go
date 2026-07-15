package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// ScopeID identifies one lexical runtime scope. Children can consume bindings
// from ancestors; siblings and descendants are out of scope.
type ScopeID struct{ path string }

func NewRootScope(name string) (ScopeID, error) {
	return newScope(ScopeID{}, name)
}

func NewChildScope(parent ScopeID, name string) (ScopeID, error) {
	if !parent.IsValid() {
		return ScopeID{}, bindingError("parent scope is zero or invalid", "a child scope needs a valid lexical ancestor", "construct the parent with NewRootScope or NewChildScope", nil)
	}
	return newScope(parent, name)
}

func newScope(parent ScopeID, name string) (ScopeID, error) {
	if name == "" || strings.TrimSpace(name) != name || strings.Contains(name, "/") {
		return ScopeID{}, bindingError("scope name is empty, padded, or contains '/'", "scope paths use exact slash-separated constructor segments", "supply one non-empty segment without '/'", nil)
	}
	if parent.path == "" {
		return ScopeID{path: name}, nil
	}
	return ScopeID{path: parent.path + "/" + name}, nil
}

func (s ScopeID) String() string { return s.path }
func (s ScopeID) IsValid() bool {
	return s.path != "" && !strings.HasPrefix(s.path, "/") && !strings.HasSuffix(s.path, "/")
}

func scopeVisible(from, declared ScopeID) bool {
	return from == declared || strings.HasPrefix(from.path, declared.path+"/")
}

// BindingKey is a typed context-input identity.
type BindingKey[T any] struct {
	name   string
	typeOf reflect.Type
}

func NewBindingKey[T any](name string) (BindingKey[T], error) {
	if _, err := validateOpaqueID("binding key", name, true); err != nil {
		return BindingKey[T]{}, err
	}
	return BindingKey[T]{name: name, typeOf: typeOf[T]()}, nil
}

func (k BindingKey[T]) String() string { return k.name }
func (k BindingKey[T]) IsValid() bool  { return k.name != "" && k.typeOf == typeOf[T]() }

// ValueRef is a typed operand referencing a context input or prior result.
type ValueRef[T any] struct {
	key    string
	typeOf reflect.Type
	scope  ScopeID
	source valueSource
}

type valueSource uint8

const (
	valueSourceInput valueSource = iota + 1
	valueSourceResult
)

func InputValueRef[T any](key BindingKey[T], scope ScopeID) (ValueRef[T], error) {
	if !key.IsValid() || !scope.IsValid() {
		return ValueRef[T]{}, bindingError("input ValueRef has an invalid key or scope", "typed operands require constructor-validated identity and lexical scope", "construct both operands before creating the reference", nil)
	}
	return ValueRef[T]{key: key.name, typeOf: typeOf[T](), scope: scope, source: valueSourceInput}, nil
}

// ResultSlot declares one typed result produced in a lexical scope.
type ResultSlot[T any] struct {
	key    BindingKey[T]
	scope  ScopeID
	codec  Codec[T]
	typeOf reflect.Type
}

func NewResultSlot[T any](key BindingKey[T], scope ScopeID, codec Codec[T]) (ResultSlot[T], error) {
	if !key.IsValid() || !scope.IsValid() || codec == nil || codec.Schema() == "" {
		return ResultSlot[T]{}, bindingError("result slot has an invalid key, scope, or codec", "runtime capture requires one typed schema and lexical owner", "supply constructor-validated operands and a non-zero codec", nil)
	}
	return ResultSlot[T]{key: key, scope: scope, codec: codec, typeOf: typeOf[T]()}, nil
}

func ResultValueRef[T any](slot ResultSlot[T]) (ValueRef[T], error) {
	if !slot.IsValid() {
		return ValueRef[T]{}, bindingError("result slot is zero or invalid", "a prior-result operand must originate from a declared slot", "construct the slot with NewResultSlot", nil)
	}
	return ValueRef[T]{key: slot.key.name, typeOf: typeOf[T](), scope: slot.scope, source: valueSourceResult}, nil
}

func (s ResultSlot[T]) IsValid() bool {
	return s.key.IsValid() && s.scope.IsValid() && s.codec != nil && s.codec.Schema() != "" && s.typeOf == typeOf[T]()
}

// ResultSlotDeclaration is the immutable non-generic metadata retained by an
// operation and mutation continuation.
type ResultSlotDeclaration struct {
	key      string
	scope    ScopeID
	schema   SchemaID
	typeName string
}

func DeclareResultSlot[T any](slot ResultSlot[T]) (ResultSlotDeclaration, error) {
	if !slot.IsValid() {
		return ResultSlotDeclaration{}, bindingError("result slot declaration is zero or invalid", "operations may retain only validated typed result slots", "construct the slot before declaring it", nil)
	}
	return ResultSlotDeclaration{
		key: slot.key.name, scope: slot.scope, schema: slot.codec.Schema(), typeName: typeOf[T]().String(),
	}, nil
}

func (d ResultSlotDeclaration) Key() string      { return d.key }
func (d ResultSlotDeclaration) Scope() ScopeID   { return d.scope }
func (d ResultSlotDeclaration) Schema() SchemaID { return d.schema }
func (d ResultSlotDeclaration) TypeName() string { return d.typeName }
func (d ResultSlotDeclaration) IsValid() bool {
	return d.key != "" && d.scope.IsValid() && d.schema != "" && d.typeName != ""
}

type bindingEntry struct {
	key      string
	scope    ScopeID
	typeOf   reflect.Type
	typeName string
	encoded  []byte
}

// RuntimeBindings is an immutable typed lexical binding environment.
type RuntimeBindings struct{ entries map[string]bindingEntry }

func NewRuntimeBindings() RuntimeBindings {
	return RuntimeBindings{entries: make(map[string]bindingEntry)}
}

func (b RuntimeBindings) Len() int { return len(b.entries) }

func BindRuntimeValue[T any](
	bindings RuntimeBindings,
	key BindingKey[T],
	scope ScopeID,
	value T,
) (RuntimeBindings, error) {
	if !key.IsValid() || !scope.IsValid() {
		return RuntimeBindings{}, bindingError("binding has an invalid key or scope", "runtime values need a stable typed key and lexical owner", "construct the key and scope first", nil)
	}
	if _, duplicate := bindings.entries[key.name]; duplicate {
		return RuntimeBindings{}, bindingError(fmt.Sprintf("binding %q is duplicated", key.name), "a lexical environment cannot resolve two values for one key", "use a distinct BindingKey or remove the duplicate", nil)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return RuntimeBindings{}, bindingError(fmt.Sprintf("binding %q cannot be encoded", key.name), "scope snapshots require exact JSON-compatible values", "bind a JSON-compatible typed value", err)
	}
	var roundTrip T
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		return RuntimeBindings{}, bindingError(fmt.Sprintf("binding %q cannot be reconstructed from its JSON", key.name), "immutable runtime bindings must return defensive typed values", "bind a type with a complete JSON round trip", err)
	}
	out := cloneBindings(bindings)
	out.entries[key.name] = bindingEntry{
		key: key.name, scope: scope, typeOf: typeOf[T](), typeName: typeOf[T]().String(),
		encoded: append([]byte(nil), encoded...),
	}
	return out, nil
}

func CaptureRuntimeResult[T any](
	bindings RuntimeBindings,
	slot ResultSlot[T],
	data []byte,
) (RuntimeBindings, error) {
	if !slot.IsValid() {
		return RuntimeBindings{}, bindingError("result capture names a zero or invalid slot", "runtime JSON must be decoded through its declared typed result schema", "construct and retain the ResultSlot", nil)
	}
	value, err := slot.codec.Decode(append([]byte(nil), data...))
	if err != nil {
		return RuntimeBindings{}, bindingError(fmt.Sprintf("result %q failed typed JSON capture", slot.key.name), "runtime output did not satisfy its declared portable domain", "return JSON matching the slot codec", err)
	}
	return BindRuntimeValue(bindings, slot.key, slot.scope, value)
}

func ResolveRuntimeValue[T any](
	bindings RuntimeBindings,
	ref ValueRef[T],
	current ScopeID,
) (T, error) {
	var zero T
	if ref.key == "" || ref.typeOf != typeOf[T]() || !ref.scope.IsValid() || !current.IsValid() {
		return zero, bindingError("ValueRef or current scope is zero or forged", "typed dataflow accepts only constructor-produced references", "construct the ValueRef and current ScopeID through their APIs", nil)
	}
	entry, found := bindings.entries[ref.key]
	if !found {
		return zero, bindingError(fmt.Sprintf("binding %q is missing", ref.key), "the referenced input/result has not been captured", "bind or capture the value before constructing the operation", nil)
	}
	if entry.typeOf != typeOf[T]() {
		return zero, bindingError(fmt.Sprintf("binding %q has type %s, not %s", ref.key, entry.typeName, typeOf[T]()), "portable ID domains and result schemas cannot be interchanged", "consume the binding with a ValueRef of its declared type", nil)
	}
	if entry.scope != ref.scope {
		return zero, bindingError(fmt.Sprintf("binding %q declaration scope changed", ref.key), "a reference cannot retarget a same-named value in another scope", "use the ValueRef produced for the original slot/key", nil)
	}
	if !scopeVisible(current, entry.scope) {
		return zero, bindingError(fmt.Sprintf("binding %q from scope %q is out of scope at %q", ref.key, entry.scope, current), "lexical dataflow cannot consume sibling or descendant values", "move consumption into the declaring scope or a child scope", nil)
	}
	var value T
	if err := json.Unmarshal(entry.encoded, &value); err != nil {
		return zero, bindingError(fmt.Sprintf("binding %q cannot be reconstructed in its declared domain", ref.key), "the immutable encoded value disagrees with the typed reference", "re-bind or re-capture the value through its declared codec", err)
	}
	return value, nil
}

func cloneBindings(bindings RuntimeBindings) RuntimeBindings {
	out := NewRuntimeBindings()
	for key, entry := range bindings.entries {
		entry.encoded = append([]byte(nil), entry.encoded...)
		out.entries[key] = entry
	}
	return out
}

// LexicalScopeSnapshot is an immutable canonical copy of bindings visible from
// one scope when a mutation continuation is constructed.
type LexicalScopeSnapshot struct {
	scope     ScopeID
	canonical []byte
}

type scopeBindingWire struct {
	Key   string          `json:"key"`
	Scope string          `json:"scope"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

func SnapshotBindings(bindings RuntimeBindings, scope ScopeID) (LexicalScopeSnapshot, error) {
	if !scope.IsValid() {
		return LexicalScopeSnapshot{}, bindingError("snapshot scope is zero or invalid", "continuation authority retains an exact lexical visibility boundary", "construct a valid scope", nil)
	}
	keys := make([]string, 0, len(bindings.entries))
	for key, entry := range bindings.entries {
		if scopeVisible(scope, entry.scope) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	wire := make([]scopeBindingWire, 0, len(keys))
	for _, key := range keys {
		entry := bindings.entries[key]
		wire = append(wire, scopeBindingWire{
			Key: entry.key, Scope: entry.scope.String(), Type: entry.typeName,
			Value: append(json.RawMessage(nil), entry.encoded...),
		})
	}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return LexicalScopeSnapshot{}, bindingError("visible bindings could not be canonicalized", "continuation reconstruction needs immutable request inputs", "bind JSON-compatible values", err)
	}
	return LexicalScopeSnapshot{scope: scope, canonical: canonical}, nil
}

func (s LexicalScopeSnapshot) Scope() ScopeID { return s.scope }
func (s LexicalScopeSnapshot) CanonicalBytes() []byte {
	return append([]byte(nil), s.canonical...)
}
func (s LexicalScopeSnapshot) IsValid() bool {
	return s.scope.IsValid() && len(s.canonical) > 0 && json.Valid(s.canonical)
}

func (s LexicalScopeSnapshot) Equal(other LexicalScopeSnapshot) bool {
	return s.scope == other.scope && bytes.Equal(s.canonical, other.canonical)
}

func bindingError(what, why, fix string, cause error) error {
	return diagnostic(
		what, why, "typed runtime bindings", "dataflow validation",
		"the semantic operation is rejected before runtime invocation", fix, cause,
	)
}
