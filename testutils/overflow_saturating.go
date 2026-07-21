// overflow_saturating.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "uaddo_detects_overflow", build: func(a, o string) *vir.Module {
		return intPrintingModule("uaddo", func(fb *vir.FunctionBuilder) vir.Operand {
			ov := fb.Emit("ov", "uaddo", vir.I8, vir.IntLiteral(200), vir.IntLiteral(100))
			return fb.Emit("ovz", "zext", vir.I32, ov)
		})
	}, wantValue: val(1)})

	register(testCase{name: "uadd_sat_clamps", build: func(a, o string) *vir.Module {
		return intPrintingModule("uadd_sat", func(fb *vir.FunctionBuilder) vir.Operand {
			r := fb.Emit("r", "uadd_sat", vir.I8, vir.IntLiteral(200), vir.IntLiteral(100))
			return fb.Emit("rz", "zext", vir.I32, r)
		})
	}, wantValue: val(255)})

	register(testCase{name: "umulh_widening", build: func(a, o string) *vir.Module {
		return intPrintingModule("umulh", func(fb *vir.FunctionBuilder) vir.Operand {
			return fb.Emit("r", "umulh", vir.I32, vir.IntLiteral(-2147483648), vir.IntLiteral(2))
		})
	}, wantValue: val(1)})
}