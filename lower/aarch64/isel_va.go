// isel_va.go
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// ValistBytes is this backend's valist: one word, a pointer to the next
// variadic argument.
//
// §3 makes valist's layout target-defined precisely so a backend can choose
// the cheapest thing that works, and one word works here for the same reason
// it works in lower/arm: the variadic prologue pushes x0-x7 *before* the
// frame record, so the register save area lands directly below the incoming
// stack arguments, and argument eightbyte i sits at ArgBase + 8i whether it
// arrived in a register or on the stack. va_start becomes one add, va_arg a
// post-indexed load, and va_end a no-op with no cursor state to reconcile.
//
// This is deliberately *not* the AAPCS64 C va_list, which is a 32-byte
// struct with separate GP and SIMD cursors. Under the stack-varargs
// convention (§7.1's `aapcs64` token, and every Mach-O target) it happens to
// coincide with the platform's own one-word va_list; under the base standard
// it does not, so a valist built here cannot be handed to a C vprintf. That
// is a documented non-conformance, not an oversight — see the README.
const ValistBytes = 8

// selVaStart seeds the cursor at the first unnamed argument.
func (s *sel) selVaStart(in *vir.Instruction) error {
	if !s.fn.Variadic {
		return fmt.Errorf("va_start outside a variadic function")
	}
	if len(in.Args) != 2 {
		return fmt.Errorf("va_start needs dst and last_named")
	}
	dst := in.Args[0]
	if dst.Kind != vir.OperandIdent {
		return fmt.Errorf("va_start's destination must be a valist ident")
	}
	// ParamEnd is computed from the actual argument layout, never from
	// ArgBase + 8*(i+1), which is only right when no preceding parameter is
	// an sret or already spilled to the stack.
	s.addImm(RegA, encoder.FP, int64(s.fr.ParamEnd), false, false)
	s.store(dst.Ident, RegA)
	return nil
}

// selVaArg reads the next argument and advances the cursor by one eightbyte.
// Every variadic argument occupies exactly one, whatever its declared width.
func (s *sel) selVaArg(in *vir.Instruction) error {
	if len(in.Args) != 1 || in.Args[0].Kind != vir.OperandIdent {
		return fmt.Errorf("va_arg needs a valist ident")
	}
	if vir.IsFloat(vir.ElemOrSelf(in.Suffix)) || vir.IsVec(in.Suffix) {
		return todo("va_arg.%s needs the SIMD&FP save area", in.Suffix)
	}
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	src := in.Args[0].Ident
	s.emit(Inst{Op: "ldr", D: R(RegAddr), M: Slot(src)})
	switch {
	case b <= 8:
		s.emit(Inst{Op: "ldrb", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	case b <= 16:
		s.emit(Inst{Op: "ldrh", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	case b <= 32:
		s.emit(Inst{Op: "ldr", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	default:
		s.emit(Inst{Op: "ldr", D: R(RegA), M: Mem(RegAddr, 0)})
	}
	if b == 1 {
		s.maskTo(RegA, 1)
	}
	s.emit(Inst{Op: "add", D: R(RegAddr), N: R(RegAddr), M: Imm(ArgWordBytes)})
	s.emit(Inst{Op: "str", D: R(RegAddr), M: Slot(src)})
	s.store(in.Result, RegA)
	return nil
}

// selVaEnd is a no-op: a bare cursor holds no state needing cleanup. The
// opcode is still required at the IR level (§4.4) and still verified there;
// emitting nothing is the correct lowering, not an elision.
func (s *sel) selVaEnd(in *vir.Instruction) error {
	if len(in.Args) != 1 {
		return fmt.Errorf("va_end needs one operand")
	}
	return nil
}