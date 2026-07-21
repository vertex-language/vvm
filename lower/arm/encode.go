package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/isa/arm/encoder"
)

// assemble resolves every OSlot operand in insts against fr, splices this
// backend's AAPCS frame setup/teardown around isel's raw instruction
// stream as ordinary push/mov/sub/pop/branch encoder.Insts, and hands the
// result to encoder.Encode. encoder.R0/RFP/RSP/RLR/RPC and CondEQ.. are
// used directly — see opr.go's header comment.
func assemble(insts []Inst, fr *Frame, big bool) ([]byte, []encoder.Fixup, error) {
	out := make([]encoder.Inst, 0, len(insts)+8)

	out = append(out,
		encoder.Inst{Op: "push", RegList: regBit(encoder.RFP) | regBit(encoder.RLR)},
		encoder.Inst{Op: "mov_r", D: encoder.R(encoder.RFP), S: encoder.R(encoder.RSP)},
	)
	if fr.Local > 0 {
		out = append(out, encoder.Inst{Op: "sub", D: encoder.R(encoder.RSP), S: encoder.Imm(int64(fr.Local))})
	}

	for i := range insts {
		in := &insts[i]
		switch in.Op {
		case "epi_ret":
			out = append(out,
				encoder.Inst{Op: "mov_r", D: encoder.R(encoder.RSP), S: encoder.R(encoder.RFP)},
				encoder.Inst{Op: "pop", RegList: regBit(encoder.RFP) | regBit(encoder.RPC)},
			)
		case "epi_jmp_sym":
			out = append(out,
				encoder.Inst{Op: "mov_r", D: encoder.R(encoder.RSP), S: encoder.R(encoder.RFP)},
				encoder.Inst{Op: "pop", RegList: regBit(encoder.RFP) | regBit(encoder.RLR)},
				encoder.Inst{Op: "b_sym", Sym: in.Sym},
			)
		case "epi_jmp_r":
			s, err := resolveOpr(in.S, fr)
			if err != nil {
				return nil, nil, err
			}
			out = append(out,
				encoder.Inst{Op: "mov_r", D: encoder.R(encoder.RSP), S: encoder.R(encoder.RFP)},
				encoder.Inst{Op: "pop", RegList: regBit(encoder.RFP) | regBit(encoder.RLR)},
				encoder.Inst{Op: "bx_r", S: s},
			)
		default:
			ei, err := toEncoderInst(in, fr)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, ei)
		}
	}

	return encoder.Encode(out, big)
}

func regBit(r encoder.Reg) uint16 { return 1 << uint(r) }

func toEncoderInst(in *Inst, fr *Frame) (encoder.Inst, error) {
	d, err := resolveOpr(in.D, fr)
	if err != nil {
		return encoder.Inst{}, err
	}
	s, err := resolveOpr(in.S, fr)
	if err != nil {
		return encoder.Inst{}, err
	}
	t, err := resolveOpr(in.T, fr)
	if err != nil {
		return encoder.Inst{}, err
	}
	x, err := resolveOpr(in.X, fr)
	if err != nil {
		return encoder.Inst{}, err
	}
	return encoder.Inst{
		Op: in.Op, D: d, S: s, T: t, X: x,
		CC: in.CC, RegList: in.RegList, Lbl: in.Lbl, Sym: in.Sym, Imm: in.Imm,
	}, nil
}

// resolveOpr rewrites an OSlot into a concrete FP-relative memory operand;
// every other kind converts straight across to encoder.Opr.
func resolveOpr(o Opr, fr *Frame) (encoder.Opr, error) {
	switch o.Kind {
	case OSlot:
		disp, ok := fr.Off[o.Slot]
		if !ok {
			return encoder.Opr{}, fmt.Errorf("arm: value %q has no frame slot", o.Slot)
		}
		return encoder.Mem(encoder.RFP, disp), nil
	case OReg:
		return encoder.R(o.Reg), nil
	case OImm:
		if o.Sym != "" {
			return encoder.SymAddr(o.Sym), nil // addend folded in by isel via in.Imm at the Inst level, not here
		}
		return encoder.Imm(o.Imm), nil
	case OMem:
		if o.Index != encoder.RNone {
			return encoder.MemIndexed(o.Base, o.Index), nil
		}
		return encoder.Mem(o.Base, o.Disp), nil
	}
	return encoder.Opr{}, nil
}