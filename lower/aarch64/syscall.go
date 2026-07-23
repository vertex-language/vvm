// syscall.go
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// syscallConv is one OS's register convention for the syscall op.
type syscallConv struct {
	num  encoder.Reg
	args []encoder.Reg
	ret  encoder.Reg
}

// AArch64 has a dedicated SVC instruction and, unlike A32, no legacy
// alternative to weigh against it. Both supported OSes take the syscall
// number in x8 — not in an argument register — which is what leaves all eight
// of x0-x7 available for arguments.
var syscallConvs = map[string]syscallConv{
	"linux": {
		num:  encoder.R8,
		args: []encoder.Reg{encoder.R0, encoder.R1, encoder.R2, encoder.R3, encoder.R4, encoder.R5},
		ret:  encoder.R0,
	},
	"freebsd": {
		num: encoder.R8,
		args: []encoder.Reg{encoder.R0, encoder.R1, encoder.R2, encoder.R3,
			encoder.R4, encoder.R5, encoder.R6, encoder.R7},
		ret: encoder.R0,
	},
}

// selSyscall traps via SVC #0. On freebsd the carry flag signals an error;
// only the value in x0 is modelled, exactly as the x86_64 backend does.
func (s *sel) selSyscall(in *vir.Instruction) error {
	conv, ok := syscallConvs[s.ix.os]
	if !ok {
		return todo("syscall convention for os %q", s.ix.os)
	}
	if len(in.Args) == 0 {
		return fmt.Errorf("syscall has no number operand")
	}
	args := in.Args[1:]
	if len(args) > len(conv.args) {
		return fmt.Errorf("syscall takes at most %d arguments on %s, got %d", len(conv.args), s.ix.os, len(args))
	}
	for _, a := range args {
		t := s.typeOfOperand(a, vir.I64)
		if !vir.IsScalarType(t) {
			return fmt.Errorf("syscall operand of type %s is not a scalar (§9.33)", t)
		}
	}

	// Arguments before the number: x8 is not an argument register in either
	// convention, so the order is free, but keeping the number last matches
	// the other backends and reads as "set up, then trap".
	for i, a := range args {
		if err := s.value(conv.args[i], a, s.typeOfOperand(a, vir.I64)); err != nil {
			return err
		}
	}
	if err := s.value(conv.num, in.Args[0], vir.I64); err != nil {
		return err
	}
	s.emit(Inst{Op: "svc", Imm: 0})
	if in.Result != "" {
		s.store(in.Result, conv.ret)
		if in.Suffix != nil {
			if b, err := s.bitsOf(in.Suffix); err == nil && b < 64 {
				s.emit(Inst{Op: "ldr", D: R(RegA), M: Slot(in.Result)})
				s.maskTo(RegA, b)
				s.store(in.Result, RegA)
			}
		}
	}
	return nil
}