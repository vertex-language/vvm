// arithmetic_bitwise.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "add_wraps_mod_2n", build: func(a, o string) *vir.Module {
		return intPrintingModule("add_wrap", func(fb *vir.FunctionBuilder) vir.Operand {
			r := fb.Add("r", vir.I8, vir.IntLiteral(250), vir.IntLiteral(10)) // wraps to 4
			return fb.Emit("rz", "zext", vir.I32, r)
		})
	}, wantValue: val(4)})

	register(testCase{name: "sub_and_mul", build: func(a, o string) *vir.Module {
		return intPrintingModule("sub_mul", func(fb *vir.FunctionBuilder) vir.Operand {
			s := fb.Sub("s", vir.I32, vir.IntLiteral(50), vir.IntLiteral(8)) // 42
			return fb.Mul("p", vir.I32, s, vir.IntLiteral(1))                // 42
		})
	}, wantValue: val(42)})

	register(testCase{name: "abs_int_min_wraps", build: func(a, o string) *vir.Module {
		return intPrintingModule("abs_intmin", func(fb *vir.FunctionBuilder) vir.Operand {
			return fb.Emit("r", "abs", vir.I32, vir.IntLiteral(-2147483648))
		})
	}, wantValue: val(-2147483648)})

	register(testCase{name: "bitwise_and_or_xor", build: func(a, o string) *vir.Module {
		return intPrintingModule("bitwise", func(fb *vir.FunctionBuilder) vir.Operand {
			and := fb.Emit("and", "and", vir.I32, vir.IntLiteral(0b1100), vir.IntLiteral(0b1010))
			or := fb.Emit("or", "or", vir.I32, and, vir.IntLiteral(0b0001))
			return fb.Emit("xor", "xor", vir.I32, or, vir.IntLiteral(0b1111)) // 6
		})
	}, wantValue: val(6)})

	register(testCase{name: "shift_count_masked", build: func(a, o string) *vir.Module {
		return intPrintingModule("shift_mask", func(fb *vir.FunctionBuilder) vir.Operand {
			return fb.Emit("r", "shl", vir.I32, vir.IntLiteral(1), vir.IntLiteral(33)) // 33 mod 32 = 1
		})
	}, wantValue: val(2)})

	register(testCase{name: "rotl", build: func(a, o string) *vir.Module {
		return intPrintingModule("rotl", func(fb *vir.FunctionBuilder) vir.Operand {
			r := fb.Emit("r", "rotl", vir.I8, vir.IntLiteral(1), vir.IntLiteral(1))
			return fb.Emit("rz", "zext", vir.I32, r)
		})
	}, wantValue: val(2)})
}