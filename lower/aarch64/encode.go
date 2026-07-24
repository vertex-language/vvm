// encode.go
package aarch64

import (
	"fmt"

	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// assemble expands the prologue and the epilogue pseudo-ops, resolves every
// OSlot to an [x29, #off] memory operand, and hands a fully-physical Inst
// stream to isa/aarch64/encoder.
func assemble(s *sel) ([]byte, []Fixup, error) {
	var out []Inst
	out = append(out, prologue(s.fr)...)
	for _, in := range s.out {
		switch in.Op {
		case "epi_ret":
			out = append(out, epilogue(s.fr)...)
			out = append(out, Inst{Op: "ret"})
		case "epi_b_sym":
			out = append(out, epilogue(s.fr)...)
			out = append(out, Inst{Op: "b", Sym: in.Sym})
		case "epi_br_reg":
			out = append(out, epilogue(s.fr)...)
			out = append(out, Inst{Op: "br", N: in.N})
		default:
			out = append(out, in)
		}
	}

	einsts := make([]encoder.Inst, 0, len(out))
	for i := range out {
		e, err := toEncoderInst(s.fr, &out[i])
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", out[i].Op, err)
		}
		einsts = append(einsts, e)
	}

	code, efx, err := encoder.Encode(einsts)
	if err != nil {
		return nil, nil, err
	}
	fx := make([]Fixup, 0, len(efx))
	for _, f := range efx {
		k, err := fromEncoderKind(f.Kind)
		if err != nil {
			return nil, nil, err
		}
		fx = append(fx, Fixup{Offset: f.Offset, Symbol: f.Symbol, Kind: k, Addend: f.Addend})
	}
	return code, fx, nil
}

// prologue builds the frame.
//
// The variadic register save area is pushed *first*, above the frame record,
// so it lands directly below the incoming stack arguments and the two form
// one contiguous, uniformly-indexed argument run — the arrangement that
// makes isel_va.go's one-word cursor possible. x0-x7 are pushed in
// descending pairs so x0 ends up at the lowest address.
func prologue(fr *Frame) []Inst {
	var out []Inst

	if fr.SaveArea != 0 {
		for i := NumIntArgRegs - 2; i >= 0; i -= 2 {
			out = append(out, Inst{
				Op: "stp",
				D:  R(IntArgRegs[i]),
				A:  R(IntArgRegs[i+1]),
				M:  MemPre(encoder.SPr, -16),
			})
		}
	}

	out = append(out, subSP(int64(fr.FrameBytes))...)
	out = append(out,
		Inst{Op: "stp", D: R(encoder.FP), A: R(encoder.LR), M: Mem(encoder.SPr, 0)},
		Inst{Op: "mov", D: R(encoder.FP), M: Rsp()},
	)

	// Spill incoming register parameters into their home slots, so the rest
	// of selection reads every value uniformly out of a slot and never has
	// to care whether a parameter arrived in x2 or on the stack.
	for i, slot := range fr.Params.Slots {
		switch slot.Class {
		case ClassReg, ClassIndirect:
			name := fr.paramName(i)
			if name == "" {
				continue
			}
			out = append(out, Inst{Op: "str", D: R(slot.Reg), M: Slot(name)})
		case ClassFPReg:
			name := fr.paramName(i)
			if name == "" {
				continue
			}
			out = append(out, Inst{Op: "fstr", W: encoder.X, D: R(slot.Reg), M: Slot(name)})
		}
	}
	return out
}

// epilogue tears the frame down. sp is restored from x29 rather than by
// undoing `sub sp, #FrameBytes` arithmetically: the two are equivalent only
// if sp has not moved since the prologue, which a dynamically-sized alloca —
// a runtime `sub sp, n` that Frame.Local knows nothing about — violates.
func epilogue(fr *Frame) []Inst {
	out := []Inst{
		{Op: "mov", D: Rsp(), M: R(encoder.FP)},
		{Op: "ldp", D: R(encoder.FP), A: R(encoder.LR), M: Mem(encoder.SPr, 0)},
	}
	return append(out, addSP(int64(fr.FrameBytes+fr.SaveArea))...)
}

