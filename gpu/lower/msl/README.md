# `gpu/lower/msl`

```go
import lower "github.com/vertex-language/vvm/gpu/lower/msl"
```

Lowers a verified `.gvir` device module (`ir/gvir.Module`) to a structured MSL IR
module (`gpu/ir/msl.Module`). It produces **one artifact**: `.gvir` declares at
most one `msl` arch, and that arch is a language floor rather than a binary
target (§3), so a single translation unit covers everything at or above it.

The result is IR, not text. Print it with `gpu/ir/msl/encoding/text`, rewrite it,
or embed it — this package makes no formatting decisions.

```go
res, err := lower.Lower(m) // m: *gvir.Module, already ir/verify'd
if err != nil {
    log.Fatal(err) // a §1.1 lowering error: this artifact fails
}
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}
for _, d := range msl.Verify(res.Module) {
    log.Println(d)
}
src, _ := text.Print(res.Module)
```

> **Naming.** This is `package msl`, and it imports `gpu/ir/msl` under that same
> name. Go only qualifies *imported* identifiers, so every `msl.` in these files
> is the IR package; this package's own identifiers are always unqualified.

---

## 1. Layout

| File | Responsibility |
| --- | --- |
| `msl.go` | Entry point (`Lower`, `LowerOptions`), `Options`/`Result`, arch → `msl.Version` selection, float-profile realization, the module-level `lowerer`. |
| `gating.go` | §4.3 capability gating: transitive feature use over the call graph, kernel exclusion, func reachability, struct availability. |
| `types.go` | `gvir.Type` → `msl.Type`, address spaces, the unsigned-representation invariant's helpers. |
| `value.go` | The §7.3 Join Convention: name → hoisted local, pointee tracking, operand and literal materialization, name hygiene. |
| `consts.go` | Module `const`s → module-scope `constant` variables and the const-init grammar. |
| `callable.go` | Kernel and func lowering: kernarg → argument buffer, `group` → threadgroup locals, `dynamic_group` → threadgroup parameter, attributes. |
| `cfg.go` | Structurization: §7.2 merge annotations → `if`/`else if`/`switch`/`while`, `break`/`continue` selection, terminators. |
| `isel.go` | Instruction selection: arithmetic, bitwise, float, comparisons, conversions, `select`, `call`. |
| `isel_mem.go` | `alloca`, `load`/`store`, `index`/`field`, `memcopy`/`memset`, and the vector opcodes. |
| `isel_sync.go` | Barriers, fences, atomics, subgroup collectives, `submask` operations. |
| `builtin.go` | §9 execution builtins → attributed kernel parameters, including the normative i64 linearization. |

MSL is C++, so there is no register model and no instruction encoding here: this
package builds a declaration/statement/expression tree and lets the Metal
frontend do instruction selection. What it *does* own is the shape of that tree.

---

## 2. Value Model

**One name, one hoisted local.** §7.3 merges values across blocks by same-name
assignment. Structurization puts a block's statements inside `if`/`while` bodies,
so a value assigned in one block and read in another cannot be a block-scoped
declaration. `value.define` binds a name to a function-scope `msl.VarDecl` on
first assignment and returns the same binding on every later one; the
declarations are spliced in at the top of the body with `Block.InsertBefore`
after the body is built. There is no phi insertion and no loop-carried-value
special case anywhere in this package. Rebinding a name at a different type is
rejected, not silently redeclared.

Locals are declared uninitialized: §7.3 rule 3 guarantees every read is
dominated by an assignment, and there is no `undef` to spell anyway.

**Unsigned representation.** A value of type `iN` lives in the *unsigned* MSL
type of that width — `uchar`, `ushort`, `uint`, `ulong`. Signed consumers
(`sdiv`, `srem`, `ashr`, `abs`, `smin`/`smax`, the `s*` compares, `sext`,
`inttos`) cast to the signed spelling for the operation and the result converts
back on assignment, which is modulo and therefore exactly §11.1's "wrapping is
modulo 2^N and always defined". This also makes 8- and 16-bit arithmetic correct
for free: C++ promotes `uchar` operands to `int`, and the narrowing back to the
declared `uchar` local is the truncation §11.1 asks for.

