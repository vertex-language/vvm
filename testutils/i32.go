// i32.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{
		name:       "i32_literal",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i32_literal", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "mov", vir.I32, vir.IntLiteral(-12345))
			})
		},
		wantValue: val(-12345),
	})

	register(testCase{
		name:       "i32_add",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i32_add", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Add("v", vir.I32, vir.IntLiteral(100), vir.IntLiteral(23))
			})
		},
		wantValue: val(123),
	})

	register(testCase{
		name:       "i32_sub",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i32_sub", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Sub("v", vir.I32, vir.IntLiteral(50), vir.IntLiteral(8))
			})
		},
		wantValue: val(42),
	})

	register(testCase{
		name:       "i32_mul",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i32_mul", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Mul("v", vir.I32, vir.IntLiteral(6), vir.IntLiteral(7))
			})
		},
		wantValue: val(42),
	})

	register(testCase{
		name:       "i32_add_wraps_mod_2_32",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i32_add_wrap", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Add("v", vir.I32, vir.IntLiteral(2147483647), vir.IntLiteral(1)) // INT32_MAX + 1
			})
		},
		wantValue: val(-2147483648),
	})
}