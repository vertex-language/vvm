// exit.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// These two don't link libc at all — no printf, nothing to flush — so
// they exercise entrythunk.go's plain raw-syscall exit path rather than
// the libc exit() path i32.go/i64.go's printerModule triggers.

func init() {
	register(testCase{
		name:       "exit_code_zero",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			m := vir.NewModule("exit_zero")
			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			fb.Return(vir.IntLiteral(0))
			return m
		},
		wantExit: 0,
	})

	register(testCase{
		name:       "exit_code_custom",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			m := vir.NewModule("exit_custom")
			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			fb.Return(vir.IntLiteral(42))
			return m
		},
		wantExit: 42,
	})
}