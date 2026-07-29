// isel_sync.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Barriers, fences, atomics and the subgroup collectives.
//
// .gvir atomics are relaxed and express ordering only through `fence` (§10.2),
// which is exactly Metal's model: memory_order_relaxed is the only order the
// atomic functions accept. The §10.2 scope operand is carried by the pointer's
// address space rather than by an argument, because that is where Metal keeps
// it — a threadgroup atomic is threadgroup-scoped by construction.

func (f *fnLower) sync(b *msl.Block, in *gvir.Instruction) error {
	switch {
	case in.Op == gvir.OpBarrier:
		return f.barrier(b, in)
	case in.Op == gvir.OpFence:
		return f.fence(b, in)
	case in.Op == gvir.OpCmpxchg:
		return f.cmpxchg(b, in)
	case in.Op.IsAtomic():
		return f.atomic(b, in)
	case in.Op.IsCollective():
		return f.collective(b, in)
	case isMaskOp(in.Op):
		return f.mask(b, in)
	}
	return fmt.Errorf("%s is not a synchronization opcode", in.Op)
}

// --- barriers (§10.1) -------------------------------------------------------

func memFlags(s gvir.Scope) msl.Expr {
	switch s {
	case gvir.ScopeNone:
		return msl.Raw("mem_flags::mem_none")
	case gvir.ScopeGrid:
		return msl.Raw("mem_flags::mem_device | mem_flags::mem_threadgroup")
	default:
		return msl.Raw("mem_flags::mem_threadgroup")
	}
}

func (f *fnLower) barrier(b *msl.Block, in *gvir.Instruction) error {
	mem := gvir.Scope(in.Exec) // the memory scope defaults to the execution scope
	if len(in.Args) > 0 && in.Args[0].Kind == gvir.OperandScope {
		mem = in.Args[0].Scope
	}
	switch in.Exec {
	case gvir.ExecGroup:
		b.Do(msl.Call("threadgroup_barrier", memFlags(mem)))
	case gvir.ExecSubgroup:
		b.Do(msl.Call("simdgroup_barrier", memFlags(mem)))
	default:
		return fmt.Errorf("barrier has no execution scope (§10.1)")
	}
	return nil
}

func (f *fnLower) fence(b *msl.Block, in *gvir.Instruction) error {
	if len(in.Args) < 2 {
		return fmt.Errorf("fence takes a scope and an ordering (§10.2)")
	}
	scope, ordering := in.Args[0].Scope, in.Args[1].Ordering
	if ordering == gvir.OrderRelaxed {
		return nil // §10.2: a no-op
	}
	if !f.l.ver.GTE(msl.Metal31) {
		return fmt.Errorf("fence %s needs atomic_thread_fence, which this backend requires %s for (module targets %s)",
			ordering, msl.Metal31.Std(), f.l.ver.Std())
	}
	// Metal's fence takes seq_cst only; it is stronger than acquire, release
	// or acqrel, and a stronger fence conforms.
	b.Do(msl.Call("atomic_thread_fence", memFlags(scope), msl.Raw("memory_order_seq_cst")))
	return nil
}

// --- atomics (§10.2) --------------------------------------------------------

// atomicType picks the MSL atomic object type for a suffix and says whether
// the operation must be read through the unsigned twin.
func (f *fnLower) atomicType(op gvir.Opcode, suffix gvir.Type) (msl.Type, msl.Type, bool, error) {
	if gvir.IsPtr(suffix) {
		return nil, nil, false, fmt.Errorf("atomics on pointer values have no MSL spelling (see todos)")
	}
	base, err := f.l.mapType(suffix)
	if err != nil {
		return nil, nil, false, err
	}
	unsigned := false
	switch op {
	case gvir.OpAtomicUMin, gvir.OpAtomicUMax:
		unsigned = true
	}
	val := base
	if unsigned {
		u, ok := unsignedTwin(base)
		if !ok {
			return nil, nil, false, fmt.Errorf("%s has no unsigned twin", base)
		}
		val = u
	}
	return msl.Atomic(val), val, unsigned, nil
}

var atomicFn = map[gvir.Opcode]string{
	gvir.OpAtomicLoad:  "atomic_load_explicit",
	gvir.OpAtomicStore: "atomic_store_explicit",
	gvir.OpAtomicAdd:   "atomic_fetch_add_explicit",
	gvir.OpAtomicSub:   "atomic_fetch_sub_explicit",
	gvir.OpAtomicAnd:   "atomic_fetch_and_explicit",
	gvir.OpAtomicOr:    "atomic_fetch_or_explicit",
	gvir.OpAtomicXor:   "atomic_fetch_xor_explicit",
	gvir.OpAtomicXchg:  "atomic_exchange_explicit",
	gvir.OpAtomicUMin:  "atomic_fetch_min_explicit",
	gvir.OpAtomicUMax:  "atomic_fetch_max_explicit",
	gvir.OpAtomicSMin:  "atomic_fetch_min_explicit",
	gvir.OpAtomicSMax:  "atomic_fetch_max_explicit",
}

