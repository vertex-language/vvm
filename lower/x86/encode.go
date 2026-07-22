// encode.go
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
			out = append(out, epilogue()...)
			out = append(out, Inst{Op: "ret"})
		case "epi_jmp_sym":
			out = append(out, epilogue()...)
			out = append(out, Inst{Op: "jmp_sym", Sym: in.Sym})
		case "epi_jmp_r":
			out = append(out, epilogue()...)
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
		insts = append(insts, Inst{Op: "sub", D: R(isax86.RESP), S: Imm(int64(fr.Local)), Sz: 4})
	}
	return insts
}

// epilogue restores esp from ebp rather than arithmetically undoing the
// prologue's `sub esp, Local`.
//
// The two are only equivalent when esp hasn't moved in between, and a
// dynamically-sized alloca.ptr lowers to a runtime `sub esp, n` that
// Frame.Local knows nothing about. `add esp, Local` would then leave esp
// low by n, and the four pops would restore garbage into edi/esi/ebx/ebp
// before returning to a bogus address. `lea esp, [ebp-SavedRegBytes]`
// is correct whatever esp has been doing, encodes in the same three
// bytes, and doesn't need Frame.Local at all — which is why this function
// no longer takes the Frame.
//
// It leaves esp at ebp+4 after the pops, so the outgoing arguments a
// tailcall wrote into the incoming argument area at [ebp+8+…] are exactly
// where the callee will look for them at [esp+4+…].
func epilogue() []Inst {
	return []Inst{
		{Op: "lea", D: R(isax86.RESP), S: Mem(isax86.REBP, -SavedRegBytes)},
		{Op: "pop", D: R(isax86.REDI)},
		{Op: "pop", D: R(isax86.RESI)},
		{Op: "pop", D: R(isax86.REBX)},
		{Op: "pop", D: R(isax86.REBP)},
	}
}

// toEncoderOpr converts this package's operand to the encoder's.
//
// The two OprKind enums are declared in the same order and a numeric cast
// would work today — which is exactly why this is an explicit switch
// instead. The one difference between the two types (OSlot) is the whole
// reason both exist, so the conversion between them should fail loudly
// when a variant is added on either side rather than silently reinterpret
// it as whatever shares its ordinal.
func toEncoderOpr(o Opr) (encoder.Opr, error) {
	var k encoder.OprKind
	switch o.Kind {
	case ONone:
		k = encoder.ONone
	case OReg:
		k = encoder.OReg
	case OImm:
		k = encoder.OImm
	case OMem:
		k = encoder.OMem
	case OSlot:
		return encoder.Opr{}, fmt.Errorf("encode: unresolved slot %q reached final assembly", o.Slot)
	default:
		return encoder.Opr{}, fmt.Errorf("encode: operand kind %d has no encoder equivalent", o.Kind)
	}
	return encoder.Opr{
		Kind:  k,
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
	return encoder.Inst{
		Op: in.Op, D: d, S: s, CC: in.CC, Sz: in.Sz,
		Lbl: in.Lbl, Sym: in.Sym, Imm: in.Imm,
	}, nil
}