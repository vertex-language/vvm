# `gpu/lower/msl`

```go
import lower "github.com/vertex-language/vvm/gpu/lower/msl"
```

Lowers a verified `.gvir` device module (`ir/gvir.Module`) to an MSL IR module
(`gpu/ir/msl.Module`). It produces **one artifact**: `.gvir` declares at most one
`msl` arch, and the arch is a language floor rather than a binary target, so a
single translation unit covers everything at or above it (§3).

The result is IR, not text. Print it with `gpu/ir/msl/encoding/text`, which does
not validate — run `msl.Verify` yourself, or leave `Options.Verify` on and let
`Lower` do it.

```go
res, err := lower.Lower(m) // m: *gvir.Module, already ir/verify'd
if err != nil {
    log.Fatal(err) // a §1.1 lowering error: this artifact fails
}
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}
msl.Resolve(res.Module)
src, _ := text.Print(res.Module)
```

> **Naming.** This is `package msl`, and it imports `gpu/ir/msl` under that same
> name. Go only qualifies *imported* identifiers, so every `msl.` in these files
> is the IR package; this package's own identifiers are always unqualified.

---

## 1. Layout

| File | Responsibility |
| --- | --- |
| `msl.go` | Entry point (`Lower`, `LowerOptions`), `Options`/`Result`, arch → language revision, the module-level `lowerer`, the runtime preamble, structs and `const`s. |
| `gating.go` | §4.3 capability gating: per-kernel feature use over the call graph, kernel exclusion. |
| `types.go` | `gvir.Type` → `msl.Type`, address spaces, the unsigned-twin table, MSL's own layout model, identifier mangling. |
| `values.go` | The §7.3 Join Convention: name → binding, pointee tracking, operand materialization, the two pointer helpers. |
| `callable.go` | Kernel and func lowering: the §6.3 argument buffer, `group` objects, `dynamic_group`, the threadgroup bound, the declaration prologue. |
| `cfg.go` | The structurizer: annotated reducible CFG → `if` / `while` / `switch`. |
| `isel.go` | Arithmetic, bitwise, float, comparisons, conversions, `select`, `call`. |
| `isel_mem.go` | `alloca`, `load`/`store`, `index`/`field`, the vector opcodes, the bulk-memory loops. |
| `isel_sync.go` | Barriers, fences, atomics, subgroup collectives, `submask` operations. |
| `builtin.go` | §9 execution builtins, including the normative i64 linearization. |

There is no register model here at all. MSL is a source language: the Metal
frontend does selection and allocation, and this package's whole job is to
produce a C++ program whose *semantics* are the ones §11 and §12 pin down.

---

## 2. Value Model

**One name, one whole-body local.** §7.3 merges values across blocks by
same-name assignment. In C++ that is a single declaration written more than
once, so every binding is declared in a prologue at the top of the function and
every instruction is an assignment to it. No phi insertion, no loop-carried
special case, and — the reason it is done this way rather than with `Let` at
first assignment — no question about which C++ scope a name that is first
assigned inside an `if` belongs to. Rebinding a name at a different type is
rejected rather than silently redeclared.

**Every pointer is a byte pointer.** `.gvir` pointers carry an address space but
no pointee type, and MSL has no untyped pointer and no integer-to-pointer
conversion. A pointer value is therefore `<space> uchar*`, which is exactly what
§8.3 already writes `index.ptr` against, and every access reinterprets it:

```c
template <typename T> inline device T *vv_at(device uchar *p) { return (device T *)p; }
// ... one overload per address space
```

`load.f32 p` is `*vv_at<float>(p)`. The overload set is resolved by the
operand's address space, so the space survives lowering without appearing
anywhere as a string. §12.3 already makes a misaligned access UB, which is
exactly the precondition the reinterpretation needs.

**Pointee tracking.** `field.ptr p, k` cannot be lowered from the pointer's type
alone. Bindings carry a best-effort `pointee`, propagated from `alloca`, `group`
declarations, struct parameters and `field.ptr`/`index.ptr`. Through a raw
`ptr[global]` kernel argument there is nothing in the IR to name the struct, and
the error says so.

**`submask` is `ulong`.** §4.6 keeps the mask opaque because its width is the
runtime subgroup width; `simd_vote::vote_t` is 64 bits, the widest that width
can be, so a `ulong` holds any mask Metal produces exactly.