const relaxed = "memory_order_relaxed"

func (f *fnLower) atomic(b *msl.Block, in *gvir.Instruction) error {
	fn, ok := atomicFn[in.Op]
	if !ok {
		return fmt.Errorf("%s has no atomic spelling", in.Op)
	}
	p, err := f.ptrOperand(in.Args[0])
	if err != nil {
		return err
	}
	if err := f.checkAtomicScope(in, p); err != nil {
		return err
	}
	at, valT, unsigned, err := f.atomicType(in.Op, in.Suffix)
	if err != nil {
		return err
	}
	obj := at.(msl.Type)
	object := msl.TCall("vv_at", []msl.TypeArg{msl.TArg(obj)}, p.ref)

	base, err := f.l.mapType(in.Suffix)
	if err != nil {
		return err
	}

	if in.Op == gvir.OpAtomicLoad {
		e := msl.Call(fn, object, msl.Raw(relaxed))
		if unsigned {
			e = reinterpret(base, e)
		}
		return f.assign(b, in, e)
	}

	v, err := f.operand(in.Args[1], base)
	if err != nil {
		return err
	}
	if unsigned {
		v = reinterpret(valT, v)
	}
	if in.Op == gvir.OpAtomicStore {
		b.Do(msl.Call(fn, object, v, msl.Raw(relaxed)))
		return nil
	}
	e := msl.Call(fn, object, v, msl.Raw(relaxed))
	if unsigned {
		e = reinterpret(base, e)
	}
	return f.assign(b, in, e)
}

// checkAtomicScope rejects the one combination Metal cannot honour: a
// grid-scoped atomic through a threadgroup pointer. Everything else is either
// exact or strictly stronger than the operand asks for.
func (f *fnLower) checkAtomicScope(in *gvir.Instruction, p *binding) error {
	var scope gvir.Scope
	for _, a := range in.Args {
		if a.Kind == gvir.OperandScope {
			scope = a.Scope
		}
	}
	space := p.gtyp.(gvir.PtrType).Space
	if scope == gvir.ScopeGrid && space == gvir.SpaceGroup {
		return fmt.Errorf("grid-scoped atomic through ptr[group]: threadgroup memory is not visible at grid scope")
	}
	if space == gvir.SpaceConstant || space == gvir.SpacePrivate {
		return fmt.Errorf("atomics are illegal on %s pointers (§10.2)", space)
	}
	return nil
}

// cmpxchg yields the old value, not a success flag (§10.2), and does not fail
// spuriously. Metal's weak form does both of the opposite things, so the loop
// retries only a spurious failure and the old value is read out of the
// expected slot the call updates.
func (f *fnLower) cmpxchg(b *msl.Block, in *gvir.Instruction) error {
	p, err := f.ptrOperand(in.Args[0])
	if err != nil {
		return err
	}
	if err := f.checkAtomicScope(in, p); err != nil {
		return err
	}
	base, err := f.l.mapType(in.Suffix)
	if err != nil {
		return err
	}
	expected, err := f.operand(in.Args[1], base)
	if err != nil {
		return err
	}
	desired, err := f.operand(in.Args[2], base)
	if err != nil {
		return err
	}
	bd, err := f.define(in)
	if err != nil {
		return err
	}
	object := msl.TCall("vv_at", []msl.TypeArg{msl.TArg(msl.Atomic(base))}, p.ref)
	slot := f.temp("cas")

	var inner error
	b.Scope(func(sb *msl.Block) {
		cur := sb.Let(base, slot, expected)
		call := msl.Call("atomic_compare_exchange_weak_explicit",
			object, cur.Addr(), desired, msl.Raw(relaxed), msl.Raw(relaxed))
		sb.While(call.Not().And(cur.Eq(expected)), func(wb *msl.Block) {
			wb.Assign(cur, expected)
		})
		sb.Assign(bd.ref, cur)
	})
	return inner
}

// --- subgroup collectives (§10.3) -------------------------------------------

