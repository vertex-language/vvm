// i16.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{
		name:       "i16_literal",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i16_literal", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Emit("v", "mov", vir.I16, vir.IntLiteral(30000))
				return fb.Emit("vz", "zext", vir.I32, v)
			})
		},
		wantValue: val(30000),
	})

	register(testCase{
		name:       "i16_wraps_mod_65536",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("i16_wrap", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Add("v", vir.I16, vir.IntLiteral(65530), vir.IntLiteral(10)) // 65540 mod 65536 = 4
				return fb.Emit("vz", "zext", vir.I32, v)
			})
		},
		wantValue: val(4),
	})
}