// isel_call.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

var xmmArgRegs = []Reg{RXMM0, RXMM1, RXMM2, RXMM3, RXMM4, RXMM5, RXMM6, RXMM7}

// maskArg applies the appropriate zero-extension or masking for an argument
// passed to a known parameter type, satisfying the SysV/Windows ABI requirement
// that the caller leaves the upper bits of a register clean.
func (s *sel) maskArg(reg Reg, t vir.Type) {
	bits := intBits(t)
	if bits == 32 {
		// A 32-bit move natively zero-extends into the 64-bit parent register
		s.emit(Inst{Op: "mov", D: R(reg), S: R(reg), Sz: 4})
	} else if bits < 32 {
		s.maskTo(reg, bits)
	}
}

// selCall lowers direct, imported (already rewritten), and indirect calls.
// Arguments go into IntArgRegs / the outgoing stack area per LayoutArgs;
// the result comes back in rax.
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
	
	// 1. Process stack arguments first (so they don't clobber arg registers)
	for i, a := range args {
		sl := plan.Slots[i]
		if sl.InReg {
			continue
		}

		outOff := sl.StackOff
		if s.os == "windows" {
			outOff += windowsShadowSpace
		}
		
		if sl.Class == classMemory {
			// byval struct argument (MEMORY-class copy)
			s.loadOperand(a, RRSI)
			s.emit(Inst{Op: "lea", D: R(RRDI), S: Mem(RRSP, int32(outOff))})
			s.emit(Inst{Op: "mov", D: R(RRCX), S: Imm(sl.Bytes), Sz: 8})
			s.emit(Inst{Op: "cld"})
			s.emit(Inst{Op: "rep_movsb"})
			continue
		}
		
		if vir.IsFloat(s.operandType(a)) {
			s.loadFloatOperand(a, RRAX, s.operandType(a))
		} else {
			s.loadOperand(a, RRAX)
			if i < len(params) {
				s.maskArg(RRAX, params[i].Type)
			}
		}
		// Guarantee the value actually reaches the stack memory allocation
		s.emit(Inst{Op: "mov", D: Mem(RRSP, int32(outOff)), S: R(RRAX), Sz: 8})
	}

	// 2. Process register arguments
	xmmSysVCount := 0
	for i, a := range args {
		sl := plan.Slots[i]
		if !sl.InReg {
			continue
		}

		isFloat := vir.IsFloat(s.operandType(a))

		if s.os == "windows" {
			// Windows x64 ABI uses strictly positional mapping
			if isFloat {
				// The Windows ABI requires floats to be mirrored in integer registers for unprototyped calls
				s.loadFloatOperand(a, sl.Reg, s.operandType(a))
				if i < len(xmmArgRegs) {
					s.emit(Inst{Op: "movq_to_xmm", D: R(xmmArgRegs[i]), S: R(sl.Reg), Sz: 8})
				}
			} else {
				s.loadOperand(a, sl.Reg)
				if i < len(params) {
					s.maskArg(sl.Reg, params[i].Type)
				}
			}
		} else {
			// System V ABI packs floats sequentially into XMM registers
			if isFloat {
				s.loadFloatOperand(a, sl.Reg, s.operandType(a))
				if xmmSysVCount < len(xmmArgRegs) {
					s.emit(Inst{Op: "movq_to_xmm", D: R(xmmArgRegs[xmmSysVCount]), S: R(sl.Reg), Sz: 8})
					xmmSysVCount++
				}
			} else {
				s.loadOperand(a, sl.Reg)
				if i < len(params) {
					s.maskArg(sl.Reg, params[i].Type)
				}
			}
		}
	}

	if variadic && s.os != "windows" {
		s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(int64(xmmSysVCount)), Sz: 8})
	}

	// 3. Dispatch the call
	if callee.Kind == vir.OperandIdent && callee.Qualifier == "" && s.isDirect(callee.Ident) {
		s.emit(Inst{Op: "call_sym", Sym: callee.Ident})
	} else {
		s.loadOperand(callee, RR11) // indirect target in a scratch reg
		s.emit(Inst{Op: "call_r", S: R(RR11)})
	}

	// 4. Cleanup and return
	if reserve > 0 {
		s.emit(Inst{Op: "add", D: R(RRSP), S: Imm(reserve), Sz: 8})
	}
	if in.Result != "" {
		if vir.IsFloat(s.types[in.Result]) {
			s.emit(Inst{Op: "movq_from_xmm", D: R(RRAX), S: R(RXMM0), Sz: 8})
		}
		s.storeReg(RRAX, in.Result)
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
			if vir.IsFloat(s.operandType(*x.Value)) {
				s.loadFloatOperand(*x.Value, IntRetReg, s.operandType(*x.Value))
				s.emit(Inst{Op: "movq_to_xmm", D: R(RXMM0), S: R(IntRetReg), Sz: 8})
			} else {
				s.loadOperand(*x.Value, IntRetReg)
				if s.f.Ret != nil {
					s.maskArg(IntRetReg, s.f.Ret)
				}
			}
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
		variadic := false
		if p, v, ok := s.ix.calleeParams(x.Callee); ok {
			params = p
			variadic = v
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
		
		xmmSysVCount := 0
		for i, a := range x.Args {
			if sl := plan.Slots[i]; sl.InReg {
				isFloat := vir.IsFloat(s.operandType(a))
				if s.os == "windows" {
					if isFloat {
						s.loadFloatOperand(a, sl.Reg, s.operandType(a))
						if i < len(xmmArgRegs) {
							s.emit(Inst{Op: "movq_to_xmm", D: R(xmmArgRegs[i]), S: R(sl.Reg), Sz: 8})
						}
					} else {
						s.loadOperand(a, sl.Reg)
						if i < len(params) {
							s.maskArg(sl.Reg, params[i].Type)
						}
					}
				} else {
					if isFloat {
						s.loadFloatOperand(a, sl.Reg, s.operandType(a))
						if xmmSysVCount < len(xmmArgRegs) {
							s.emit(Inst{Op: "movq_to_xmm", D: R(xmmArgRegs[xmmSysVCount]), S: R(sl.Reg), Sz: 8})
							xmmSysVCount++
						}
					} else {
						s.loadOperand(a, sl.Reg)
						if i < len(params) {
							s.maskArg(sl.Reg, params[i].Type)
						}
					}
				}
			} else {
				return todo("tailcall with stack arguments")
			}
		}
		
		if variadic && s.os != "windows" {
			s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(int64(xmmSysVCount)), Sz: 8})
		}
		
		s.emit(Inst{Op: "epi_jmp_sym", Sym: x.Callee})
		return nil
	}
	
	// Indirect tailcall via fnsig; first arg is the function pointer.
	s.loadOperand(x.Args[0], RR11)
	regs := intArgRegs(s.os)
	xmmSysVCount := 0
	for i, a := range x.Args[1:] {
		if i < len(regs) {
			isFloat := vir.IsFloat(s.operandType(a))
			if s.os == "windows" {
				if isFloat {
					s.loadFloatOperand(a, regs[i], s.operandType(a))
					if i < len(xmmArgRegs) {
						s.emit(Inst{Op: "movq_to_xmm", D: R(xmmArgRegs[i]), S: R(regs[i]), Sz: 8})
					}
				} else {
					s.loadOperand(a, regs[i])
				}
			} else {
				if isFloat {
					s.loadFloatOperand(a, regs[i], s.operandType(a))
					if xmmSysVCount < len(xmmArgRegs) {
						s.emit(Inst{Op: "movq_to_xmm", D: R(xmmArgRegs[xmmSysVCount]), S: R(regs[i]), Sz: 8})
						xmmSysVCount++
					}
				} else {
					s.loadOperand(a, regs[i])
				}
			}
		} else {
			return todo("indirect tailcall with stack arguments")
		}
	}
	s.emit(Inst{Op: "epi_jmp_r", S: R(RR11)})
	return nil
}