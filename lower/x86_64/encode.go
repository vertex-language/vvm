package x86_64

import (
	"fmt"

	"github.com/vertex-language/vvm/isa/x86_64/encoder"
)

// assemble finishes a function's Inst stream and hands it to
// isa/x86_64/encoder. encoder.Encode splices no prologue/epilogue of its
// own — it doesn't know what a function is. So frame setup/teardown is
// this package's job: assemble resolves every KSlot against fr, prepends
// the prologue, and expands isel's three exit markers (epi_ret /
// epi_jmp_sym / epi_jmp_r) into the epilogue followed by the plain
// ret/jmp_sym/jmp_r the encoder knows about. Everything else passes
// through 1:1.
func assemble(insts []Inst, fr *Frame) ([]byte, []encoder.Fixup, error) {
	if err := resolveSlots(insts, fr); err != nil {
		return nil, nil, err
	}
	out := prologue(fr.Local)
	for i := range insts {
		enc, err := toEncoderInsts(&insts[i])
		if err != nil {
			return nil, nil, err
		}
		out = append(out, enc...)
	}
	return encoder.Encode(out)
}

func prologue(localBytes int32) []encoder.Inst {
	insts := []encoder.Inst{
		{Op: "push", S: encoder.R(isax86_64RBP)},
		{Op: "mov", D: encoder.R(isax86_64RBP), S: encoder.R(isax86_64RSP), Sz: 8},
	}
	if localBytes > 0 {
		insts = append(insts, encoder.Inst{Op: "sub", D: encoder.R(isax86_64RSP), S: encoder.Imm(int64(localBytes)), Sz: 8})
	}
	return insts
}

func epilogue() []encoder.Inst {
	return []encoder.Inst{
		{Op: "mov", D: encoder.R(isax86_64RSP), S: encoder.R(isax86_64RBP), Sz: 8},
		{Op: "pop", D: encoder.R(isax86_64RBP)},
	}
}

func resolveSlots(insts []Inst, fr *Frame) error {
	fix := func(o *Opr) error {
		if o.K != KSlot {
			return nil
		}
		d, ok := fr.Off[o.Slot]
		if !ok {
			return fmt.Errorf("encode: value %q has no frame slot", o.Slot)
		}
		*o = Opr{K: KMem, Base: isax86_64RBP, Disp: d}
		return nil
	}
	for i := range insts {
		if err := fix(&insts[i].D); err != nil {
			return err
		}
		if err := fix(&insts[i].S); err != nil {
			return err
		}
	}
	return nil
}

func toEncoderOpr(o Opr) (encoder.Opr, error) {
	switch o.K {
	case KNone:
		return encoder.Opr{}, nil
	case KReg:
		return encoder.R(o.Reg), nil
	case KImm:
		return encoder.Imm(o.Imm), nil
	case KSym:
		return encoder.SymAddr(o.Sym), nil
	case KMem:
		return encoder.Mem(o.Base, o.Disp), nil
	case KRIP:
		return encoder.Opr{K: encoder.KRIP, Sym: o.Sym, Disp: o.Disp}, nil
	case KSlot:
		return encoder.Opr{}, fmt.Errorf("encode: unresolved slot %q reached the encoder (regalloc bug)", o.Slot)
	}
	return encoder.Opr{}, fmt.Errorf("encode: unknown operand kind %d", o.K)
}

func toEncoderInsts(in *Inst) ([]encoder.Inst, error) {
	switch in.Op {
	case "epi_ret":
		return append(epilogue(), encoder.Inst{Op: "ret"}), nil
	case "epi_jmp_sym":
		return append(epilogue(), encoder.Inst{Op: "jmp_sym", Sym: in.Sym}), nil
	case "epi_jmp_r":
		s, err := toEncoderOpr(in.S)
		if err != nil {
			return nil, err
		}
		return append(epilogue(), encoder.Inst{Op: "jmp_r", S: s}), nil
	}
	d, err := toEncoderOpr(in.D)
	if err != nil {
		return nil, err
	}
	s, err := toEncoderOpr(in.S)
	if err != nil {
		return nil, err
	}
	return []encoder.Inst{{Op: in.Op, D: d, S: s, CC: in.CC, Sz: in.Sz, Lbl: in.Lbl, Sym: in.Sym, Imm: in.Imm}}, nil
}