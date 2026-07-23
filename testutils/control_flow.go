// control_flow.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// br, br_if, switch, select, and loop-carried values (ir.md §5 Join
// Convention). Each case here is deliberately still "one fact": which arm
// of a single branch/switch/select gets taken, or that a loop-carried
// value satisfies definite-assignment on both the entry edge and the back
// edge.

func init() {
	register(testCase{
		name:       "br_unconditional",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("br_unconditional", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := identity(fb, "v", vir.I32, vir.IntLiteral(7))
				fb.Branch("cont")
				fb.Label("cont")
				return v
			})
		},
		wantValue: val(7),
	})

	register(testCase{
		name:       "br_if_true_takes_then",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("br_if_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				fb.BranchIf(vir.BoolLiteral(true), "then", "else")

				fb.Label("then")
				identity(fb, "v", vir.I32, vir.IntLiteral(111))
				fb.Branch("join")

				fb.Label("else")
				identity(fb, "v", vir.I32, vir.IntLiteral(222))
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
				identity(fb, "v", vir.I32, vir.IntLiteral(111))
				fb.Branch("join")

				fb.Label("else")
				identity(fb, "v", vir.I32, vir.IntLiteral(222))
				fb.Branch("join")

				fb.Label("join")
				return vir.Ident("v")
			})
		},
		wantValue: val(222),
	})

	register(testCase{
		name:       "switch_matches_case",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("switch_match", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				x := identity(fb, "x", vir.I32, vir.IntLiteral(2))
				fb.Switch(x, "default",
					vir.SwitchCase{Value: 1, Label: "case1"},
					vir.SwitchCase{Value: 2, Label: "case2"},
				)

				fb.Label("default")
				identity(fb, "v", vir.I32, vir.IntLiteral(0))
				fb.Branch("join")

				fb.Label("case1")
				identity(fb, "v", vir.I32, vir.IntLiteral(100))
				fb.Branch("join")

				fb.Label("case2")
				identity(fb, "v", vir.I32, vir.IntLiteral(200))
				fb.Branch("join")

				fb.Label("join")
				return vir.Ident("v")
			})
		},
		wantValue: val(200),
	})

	register(testCase{
		name:       "switch_falls_to_default",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("switch_default", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				x := identity(fb, "x", vir.I32, vir.IntLiteral(99))
				fb.Switch(x, "default",
					vir.SwitchCase{Value: 1, Label: "case1"},
					vir.SwitchCase{Value: 2, Label: "case2"},
				)

				fb.Label("default")
				identity(fb, "v", vir.I32, vir.IntLiteral(0))
				fb.Branch("join")

				fb.Label("case1")
				identity(fb, "v", vir.I32, vir.IntLiteral(100))
				fb.Branch("join")

				fb.Label("case2")
				identity(fb, "v", vir.I32, vir.IntLiteral(200))
				fb.Branch("join")

				fb.Label("join")
				return vir.Ident("v")
			})
		},
		wantValue: val(0),
	})

	register(testCase{
		name:       "select_true_branch",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("select_true", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				cond := fb.Emit("cond", vir.OpSlt, vir.I32, vir.IntLiteral(3), vir.IntLiteral(5))
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(10), vir.IntLiteral(20))
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
				cond := fb.Emit("cond", vir.OpSgt, vir.I32, vir.IntLiteral(3), vir.IntLiteral(5))
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(10), vir.IntLiteral(20))
			})
		},
		wantValue: val(20),
	})

	register(testCase{
		name:       "loop_sum_1_to_5",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("loop_sum", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				identity(fb, "i", vir.I32, vir.IntLiteral(1))
				identity(fb, "sum", vir.I32, vir.IntLiteral(0))
				fb.Branch("loop")

				fb.Label("loop")
				cond := fb.Emit("cond", vir.OpSle, vir.I32, vir.Ident("i"), vir.IntLiteral(5))
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