// syscall.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// Per-OS syscall register conventions for the `syscall` op.
//
// NOTE: the long-mode syscall instruction (0F 05) is not yet in the
// encoder's op switch. This file emits an Inst{Op: "syscall"}; the encoder
// needs a one-line `case "syscall": e.u8(0x0F, 0x05)` to consume it. Until
// then selSyscall's output won't encode — this is a localized dependency,
// not a design gap.
//
// linux: number in rax; args in rdi, rsi, rdx, r10, r8, r9; `syscall`.
//        (r10 replaces rcx, which `syscall` clobbers.) Result in rax.
var linuxSyscallArgRegs = []Reg{RRDI, RRSI, RRDX, RR10, RR8, RR9}

// freebsd: number in rax; args in the same registers as a normal SysV call
//          (rdi, rsi, rdx, rcx, r8, r9); `syscall`. Result in rax, carry
//          flag signals error. We model only the register-arg path.
var freebsdSyscallArgRegs = []Reg{RRDI, RRSI, RRDX, RRCX, RR8, RR9}

func syscallArgRegs(os string) ([]Reg, error) {
	switch os {
	case "linux":
		return linuxSyscallArgRegs, nil
	case "freebsd":
		return freebsdSyscallArgRegs, nil
	}
	return nil, todo("syscall convention for os %q", os)
}

// selSyscall lowers `syscall.<type> num, args...` (max six scalar args).
func (s *sel) selSyscall(in *vir.Instruction) error {
	regs, err := syscallArgRegs(s.os)
	if err != nil {
		return err
	}
	if len(in.Args) < 1 {
		return errBadModule("syscall needs at least a number operand")
	}
	if len(in.Args)-1 > len(regs) {
		return errBadModule("syscall has more than six arguments")
	}
	// Number into rax, then each arg into its register — args first so a
	// value living in rax isn't clobbered before it's read.
	for i := len(in.Args) - 1; i >= 1; i-- {
		s.loadOperand(in.Args[i], regs[i-1])
	}
	s.loadOperand(in.Args[0], RRAX)
	s.emit(Inst{Op: "syscall"})
	if in.Result != "" {
		s.storeReg(RRAX, in.Result)
	}
	return nil
}