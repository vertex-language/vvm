// isel_sync.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Barriers, fences, atomics and the subgroup collectives.
//
// .gvir atomics are relaxed and express ordering only through fence (§10.2),
// which is AMDTX's model too (§12.4), so this file maps scopes across and
// leaves the cache-maintenance sequence to AMDTX lowering: §8.3 says in as
// many words that code needing portable ordering should use fence and let
// lowering pick the bits.

func amdScope(s gvir.Scope) (amdtx.Scope, error) {
	switch s {
	case gvir.ScopeSubgroup:
		return amdtx.Wavefront, nil
	case gvir.ScopeGroup:
		return amdtx.Workgroup, nil
	case gvir.ScopeGrid:
		return amdtx.Agent, nil
	}
	return amdtx.NoScope, fmt.Errorf("scope %q has no AMDTX spelling", s)
}

func amdOrdering(o gvir.Ordering) (amdtx.Ordering, bool) {
	switch o {
	case gvir.OrderAcquire:
		return amdtx.Acquire, true
	case gvir.OrderRelease:
		return amdtx.Release, true
	case gvir.OrderAcqRel:
		return amdtx.AcqRel, true
	case gvir.OrderSeqCst:
		return amdtx.SeqCst, true
	}
	return amdtx.NoOrdering, false // relaxed is a no-op
}

func (b *bodyLowerer) sync(o *amdtx.Body, in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpBarrier:
		return b.barrier(o, in)
	case gvir.OpFence:
		return b.fence(o, in)
	case gvir.OpMaskCount, gvir.OpMaskTest, gvir.OpMaskFirst, gvir.OpMaskEmpty:
		return b.maskOp(o, in)
	case gvir.OpMaskLt, gvir.OpMaskLe, gvir.OpMaskGt, gvir.OpMaskGe, gvir.OpMaskEq:
		return fmt.Errorf("%s is a per-lane submask value and amdgcn has no lanemask special "+
			"registers to read it from; materializing it conflicts with submask living in a "+
			".lanemask and both halves need doing together", in.Op)
	}
	if in.Op.IsAtomic() {
		return b.atomic(o, in)
	}
	if in.Op.IsCollective() {
		return b.collective(o, in)
	}
	return fmt.Errorf("%s is not a synchronization opcode", in.Op)
}

func (b *bodyLowerer) barrier(o *amdtx.Body, in *gvir.Instruction) error {
	mem := gvir.Scope("")
	if len(in.Args) > 0 && in.Args[0].Kind == gvir.OperandScope {
		mem = in.Args[0].Scope
	} else {
		mem = gvir.Scope(in.Exec) // the memory scope defaults to the execution scope
	}
	if mem != gvir.ScopeNone {
		s, err := amdScope(mem)
		if err != nil {
			return err
		}
		if err := b.flushStores(o); err != nil {
			return err
		}
		o.Fence(amdtx.AcqRel, s)
	}
	if in.Exec == gvir.ExecGroup {
		// V21 holds because §7.4 makes the enclosing conditions group-uniform
		// and guard lowering turns those into %scc guards.
		o.Barrier()
	}
	// barrier.subgroup needs no execution barrier: a wave is synchronized by
	// construction on amdgcn.
	return nil
}

func (b *bodyLowerer) fence(o *amdtx.Body, in *gvir.Instruction) error {
	if len(in.Args) < 2 {
		return fmt.Errorf("fence takes a scope and an ordering")
	}
	ord, ok := amdOrdering(in.Args[1].Ordering)
	if !ok {
		return nil // fence relaxed is a no-op (§10.2), and V38 rejects a bare fence
	}
	s, err := amdScope(in.Args[0].Scope)
	if err != nil {
		return err
	}
	if err := b.flushStores(o); err != nil {
		return err
	}
	o.Fence(ord, s)
	return nil
}

// flushStores waits on the separate store counter. On GFX10 and GFX11 vscnt
// is not part of s_waitcnt and is written as a distinct mnemonic (§12.2).
func (b *bodyLowerer) flushStores(o *amdtx.Body) error {
	if b.l.target.HasVSCnt() {
		o.WaitcntVScnt(0)
	}
	return nil
}

