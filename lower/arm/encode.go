// encode.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/arm/encoder"
)

// assemble finishes a function: it prepends the prologue, expands the
// epilogue pseudo-ops, resolves every OSlot to [fp, #off], and hands the
// stream to the encoder.
func (c *fnLower) assemble() ([]byte, []Fixup, error) {
	body := c.out
	c.out = nil
	c.prologue()
	stream := append(c.out, body...)

	var final []encoder.Inst
	for _, in := range stream {
		expanded, err := c.expand(in)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range expanded {
			if err := c.resolveSlots(&e); err != nil {
				return nil, nil, err
			}
			ei, err := toEncoderInst(e)
			if err != nil {
				return nil, nil, err
			}
			final = append(final, ei)
		}
	}

	code, efx, err := encoder.Encode(final)
	if err != nil {
		return nil, nil, err
	}
	fixups := make([]Fixup, 0, len(efx))
	for _, f := range efx {
		cf, err := fromEncoderFixup(f)
		if err != nil {
			return nil, nil, err
		}
		fixups = append(fixups, cf)
	}
	return code, fixups, nil
}

// prologue emits, in order:
//
//	push {r0-r3}        (variadic only — see frame.go for why it is first)
//	push {fp, lr}
//	mov  fp, sp
//	sub  sp, sp, #Local
//	<spill incoming parameters into their home slots>
//
// Nothing clobbers r0-r3 before the spill, so a register parameter is
// stored straight from the register it arrived in.
func (c *fnLower) prologue() {
	if c.frame.Variadic {
		c.emit(Inst{Op: "push", M: RegList(R0, R1, R2, R3)})
	}
	c.emit(Inst{Op: "push", M: RegList(FP, LR)})
	c.mov(FP, R(SP))
	if c.frame.Local > 0 {
		// ip, not an argument register: the parameters are still live.
		c.addImm(SP, SP, -int32(c.frame.Local), IP)
	}

	for i, p := range c.f.Params {
		if p.Name == "" {
			continue
		}
		s := c.frame.Params[i]
		switch {
		case s.ByVal:
			// A byval parameter's value is the address of the caller's
			// copy, but AAPCS may have split that copy across registers
			// and the stack, in which case the callee has to reassemble
			// it before it can take an address at all.
			c.emit(Inst{Op: "ud"}) // unreachable: rejected below
		case len(s.Regs) == 1:
			c.emit(Inst{Op: "str", D: R(s.Regs[0]), M: Slot(p.Name)})
		case s.Stack == ArgWordBytes:
			off := c.frame.ArgBase + int32(s.Off)
			c.emit(Inst{Op: "ldr", D: R(IP), M: Mem(FP, off)})
			c.emit(Inst{Op: "str", D: R(IP), M: Slot(p.Name)})
		}
	}
}

// expand turns the epilogue pseudo-ops into real instructions.
//
// sp is restored with mov sp, fp rather than by undoing sub sp, #Local
// arithmetically. The two are equivalent only if sp has not moved since
// the prologue, which a dynamically sized alloca — a runtime sub sp that
// Frame.Local knows nothing about — violates.
func (c *fnLower) expand(in Inst) ([]Inst, error) {
	switch in.Op {
	case "epi_ret", "epi_jmp_sym", "epi_jmp_r":
	default:
		return []Inst{in}, nil
	}
	out := []Inst{
		{Op: "mov", D: R(SP), M: R(FP)},
		{Op: "pop", M: RegList(FP, LR)},
	}
	if c.frame.Variadic {
		// Drop the r0-r3 save area the prologue pushed below the incoming
		// arguments. This is why the epilogue cannot use the idiomatic
		// pop {fp, pc}: sp still has to move afterwards.
		out = append(out, Inst{Op: "add", D: R(SP), N: R(SP), M: Imm(VarargSaveBytes)})
	}
	switch in.Op {
	case "epi_ret":
		out = append(out, Inst{Op: "bx", M: R(LR)})
	case "epi_jmp_sym":
		out = append(out, Inst{Op: "b", Sym: in.Sym})
	case "epi_jmp_r":
		out = append(out, Inst{Op: "bx", M: in.M})
	}
	return out, nil
}

func (c *fnLower) resolveSlots(in *Inst) error {
	for _, o := range []*Opr{&in.D, &in.N, &in.M, &in.A} {
		if o.Kind != OSlot {
			continue
		}
		off, err := c.frame.Offset(o.Slot)
		if err != nil {
			return err
		}
		*o = Mem(FP, off)
	}
	return nil
}

var _ = vir.Void