**`i1` is `bool`, `vec[i1,N]` is `boolN`.** Both are value-only in `.gvir` and
non-storable in MSL practice, so nothing has to defend the invariant.

**Pointers are untyped bytes.** `.gvir` pointers carry an address space but no
pointee type, so every pointer value is `SPACE uchar*` and each access
`reinterpret_cast`s at the point of use. `index.ptr` is therefore plain pointer
arithmetic with no scaling. `value` carries a best-effort `pointee`, propagated
from `alloca`, `group` declarations, struct parameters, and chains of
`field.ptr`/`index.ptr`; a `field.ptr` through a pointer whose provenance is a
raw kernel argument is a lowering error with an explicit message (§9).

**`submask` is `simd_vote::vote_t`.** The module aliases it as `gvir_submask`.
§4.6 makes the width runtime-determined, which is exactly why the mask
constants (`mask_lt`, …) are computed from `thread_index_in_simdgroup` and
`threads_per_simdgroup` rather than read out of a special register.

---

## 3. Address Spaces and Parameters

| `.gvir` | MSL | Bound by |
| --- | --- | --- |
| `global` | `device` | argument-buffer field |
| `constant` | `constant` | argument-buffer field |
| `group` | `threadgroup` | `group` decl → threadgroup local; `dynamic_group` → `[[threadgroup(0)]]` parameter |
| `private` | `thread` | `alloca` → function-scope local; struct parameter → prologue copy |

There is no generic pointer in either language, so no space-cast ever appears.
`§5`'s "a value's space is fixed at first binding" is enforced by the binding's
type, which includes the space.

### 3.1 The argument buffer

§6.3 pins the kernarg layout and requires it to be byte-identical across all
three backends, so the generated struct is **not** left to Metal's struct rules:
`gvir.Module.KernargLayout` is the single derivation, and explicit
`uchar _padN[k]` fields realize every gap and the trailing padding.

```text
struct vector_add_args {
    device float *a;
    device float *b;
    device float *c;
    uint          n;
    uchar         _pad_tail[4];
};

kernel void vector_add(
    constant vector_add_args &gvir_args [[buffer(0)]],
    uint3                     gvir_tid  [[thread_position_in_grid]])
```

This requires **Argument Buffers Tier 2**; on Tier 1 pipeline creation fails and
the launcher reports `GVIR_ERR_UNAVAILABLE`, exactly as §6.3 says.

Kernel parameters become prologue-initialized locals rather than direct uses of
`gvir_args.x`, because §7.3 counts parameters as entry-block assignments and
permits reassignment. `func` parameters are MSL parameters directly — C++ passes
by value, so they are already assignable locals.

A struct parameter is copied into a `thread` local in the prologue and the name
binds to a `thread uchar*` into that copy, so `field.ptr` works on it and the
pointee is known (§4.7 forbids holding an aggregate in a named value).

### 3.2 Group memory

`group` declarations become `threadgroup` locals at the top of the kernel body;
`align N` rides along as `[[gnu::aligned(N)]]`, since MSL has no typed
`alignas` node and `RawAttr` is the escape hatch for exactly this. Zero
initialization is not emitted — §8.2 does not guarantee it.

---

## 4. Control Flow

This is the backend §7.2 exists for. Merge annotations are **consumed**, not
dropped: they are what makes a one-pass structurizer possible without a
relooper.

* `loop_merge Lexit, Lcontinue` → `while (true) { … }`, continuing at `Lexit`.
* `merge L` on a `br_if` → `if (c) { … } else { … }`, continuing at `L`. An
  empty else arm is omitted; the printer already collapses `else { if … }` into
  `else if`.
* `merge L` on a `switch` → `switch` with one arm per distinct successor label
  and an implicit `break` per arm. A `default` equal to the merge label emits no
  default arm.
* `br` is fallthrough; `br_if` with one distinct successor (§ `Successors`
  deduplicates) is fallthrough.
* `return` → `return;` / `return x;`. `unreachable` → `__builtin_unreachable();`,
  which is the least surprising realization of §12.6 in a language with no trap.

