package msl

// MemFlags mirrors mem_flags for barrier operations.
type MemFlags string

// Barrier memory flags.
const (
	MemNone                  MemFlags = "mem_flags::mem_none"
	MemDevice                MemFlags = "mem_flags::mem_device"
	MemThreadgroup           MemFlags = "mem_flags::mem_threadgroup"
	MemTexture               MemFlags = "mem_flags::mem_texture"
	MemThreadgroupImageblock MemFlags = "mem_flags::mem_threadgroup_imageblock"
	MemObjectData            MemFlags = "mem_flags::mem_object_data"
)

// MemoryOrder mirrors the C++ memory orders MSL atomics accept. MSL
// supports only relaxed ordering.
type MemoryOrder string

// Relaxed is memory_order_relaxed, the only ordering MSL supports.
const Relaxed MemoryOrder = "memory_order_relaxed"

// ThreadgroupBarrier emits threadgroup_barrier(flags);.
func (cb *CodeBuilder) ThreadgroupBarrier(f MemFlags) *CodeBuilder {
	return cb.Expr(CallExpr("threadgroup_barrier", Ident(f)))
}

// SIMDGroupBarrier emits simdgroup_barrier(flags);.
func (cb *CodeBuilder) SIMDGroupBarrier(f MemFlags) *CodeBuilder {
	return cb.Expr(CallExpr("simdgroup_barrier", Ident(f)))
}

// SIMD-group expression helpers.
func SimdShuffle(x, lane any) Expr     { return CallExpr("simd_shuffle", x, lane) }
func SimdShuffleXor(x, mask any) Expr  { return CallExpr("simd_shuffle_xor", x, mask) }
func SimdShuffleUp(x, delta any) Expr  { return CallExpr("simd_shuffle_up", x, delta) }
func SimdShuffleDown(x, delta any) Expr { return CallExpr("simd_shuffle_down", x, delta) }
func SimdBroadcast(x, lane any) Expr   { return CallExpr("simd_broadcast", x, lane) }
func SimdSum(x any) Expr               { return CallExpr("simd_sum", x) }
func SimdProduct(x any) Expr           { return CallExpr("simd_product", x) }
func SimdMax(x any) Expr               { return CallExpr("simd_max", x) }
func SimdMin(x any) Expr               { return CallExpr("simd_min", x) }
func SimdBallot(p any) Expr            { return CallExpr("simd_ballot", p) }
func SimdAll(p any) Expr               { return CallExpr("simd_all", p) }
func SimdAny(p any) Expr               { return CallExpr("simd_any", p) }

func ordExpr(ords []MemoryOrder, i int) Expr {
	if i < len(ords) {
		return Ident(ords[i])
	}
	return Ident(Relaxed)
}

// Atomic expression helpers over atomic_*_explicit. Orders default to
// Relaxed when omitted (the only order MSL supports anyway).
func AtomicLoad(p any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_load_explicit", p, ordExpr(ords, 0))
}
func AtomicStore(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_store_explicit", p, v, ordExpr(ords, 0))
}
func AtomicExchange(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_exchange_explicit", p, v, ordExpr(ords, 0))
}
func AtomicFetchAdd(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_fetch_add_explicit", p, v, ordExpr(ords, 0))
}
func AtomicFetchSub(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_fetch_sub_explicit", p, v, ordExpr(ords, 0))
}
func AtomicFetchAnd(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_fetch_and_explicit", p, v, ordExpr(ords, 0))
}
func AtomicFetchOr(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_fetch_or_explicit", p, v, ordExpr(ords, 0))
}
func AtomicFetchXor(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_fetch_xor_explicit", p, v, ordExpr(ords, 0))
}
func AtomicFetchMin(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_fetch_min_explicit", p, v, ordExpr(ords, 0))
}
func AtomicFetchMax(p, v any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_fetch_max_explicit", p, v, ordExpr(ords, 0))
}

// AtomicCompareExchangeWeak builds
// atomic_compare_exchange_weak_explicit(p, expected, desired, succ, fail).
// expected must be a pointer expression (use AddrOf on a local).
func AtomicCompareExchangeWeak(p, expected, desired any, ords ...MemoryOrder) Expr {
	return CallExpr("atomic_compare_exchange_weak_explicit",
		p, expected, desired, ordExpr(ords, 0), ordExpr(ords, 1))
}

// Texture and sampler helpers.

// Sample builds tex.sample(smp, uv).
func Sample(tex, smp, uv any) Expr { return Method(tex, "sample", smp, uv) }

// TexRead builds tex.read(coord).
func TexRead(tex, coord any) Expr { return Method(tex, "read", coord) }

// TexWrite emits tex.write(val, coord);.
func (cb *CodeBuilder) TexWrite(tex, val, coord any) *CodeBuilder {
	return cb.Expr(Method(tex, "write", val, coord))
}