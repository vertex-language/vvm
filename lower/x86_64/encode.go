// encode.go
package x86_64

import (
	"fmt"

	enc "github.com/vertex-language/vvm/isa/x86_64/encoder"
)

// finish assembles the function: expand epilogue pseudo-ops, prepend the
// prologue, resolve every OSlot to an [rbp+off] operand, and hand the stream
// to the generic encoder.
func (s *sel) finish() ([]byte, []enc.Fixup, error) {
	var stream []Inst
	stream = append(stream, s.prologue()...)
	for _, in := range s.out {
		switch in.Op {
		case "epi_ret":
			stream = append(stream, s.epilogue()...)
			stream = append(stream, Inst{Op: "ret"})
		case "epi_jmp_sym":
			stream = append(stream, s.epilogue()...)
			stream = append(stream, Inst{Op: "jmp_sym", Sym: in.Sym})
		case "epi_jmp_r":
			stream = append(stream, s.epilogue()...)
			stream = append(stream, Inst{Op: "jmp_r", S: in.S})
		default:
			stream = append(stream, in)
		}
	}

	encInsts := make([]enc.Inst, 0, len(stream))
	for _, in := range stream {
		encInsts = append(encInsts, toEncoderInst(s.fr, in))
	}
	return enc.Encode(encInsts)
}

// prologue: push rbp; mov rbp,rsp; push callee-saved; sub rsp,Local; then
// spill incoming register params into their home slots; then, for variadic
// functions, spill all six GP arg regs into the save area.
func (s *sel) prologue() []Inst {
	var p []Inst
	p = append(p, Inst{Op: "push", S: R(RRBP)})
	p = append(p, Inst{Op: "mov", D: R(RRBP), S: R(RRSP), Sz: 8})
	for _, r := range CalleeSaved {
		p = append(p, Inst{Op: "push", S: R(r)})
	}
	if s.fr.Local > 0 {
		p = append(p, Inst{Op: "sub", D: R(RRSP), S: Imm(s.fr.Local), Sz: 8})
	}
	// Spill register params to their value slots.
	for i, param := range s.f.Params {
		sl := s.fr.incoming.Slots[i]
		if !sl.InReg {
			continue
		}
		if slot, ok := s.fr.SlotOf(param.Name); ok {
			p = append(p, Inst{Op: "mov", D: Slot(slot), S: R(sl.Reg), Sz: 8})
		}
	}
	if s.fr.Variadic {
		off := s.fr.SaveArea
		for i, r := range IntArgRegs {
			p = append(p, Inst{Op: "mov", D: Mem(RRBP, int32(off+int64(i)*8)), S: R(r), Sz: 8})
		}
	}
	return p
}

// epilogue: restore rsp via lea (so a dynamic alloca is unwound too), pop
// callee-saved in reverse, pop rbp.
func (s *sel) epilogue() []Inst {
	var p []Inst
	p = append(p, Inst{Op: "lea", D: R(RRSP), S: Mem(RRBP, int32(-SavedRegBytes))})
	for i := len(CalleeSaved) - 1; i >= 0; i-- {
		p = append(p, Inst{Op: "pop", D: R(CalleeSaved[i])})
	}
	p = append(p, Inst{Op: "pop", D: R(RRBP)})
	return p
}

// toEncoderInst converts one pre-encoding Inst to an encoder.Inst, resolving
// OSlot operands to [rbp+off]. The conversion is explicit (not a numeric
// cast) so a new OprKind on either side fails loudly instead of silently
// reinterpreting.
func toEncoderInst(fr *Frame, in Inst) enc.Inst {
	return enc.Inst{
		Op:  in.Op,
		D:   toEncoderOpr(fr, in.D),
		S:   toEncoderOpr(fr, in.S),
		CC:  in.CC,
		Sz:  in.Sz,
		Lbl: in.Lbl,
		Sym: in.Sym,
		Imm: in.Imm,
	}
}

func toEncoderOpr(fr *Frame, o Opr) enc.Opr {
	switch o.Kind {
	case ONone:
		return enc.Opr{}
	case OReg:
		return enc.R(o.Reg)
	case OImm:
		if o.Sym != "" {
			eo := enc.SymAddr(o.Sym)
			eo.Imm = o.Imm
			return eo
		}
		return enc.Imm(o.Imm)
	case OMem:
		if o.RIPSym != "" {
			return enc.MemRIP(o.RIPSym, o.Disp)
		}
		if o.MSym != "" {
			return enc.MemAbs(o.MSym, o.Disp)
		}
		if o.Index != RNone {
			return enc.MemIndexed(o.Base, o.Index, o.Scale, o.Disp)
		}
		return enc.Mem(o.Base, o.Disp)
	case OSlot:
		return enc.Mem(RRBP, fr.Offset(o.Slot))
	}
	panic(fmt.Sprintf("lower/x86_64: unresolved operand kind %d", o.Kind))
}