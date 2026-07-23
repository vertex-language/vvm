// syscall.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// syscallConv is one OS's register convention for the syscall op.
type syscallConv struct {
	Num  Reg   // register carrying the system call number
	Args []Reg // argument registers, in order
	Op   string
}

// Both supported systems use the EABI convention: the number goes in r7
// rather than in an argument register (r7 is not part of the argument
// sequence, so a syscall's arguments sit exactly where a call's would),
// and the trap is svc #0 rather than the legacy OABI's immediate-encoded
// swi.
func syscallConvFor(os string) (syscallConv, error) {
	switch os {
	case "linux":
		return syscallConv{Num: R7, Args: []Reg{R0, R1, R2, R3, R4, R5}, Op: "svc"}, nil
	case "freebsd":
		// Same shape; FreeBSD additionally signals failure in the carry
		// flag, which the syscall op has no way to name and this backend
		// therefore does not model.
		return syscallConv{Num: R7, Args: []Reg{R0, R1, R2, R3, R4, R5}, Op: "svc"}, nil
	}
	return syscallConv{}, todo("syscalls on os %q", os)
}

const (
	R4 = 4
	R5 = 5
	R7 = 7
)

func (c *fnLower) selSyscall(in *vir.Instruction) error {
	conv, err := syscallConvFor(c.x.m.Target.OS)
	if err != nil {
		return err
	}
	if len(in.Args) == 0 {
		return fmt.Errorf("syscall has no system call number")
	}
	args := in.Args[1:]
	if len(args) > len(conv.Args) {
		return fmt.Errorf("syscall takes at most %d arguments (§4.2)", len(conv.Args))
	}
	for _, a := range args {
		if a.Kind == vir.OperandIdent {
			if t, ok := c.types[a.Ident]; ok && !vir.IsScalarType(t) {
				return fmt.Errorf("syscall operand %s is not a scalar (§9.33)", a.Ident)
			}
		}
	}

	// r7 and r4/r5 are callee-saved under AAPCS, so the sequence brackets
	// itself with a push/pop rather than assuming a caller tolerates the
	// clobber. The pair keeps sp 8-byte aligned across the trap.
	c.emit(Inst{Op: "push", M: RegList(R4, R5, R7, IP)})
	for i, a := range args {
		if err := c.into(a, conv.Args[i]); err != nil {
			return err
		}
	}
	if err := c.into(in.Args[0], conv.Num); err != nil {
		return err
	}
	c.emit(Inst{Op: conv.Op, Imm: 0})
	c.emit(Inst{Op: "pop", M: RegList(R4, R5, R7, IP)})

	if in.Result != "" {
		if w, ok := intWidth(c.types[in.Result]); ok {
			c.maskTo(R0, w)
		}
		c.storeSlot(R0, in.Result)
	}
	return nil
}