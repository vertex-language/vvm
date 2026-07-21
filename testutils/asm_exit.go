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
	register(testCase{
		name:       "asm_raw_exit_aarch64",
		hostArches: []string{"aarch64"},
		hostOSes:   []string{"linux"},
		build:      func(a, o string) *vir.Module { return buildAsmExitNative(42) },
		wantExit:   42,
	})
}

func buildAsmExitIntel(code int64) *vir.Module {
	m := vir.NewModule("asm_exit")
	m.SetTarget("x86_64", "linux", "gnu")
	m.SetAsmDialect(vir.DialectIntel)

	fb := m.DeclareFunction("main", nil, vir.I32, true)
	c := fb.Emit("code", "mov", vir.I32, vir.IntLiteral(code))
	fb.BeginAsm().
		In("rdi", c.Ident).
		Clobber("rcx", "r11").
		Code(
			vir.AsmInstructionLine("mov", vir.AsmRegister("rax"), vir.AsmImmediate(vir.IntLiteral(60))),
			vir.AsmInstructionLine("syscall"),
		).
		End()
	fb.Unreachable()
	return m
}

func buildAsmExitNative(code int64) *vir.Module {
	m := vir.NewModule("asm_exit")
	m.SetTarget("aarch64", "linux", "gnu")
	m.SetAsmDialect(vir.DialectNative)

	fb := m.DeclareFunction("main", nil, vir.I32, true)
	c := fb.Emit("code", "mov", vir.I32, vir.IntLiteral(code))
	fb.BeginAsm().
		In("x0", c.Ident).
		Clobber("x8").
		Code(
			vir.AsmInstructionLine("mov", vir.AsmRegister("x8"), vir.AsmImmediate(vir.IntLiteral(93))),
			vir.AsmInstructionLine("svc", vir.AsmImmediate(vir.IntLiteral(0))),
		).
		End()
	fb.Unreachable()
	return m
}