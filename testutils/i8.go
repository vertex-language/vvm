// i8.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{
		name:       "i8_literal",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i8_literal", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Emit("v", "mov", vir.I8, vir.IntLiteral(100))
				return fb.Emit("vz", "zext", vir.I32, v)
			})
		},
		wantValue: val(100),
	})

	register(testCase{
		name:       "i8_wraps_mod_256",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i8_wrap", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Add("v", vir.I8, vir.IntLiteral(250), vir.IntLiteral(10)) // 260 mod 256 = 4
				return fb.Emit("vz", "zext", vir.I32, v)
			})
		},
		wantValue: val(4),
	})
}