**Vectors are vectors.** Unlike the other two backends, MSL has real vector
types, so `vec[T,N]` is one value. `vec[T,3]` is `T3`, which MSL lays out as
four elements — the same rule §4.4 states, which is why the two layout models
in `types.go` agree without a special case.

---

## 3. Signedness

`.gvir` puts signedness in the opcode (`udiv` vs `sdiv`); MSL puts it in the
type. Integers map to the signed MSL spellings, and every `u*` opcode reads both
operands through `as_type` at the unsigned twin and reads the result back:

```text
udiv.i32 a, b   ->   as_type<int>(as_type<uint>(b) == 0u ? 0u
                                                         : as_type<uint>(a) / as_type<uint>(b))
```

`as_type` is a bit-level reinterpretation with no code behind it, so this costs
nothing in the compiled artifact. The affected opcodes are `udiv`, `urem`,
`umulh`, `umin`, `umax`, `lshr`, `rotl`/`rotr`, the `u*` compares, `zext`,
`inttou`, `utoint`, and the two unsigned atomics.

---

## 4. Address Spaces, Parameters and Group Memory

| `.gvir` | MSL | Bound by |
| --- | --- | --- |
| `global` | `device` | argument-buffer field |
| `constant` | `constant` | argument-buffer field |
| `group` | `threadgroup` | `group` decl → threadgroup local; `dynamic_group` → `[[threadgroup(0)]]` |
| `private` | `thread` | `alloca` → function-scope local |

**The argument buffer.** §6.3's packed layout is emitted as a struct whose
offsets are computed from the specification and whose padding is explicit, then
bound as `constant vv_args_t& [[buffer(0)]]`. MSL's own layout of the same
parameter list is computed independently in `types.go` and compared field by
field; a divergence is a lowering error, never a different buffer. This is the
Tier 2 requirement §6.3 states, and `[[id(n)]]` carries the §6.2 parameter
index, which is the portable identity of a parameter and therefore the right
thing for the encoder to see.

**`group` and `alloca` objects are declared by size and alignment, not by
type.** Nothing in the IR ever names one — naming a `group` yields its address
(§8.2), and `alloca` yields a `ptr[private]` (§8.1) — so only the byte count and
the alignment are observable. `storageType` picks the MSL type with exactly
those two properties (`uchar`, `ushort`, `uint`, `uint2`, `uint4`), which is how
`align 16` is expressed without an `alignas` this IR cannot spell. Alignments
above 16 have no vector type to express them and are a lowering error.

**Struct parameters** (§6.2 permits them; §4.7 forbids holding an aggregate in a
named value) are copied into a `thread` local in the prologue, and the name
binds to a byte pointer at that copy — so `field.ptr` works on it and the
pointee is known.

**`dynamic_group_size`.** §6.3 says every backend carries the dynamic group size
natively. On Metal the threadgroup allocation's length is set by the host with
`setThreadgroupMemoryLength` and is *not* readable from the shader, so this
backend takes it in a backend-private one-word buffer at `[[buffer(1)]]`. That
is outside the §6.3 buffer, which stays byte-identical; the generated launcher
fills it.

---

## 5. Control Flow

MSL is C++: no `goto`, no label, no fallback. §7.2's merge annotations are not
just required here, they are the entire input to `cfg.go`.

* `loop_merge Lexit, Lcontinue` → `while (true) { ... }`, with the header's own
  lines at the top of every iteration. A header that tests and exits becomes
  `if (c) { body } else { break; }` with no extra structure.
* `merge L` on a `br_if` → `if` / `else`, both arms stopping at `L`, which the
  caller emits once afterwards. When one arm *is* `L`, the condition is inverted
  rather than an empty branch emitted.
* `merge L` on a `switch` → a C++ `switch`; case labels sharing a target become
  one arm with several labels.
* A branch to the loop exit is `break`, to the header `continue`.
* `return` needs no special handling — unlike `s_endpgm` on amdgcn, a `return`
  under divergent control flow is ordinary C++.
* `unreachable` → `return` with a comment. §12.6 makes executing one UB, so any
  realization conforms and this one keeps the function well-formed.

Two things are lowering errors with explicit messages rather than approximations:

* **A branch to the loop exit from inside a `switch`.** C++ `break` would leave
  the switch, not the loop. (`continue` is unambiguous and is fine.)
