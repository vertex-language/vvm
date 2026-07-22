# mem_arch.md — Memory Management, Bedrock Up

## Overview

There is exactly one operation that talks to the allocator: `mem_alloc` / `mem_free`
(see `foundation.vir`). Every "memory management strategy" — GC, RAII, refcounting,
arenas — is not a different way of *getting* memory. It's a different policy for
**when `mem_free` runs** and **how many times `mem_alloc` gets called**. All of them
compile down to the same two extern-backed calls; nothing above the foundation layer
ever touches a syscall or a libc/kernel32 symbol directly.

Ownership checking (borrow checking, `unique_ptr`'s no-copy rule) is not a fifth
entry in that list. It's a compile-time legality gate — it decides whether code is
*allowed to exist*, not when memory is freed. It runs before codegen and leaves zero
runtime footprint. See §5.

---

## 0. The One Primitive

```vir
p = call mem_alloc, size    // ptr, or traps/returns null per foundation's contract
call mem_free, p            // the only way memory ever returns to the system
```

Everything below is pure-compute `.vir` — no `target-decl`, no `link`, no `extern` —
built entirely from these two calls plus core opcodes (`field.ptr`, `index.ptr`,
atomics). It links against whichever `foundation.vir` the build supplies and never
knows if it's on Linux, Windows, or Darwin.

---

## 1. Manual — the baseline, no policy layer

The programmer calls `mem_alloc`/`mem_free` directly and is solely responsible for
matching them. Every other strategy is this, plus an automated rule for *when*
`mem_free` gets called.

```vir
p = call mem_alloc, 16
// ... use p ...
call mem_free, p
```

**Decides when free runs:** the programmer, by hand.
**Cost added:** none — this *is* the primitive.

---

## 2. RAII — compile-time inserted, scope-bound

The frontend inserts `mem_free` at every exit edge of the owning scope: normal fall-
through, early `return`, and unwind paths. This is mechanical because §1.3 rule 2
guarantees every block ends in exactly one terminator — "every exit path" is a
closed, enumerable set the frontend can walk.

```vir
fn scoped_use(cond i1) i64 :
    p = call mem_alloc, 16
    br_if cond, early_out, normal
early_out:
    call mem_free, p        // inserted at this exit too
    return -1
normal:
    v = load.i64 p
    call mem_free, p        // inserted here
    return v
end
```

**Decides when free runs:** the compiler, at a statically known point (scope exit).
**Ownership:** single, non-shared.
**Cost added:** none beyond the call itself — deterministic, no bookkeeping.

---

## 3. Refcounted — compile-time-*inserted* calls, runtime-*decided* free

Retain/release calls are inserted by the compiler (at copy, at scope exit) — same
mechanical insertion as RAII. But *whether* `mem_free` actually fires is a runtime
decision: the atomic decrement hitting zero. This is the real distinction from RAII
— RAII's free point is known at compile time; refcounting's is not. Object lifetime
routinely outlives any single scope.

```vir
struct RcBox(refcount i64, value i64)

export fn rc_retain(p ptr) ptr :
    rc = field.ptr p, RcBox, refcount
    old = atomic_add.i64 rc, 1, relaxed
    return p
end

export fn rc_release(p ptr) void :
    rc = field.ptr p, RcBox, refcount
    old = atomic_sub.i64 rc, 1, acqrel
    was_last = eq.i64 old, 1
    br_if was_last, do_free, done
do_free:
    call mem_free, p
    br done
done:
    return
end
```

**Decides when free runs:** a runtime counter, checked on every release.
**Cost added:** an atomic op per share and per scope exit — real, but bounded and
local; no global pass.

---

## 4. Arena / Pool — one bulk `mem_alloc`, bump-pointer sub-allocation

A single large slab is acquired once. Individual "allocations" inside it are pure
pointer arithmetic (`index.ptr`) — no calls to the allocator at all. Individual
objects are never freed one at a time; the whole slab goes away in one `mem_free`,
or is reset and reused.

```vir
struct Arena(base ptr, size i64, offset i64)

export fn arena_new(size i64) ptr :
    mem = call mem_alloc, size
    a   = call mem_alloc, 24
    bf  = field.ptr a, Arena, base
    store.ptr bf, mem
    sf  = field.ptr a, Arena, size
    store.i64 sf, size
    of  = field.ptr a, Arena, offset
    store.i64 of, 0
    return a
end

export fn arena_alloc(a ptr, size i64) ptr :
    of   = field.ptr a, Arena, offset
    cur  = load.i64 of
    bf   = field.ptr a, Arena, base
    base = load.ptr bf
    p    = index.ptr base, i8, cur     // no allocator call — pure arithmetic
    next = add.i64 cur, size
    store.i64 of, next
    return p
end

export fn arena_reset(a ptr) void :
    of = field.ptr a, Arena, offset
    store.i64 of, 0                    // reclaim everything, one instruction
    return
end

export fn arena_destroy(a ptr) void :
    bf   = field.ptr a, Arena, base
    base = load.ptr bf
    call mem_free, base
    call mem_free, a
    return
end
```

**Decides when free runs:** the programmer, in bulk, for the whole region at once.
**Ratio:** N objects, 1 `mem_alloc`, 1 `mem_free`.
**Cost added:** none per object — the win here is precisely that per-object cost
disappears.

---

## 5. GC Tracing — few slabs, self-managed, deferred + batched

Structurally this is an arena that also decides *reclamation* for you: it front-
loads one (or a small, growable set of) slab(s) via `mem_alloc`, sub-allocates by
bumping within them exactly like §4, but instead of the programmer resetting the
whole thing, a tracing pass walks a root set and decides what's still reachable.
Reclamation is deferred (doesn't happen at the point of last use) and batched
(happens for many dead objects at once, not one call per object).

```vir
struct Slab(base ptr, cap i64, offset i64)

export fn gc_alloc(slab ptr, size i64) ptr :
    of   = field.ptr slab, Slab, offset
    cur  = load.i64 of
    cf   = field.ptr slab, Slab, cap
    cap  = load.i64 cf
    remaining = sub.i64 cap, cur
    fits = sle.i64 size, remaining
    br_if fits, alloc_here, grow
alloc_here:
    bf   = field.ptr slab, Slab, base
    base = load.ptr bf
    p    = index.ptr base, i8, cur
    nxt  = add.i64 cur, size
    store.i64 of, nxt
    return p
grow:
    // acquire another mem_alloc'd slab, link it, retry — elided
    trap
end

// mark/sweep over the root set walks reachable RcBox-shaped nodes, compacting or
// tracking liveness within the slab; mem_free is only called when a whole slab is
// torn down, never per object.
```

**Decides when free runs:** a tracing pass, on its own schedule — not tied to scope
or a counter.
**Ratio:** a handful of `mem_alloc` calls for the lifetime of the program; `mem_free`
calls are rare and batched.
**Cost added:** the trace/mark/sweep pass itself — paid periodically, not per
allocation.

---

## 6. Ownership checking (borrow, `unique_ptr`) — not a strategy, a gate

Both the Rust borrow checker and `unique_ptr`'s deleted copy-constructor do the same
*kind* of thing: a compile-time aliasing check that rejects programs, contributing
no instructions to the output. Neither one, by itself, ever calls `mem_alloc` or
`mem_free`.

- The **borrow checker** is fully erased — it enforces shared-XOR-mutable and
  lifetime validity, then disappears. Rust's actual runtime memory strategy is
  `Drop`, which is §2 (RAII) — the borrow checker is what makes RAII *safe* to rely
  on (no dangling, no aliased mutation), not what performs it.
- **`unique_ptr`**'s no-copy check is the same category of thing, and it's likewise
  paired with §2 — the destructor-inserted `delete` is what actually frees memory;
  the check just prevents two owners from both trying to.

So "ownership" sits orthogonal to the table below. It's a verification layer that
can be bolted onto §1–§4 (most often §2) to catch misuse before the program runs —
it is never itself a fifth way to acquire or reclaim memory.

---

## Summary

| Strategy | Decides *when* free runs | alloc : free ratio | Runtime cost added |
|---|---|---|---|
| Manual | programmer | 1 : 1 | none |
| RAII | compiler, at scope exit | 1 : 1 | none — deterministic point |
| Refcounted | runtime counter hitting zero | 1 alloc, N retain/release, 1 free | atomic op per share/scope |
| Arena/Pool | programmer, in bulk | N objects : 1 alloc, 1 free | none per object |
| GC Tracing | tracing pass, deferred | few allocs, batched frees | periodic mark/sweep pass |
| Ownership (borrow/unique) | — not applicable — | 0 | 0, fully erased at compile time |

Every row except the last one is a real, running piece of code sitting on top of
`mem_alloc`/`mem_free`. The last row is a filter on which programs are allowed to
reach the compiler's codegen stage at all.