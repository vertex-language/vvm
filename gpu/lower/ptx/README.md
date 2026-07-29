# `gpu/lower/ptx`

```go
import lower "github.com/vertex-language/vvm/gpu/lower/ptx"
```

Lowers a verified `.gvir` device module (`ir/gvir.Module`) to a structured PTX IR
module (`gpu/ir/ptx.Module`). It produces **one artifact**: `.gvir` declares at
most one `ptx` arch and PTX is JIT-forward, so a single module covers everything
at or above the declared floor (§3).

The result is IR, not text. Print it with `gpu/ir/ptx/encoding/text`, rewrite it,
or embed it — this package makes no formatting decisions.

```go
res, err := lower.Lower(m) // m: *gvir.Module, already ir/verify'd
if err != nil {
    log.Fatal(err) // a §1.1 lowering error: this artifact fails
}
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}
src, _ := text.Print(res.Module)
```

> **Naming.** This is `package ptx`, and it imports `gpu/ir/ptx` under that same
> name. Go only qualifies *imported* identifiers, so every `ptx.` in these files
> is the IR package; this package's own identifiers are always unqualified.

---

## 1. Layout

| File | Responsibility |
| --- | --- |
| `ptx.go` | Entry point (`Lower`, `LowerOptions`), `Options`/`Result`, arch and ISA selection, the module-level `lowerer`. |
| `gating.go` | §4.3 capability gating: per-kernel feature use over the call graph, kernel exclusion, and the §9.2 `subgroup_size` check. |
| `types.go` | `gvir.Type` → `ptx.Type`, address spaces, register widths, and the zero-extension invariant's helpers. |
| `value.go` | The §7.3 Join Convention: name → register binding, vector lane vectors, pointee tracking, operand materialization. |
| `consts.go` | Module `const`s → `.const` variables, plus the scalar-inlining table. |
| `callable.go` | Kernel and func lowering: kernarg → `.param`, `group` → `.shared`, `dynamic_group` → `.extern .shared`, tuning directives. |
| `cfg.go` | Block emission order, label binding, terminators. Merge annotations are dropped — PTX needs no structured control flow. |
| `isel.go` | Instruction selection: arithmetic, bitwise, float, comparisons, conversions, `select`, `call`. |
| `isel_mem.go` | `alloca`, `load`/`store`, `index`/`field`, `memcopy`/`memset`, and the vector opcodes. |
| `isel_sync.go` | Barriers, fences, atomics, subgroup collectives, `submask` operations. |
| `builtin.go` | §9 execution builtins, including the normative i64 linearization. |
| `half.go` | `f16`/`bf16` literal bit patterns, since PTX takes 16-bit float immediates as bit patterns. |

There is no register allocator and no scratch-register discipline of the x86_64
kind: PTX is a virtual-register ISA and `ptxas` allocates. `Body.Regs` hands out
as many virtual registers as selection wants.

---

## 2. Value Model

**One name, one register.** §7.3 merges values across blocks by same-name
assignment, which is exactly a virtual register that is written more than once.
`value.define` binds a name to a register on first assignment and returns the
same register on every later assignment, so no phi insertion and no
loop-carried-value special case exists anywhere in this package. Rebinding a name
at a different type is rejected rather than silently reallocated.

**Vectors are lane vectors.** PTX has no vector registers — `.v2`/`.v4` is a
property of `ld`/`st` operand arity, not of a type — so a `vec[T,N]` value is `N`
scalar registers and every arithmetic opcode is emitted `N` times. `vec[T,3]`
keeps three registers; the padding lane (§4.4) is never materialized, which is
consistent with `extract`/`insert` at `k = 3` being a verification error.

**`submask` is `.b32`.** PTX's warp is 32 lanes wide, so the opaque lane mask
(§4.6) is one `b32` register and `mask_lt`/`mask_le`/… are the `%lanemask_*`
special registers directly.

**Pointee tracking.** `.gvir` pointers carry an address space but no pointee type,
so `field.ptr p, k` cannot be lowered from the pointer's type alone. `value`
carries a best-effort `pointee`, propagated from `alloca`, `group` declarations,
`field.ptr` and `index.ptr`. A `field.ptr` through a pointer whose provenance is a
kernel parameter is a lowering error with an explicit message (see §9).

---

## 3. Zero-Extension Invariant

A value of type `iN` lives in a register of width `max(16, N)` holding the value
**zero-extended**. PTX 8-bit registers are usable only by `ld`, `st` and `cvt`, so
`i8` is promoted to a 16-bit register exactly as `nvcc` does (`%rs`).

* Wrapping operations on `i8` (`add`, `sub`, `mul`, `neg`, `not`, `shl`, `rotl`,
  `rotr`, `brev`, `bswap`) are followed by `and.b16 d, d, 255` to restore the
  invariant. §11.1's "wrapping is modulo 2^N and always defined" is therefore
  literally true at 8 bits, not approximately.