// atomic lowers the §10.2 opcodes. The scope is the final operand and is
// carried by the surrounding fences, not by the instruction: every atomic is
// relaxed.
func (b *bodyLowerer) atomic(o *amdtx.Body, in *gvir.Instruction) error {
	p, err := b.srcValue(o, in.Args[0], gvir.PtrGlobal)
	if err != nil {
		return err
	}
	if p.space != gvir.SpaceGlobal && p.space != gvir.SpaceGroup {
		return fmt.Errorf("atomics are legal on global and group pointers only (§10.2)")
	}
	prefix := "global"
	if p.space == gvir.SpaceGroup {
		prefix = "ds"
	}
	addr, err := b.address(p, p.space)
	if err != nil {
		return err
	}
	elem := gvir.ElemOrSelf(in.Suffix)
	wsfx := "u32"
	if regWidth1(elem) == amdtx.B64 {
		wsfx = "u64"
	}

	switch in.Op {
	case gvir.OpAtomicLoad:
		dst, err := b.define(in.Result, in.Suffix)
		if err != nil {
			return err
		}
		// An atomic load is a load with the cache bypassed; ordering comes
		// from the fences around it.
		o.InstN(prefix+"_load_b"+widthDigits(elem), []amdtx.Operand{dst.regs[0]},
			[]amdtx.Operand{addr}, amdtx.GLC)
		return b.waitFor(o, p.space)

	case gvir.OpAtomicStore:
		src, err := b.srcValue(o, in.Args[1], in.Suffix)
		if err != nil {
			return err
		}
		o.InstN(prefix+"_store_b"+widthDigits(elem), nil,
			[]amdtx.Operand{addr, src.reg(0)}, amdtx.GLC)
		return nil

	case gvir.OpCmpxchg:
		return fmt.Errorf("cmpxchg needs the paired-operand form of global_atomic_cmpswap; unimplemented")
	}

	mn, ok := atomicMnemonic(in.Op)
	if !ok {
		return fmt.Errorf("%s has no atomic mnemonic", in.Op)
	}
	if f := floatBits(elem); f != 0 {
		if in.Op != gvir.OpAtomicAdd {
			return fmt.Errorf("only atomic_add is legal on floats (§10.2)")
		}
		wsfx = "f32"
		if f == 64 {
			wsfx = "f64"
		}
	}
	src, err := b.srcValue(o, in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	full := prefix + "_atomic_" + mn + "_" + wsfx
	if in.Result == "" {
		o.InstN(full, nil, []amdtx.Operand{addr, src.reg(0)})
		return nil
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	// .glc is what makes the atomic return the old value; §8.3 marks the
	// cache-policy modifiers target-specific, and the GFX11 spelling is a todo.
	o.InstN(full, []amdtx.Operand{dst.regs[0]}, []amdtx.Operand{addr, src.reg(0)}, amdtx.GLC)
	return b.waitFor(o, p.space)
}

func atomicMnemonic(op gvir.Opcode) (string, bool) {
	switch op {
	case gvir.OpAtomicAdd:
		return "add", true
	case gvir.OpAtomicSub:
		return "sub", true
	case gvir.OpAtomicAnd:
		return "and", true
	case gvir.OpAtomicOr:
		return "or", true
	case gvir.OpAtomicXor:
		return "xor", true
	case gvir.OpAtomicXchg:
		return "swap", true
	case gvir.OpAtomicUMin:
		return "umin", true
	case gvir.OpAtomicUMax:
		return "umax", true
	case gvir.OpAtomicSMin:
		return "smin", true
	case gvir.OpAtomicSMax:
		return "smax", true
	}
	return "", false
}

func regWidth1(t gvir.Type) amdtx.Width {
	w, _ := regWidth(t)
	return w
}

func widthDigits(t gvir.Type) string {
	if regWidth1(t) == amdtx.B64 {
		return "64"
	}
	return "32"
}

// ---- Collectives ----------------------------------------------------------

func (b *bodyLowerer) collective(o *amdtx.Body, in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpShuffle, gvir.OpShuffleXor, gvir.OpShuffleUp, gvir.OpShuffleDown:
		return b.shuffle(o, in)
	case gvir.OpBroadcast:
		return b.broadcast(o, in)
	case gvir.OpBroadcastFirst:
		return b.broadcastFirst(o, in)
	case gvir.OpBallot:
		return b.ballot(o, in)
	case gvir.OpAny, gvir.OpAll:
		return b.anyAll(o, in)
	case gvir.OpSubAdd, gvir.OpSubMin, gvir.OpSubMax, gvir.OpSubAnd, gvir.OpSubOr, gvir.OpSubXor:
		return b.reduce(o, in)
	}
	return fmt.Errorf("%s is not yet lowered", in.Op)
}

// laneID materializes the calling lane's index with the standard mbcnt pair.
func (b *bodyLowerer) laneID(o *amdtx.Body) (*value, error) {
	v, err := b.temp(gvir.I32)
	if err != nil {
		return nil, err
	}
	o.Inst("v_mbcnt_lo_u32_b32", v.regs[0], amdtx.Imm(-1), amdtx.Imm(0))
	if b.l.wave == amdtx.Wave64 {
		o.Inst("v_mbcnt_hi_u32_b32", v.regs[0], amdtx.Imm(-1), v.regs[0])
	}
	return v, nil
}

// bpermute reads src from the lane named by index. ds_bpermute takes a byte
// address, so the index is scaled by four.
func (b *bodyLowerer) bpermute(o *amdtx.Body, src, index amdtx.Operand) (*value, error) {
	addr, err := b.temp(gvir.I32)
	if err != nil {
		return nil, err
	}
	o.Inst("v_lshlrev_b32", addr.regs[0], amdtx.Imm(2), index)
	dst, err := b.temp(gvir.I32)
	if err != nil {
		return nil, err
	}
	o.Inst("ds_bpermute_b32", dst.regs[0], addr.regs[0], src)
	o.Waitcnt(amdtx.LGKM(0))
	return dst, nil
}

func (b *bodyLowerer) shuffle(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if regWidth1(elem) != amdtx.B32 {
		return fmt.Errorf("%s is implemented for 32-bit element types only; ds_bpermute_b32 moves one "+
			"dword and the 64-bit form needs the pair", in.Op)
	}
	lane, err := b.laneID(o)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for l := 0; l < laneCount(in.Suffix); l++ {
		v, err := b.src(o, in.Args[0], elem, l)
		if err != nil {
			return err
		}
		arg, err := b.src(o, in.Args[1], gvir.I32, 0)
		if err != nil {
			return err
		}
		idx, err := b.temp(gvir.I32)
		if err != nil {
			return err
		}
		switch in.Op {
		case gvir.OpShuffle:
			o.Inst("v_mov_b32", idx.regs[0], arg)
		case gvir.OpShuffleXor:
			o.Inst("v_xor_b32", idx.regs[0], lane.regs[0], arg)
		case gvir.OpShuffleUp:
			o.Inst("v_sub_u32", idx.regs[0], lane.regs[0], arg)
		case gvir.OpShuffleDown:
			o.Inst("v_add_u32", idx.regs[0], lane.regs[0], arg)
		}
		if in.Op == gvir.OpShuffleUp || in.Op == gvir.OpShuffleDown {
			// Shifted-out lanes return the source value (§10.3): an index
			// outside the wave falls back to the lane's own.
			p, err := b.temp(gvir.I1)
			if err != nil {
				return err
			}
			o.Inst("v_cmp_lt_u32", p.regs[0], idx.regs[0], amdtx.Imm(int64(b.l.wave.Bits())))
			o.Inst("v_cndmask_b32", idx.regs[0], lane.regs[0], idx.regs[0], p.regs[0])
		}
		got, err := b.bpermute(o, v, idx.regs[0])
		if err != nil {
			return err
		}
		o.Inst("v_mov_b32", dst.regs[l], got.regs[0])
	}
	return nil
}

// broadcast reads one lane into an SGPR and moves it back out. v_readlane is
// one of the two designated cross-lane readback forms V7 exempts from the
// vector-destination rule, and V22 checks the index against the wave width.
func (b *bodyLowerer) broadcast(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if regWidth1(elem) != amdtx.B32 {
		return fmt.Errorf("broadcast is implemented for 32-bit element types only")
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	idx, err := b.src(o, in.Args[1], gvir.I32, 0)
	if err != nil {
		return err
	}
	for l := 0; l < laneCount(in.Suffix); l++ {
		v, err := b.src(o, in.Args[0], elem, l)
		if err != nil {
			return err
		}
		s := b.regs.New(amdtx.Sgpr(amdtx.B32), fmt.Sprintf("bc$%d", b.ntmp))
		b.ntmp++
		o.Inst("v_readlane_b32", s, v, idx)
		o.Inst("v_mov_b32", dst.regs[l], s)
	}
	return nil
}

func (b *bodyLowerer) broadcastFirst(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if regWidth1(elem) != amdtx.B32 {
		return fmt.Errorf("broadcast_first is implemented for 32-bit element types only")
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for l := 0; l < laneCount(in.Suffix); l++ {
		v, err := b.src(o, in.Args[0], elem, l)
		if err != nil {
			return err
		}
		s := b.regs.New(amdtx.Sgpr(amdtx.B32), fmt.Sprintf("bf$%d", b.ntmp))
		b.ntmp++
		o.Inst("v_readfirstlane_b32", s, v)
		o.Inst("v_mov_b32", dst.regs[l], s)
	}
	return nil
}

// ballot is a mask copy: an i1 already lives in a .lanemask and v_cmp writes
// zero into inactive lanes, which is exactly §10.3's "inactive lanes read as
// false".
func (b *bodyLowerer) ballot(o *amdtx.Body, in *gvir.Instruction) error {
	src, err := b.src(o, in.Args[0], gvir.I1, 0)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, gvir.Submask)
	if err != nil {
		return err
	}
	o.Inst("s_mov_"+b.l.maskSuffix(), dst.regs[0], src)
	return nil
}

func (b *bodyLowerer) anyAll(o *amdtx.Body, in *gvir.Instruction) error {
	src, err := b.src(o, in.Args[0], gvir.I1, 0)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, gvir.I1)
	if err != nil {
		return err
	}
	sfx, cmp := b.l.maskSuffix(), b.l.maskCmpSuffix()
	if in.Op == gvir.OpAny {
		o.Inst("s_cmp_lg_"+cmp, amdtx.SCC, src, amdtx.Imm(0))
	} else {
		t := b.regs.New(amdtx.Lane, fmt.Sprintf("all$%d", b.ntmp))
		b.ntmp++
		o.Inst("s_andn2_"+sfx, t, amdtx.Exec, src)
		o.Inst("s_cmp_eq_"+cmp, amdtx.SCC, t, amdtx.Imm(0))
	}
	o.Inst("s_cselect_"+sfx, dst.regs[0], amdtx.Imm(-1), amdtx.Imm(0))
	return nil
}

// reduce is a butterfly over ds_bpermute. §9.2 guarantees inactivity comes
// only from unpopulated trailing lanes, so the active set is a prefix and
// "partner < active count" is an exact activity test; an inactive partner
// contributes the operation's identity, which keeps the reduction exact.
func (b *bodyLowerer) reduce(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if regWidth1(elem) != amdtx.B32 {
		return fmt.Errorf("the subgroup reductions are implemented for 32-bit element types only")
	}
	mn, ident, err := reduceForm(in.Op, elem)
	if err != nil {
		return err
	}
	lane, err := b.laneID(o)
	if err != nil {
		return err
	}
	count := b.regs.New(amdtx.Sgpr(amdtx.B32), fmt.Sprintf("act$%d", b.ntmp))
	b.ntmp++
	o.Inst("s_bcnt1_i32_"+b.l.maskSuffix(), count, amdtx.Exec)

	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for l := 0; l < laneCount(in.Suffix); l++ {
		v, err := b.src(o, in.Args[0], elem, l)
		if err != nil {
			return err
		}
		acc, err := b.temp(elem)
		if err != nil {
			return err
		}
		o.Inst("v_mov_b32", acc.regs[0], v)
		for step := 1; step < b.l.wave.Bits(); step <<= 1 {
			partner, _ := b.temp(gvir.I32)
			o.Inst("v_xor_b32", partner.regs[0], acc0(lane), amdtx.Imm(int64(step)))
			got, err := b.bpermute(o, acc.regs[0], partner.regs[0])
			if err != nil {
				return err
			}
			p, _ := b.temp(gvir.I1)
			o.Inst("v_cmp_lt_u32", p.regs[0], partner.regs[0], count)
			o.Inst("v_cndmask_b32", got.regs[0], ident, got.regs[0], p.regs[0])
			o.Inst(mn, acc.regs[0], acc.regs[0], got.regs[0])
		}
		o.Inst("v_mov_b32", dst.regs[l], acc.regs[0])
	}
	return nil
}

func acc0(v *value) amdtx.Operand { return v.regs[0] }

func reduceForm(op gvir.Opcode, elem gvir.Type) (string, amdtx.Operand, error) {
	isFloat := floatBits(elem) != 0
	switch op {
	case gvir.OpSubAdd:
		if isFloat {
			// §11.3 keeps each addition IEEE; only the order is unspecified,
			// which a butterfly satisfies.
			return "v_add_f32", amdtx.FImm(0), nil
		}
		return "v_add_u32", amdtx.Imm(0), nil
	case gvir.OpSubMin:
		if isFloat {
			return "v_min_f32", amdtx.FImm(0), fmt.Errorf("float reductions need +Inf as the identity; unimplemented")
		}
		return "v_min_i32", amdtx.Imm(int64(int32(1<<31 - 1))), nil
	case gvir.OpSubMax:
		if isFloat {
			return "v_max_f32", amdtx.FImm(0), fmt.Errorf("float reductions need -Inf as the identity; unimplemented")
		}
		return "v_max_i32", amdtx.Imm(int64(int32(-1 << 31))), nil
	case gvir.OpSubAnd:
		return "v_and_b32", amdtx.Imm(-1), nil
	case gvir.OpSubOr:
		return "v_or_b32", amdtx.Imm(0), nil
	case gvir.OpSubXor:
		return "v_xor_b32", amdtx.Imm(0), nil
	}
	return "", nil, fmt.Errorf("%s is not a reduction", op)
}

// maskOp lowers the submask queries. A submask lives in a .lanemask, so these
// are scalar opcodes writing SGPRs, moved back into the value's VGPR.
func (b *bodyLowerer) maskOp(o *amdtx.Body, in *gvir.Instruction) error {
	m, err := b.src(o, in.Args[0], gvir.Submask, 0)
	if err != nil {
		return err
	}
	sfx, cmp := b.l.maskSuffix(), b.l.maskCmpSuffix()
	switch in.Op {
	case gvir.OpMaskCount, gvir.OpMaskFirst:
		dst, err := b.define(in.Result, gvir.I32)
		if err != nil {
			return err
		}
		s := b.regs.New(amdtx.Sgpr(amdtx.B32), fmt.Sprintf("mq$%d", b.ntmp))
		b.ntmp++
		mn := "s_bcnt1_i32_" + sfx
		if in.Op == gvir.OpMaskFirst {
			mn = "s_ff1_i32_" + sfx
		}
		o.Inst(mn, s, m)
		o.Inst("v_mov_b32", dst.regs[0], s)
		return nil

	case gvir.OpMaskEmpty:
		dst, err := b.define(in.Result, gvir.I1)
		if err != nil {
			return err
		}
		o.Inst("s_cmp_eq_"+cmp, amdtx.SCC, m, amdtx.Imm(0))
		o.Inst("s_cselect_"+sfx, dst.regs[0], amdtx.Imm(-1), amdtx.Imm(0))
		return nil

	case gvir.OpMaskTest:
		lane, err := b.src(o, in.Args[1], gvir.I32, 0)
		if err != nil {
			return err
		}
		dst, err := b.define(in.Result, gvir.I1)
		if err != nil {
			return err
		}
		sh, _ := b.temp(gvir.I64)
		bit, _ := b.temp(gvir.I32)
		o.Inst("v_lshrrev_b64", sh.regs[0], lane, m)
		o.Inst("v_and_b32", bit.regs[0], amdtx.Imm(1), sh.regs[0].Dword(0))
		o.Inst("v_cmp_ne_u32", dst.regs[0], bit.regs[0], amdtx.Imm(0))
		return nil
	}
	return fmt.Errorf("%s is not a submask query", in.Op)
}

// ---- Execution builtins ---------------------------------------------------

// builtin lowers §9. The unsuffixed positional and extent forms are i64 and
// use the normative linearization; mul_lo/mul_hi give an exact 64-bit product
// from two u32 factors, so nothing is computed at 32 bits and widened.
func (b *bodyLowerer) builtin(o *amdtx.Body, in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpThreadInGroup:
		return b.dimBuiltin(o, in, []amdtx.Operand{amdtx.TidX, amdtx.TidY, amdtx.TidZ})
	case gvir.OpGroupInGrid:
		return b.dimBuiltin(o, in, []amdtx.Operand{amdtx.WgIdX, amdtx.WgIdY, amdtx.WgIdZ})
	case gvir.OpThreadsPerGroup:
		return b.groupExtent(o, in)
	case gvir.OpThreadsPerSubgroup:
		return b.constantBuiltin(o, in, int64(b.l.wave.Bits()), gvir.I32)
	case gvir.OpThreadInSubgroup:
		v, err := b.laneID(o)
		if err != nil {
			return err
		}
		dst, err := b.define(in.Result, gvir.I64)
		if err != nil {
			return err
		}
		o.Inst("v_mov_b32", dst.regs[0].Dword(0), v.regs[0])
		o.Inst("v_mov_b32", dst.regs[0].Dword(1), amdtx.Imm(0))
		return nil
	case gvir.OpThreadInGrid:
		return b.threadInGrid(o, in)
	case gvir.OpDynamicGroupSize:
		return fmt.Errorf("dynamic_group_size lives in the code-object V5 implicit argument block, " +
			"whose layout AMDTX §11.3 sources from LLVM and reproduces nowhere; reading it needs that table")
	case gvir.OpGroupsPerGrid, gvir.OpThreadsPerGrid, gvir.OpSubgroupsPerGroup, gvir.OpSubgroupInGroup:
		return b.derivedExtent(o, in)
	}
	return fmt.Errorf("%s is not yet lowered", in.Op)
}