* **A separate continue block that is not a straight-line latch.** MSL's
  `continue` jumps *past* the latch, so the latch is tail-duplicated at each
  edge that reaches it — correct for a plain block, and refused for one carrying
  its own merge annotation.

A block reachable from two regions is refused too, naming the block: the CFG is
reducible and annotated (§7.1, §7.2), so that means the annotations do not
describe the graph.

Non-terminating loops are emitted as written; nothing here deletes or reorders
around a side-effect-free cycle (§7.1).

---

## 6. Semantic Corners

Where Metal's defaults and `.gvir`'s pinned behaviour disagree, selection emits
more than the obvious thing:

* **Division and remainder (§11.1).** Metal leaves division by zero undefined;
  `.gvir` requires `0`. Both guards fold into one predicate — `b == 0`, or the
  `INT_MIN / -1` overflow — the divisor is replaced by `1` so the machine
  division is always defined, and the result forced to `0`.
* **Shift and rotate counts (§11.2).** C++ leaves a count at or above the width
  undefined; `.gvir` masks it. The `& (N-1)` is emitted.
* **`ctlz`/`cttz` of zero (§11.2).** MSL follows OpenCL: `clz`/`ctz` of zero
  already yield the operand width, which is what §11.2 pins. Nothing extra.
* **`bswap` (§11.2).** No MSL builtin at any width; the byte permutation is
  generated, which is mechanical and correct at 8/16/32/64.
* **`min`/`max` (§11.3).** MSL's `min`/`max` are not NaN-quieting; `fmin`/`fmax`
  are, and are what IEEE `minNum`/`maxNum` means. `fmin`/`fmax` is emitted.
* **`div` and `sqrt` (§11.3).** Metal's `/` and `sqrt` are approximations under
  fast math. `precise::divide` and `precise::sqrt` are the spellings that are
  not, and are always emitted — `approx` buys `fast::` forms for the §11.6
  opcodes and nothing else.
* **`round` (§11.3).** `round` is already half-away-from-zero in MSL;
  `round_even` is `rint`, `trunc_f` is `trunc`.
* **Ordered not-equal (§11.4).** C++ `!=` is the *unordered* form and is true for
  a NaN operand. `one` is emitted as `a < b || a > b`.
* **Bool vectors (§4.5).** `&&`, `||` and `!` do not apply elementwise to a
  `bool` vector; `&`, `|` and `^` do. Every predicate combination picks between
  them on the value's shape.
* **Float-to-int (§11.5).** Metal's conversion is undefined out of range;
  §11.5's table is saturating and total. The clamp and the NaN case are emitted,
  against bounds that are exact powers of two and therefore exactly
  representable in every float format.
* **`select` (§11.7).** Scalar conditions become the ternary; a `vec[i1,N]`
  condition becomes MSL's `select(a, b, c)`, whose arms are written in the
  opposite order. The ternary does not evaluate both arms, which §11.7 says it
  may — the operands are already-computed values, so nothing is observable.
* **`cmpxchg` (§10.2).** It yields the old value, not a flag, and does not fail
  spuriously. Metal's `atomic_compare_exchange_weak_explicit` does both of the
  opposite things: the loop retries only a spurious failure, and the old value
  is read out of the expected slot the call updates.
* **`shuffle_up`/`shuffle_down` (§10.3).** §10.3 pins shifted-out lanes to the
  source value; Metal leaves the result unspecified when the source lane does not
  exist. The lane guard is part of the lowering.
* **`mask_lt`/`le`/`gt`/`ge`/`eq` (§10.3).** Metal has no lane-mask special
  registers; they are computed from `thread_index_in_simdgroup` in `ulong`,
  exact at any width Metal reports.

---

## 7. Synchronization

`.gvir` atomics are relaxed and express ordering only through `fence` (§10.2),
which is precisely Metal's model — `memory_order_relaxed` is the only order the
atomic functions accept — so nothing sits in between.

The §10.2 **scope operand is carried by the address space**, because that is
where Metal keeps it: a threadgroup atomic is threadgroup-scoped by
construction, and a device atomic is device-scoped. A `group`-scoped atomic
through a `device` pointer is therefore stronger than asked and conforming; a
`grid`-scoped atomic through a `ptr[group]` is impossible and is a lowering
error.

