// integers.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// i8/i16/i32/i64 literals, arithmetic, and modular wraparound (ir.md §4
// "Integer semantics: all iN add/sub/mul/neg wrap modulo 2^N").

func init() {
	register(testCase{
		name: "i8_literal",
		build: func() *vir.Module {
			return i32PrintingModule("i8_literal", func(fb *vir.FunctionBuilder) vir.Operand {
				v := identity(fb, "v", vir.I8, vir.IntLiteral(100))
				return fb.Emit("vz", vir.OpZext, vir.I32, v)
			})
		},
		wantValue: val(100),
	})

	register(testCase{
		name: "i8_wraps_mod_256",
		build: func() *vir.Module {
			return i32PrintingModule("i8_wrap", func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Add("v", vir.I8, vir.IntLiteral(250), vir.IntLiteral(10)) // 260 mod 256 = 4
				return fb.Emit("vz", vir.OpZext, vir.I32, v)
			})
		},
		wantValue: val(4),
	})

	register(testCase{
		name: "i16_literal",
		build: func() *vir.Module {
			return i32PrintingModule("i16_literal", func(fb *vir.FunctionBuilder) vir.Operand {
				v := identity(fb, "v", vir.I16, vir.IntLiteral(30000))
				return fb.Emit("vz", vir.OpZext, vir.I32, v)
			})
		},
		wantValue: val(30000),
	})

	register(testCase{
		name: "i16_wraps_mod_65536",
		build: func() *vir.Module {
			return i32PrintingModule("i16_wrap", func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Add("v", vir.I16, vir.IntLiteral(65530), vir.IntLiteral(10)) // 65540 mod 65536 = 4
				return fb.Emit("vz", vir.OpZext, vir.I32, v)
			})
		},
		wantValue: val(4),
	})

	register(testCase{
		name: "i32_literal",
		build: func() *vir.Module {
			return i32PrintingModule("i32_literal", func(fb *vir.FunctionBuilder) vir.Operand {
				return identity(fb, "v", vir.I32, vir.IntLiteral(-12345))
			})
		},
		wantValue: val(-12345),
	})

	register(testCase{
		name: "i32_add",
		build: func() *vir.Module {
			return i32PrintingModule("i32_add", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Add("v", vir.I32, vir.IntLiteral(100), vir.IntLiteral(23))
			})
		},
		wantValue: val(123),
	})

	register(testCase{
		name: "i32_sub",
		build: func() *vir.Module {
			return i32PrintingModule("i32_sub", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Sub("v", vir.I32, vir.IntLiteral(50), vir.IntLiteral(8))
			})
		},
		wantValue: val(42),
	})

	register(testCase{
		name: "i32_mul",
		build: func() *vir.Module {
			return i32PrintingModule("i32_mul", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Mul("v", vir.I32, vir.IntLiteral(6), vir.IntLiteral(7))
			})
		},
		wantValue: val(42),
	})

	register(testCase{
		name: "i32_add_wraps_mod_2_32",
		build: func() *vir.Module {
			return i32PrintingModule("i32_add_wrap", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Add("v", vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(1)) // INT32_MAX + 1
			})
		},
		wantValue: val(-2147483648),
	})

	register(testCase{
		name: "i64_literal",
		build: func() *vir.Module {
			return i64PrintingModule("i64_literal", func(fb *vir.FunctionBuilder) vir.Operand {
				return identity(fb, "v", vir.I64, vir.IntLiteral(9000000000)) // exceeds i32 range
			})
		},
		wantValue: val(9000000000),
	})

	register(testCase{
		name: "i64_add",
		build: func() *vir.Module {
			return i64PrintingModule("i64_add", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Add("v", vir.I64, vir.IntLiteral(5000000000), vir.IntLiteral(1))
			})
		},
		wantValue: val(5000000001),
	})

	register(testCase{
		name: "i64_sub",
		build: func() *vir.Module {
			return i64PrintingModule("i64_sub", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Sub("v", vir.I64, vir.IntLiteral(10000000000), vir.IntLiteral(1))
			})
		},
		wantValue: val(9999999999),
	})

	register(testCase{
		name: "i64_mul",
		build: func() *vir.Module {
			return i64PrintingModule("i64_mul", func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Mul("v", vir.I64, vir.IntLiteral(3000000000), vir.IntLiteral(2))
			})
		},
		wantValue: val(6000000000),
	})
}