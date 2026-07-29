package ptx

import (
	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Instruction selection. Every §11 opcode either maps onto one PTX instruction
// or onto a short sequence that pins a behaviour PTX leaves open — division by
// zero, ctlz of zero, masked shift counts, half-away-from-zero rounding. Those
// sequences are the whole reason this file is longer than a table.

func (f *fn) instr(in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpLoc:
		return f.loc(in)

	// --- Integer / shared arithmetic (§11.1, §11.3) ------------------------
	case gvir.OpAdd, gvir.OpSub, gvir.OpMul, gvir.OpNeg, gvir.OpAbs:
		if isFloatElem(in.Suffix) {
			return f.floatArith(in)
		}
		return f.intArith(in)
	case gvir.OpUDiv, gvir.OpSDiv, gvir.OpURem, gvir.OpSRem:
		return f.divRem(in)
	case gvir.OpUMulH, gvir.OpSMulH:
		return f.mulHigh(in)
	case gvir.OpUMin, gvir.OpUMax, gvir.OpSMin, gvir.OpSMax:
		return f.intMinMax(in)

	// --- Bitwise and shifts (§11.2) ----------------------------------------
	case gvir.OpAnd, gvir.OpOr, gvir.OpXor, gvir.OpNot:
		return f.bitwise(in)
	case gvir.OpShl, gvir.OpLShr, gvir.OpAShr:
		return f.shift(in)
	case gvir.OpRotl, gvir.OpRotr:
		return f.rotate(in)
	case gvir.OpCtlz, gvir.OpCttz:
		return f.countZeros(in)
	case gvir.OpPopcnt:
		return f.popcount(in)
	case gvir.OpBrev:
		return f.bitReverse(in)
	case gvir.OpBSwap:
		return f.byteSwap(in)

	// --- Float (§11.3) ------------------------------------------------------
	case gvir.OpDiv, gvir.OpSqrt, gvir.OpFma, gvir.OpMin, gvir.OpMax, gvir.OpCopysign:
		return f.floatArith(in)
	case gvir.OpFloor, gvir.OpCeil, gvir.OpRoundEven, gvir.OpTruncF:
		return f.floatRound(in)
	case gvir.OpRound:
		return f.roundHalfAway(in)
	case gvir.OpIsNaN, gvir.OpIsInf:
		return f.floatClassify(in)

	// --- Approximate opcodes (§11.6) ----------------------------------------
	case gvir.OpRcp, gvir.OpRsqrt, gvir.OpSin, gvir.OpCos, gvir.OpExp2, gvir.OpLog2, gvir.OpTanh:
		return f.approx(in)

	// --- Comparisons (§11.4) -------------------------------------------------
	case gvir.OpEq, gvir.OpNe, gvir.OpUlt, gvir.OpUle, gvir.OpUgt, gvir.OpUge,
		gvir.OpSlt, gvir.OpSle, gvir.OpSgt, gvir.OpSge,
		gvir.OpOeq, gvir.OpOne, gvir.OpOlt, gvir.OpOle, gvir.OpOgt, gvir.OpOge,
		gvir.OpOrd, gvir.OpUnord:
		return f.compare(in)

	// --- Conversions (§11.5) -------------------------------------------------
	case gvir.OpTrunc, gvir.OpSext, gvir.OpZext, gvir.OpFPTrunc, gvir.OpFPExt,
		gvir.OpStoint, gvir.OpUtoint, gvir.OpInttos, gvir.OpInttou:
		return f.convert(in)
	case gvir.OpBitcast:
		return f.bitcast(in)

	// --- Select and calls (§11.7) --------------------------------------------
	case gvir.OpSelect:
		return f.selectOp(in)
	case gvir.OpCall:
		return f.call(in)

	// --- Memory (§8) ----------------------------------------------------------
	case gvir.OpAlloca:
		return f.alloca(in)
	case gvir.OpLoad:
		return f.load(in)
	case gvir.OpStore:
		return f.store(in)
	case gvir.OpIndex:
		return f.indexPtr(in)
	case gvir.OpField:
		return f.fieldPtr(in)
	case gvir.OpMemcopy:
		return f.memcopy(in)
	case gvir.OpMemset:
		return f.memset(in)
	case gvir.OpMemmove:
		return todof("memmove needs a direction test and a backward loop; unimplemented (README §9)")

	// --- Vectors (§4.4, §8.3) --------------------------------------------------
	case gvir.OpExtract:
		return f.extract(in)
	case gvir.OpInsert:
		return f.insert(in)
	case gvir.OpSplat:
		return f.splat(in)
	case gvir.OpSwizzle:
		return f.swizzle(in)

	// --- Synchronization, atomics, collectives (§10) ----------------------------
	case gvir.OpBarrier:
		return f.barrier(in)
	case gvir.OpFence:
		return f.fence(in)
	case gvir.OpAtomicLoad, gvir.OpAtomicStore:
		return f.atomicAccess(in)
	case gvir.OpAtomicAdd, gvir.OpAtomicSub, gvir.OpAtomicAnd, gvir.OpAtomicOr,
		gvir.OpAtomicXor, gvir.OpAtomicXchg, gvir.OpAtomicUMin, gvir.OpAtomicUMax,
		gvir.OpAtomicSMin, gvir.OpAtomicSMax:
		return f.atomicRMW(in)
	case gvir.OpCmpxchg:
		return f.cmpxchg(in)
	case gvir.OpShuffle, gvir.OpShuffleXor, gvir.OpShuffleUp, gvir.OpShuffleDown, gvir.OpBroadcast:
		return f.shuffle(in)
	case gvir.OpBroadcastFirst:
		return f.broadcastFirst(in)
	case gvir.OpAny, gvir.OpAll:
		return f.vote(in)
	case gvir.OpBallot:
		return f.ballot(in)
	case gvir.OpSubAdd, gvir.OpSubMin, gvir.OpSubMax, gvir.OpSubAnd, gvir.OpSubOr, gvir.OpSubXor:
		return f.subReduce(in)
	case gvir.OpMaskCount, gvir.OpMaskTest, gvir.OpMaskFirst, gvir.OpMaskEmpty:
		return f.maskOp(in)
	case gvir.OpMaskLt, gvir.OpMaskLe, gvir.OpMaskGt, gvir.OpMaskGe, gvir.OpMaskEq:
		return f.maskConst(in)

	// --- Execution builtins (§9) -------------------------------------------------
	default:
		if in.Op.IsBuiltin() {
			return f.builtin(in)
		}
	}
	return todof("%s is not implemented by this backend", in.Op)
}

