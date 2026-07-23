// process_exit.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Plain exit-code cases with no libc linked at all — no printf, nothing
// to flush — so they exercise entrythunk.go's raw-syscall exit path
// rather than the libc exit() path helpers.go's printerModule triggers.
//
// Deliberately not included here: `trap`/`unreachable` as an expected
// process outcome. Both are legitimate terminators worth testing, but I
// don't have a confirmed answer for what vvm.RunModule reports in
// res.ExitCode when the process dies that way (signal-based exit codes
// are a convention, not something visible from this package alone) — add
// those once that's pinned down, rather than guessing a number here.

func init() {
	register(testCase{
		name:       "exit_code_zero",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			m := vir.NewModule("exit_zero")
			m.SetTarget(a, o, abiFor(o))
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
			m.SetTarget(a, o, abiFor(o))
			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			fb.Return(vir.IntLiteral(42))
			return m
		},
		wantExit: 42,
	})
}