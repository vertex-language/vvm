// isel_call.go
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// selCall lowers a direct or indirect call.
//
// Ordering matters and is the mirror of lower/x86_64's: the outgoing stack
// area is filled first, because writing it needs only scratch registers,
// and the argument registers are loaded last so nothing overwrites x0-x7
// between their being set and the branch.
func (s *sel) selCall(in *vir.Instruction) error {
	var (
		callee   *callable
		sig      *vir.FunctionSignature
		args     []vir.Operand
		indirect bool
	)
	switch {
	case in.Sig != "":
		var ok bool
		sig, ok = s.ix.sigs[in.Sig]
		if !ok {
			return fmt.Errorf("undeclared fnsig %s", in.Sig)
		}
		indirect = true
		args = in.Args[1:]
	default:
		if len(in.Args) == 0 || in.Args[0].Kind != vir.OperandIdent {
			return fmt.Errorf("call has no callee operand")
		}
		var ok bool
		callee, ok = s.ix.funcs[in.Args[0].Ident]
		if !ok {
			return fmt.Errorf("undeclared callee %s", in.Args[0].Ident)
		}
		args = in.Args[1:]
	}

	descs, err := s.describeArgs(callee, sig, args)
	if err != nil {
		return err
	}
	plan, err := PlanCall(s.ix.layout, descs, s.ix.stackVarargs)
	if err != nil {
		return err
	}

	// An indirect target is parked in x16 (IP0) before the argument
	// registers are touched: it is scratch by convention and never an
	// argument register, so nothing can clobber it in between.
	if indirect {
		if err := s.value(RegAddr, in.Args[0], vir.Ptr); err != nil {
			return err
		}
	}

	if plan.Reserve != 0 {
		s.addImm(encoder.SPr, encoder.SPr, -int64(plan.Reserve), true, true)
	}
	for i, slot := range plan.Slots {
		if slot.Class != ClassStack {
			continue
		}
		if vir.IsFloat(descs[i].Type) {
			if err := s.valueFP(RegFA, args[i], descs[i].Type); err != nil {
				return err
			}
			b, _ := s.bitsOf(descs[i].Type)
			w := encoder.W
			if b == 64 {
				w = encoder.X
			}
			s.emit(Inst{Op: "fstr", W: w, D: R(RegFA), M: Mem(encoder.SPr, int64(slot.Off))})
		} else {
			if err := s.value(RegA, args[i], descs[i].Type); err != nil {
				return err
			}
			s.emit(Inst{Op: "str", D: R(RegA), M: Mem(encoder.SPr, int64(slot.Off))})
		}
	}
	for i, slot := range plan.Slots {
		switch slot.Class {
		case ClassReg, ClassIndirect:
			if err := s.value(slot.Reg, args[i], descs[i].Type); err != nil {
				return err
			}
		case ClassFPReg:
			if err := s.valueFP(slot.Reg, args[i], descs[i].Type); err != nil {
				return err
			}
		}
	}

	if indirect {
		s.emit(Inst{Op: "blr", N: R(RegAddr)})
	} else {
		s.emit(Inst{Op: "bl", Sym: callee.sym})
	}

	if plan.Reserve != 0 {
		s.addImm(encoder.SPr, encoder.SPr, int64(plan.Reserve), true, true)
	}
	if in.Result != "" {
		if vir.IsFloat(in.Suffix) {
			s.storeFP(in.Result, FPRetReg, in.Suffix)
		} else {
			s.store(in.Result, IntRetReg)
		}
	}
	return nil
}

