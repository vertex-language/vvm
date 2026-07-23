// x86_64_linux_stub.go
package crt

import (
	"fmt"

	"github.com/vertex-language/vvm/objectfile/elf"
)

func init() {
	Register("x86_64", "linux", buildX86_64Linux)
}

// buildX86_64Linux hand-encodes _start directly as x86-64 machine bytes,
// per isa/x86_64's own documented register/ModRM/SIB/REX field layout —
// this is that package's tables applied by hand, not through Encode.
//
//	mov rax, rsp                  ; capture argc/argv/envp's base before
//	                               ; anything else moves rsp
//	[mov edi, [rax]]              ; argc, if the signature wants it
//	[lea rsi, [rax+8]]            ; argv, if the signature wants it
//	[lea rdx, [rax+rdi*8+16]]     ; envp, if the signature wants it
//	call userMain                 ; R_X86_64_PLT32, addend -4
//	mov edi, eax                  ; stage the i32 return value as the exit code
//	call exit                     ; if the module links libc (flushes stdio)
//	  -- or --
//	mov eax, 60 ; syscall         ; SYS_exit, otherwise
//	hlt                           ; defined halt if either somehow returned
func buildX86_64Linux(args BuildArgs) (Stub, error) {
	if args.Format != FormatELF {
		return Stub{}, fmt.Errorf("crt/x86_64-linux: only elf output is supported, got %s", args.Format)
	}

	var code []byte
	var relocs []elf.Reloc
	emit := func(b ...byte) { code = append(code, b...) }

	// mov rax, rsp                48 89 E0
	emit(0x48, 0x89, 0xE0)

	var wantArgc, wantArgv, wantEnvp bool
	switch args.Signature {
	case SignatureBare:
	case SignatureArgcArgv:
		wantArgc, wantArgv = true, true
	case SignatureArgcArgvEnvp:
		wantArgc, wantArgv, wantEnvp = true, true, true
	default:
		return Stub{}, fmt.Errorf("crt/x86_64-linux: unrecognized signature %d", args.Signature)
	}

	if wantArgc {
		// mov edi, [rax]           8B 38
		emit(0x8B, 0x38)
	}
	if wantArgv {
		// lea rsi, [rax+8]         48 8D 70 08
		emit(0x48, 0x8D, 0x70, 0x08)
	}
	if wantEnvp {
		// lea rdx, [rax+rdi*8+16]  48 8D 54 F8 10
		emit(0x48, 0x8D, 0x54, 0xF8, 0x10)
	}

	// call userMain                E8 rel32
	emit(0xE8, 0, 0, 0, 0)
	relocs = append(relocs, elf.Reloc{
		Offset: uint32(len(code) - 4),
		Symbol: args.UserMain,
		Kind:   elf.RelocPLT32,
		Addend: -4,
	})

	// mov edi, eax                 89 C7
	emit(0x89, 0xC7)

	if args.NeedsLibC {
		// call exit                  E8 rel32   (noreturn: flushes libc's
		// buffered stdio, which a bare SYS_exit below would silently skip)
		emit(0xE8, 0, 0, 0, 0)
		relocs = append(relocs, elf.Reloc{
			Offset: uint32(len(code) - 4),
			Symbol: "exit",
			Kind:   elf.RelocPLT32,
			Addend: -4,
		})
	} else {
		// mov eax, 60 ; syscall       B8 3C 00 00 00 ; 0F 05
		emit(0xB8, 0x3C, 0x00, 0x00, 0x00)
		emit(0x0F, 0x05)
	}

	// hlt                          F4
	emit(0xF4)

	f := elf.NewFile(elf.TargetLinuxAMD64)
	f.AddSection(elf.Section{
		Kind:  elf.SectionText,
		Align: 16,
		Code:  code,
		Symbols: []elf.Symbol{
			{Name: "_start", Offset: 0, Size: uint32(len(code)),
				Binding: elf.BindingGlobal, Kind: elf.SymFunc},
		},
		Relocs: relocs,
	})

	obj, err := f.Serialize()
	if err != nil {
		return Stub{}, fmt.Errorf("crt/x86_64-linux: %w", err)
	}
	return Stub{Symbol: "_start", Object: obj}, nil
}