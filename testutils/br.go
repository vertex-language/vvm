// br.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{
		name:       "br_unconditional",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("br_unconditional", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Emit("v", "mov", vir.I32, vir.IntLiteral(7))
				fb.Branch("cont")
				fb.Label("cont")
				return v
			})
		},
		wantValue: val(7),
	})
}