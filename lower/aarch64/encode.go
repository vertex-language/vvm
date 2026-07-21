package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// assemble expands isel.go's function-exit markers and OSlot operands
// against fr, splices the ordinary stp_pre/mov_r_sp/sub_sp prologue and
// its mov_to_sp/ldp_post epilogue counterpart in as ordinary instructions
// (isa/aarch64/encoder splices nothing itself — see README.md), and
// converts the result to isa/aarch64/encoder's own Inst/Opr shape for
// Encode.
func assemble(insts []Inst, fr *Frame) ([]byte, []Fixup, error) {
	body, err := toEncoderInsts(insts, fr)
	if err != nil {
		return nil, nil, err
	}
	out := append(prologue(fr.Local), body...)
	return encoder.Encode(out)
}

func prologue(localBytes int32) []encoder.Inst {
	insts := []encoder.Inst{
		{Op: "stp_pre", D: encoder.Mem(encoder.SP, -16), S: encoder.R(encoder.FP), T: encoder.R(encoder.LR)},
		{Op: "mov_r_sp", D: encoder.R(encoder.FP)},
	}
	if localBytes > 0 {
		insts = append(insts, encoder.Inst{Op: "sub_sp", Imm: int64(localBytes)})
	}
	return insts
}

func epilogue() []encoder.Inst {
	return []encoder.Inst{
		{Op: "mov_to_sp", S: encoder.R(encoder.FP)},
		{Op: "ldp_post", D: encoder.Mem(encoder.SP, 16), S: encoder.R(encoder.FP), T: encoder.R(encoder.LR)},
	}
}

// resolveOpr rewrites an OSlot operand into a concrete FP-relative
// encoder.Mem operand and converts every other kind to encoder.Opr as-is.
func resolveOpr(o Opr, fr *Frame) (encoder.Opr, error) {
	switch o.Kind {
	case ONone:
		return encoder.Opr{}, nil
	case OReg:
		return encoder.R(o.Reg), nil
	case OImm:
		return encoder.Imm(o.Imm), nil
	case OMem:
		return encoder.Mem(o.Base, o.Disp), nil
	case OSlot:
		d, ok := fr.Off[o.Slot]
		if !ok {
			return encoder.Opr{}, fmt.Errorf("encode: value %q has no frame slot", o.Slot)
		}
		return encoder.Mem(encoder.FP, d), nil
	}
	return encoder.Opr{}, fmt.Errorf("encode: bad operand kind %d", o.Kind)
}

// toEncoderInsts is the only place that expands epi_ret/epi_jmp_sym/
// epi_jmp_r into the real epilogue followed by the plain exit
// instruction isa/aarch64/encoder actually knows about (ret/b_sym/br_r).
func toEncoderInsts(insts []Inst, fr *Frame) ([]encoder.Inst, error) {
	var out []encoder.Inst
	for _, in := range insts {
		switch in.Op {
		case "epi_ret":
			out = append(out, epilogue()...)
			out = append(out, encoder.Inst{Op: "ret"})
			continue
		case "epi_jmp_sym":
			out = append(out, epilogue()...)
			out = append(out, encoder.Inst{Op: "b_sym", Sym: in.Sym})
			continue
		case "epi_jmp_r":
			out = append(out, epilogue()...)
			s, err := resolveOpr(in.S, fr)
			if err != nil {
				return nil, err
			}
			out = append(out, encoder.Inst{Op: "br_r", S: s})
			continue
		}
		d, err := resolveOpr(in.D, fr)
		if err != nil {
			return nil, err
		}
		s, err := resolveOpr(in.S, fr)
		if err != nil {
			return nil, err
		}
		t, err := resolveOpr(in.T, fr)
		if err != nil {
			return nil, err
		}
		x, err := resolveOpr(in.X, fr)
		if err != nil {
			return nil, err
		}
		out = append(out, encoder.Inst{
			Op: in.Op, D: d, S: s, T: t, X: x,
			Cc: in.Cc, Sz: in.Sz, Lbl: in.Lbl, Sym: in.Sym, Imm: in.Imm,
		})
	}
	return out, nil
}