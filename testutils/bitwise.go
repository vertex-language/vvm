// bitwise.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// and/or/xor/not/shl/lshr/ashr/rotl/rotr/ctlz/cttz/popcnt (ir.md §4
// "Bits"), plus shift-count masking ("Shift counts are masked to the
// operand's bit width").

func init() {
	register(testCase{
		name: "bitwise_and",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_and", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpAnd, vir.I32, vir.IntLiteral(0b1100), vir.IntLiteral(0b1010))
			})
		},
		wantValue: val(0b1000),
	})

	register(testCase{
		name: "bitwise_or",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_or", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpOr, vir.I32, vir.IntLiteral(0b1100), vir.IntLiteral(0b0010))
			})
		},
		wantValue: val(0b1110),
	})

	register(testCase{
		name: "bitwise_xor",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_xor", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpXor, vir.I32, vir.IntLiteral(0b1100), vir.IntLiteral(0b1010))
			})
		},
		wantValue: val(0b0110),
	})

	register(testCase{
		name: "bitwise_not",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_not", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpNot, vir.I32, vir.IntLiteral(0))
			})
		},
		wantValue: val(-1), // ~0 = 0xFFFFFFFF
	})

	// Shift counts are masked mod operand width (§4): shifting an i32 by
	// 33 behaves identically to shifting by 1.
	register(testCase{
		name: "bitwise_shl_masks_count",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_shl_mask", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpShl, vir.I32, vir.IntLiteral(1), vir.IntLiteral(33))
			})
		},
		wantValue: val(2),
	})

	// Shift by 0 is a no-op — the low boundary of the masking rule, distinct
	// from "shift by width" below (both reduce to the same masked count,
	// but this pins the identity case on its own).
	register(testCase{
		name: "bitwise_shl_by_zero_is_noop",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_shl_zero", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpShl, vir.I32, vir.IntLiteral(1234), vir.IntLiteral(0))
			})
		},
		wantValue: val(1234),
	})

	// Shifting an i32 by exactly its own bit width (32) masks to a count of
	// 0 — the high boundary that "shift by 33" (masks to 1) doesn't cover.
	register(testCase{
		name: "bitwise_shl_by_width_masks_to_zero",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_shl_width", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpShl, vir.I32, vir.IntLiteral(1), vir.IntLiteral(32))
			})
		},
		wantValue: val(1), // count masks to 0 mod 32, so this is shl by 0
	})

	register(testCase{
		name: "bitwise_lshr_zero_fills",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_lshr", func(fb *vir.FunctionBuilder) vir.Operand {
				// 0xFFFFFFF8 (-8) >> 1 logical = 0x7FFFFFFC
				return fb.Emit("v", vir.OpLShr, vir.I32, vir.IntLiteral(-8), vir.IntLiteral(1))
			})
		},
		wantValue: val(2147483644),
	})

	register(testCase{
		name: "bitwise_ashr_sign_extends",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_ashr", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpAShr, vir.I32, vir.IntLiteral(-8), vir.IntLiteral(1))
			})
		},
		wantValue: val(-4),
	})

	register(testCase{
		name: "bitwise_rotl",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_rotl", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpRotl, vir.I32, vir.IntLiteral(1), vir.IntLiteral(1))
			})
		},
		wantValue: val(2),
	})

	register(testCase{
		name: "bitwise_rotr",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_rotr", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpRotr, vir.I32, vir.IntLiteral(1), vir.IntLiteral(1))
			})
		},
		wantValue: val(-2147483648), // LSB rotates into the sign bit
	})

	register(testCase{
		name: "bitwise_ctlz",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_ctlz", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpCtlz, vir.I32, vir.IntLiteral(1)) // 0x00000001
			})
		},
		wantValue: val(31),
	})

	register(testCase{
		name: "bitwise_cttz",
		build: func() *vir.Module {
			return i32PrintingModule("bitwise_cttz", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpCttz, vir.I32, vir.IntLiteral(8)) // 0b1000
			})
		},
		wantValue: val(3),
	})

	register(testCase{
		name: "bitwise_popcnt",
		build: func() *vir.Module {
			m := i32PrintingModule("bitwise_popcnt", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", vir.OpPopcnt, vir.I32, vir.IntLiteral(0b1011))
			})
			// Override default target to include the required tier
			m.SetTarget(arch, osName, abiFor(), "popcnt")
			return m
		},
		wantValue: val(3),
	})
}