**Latches.** When the continue target is empty and branches back to the header,
the loop is emitted plainly and `br Lcontinue` becomes `continue`. When the
latch carries instructions, C's `continue` would skip them, so the body is
wrapped:

```text
gvir_brk0 = false;
while (true) {
    do { … body … } while (false);   // `continue` -> break
    if (gvir_brk0) { break; }
    … latch …                        // back edge -> fallthrough
}
```

**Non-local transfers are a lowering error, not a miscompile.** A `break` or
`continue` emitted from inside an intervening `switch` or inner loop would bind
to the wrong construct, so the structurizer tracks break depth and fails with an
explicit message instead (§9). Selections do not count — `if` is not breakable —
which is the overwhelmingly common case.

A block reachable from two structured paths, or a multi-successor block with no
merge annotation, is likewise a lowering error rather than silent duplication.

---

## 5. Semantic Corners

Where MSL's default behaviour and `.gvir`'s pinned behaviour disagree, and
selection therefore emits more than one operation:

* **Division and remainder (§11.1).** MSL leaves division by zero undefined;
  `.gvir` requires `0`. Every `udiv`/`sdiv`/`urem`/`srem` is guarded branchlessly
  with `select`: the divisor is replaced by `1` under the guard and the result
  forced to `0` under the same guard. `sdiv`/`srem` fold `INT_MIN / -1` into it.
* **`ctlz`/`cttz` of zero (§11.2).** MSL's `clz`/`ctz` already return the type's
  bit width for a zero operand, which is §11.2 exactly — emitted as-is, no clamp.
* **Shifts and rotates (§11.2).** The count is masked with `N-1` explicitly
  rather than relying on the OpenCL-inherited masking. `rotr` is
  `rotate(x, (N - c) & (N-1))`, which is correct at `c ≡ 0` because `N & (N-1)`
  is zero.
* **`min`/`max` (§11.3).** Emitted as `fmin`/`fmax`, which are NaN-quieting.
  MSL's `min`/`max` are the comparison forms and would not satisfy `minNum`.
* **`round`/`round_even` (§11.3).** MSL `round` is already half-away-from-zero;
  `round_even` is `rint`, `trunc_f` is `trunc`.
* **Float-to-int (§11.5).** MSL conversion is neither saturating nor total, so
  `stoint`/`utoint` emit a three-`select` sequence against `±2^(N-1)` (exactly
  representable) plus an `isnan` arm yielding `0` — the §11.5 table verbatim.
* **`one` (§11.4).** Ordered not-equal is `(a < b) || (a > b)`; C++ `!=` is the
  *unordered* predicate and is true for NaN.
* **Atomics (§10.2).** Every atomic is `atomic_*_explicit(…, memory_order_relaxed)`
  through a `reinterpret_cast` to `SPACE atomic_uint*` / `atomic_int*` /
  `atomic_ulong*` / `atomic_float*`, since `.gvir` pointers have no pointee. The
  §10.2 **scope operand is dropped**: MSL expresses scope through the address
  space and the barrier's `mem_flags`, not per operation. Ordering is likewise
  never attached, because the IR expresses it only through `fence`.
* **`cmpxchg` (§10.2).** MSL offers only the weak, flag-returning form, and
  §10.2 requires the old value and forbids spurious failure. Emitted as a retry
  loop that spins only while the compare-exchange failed *and* the expected slot
  is unchanged, then binds the slot as the result.
* **`shuffle_up`/`shuffle_down` (§10.3).** §10.3 says shifted-out lanes return
  the source value; MSL leaves them unspecified. Emitted as
  `select(v, simd_shuffle_up(v, d), lane >= d)` — `select`, not a ternary,
  because the shuffle must not end up under divergent control.
* **Barriers (§10.1).** `barrier.group` is `threadgroup_barrier`,
  `barrier.subgroup` is `simdgroup_barrier`. Memory scope maps `none` →
  `mem_none`, `subgroup`/`group` → `mem_threadgroup`, `grid` →
  `mem_device | mem_threadgroup`.
* **`group_size` (§6.1).** Lowers to `[[max_total_threads_per_threadgroup(X*Y*Z)]]`
  — a thread-count bound only. The exact shape is the launcher's contract, which
  is what §6.1 already says for this backend.
