// isel_call.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

func (c *fnLower) terminator(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		c.br(AL, x.Label)
		return nil

	case vir.BranchIf:
		if err := c.into(x.Cond, R0); err != nil {
			return err
		}
		c.cmp(R0, Imm(0))
		c.br(NE, x.Then)
		c.br(AL, x.Else)
		return nil

	case vir.Switch:
		// A compare chain. A jump table would need a PC-relative load and
		// a literal pool, which is a lowering choice worth making only
		// with a density heuristic this backend does not have yet.
		if err := c.into(x.Value, R0); err != nil {
			return err
		}
		for _, cs := range x.Cases {
			if isSmallImm(cs.Value) {
				c.cmp(R0, Imm(cs.Value))
			} else {
				c.movImm(IP, cs.Value)
				c.cmp(R0, R(IP))
			}
			c.br(EQ, cs.Label)
		}
		c.br(AL, x.Default)
		return nil

	case vir.Return:
		if x.Value != nil {
			if err := c.into(*x.Value, IntRetReg); err != nil {
				return err
			}
		}
		c.emit(Inst{Op: "epi_ret"})
		return nil

	case vir.TailCall:
		return c.selTailCall(x)

	case vir.Trap:
		c.emit(Inst{Op: "ud"})
		return nil

	case vir.Unreachable:
		// Executing this is UB (§5.4 #6); an undefined instruction turns
		// that into an immediate fault rather than a fallthrough into
		// whatever was laid out next.
		c.emit(Inst{Op: "ud"})
		return nil
	}
	return fmt.Errorf("unknown terminator %T", t)
}

func isSmallImm(v int64) bool { return v >= 0 && v <= 0xFF }

// argTypes derives the fixed types of a call's actual arguments, for the
// unnamed variadic tail (whose placement the callee's declaration cannot
// describe).
func (c *fnLower) argTypes(args []vir.Operand) ([]vir.Type, error) {
	out := make([]vir.Type, len(args))
	for i, a := range args {
		if a.Kind == vir.OperandIdent {
			if t, ok := c.types[a.Ident]; ok {
				out[i] = t
				continue
			}
		}
		// A literal in the variadic tail: every unnamed argument occupies
		// one word, so a word-sized stand-in places it correctly.
		out[i] = vir.I32
	}
	return out, nil
}

func (c *fnLower) selCall(in *vir.Instruction) error {
	params, variadic, err := c.x.calleeParams(in)
	if err != nil {
		return err
	}
	indirect := in.Sig != ""
	args := in.Args
	if !indirect {
		args = in.Args[1:] // Args[0] is the callee identifier
	} else {
		args = in.Args[1:] // Args[0] is the function pointer
	}
	if len(args) < len(params) {
		return fmt.Errorf("call passes %d arguments to a %d-parameter callee", len(args), len(params))
	}
	var tail []vir.Type
	if variadic && len(args) > len(params) {
		ts, err := c.argTypes(args[len(params):])
		if err != nil {
			return err
		}
		tail = ts
	} else if len(args) > len(params) {
		return fmt.Errorf("call passes %d arguments to a non-variadic %d-parameter callee",
			len(args), len(params))
	}

	list, err := callArgs(c.x.layout, params, tail)
	if err != nil {
		return err
	}
	slots, stack, err := PlanCall(c.x.layout, list)
	if err != nil {
		return err
	}
	if stack > 0 {
		c.addImm(SP, SP, -int32(stack), IP)
	}
	if err := c.writeArgs(args, slots); err != nil {
		return err
	}

	if indirect {
		// The target goes into ip last: it is not an argument register,
		// so nothing written above can clobber it, and nothing below
		// needs it.
		if err := c.into(in.Args[0], IP); err != nil {
			return err
		}
		c.emit(Inst{Op: "blx", M: R(IP)})
	} else {
		sym, ok := c.x.symbol(in.Args[0].Ident)
		if !ok {
			return fmt.Errorf("no symbol for callee %q", in.Args[0].Ident)
		}
		c.emit(Inst{Op: "bl", Sym: sym})
	}
	if stack > 0 {
		c.addImm(SP, SP, int32(stack), IP)
	}

	if in.Result != "" {
		t := c.types[in.Result]
		if w, ok := intWidth(t); ok {
			c.maskTo(IntRetReg, w)
		}
		c.storeSlot(IntRetReg, in.Result)
	}
	return nil
}

// writeArgs places a call's arguments. Stack arguments go first because
// staging them uses r0 as scratch; register arguments are loaded straight
// from their slots afterwards, so no argument register is ever read after
// being overwritten.
func (c *fnLower) writeArgs(args []vir.Operand, slots []ArgSlot) error {
	for i, s := range slots {
		if s.ByVal {
			return todo("byval arguments on a call path")
		}
		if s.Split() {
			return todo("an argument split between the core registers and the stack")
		}
		if s.Stack == 0 {
			continue
		}
		if s.Stack != ArgWordBytes {
			return todo("multi-word stack arguments")
		}
		if err := c.into(args[i], R0); err != nil {
			return err
		}
		c.emit(Inst{Op: "str", D: R(R0), M: Mem(SP, int32(s.Off))})
	}
	for i, s := range slots {
		if len(s.Regs) == 0 {
			continue
		}
		if len(s.Regs) != 1 {
			return todo("multi-register arguments")
		}
		if err := c.into(args[i], s.Regs[0]); err != nil {
			return err
		}
	}
	return nil
}

// selTailCall reuses the caller's frame. Only the register-only case is
// implemented: restaging stack arguments means writing into an incoming
// argument area that may still hold a parameter this call is reading, and
// the staging buffer would have to live in a frame that is about to be
// torn down.
func (c *fnLower) selTailCall(t vir.TailCall) error {
	in := &vir.Instruction{Op: vir.OpCall, Sig: t.Sig}
	if t.Callee != "" {
		in.Args = append([]vir.Operand{vir.Ident(t.Callee)}, t.Args...)
	} else {
		in.Args = t.Args // Args[0] is the function pointer
	}
	params, variadic, err := c.x.calleeParams(in)
	if err != nil {
		return err
	}
	args := in.Args[1:]
	var tail []vir.Type
	if variadic && len(args) > len(params) {
		if tail, err = c.argTypes(args[len(params):]); err != nil {
			return err
		}
	}
	list, err := callArgs(c.x.layout, params, tail)
	if err != nil {
		return err
	}
	slots, stack, err := PlanCall(c.x.layout, list)
	if err != nil {
		return err
	}
	if stack > 0 {
		return todo("stack-argument restaging on a tailcall")
	}
	for _, s := range slots {
		if s.ByVal {
			return todo("byval on a tailcall path")
		}
	}
	if err := c.writeArgs(args, slots); err != nil {
		return err
	}

	// r0-r3 hold the arguments and ip holds an indirect target; the
	// epilogue writes only sp, fp and lr, so all of them survive it.
	if t.Callee != "" {
		sym, ok := c.x.symbol(t.Callee)
		if !ok {
			return fmt.Errorf("no symbol for tailcall target %q", t.Callee)
		}
		c.emit(Inst{Op: "epi_jmp_sym", Sym: sym})
		return nil
	}
	if err := c.into(in.Args[0], IP); err != nil {
		return err
	}
	c.emit(Inst{Op: "epi_jmp_r", M: R(IP)})
	return nil
}