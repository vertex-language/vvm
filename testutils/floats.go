// floats.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// f32/f64 literals, arithmetic, min/max's IEEE-754-2019 semantics (NaN
// propagation, ordered signed zero), float comparisons, fpromote/fdemote,
// and int<->float conversions (ir.md §4 "Float Semantics", "Conversions").

func init() {
	register(testCase{
		name: "float_literal_f64",
		build: func() *vir.Module {
			return f64PrintingModule("float_literal_f64", func(fb *vir.FunctionBuilder) vir.Operand {
				return identity(fb, "v", vir.F64, vir.FloatLiteral(3.5))
			})
		},
		wantFloatValue: fval(3.5),
	})

	register(testCase{
		name: "float_literal_f32",
		build: func() *vir.Module {
			return f32PrintingModule("float_literal_f32", func(fb *vir.FunctionBuilder) vir.Operand {
				return identity(fb, "v", vir.F32, vir.FloatLiteral(2.5))
			})
		},
		wantFloatValue: fval(2.5),
	})

	register(testCase{
		name: "float_add",
		build: func() *vir.Module {
			return f64PrintingModule("float_add", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpAdd, vir.F64, vir.FloatLiteral(1.25), vir.FloatLiteral(2.5))
			})
		},
		wantFloatValue: fval(3.75),
	})

	register(testCase{
		name: "float_sub",
		build: func() *vir.Module {
			return f64PrintingModule("float_sub", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSub, vir.F64, vir.FloatLiteral(5.0), vir.FloatLiteral(1.5))
			})
		},
		wantFloatValue: fval(3.5),
	})

	register(testCase{
		name: "float_mul",
		build: func() *vir.Module {
			return f64PrintingModule("float_mul", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpMul, vir.F64, vir.FloatLiteral(2.5), vir.FloatLiteral(4.0))
			})
		},
		wantFloatValue: fval(10.0),
	})

	register(testCase{
		name: "float_sqrt",
		build: func() *vir.Module {
			return f64PrintingModule("float_sqrt", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSqrt, vir.F64, vir.FloatLiteral(81.0))
			})
		},
		wantFloatValue: fval(9.0),
	})

	// --- min/max: NaN propagates.
	register(testCase{
		name: "float_min_nan_propagates",
		build: func() *vir.Module {
			return f64PrintingModule("float_min_nan", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpMin, vir.F64, vir.FloatLiteral(nan()), vir.FloatLiteral(5.0))
			})
		},
		wantFloatValue: fval(nan()),
	})

	register(testCase{
		name: "float_max_nan_propagates",
		build: func() *vir.Module {
			return f64PrintingModule("float_max_nan", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpMax, vir.F64, vir.FloatLiteral(nan()), vir.FloatLiteral(5.0))
			})
		},
		wantFloatValue: fval(nan()),
	})

	// --- min/max: signed zero is ordered (-0.0 < +0.0).
	register(testCase{
		name: "float_min_signed_zero",
		build: func() *vir.Module {
			return f64PrintingModule("float_min_zero", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpMin, vir.F64, vir.FloatLiteral(negZero()), vir.FloatLiteral(0.0))
			})
		},
		wantFloatValue: fval(negZero()),
	})

	register(testCase{
		name: "float_max_signed_zero",
		build: func() *vir.Module {
			return f64PrintingModule("float_max_zero", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpMax, vir.F64, vir.FloatLiteral(negZero()), vir.FloatLiteral(0.0))
			})
		},
		wantFloatValue: fval(0.0),
	})

	// --- comparisons: lt/gt/le/ge, including NaN unordered (always false).
	register(testCase{
		name: "float_cmp_lt_true",
		build: func() *vir.Module {
			return i32PrintingModule("float_cmp_lt", func(fb *vir.FunctionBuilder) vir.Operand {
				cond := fb.Emit("cond", vir.OpLt, vir.F64, vir.FloatLiteral(3.0), vir.FloatLiteral(5.0))
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name: "float_cmp_nan_is_unordered",
		build: func() *vir.Module {
			return i32PrintingModule("float_cmp_nan", func(fb *vir.FunctionBuilder) vir.Operand {
				cond := fb.Emit("cond", vir.OpLt, vir.F64, vir.FloatLiteral(nan()), vir.FloatLiteral(5.0))
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(0),
	})

	// --- fpromote / fdemote.
	register(testCase{
		name: "float_fpromote",
		build: func() *vir.Module {
			return f64PrintingModule("float_fpromote", func(fb *vir.FunctionBuilder) vir.Operand {
				v := identity(fb, "v", vir.F32, vir.FloatLiteral(1.5))
				return fb.Emit("p", vir.OpFpromote, vir.F64, v)
			})
		},
		wantFloatValue: fval(1.5),
	})

	register(testCase{
		name: "float_fdemote",
		build: func() *vir.Module {
			// f32PrintingModule re-promotes for the printf boundary, so this
			// still checks that fdemote itself landed on the right value.
			return f32PrintingModule("float_fdemote", func(fb *vir.FunctionBuilder) vir.Operand {
				v := identity(fb, "v", vir.F64, vir.FloatLiteral(2.5))
				return fb.Emit("d", vir.OpFdemote, vir.F32, v)
			})
		},
		wantFloatValue: fval(2.5),
	})

	// --- int <-> float conversions.
	register(testCase{
		name: "convert_sfromint",
		build: func() *vir.Module {
			return f64PrintingModule("convert_sfromint", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSfromint, vir.F64, vir.IntLiteral(-5))
			})
		},
		wantFloatValue: fval(-5.0),
	})

	register(testCase{
		name: "convert_ufromint",
		build: func() *vir.Module {
			return f64PrintingModule("convert_ufromint", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpUfromint, vir.F64, vir.IntLiteral(200))
			})
		},
		wantFloatValue: fval(200.0),
	})

	register(testCase{
		name: "convert_stoint_sat_clamps_high",
		build: func() *vir.Module {
			return i32PrintingModule("convert_stoint_sat", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpStointSat, vir.I32, vir.FloatLiteral(1e20))
			})
		},
		wantValue: val(2147483647),
	})

	register(testCase{
		name: "convert_utoint_sat_clamps_negative_to_zero",
		build: func() *vir.Module {
			return i32PrintingModule("convert_utoint_sat", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpUtointSat, vir.I32, vir.FloatLiteral(-5.0))
			})
		},
		wantValue: val(0),
	})

	register(testCase{
		name: "convert_stoint_in_range",
		build: func() *vir.Module {
			return i32PrintingModule("convert_stoint", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpStoint, vir.I32, vir.FloatLiteral(42.9)) // truncates toward zero
			})
		},
		wantValue: val(42),
	})
}

func nan() float64     { return floatNaN }
func negZero() float64 { return floatNegZero }