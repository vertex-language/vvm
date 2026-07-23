// bitwise.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// and/or/xor/not/shl/lshr/ashr/rotl/rotr/ctlz/cttz/popcnt (ir.md §4
// "Bits"), plus shift-count masking ("Shift counts are masked to the
// operand's bit width").

func init() {
	register(testCase{
		name:       "bitwise_and",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_and", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpAnd, vir.I32, vir.IntLiteral(0b1100), vir.IntLiteral(0b1010))
			})
		},
		wantValue: val(0b1000),
	})

	register(testCase{
		name:       "bitwise_or",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_or", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpOr, vir.I32, vir.IntLiteral(0b1100), vir.IntLiteral(0b0010))
			})
		},
		wantValue: val(0b1110),
	})

	register(testCase{
		name:       "bitwise_xor",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_xor", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpXor, vir.I32, vir.IntLiteral(0b1100), vir.IntLiteral(0b1010))
			})
		},
		wantValue: val(0b0110),
	})

	register(testCase{
		name:       "bitwise_not",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_not", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpNot, vir.I32, vir.IntLiteral(0))
			})
		},
		wantValue: val(-1), // ~0 = 0xFFFFFFFF
	})

	// Shift counts are masked mod operand width (§4): shifting an i32 by
	// 33 behaves identically to shifting by 1.
	register(testCase{
		name:       "bitwise_shl_masks_count",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_shl_mask", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpShl, vir.I32, vir.IntLiteral(1), vir.IntLiteral(33))
			})
		},
		wantValue: val(2),
	})

	register(testCase{
		name:       "bitwise_lshr_zero_fills",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_lshr", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				// 0xFFFFFFF8 (-8) >> 1 logical = 0x7FFFFFFC
				return fb.Emit("v", vir.OpLShr, vir.I32, vir.IntLiteral(-8), vir.IntLiteral(1))
			})
		},
		wantValue: val(2147483644),
	})

	register(testCase{
		name:       "bitwise_ashr_sign_extends",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_ashr", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpAShr, vir.I32, vir.IntLiteral(-8), vir.IntLiteral(1))
			})
		},
		wantValue: val(-4),
	})

	register(testCase{
		name:       "bitwise_rotl",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_rotl", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpRotl, vir.I32, vir.IntLiteral(1), vir.IntLiteral(1))
			})
		},
		wantValue: val(2),
	})

	register(testCase{
		name:       "bitwise_rotr",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_rotr", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpRotr, vir.I32, vir.IntLiteral(1), vir.IntLiteral(1))
			})
		},
		wantValue: val(-2147483648), // LSB rotates into the sign bit
	})

	register(testCase{
		name:       "bitwise_ctlz",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_ctlz", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpCtlz, vir.I32, vir.IntLiteral(1)) // 0x00000001
			})
		},
		wantValue: val(31),
	})

	register(testCase{
		name:       "bitwise_cttz",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_cttz", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpCttz, vir.I32, vir.IntLiteral(8)) // 0b1000
			})
		},
		wantValue: val(3),
	})

	register(testCase{
		name:       "bitwise_popcnt",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("bitwise_popcnt", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpPopcnt, vir.I32, vir.IntLiteral(0b1011))
			})
		},
		wantValue: val(3),
	})
}