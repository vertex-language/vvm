// tailcall.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Links no libc — same raw-syscall exit path as exit.go — because the fact
// under test is the tailcall terminator itself, not printf plumbing. "main"
// never returns its own value; control transfers to "answer", whose return
// becomes the process's exit code (ir.md §5 tailcall, §9.29).

func init() {
	register(testCase{
		name:       "tailcall_direct",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			m := vir.NewModule("tailcall_direct")

			callee := m.DeclareFunction("answer", nil, vir.I32, false)
			callee.Return(vir.IntLiteral(55))

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			mainFn.TailCall("answer")

			return m
		},
		wantExit: 55,
	})
}