func (f *fn) loc(in *gvir.Instruction) error {
	if len(in.Args) < 2 {
		return todof("loc takes a file and a line")
	}
	file := f.pm.File(in.Args[0].Str)
	col := 0
	if len(in.Args) > 2 {
		col = int(in.Args[2].Int)
	}
	f.b.Loc(file, int(in.Args[1].Int), col)
	return nil
}

// ---------------------------------------------------------------------------
// Integer arithmetic (§11.1)
// ---------------------------------------------------------------------------

func (f *fn) intArith(in *gvir.Instruction) error {
	ut, err := intType(in.Suffix, false)
	if err != nil {
		return err
	}
	st, err := intType(in.Suffix, true)
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		switch in.Op {
		case gvir.OpAdd:
			f.b.Add(ut, d, s[0], s[1])
		case gvir.OpSub:
			f.b.Sub(ut, d, s[0], s[1])
		case gvir.OpMul:
			// Wrapping is modulo 2^N and always defined (§11.1): the low half.
			f.b.Mul(ut, d, s[0], s[1], ptx.MulLo)
		case gvir.OpNeg:
			f.b.Neg(st, d, f.signed(s[0], in.Suffix))
		case gvir.OpAbs:
			f.b.Abs(st, d, f.signed(s[0], in.Suffix))
		}
		return nil
	})
}