var simdFn = map[gvir.Opcode]string{
	gvir.OpShuffle:        "simd_shuffle",
	gvir.OpShuffleXor:     "simd_shuffle_xor",
	gvir.OpShuffleUp:      "simd_shuffle_up",
	gvir.OpShuffleDown:    "simd_shuffle_down",
	gvir.OpBroadcast:      "simd_broadcast",
	gvir.OpBroadcastFirst: "simd_broadcast_first",
	gvir.OpAny:            "simd_any",
	gvir.OpAll:            "simd_all",
	gvir.OpSubAdd:         "simd_sum",
	gvir.OpSubMin:         "simd_min",
	gvir.OpSubMax:         "simd_max",
	gvir.OpSubAnd:         "simd_and",
	gvir.OpSubOr:          "simd_or",
	gvir.OpSubXor:         "simd_xor",
}

func (f *fnLower) collective(b *msl.Block, in *gvir.Instruction) error {
	if in.Op == gvir.OpBallot {
		c, err := f.operand(in.Args[0], msl.Bool)
		if err != nil {
			return err
		}
		// simd_vote converts explicitly to its 64-bit vote_t, which is the
		// widest an opaque submask can be (§4.6).
		return f.assign(b, in, msl.Cast(msl.ULong, msl.Call("simd_ballot", c)))
	}

	fn, ok := simdFn[in.Op]
	if !ok {
		return fmt.Errorf("%s has no MSL collective", in.Op)
	}
	if in.Op == gvir.OpAny || in.Op == gvir.OpAll {
		c, err := f.operand(in.Args[0], msl.Bool)
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Call(fn, c))
	}

	t, err := f.l.mapType(in.Suffix)
	if err != nil {
		return err
	}
	v, err := f.operand(in.Args[0], t)
	if err != nil {
		return err
	}
	if in.Op.Arity() == 1 {
		return f.assign(b, in, msl.Call(fn, v))
	}
	lane, err := f.operand(in.Args[1], msl.UShort)
	if err != nil {
		return err
	}
	e := msl.Call(fn, v, lane)

	// §10.3 pins shifted-out lanes to the source value; Metal leaves the
	// result unspecified when the source lane does not exist, so the guard is
	// part of the lowering.
	switch in.Op {
	case gvir.OpShuffleUp:
		me := f.builtinParam(msl.ThreadIndexInSIMDGroup, "lane", msl.UShort)
		e = msl.Cond(me.Ge(lane), e, v)
	case gvir.OpShuffleDown:
		me := f.builtinParam(msl.ThreadIndexInSIMDGroup, "lane", msl.UShort)
		width := f.builtinParam(msl.ThreadsPerSIMDGroup, "simdwidth", msl.UShort)
		e = msl.Cond(me.Add(lane).Lt(width), e, v)
	}
	return f.assign(b, in, e)
}

// --- submask operations (§10.3) ---------------------------------------------

// Metal has no lane-mask special registers, so the mask_* constants are
// computed from the lane index. submask is a ulong, so the arithmetic is
// exact at every subgroup width Metal reports.
func (f *fnLower) mask(b *msl.Block, in *gvir.Instruction) error {
	one := msl.Cast(msl.ULong, msl.I(1))
	zero := msl.Cast(msl.ULong, msl.I(0))

	switch in.Op {
	case gvir.OpMaskLt, gvir.OpMaskLe, gvir.OpMaskGt, gvir.OpMaskGe, gvir.OpMaskEq:
		lane := msl.Cast(msl.ULong, f.builtinParam(msl.ThreadIndexInSIMDGroup, "lane", msl.UShort))
		bit := one.Shl(lane)
		switch in.Op {
		case gvir.OpMaskEq:
			return f.assign(b, in, bit)
		case gvir.OpMaskLt:
			return f.assign(b, in, bit.Sub(one))
		case gvir.OpMaskLe:
			return f.assign(b, in, bit.Shl(one).Sub(one))
		case gvir.OpMaskGt:
			return f.assign(b, in, bit.Shl(one).Sub(one).BitNot())
		}
		return f.assign(b, in, bit.Sub(one).BitNot())
	}

	m, err := f.operand(in.Args[0], msl.ULong)
	if err != nil {
		return err
	}
	switch in.Op {
	case gvir.OpMaskCount:
		return f.assign(b, in, msl.Cast(msl.Int, msl.Call("popcount", m)))
	case gvir.OpMaskFirst:
		return f.assign(b, in, msl.Cast(msl.Int, msl.Call("ctz", m)))
	case gvir.OpMaskEmpty:
		return f.assign(b, in, m.Eq(zero))
	case gvir.OpMaskTest:
		lane, err := f.operand(in.Args[1], msl.UShort)
		if err != nil {
			return err
		}
		return f.assign(b, in, m.Shr(msl.Cast(msl.ULong, lane)).BitAnd(one).Eq(one))
	}
	return fmt.Errorf("%s is not a submask opcode", in.Op)
}