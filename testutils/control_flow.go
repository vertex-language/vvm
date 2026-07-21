// control_flow.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "loop_join_convention", build: func(a, o string) *vir.Module {
		return intPrintingModule("loop_sum", func(fb *vir.FunctionBuilder) vir.Operand {
			fb.Emit("i", "mov", vir.I32, vir.IntLiteral(1))
			fb.Emit("sum", "mov", vir.I32, vir.IntLiteral(0))
			fb.Branch("loop")

			fb.Label("loop")
			cond := fb.Emit("cond", "sle", vir.I32, vir.Ident("i"), vir.IntLiteral(10))
			fb.BranchIf(cond, "body", "done")

			fb.Label("body")
			fb.Emit("sum", "add", vir.I32, vir.Ident("sum"), vir.Ident("i"))
			fb.Emit("i", "add", vir.I32, vir.Ident("i"), vir.IntLiteral(1))
			fb.Branch("loop")

			fb.Label("done")
			return vir.Ident("sum")
		})
	}, wantValue: val(55)})

	register(testCase{name: "switch_terminator", build: func(a, o string) *vir.Module {
		return intPrintingModule("switch_ex", func(fb *vir.FunctionBuilder) vir.Operand {
			fb.Switch(vir.IntLiteral(2), "def",
				vir.SwitchCase{Value: 1, Label: "case1"},
				vir.SwitchCase{Value: 2, Label: "case2"})

			fb.Label("case1")
			fb.Emit("r", "mov", vir.I32, vir.IntLiteral(10))
			fb.Branch("done")

			fb.Label("case2")
			fb.Emit("r", "mov", vir.I32, vir.IntLiteral(20))
			fb.Branch("done")

			fb.Label("def")
			fb.Emit("r", "mov", vir.I32, vir.IntLiteral(0))
			fb.Branch("done")

			fb.Label("done")
			return vir.Ident("r")
		})
	}, wantValue: val(20)})
}