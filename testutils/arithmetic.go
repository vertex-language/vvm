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
		name:       "arith_udiv",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_udiv", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "udiv", vir.I32, vir.IntLiteral(100), vir.IntLiteral(7))
			})
		},
		wantValue: val(14),
	})

	register(testCase{
		name:       "arith_sdiv_negative",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_sdiv_negative", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "sdiv", vir.I32, vir.IntLiteral(-100), vir.IntLiteral(7))
			})
		},
		wantValue: val(-14),
	})

	register(testCase{
		name:       "arith_urem",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_urem", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "urem", vir.I32, vir.IntLiteral(100), vir.IntLiteral(7))
			})
		},
		wantValue: val(2),
	})

	register(testCase{
		name:       "arith_srem_negative",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_srem_negative", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "srem", vir.I32, vir.IntLiteral(-100), vir.IntLiteral(7))
			})
		},
		wantValue: val(-2),
	})

	register(testCase{
		name:       "arith_neg",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_neg", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "neg", vir.I32, vir.IntLiteral(5))
			})
		},
		wantValue: val(-5),
	})

	register(testCase{
		name:       "arith_abs",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_abs", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "abs", vir.I32, vir.IntLiteral(-5))
			})
		},
		wantValue: val(5),
	})

	// abs(INT_MIN) wraps to INT_MIN — explicitly called out in §4 as the
	// one place `abs` doesn't behave like ordinary absolute value.
	register(testCase{
		name:       "arith_abs_int_min_wraps",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_abs_int_min", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "abs", vir.I32, vir.IntLiteral(-2147483648))
			})
		},
		wantValue: val(-2147483648),
	})

	// --- overflow predicates: same two operands as the wrapping op, but
	// return i1 (true iff the wrapping result differs from the infinitely
	// ranged one). Routed through select to print as 0/1, same pattern as
	// select.go / comparisons.go.
	register(testCase{
		name:       "arith_saddo_overflows",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_saddo_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", "saddo", vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(1))
				return fb.Emit("v", "select", vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name:       "arith_saddo_no_overflow",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_saddo_false", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", "saddo", vir.I32, vir.IntLiteral(1), vir.IntLiteral(1))
				return fb.Emit("v", "select", vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(0),
	})

	register(testCase{
		name:       "arith_uaddo_overflows",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_uaddo_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", "uaddo", vir.I32, vir.IntLiteral(-1), vir.IntLiteral(1)) // 0xFFFFFFFF + 1
				return fb.Emit("v", "select", vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name:       "arith_usubo_underflows",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_usubo_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", "usubo", vir.I32, vir.IntLiteral(0), vir.IntLiteral(1))
				return fb.Emit("v", "select", vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name:       "arith_smulo_overflows",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_smulo_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				of := fb.Emit("of", "smulo", vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(2))
				return fb.Emit("v", "select", vir.I32, of, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	// --- widening multiply: result is the high half of the double-width
	// product.
	register(testCase{
		name:       "arith_umulh",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_umulh", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				// 0xFFFFFFFF (unsigned) * 2 = 0x1FFFFFFFE; high 32 bits = 1.
				return fb.Emit("v", "umulh", vir.I32, vir.IntLiteral(-1), vir.IntLiteral(2))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name:       "arith_smulh",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_smulh", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				// 0x40000000 * 4 = 0x100000000; high 32 bits = 1.
				return fb.Emit("v", "smulh", vir.I32, vir.IntLiteral(1073741824), vir.IntLiteral(4))
			})
		},
		wantValue: val(1),
	})

	// --- saturating add/sub: clamp to the representable range instead of
	// wrapping.
	register(testCase{
		name:       "arith_uadd_sat_clamps",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_uadd_sat", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				// UINT32_MAX + 10 saturates at UINT32_MAX (prints as -1).
				return fb.Emit("v", "uadd_sat", vir.I32, vir.IntLiteral(-1), vir.IntLiteral(10))
			})
		},
		wantValue: val(-1),
	})

	register(testCase{
		name:       "arith_sadd_sat_clamps",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_sadd_sat", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "sadd_sat", vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(1))
			})
		},
		wantValue: val(2147483647),
	})

	register(testCase{
		name:       "arith_usub_sat_clamps_to_zero",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_usub_sat", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "usub_sat", vir.I32, vir.IntLiteral(5), vir.IntLiteral(10))
			})
		},
		wantValue: val(0),
	})

	register(testCase{
		name:       "arith_ssub_sat_clamps",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("arith_ssub_sat", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "ssub_sat", vir.I32, vir.IntLiteral(-2147483648), vir.IntLiteral(1))
			})
		},
		wantValue: val(-2147483648),
	})
}