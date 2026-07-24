// arithmetic.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Division/remainder, neg/abs, overflow predicates, widening multiply, and
// saturating add/sub (ir.md §4 "Math"). Deliberately avoids every trapping
// input (div-by-zero, INT_MIN/-1, stoint out of range) — this suite has no
// confirmed convention yet for what RunModule reports on a trapped process,
// so those cases are left for a dedicated trap-handling pass instead of
// guessing an exit code here.

func init() {
	register(testCase{
		name: "arith_udiv",
		build: func() *vir.Module {
			return i32PrintingModule("arith_udiv", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpUDiv, vir.I32, vir.IntLiteral(100), vir.IntLiteral(7))
			})
		},
		wantValue: val(14),
	})

	register(testCase{
		name: "arith_sdiv_negative",
		build: func() *vir.Module {
			return i32PrintingModule("arith_sdiv_negative", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSDiv, vir.I32, vir.IntLiteral(-100), vir.IntLiteral(7))
			})
		},
		wantValue: val(-14),
	})

	register(testCase{
		name: "arith_urem",
		build: func() *vir.Module {
			return i32PrintingModule("arith_urem", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpURem, vir.I32, vir.IntLiteral(100), vir.IntLiteral(7))
			})
		},
		wantValue: val(2),
	})

	register(testCase{
		name: "arith_srem_negative",
		build: func() *vir.Module {
			return i32PrintingModule("arith_srem_negative", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSRem, vir.I32, vir.IntLiteral(-100), vir.IntLiteral(7))
			})
		},
		wantValue: val(-2),
	})

	register(testCase{
		name: "arith_neg",
		build: func() *vir.Module {
			return i32PrintingModule("arith_neg", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpNeg, vir.I32, vir.IntLiteral(5))
			})
		},
		wantValue: val(-5),
	})

	register(testCase{
		name: "arith_abs",
		build: func() *vir.Module {
			return i32PrintingModule("arith_abs", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpAbs, vir.I32, vir.IntLiteral(-5))
			})
		},
		wantValue: val(5),
	})

	// abs(INT_MIN) wraps to INT_MIN — explicitly called out in §4 as the
	// one place `abs` doesn't behave like ordinary absolute value.
	register(testCase{
		name: "arith_abs_int_min_wraps",
		build: func() *vir.Module {
			return i32PrintingModule("arith_abs_int_min", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpAbs, vir.I32, vir.IntLiteral(-2147483648))
			})
		},
		wantValue: val(-2147483648),
	})

	// --- overflow predicates: same two operands as the wrapping op, but
	// return i1 (true iff the wrapping result differs from the infinitely
	// ranged one). Routed through select to print as 0/1, same pattern as
	// select.go / comparisons.go.
	register(testCase{
		name: "arith_saddo_overflows",
		build: func() *vir.Module {
			return i32PrintingModule("arith_saddo_true", func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", vir.OpSAddO, vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(1))
				return fb.Emit("v", vir.OpSelect, vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name: "arith_saddo_no_overflow",
		build: func() *vir.Module {
			return i32PrintingModule("arith_saddo_false", func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", vir.OpSAddO, vir.I32, vir.IntLiteral(1), vir.IntLiteral(1))
				return fb.Emit("v", vir.OpSelect, vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(0),
	})

	register(testCase{
		name: "arith_uaddo_overflows",
		build: func() *vir.Module {
			return i32PrintingModule("arith_uaddo_true", func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", vir.OpUAddO, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(1)) // 0xFFFFFFFF + 1
				return fb.Emit("v", vir.OpSelect, vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name: "arith_usubo_underflows",
		build: func() *vir.Module {
			return i32PrintingModule("arith_usubo_true", func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", vir.OpUSubO, vir.I32, vir.IntLiteral(0), vir.IntLiteral(1))
				return fb.Emit("v", vir.OpSelect, vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name: "arith_smulo_overflows",
		build: func() *vir.Module {
			return i32PrintingModule("arith_smulo_true", func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", vir.OpSMulO, vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(2))
				return fb.Emit("v", vir.OpSelect, vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	// --- widening multiply: result is the high half of the double-width
	// product.
	register(testCase{
		name: "arith_umulh",
		build: func() *vir.Module {
			return i32PrintingModule("arith_umulh", func(fb *vir.FunctionBuilder) vir.Operand {
				// 0xFFFFFFFF (unsigned) * 2 = 0x1FFFFFFFE; high 32 bits = 1.
				return fb.Emit("v", vir.OpUMulH, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(2))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name: "arith_smulh",
		build: func() *vir.Module {
			return i32PrintingModule("arith_smulh", func(fb *vir.FunctionBuilder) vir.Operand {
				// 0x40000000 * 4 = 0x100000000; high 32 bits = 1.
				return fb.Emit("v", vir.OpSMulH, vir.I32, vir.IntLiteral(1073741824), vir.IntLiteral(4))
			})
		},
		wantValue: val(1),
	})

	// --- saturating add/sub: clamp to the representable range instead of
	// wrapping.
	register(testCase{
		name: "arith_uadd_sat_clamps",
		build: func() *vir.Module {
			return i32PrintingModule("arith_uadd_sat", func(fb *vir.FunctionBuilder) vir.Operand {
				// UINT32_MAX + 10 saturates at UINT32_MAX (prints as -1).
				return fb.Emit("v", vir.OpUAddSat, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(10))
			})
		},
		wantValue: val(-1),
	})

	register(testCase{
		name: "arith_sadd_sat_clamps",
		build: func() *vir.Module {
			return i32PrintingModule("arith_sadd_sat", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSAddSat, vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(1))
			})
		},
		wantValue: val(2147483647),
	})

	register(testCase{
		name: "arith_usub_sat_clamps_to_zero",
		build: func() *vir.Module {
			return i32PrintingModule("arith_usub_sat", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpUSubSat, vir.I32, vir.IntLiteral(5), vir.IntLiteral(10))
			})
		},
		wantValue: val(0),
	})

	register(testCase{
		name: "arith_ssub_sat_clamps",
		build: func() *vir.Module {
			return i32PrintingModule("arith_ssub_sat", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSSubSat, vir.I32, vir.IntLiteral(-2147483648), vir.IntLiteral(1))
			})
		},
		wantValue: val(-2147483648),
	})
}