* **Float profile (§11.6).** Metal compiles with fast math by default, so
  `contract` off emits `#pragma METAL fp math_mode(safe)` behind a
  `__METAL_VERSION__ >= 320` gate and a comment recording that `metal3.0`/`3.1`
  artifacts need `-fno-fast-math`. `approx` selects the `fast::` namespace for
  `rcp`/`rsqrt`/`sin`/`cos`/`exp2`/`log2`; emitting one without the flag is a
  lowering error, not a silent strict substitution.
* **Name hygiene.** §2 gives `.gvir` a flat module-wide namespace with no
  shadowing, so `msl.Resolve` is never run: there is nothing to disambiguate.
  Value names that collide with an MSL keyword or a stdlib spelling get a
  trailing underscore, and every synthesized name carries the `gvir_` prefix
  (user names starting with it are mangled the same way).

---

## 6. Capability Gating

`gating.go` implements §4.3 rules 1–4 for this one artifact. Feature use is
collected transitively — signature, `group` declarations, every instruction type
suffix, and every reachable `func`'s signature and body — and checked against
`gvir.Supports(BackendMSL, arch, f)`.

* An excluded kernel is **not emitted** and appears in `Result.Excluded`. It is
  not an error: rule 4 makes exclusion from *every* artifact a gating error, and
  that is a whole-module judgement `ir/verify` makes.
* A `func` is emitted only if some emitted kernel reaches it (rule 3).
* All three §4.3 features actually bite here: `f64` and `subgroup_size` are
  unavailable on every `msl` artifact, and `bf16` needs `metal31`. A struct
  whose fields need an unavailable feature is replaced by a comment — only an
  excluded kernel could have referenced it.

`msl.Verify` will additionally warn if a `bfloat` reaches a `metal3.0` module;
gating means it should not, and a warning there is a bug in this package.

---

## 7. Missing Against the Spec (Todos)

Valid IR this backend does not yet lower; each returns an error at `Lower` time
rather than emitting something approximate.

* **`memmove`.** `memcopy` and `memset` emit forward byte loops. `memmove` needs
  a direction test, and `dst < src` is not even expressible when the operands sit
  in different address spaces (§5 forbids the comparison); unimplemented.
* **`field.ptr` through an untyped pointer.** Lowerable only when the pointee is
  recoverable from provenance (`alloca`, a `group` declaration, a struct
  parameter, or a chain of `field.ptr`/`index.ptr` from one of those). Through a
  raw `ptr[global]` kernel argument there is nothing in the IR to name the
  struct, and the error says so.
* **`bswap` on `i64`.** 8/16/32 are implemented; the 64-bit form needs an
  eight-term shift/mask sequence.
* **Atomics on `ptr[*]`.** §10.2 permits them; realizing one needs a
  pointer↔integer round trip whose conformance in MSL is not established, so
  nothing is emitted instead.
* **Non-local `break`/`continue`.** A loop exit or continue crossing an
  intervening `switch` or inner loop needs flag propagation through each nested
  construct (§4); currently a lowering error.
* **Multi-successor blocks with no merge annotation**, including an entry block
  ending in a two-way `br_if` — §2's grammar attaches `merge-decl` to labelled
  blocks only, and this backend cannot invent the reconvergence point.
* **Execution builtins inside a `func`.** §9 builtins lower to attributed kernel
  parameters; a `func` has no such surface, so they must be passed in. Reaching
  one from a `func` body is a lowering error.
* **`dynamic_group_size`.** §6.3 asserts every backend carries the dynamic group
  size natively — "a threadgroup length on `msl`". That mechanism has no typed
  MSL spelling, so the length arrives as a `RawAttr`-attributed parameter and is
  the one construct here whose acceptance by the Metal frontend is not
  established. It is emitted only when the kernel actually reads it.
* **Under-alignment.** `align N` on `load`/`store`/atomics is not expressible in
  MSL; the clause is dropped and violating natural alignment stays §12.3 UB.
* **64-bit atomics** lower to `atomic_ulong`, which needs `metal3.1` and
  hardware support; nothing here gates it, because §4.3's list is closed.