package stablehlo

// Elementwise unary ops: result type is the operand type.

func (cb *CodeBuilder) Abs(x Value) Value              { return cb.unary("stablehlo.abs", x) }
func (cb *CodeBuilder) Ceil(x Value) Value             { return cb.unary("stablehlo.ceil", x) }
func (cb *CodeBuilder) Floor(x Value) Value            { return cb.unary("stablehlo.floor", x) }
func (cb *CodeBuilder) Cosine(x Value) Value           { return cb.unary("stablehlo.cosine", x) }
func (cb *CodeBuilder) Sine(x Value) Value             { return cb.unary("stablehlo.sine", x) }
func (cb *CodeBuilder) Tan(x Value) Value              { return cb.unary("stablehlo.tan", x) } // >= 1.4
func (cb *CodeBuilder) Tanh(x Value) Value             { return cb.unary("stablehlo.tanh", x) }
func (cb *CodeBuilder) Exponential(x Value) Value      { return cb.unary("stablehlo.exponential", x) }
func (cb *CodeBuilder) ExponentialMinusOne(x Value) Value {
	return cb.unary("stablehlo.exponential_minus_one", x)
}
func (cb *CodeBuilder) Log(x Value) Value              { return cb.unary("stablehlo.log", x) }
func (cb *CodeBuilder) LogPlusOne(x Value) Value       { return cb.unary("stablehlo.log_plus_one", x) }
func (cb *CodeBuilder) Logistic(x Value) Value         { return cb.unary("stablehlo.logistic", x) }
func (cb *CodeBuilder) Negate(x Value) Value           { return cb.unary("stablehlo.negate", x) }
func (cb *CodeBuilder) Not(x Value) Value              { return cb.unary("stablehlo.not", x) }
func (cb *CodeBuilder) Rsqrt(x Value) Value            { return cb.unary("stablehlo.rsqrt", x) }
func (cb *CodeBuilder) Sqrt(x Value) Value             { return cb.unary("stablehlo.sqrt", x) }
func (cb *CodeBuilder) Cbrt(x Value) Value             { return cb.unary("stablehlo.cbrt", x) }
func (cb *CodeBuilder) Sign(x Value) Value             { return cb.unary("stablehlo.sign", x) }
func (cb *CodeBuilder) Popcnt(x Value) Value           { return cb.unary("stablehlo.popcnt", x) }
func (cb *CodeBuilder) Clz(x Value) Value              { return cb.unary("stablehlo.count_leading_zeros", x) }
func (cb *CodeBuilder) RoundNearestEven(x Value) Value { return cb.unary("stablehlo.round_nearest_even", x) }
func (cb *CodeBuilder) RoundNearestAfz(x Value) Value  { return cb.unary("stablehlo.round_nearest_afz", x) }

// IsFinite returns a boolean tensor of the operand's shape.
func (cb *CodeBuilder) IsFinite(x Value) Value {
	return cb.emit1("stablehlo.is_finite", []Value{x}, nil, withElem(x.Type(), I1))
}

// Elementwise binary ops: result type is the lhs type.

func (cb *CodeBuilder) Add(a, b Value) Value       { return cb.binary("stablehlo.add", a, b) }
func (cb *CodeBuilder) Subtract(a, b Value) Value  { return cb.binary("stablehlo.subtract", a, b) }
func (cb *CodeBuilder) Multiply(a, b Value) Value  { return cb.binary("stablehlo.multiply", a, b) }
func (cb *CodeBuilder) Divide(a, b Value) Value    { return cb.binary("stablehlo.divide", a, b) }
func (cb *CodeBuilder) Remainder(a, b Value) Value { return cb.binary("stablehlo.remainder", a, b) }
func (cb *CodeBuilder) Maximum(a, b Value) Value   { return cb.binary("stablehlo.maximum", a, b) }
func (cb *CodeBuilder) Minimum(a, b Value) Value   { return cb.binary("stablehlo.minimum", a, b) }
func (cb *CodeBuilder) Power(a, b Value) Value     { return cb.binary("stablehlo.power", a, b) }
func (cb *CodeBuilder) Atan2(a, b Value) Value     { return cb.binary("stablehlo.atan2", a, b) }
func (cb *CodeBuilder) And(a, b Value) Value       { return cb.binary("stablehlo.and", a, b) }
func (cb *CodeBuilder) Or(a, b Value) Value        { return cb.binary("stablehlo.or", a, b) }
func (cb *CodeBuilder) Xor(a, b Value) Value       { return cb.binary("stablehlo.xor", a, b) }
func (cb *CodeBuilder) ShiftLeft(a, b Value) Value { return cb.binary("stablehlo.shift_left", a, b) }
func (cb *CodeBuilder) ShiftRightArith(a, b Value) Value {
	return cb.binary("stablehlo.shift_right_arithmetic", a, b)
}
func (cb *CodeBuilder) ShiftRightLogical(a, b Value) Value {
	return cb.binary("stablehlo.shift_right_logical", a, b)
}

// Compare emits "stablehlo.compare"; the result is a boolean tensor of the
// lhs shape.
func (cb *CodeBuilder) Compare(dir CompareDirection, a, b Value) Value {
	return cb.emit1("stablehlo.compare", []Value{a, b},
		[]NamedAttr{nattr("comparison_direction", enumAttr("comparison_direction", string(dir)))},
		withElem(a.Type(), I1))
}

// CompareWith is Compare with an explicit compare_type.
func (cb *CodeBuilder) CompareWith(dir CompareDirection, ct CompareType, a, b Value) Value {
	return cb.emit1("stablehlo.compare", []Value{a, b},
		[]NamedAttr{
			nattr("comparison_direction", enumAttr("comparison_direction", string(dir))),
			nattr("compare_type", enumAttr("comparison_type", string(ct))),
		},
		withElem(a.Type(), I1))
}

// Select emits "stablehlo.select"; result type follows onTrue.
func (cb *CodeBuilder) Select(pred, onTrue, onFalse Value) Value {
	return cb.emit1("stablehlo.select", []Value{pred, onTrue, onFalse}, nil, onTrue.Type())
}

// Clamp emits "stablehlo.clamp"; result type follows x.
func (cb *CodeBuilder) Clamp(min, x, max Value) Value {
	return cb.emit1("stablehlo.clamp", []Value{min, x, max}, nil, x.Type())
}