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
// This stub's shape is deliberately different from x86_64_linux_stub.go,
// not just a byte-for-byte port of it. x86_64-linux's raw syscall
// fallback exists because Linux's syscall ABI is stable, documented, and
// something a bare, libc-free process is entitled to use. macOS offers
// no equivalent: the Mach `svc 0x80` trap works today, but it is not a
// documented, stable process-entry contract the way Linux's `syscall`
// instruction is — Apple's own toolchains never emit an unlinked-against-
// libSystem executable this way. So unlike the Linux stub, this one has
// no bare-syscall branch at all: BuildArgs.NeedsLibC == false is a hard
// error here, not a fallback path.
//
// It's also simpler than the Linux stub for a different reason: on
// arm64 macOS, dyld's own bootstrap (__dyld_start, per Apple's
// dyldStartup.s) already parses the kernel's raw process-entry stack and
// hands the resolved argc/argv/envp to this object's entry symbol
// *in registers* — x0=argc, x1=argv, x2=envp — matching where a
// recognized main() shape already expects them (AAPCS64 arg0/arg1/arg2).
// x86_64-linux's stub has to read `sp` itself and stage those registers
// by hand because nothing upstream of it already did that work; here,
// nothing upstream needs undoing. This has not been verified against a
// physical arm64 macOS target — flagged the same way linker/macho's own
// README tracks its unverified xros/xrsimulator PLT coverage — but it
// follows directly from the (arch, os) split this repo already draws:
// libSystem symbols resolve as ordinary undefined externals either way,
// so getting this wrong would surface as a crash before `main` runs, not
// a silent miscompile.
//
//	bl _main    ; ARM64_RELOC_BRANCH26, argc/argv/envp already in x0/x1/x2
//	bl _exit    ; main's return value is already in x0/w0, exit's arg0 slot
//	brk #0      ; defined trap, in case exit somehow returns
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
		// places argc/argv/envp in x0/x1/x2 (see doc comment above), which
		// is exactly where AAPCS64 expects a function's first three
		// arguments — a signature that only wants fewer of them simply
		// leaves the extra incoming registers unread.
	default:
		return Stub{}, fmt.Errorf("crt/aarch64-macos: unrecognized signature %d", args.Signature)
	}

	var code []byte
	var relocs []macho.Reloc
	emit := func(b ...byte) { code = append(code, b...) }

	// bl _main                     94 00 00 00  (imm26=0; the real branch
	//                               offset is computed by the *linker*'s
	//                               ARM64_RELOC_BRANCH26 patcher at link
	//                               time, not written here — see object.go's
	//                               "ARM64-family instruction relocations
	//                               encode nothing useful in the instruction
	//                               word" note. Addend stays 0.)
	emit(0x94, 0x00, 0x00, 0x00)
	relocs = append(relocs, macho.Reloc{
		Offset: uint32(len(code) - 4),
		Symbol: args.UserMain,
		Kind:   macho.RelocPCRel26,
		Addend: 0,
	})

	// bl _exit                     94 00 00 00  (main's return value is
	//                               already sitting in x0/w0 — the exact
	//                               register exit's own first argument
	//                               needs — so there is nothing to move
	//                               between the two calls.)
	emit(0x94, 0x00, 0x00, 0x00)
	relocs = append(relocs, macho.Reloc{
		Offset: uint32(len(code) - 4),
		Symbol: "exit",
		Kind:   macho.RelocPCRel26,
		Addend: 0,
	})

	// brk #0                       00 00 20 D4
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