// describeArgs pairs actual operands with the callee's declared parameters,
// marking anything past the declared list as the unnamed variadic tail.
func (s *sel) describeArgs(c *callable, sig *vir.FunctionSignature, args []vir.Operand) ([]ArgDesc, error) {
	out := make([]ArgDesc, len(args))
	for i, a := range args {
		d := ArgDesc{Named: true}
		switch {
		case c != nil && i < len(c.params):
			d.Type, d.ByVal, d.SRet = c.params[i].Type, c.params[i].ByVal, c.params[i].SRet
		case sig != nil && i < len(sig.Params):
			d.Type = sig.Params[i]
		default:
			// The unnamed tail: the operand's own fixed type is all there is
			// to go on, and each such argument takes one flat eightbyte.
			d.Type = s.typeOfOperand(a, vir.I64)
			d.Named = false
		}
		if d.Type == nil {
			d.Type = vir.I64
		}
		out[i] = d
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Terminators.
// ---------------------------------------------------------------------------

func (s *sel) terminator(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		s.emit(Inst{Op: "b", Lbl: x.Label})
		return nil

	case vir.BranchIf:
		if err := s.value(RegA, x.Cond, vir.I1); err != nil {
			return err
		}
		// CBNZ tests the whole register against zero, which is exactly the
		// i1 predicate under the zero-extension invariant — no compare
		// needed, and no flags disturbed.
		s.emit(Inst{Op: "cbnz", W: encoder.W, D: R(RegA), Lbl: x.Then})
		s.emit(Inst{Op: "b", Lbl: x.Else})
		return nil

	case vir.Switch:
		return s.selSwitch(x)

	case vir.Return:
		if x.Value != nil {
			t := s.typeOfOperand(*x.Value, s.fn.Ret)
			if vir.IsFloat(t) {
				if err := s.valueFP(FPRetReg, *x.Value, t); err != nil {
					return err
				}
			} else {
				if err := s.value(IntRetReg, *x.Value, t); err != nil {
					return err
				}
			}
		}
		s.emit(Inst{Op: "epi_ret"})
		return nil

	case vir.TailCall:
		return s.selTailCall(x)

	case vir.Trap:
		s.emit(Inst{Op: "udf"})
		return nil

	case vir.Unreachable:
		// Executing it is UB (§5.4 #6). A deterministic fault is a better
		// realisation of "this cannot happen" than falling into whatever
		// bytes come next.
		s.emit(Inst{Op: "udf"})
		return nil
	}
	return fmt.Errorf("unhandled terminator %T", t)
}

// selSwitch emits a compare chain. A jump table would need a PC-relative
// table load and a data relocation into the middle of a function; that is a
// todo, and a chain is correct meanwhile.
func (s *sel) selSwitch(x vir.Switch) error {
	t := s.typeOfOperand(x.Value, vir.I32)
	b, err := s.bitsOf(t)
	if err != nil {
		return err
	}
	if err := s.value(RegA, x.Value, t); err != nil {
		return err
	}
	w := widthFor(b)
	for _, c := range x.Cases {
		s.cmpImm(RegA, int64(uint64(c.Value)&lowMask(b)), w)
		s.emit(Inst{Op: "b.cond", CC: encoder.EQ, Lbl: c.Label})
	}
	s.emit(Inst{Op: "b", Lbl: x.Default})
	return nil
}

// selTailCall reuses the caller's frame. Arguments are placed *before* the
// epilogue runs, which is safe only because the epilogue touches nothing but
// sp, x29, x30 and — for an oversized frame — x16: x0-x7 survive it intact.
//
// Stack-argument restaging is a todo. Writing outgoing stack arguments into
// the incoming argument area directly can destroy a parameter that has not
// been read yet whenever argument i overlaps parameter j > i, and staging
// them below the frame needs a frame that is about to be torn down.
func (s *sel) selTailCall(x vir.TailCall) error {
	var (
		callee   *callable
		sig      *vir.FunctionSignature
		args     = x.Args
		indirect bool
	)
	if x.Sig != "" {
		var ok bool
		sig, ok = s.ix.sigs[x.Sig]
		if !ok {
			return fmt.Errorf("undeclared fnsig %s", x.Sig)
		}
		indirect = true
		args = x.Args[1:]
	} else {
		var ok bool
		callee, ok = s.ix.funcs[x.Callee]
		if !ok {
			return fmt.Errorf("undeclared tailcall target %s", x.Callee)
		}
	}

	descs, err := s.describeArgs(callee, sig, args)
	if err != nil {
		return err
	}
	for _, d := range descs {
		if d.ByVal != "" {
			return todo("byval on a tailcall path (the copy needs a frame about to be torn down)")
		}
	}
	plan, err := PlanCall(s.ix.layout, descs, s.ix.stackVarargs)
	if err != nil {
		return err
	}
	if plan.StackBytes != 0 {
		return todo("tailcall with %d bytes of stack arguments needs restaging", plan.StackBytes)
	}

	if indirect {
		if err := s.value(RegAux, x.Args[0], vir.Ptr); err != nil {
			return err
		}
	}
	for i, slot := range plan.Slots {
		switch slot.Class {
		case ClassReg, ClassIndirect:
			if err := s.value(slot.Reg, args[i], descs[i].Type); err != nil {
				return err
			}
		case ClassFPReg:
			if err := s.valueFP(slot.Reg, args[i], descs[i].Type); err != nil {
				return err
			}
		}
	}
	if indirect {
		s.emit(Inst{Op: "epi_br_reg", N: R(RegAux)})
		return nil
	}
	s.emit(Inst{Op: "epi_b_sym", Sym: callee.sym})
	return nil
}