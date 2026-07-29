package ptx

import (
	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Synchronization, atomics and subgroup collectives (§10).
//
// Uniform reachability is a verification requirement (§7.4), already discharged
// by ir/verify. That guarantee is what makes the membermask arithmetic below
// sound: every lane that could reach a collective does reach it, so the only
// inactive lanes are the unpopulated tail of a partial subgroup (§9.2), and the
// active set is therefore always a prefix.

// ---------------------------------------------------------------------------
// Barriers (§10.1)
// ---------------------------------------------------------------------------

func (f *fn) barrier(in *gvir.Instruction) error {
	mem := gvir.Scope(in.Exec) // the memory scope defaults to the execution scope
	if len(in.Args) > 0 {
		mem = in.Args[0].Scope
	}

	// A memory scope wider than the execution barrier's own reach needs an
	// explicit fence ahead of it; bar.sync already orders CTA-scope memory.
	switch mem {
	case gvir.ScopeGrid:
		f.b.Membar(ptx.LevelGL)
	case gvir.ScopeGroup:
		if in.Exec == gvir.ExecSubgroup {
			f.b.Membar(ptx.LevelCTA)
		}
	case gvir.ScopeNone, gvir.ScopeSubgroup:
		// Execution only, or warp-scope memory, which is implicit.
	}

	switch in.Exec {
	case gvir.ExecGroup:
		f.b.BarSync(ptx.Imm(0))
	case gvir.ExecSubgroup:
		f.b.BarWarpSync(f.activeMask())
	default:
		return todof("barrier has no execution scope (§10.1)")
	}
	return nil
}

func (f *fn) fence(in *gvir.Instruction) error {
	if len(in.Args) != 2 {
		return todof("fence takes a scope and an ordering (§10.2)")
	}
	scope := in.Args[0].Scope
	order := in.Args[1].Ordering
	if order == gvir.OrderRelaxed {
		// fence relaxed is a no-op (§10.2).
		return nil
	}
	sc, err := memScope(scope)
	if err != nil {
		return err
	}
	sem := ptx.SemAcqRel
	if order == gvir.OrderSeqCst {
		sem = ptx.SemSC
	}
	f.b.Fence(sem, sc)
	return nil
}

// memScope maps a .gvir scope onto a PTX memory-consistency scope. There is no
// warp scope in PTX; cta is the tightest scope that contains a subgroup.
func memScope(s gvir.Scope) (ptx.Scope, error) {
	switch s {
	case gvir.ScopeSubgroup, gvir.ScopeGroup:
		return ptx.ScopeCTA, nil
	case gvir.ScopeGrid:
		return ptx.ScopeGPU, nil
	}
	return ptx.NoScope, todof("scope %q is not a memory scope here (§10.2)", s)
}

// ---------------------------------------------------------------------------
// Atomics (§10.2)
// ---------------------------------------------------------------------------

// Every atomic is relaxed and carries a scope as its final operand; ordering is
// expressed separately with fence, because one backend exposes relaxed RMW only.

func (f *fn) atomicAccess(in *gvir.Instruction) error {
	p, sp, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	scope := in.Args[len(in.Args)-1].Scope
	sc, err := memScope(scope)
	if err != nil {
		return err
	}
	mt, err := memType(in.Suffix)
	if err != nil {
		return err
	}

	if in.Op == gvir.OpAtomicLoad {
		dst, err := f.result(in)
		if err != nil {
			return err
		}
		f.b.Ld(mt, dst.reg(), ptx.At(p.reg()), ptx.SemRelaxed, sc, sp)
		return nil
	}
	v, err := f.scalar(in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	f.b.St(mt, ptx.At(p.reg()), v, ptx.SemRelaxed, sc, sp)
	return nil
}

func (f *fn) atomicRMW(in *gvir.Instruction) error {
	p, sp, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	sc, err := memScope(in.Args[len(in.Args)-1].Scope)
	if err != nil {
		return err
	}
	v, err := f.scalar(in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}

	var (
		op ptx.AtomOp
		t  ptx.Type
	)
	signed := in.Op == gvir.OpAtomicSMin || in.Op == gvir.OpAtomicSMax
	switch in.Op {
	case gvir.OpAtomicAdd:
		op = ptx.AtomAdd
	case gvir.OpAtomicSub:
		// PTX has no atom.sub: negate and add. Wrapping makes this exact.
		op = ptx.AtomAdd
		neg, err := f.negateForAtomic(in.Suffix, v)
		if err != nil {
			return err
		}
		v = neg
	case gvir.OpAtomicAnd:
		op = ptx.AtomAnd
	case gvir.OpAtomicOr:
		op = ptx.AtomOr
	case gvir.OpAtomicXor:
		op = ptx.AtomXor
	case gvir.OpAtomicXchg:
		op = ptx.AtomExch
	case gvir.OpAtomicUMin, gvir.OpAtomicSMin:
		op = ptx.AtomMin
	case gvir.OpAtomicUMax, gvir.OpAtomicSMax:
		op = ptx.AtomMax
	}

	switch {
	case isFloatElem(in.Suffix):
		t, err = regType(in.Suffix)
	case op == ptx.AtomAnd || op == ptx.AtomOr || op == ptx.AtomXor || op == ptx.AtomExch:
		t, err = bitType(in.Suffix)
	default:
		t, err = intType(in.Suffix, signed)
	}
	if err != nil {
		return err
	}

	quals := []ptx.Qual{ptx.SemRelaxed, sc, sp, op}
	f.b.Atom(t, dst.reg(), ptx.At(p.reg()), []ptx.Operand{v}, quals...)
	return nil
}

func (f *fn) negateForAtomic(t gvir.Type, v ptx.Operand) (ptx.Operand, error) {
	if imm, ok := v.(ptx.Imm); ok {
		return ptx.Imm(-int64(imm)), nil
	}
	st, err := intType(t, true)
	if err != nil {
		return nil, err
	}
	if isFloatElem(t) {
		st, err = regType(t)
		if err != nil {
			return nil, err
		}
	}
	d := f.tempReg(regTypeOf(st))
	f.b.Neg(st, d, v)
	return d, nil
}

// cmpxchg yields the old value at p, not a success flag (§10.2) — which is also
// what atom.cas does, so there is nothing to translate.
func (f *fn) cmpxchg(in *gvir.Instruction) error {
	p, sp, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	expected, err := f.scalar(in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	desired, err := f.scalar(in.Args[2], in.Suffix)
	if err != nil {
		return err
	}
	sc, err := memScope(in.Args[3].Scope)
	if err != nil {
		return err
	}
	t, err := bitType(in.Suffix)
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	f.b.Atom(t, dst.reg(), ptx.At(p.reg()), []ptx.Operand{expected, desired},
		ptx.SemRelaxed, sc, sp, ptx.AtomCAS)
	return nil
}

// ---------------------------------------------------------------------------
// Subgroup collectives (§10.3)
// ---------------------------------------------------------------------------

// activeMask is the membermask every collective below uses. Reading from an
// inactive lane yields a frozen unspecified value (§10.3), which is exactly what
// a shfl from a non-member lane produces.
func (f *fn) activeMask() ptx.Reg {
	m := f.tempReg(ptx.B32)
	f.b.Activemask(ptx.B32, m)
	return m
}

func (f *fn) shuffle(in *gvir.Instruction) error {
	var (
		mode  ptx.ShflMode
		clamp int64 = 0x1f
	)
	switch in.Op {
	case gvir.OpShuffle, gvir.OpBroadcast:
		mode = ptx.ShflIdx
	case gvir.OpShuffleXor:
		mode = ptx.ShflBfly
	case gvir.OpShuffleUp:
		mode, clamp = ptx.ShflUp, 0 // shifted-out lanes return the source value
	case gvir.OpShuffleDown:
		mode = ptx.ShflDown
	}

	mask := f.activeMask()
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	src, err := f.lanes(in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	delta, err := f.scalar(in.Args[1], gvir.I32)
	if err != nil {
		return err
	}
	for k, d := range dst.regs {
		if err := f.shflOne(gvir.ElemOrSelf(in.Suffix), d, src[k], delta, mode, clamp, mask); err != nil {
			return err
		}
	}
	return nil
}

// shflOne shuffles one scalar of any width. shfl.sync moves 32 bits, so 64-bit
// values are split and 16-bit values are widened.
func (f *fn) shflOne(t gvir.Type, d ptx.Reg, v, lane ptx.Operand, mode ptx.ShflMode, clamp int64, mask ptx.Reg) error {
	switch regBits(t) {
	case 32:
		f.b.ShflSync(ptx.B32, d, v, lane, ptx.Imm(clamp), mask, mode)
		return nil

	case 64:
		lo, hi := f.split64(v)
		dlo, dhi := f.tempReg(ptx.B32), f.tempReg(ptx.B32)
		f.b.ShflSync(ptx.B32, dlo, lo, lane, ptx.Imm(clamp), mask, mode)
		f.b.ShflSync(ptx.B32, dhi, hi, lane, ptx.Imm(clamp), mask, mode)
		f.join64(d, dlo, dhi)
		return nil

	case 16:
		wide := f.tempReg(ptx.B32)
		bits := f.tempReg(ptx.B16)
		f.b.Mov(ptx.B16, bits, v)
		f.b.Cvt(ptx.U32, ptx.U16, wide, bits)
		out := f.tempReg(ptx.B32)
		f.b.ShflSync(ptx.B32, out, wide, lane, ptx.Imm(clamp), mask, mode)
		narrow := f.tempReg(ptx.B16)
		f.b.Cvt(ptx.U16, ptx.U32, narrow, out)
		f.b.Mov(ptx.B16, d, narrow)
		return nil
	}
	return todof("no shuffle for a %d-bit value", regBits(t))
}

func (f *fn) split64(v ptx.Operand) (lo, hi ptx.Reg) {
	lo, hi = f.tempReg(ptx.B32), f.tempReg(ptx.B32)
	f.b.Emit("mov.b64", []ptx.Operand{ptx.Vec(lo, hi)}, []ptx.Operand{v})
	return lo, hi
}

func (f *fn) join64(d ptx.Reg, lo, hi ptx.Reg) {
	f.b.Emit("mov.b64", []ptx.Operand{d}, []ptx.Operand{ptx.Vec(lo, hi)})
}

// broadcastFirst takes the value from the lowest active lane (§10.3): reverse
// the active mask and count leading zeros to get its index.
func (f *fn) broadcastFirst(in *gvir.Instruction) error {
	mask := f.activeMask()
	rev := f.tempReg(ptx.B32)
	first := f.tempReg(ptx.U32)
	f.b.Brev(ptx.B32, rev, mask)
	f.b.Clz(ptx.B32, first, rev)

	dst, err := f.result(in)
	if err != nil {
		return err
	}
	src, err := f.lanes(in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	for k, d := range dst.regs {
		if err := f.shflOne(gvir.ElemOrSelf(in.Suffix), d, src[k], first, ptx.ShflIdx, 0x1f, mask); err != nil {
			return err
		}
	}
	return nil
}

func (f *fn) vote(in *gvir.Instruction) error {
	p, err := f.pred(in.Args[0])
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	mode := ptx.VoteAny
	if in.Op == gvir.OpAll {
		mode = ptx.VoteAll
	}
	f.b.VoteSync(ptx.Pred, dst.reg(), p, f.activeMask(), mode)
	return nil
}

func (f *fn) ballot(in *gvir.Instruction) error {
	p, err := f.pred(in.Args[0])
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	// Inactive lanes read as false, which is what vote.ballot yields for a
	// non-member lane.
	f.b.VoteSync(ptx.B32, dst.reg(), p, f.activeMask(), ptx.VoteBallot)
	return nil
}

// subReduce is a five-step shfl.down tree followed by a broadcast from lane 0.
// Each combine is predicated on the partner lane being present in activemask;
// §9.2 makes the active set a prefix, so the tree is exact rather than
// approximate. redux.sync would be one instruction but needs sm_80, above the
// sm_70 floor §3 pins.
func (f *fn) subReduce(in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	mask := f.activeMask()

	lane := f.tempReg(ptx.U32)
	f.b.MovSReg(lane, ptx.LaneID)

	dst, err := f.result(in)
	if err != nil {
		return err
	}
	src, err := f.lanes(in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	mt, err := movType(elem)
	if err != nil {
		return err
	}
	selT, err := selpType(elem)
	if err != nil {
		return err
	}
	rt, err := regType(elem)
	if err != nil {
		return err
	}

	for k, d := range dst.regs {
		acc := f.tempReg(rt)
		f.b.Mov(mt, acc, src[k])

		for _, delta := range []int64{16, 8, 4, 2, 1} {
			partnerVal := f.tempReg(rt)
			if err := f.shflOne(elem, partnerVal, acc, ptx.Imm(delta), ptx.ShflDown, 0x1f, mask); err != nil {
				return err
			}

			partner := f.tempReg(ptx.U32)
			f.b.Add(ptx.U32, partner, lane, ptx.Imm(delta))
			present := f.maskTest(mask, partner)

			combined := f.tempReg(rt)
			if err := f.reduceStep(in.Op, elem, combined, acc, partnerVal); err != nil {
				return err
			}
			f.b.Selp(selT, acc, combined, acc, present)
		}
		// Lane 0 now holds the reduction over the whole active prefix.
		if err := f.shflOne(elem, d, acc, ptx.Imm(0), ptx.ShflIdx, 0x1f, mask); err != nil {
			return err
		}
	}
	return nil
}

func (f *fn) reduceStep(op gvir.Opcode, t gvir.Type, d ptx.Reg, x, y ptx.Operand) error {
	if gvir.IsFloat(t) {
		ft, err := regType(t)
		if err != nil {
			return err
		}
		switch op {
		case gvir.OpSubAdd:
			// Reduction order is unspecified (§10.3); the additions are IEEE.
			f.b.Add(ft, d, x, y, ptx.RN)
		case gvir.OpSubMin:
			f.b.Min(ft, d, x, y)
		case gvir.OpSubMax:
			f.b.Max(ft, d, x, y)
		default:
			return todof("%s is not defined on %s", op, t)
		}
		return nil
	}

	ut, err := intType(t, false)
	if err != nil {
		return err
	}
	bt, err := bitType(t)
	if err != nil {
		return err
	}
	switch op {
	case gvir.OpSubAdd:
		f.b.Add(ut, d, x, y)
	case gvir.OpSubMin:
		f.b.Min(ut, d, x, y)
	case gvir.OpSubMax:
		f.b.Max(ut, d, x, y)
	case gvir.OpSubAnd:
		f.b.And(bt, d, x, y)
	case gvir.OpSubOr:
		f.b.Or(bt, d, x, y)
	case gvir.OpSubXor:
		f.b.Xor(bt, d, x, y)
	}
	return nil
}

// ---------------------------------------------------------------------------
// submask operations (§10.3)
// ---------------------------------------------------------------------------

func (f *fn) maskOp(in *gvir.Instruction) error {
	m, err := f.scalar(in.Args[0], gvir.Submask)
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	switch in.Op {
	case gvir.OpMaskCount:
		f.b.Popc(ptx.B32, dst.reg(), m)
	case gvir.OpMaskEmpty:
		f.b.Setp(ptx.B32, ptx.Eq, dst.reg(), m, ptx.Imm(0))
	case gvir.OpMaskFirst:
		rev := f.tempReg(ptx.B32)
		f.b.Brev(ptx.B32, rev, m)
		f.b.Clz(ptx.B32, dst.reg(), rev)
	case gvir.OpMaskTest:
		lane, err := f.scalar(in.Args[1], gvir.I32)
		if err != nil {
			return err
		}
		r, ok := m.(ptx.Reg)
		if !ok {
			r = f.tempReg(ptx.B32)
			f.b.Mov(ptx.B32, r, m)
		}
		p := f.maskTest(r, lane)
		f.b.Mov(ptx.Pred, dst.reg(), p)
	}
	return nil
}

// maskTest is `(m >> lane) & 1`. A lane at or beyond the warp width shifts the
// mask out entirely — PTX clamps the count — which is the answer we want.
func (f *fn) maskTest(m ptx.Reg, lane ptx.Operand) ptx.Reg {
	shifted := f.tempReg(ptx.B32)
	bit := f.tempReg(ptx.B32)
	p := f.tempReg(ptx.Pred)
	f.b.Shr(ptx.U32, shifted, m, lane)
	f.b.And(ptx.B32, bit, shifted, ptx.Imm(1))
	f.b.Setp(ptx.U32, ptx.Ne, p, bit, ptx.Imm(0))
	return p
}

func (f *fn) maskConst(in *gvir.Instruction) error {
	var sreg ptx.SReg
	switch in.Op {
	case gvir.OpMaskLt:
		sreg = ptx.LaneMaskLt
	case gvir.OpMaskLe:
		sreg = ptx.LaneMaskLe
	case gvir.OpMaskGt:
		sreg = ptx.LaneMaskGt
	case gvir.OpMaskGe:
		sreg = ptx.LaneMaskGe
	case gvir.OpMaskEq:
		sreg = ptx.LaneMaskEq
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	f.b.MovSReg(dst.reg(), sreg)
	return nil
}