// divRem pins §11.1's boundary cases. PTX leaves division by zero undefined;
// .gvir requires 0, and requires the same of sdiv/srem of INT_MIN by -1.
func (f *fn) divRem(in *gvir.Instruction) error {
	signed := in.Op == gvir.OpSDiv || in.Op == gvir.OpSRem
	rem := in.Op == gvir.OpURem || in.Op == gvir.OpSRem

	at, err := intType(in.Suffix, signed)
	if err != nil {
		return err
	}
	ut, err := intType(in.Suffix, false)
	if err != nil {
		return err
	}
	bits := valueBits(in.Suffix)

	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		x, y := s[0], s[1]
		if signed {
			x, y = f.signed(x, in.Suffix), f.signed(y, in.Suffix)
		}

		// bad = (y == 0) || (signed && x == INT_MIN && y == -1)
		bad := f.tempReg(ptx.Pred)
		f.b.Setp(ut, ptx.Eq, bad, y, ptx.Imm(0))
		if signed {
			pmin := f.tempReg(ptx.Pred)
			pneg := f.tempReg(ptx.Pred)
			both := f.tempReg(ptx.Pred)
			f.b.Setp(at, ptx.Eq, pmin, x, ptx.Imm(-1<<(bits-1)))
			f.b.Setp(at, ptx.Eq, pneg, y, ptx.Imm(-1))
			f.b.And(ptx.Pred, both, pmin, pneg)
			f.b.Or(ptx.Pred, bad, bad, both)
		}

		// Substitute a safe divisor, then force the result to zero. Predicating
		// the division itself would leave d undefined on the taken path, and
		// §7.3 rule 3 says there is no undef at the IR level.
		safe := f.tempReg(regTypeOf(at))
		f.b.Selp(at, safe, ptx.Imm(1), y, bad)

		raw := f.tempReg(regTypeOf(at))
		if rem {
			f.b.Rem(at, raw, x, safe)
		} else {
			f.b.Div(at, raw, x, safe)
		}
		f.b.Selp(at, d, ptx.Imm(0), raw, bad)
		return nil
	})
}

func (f *fn) mulHigh(in *gvir.Instruction) error {
	signed := in.Op == gvir.OpSMulH
	t, err := intType(in.Suffix, signed)
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		x, y := s[0], s[1]
		if signed {
			x, y = f.signed(x, in.Suffix), f.signed(y, in.Suffix)
		}
		f.b.Mul(t, d, x, y, ptx.MulHi)
		return nil
	})
}

func (f *fn) intMinMax(in *gvir.Instruction) error {
	signed := in.Op == gvir.OpSMin || in.Op == gvir.OpSMax
	t, err := intType(in.Suffix, signed)
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		x, y := s[0], s[1]
		if signed {
			x, y = f.signed(x, in.Suffix), f.signed(y, in.Suffix)
		}
		if in.Op == gvir.OpSMin || in.Op == gvir.OpUMin {
			f.b.Min(t, d, x, y)
		} else {
			f.b.Max(t, d, x, y)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Bitwise and shifts (§11.2)
// ---------------------------------------------------------------------------

func (f *fn) bitwise(in *gvir.Instruction) error {
	// and/or/xor/not additionally accept i1 and vec[i1,N] (§4.5), where PTX
	// spells them on .pred rather than on a bits type.
	t := ptx.Pred
	if !isPredElem(in.Suffix) {
		var err error
		if t, err = bitType(in.Suffix); err != nil {
			return err
		}
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		switch in.Op {
		case gvir.OpAnd:
			f.b.And(t, d, s[0], s[1])
		case gvir.OpOr:
			f.b.Or(t, d, s[0], s[1])
		case gvir.OpXor:
			f.b.Xor(t, d, s[0], s[1])
		case gvir.OpNot:
			f.b.Not(t, d, s[0])
		}
		return nil
	})
}

// shift masks the count to the operand width (§11.2). PTX clamps instead, so
// the mask is not redundant: a count of 33 on an i32 must shift by 1, not 0.
func (f *fn) shift(in *gvir.Instruction) error {
	bits := valueBits(in.Suffix)
	bt, err := bitType(in.Suffix)
	if err != nil {
		return err
	}
	ut, err := intType(in.Suffix, false)
	if err != nil {
		return err
	}
	st, err := intType(in.Suffix, true)
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		n := f.maskCount(s[1], bits)
		switch in.Op {
		case gvir.OpShl:
			f.b.Shl(bt, d, s[0], n)
		case gvir.OpLShr:
			f.b.Shr(ut, d, s[0], n)
		case gvir.OpAShr:
			f.b.Shr(st, d, f.signed(s[0], in.Suffix), n)
		}
		return nil
	})
}

