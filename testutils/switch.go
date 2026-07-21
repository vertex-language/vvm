// switch.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Same three-arm switch in both cases; only the scrutinee changes, so one
// case lands on a named label and the other falls through to the default —
// that fallthrough is the one fact "switch_falls_to_default" adds beyond
// "switch_matches_case".

func init() {
	register(testCase{
		name:       "switch_matches_case",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("switch_match", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				x := fb.Emit("x", "mov", vir.I32, vir.IntLiteral(2))
				fb.Switch(x, "default",
					vir.SwitchCase{Value: 1, Label: "case1"},
					vir.SwitchCase{Value: 2, Label: "case2"},
				)

				fb.Label("default")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(0))
				fb.Branch("join")

				fb.Label("case1")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(100))
				fb.Branch("join")

				fb.Label("case2")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(200))
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
				x := fb.Emit("x", "mov", vir.I32, vir.IntLiteral(99))
				fb.Switch(x, "default",
					vir.SwitchCase{Value: 1, Label: "case1"},
					vir.SwitchCase{Value: 2, Label: "case2"},
				)

				fb.Label("default")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(0))
				fb.Branch("join")

				fb.Label("case1")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(100))
				fb.Branch("join")

				fb.Label("case2")
				fb.Emit("v", "mov", vir.I32, vir.IntLiteral(200))
				fb.Branch("join")

				fb.Label("join")
				return vir.Ident("v")
			})
		},
		wantValue: val(0),
	})
}