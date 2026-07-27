package stablehlo

// Type conversion.

func (cb *CodeBuilder) Convert(t Type, x Value) Value {
	return cb.emit1("stablehlo.convert", []Value{x}, nil, t)
}

func (cb *CodeBuilder) BitcastConvert(t Type, x Value) Value {
	return cb.emit1("stablehlo.bitcast_convert", []Value{x}, nil, t)
}

func (cb *CodeBuilder) Reshape(t Type, x Value) Value {
	return cb.emit1("stablehlo.reshape", []Value{x}, nil, t)
}

// Shape & data movement.

func (cb *CodeBuilder) BroadcastInDim(t Type, x Value, dims []int64) Value {
	return cb.emit1("stablehlo.broadcast_in_dim", []Value{x},
		[]NamedAttr{nattr("broadcast_dimensions", I64Array(dims...))}, t)
}

func (cb *CodeBuilder) Transpose(t Type, x Value, perm []int64) Value {
	return cb.emit1("stablehlo.transpose", []Value{x},
		[]NamedAttr{nattr("permutation", I64Array(perm...))}, t)
}

func (cb *CodeBuilder) Slice(t Type, x Value, starts, limits, strides []int64) Value {
	return cb.emit1("stablehlo.slice", []Value{x},
		[]NamedAttr{
			nattr("start_indices", I64Array(starts...)),
			nattr("limit_indices", I64Array(limits...)),
			nattr("strides", I64Array(strides...)),
		}, t)
}

func (cb *CodeBuilder) DynamicSlice(t Type, x Value, startIdx []Value, sizes []int64) Value {
	ops := append([]Value{x}, startIdx...)
	return cb.emit1("stablehlo.dynamic_slice", ops,
		[]NamedAttr{nattr("slice_sizes", I64Array(sizes...))}, t)
}

func (cb *CodeBuilder) DynamicUpdateSlice(x, update Value, startIdx []Value) Value {
	ops := append([]Value{x, update}, startIdx...)
	return cb.emit1("stablehlo.dynamic_update_slice", ops, nil, x.Type())
}

func (cb *CodeBuilder) Concatenate(t Type, dim int64, xs ...Value) Value {
	return cb.emit1("stablehlo.concatenate", xs,
		[]NamedAttr{nattr("dimension", I64Attr(dim))}, t)
}

func (cb *CodeBuilder) Pad(t Type, x, padVal Value, low, high, interior []int64) Value {
	return cb.emit1("stablehlo.pad", []Value{x, padVal},
		[]NamedAttr{
			nattr("edge_padding_low", I64Array(low...)),
			nattr("edge_padding_high", I64Array(high...)),
			nattr("interior_padding", I64Array(interior...)),
		}, t)
}

func (cb *CodeBuilder) Reverse(t Type, x Value, dims []int64) Value {
	return cb.emit1("stablehlo.reverse", []Value{x},
		[]NamedAttr{nattr("dimensions", I64Array(dims...))}, t)
}

func (cb *CodeBuilder) Iota(t Type, dim int64) Value {
	return cb.emit1("stablehlo.iota", nil,
		[]NamedAttr{nattr("iota_dimension", I64Attr(dim))}, t)
}

func (cb *CodeBuilder) Gather(t Type, operand, startIndices Value, dims GatherDims, sliceSizes []int64) Value {
	attrs := []NamedAttr{
		nattr("dimension_numbers", dims),
		nattr("slice_sizes", I64Array(sliceSizes...)),
	}
	if dims.IndicesAreSorted {
		attrs = append(attrs, nattr("indices_are_sorted", BoolAttr(true)))
	}
	return cb.emit1("stablehlo.gather", []Value{operand, startIndices}, attrs, t)
}