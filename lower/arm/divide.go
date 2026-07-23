// divide.go
package arm

import (
	"github.com/vertex-language/vvm/ir/vir"
)

// Division is the one place this backend has to synthesize a whole
// algorithm rather than select an instruction.
//
// A32 has no divide in the base instruction set — SDIV/UDIV arrived with
// the ARMv7 integer-divide extension and are absent from
// isa/arm/encoder's switch entirely — and the obvious alternative, a call
// to __aeabi_idiv, would put a runtime support library underneath an IR
// whose first design principle is that there isn't one (§1). So the
// quotient is computed inline with a restoring shift-subtract loop.
//
// The §5.3 traps are emitted as `ud`, the permanently-undefined encoding:
// a trap must halt deterministically and must not be removable by codegen,
// which is exactly what an undefined instruction gives on this machine.
func (c *fnLower) selDivide(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSDiv || in.Op == vir.OpSRem
	wantRem := in.Op == vir.OpURem || in.Op == vir.OpSRem

	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	if signed {
		c.sext32(R0, w)
		c.sext32(R1, w)
	}

	// Trap on a zero divisor.
	ok := c.newLabel()
	c.cmp(R1, Imm(0))
	c.br(NE, ok)
	c.emit(Inst{Op: "ud"})
	c.mark(ok)

	if signed {
		// Trap on INT_MIN / -1, tested at the *operand's* width: the
		// 32-bit quotient of -128 / -1 is perfectly representable, which
		// is exactly why the check cannot be left to the arithmetic.
		ok2 := c.newLabel()
		c.emit(Inst{Op: "cmn", N: R(R1), M: Imm(1)}) // r1 == -1 ?
		c.br(NE, ok2)
		c.movImm(IP, -(int64(1) << (w - 1)))
		c.cmp(R0, R(IP))
		c.br(NE, ok2)
		c.emit(Inst{Op: "ud"})
		c.mark(ok2)

		// Both signs are needed after the unsigned core has consumed the
		// operands: the quotient's is num^den, the remainder's is num's.
		c.emit(Inst{Op: "push", M: RegList(R0, R1)})
		c.cmp(R0, Imm(0))
		c.emit(Inst{Op: "rsb", CC: LT, D: R(R0), N: R(R0), M: Imm(0)})
		c.cmp(R1, Imm(0))
		c.emit(Inst{Op: "rsb", CC: LT, D: R(R1), N: R(R1), M: Imm(0)})
	}

	c.emitUDivCore()

	if signed {
		c.emit(Inst{Op: "pop", M: RegList(R3, IP)}) // r3 = num, ip = den
		if wantRem {
			c.cmp(R3, Imm(0))
			c.emit(Inst{Op: "rsb", CC: LT, D: R(R0), N: R(R0), M: Imm(0)})
		} else {
			c.dp("eor", R(IP), R(R3), R(IP))
			c.cmp(IP, Imm(0))
			c.emit(Inst{Op: "rsb", CC: LT, D: R(R2), N: R(R2), M: Imm(0)})
		}
	}

	res := R2 // quotient
	if wantRem {
		res = R0
	}
	c.maskTo(res, w)
	c.storeSlot(res, in.Result)
	return nil
}

// emitUDivCore divides r0 by r1, leaving the quotient in r2 and the
// remainder in r0, clobbering r1 and r3. The divisor is shifted left until
// it is no smaller than the dividend, then the classic restoring loop
// walks it back down; the running bit in r3 doubles as the loop counter,
// so termination costs no extra register.
//
// The caller has already ruled out a zero divisor.
func (c *fnLower) emitUDivCore() {
	shift := c.newLabel()
	shiftDone := c.newLabel()
	loop := c.newLabel()

	c.mov(R2, Imm(0))
	c.mov(R3, Imm(1))

	c.mark(shift)
	c.cmp(R1, R(R0))
	c.br(HS, shiftDone)
	c.emit(Inst{Op: "tst", N: R(R1), M: Imm(0x80000000)}) // next shift would drop a bit
	c.br(NE, shiftDone)
	c.mov(R1, RShift(R1, LSL, 1))
	c.mov(R3, RShift(R3, LSL, 1))
	c.br(AL, shift)
	c.mark(shiftDone)

	c.mark(loop)
	c.cmp(R0, R(R1))
	c.emit(Inst{Op: "sub", CC: HS, D: R(R0), N: R(R0), M: R(R1)})
	c.emit(Inst{Op: "orr", CC: HS, D: R(R2), N: R(R2), M: R(R3)})
	c.mov(R1, RShift(R1, LSR, 1))
	c.emit(Inst{Op: "mov", S: true, D: R(R3), M: RShift(R3, LSR, 1)})
	c.br(NE, loop)
}

var _ = vir.OpUDiv