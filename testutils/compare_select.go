// compare_select.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "compare_and_select", build: func(a, o string) *vir.Module {
		return intPrintingModule("select", func(fb *vir.FunctionBuilder) vir.Operand {
			cond := fb.Emit("cond", "slt", vir.I32, vir.IntLiteral(3), vir.IntLiteral(9))
			return fb.Emit("r", "select", vir.I32, cond, vir.IntLiteral(111), vir.IntLiteral(222))
		})
	}, wantValue: val(111)})

	register(testCase{name: "smin_umax_intrinsics", build: func(a, o string) *vir.Module {
		return intPrintingModule("minmax", func(fb *vir.FunctionBuilder) vir.Operand {
			mn := fb.Emit("mn", "smin", vir.I32, vir.IntLiteral(-5), vir.IntLiteral(3))
			mx := fb.Emit("mx", "umax", vir.I32, vir.IntLiteral(10), vir.IntLiteral(20))
			return fb.Add("r", vir.I32, mn, mx) // 15
		})
	}, wantValue: val(15)})
}