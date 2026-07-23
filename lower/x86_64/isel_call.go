// isel_call.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// selCall lowers direct, imported (already rewritten), and indirect calls.
// Arguments go into IntArgRegs / the outgoing stack area per LayoutArgs;
// the result comes back in rax. Variadic calls set al = 0 (no XMM args —
// float varargs are a todo).
func (s *sel) selCall(in *vir.Instruction) error {
	callee := in.Args[0]
	args := in.Args[1:]

	var params []vir.Param
	variadic := false
	if callee.Kind == vir.OperandIdent && callee.Qualifier == "" {
		if p, v, ok := s.ix.calleeParams(callee.Ident); ok {
			params, variadic = p, v
		}
	}

	plan, reserve, err := s.l.PlanCall(params, len(args))
	if err != nil {
		return err
	}
	if reserve > 0 {
		s.emit(Inst{Op: "sub", D: R(RRSP), S: Imm(reserve), Sz: 8})
	}
	// Stack args first (they don't clobber arg registers), then register
	// args, so a value read from a slot into rdi isn't overwritten early.
	for i, a := range args {
		sl := plan.Slots[i]
		if sl.Class == classMemory {
			// byval struct argument (MEMORY-class copy)
			s.loadOperand(a, RRSI)
			s.emit(Inst{Op: "lea", D: R(RRDI), S: Mem(RRSP, int32(sl.StackOff))})
			s.emit(Inst{Op: "mov", D: R(RRCX), S: Imm(sl.Bytes), Sz: 8})
			s.emit(Inst{Op: "cld"})
			s.emit(Inst{Op: "rep_movsb"})
			continue
		}
		if !sl.InReg {
			s.loadOperand(a, RRAX)
			s.emit(Inst{Op: "mov", D: Mem(RRSP, int32(sl.StackOff)), S: R(RRAX), Sz: 8})
		}
	}
	for i, a := range args {
		if sl := plan.Slots[i]; sl.InReg {
			s.loadOperand(a, sl.Reg)
		}
	}
	if variadic {
		s.emit(Inst{Op: "xor", D: R(RRAX), S: R(RRAX), Sz: 4}) // al = 0 XMM regs
	}

	if callee.Kind == vir.OperandIdent && callee.Qualifier == "" && s.isDirect(callee.Ident) {
		s.emit(Inst{Op: "call_sym", Sym: callee.Ident})
	} else {
		s.loadOperand(callee, RR11) // indirect target in a scratch reg
		s.emit(Inst{Op: "call_r", S: R(RR11)})
	}

	if reserve > 0 {
		s.emit(Inst{Op: "add", D: R(RRSP), S: Imm(reserve), Sz: 8})
	}
	if in.Result != "" {
		s.storeReg(IntRetReg, in.Result)
	}
	return nil
}

func (s *sel) isDirect(name string) bool {
	if _, ok := s.ix.funcs[name]; ok {
		return true
	}
	_, ok := s.ix.externs[name]
	return ok
}

// selTerm lowers a block terminator.
func (s *sel) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		s.emit(Inst{Op: "jmp", Lbl: blockLabel(s.f.Name, x.Label)})
	case vir.BranchIf:
		s.loadOperand(x.Cond, RRAX)
		s.emit(Inst{Op: "test", D: R(RRAX), S: R(RRAX), Sz: 4})
		s.emit(Inst{Op: "jcc", CC: CondNE, Lbl: blockLabel(s.f.Name, x.Then)})
		s.emit(Inst{Op: "jmp", Lbl: blockLabel(s.f.Name, x.Else)})
	case vir.Switch:
		s.loadOperand(x.Value, RRAX)
		for _, c := range x.Cases {
			s.emit(Inst{Op: "cmp", D: R(RRAX), S: Imm(c.Value), Sz: 8})
			s.emit(Inst{Op: "jcc", CC: CondE, Lbl: blockLabel(s.f.Name, c.Label)})
		}
		s.emit(Inst{Op: "jmp", Lbl: blockLabel(s.f.Name, x.Default)})
	case vir.Return:
		if x.Value != nil {
			s.loadOperand(*x.Value, IntRetReg)
		}
		s.emit(Inst{Op: "epi_ret"})
	case vir.TailCall:
		return s.selTailCall(x)
	case vir.Trap:
		s.emit(Inst{Op: "ud2"})
	case vir.Unreachable:
		s.emit(Inst{Op: "ud2"})
	default:
		return errBadModule("unknown terminator %T", t)
	}
	return nil
}

// selTailCall stages outgoing args below the current frame, block-copies
// them into the incoming argument area, then jumps to the target after the
// epilogue. byval is rejected on a tailcall path (the copy would need a
// frame that's about to be torn down).
func (s *sel) selTailCall(x vir.TailCall) error {
	if x.Callee != "" {
		var params []vir.Param
		if p, _, ok := s.ix.calleeParams(x.Callee); ok {
			params = p
		}
		plan, _, err := s.l.PlanCall(params, len(x.Args))
		if err != nil {
			return err
		}
		for _, sl := range plan.Slots {
			if sl.Class == classMemory {
				return errBadModule("byval argument on a tailcall path")
			}
		}
		// Load register args (indirect stack-arg restaging omitted here;
		// register-only tailcalls are the common case).
		for i, a := range x.Args {
			if sl := plan.Slots[i]; sl.InReg {
				s.loadOperand(a, sl.Reg)
			} else {
				return todo("tailcall with stack arguments")
			}
		}
		s.emit(Inst{Op: "epi_jmp_sym", Sym: x.Callee})
		return nil
	}
	// Indirect tailcall via fnsig; first arg is the function pointer.
	s.loadOperand(x.Args[0], RR11)
	for i, a := range x.Args[1:] {
		if i < len(IntArgRegs) {
			s.loadOperand(a, IntArgRegs[i])
		} else {
			return todo("indirect tailcall with stack arguments")
		}
	}
	s.emit(Inst{Op: "epi_jmp_r", S: R(RR11)})
	return nil
}