func (b *bodyLowerer) dimBuiltin(o *amdtx.Body, in *gvir.Instruction, regs []amdtx.Operand) error {
	if in.Dim != gvir.DimNone {
		dst, err := b.define(in.Result, gvir.I32)
		if err != nil {
			return err
		}
		o.Inst("v_mov_b32", dst.regs[0], regs[dimIndex(in.Dim)])
		return nil
	}
	// px + py*ex + pz*ex*ey, computed in i64 (§9 "Linearization").
	return fmt.Errorf("the unsuffixed linearized form of %s needs the extent operands; use the "+
		"dimension-suffixed form until the extent builtins are complete", in.Op)
}

// groupExtent answers threads_per_group from the declared shape when there is
// one, and from the AQL dispatch packet otherwise.
func (b *bodyLowerer) groupExtent(o *amdtx.Body, in *gvir.Instruction) error {
	if in.Dim == gvir.DimNone {
		if !b.wgConstOK {
			return fmt.Errorf("the linearized threads_per_group needs all three components; declare " +
				"group_size or use the dimension-suffixed form")
		}
		n := b.wgConst[0] * b.wgConst[1] * b.wgConst[2]
		return b.constantBuiltin(o, in, int64(n), gvir.I64)
	}
	if b.wgConstOK {
		return b.constantBuiltin(o, in, int64(b.wgConst[dimIndex(in.Dim)]), gvir.I32)
	}
	// The AQL dispatch packet holds workgroup_size_{x,y,z} as three u16 at
	// offsets 4, 6 and 8.
	off := int64(4 + 2*dimIndex(in.Dim))
	s := b.regs.New(amdtx.Sgpr(amdtx.B32), fmt.Sprintf("wg$%d", b.ntmp))
	b.ntmp++
	o.SLoad(s, amdtx.At(amdtx.DispatchPtr, off&^3))
	o.Waitcnt(amdtx.LGKM(0))
	dst, err := b.define(in.Result, gvir.I32)
	if err != nil {
		return err
	}
	shift := amdtx.Imm(16 * (off & 3) / 2)
	o.Inst("v_lshrrev_b32", dst.regs[0], shift, s)
	o.Inst("v_and_b32", dst.regs[0], amdtx.Imm(0xffff), dst.regs[0])
	return nil
}

