package x86

import (
	"fmt"

	isax86 "github.com/vertex-language/vvm/isa/x86"
	"github.com/vertex-language/vvm/isa/x86/encoder"
)

func assemble(body []Inst, fr *Frame) ([]byte, []encoder.Fixup, error) {
	out := prologue(fr)
	for _, in := range body {
		switch in.Op {
		case "epi_ret":
			out = append(out, epilogue(fr)...)
			out = append(out, Inst{Op: "ret"})
		case "epi_jmp_sym":
			out = append(out, epilogue(fr)...)
			out = append(out, Inst{Op: "jmp_sym", Sym: in.Sym})
		case "epi_jmp_r":
			out = append(out, epilogue(fr)...)
			out = append(out, Inst{Op: "jmp_r", S: in.S})
		default:
			out = append(out, in)
		}
	}

	einsts := make([]encoder.Inst, len(out))
	for i := range out {
		if err := resolveSlot(&out[i].D, fr); err != nil {
			return nil, nil, err
		}
		if err := resolveSlot(&out[i].S, fr); err != nil {
			return nil, nil, err
		}
		ei, err := toEncoderInst(out[i])
		if err != nil {
			return nil, nil, err
		}
		einsts[i] = ei
	}
	return encoder.Encode(einsts)
}

func resolveSlot(o *Opr, fr *Frame) error {
	if o.Kind != OSlot {
		return nil
	}
	off, ok := fr.Offset(o.Slot)
	if !ok {
		return fmt.Errorf("encode: value %q has no frame slot", o.Slot)
	}
	*o = Mem(isax86.REBP, off)
	return nil
}

func prologue(fr *Frame) []Inst {
	insts := []Inst{
		{Op: "push", S: R(isax86.REBP)},
		{Op: "mov", D: R(isax86.REBP), S: R(isax86.RESP), Sz: 4},
		{Op: "push", S: R(isax86.REBX)},
		{Op: "push", S: R(isax86.RESI)},
		{Op: "push", S: R(isax86.REDI)},
	}
	if fr.Local > 0 {
		insts = append(insts, Inst{Op: "sub", D: R(isax86.RESP), S: Imm(int64(fr.Local))})
	}
	return insts
}

func epilogue(fr *Frame) []Inst {
	var insts []Inst
	if fr.Local > 0 {
		insts = append(insts, Inst{Op: "add", D: R(isax86.RESP), S: Imm(int64(fr.Local))})
	}
	return append(insts,
		Inst{Op: "pop", D: R(isax86.REDI)},
		Inst{Op: "pop", D: R(isax86.RESI)},
		Inst{Op: "pop", D: R(isax86.REBX)},
		Inst{Op: "pop", D: R(isax86.REBP)},
	)
}

func toEncoderOpr(o Opr) (encoder.Opr, error) {
	if o.Kind == OSlot {
		return encoder.Opr{}, fmt.Errorf("encode: unresolved slot %q reached final assembly", o.Slot)
	}
	return encoder.Opr{
		Kind:  encoder.OprKind(o.Kind),
		Reg:   o.Reg,
		Imm:   o.Imm,
		Sym:   o.Sym,
		Base:  o.Base,
		Index: o.Index,
		Scale: o.Scale,
		Disp:  o.Disp,
		MSym:  o.MSym,
	}, nil
}

func toEncoderInst(in Inst) (encoder.Inst, error) {
	d, err := toEncoderOpr(in.D)
	if err != nil {
		return encoder.Inst{}, err
	}
	s, err := toEncoderOpr(in.S)
	if err != nil {
		return encoder.Inst{}, err
	}
	return encoder.Inst{Op: in.Op, D: d, S: s, CC: in.CC, Sz: in.Sz, Lbl: in.Lbl, Sym: in.Sym, Imm: in.Imm}, nil
}