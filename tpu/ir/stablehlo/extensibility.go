package stablehlo

// CustomCallOpts carries the optional custom_call attribute surface.
type CustomCallOpts struct {
	BackendConfig      Attr   // e.g. StrAttr("...") or a raw DictionaryAttr (>= 1.3)
	HasSideEffect      bool
	CalledComputations []Attr // SymRef values
	ApiVersion         int32  // 0 => omitted
	Extra              []NamedAttr
}

// CustomCall emits "stablehlo.custom_call".
func (cb *CodeBuilder) CustomCall(resultTypes []Type, target string, inputs []Value, opts CustomCallOpts) []Value {
	attrs := []NamedAttr{nattr("call_target_name", StrAttr(target))}
	if opts.HasSideEffect {
		attrs = append(attrs, nattr("has_side_effect", BoolAttr(true)))
	}
	if opts.BackendConfig != nil {
		attrs = append(attrs, nattr("backend_config", opts.BackendConfig))
	}
	if len(opts.CalledComputations) > 0 {
		attrs = append(attrs, nattr("called_computations", listAttr(opts.CalledComputations)))
	}
	if opts.ApiVersion != 0 {
		attrs = append(attrs, nattr("api_version", I32Attr(opts.ApiVersion)))
	}
	attrs = append(attrs, opts.Extra...)
	return cb.emit("stablehlo.custom_call", inputs, attrs, resultTypes...)
}

// Composite emits "stablehlo.composite" (requires StableHLO >= 0.19).
// decomposition is a SymRef to an ordinary Func in the same module; attrs
// render as the composite_attributes dictionary.
func (cb *CodeBuilder) Composite(resultTypes []Type, name string, inputs []Value,
	decomposition Attr, attrs []NamedAttr) []Value {
	na := []NamedAttr{
		nattr("name", StrAttr(name)),
		nattr("decomposition", decomposition),
	}
	if len(attrs) > 0 {
		na = append(na, nattr("composite_attributes", dictAttr(attrs)))
	}
	return cb.emit("stablehlo.composite", inputs, na, resultTypes...)
}

// UniformQuantize emits "stablehlo.uniform_quantize"; qt is the quantized
// result tensor type (see Quant).
func (cb *CodeBuilder) UniformQuantize(qt Type, x Value) Value {
	return cb.emit1("stablehlo.uniform_quantize", []Value{x}, nil, qt)
}

// UniformDequantize emits "stablehlo.uniform_dequantize".
func (cb *CodeBuilder) UniformDequantize(t Type, x Value) Value {
	return cb.emit1("stablehlo.uniform_dequantize", []Value{x}, nil, t)
}