func (b *bodyLowerer) threadInGrid(o *amdtx.Body, in *gvir.Instruction) error {
	if in.Dim == gvir.DimNone {
		return fmt.Errorf("the linearized thread_in_grid needs every extent; use the " +
			"dimension-suffixed form until the extent builtins are complete")
	}
	if !b.wgConstOK {
		return fmt.Errorf("thread_in_grid needs the group extent; declare group_size or wait for the " +
			"dispatch-packet path to cover the multiply")
	}
	d := dimIndex(in.Dim)
	dst, err := b.define(in.Result, gvir.I32)
	if err != nil {
		return err
	}
	wg := []amdtx.Operand{amdtx.WgIdX, amdtx.WgIdY, amdtx.WgIdZ}[d]
	tid := []amdtx.Operand{amdtx.TidX, amdtx.TidY, amdtx.TidZ}[d]
	s := b.regs.New(amdtx.Sgpr(amdtx.B32), fmt.Sprintf("base$%d", b.ntmp))
	b.ntmp++
	o.Inst("s_mul_i32", s, wg, amdtx.Imm(int64(b.wgConst[d])))
	o.Inst("v_add_u32", dst.regs[0], s, tid)
	return nil
}

func (b *bodyLowerer) derivedExtent(o *amdtx.Body, in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpSubgroupsPerGroup:
		if !b.wgConstOK {
			return fmt.Errorf("subgroups_per_group needs the group extent; declare group_size")
		}
		n := b.wgConst[0] * b.wgConst[1] * b.wgConst[2]
		w := b.l.wave.Bits()
		return b.constantBuiltin(o, in, int64((n+w-1)/w), gvir.I32)
	case gvir.OpSubgroupInGroup:
		if !b.wgConstOK {
			return fmt.Errorf("subgroup_in_group needs the group extent; declare group_size")
		}
		return fmt.Errorf("subgroup_in_group needs the linearized thread_in_group; unimplemented")
	}
	return fmt.Errorf("%s needs a division by the group extent; it is a shift only when group_size "+
		"is a declared power of two, and the general case is unimplemented", in.Op)
}

func (b *bodyLowerer) constantBuiltin(o *amdtx.Body, in *gvir.Instruction, n int64, t gvir.Type) error {
	dst, err := b.define(in.Result, t)
	if err != nil {
		return err
	}
	o.Inst("v_mov_b32", dword(dst.regs[0], 0, dwordsOf(t)), amdtx.Imm(n))
	if dwordsOf(t) == 2 {
		o.Inst("v_mov_b32", dst.regs[0].Dword(1), amdtx.Imm(n>>32))
	}
	return nil
}

func dimIndex(d gvir.Dim) int {
	switch d {
	case gvir.DimY:
		return 1
	case gvir.DimZ:
		return 2
	}
	return 0
}