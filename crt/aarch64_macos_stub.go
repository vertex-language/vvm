// aarch64_macos_stub.go
package crt

import (
	"fmt"

	"github.com/vertex-language/vvm/objectfile/macho"
)

func init() {
	Register("aarch64", "macos", buildAarch64Macos)
}

// buildAarch64Macos hand-encodes the process-entry stub directly as
// AArch64 machine bytes, per isa/aarch64's own documented instruction
// field layout — same as x86_64_linux_stub.go, one arch/os cell at a
// time.
//
// This stub calls _main and _exit *indirectly* — adrp/add to materialize
// each symbol's address into a register, then blr through it — rather
// than with a direct `bl`. That's deliberate, not stylistic: a direct
// `bl _main`/`bl _exit` here was observed corrupting the target
// instruction's own opcode bits at link time (verified via `otool -tV`
// against a real linked binary — the patched low-26-bit branch offset
// came out numerically correct, but the fixed top-6 BL opcode bits came
// out zeroed, producing an illegal instruction / SIGILL at runtime).
// That only reproduces on this stub's *direct*, non-PLT branch path —
// `bl printf` from ordinary lowered code goes through PLT-stub injection
// instead and disassembles correctly — so the adrp+add+blr sequence
// below is a working, independently-verified substitute (the identical
// adrp/add pair a global reference like `fmt` already uses correctly),
// not a guess. If linker/macho/arm64's direct-branch BRANCH26 patcher is
// fixed later, this can revert to two plain `bl`s — tracked as a known
// workaround, not a permanent design choice.
//
// Unlike x86_64_linux_stub.go, this has no bare-syscall branch at all:
// macOS offers no documented, stable process-entry syscall contract the
// way Linux's syscall instruction does, so BuildArgs.NeedsLibC == false
// is a hard error here rather than a fallback path.
//
// It's also simpler than the Linux stub in argument staging: on arm64
// macOS, dyld's own bootstrap (__dyld_start, per Apple's dyldStartup.s)
// already parses the kernel's raw process-entry stack and hands the
// resolved argc/argv/envp to this object's entry symbol *in registers*
// — x0=argc, x1=argv, x2=envp — exactly where AAPCS64 already expects a
// function's first three arguments, so there is nothing to stage. This
// register-convention claim has not been independently verified against
// a physical target the same way the branch-corruption workaround above
// was; flagged the same way linker/macho's own README tracks its
// unverified xros/xrsimulator PLT coverage.
//
//	adrp x9, _main   ; ARM64_RELOC_PAGE21
//	add  x9, x9, _main  ; ARM64_RELOC_PAGEOFF12  (argc/argv/envp already in x0/x1/x2)
//	blr  x9
//	adrp x9, _exit   ; ARM64_RELOC_PAGE21        (main's return value is already in x0/w0)
//	add  x9, x9, _exit  ; ARM64_RELOC_PAGEOFF12
//	blr  x9
//	brk  #0          ; defined trap, in case exit somehow returns
func buildAarch64Macos(args BuildArgs) (Stub, error) {
	if args.Format != FormatMachO {
		return Stub{}, fmt.Errorf("crt/aarch64-macos: only macho output is supported, got %s", args.Format)
	}
	if !args.NeedsLibC {
		return Stub{}, fmt.Errorf(
			"crt/aarch64-macos: no libSystem dependency declared — macOS has no " +
				"documented raw-syscall process-exit convention to fall back to " +
				"(unlike Linux's syscall instruction), so automatic main() wiring " +
				"requires `link shared \"System\"`; alternatively name your entry " +
				"fn \"_start\" and write the process-entry sequence yourself")
	}
	switch args.Signature {
	case SignatureBare, SignatureArgcArgv, SignatureArgcArgvEnvp:
		// No register staging needed for any of the three: dyld already
		// places argc/argv/envp in x0/x1/x2, which is exactly where
		// AAPCS64 expects a function's first three arguments — a
		// signature that only wants fewer of them simply leaves the
		// extra incoming registers unread.
	default:
		return Stub{}, fmt.Errorf("crt/aarch64-macos: unrecognized signature %d", args.Signature)
	}

	var code []byte
	var relocs []macho.Reloc
	emit := func(b ...byte) { code = append(code, b...) }

	callIndirect := func(sym string) {
		// adrp x9, sym                 09 00 00 90
		emit(0x09, 0x00, 0x00, 0x90)
		relocs = append(relocs, macho.Reloc{
			Offset: uint32(len(code) - 4),
			Symbol: sym,
			Kind:   macho.RelocADRPage21,
			Addend: 0,
		})
		// add  x9, x9, :lo12:sym        29 01 00 91
		emit(0x29, 0x01, 0x00, 0x91)
		relocs = append(relocs, macho.Reloc{
			Offset: uint32(len(code) - 4),
			Symbol: sym,
			Kind:   macho.RelocAddOff12,
			Addend: 0,
		})
		// blr  x9                      20 01 3F D6
		emit(0x20, 0x01, 0x3F, 0xD6)
	}

	callIndirect(args.UserMain)
	callIndirect("exit")

	// brk #0                          00 00 20 D4
	emit(0x00, 0x00, 0x20, 0xD4)

	f := macho.NewFile(macho.TargetDarwinARM64)
	f.AddSection(macho.Section{
		Kind:  macho.SectionText,
		Align: 4,
		Code:  code,
		Symbols: []macho.Symbol{
			// Name "start", not "_start": write.go's symbol encoder always
			// prepends "_" itself (Mach-O's universal C-symbol convention),
			// so this becomes the on-disk/link-visible symbol "_start" —
			// matching the "_start" string vvm's own build.go/dispatch.go
			// pass around uniformly across every format.
			{Name: "start", Offset: 0, Size: uint32(len(code)),
				Binding: macho.BindingGlobal, Kind: macho.SymFunc},
		},
		Relocs: relocs,
	})

	obj, err := f.Serialize()
	if err != nil {
		return Stub{}, fmt.Errorf("crt/aarch64-macos: %w", err)
	}
	return Stub{Symbol: "_start", Object: obj}, nil
}