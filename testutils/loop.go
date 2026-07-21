// loop.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// The one fact under test here is loop-carried values (ir.md §5 rule 4):
// "sum" and "i" are each assigned once before the loop and reassigned once
// per iteration in the loop body, satisfying definite-assignment on both
// the entry edge and the back edge into "loop".

func init() {
	register(testCase{
		name:       "loop_sum_1_to_5",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("loop_sum", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				fb.Emit("i", "mov", vir.I32, vir.IntLiteral(1))
				fb.Emit("sum", "mov", vir.I32, vir.IntLiteral(0))
				fb.Branch("loop")

				fb.Label("loop")
				cond := fb.Emit("cond", "sle", vir.I32, vir.Ident("i"), vir.IntLiteral(5))
				fb.BranchIf(cond, "body", "done")

				fb.Label("body")
				fb.Add("sum", vir.I32, vir.Ident("sum"), vir.Ident("i"))
				fb.Add("i", vir.I32, vir.Ident("i"), vir.IntLiteral(1))
				fb.Branch("loop")

				fb.Label("done")
				return vir.Ident("sum")
			})
		},
		wantValue: val(15), // 1+2+3+4+5
	})
}