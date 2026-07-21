// tailcall_indirect.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Companion to tailcall.go's direct case: same no-libc raw-exit shape, but
// the callee is reached through a fnsig-typed function pointer rather than
// by name, exercising tailcall.<fnsig> (ir.md §4 Calls & Control, §9.29). A
// bare fn name in operand position already yields its address as ptr
// (§4 Addresses), so no separate global slot is needed to get the pointer.
func init() {
	register(testCase{
		name:       "tailcall_indirect",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			m := vir.NewModule("tailcall_indirect")

			sig := m.DeclareFunctionSignature("answer_sig", nil, false, vir.I32)

			callee := m.DeclareFunction("answer", nil, vir.I32, false)
			callee.Return(vir.IntLiteral(77))

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			mainFn.TailCallIndirect(sig.Name, vir.Ident("answer"))
			return m
		},
		wantExit: 77,
	})
}