`fence relaxed` emits nothing (§10.2 makes it a no-op). Any other ordering emits
`atomic_thread_fence` with `memory_order_seq_cst` — stronger than acquire,
release or acqrel, and a stronger fence conforms. That function needs Metal 3.1,
so a non-relaxed `fence` in a `metal30` artifact is a lowering error naming the
revision rather than something weaker emitted quietly.

`barrier.group` is `threadgroup_barrier`, `barrier.subgroup` is
`simdgroup_barrier`, and the memory scope becomes the `mem_flags` argument
(`none` → `mem_none`, `subgroup`/`group` → `mem_threadgroup`, `grid` →
`mem_device | mem_threadgroup`). §7.4 already guarantees uniform reachability,
so no guard is needed around either.

---

## 8. Capability Gating

`gating.go` implements §4.3 rules 1–4 for this artifact. Unlike the other two
backends, where only `bf16` ever actually bites, **two of the three gated
features are unconditional here**: there is no `f64` on any Metal target (§4.2)
and no expressible subgroup width (§9.2). A kernel touching either lowers on
`ptx` and `amdgcn` and is excluded here.

* Feature use is collected transitively over the call graph — signature, `group`
  declarations, every instruction suffix, and every reachable `func`'s signature
  and body.
* An excluded kernel is **not emitted** and appears in `Result.Excluded`. It is
  not an error: §4.3 rule 4 makes exclusion from *every* artifact a gating
  error, and that is a whole-module judgement `ir/verify` makes.
* A `func` is emitted only if some emitted kernel reaches it (rule 3).
* `bf16` needs `metal31`, which is the one gate that depends on the declared
  arch.

---

## 9. Float Profile

`float_profile` (§11.6) is module-wide and both flags default off.

* `approx` gates `rcp`/`rsqrt`/`sin`/`cos`/`exp2`/`log2`/`tanh`, which lower to
  `fast::divide(1, x)`, `fast::rsqrt`, `fast::sin`, `fast::cos`, `fast::exp2`,
  `fast::log2` and `precise::tanh`. Emitting one without the flag is a lowering
  error, not a silent strict substitution.
* Explicit `fma` is always `fma` — a single rounding by definition (§11.3).
* `contract` off means no `mul`+`add` fusion. This package never contracts on
  its own, but the Metal frontend fuses under fast math and MSL has no in-source
  switch this backend can version-gate honestly below Metal 3.2. The artifact
  therefore carries a comment recording that it must be compiled with
  `-fno-fast-math`, and the requirement belongs to the toolchain invocation in
  `gvir_arch.md` §6.

---

## 10. Missing Against the Spec (Todos)

Valid IR this backend does not yet lower; each returns an error at `Lower` time
rather than emitting something approximate.

* **§9 builtins inside a `func`.** MSL delivers every builtin as an attributed
  *kernel parameter*, so a `func` has no way to read one without changing the
  calling convention. Threading them through as ordinary parameters is the fix
  and touches every call site.
* **64-bit `umulh`/`smulh`.** `mulhi` is 32-bit; the 64-bit form needs a split
  sequence.
* **Atomics on pointer values.** §10.2 makes them legal on `ptr[*]`; MSL has no
  atomic pointer type and no pointer-to-integer conversion to build one from.
* **`f16`/`bf16` atomics and `atomic_add` on `f64`.** The first has no Metal
  type; the second is gated away here anyway.
* **`alloca`/`group` alignment above 16 bytes.** §2 admits `align` up to 1024;
  the largest MSL vector is 16 bytes, and `alignas` is not expressible in the
  `msl` IR. Both halves need doing together.
* **`loc` as `#line`.** Locations are emitted as comments. Real `#line`
  directives would let the Metal frontend's diagnostics point at the original
  source, but they must print at column zero, which is a printer concern.
* **Non-constant `align` interactions.** `align N` is accepted and used to pick
  the storage type for `alloca`/`group`; on `load`/`store`/atomics it is
  currently discarded, since the reinterpretation carries the accessed type's
  natural alignment and §12.3 makes any mismatch UB.
* **`readonly`.** MSL has no equivalent qualifier, so the §6.4 assertion is
  dropped rather than translated. It costs an optimization, not correctness.
* **Multi-level exits.** A branch from inside a `switch` to an enclosing loop's
  exit is refused (§5). A flag variable would express it; whether that is better
  than refusing is unsettled.