* Signed consumers (`sdiv`, `srem`, `ashr`, `abs`, `smin`/`smax`, the `s*`
  compares, `sext`, `inttos`) sign-extend an `i8` operand with `cvt.s16.s8` into a
  scratch copy that is never written back to the value's own register.
* `i16`, `i32` and `i64` need no masking: the register is exactly the value width.

---

## 4. Address Spaces and Parameters

`.gvir` has no generic pointer, so no `cvta` appears in the body: every `ld`/`st`
carries the space qualifier taken from the pointer's type (§5).

The one exception is the kernel prologue. A `ptr[global]` or `ptr[constant]`
kernel argument arrives in `.param` space as a **generic** address, so the
prologue emits the standard pair:

```text
ld.param.u64    %rd1, [out];
cvta.to.global.u64  %rd1, %rd1;
```

after which the value is a space-specific address for the rest of the body.

| `.gvir` | PTX state space | Bound by |
| --- | --- | --- |
| `global` | `.global` | kernel param + `cvta.to.global` |
| `constant` | `.const` | kernel param + `cvta.to.const` |
| `group` | `.shared` | `group` decl → function-scope `.shared .b8 name[bytes]` |
| `group` (dynamic) | `.shared` | module-scope `.extern .shared .b8 $dyn_smem[]` |
| `private` | `.local` | `alloca` → function-scope `.local .b8 name[bytes]` |

`dynamic_group` collapses to **one** module-scope extern array shared by every
kernel that declares one, aligned to the maximum requested alignment — PTX has a
single dynamic shared window per launch, so per-kernel names are aliases of it.
`dynamic_group_size` is `%dynamic_smem_size`, which is why §6.3's "no hidden
trailer" costs nothing here: the kernarg buffer is the explicit parameter list and
the size arrives out of band.

Struct parameters (§6.2 permits them, §4.7 forbids holding an aggregate in a named
value) are lowered as `.param .align A .b8 s[size]` and copied into a `.local`
buffer in the prologue; the name binds to that `ptr[private]`, so `field.ptr`
works on it and the pointee is known.

---

## 5. Control Flow

Merge annotations (§7.2) are **dropped**. They exist because two of the three
backends require structured control flow; PTX does not, and a reducible annotated
CFG is already a legal PTX branch graph. When `Options.Comments` is set they are
re-emitted as trailing comments for readability, never as directives.

* `br` → `bra`
* `br_if` → `@%p bra Then; bra Else;` — predication is attached to the branch
  itself via `Instr.If`, so it cannot leak past a label.
* `switch` → a `setp`/`@%p bra` chain, then `bra default`. `brx.idx` needs a dense
  index and a `.branchtargets` table; the compare chain is correct for every
  `switch` §2 admits and is what a sparse case list would lower to anyway.
* `return` in a kernel → `ret;`. In a func, the value is stored to the return
  `.param` first.
* `unreachable` → `trap;` — §12.6 makes executing one UB, and `trap` is the
  least surprising realization of it.

Non-terminating loops are emitted as written; nothing in this package deletes or
reorders around a side-effect-free cycle (§7.1).

---

## 6. Semantic Corners

These are the places where PTX's default behaviour and `.gvir`'s pinned behaviour
disagree, and where selection therefore emits more than one instruction:

* **Division and remainder (§11.1).** PTX leaves division by zero undefined;
  `.gvir` requires `0`. Every `udiv`/`sdiv`/`urem`/`srem` is guarded: the divisor
  is replaced by `1` under a predicate and the result forced to `0` under the same
  predicate. `sdiv`/`srem` additionally fold `INT_MIN / -1` into that predicate.
* **`ctlz`/`cttz` of zero (§11.2).** Computed as `clz.b32(x) - (32 - N)` for
  `N < 32`, which yields `N` for `x = 0` without a branch; `cttz` is `brev` then
  `clz`, clamped to `N` with `min.u32`.
* **Shift and rotate counts (§11.2).** PTX clamps counts at the operand width;
  `.gvir` masks them. The count is `and`-ed with `N-1` first. 32-bit rotates use
  `shf.l.wrap`/`shf.r.wrap`; other widths use the shift pair.
* **`min`/`max` (§11.3).** PTX `min.f32` without the `.NaN` qualifier is already
  IEEE `minNum`, so the qualifier is deliberately *not* emitted.
* **`round` (§11.3).** Half-away-from-zero has no PTX rounding mode. Emitted as
  `t = trunc(x); if |x - t| >= 0.5 then t + copysign(1, x) else t`.
  `round_even` is `cvt.rni`, `trunc_f` is `cvt.rzi`, `floor`/`ceil` are
  `cvt.rmi`/`cvt.rpi`.
