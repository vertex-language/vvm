// syscall.go describes, per target OS, how Vertex IR's
// `syscall.<type> sysno, args...` lowers to a 32-bit x86 trap: which
// physical registers (if any) carry the syscall number and each argument,
// how the trap itself is invoked, and where the kernel leaves its result.
package x86

import isax86 "github.com/vertex-language/vvm/isa/x86"

// SyscallConvention is one OS's 32-bit syscall trap ABI.
type SyscallConvention struct {
	// RegisterFor reports the physical register that should hold operand i
	// (i == 0 is sysno, i == 1..6 are the syscall's arguments), or
	// ok == false if that operand must instead be pushed on the stack.
	RegisterFor func(i int) (r isax86.Reg, ok bool)

	// StackArgsPushRetAddrPlaceholder is true when the kernel's int 0x80
	// entry point expects the same stack shape as a `call` — a bogus
	// return-address slot beneath the stacked arguments — as on FreeBSD.
	StackArgsPushRetAddrPlaceholder bool

	// Trap is the instruction that executes the trap itself.
	Trap Inst

	// Result is the physical register the trap leaves its return value in.
	Result isax86.Reg
}

// linuxSyscallRegs is the classic i386 Linux kernel entry convention
// (arch/x86/entry): the seven slots (sysno plus up to six arguments) are
// all register-passed: eax, ebx, ecx, edx, esi, edi, ebp.
var linuxSyscallRegs = [...]isax86.Reg{
	isax86.REAX, isax86.REBX, isax86.RECX, isax86.REDX, isax86.RESI, isax86.REDI, isax86.REBP,
}

var syscallConventions = map[string]SyscallConvention{
	"linux": {
		RegisterFor: func(i int) (isax86.Reg, bool) {
			if i < 0 || i >= len(linuxSyscallRegs) {
				return isax86.RNone, false
			}
			return linuxSyscallRegs[i], true
		},
		Trap:   Inst{Op: "int", Imm: 0x80},
		Result: isax86.REAX,
	},
	// FreeBSD i386 syscalls also trap via `int 0x80`, but pass every
	// argument on the stack, cdecl-style, with only sysno in a register
	// (eax). The int 0x80 entry point reads arguments as though it were a
	// `call`, so the caller must push a dummy return-address slot beneath
	// the arguments.
	"freebsd": {
		RegisterFor: func(i int) (isax86.Reg, bool) {
			if i == 0 {
				return isax86.REAX, true
			}
			return isax86.RNone, false
		},
		StackArgsPushRetAddrPlaceholder: true,
		Trap:                            Inst{Op: "int", Imm: 0x80},
		Result:                          isax86.REAX,
	},
}

// syscallConventionFor returns the syscall convention for a target OS, or
// ok == false if this backend has no wired convention for it (§4:
// "Unsupported natively on os = none/uefi without an explicitly enabled
// feature-tier flag").
func syscallConventionFor(os string) (SyscallConvention, bool) {
	c, ok := syscallConventions[os]
	return c, ok
}