package x86_64

import isax86_64 "github.com/vertex-language/vvm/isa/x86_64"

// SyscallConvention is the per-target-OS syscall trap convention for
// x86_64: which register carries the syscall number, which registers
// carry the (up to six) arguments, which register carries the result,
// and which Inst op is the trap instruction. This is a lowering-policy
// table (which OS uses which registers), not an ISA fact, so it lives
// here rather than in isa/x86_64 — but every register in it is named via
// isax86_64.RAX etc. directly.
type SyscallConvention struct {
	NR     isax86_64.Reg
	Args   []isax86_64.Reg
	Result isax86_64.Reg
	Trap   string
}

var syscallConventions = map[string]SyscallConvention{}

func registerSyscallConvention(os string, c SyscallConvention) { syscallConventions[os] = c }

func LookupSyscall(os string) (SyscallConvention, bool) {
	c, ok := syscallConventions[os]
	return c, ok
}

func init() {
	// Linux and FreeBSD x86_64 share the same `syscall`-instruction
	// register assignment: NR in RAX, args in RDI/RSI/RDX/R10/R8/R9 (R10,
	// not RCX, since `syscall` itself clobbers RCX/R11), result in RAX.
	sysv := SyscallConvention{
		NR:     isax86_64.RAX,
		Args:   []isax86_64.Reg{isax86_64.RDI, isax86_64.RSI, isax86_64.RDX, isax86_64.R10, isax86_64.R8, isax86_64.R9},
		Result: isax86_64.RAX,
		Trap:   "syscall",
	}
	registerSyscallConvention("linux", sysv)
	registerSyscallConvention("freebsd", sysv)
	// Windows stays unregistered: no stable documented user-mode syscall
	// convention on x86_64 Windows; LookupSyscall("windows") reports
	// ok == false and the caller surfaces that as a lowering error.
}