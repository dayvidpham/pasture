package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

// Codec is a typed, versioned portable value codec.
type Codec[T any] interface {
	Schema() SchemaID
	Encode(T) ([]byte, error)
	Decode([]byte) (T, error)
}

// JSONCodec is a strict JSON implementation with an optional semantic validator.
type JSONCodec[T any] struct {
	schema   SchemaID
	validate func(T) error
}

func NewJSONCodec[T any](schema SchemaID, validate func(T) error) (JSONCodec[T], error) {
	if err := validateNamedID("codec schema", string(schema)); err != nil {
		return JSONCodec[T]{}, err
	}
	return JSONCodec[T]{schema: schema, validate: validate}, nil
}

func (c JSONCodec[T]) Schema() SchemaID { return c.schema }

func (c JSONCodec[T]) Encode(value T) ([]byte, error) {
	if c.schema == "" {
		return nil, diagnostic(
			"JSON codec is zero", "a zero codec has no stable schema identity",
			"JSONCodec.Encode", "codec validation", "the value cannot be encoded",
			"construct the codec with NewJSONCodec", nil,
		)
	}
	if c.validate != nil {
		if err := c.validate(value); err != nil {
			return nil, diagnostic(
				"value failed codec validation", "the schema validator rejected the typed value",
				string(c.schema), "codec validation", "invalid input would cross the runtime boundary",
				"correct the value before encoding", err,
			)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, diagnostic(
			"typed value could not be encoded as JSON", "the value contains data unsupported by its codec",
			string(c.schema), "codec encoding", "the runtime request cannot be constructed",
			"use a value supported by the declared schema", err,
		)
	}
	return encoded, nil
}

func (c JSONCodec[T]) Decode(data []byte) (T, error) {
	var zero T
	if c.schema == "" {
		return zero, diagnostic(
			"JSON codec is zero", "a zero codec has no stable schema identity",
			"JSONCodec.Decode", "codec validation", "the value cannot be decoded",
			"construct the codec with NewJSONCodec", nil,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, diagnostic(
			"JSON value does not match its declared schema", "malformed or unknown fields are not portable",
			string(c.schema), "codec decoding", "the runtime result cannot be trusted",
			"encode exactly one value using the declared schema", err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("additional JSON value follows the first")
		}
		return zero, diagnostic(
			"JSON value has trailing content", "the codec accepts exactly one value",
			string(c.schema), "codec decoding", "the runtime result is ambiguous",
			"remove trailing fields or values", err,
		)
	}
	if c.validate != nil {
		if err := c.validate(value); err != nil {
			return zero, diagnostic(
				"decoded value failed semantic validation", "wire shape alone does not satisfy the codec contract",
				string(c.schema), "codec validation", "the runtime result cannot be consumed",
				"return a value satisfying the declared schema semantics", err,
			)
		}
	}
	return value, nil
}

// DescriptorSemantics is the portable behavioral contract carried by a typed
// operation or effect descriptor.
type DescriptorSemantics struct {
	Summary        string
	Preconditions  []string
	Postconditions []string
	Result         string
}

func (s DescriptorSemantics) validate(where string) error {
	if s.Summary == "" || s.Result == "" {
		return diagnostic(
			"descriptor semantics omit summary or result contract",
			"runtime binding compatibility requires both intent and result meaning",
			where, "descriptor validation", "the descriptor cannot be registered",
			"provide a non-empty Summary and Result", nil,
		)
	}
	return nil
}

// OperationDescriptor is the only valid lookup operand for a semantic operation.
type OperationDescriptor[In, Out any] struct {
	id        SemanticOperationID
	input     Codec[In]
	output    Codec[Out]
	semantics DescriptorSemantics
	effects   EffectSet
	inType    reflect.Type
	outType   reflect.Type
}

// EffectDescriptor is the only valid lookup operand for a typed effect.
type EffectDescriptor[In, Out any] struct {
	id        EffectID
	input     Codec[In]
	output    Codec[Out]
	semantics DescriptorSemantics
	effects   EffectSet
	inType    reflect.Type
	outType   reflect.Type
}

func NewOperationDescriptor[In, Out any](
	id SemanticOperationID,
	input Codec[In],
	output Codec[Out],
	semantics DescriptorSemantics,
	effects EffectSet,
) (OperationDescriptor[In, Out], error) {
	if !id.IsValid() {
		return OperationDescriptor[In, Out]{}, diagnostic(
			"semantic operation identity is zero or invalid", "descriptors require a constructor-validated protocol identity",
			"NewOperationDescriptor", "descriptor validation", "runtime lookup could be bypassed or ambiguous",
			"construct the ID with NewSemanticOperationID", nil,
		)
	}
	if input == nil || output == nil || input.Schema() == "" || output.Schema() == "" {
		return OperationDescriptor[In, Out]{}, diagnostic(
			"operation descriptor has a missing input or output codec", "typed runtime lookup requires stable schemas in both directions",
			id.String(), "descriptor validation", "the operation cannot be lowered safely",
			"supply non-zero typed codecs", nil,
		)
	}
	if err := semantics.validate(id.String()); err != nil {
		return OperationDescriptor[In, Out]{}, err
	}
	if !effects.IsValid() {
		return OperationDescriptor[In, Out]{}, diagnostic(
			"operation descriptor has a noncanonical effect set", "effects are part of binding compatibility",
			id.String(), "descriptor validation", "the operation cannot be registered",
			"construct effects with NewEffectSet", nil,
		)
	}
	return OperationDescriptor[In, Out]{
		id: id, input: input, output: output, semantics: cloneSemantics(semantics), effects: effects,
		inType: typeOf[In](), outType: typeOf[Out](),
	}, nil
}

func NewEffectDescriptor[In, Out any](
	id EffectID,
	input Codec[In],
	output Codec[Out],
	semantics DescriptorSemantics,
	effects EffectSet,
) (EffectDescriptor[In, Out], error) {
	if !id.IsValid() {
		return EffectDescriptor[In, Out]{}, diagnostic(
			"effect identity is zero or invalid", "descriptors require a constructor-validated effect identity",
			"NewEffectDescriptor", "descriptor validation", "runtime lookup could be bypassed or ambiguous",
			"construct the ID with NewEffectID", nil,
		)
	}
	if input == nil || output == nil || input.Schema() == "" || output.Schema() == "" {
		return EffectDescriptor[In, Out]{}, diagnostic(
			"effect descriptor has a missing input or output codec", "typed runtime lookup requires stable schemas in both directions",
			id.String(), "descriptor validation", "the effect cannot be lowered safely",
			"supply non-zero typed codecs", nil,
		)
	}
	if err := semantics.validate(id.String()); err != nil {
		return EffectDescriptor[In, Out]{}, err
	}
	if !effects.IsValid() {
		return EffectDescriptor[In, Out]{}, diagnostic(
			"effect descriptor has a noncanonical effect set", "effects are part of binding compatibility",
			id.String(), "descriptor validation", "the effect cannot be registered",
			"construct effects with NewEffectSet", nil,
		)
	}
	return EffectDescriptor[In, Out]{
		id: id, input: input, output: output, semantics: cloneSemantics(semantics), effects: effects,
		inType: typeOf[In](), outType: typeOf[Out](),
	}, nil
}

func typeOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

func cloneSemantics(value DescriptorSemantics) DescriptorSemantics {
	value.Preconditions = append([]string(nil), value.Preconditions...)
	value.Postconditions = append([]string(nil), value.Postconditions...)
	return value
}

func (d OperationDescriptor[In, Out]) ID() SemanticOperationID { return d.id }
func (d EffectDescriptor[In, Out]) ID() EffectID               { return d.id }
func (d OperationDescriptor[In, Out]) InputCodec() Codec[In]   { return d.input }
func (d OperationDescriptor[In, Out]) OutputCodec() Codec[Out] { return d.output }
func (d EffectDescriptor[In, Out]) InputCodec() Codec[In]      { return d.input }
func (d EffectDescriptor[In, Out]) OutputCodec() Codec[Out]    { return d.output }
func (d OperationDescriptor[In, Out]) Semantics() DescriptorSemantics {
	return cloneSemantics(d.semantics)
}
func (d EffectDescriptor[In, Out]) Semantics() DescriptorSemantics {
	return cloneSemantics(d.semantics)
}
func (d OperationDescriptor[In, Out]) Effects() EffectSet { return d.effects }
func (d EffectDescriptor[In, Out]) Effects() EffectSet    { return d.effects }
func (d OperationDescriptor[In, Out]) IsValid() bool {
	return d.id.IsValid() && d.input != nil && d.output != nil && d.inType == typeOf[In]() && d.outType == typeOf[Out]() && d.effects.IsValid()
}
func (d EffectDescriptor[In, Out]) IsValid() bool {
	return d.id.IsValid() && d.input != nil && d.output != nil && d.inType == typeOf[In]() && d.outType == typeOf[Out]() && d.effects.IsValid()
}