// maskCount computes `n & (bits-1)` in a 32-bit register, which is the shape
// every PTX shift and rotate wants its count in.
func (f *fn) maskCount(n ptx.Operand, bits int) ptx.Operand {
	if imm, ok := n.(ptx.Imm); ok {
		return ptx.Imm(int64(imm) & int64(bits-1))
	}
	wide := f.widen32(n)
	d := f.tempReg(ptx.U32)
	f.b.And(ptx.B32, d, wide, ptx.Imm(int64(bits-1)))
	return d
}

// widen32 moves a value into a 32-bit register, zero-extending a narrow one.
func (f *fn) widen32(o ptx.Operand) ptx.Operand {
	r, ok := o.(ptx.Reg)
	if !ok {
		return o
	}
	switch r.Type() {
	case ptx.U32, ptx.S32, ptx.B32:
		return r
	}
	d := f.tempReg(ptx.U32)
	f.b.Cvt(ptx.U32, ptx.U16, d, r)
	return d
}

func (f *fn) rotate(in *gvir.Instruction) error {
	bits := valueBits(in.Suffix)
	bt, err := bitType(in.Suffix)
	if err != nil {
		return err
	}
	ut, err := intType(in.Suffix, false)
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		n := f.maskCount(s[1], bits)

		if bits == 32 {
			// The funnel shift rotates in one instruction when both halves are
			// the same value.
			dir := ptx.DirL
			if in.Op == gvir.OpRotr {
				dir = ptx.DirR
			}
			f.b.Shf(ptx.B32, d, s[0], s[0], n, dir, ptx.Wrap)
			return nil
		}

		// (x << n) | (x >> (N-n)); PTX clamps a shift of N to zero, which is
		// exactly what the n == 0 case needs.
		inv := f.tempReg(ptx.U32)
		f.b.Sub(ptx.U32, inv, ptx.Imm(int64(bits)), n)
		lo := f.tempReg(regTypeOf(bt))
		hi := f.tempReg(regTypeOf(bt))
		if in.Op == gvir.OpRotl {
			f.b.Shl(bt, lo, s[0], n)
			f.b.Shr(ut, hi, s[0], inv)
		} else {
			f.b.Shr(ut, lo, s[0], n)
			f.b.Shl(bt, hi, s[0], inv)
		}
		f.b.Or(bt, d, lo, hi)
		return nil
	})
}

// countZeros pins §11.2's "ctlz/cttz of zero yield the operand bit width".
func (f *fn) countZeros(in *gvir.Instruction) error {
	bits := valueBits(in.Suffix)
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		if bits == 64 {
			t := f.tempReg(ptx.U32)
			if in.Op == gvir.OpCttz {
				rev := f.tempReg(ptx.B64)
				f.b.Brev(ptx.B64, rev, s[0])
				f.b.Clz(ptx.B64, t, rev)
			} else {
				f.b.Clz(ptx.B64, t, s[0])
			}
			f.b.Cvt(ptx.U64, ptx.U32, d, t)
			return nil
		}

		x := f.widen32(s[0])
		c := f.tempReg(ptx.U32)
		if in.Op == gvir.OpCtlz {
			// clz32 counts (32-N) padding zeros too; subtracting them yields N
			// for a zero input without a branch.
			f.b.Clz(ptx.B32, c, x)
			if bits < 32 {
				f.b.Sub(ptx.U32, c, c, ptx.Imm(int64(32-bits)))
			}
		} else {
			rev := f.tempReg(ptx.B32)
			f.b.Brev(ptx.B32, rev, x)
			f.b.Clz(ptx.B32, c, rev)
			if bits < 32 {
				f.b.Min(ptx.U32, c, c, ptx.Imm(int64(bits)))
			}
		}
		f.narrowInto(d, c, bits)
		return nil
	})
}

