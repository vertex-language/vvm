package stablehlo

// Region-carrying ops. Bodies are built by callback: the closure receives
// a fresh RegionBuilder and the region's block arguments and must end with
// rb.Return(...). Block-argument types are derived from operand types
// (reduce/sort/map/...) or supplied via the operands themselves (while).

// Reduce emits a single-result "stablehlo.reduce". The body receives 2N
// scalar block arguments (accumulators then elements), typed after inits.
func (cb *CodeBuilder) Reduce(t Type, inputs, inits []Value, dims []int64,
	body func(*RegionBuilder, []Value)) Value {
	return cb.ReduceN([]Type{t}, inputs, inits, dims, body)[0]
}

// ReduceN is Reduce for variadic reductions with multiple results.
func (cb *CodeBuilder) ReduceN(resultTypes []Type, inputs, inits []Value, dims []int64,
	body func(*RegionBuilder, []Value)) []Value {
	r := cb.region(doubledInitTypes(inits), body)
	ops := append(append([]Value{}, inputs...), inits...)
	return cb.emitR("stablehlo.reduce", ops,
		[]NamedAttr{nattr("dimensions", I64Array(dims...))},
		[]*Region{r}, resultTypes...)
}

// WindowConfig carries reduce_window / select_and_scatter window attributes.
type WindowConfig struct {
	WindowDimensions []int64
	WindowStrides    []int64
	BaseDilations    []int64
	WindowDilations  []int64
	Padding          [][]int64 // one [low, high] pair per dim
}

func (w WindowConfig) attrs() []NamedAttr {
	attrs := []NamedAttr{nattr("window_dimensions", I64Array(w.WindowDimensions...))}
	if len(w.WindowStrides) > 0 {
		attrs = append(attrs, nattr("window_strides", I64Array(w.WindowStrides...)))
	}
	if len(w.BaseDilations) > 0 {
		attrs = append(attrs, nattr("base_dilations", I64Array(w.BaseDilations...)))
	}
	if len(w.WindowDilations) > 0 {
		attrs = append(attrs, nattr("window_dilations", I64Array(w.WindowDilations...)))
	}
	if len(w.Padding) > 0 {
		attrs = append(attrs, nattr("padding", DenseI64Matrix(w.Padding)))
	}
	return attrs
}

func (cb *CodeBuilder) ReduceWindow(resultTypes []Type, inputs, inits []Value, cfg WindowConfig,
	body func(*RegionBuilder, []Value)) []Value {
	r := cb.region(doubledInitTypes(inits), body)
	ops := append(append([]Value{}, inputs...), inits...)
	return cb.emitR("stablehlo.reduce_window", ops, cfg.attrs(), []*Region{r}, resultTypes...)
}

// Map emits "stablehlo.map"; the body receives one scalar per input.
func (cb *CodeBuilder) Map(t Type, inputs []Value, dims []int64,
	body func(*RegionBuilder, []Value)) Value {
	args := make([]Type, len(inputs))
	for i, in := range inputs {
		args[i] = scalarOf(in.Type())
	}
	r := cb.region(args, body)
	return cb.emitR("stablehlo.map", inputs,
		[]NamedAttr{nattr("dimensions", I64Array(dims...))},
		[]*Region{r}, t)[0]
}

// Sort emits "stablehlo.sort"; results mirror input types. The comparator
// receives two scalars per input and must return a boolean scalar.
func (cb *CodeBuilder) Sort(inputs []Value, dim int64, isStable bool,
	comparator func(*RegionBuilder, []Value)) []Value {
	args := make([]Type, 0, 2*len(inputs))
	rts := make([]Type, len(inputs))
	for i, in := range inputs {
		s := scalarOf(in.Type())
		args = append(args, s, s)
		rts[i] = in.Type()
	}
	r := cb.region(args, comparator)
	return cb.emitR("stablehlo.sort", inputs,
		[]NamedAttr{
			nattr("dimension", I64Attr(dim)),
			nattr("is_stable", BoolAttr(isStable)),
		}, []*Region{r}, rts...)
}

