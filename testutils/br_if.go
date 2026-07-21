// br_if.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Both cases share one shape: assign "v" in each arm, join, and print it —
// the only thing that differs is which literal condition steers br_if, and
// which arm's assignment should therefore be the one that's visible at the
// join label (Join Convention, ir.md §5).

func init() {
	register(testCase{
		name:       "br_if_true_takes_then",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("br_if_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				fb.BranchIf(vir.BoolLiteral(true), "then", "else")

				fb.Label("then")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(111))
				fb.Branch("join")

				fb.Label("else")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(222))
				fb.Branch("join")

				fb.Label("join")
				return vir.Ident("v")
			})
		},
		wantValue: val(111),
	})

	register(testCase{
		name:       "br_if_false_takes_else",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("br_if_false", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				fb.BranchIf(vir.BoolLiteral(false), "then", "else")

				fb.Label("then")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(111))
				fb.Branch("join")

				fb.Label("else")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(222))
				fb.Branch("join")

				fb.Label("join")
				return vir.Ident("v")
			})
		},
		wantValue: val(222),
	})
}