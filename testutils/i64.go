// i64.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{
		name:       "i64_literal",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i64PrintingModule("i64_literal", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Emit("v", "mov", vir.I64, vir.IntLiteral(9000000000)) // exceeds i32 range
			})
		},
		wantValue: val(9000000000),
	})

	register(testCase{
		name:       "i64_add",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i64PrintingModule("i64_add", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Add("v", vir.I64, vir.IntLiteral(5000000000), vir.IntLiteral(1))
			})
		},
		wantValue: val(5000000001),
	})

	register(testCase{
		name:       "i64_sub",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i64PrintingModule("i64_sub", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Sub("v", vir.I64, vir.IntLiteral(10000000000), vir.IntLiteral(1))
			})
		},
		wantValue: val(9999999999),
	})

	register(testCase{
		name:       "i64_mul",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i64PrintingModule("i64_mul", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				return fb.Mul("v", vir.I64, vir.IntLiteral(3000000000), vir.IntLiteral(2))
			})
		},
		wantValue: val(6000000000),
	})
}