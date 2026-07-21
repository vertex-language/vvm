// asm_exit.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{
		name:       "asm_raw_exit_x86_64",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build:      func(a, o string) *vir.Module { return buildAsmExitIntel(42) },
		wantExit:   42,
	})
	// aarch64 case intentionally not registered: only x86_64/linux/gnu is
	// supported for now.
}

func buildAsmExitIntel(code int64) *vir.Module {
	m := vir.NewModule("asm_exit")
	m.SetTarget("x86_64", "linux", "gnu")
	m.SetAsmDialect(vir.DialectIntel)

	// No libc extern group here, so there's no _start -> main shim to rely
	// on; this module is its own process entry point.
	fb := m.DeclareFunction("_start", nil, vir.I32, true)
	c := fb.Emit("code", "mov", vir.I32, vir.IntLiteral(code))
	fb.BeginAsm().
		In("edi", c.Ident). // i32 value -> 32-bit sub-register (§9.36)
		Clobber("rcx", "r11").
		Code(
			vir.AsmInstructionLine("mov", vir.AsmRegister("rax"), vir.AsmImmediate(vir.IntLiteral(60))),
			vir.AsmInstructionLine("syscall"),
		).
		End()
	fb.Unreachable()
	return m
}