// intrinsics.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// fma/copysign/floor/ceil/trunc_f/nearest, and smin/smax/umin/umax plus
// bswap/bitrev (ir.md §4 "Intrinsics" — "must compile to 1-2 CPU
// instructions, no libcalls"). This whole opcode family had zero coverage
// before: nothing in the prior suite exercised any of these ten opcodes.
//
// smin/smax/umin/umax are the integer counterparts the spec points to when
// it says bare min/max are illegal on integers (§4 "min.iN/max.iN are
// illegal — use smin/smax/umin/umax"); floats.go already covers the bare
// float min/max NaN/signed-zero behavior, so these cases only need to pin
// down the signed-vs-unsigned reading, mirroring comparisons.go's pattern.

func init() {
	register(testCase{
		name: "intrinsic_fma",
		build: func() *vir.Module {
			return f64PrintingModule("intrinsic_fma", func(fb *vir.FunctionBuilder) vir.Operand {
				// 2.0 * 3.0 + 1.0 = 7.0, computed as a single contracted op
				// (§4: "fma is the only contracted op and only written
				// explicitly").
				return fb.Emit("v", vir.OpFma, vir.F64, vir.FloatLiteral(2.0), vir.FloatLiteral(3.0), vir.FloatLiteral(1.0))
			})
		},
		wantFloatValue: fval(7.0),
	})

	register(testCase{
		name: "intrinsic_copysign",
		build: func() *vir.Module {
			return f64PrintingModule("intrinsic_copysign", func(fb *vir.FunctionBuilder) vir.Operand {
				// Magnitude of the first operand, sign of the second.
				return fb.Emit("v", vir.OpCopysign, vir.F64, vir.FloatLiteral(3.0), vir.FloatLiteral(-1.0))
			})
		},
		wantFloatValue: fval(-3.0),
	})

	register(testCase{
		name: "intrinsic_floor",
		build: func() *vir.Module {
			return f64PrintingModule("intrinsic_floor", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpFloor, vir.F64, vir.FloatLiteral(3.7))
			})
		},
		wantFloatValue: fval(3.0),
	})

	register(testCase{
		name: "intrinsic_ceil",
		build: func() *vir.Module {
			return f64PrintingModule("intrinsic_ceil", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpCeil, vir.F64, vir.FloatLiteral(3.2))
			})
		},
		wantFloatValue: fval(4.0),
	})

	// trunc_f rounds toward zero — the one place its result differs from
	// floor for a negative input (floor(-3.7) would be -4.0).
	register(testCase{
		name: "intrinsic_trunc_f_rounds_toward_zero",
		build: func() *vir.Module {
			return f64PrintingModule("intrinsic_trunc_f", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpTruncF, vir.F64, vir.FloatLiteral(-3.7))
			})
		},
		wantFloatValue: fval(-3.0),
	})

	// nearest uses round-to-nearest-ties-to-even (§1's "Strict Semantics" /
	// §4 float rules), not round-half-away-from-zero — 2.5 rounds down to
	// the even neighbor, 3.5 rounds up to the even neighbor.
	register(testCase{
		name: "intrinsic_nearest_ties_to_even_down",
		build: func() *vir.Module {
			return f64PrintingModule("intrinsic_nearest_down", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpNearest, vir.F64, vir.FloatLiteral(2.5))
			})
		},
		wantFloatValue: fval(2.0),
	})

	register(testCase{
		name: "intrinsic_nearest_ties_to_even_up",
		build: func() *vir.Module {
			return f64PrintingModule("intrinsic_nearest_up", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpNearest, vir.F64, vir.FloatLiteral(3.5))
			})
		},
		wantFloatValue: fval(4.0),
	})

	// --- smin/smax/umin/umax: same bit pattern (-1 == 0xFFFFFFFF), read
	// two different ways, same idiom as comparisons.go's slt/ult pair.
	register(testCase{
		name: "intrinsic_smin_treats_as_signed",
		build: func() *vir.Module {
			return i32PrintingModule("intrinsic_smin", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSMin, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(3))
			})
		},
		wantValue: val(-1),
	})

	register(testCase{
		name: "intrinsic_smax_treats_as_signed",
		build: func() *vir.Module {
			return i32PrintingModule("intrinsic_smax", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpSMax, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(3))
			})
		},
		wantValue: val(3),
	})

	register(testCase{
		name: "intrinsic_umin_treats_as_unsigned",
		build: func() *vir.Module {
			return i32PrintingModule("intrinsic_umin", func(fb *vir.FunctionBuilder) vir.Operand {
				// -1 as unsigned is 0xFFFFFFFF, the larger value — so umin
				// picks 3, the opposite of smin's answer above.
				return fb.Emit("v", vir.OpUMin, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(3))
			})
		},
		wantValue: val(3),
	})

	register(testCase{
		name: "intrinsic_umax_treats_as_unsigned",
		build: func() *vir.Module {
			return i32PrintingModule("intrinsic_umax", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpUMax, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(3))
			})
		},
		wantValue: val(-1),
	})

	// bswap: byte-reverses the 32-bit pattern 0x11223344 -> 0x44332211.
	// i8 is explicitly rejected for bswap (opcode.go comment, §9.20) so
	// i32 is the smallest legal width to test against.
	register(testCase{
		name: "intrinsic_bswap_reverses_bytes",
		build: func() *vir.Module {
			return i32PrintingModule("intrinsic_bswap", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpBSwap, vir.I32, vir.IntLiteral(0x11223344))
			})
		},
		wantValue: val(0x44332211), // 1144201745
	})

	// bitrev: reverses bit order — the single set LSB moves to the MSB,
	// which reads back as INT32_MIN.
	register(testCase{
		name: "intrinsic_bitrev_reverses_bits",
		build: func() *vir.Module {
			return i32PrintingModule("intrinsic_bitrev", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpBitrev, vir.I32, vir.IntLiteral(1))
			})
		},
		wantValue: val(-2147483648),
	})
}