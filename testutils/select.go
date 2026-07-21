// select.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// select.<type> is the first construct in this suite to actually consume
// an i1 produced by a comparison rather than only using i1 via a literal
// br_if condition. Both operands of select are always evaluated (ir.md §4
// Selection); these two cases only check which one gets chosen.
func init() {
	register(testCase{
		name:       "select_true_branch",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("select_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				cond := fb.Emit("cond", "slt", vir.I32, vir.IntLiteral(3), vir.IntLiteral(5)) // 3 < 5
				return fb.Emit("v", "select", vir.I32, cond, vir.IntLiteral(10), vir.IntLiteral(20))
			})
		},
		wantValue: val(10),
	})

	register(testCase{
		name:       "select_false_branch",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("select_false", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				cond := fb.Emit("cond", "sgt", vir.I32, vir.IntLiteral(3), vir.IntLiteral(5)) // 3 > 5
				return fb.Emit("v", "select", vir.I32, cond, vir.IntLiteral(10), vir.IntLiteral(20))
			})
		},
		wantValue: val(20),
	})
}