func (f *fn) popcount(in *gvir.Instruction) error {
	bits := valueBits(in.Suffix)
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		if bits == 64 {
			t := f.tempReg(ptx.U32)
			f.b.Popc(ptx.B64, t, s[0])
			f.b.Cvt(ptx.U64, ptx.U32, d, t)
			return nil
		}
		c := f.tempReg(ptx.U32)
		f.b.Popc(ptx.B32, c, f.widen32(s[0]))
		f.narrowInto(d, c, bits)
		return nil
	})
}

func (f *fn) bitReverse(in *gvir.Instruction) error {
	bits := valueBits(in.Suffix)
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		if bits == 64 {
			f.b.Brev(ptx.B64, d, s[0])
			return nil
		}
		rev := f.tempReg(ptx.B32)
		f.b.Brev(ptx.B32, rev, f.widen32(s[0]))
		if bits < 32 {
			f.b.Shr(ptx.U32, rev, rev, ptx.Imm(int64(32-bits)))
		}
		f.narrowInto(d, rev, bits)
		return nil
	})
}

func (f *fn) byteSwap(in *gvir.Instruction) error {
	bits := valueBits(in.Suffix)
	if bits == 64 {
		return todof("bswap on i64 needs a split/prmt/join sequence; unimplemented (README §9)")
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		switch bits {
		case 8:
			f.b.Mov(ptx.B16, d, s[0])
		case 16:
			x := f.widen32(s[0])
			lo := f.tempReg(ptx.B32)
			hi := f.tempReg(ptx.B32)
			f.b.Shl(ptx.B32, lo, x, ptx.Imm(8))
			f.b.Shr(ptx.U32, hi, x, ptx.Imm(8))
			t := f.tempReg(ptx.B32)
			f.b.Or(ptx.B32, t, lo, hi)
			f.narrowInto(d, t, 16)
		case 32:
			// prmt selects bytes 3,2,1,0 of {b, a} in one instruction.
			f.b.Prmt(ptx.B32, d, s[0], ptx.Imm(0), ptx.Imm(0x0123))
		}
		return nil
	})
}

// narrowInto moves a 32-bit computation back into a value register of `bits`.
func (f *fn) narrowInto(d ptx.Reg, src ptx.Reg, bits int) {
	if bits >= 32 {
		f.b.Mov(ptx.B32, d, src)
		return
	}
	f.b.Cvt(ptx.U16, ptx.U32, d, src)
	if bits == 8 {
		f.b.And(ptx.B16, d, d, ptx.Imm(0xff))
	}
}

// regTypeOf is the register type to allocate for a value of instruction type t.
func regTypeOf(t ptx.Type) ptx.Type {
	switch t.Bits() {
	case 8, 16:
		return ptx.U16
	case 32:
		if t.IsFloat() {
			return ptx.F32
		}
		return ptx.U32
	case 64:
		if t.IsFloat() {
			return ptx.F64
		}
		return ptx.U64
	}
	return t
}

// ---------------------------------------------------------------------------
// Float (§11.3)
// ---------------------------------------------------------------------------

func (f *fn) floatArith(in *gvir.Instruction) error {
	t, err := regType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		switch in.Op {
		case gvir.OpAdd:
			f.b.Add(t, d, s[0], s[1], ptx.RN)
		case gvir.OpSub:
			f.b.Sub(t, d, s[0], s[1], ptx.RN)
		case gvir.OpMul:
			f.b.Mul(t, d, s[0], s[1], ptx.RN)
		case gvir.OpDiv:
			f.b.Div(t, d, s[0], s[1], ptx.RN)
		case gvir.OpNeg:
			f.b.Neg(t, d, s[0])
		case gvir.OpAbs:
			f.b.Abs(t, d, s[0])
		case gvir.OpSqrt:
			f.b.Sqrt(t, d, s[0], ptx.RN)
		case gvir.OpFma:
			// A single rounding by definition (§11.3), and ptx.Verify insists
			// on the explicit qualifier anyway.
			f.b.Fma(t, d, s[0], s[1], s[2], ptx.RN)
		case gvir.OpMin:
			// PTX min without .NaN is IEEE minNum: one NaN operand returns the
			// other. The qualifier is deliberately absent (§11.3).
			f.b.Min(t, d, s[0], s[1])
		case gvir.OpMax:
			f.b.Max(t, d, s[0], s[1])
		case gvir.OpCopysign:
			f.b.Copysign(t, d, s[1], s[0])
		}
		return nil
	})
}

