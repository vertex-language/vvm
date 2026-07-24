// x86_64_windows_stub.go
package crt

import (
	"fmt"

	"github.com/vertex-language/vvm/objectfile/coff"
)

func init() {
	Register("x86_64", "windows", buildX86_64Windows)
}

// buildX86_64Windows hand-encodes _start directly as x86-64 machine bytes,
// the same approach x86_64_linux_stub.go and aarch64_macos_stub.go use for
// their own (arch, os) pairs.
//
// Only SignatureBare is supported. Windows hands the raw entry point no
// argc/argv/envp at all — informally-documented reverse-engineering of
// kernel32's BaseThreadInitThunk (Microsoft doesn't publish its exact
// signature) describes it calling the thread's start routine as a plain
// function taking a start address and a thread parameter, not a parsed
// command line. Real CRT startup recovers argc/argv itself by calling
// GetCommandLineA/W and tokenizing the string — that's a real parser, not
// a register-move sequence, so ArgcArgv/ArgcArgvEnvp fail loudly here
// rather than pretending to stage arguments that were never placed
// anywhere.
//
// Unlike the Linux stub, this ignores BuildArgs.NeedsLibC and always calls
// kernel32's ExitProcess — kernel32.dll is unconditionally mapped into
// every Windows process (the same guaranteed-present role libSystem plays
// for the macOS stub), so there's no bare-syscall fallback to choose
// between the way x86_64-linux has one.
//
// Addend convention: objectfile/coff's implicit-addend writer expects the
// same -4 (ELF-RELA-style "S + A - P") convention x86_64_linux_stub.go
// already uses for its own call sites, then compensates internally for
// AMD64 REL32's "+4" COFF definition (see write.go's applyImplicitAddends
// doc comment) — passing plain -4 here, not 0, is what actually round-trips
// correctly through linker/pe's own coffReadAddend + amd64Patcher.Apply.
//
//	sub  rsp, 40        ; x64 ABI: 32B shadow space + 8B for 16-byte
//	                    ; alignment after the CALL-pushed return address
//	call userMain
//	mov  ecx, eax       ; stage exit code as ExitProcess's first arg (RCX)
//	call ExitProcess    ; noreturn
//	hlt                 ; defined halt if it somehow returns
func buildX86_64Windows(args BuildArgs) (Stub, error) {
	if args.Format != FormatCOFF {
		return Stub{}, fmt.Errorf("crt/x86_64-windows: only coff output is supported, got %s", args.Format)
	}
	if args.Signature != SignatureBare {
		return Stub{}, fmt.Errorf(
			"crt/x86_64-windows: only a zero-argument main() supports automatic "+
				"wiring — Windows hands the raw entry point no argc/argv/envp at "+
				"all (unlike Linux/macOS); name your entry fn \"_start\" and parse "+
				"the command line yourself via GetCommandLineA if you need argv "+
				"(signature %d requested)", args.Signature)
	}

	var code []byte
	var relocs []coff.Reloc
	emit := func(b ...byte) { code = append(code, b...) }

	// sub rsp, 40                48 83 EC 28
	emit(0x48, 0x83, 0xEC, 0x28)

	// call userMain               E8 rel32
	emit(0xE8, 0, 0, 0, 0)
	relocs = append(relocs, coff.Reloc{
		Offset: uint32(len(code) - 4),
		Symbol: args.UserMain,
		Kind:   coff.RelocPCRel32,
		Addend: -4,
	})

	// mov ecx, eax                89 C1
	emit(0x89, 0xC1)

	// call ExitProcess            E8 rel32   (noreturn)
	emit(0xE8, 0, 0, 0, 0)
	relocs = append(relocs, coff.Reloc{
		Offset: uint32(len(code) - 4),
		Symbol: "ExitProcess",
		Kind:   coff.RelocPCRel32,
		Addend: -4,
	})

	// hlt                          F4
	emit(0xF4)

	f := coff.NewFile(coff.TargetWindowsAMD64)
	f.AddSection(coff.Section{
		Kind:  coff.SectionText,
		Align: 16,
		Code:  code,
		Symbols: []coff.Symbol{
			{Name: "_start", Offset: 0, Size: uint32(len(code)),
				Binding: coff.BindingGlobal, Kind: coff.SymFunc},
		},
		Relocs: relocs,
	})

	obj, err := f.Serialize()
	if err != nil {
		return Stub{}, fmt.Errorf("crt/x86_64-windows: %w", err)
	}
	return Stub{Symbol: "_start", Object: obj}, nil
}