// subSP / addSP move the stack pointer by a possibly-large constant. The
// shifted-register add/sub forms cannot name sp at all, so an amount too
// large for the 12-bit immediate goes through the *extended* form with x16,
// which takes Xn|SP in both Rd and Rn.
func subSP(n int64) []Inst { return moveSP("sub", n) }
func addSP(n int64) []Inst { return moveSP("add", n) }

func moveSP(op string, n int64) []Inst {
	if n == 0 {
		return nil
	}
	if n <= 4095 {
		return []Inst{{Op: op, D: Rsp(), N: Rsp(), M: Imm(n)}}
	}
	var out []Inst
	// x16 is dead across the prologue and, in the epilogue, is restored-free
	// scratch: x29/x30 are already back and no argument register is touched,
	// which is what keeps a register-only tailcall legal after this runs.
	out = append(out, Inst{Op: "movz", D: R(RegAddr), Imm: n & 0xFFFF, Imm2: 0})
	if hi := (n >> 16) & 0xFFFF; hi != 0 {
		out = append(out, Inst{Op: "movk", D: R(RegAddr), Imm: hi, Imm2: 16})
	}
	return append(out, Inst{Op: op, D: Rsp(), N: Rsp(), M: RExt(RegAddr, encoder.UXTX, 0)})
}

// paramName is the declared name of parameter i, if it has a slot.
func (fr *Frame) paramName(i int) string {
	if i < len(fr.paramNames) {
		return fr.paramNames[i]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Translation to the encoder's shapes.
// ---------------------------------------------------------------------------

// toEncoderOpr converts explicitly rather than by numeric cast, even though
// the two OprKind enums are declared in the same order — OSlot is the one
// variant with no encoder equivalent, and an explicit switch fails loudly if
// either enum gains a case instead of silently reinterpreting it.
func toEncoderOpr(fr *Frame, o Opr) (encoder.Opr, error) {
	switch o.Kind {
	case ONone:
		return encoder.Opr{}, nil
	case OReg:
		return encoder.Opr{
			Kind: encoder.OReg, Reg: o.Reg, SP: o.SP,
			Shift: o.Shift, ShiftAmt: o.ShiftAmt,
		}, nil
	case OExt:
		return encoder.Opr{
			Kind: encoder.OExt, Reg: o.Reg, SP: o.SP,
			Ext: o.Ext, ExtAmt: o.ExtAmt,
		}, nil
	case OImm:
		return encoder.Opr{Kind: encoder.OImm, Imm: o.Imm, Sym: o.Sym}, nil
	case OMem:
		return encoder.Opr{
			Kind: encoder.OMem, Mode: o.Mode, Base: o.Base, Index: o.Index,
			Disp: o.Disp, Sym: o.Sym, Ext: o.Ext, Scaled: o.Scaled,
		}, nil
	case OSlot:
		off, err := fr.Offset(o.Slot)
		if err != nil {
			return encoder.Opr{}, err
		}
		return encoder.Opr{
			Kind: encoder.OMem, Mode: encoder.ModeOffset,
			Base: encoder.FP, Index: encoder.RNone, Disp: off,
		}, nil
	}
	return encoder.Opr{}, fmt.Errorf("unknown operand kind %d", o.Kind)
}

func toEncoderInst(fr *Frame, in *Inst) (encoder.Inst, error) {
	var out encoder.Inst
	out.Op, out.W, out.CC = in.Op, in.W, in.CC
	out.Lbl, out.Sym, out.Imm, out.Imm2 = in.Lbl, in.Sym, in.Imm, in.Imm2

	var err error
	if out.D, err = toEncoderOpr(fr, in.D); err != nil {
		return out, err
	}
	if out.N, err = toEncoderOpr(fr, in.N); err != nil {
		return out, err
	}
	if out.M, err = toEncoderOpr(fr, in.M); err != nil {
		return out, err
	}
	if out.A, err = toEncoderOpr(fr, in.A); err != nil {
		return out, err
	}
	return out, nil
}