func (f *fn) floatRound(in *gvir.Instruction) error {
	t, err := regType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	var mode ptx.Round
	switch in.Op {
	case gvir.OpFloor:
		mode = ptx.RMI
	case gvir.OpCeil:
		mode = ptx.RPI
	case gvir.OpRoundEven:
		mode = ptx.RNI
	case gvir.OpTruncF:
		mode = ptx.RZI
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		f.b.Cvt(t, t, d, s[0], mode)
		return nil
	})
}

// roundHalfAway implements §11.3's `round`. PTX has no half-away-from-zero
// rounding mode, and `trunc(x + copysign(0.5, x))` is wrong for the largest
// value below .5, so the fraction is tested explicitly.
func (f *fn) roundHalfAway(in *gvir.Instruction) error {
	t, err := regType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	half, err := immOperand(gvir.FloatLiteral(0.5), gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	one, err := immOperand(gvir.FloatLiteral(1), gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		trunc := f.tempReg(t)
		f.b.Cvt(t, t, trunc, s[0], ptx.RZI)

		frac := f.tempReg(t)
		f.b.Sub(t, frac, s[0], trunc, ptx.RN)
		f.b.Abs(t, frac, frac)

		p := f.tempReg(ptx.Pred)
		f.b.Setp(t, ptx.Ge, p, frac, half)

		step := f.tempReg(t)
		f.b.Copysign(t, step, s[0], one)

		bumped := f.tempReg(t)
		f.b.Add(t, bumped, trunc, step, ptx.RN)
		f.b.Selp(t, d, bumped, trunc, p)
		return nil
	})
}

func (f *fn) floatClassify(in *gvir.Instruction) error {
	t, err := regType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		if in.Op == gvir.OpIsNaN {
			f.b.Setp(t, ptx.Nan, d, s[0], s[0])
		} else {
			f.b.Testp(t, d, s[0], ptx.Infinite)
		}
		return nil
	})
}

