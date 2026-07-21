// encode.go is the last stage of the pipeline: it takes a selected Inst
// stream (labels, OSlot operands, epilogue pseudo-ops and all) plus the
// Frame instruction selection was built against, and produces real IA-32
// bytes. It wraps the body in a prologue/epilogue, resolves every OSlot to
// its EBP-relative home, expands "epi_ret"/"epi_jmp_sym"/"epi_jmp_r" into
// their real instruction sequences, and hands the fully concrete stream to
// isa/x86/encoder — the generic, vir-agnostic assembler — for byte
// emission. This is what used to be package mcode's own (unseen) Encode
// plus package regalloc's ResolveSlots; there's no reason for either to be
// a separate package from the Inst type they operate on.
package x86

import (
	"fmt"

	isax86 "github.com/vertex-language/vvm/isa/x86"
	"github.com/vertex-language/vvm/isa/x86/encoder"
)

// assemble turns one function's selected instruction stream into machine
// bytes and its relocations.
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

// resolveSlot rewrites an OSlot operand into the concrete EBP-relative
// memory operand its Frame assigned it. Every other operand kind is
// already what isa/x86/encoder expects and passes through untouched.
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

// toEncoderOpr and toEncoderInst are the one necessary translation this
// package performs: isa/x86/encoder.Opr has no OSlot variant at all (it's
// strictly post-resolution), so once resolveSlot has run, converting is a
// plain field-for-field copy. Reaching this with an unresolved OSlot still
// present is a bug in this package, not a user error — reported, not
// panicked, so a caller can decide how to surface it.
func toEncoderOpr(o Opr) (encoder.Opr, error) {
	if o.Kind == OSlot {
		return encoder.Opr{}, fmt.Errorf("encode: unresolved slot %q reached final assembly", o.Slot)
	}
	return encoder.Opr{
		Kind:  encoder.OprKind(o.Kind), // ONone/OReg/OImm/OMem share position with encoder.OprKind
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