* **Float-to-int (§11.5).** `cvt.rzi` with an integer destination already
  saturates and maps NaN to `0`, matching the table exactly — no clamp sequence.
* **Atomics (§10.2).** Every atomic is `atom.relaxed.<scope>` with no ordering,
  because the IR expresses ordering only through `fence`. `atomic_sub` becomes
  `neg` + `atom.add` (PTX has no `atom.sub`). `cmpxchg` is `atom.cas`, which
  already yields the old value rather than a flag.
* **Reductions (§10.3).** `sub_*` is a five-step `shfl.sync.down` tree followed by
  a broadcast from lane 0, with each combine predicated on the partner lane being
  present in `activemask`. §9.2 guarantees inactivity comes only from unpopulated
  trailing lanes, so the active set is always a prefix and the tree is exact.
  `redux.sync` would be one instruction but needs `sm_80`, above the `sm_70` floor.
* **`broadcast_first` (§10.3).** `activemask` → `brev` → `clz` gives the lowest
  active lane, then `shfl.sync.idx`.
* **Barriers (§10.1).** `barrier.group` is `bar.sync 0`; `barrier.subgroup` is
  `bar.warp.sync <activemask>`. A `grid` memory scope adds `membar.gl` ahead of
  the barrier; a `group` memory scope on a subgroup-scoped barrier adds
  `membar.cta`. `none` emits the execution barrier alone.

---

## 7. Capability Gating

`gating.go` implements §4.3 rules 1–4 for this one artifact. A kernel's feature use
is collected transitively over the call graph — signature, `group` declarations,
every instruction suffix, and every reachable `func`'s signature and body — and
checked against `gvir.Supports(BackendPTX, arch, f)`.

* An excluded kernel is **not emitted** and appears in `Result.Excluded`. It is not
  an error: §4.3 rule 4 makes exclusion from *every* artifact a gating error, and
  that is a whole-module judgement `ir/verify` makes, not one this backend can.
* A `func` is emitted only if some emitted kernel reaches it (rule 3).
* On this backend only `bf16` is ever actually gated: `f64` and `subgroup_size` are
  available on every `sm_*` in the table. `subgroup_size N` with `N ≠ 32` is a
  **lowering error** (§9.2), returned from `Lower`.

---

## 8. Float Profile

`float_profile` (§11.6) is module-wide and both flags default off.

* `contract` off ⇒ nothing is fused. This package never contracts on its own — a
  `mul` and an `add` are emitted as two instructions — but `ptxas` will contract
  unless told not to, so with `contract` off the module carries
  `.pragma "nofma";` at module scope. With `contract` on the pragma is omitted and
  fusion is left to `ptxas`.
* `approx` gates `rcp`/`rsqrt`/`sin`/`cos`/`exp2`/`log2`/`tanh`, which lower to
  `rcp.approx.f32`, `rsqrt.approx.f32`, `sin.approx.f32`, `cos.approx.f32`,
  `ex2.approx.f32`, `lg2.approx.f32` and `tanh.approx.f32`. Emitting one without
  the flag is a lowering error, not a silent strict substitution.
* Explicit `fma` is always `fma.rn` — a single rounding by definition (§11.3), and
  `ptx.Verify` requires the explicit rounding qualifier anyway.

---

## 9. Missing Against the Spec (Todos)

Valid IR this backend does not yet lower; each returns an error at `Lower` time
rather than emitting something approximate.

* **`memmove`.** `memcopy` and `memset` emit forward byte loops. `memmove` needs a
  direction test and a backward loop; unimplemented.
* **`field.ptr` through an untyped pointer.** Lowerable only when the pointee is
  recoverable from provenance (`alloca`, a `group` declaration, a struct parameter,
  or a chain of `field.ptr`/`index.ptr` from one of those). Through a raw
  `ptr[global]` kernel argument there is nothing in the IR to name the struct, and
  the error says so.
* **`bswap` on `i64`.** 8/16/32 are implemented (identity, shift pair, and `prmt`
  respectively); the 64-bit form needs a split/`prmt`/swap/join sequence.
* **`bf16` arithmetic below `sm_90`.** `.gvir` makes `bf16` available at `sm_80`,
  where PTX supports conversion but not `add`/`mul`/… on `bf16`. Arithmetic is
  emitted natively, so an `sm_80` module doing `bf16` math will be rejected by
  `ptxas`. Promotion through `f32` would double-round and is not conforming, so
  nothing is emitted instead of it.
* **`f16`/`bf16` vector packing.** `f16x2`/`bf16x2` packed arithmetic is never
  formed; `vec[f16,2]` is two scalar `f16` operations.
* **Non-constant `align` interactions.** `align N` is forwarded to `ld`/`st`/atomics
  verbatim; nothing verifies it against the pointer's provenance (that is §12.3 UB
  and `ir/verify`'s business).