package stablehlo

import "fmt"

// CodeBuilder is an append-only op builder for a function body. Emit
// methods take Value (and attribute) arguments and return result Values.
type CodeBuilder struct {
	fn         *Func
	terminator string // "func.return" at function level, "stablehlo.return" in regions
	ops        []*Op
	returned   bool
}

// Ops returns the emitted ops in emission order.
func (cb *CodeBuilder) Ops() []*Op { return cb.ops }

// Returned reports whether Return has been called on this builder.
func (cb *CodeBuilder) Returned() bool { return cb.returned }

// Terminator returns the terminator op name for this builder's level.
func (cb *CodeBuilder) Terminator() string { return cb.terminator }

// RegionBuilder builds the single block of a region-carrying op. Its
// Return emits stablehlo.return.
type RegionBuilder struct {
	*CodeBuilder
}

// --- core emission ---

func (cb *CodeBuilder) emit(name string, operands []Value, attrs []NamedAttr, resultTypes ...Type) []Value {
	op := &Op{Name: name, Operands: operands, Attrs: attrs, ResultTypes: resultTypes}
	if v, ok := MinVersionForOp(name); ok {
		op.MinVersion = v
	}
	op.results = make([]Value, len(resultTypes))
	for i, t := range resultTypes {
		op.results[i] = newValue(t)
	}
	cb.ops = append(cb.ops, op)
	return op.results
}

func (cb *CodeBuilder) emit1(name string, operands []Value, attrs []NamedAttr, t Type) Value {
	return cb.emit(name, operands, attrs, t)[0]
}

func (cb *CodeBuilder) emitR(name string, operands []Value, attrs []NamedAttr, regions []*Region, resultTypes ...Type) []Value {
	rs := cb.emit(name, operands, attrs, resultTypes...)
	cb.ops[len(cb.ops)-1].Regions = regions
	return rs
}

func (cb *CodeBuilder) unary(name string, x Value) Value {
	return cb.emit1(name, []Value{x}, nil, x.Type())
}

func (cb *CodeBuilder) binary(name string, a, b Value) Value {
	return cb.emit1(name, []Value{a, b}, nil, a.Type())
}

// region builds a single-block region, invoking build with a fresh
// RegionBuilder and the block-argument values.
func (cb *CodeBuilder) region(argTypes []Type, build func(*RegionBuilder, []Value)) *Region {
	body := &CodeBuilder{fn: cb.fn, terminator: "stablehlo.return"}
	r := &Region{ArgTypes: argTypes, Body: body}
	r.args = make([]Value, len(argTypes))
	for i, t := range argTypes {
		r.args[i] = newValue(t)
	}
	build(&RegionBuilder{body}, r.args)
	return r
}

// --- misc ops ---

// Constant emits "stablehlo.constant" for the literal.
func (cb *CodeBuilder) Constant(lit Literal) Value {
	return cb.emit1("stablehlo.constant", nil,
		[]NamedAttr{nattr("value", RawAttrValue(lit.String()))}, lit.Ty)
}

// Scalar is convenience coercion: it emits a 0-d splat constant of the
// given element type. v may be any Go scalar accepted by Splat.
func (cb *CodeBuilder) Scalar(v any, e ElemType) Value {
	return cb.Constant(Splat(v, Tensor(e)))
}

// Return emits the terminator for this builder's level (func.return at
// function level, stablehlo.return in regions).
func (cb *CodeBuilder) Return(xs ...Value) {
	cb.emit(cb.terminator, xs, nil)
	cb.returned = true
}

// Comment attaches a trailing // comment to the last emitted op.
func (cb *CodeBuilder) Comment(s string) {
	if n := len(cb.ops); n > 0 {
		cb.ops[n-1].Comment = s
	}
}

// Raw emits an op from a verbatim format string. Operand Values are
// interpolated for %s verbs by printed name; results are fresh Values that
// participate in SSA numbering. The format string is everything to the
// right of the "=" (or the whole line when resultTypes is empty).
func (cb *CodeBuilder) Raw(resultTypes []Type, format string, operands ...Value) []Value {
	op := &Op{RawFormat: format, RawOperands: operands, ResultTypes: resultTypes}
	op.results = make([]Value, len(resultTypes))
	for i, t := range resultTypes {
		op.results[i] = newValue(t)
	}
	cb.ops = append(cb.ops, op)
	return op.results
}

// RawAttrOnLast attaches a raw attribute to the last emitted op.
func (cb *CodeBuilder) RawAttrOnLast(name, value string) {
	if n := len(cb.ops); n > 0 {
		cb.ops[n-1].Attrs = append(cb.ops[n-1].Attrs, RawAttr(name, value))
	}
}

// Tuple / GetTupleElement (HLO-ABI legacy).

func (cb *CodeBuilder) Tuple(xs ...Value) Value {
	ts := make([]Type, len(xs))
	for i, x := range xs {
		ts[i] = x.Type()
	}
	return cb.emit1("stablehlo.tuple", xs, nil, Tuple(ts...))
}

func (cb *CodeBuilder) GetTupleElement(x Value, index int32) Value {
	t := x.Type()
	if elems, ok := tupleElems(t); ok && int(index) < len(elems) {
		t = elems[index]
	}
	return cb.emit1("stablehlo.get_tuple_element", []Value{x},
		[]NamedAttr{nattr("index", I32Attr(index))}, t)
}

// Rng emits "stablehlo.rng". The shape operand required by the op is
// synthesized as an i64 constant from t's (static) dimensions.
func (cb *CodeBuilder) Rng(t Type, a, b Value, dist RngDistribution) Value {
	dims, ok := tensorDims(t)
	if !ok {
		panic(fmt.Sprintf("stablehlo: Rng result must be a tensor type, got %s", TypeString(t)))
	}
	shape := cb.Constant(DenseI64(dims, Tensor(SI64, int64(len(dims)))))
	return cb.emit1("stablehlo.rng", []Value{a, b, shape},
		[]NamedAttr{nattr("rng_distribution", enumAttr("rng_distribution", string(dist)))}, t)
}

// RngBitGenerator emits "stablehlo.rng_bit_generator", returning
// (outputState, output).
func (cb *CodeBuilder) RngBitGenerator(stateT, outT Type, alg RngAlgorithm, initialState Value) (Value, Value) {
	rs := cb.emit("stablehlo.rng_bit_generator", []Value{initialState},
		[]NamedAttr{nattr("rng_algorithm", enumAttr("rng_algorithm", string(alg)))}, stateT, outT)
	return rs[0], rs[1]
}