// approx lowers the §11.6 opcodes. Emitting one without float_profile approx is
// a lowering error rather than a silent strict substitution: the accuracy floors
// are part of the contract and a strict result is a different program.
func (f *fn) approx(in *gvir.Instruction) error {
	if !f.gm.Profile.Approx {
		return todof("%s requires `float_profile approx` (§11.6)", in.Op)
	}
	t, err := regType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		switch in.Op {
		case gvir.OpRcp:
			f.b.Rcp(t, d, s[0], ptx.Approx)
		case gvir.OpRsqrt:
			f.b.Rsqrt(t, d, s[0], ptx.Approx)
		case gvir.OpSin:
			f.b.Sin(t, d, s[0], ptx.Approx)
		case gvir.OpCos:
			f.b.Cos(t, d, s[0], ptx.Approx)
		case gvir.OpExp2:
			f.b.Ex2(t, d, s[0], ptx.Approx)
		case gvir.OpLog2:
			f.b.Lg2(t, d, s[0], ptx.Approx)
		case gvir.OpTanh:
			f.b.Tanh(t, d, s[0], ptx.Approx)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Comparisons (§11.4)
// ---------------------------------------------------------------------------

func (f *fn) compare(in *gvir.Instruction) error {
	var (
		cmp    ptx.Cmp
		signed bool
	)
	switch in.Op {
	case gvir.OpEq:
		cmp = ptx.Eq
	case gvir.OpNe:
		cmp = ptx.Ne
	case gvir.OpUlt:
		cmp = ptx.Lo
	case gvir.OpUle:
		cmp = ptx.Ls
	case gvir.OpUgt:
		cmp = ptx.Hi
	case gvir.OpUge:
		cmp = ptx.Hs
	case gvir.OpSlt:
		cmp, signed = ptx.Lt, true
	case gvir.OpSle:
		cmp, signed = ptx.Le, true
	case gvir.OpSgt:
		cmp, signed = ptx.Gt, true
	case gvir.OpSge:
		cmp, signed = ptx.Ge, true
	// Only ordered float predicates plus ord/unord exist (§11.4); PTX spells
	// the ordered forms unqualified and the unordered ones with a `u`.
	case gvir.OpOeq:
		cmp = ptx.Eq
	case gvir.OpOne:
		cmp = ptx.Ne
	case gvir.OpOlt:
		cmp = ptx.Lt
	case gvir.OpOle:
		cmp = ptx.Le
	case gvir.OpOgt:
		cmp = ptx.Gt
	case gvir.OpOge:
		cmp = ptx.Ge
	case gvir.OpOrd:
		cmp = ptx.Num
	case gvir.OpUnord:
		cmp = ptx.Nan
	}

	var (
		t   ptx.Type
		err error
	)
	switch {
	case gvir.IsPtrWord(in.Suffix):
		// eq.ptr, ult.ptr, … — same address space only, checked in ir/verify.
		t = ptx.U64
	case isFloatElem(in.Suffix):
		t, err = regType(gvir.ElemOrSelf(in.Suffix))
	default:
		t, err = intType(in.Suffix, signed)
	}
	if err != nil {
		return err
	}

	srcType := in.Suffix
	if gvir.IsPtrWord(in.Suffix) {
		// The operands are real pointers; the suffix is only the bare word.
		if pt, ok := f.typeOf(in.Args[0]); ok {
			srcType = pt
		}
	}

	dst, err := f.result(in)
	if err != nil {
		return err
	}
	a, err := f.lanes(in.Args[0], srcType)
	if err != nil {
		return err
	}
	c, err := f.lanes(in.Args[1], srcType)
	if err != nil {
		return err
	}
	for k, d := range dst.regs {
		x, y := a[k], c[k]
		if signed {
			x, y = f.signed(x, in.Suffix), f.signed(y, in.Suffix)
		}
		f.b.Setp(t, cmp, d, x, y)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Conversions (§11.5)
// ---------------------------------------------------------------------------

func (f *fn) convert(in *gvir.Instruction) error {
	src, ok := f.typeOf(in.Args[0])
	if !ok {
		return todof("%s operand has no bound type", in.Op)
	}
	dstElem := gvir.ElemOrSelf(in.Suffix)
	srcElem := gvir.ElemOrSelf(src)

	var (
		dt, st ptx.Type
		quals  []ptx.Qual
		err    error
	)
	switch in.Op {
	case gvir.OpTrunc, gvir.OpZext:
		if dt, err = intType(dstElem, false); err == nil {
			st, err = intType(srcElem, false)
		}
	case gvir.OpSext:
		if dt, err = intType(dstElem, true); err == nil {
			st, err = intType(srcElem, true)
		}
	case gvir.OpFPTrunc:
		if dt, err = regType(dstElem); err == nil {
			st, err = regType(srcElem)
		}
		quals = append(quals, ptx.RN)
	case gvir.OpFPExt:
		if dt, err = regType(dstElem); err == nil {
			st, err = regType(srcElem)
		}
	case gvir.OpStoint:
		// Float-to-int is saturating and total (§11.5); PTX cvt with an integer
		// destination already clamps and maps NaN to 0, so .rzi alone is the
		// whole story — no clamp sequence, one family not two.
		if dt, err = intType(dstElem, true); err == nil {
			st, err = regType(srcElem)
		}
		quals = append(quals, ptx.RZI)
	case gvir.OpUtoint:
		if dt, err = intType(dstElem, false); err == nil {
			st, err = regType(srcElem)
		}
		quals = append(quals, ptx.RZI)
	case gvir.OpInttos:
		if dt, err = regType(dstElem); err == nil {
			st, err = intType(srcElem, true)
		}
		quals = append(quals, ptx.RN)
	case gvir.OpInttou:
		if dt, err = regType(dstElem); err == nil {
			st, err = intType(srcElem, false)
		}
		quals = append(quals, ptx.RN)
	}
	if err != nil {
		return err
	}

	dst, err := f.result(in)
	if err != nil {
		return err
	}
	a, err := f.lanes(in.Args[0], src)
	if err != nil {
		return err
	}
	for k, d := range dst.regs {
		x := a[k]
		if in.Op == gvir.OpSext || in.Op == gvir.OpInttos {
			x = f.signed(x, srcElem)
		}
		f.b.Cvt(dt, st, d, x, quals...)
	}
	if needsMask(in.Suffix) {
		return f.maskLanes(dst)
	}
	return nil
}

func (f *fn) bitcast(in *gvir.Instruction) error {
	// Equal bit widths and the differing-address-space rule are two-operand
	// checks that ir/verify has already made; here it is a typed move.
	t, err := bitType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	return f.each(in, func(d ptx.Reg, s []ptx.Operand) error {
		f.b.Mov(t, d, s[0])
		return nil
	})
}

// ---------------------------------------------------------------------------
// Select and calls (§11.7)
// ---------------------------------------------------------------------------

func (f *fn) selectOp(in *gvir.Instruction) error {
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	condType, _ := f.typeOf(in.Args[0])
	a, err := f.lanes(in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	c, err := f.lanes(in.Args[2], in.Suffix)
	if err != nil {
		return err
	}

	// The condition is i1 (whole-vector) or vec[i1,N] (elementwise).
	conds := make([]ptx.Reg, len(dst.regs))
	if gvir.IsPredicateVector(condType) {
		v, ok := f.lookup(in.Args[0])
		if !ok || len(v.regs) != len(dst.regs) {
			return todof("select condition has the wrong lane count")
		}
		copy(conds, v.regs)
	} else {
		p, err := f.pred(in.Args[0])
		if err != nil {
			return err
		}
		for i := range conds {
			conds[i] = p
		}
	}

	elem := gvir.ElemOrSelf(in.Suffix)
	if gvir.IsBool(elem) {
		// selp has no predicate form: (c & a) | (!c & b). Both arms are
		// evaluated either way, which §11.7 requires anyway.
		for k, d := range dst.regs {
			nc := f.tempReg(ptx.Pred)
			t1 := f.tempReg(ptx.Pred)
			t2 := f.tempReg(ptx.Pred)
			f.b.Not(ptx.Pred, nc, conds[k])
			f.b.And(ptx.Pred, t1, conds[k], a[k])
			f.b.And(ptx.Pred, t2, nc, c[k])
			f.b.Or(ptx.Pred, d, t1, t2)
		}
		return nil
	}

	t, err := selpType(elem)
	if err != nil {
		return err
	}
	for k, d := range dst.regs {
		f.b.Selp(t, d, a[k], c[k], conds[k])
	}
	return nil
}

func selpType(t gvir.Type) (ptx.Type, error) {
	if gvir.IsFloat(t) {
		return regType(t)
	}
	return bitType(t)
}

func (f *fn) call(in *gvir.Instruction) error {
	if len(in.Args) == 0 {
		return todof("call has no callee")
	}
	name := in.Args[0].Ident
	callee := f.funcs[name]
	gcallee := f.gm.FuncByName(name)
	if callee == nil || gcallee == nil {
		return todof("call to undefined func %q", name)
	}
	if len(in.Args)-1 != len(gcallee.Params) {
		return todof("call to %s passes %d arguments, %d declared", name, len(in.Args)-1, len(gcallee.Params))
	}

	var (
		args     []ptx.Operand
		argTypes []ptx.Type
	)
	for i, p := range gcallee.Params {
		ops, err := f.lanes(in.Args[i+1], p.Type)
		if err != nil {
			return err
		}
		mt, err := memType(gvir.ElemOrSelf(p.Type))
		if err != nil {
			return err
		}
		for _, o := range ops {
			args = append(args, o)
			argTypes = append(argTypes, mt)
		}
	}

	var rets []ptx.Reg
	if !gvir.IsVoid(gcallee.Ret) {
		dst, err := f.define(in.Result, gcallee.Ret)
		if err != nil {
			return err
		}
		rets = dst.regs
	}

	// Invoke emits the whole ABI sequence: the nested scope, the .param
	// declarations, the argument stores, the call, and the result loads.
	f.b.Invoke(callee, args, argTypes, rets)
	return nil
}