// SelectAndScatter emits "stablehlo.select_and_scatter". Both regions
// receive two scalar block arguments.
func (cb *CodeBuilder) SelectAndScatter(t Type, operand, source, initVal Value, cfg WindowConfig,
	selectFn, scatterFn func(*RegionBuilder, []Value)) Value {
	s := scalarOf(operand.Type())
	sel := cb.region([]Type{s, s}, selectFn)
	sca := cb.region([]Type{s, s}, scatterFn)
	attrs := []NamedAttr{}
	if len(cfg.WindowDimensions) > 0 {
		attrs = cfg.attrs()
	}
	return cb.emitR("stablehlo.select_and_scatter", []Value{operand, source, initVal},
		attrs, []*Region{sel, sca}, t)[0]
}

// Scatter emits "stablehlo.scatter". The update computation receives 2N
// scalars (current values then updates).
func (cb *CodeBuilder) Scatter(resultTypes []Type, inputs []Value, scatterIndices Value,
	updates []Value, dims ScatterDims, indicesAreSorted, uniqueIndices bool,
	updateFn func(*RegionBuilder, []Value)) []Value {
	args := make([]Type, 0, 2*len(inputs))
	for _, in := range inputs {
		args = append(args, scalarOf(in.Type()))
	}
	for _, in := range inputs {
		args = append(args, scalarOf(in.Type()))
	}
	r := cb.region(args, updateFn)
	ops := append(append([]Value{}, inputs...), scatterIndices)
	ops = append(ops, updates...)
	attrs := []NamedAttr{nattr("scatter_dimension_numbers", dims)}
	if indicesAreSorted {
		attrs = append(attrs, nattr("indices_are_sorted", BoolAttr(true)))
	}
	if uniqueIndices {
		attrs = append(attrs, nattr("unique_indices", BoolAttr(true)))
	}
	return cb.emitR("stablehlo.scatter", ops, attrs, []*Region{r}, resultTypes...)
}

// While emits "stablehlo.while". Results and both regions' block-argument
// types mirror the init types. cond must return a boolean scalar.
func (cb *CodeBuilder) While(inits []Value,
	cond, body func(*RegionBuilder, []Value)) []Value {
	args := make([]Type, len(inits))
	rts := make([]Type, len(inits))
	for i, v := range inits {
		args[i] = v.Type()
		rts[i] = v.Type()
	}
	rc := cb.region(args, cond)
	rb := cb.region(args, body)
	return cb.emitR("stablehlo.while", inits, nil, []*Region{rc, rb}, rts...)
}

// If emits "stablehlo.if"; both regions take no block arguments.
func (cb *CodeBuilder) If(resultTypes []Type, pred Value,
	thenFn, elseFn func(*RegionBuilder, []Value)) []Value {
	rt := cb.region(nil, thenFn)
	re := cb.region(nil, elseFn)
	return cb.emitR("stablehlo.if", []Value{pred}, nil, []*Region{rt, re}, resultTypes...)
}

// Case emits "stablehlo.case"; branch regions take no block arguments.
func (cb *CodeBuilder) Case(resultTypes []Type, index Value,
	branches []func(*RegionBuilder, []Value)) []Value {
	rs := make([]*Region, len(branches))
	for i, fn := range branches {
		rs[i] = cb.region(nil, fn)
	}
	return cb.emitR("stablehlo.case", []Value{index}, nil, rs, resultTypes...)
}

func doubledInitTypes(inits []Value) []Type {
	args := make([]Type, 0, 2*len(inits))
	for _, v := range inits {
		args = append(args, scalarOf(v.Type()))
	}
	for _, v := range inits {
		args = append(args, scalarOf(v.Type()))
	}
	return args
}