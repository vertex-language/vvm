# Vertex GPU IR — Language Specification (v1.0)

**File extension:** `.gvir` (text). Device-only compute-kernel IR for SIMT hardware, targeting NVIDIA (PTX), AMD (amdgcn), and Apple (MSL) from one module.

Sibling of Vertex IR `.vir` v2.1, not a subset: shares lexical conventions, block/terminator shape, the Join Convention, and exhaustive-UB discipline; shares neither type system, opcode table, memory model, nor trap semantics. Divergences: `gvir_arch.md` §9. Host integration requires `.vir` v2.1 (`gvir_arch.md` §3–§5). Per-backend lowering is `gvir_arch.md` §6.

---

## 1. Principles

* **SIMT, device-only.** No host entry point, host calls, host globals, I/O, allocation, or recursion.
* **One semantics.** An opcode means the same thing on all targets or it is not in the IR. Real hardware differences are named and queryable (§9.2).
* **Capability gating, never silent degradation** (§4.3).
* **Explicit address spaces.** No generic pointer in v1; the space is part of the type.
* **Annotated CFG.** Every divergent construct declares its reconvergence point (§7.2).
* **Static footprint.** Private and group memory statically bounded per kernel; `dynamic_group` is the one declared exception.
* **No traps.** Everything `.vir` traps on has deterministic fallback semantics.
* **Exhaustive UB.** 10 triggers (§12). Nothing else is undefined.

---

## 2. Module Grammar & Order

Line-oriented; no separators or continuations; indentation conventional. Strict section order permits one-pass verification.

```text
module          := version-decl module-header target-decl float-profile-decl?
                   struct-decl* const-decl* func-def* kernel-def*

version-decl    := "gvir" version-literal
version-literal := int-literal "." int-literal
module-header   := "module" ident
target-decl     := "target" backend ("," backend)*
backend         := "ptx" arch-list? | "amdgcn" arch-list | "msl" arch-list?
arch-list       := "[" ident ("," ident)* "]"
float-profile-decl := "float_profile" ("strict" | "bounded")

struct-decl     := "struct" ident "(" field ("," field)* ")"
field           := ident type
const-decl      := "const" ident type "=" const-init
const-init      := literal | "zero" | "(" const-init ("," const-init)* ")"

type            := int-type | float-type | vec-type | ptr-type
                 | "struct" ident | array-type | "void" | "submask"
int-type        := "i1" | "i8" | "i16" | "i32" | "i64"
float-type      := "f16" | "bf16" | "f32" | "f64"
vec-elem-type   := "i8" | "i16" | "i32" | "i64" | float-type
vec-type        := "vec" "[" vec-elem-type "," vec-width "]"
pvec-type       := "vec" "[" "i1" "," vec-width "]"
vec-width       := "2" | "3" | "4"
array-type      := "array" "[" type "," int-literal "]"
ptr-type        := "ptr" "[" addr-space "]"
addr-space      := "global" | "group" | "private" | "constant"

kernel-def      := "kernel" ident "(" kparam-list? ")" kernel-attr* ":"
                   group-decl* entry-block block* "end"
kparam-list     := kparam ("," kparam)*
kparam          := ident kparam-type
kparam-type     := "ptr" "[" ("global" | "constant") "]"
                 | int-type | float-type | vec-type | "struct" ident
kernel-attr     := "group_size" int-literal "," int-literal "," int-literal
                 | "max_group_size" int-literal
                 | "min_groups_per_unit" int-literal
                 | "subgroup_size" int-literal
                 | "dynamic_group" ident ("align" int-literal)?
group-decl      := "group" ident type ("align" int-literal)?

func-def        := "func" ident "(" param-list? ")" ret-type func-attr* ":"
                   entry-block block* "end"
param-list      := param ("," param)*
param           := ident type
ret-type        := int-type | float-type | vec-type | pvec-type
                 | ptr-type | "submask" | "void"
func-attr       := "inline" | "noinline" | "readonly"

entry-block     := alloca-line* body-line* terminator
block           := label-line merge-decl? body-line* terminator
label-line      := ident ":"
label           := ident
merge-decl      := "merge" label | "loop_merge" label "," label
alloca-line     := ident "=" "alloca" "." type ("align" int-literal)?
body-line       := inst | loc-line
loc-line        := "loc" string-literal int-literal int-literal?

inst            := ident "=" op operand-list? align-clause?
                 | op operand-list? align-clause?
                 | builtin-inst | barrier-inst
builtin-inst    := ident "=" builtin-name dim-suffix?
builtin-name    := "thread_in_grid" | "thread_in_group" | "group_in_grid"
                 | "thread_in_subgroup" | "subgroup_in_group"
                 | "threads_per_group" | "groups_per_grid" | "threads_per_grid"
                 | "threads_per_subgroup" | "subgroups_per_group"
                 | "dynamic_group_size"
dim-suffix      := "." ("x" | "y" | "z")
barrier-inst    := "barrier" "." exec-scope ("," scope)?

op              := ident ("." (ident | type))?
operand-list    := operand ("," operand)*
align-clause    := "," "align" int-literal

exec-scope      := "subgroup" | "group"
scope           := "subgroup" | "group" | "grid" | "none"
ordering        := "relaxed" | "acquire" | "release" | "acqrel" | "seqcst"

terminator      := "br" label
                 | "br_if" operand "," label "," label
                 | "switch" operand "," label ("," int-literal label)*
                 | "return" operand? | "unreachable"

operand         := ident | literal | ordering | scope
literal         := int-literal | float-literal | bool-literal | "null"
int-literal     := "-"? [0-9]+
float-literal   := "-"? [0-9]+ "." [0-9]+ ("e" "-"? [0-9]+)? | "NaN" | "Inf" | "-Inf"
bool-literal    := "true" | "false"
```

* **Lexical.** Identifiers `[A-Za-z_][A-Za-z0-9_]*`, no sigils. Two consecutive underscores (`__`) are forbidden — reserved as the host symbol ABI separator (`gvir_arch.md` §4). Comments `//` to end of line. Flat module-wide namespace, no shadowing, declare-before-use, no forward references.
* **Opcode suffixes may contain commas** (`add.vec[f32,4] a, b`). A comma inside an unclosed `[` or `(` belongs to the suffix; parsers must track bracket depth when splitting an `inst` line.
* **Strings** appear only in `loc`.
* **`align N`** requires a power of two, `1 ≤ N ≤ 1024`; otherwise a verification error.
* `pvec-type` is reachable only as a `ret-type` or `param` type, never from `type` (§4.5).

---

## 3. Targets

```text
target ptx[sm_80, sm_90], amdgcn[gfx1100, gfx942], msl[metal32]
```

| Backend | Arch list | Meaning |
| --- | --- | --- |
| `ptx` | optional | Minimum SM per listed arch; omitted means `sm_70` and JIT-forward |
| `amdgcn` | **required** | One artifact per `gfx` target; no JIT fallback, so no default |
| `msl` | optional | Language floor: `metal30`, `metal31`, `metal32` (default), `metal40`, `metal41`. Gates language features only |

One artifact per `(backend, arch)` pair. Arch identifiers are validated against a table maintained outside the grammar; aliases (`gfx11`, `sm90`, `metal3.2`) are rejected.

---

## 4. Types

### 4.1 Scalars

Integers `i1` (boolean), `i8`, `i16`, `i32`, `i64`. No `i128`. Floats `f16`, `bf16`, `f32`, `f64`. Pointers are 64-bit on every backend and always space-qualified.

### 4.2 Float availability

| Type | ptx | amdgcn | msl |
| --- | --- | --- | --- |
| `f16`, `f32` | ✅ | ✅ | ✅ |
| `bf16` | sm_80+ | gfx90a+ | `metal31`+ |
| `f64` | ✅ | ✅ | ❌ |

Anything not universally ✅ is gated (§4.3).

### 4.3 Capability gating

| Gated feature | Unavailable where |
| --- | --- |
| `bf16` type | `ptx` < `sm_80`; `amdgcn` < `gfx90a`; `msl` < `metal31` |
| `f64` type | all `msl` artifacts |
| `subgroup_size` attribute | all `msl` artifacts (§9.2) |

1. A kernel **uses** a gated feature if it appears in its signature, its `group` declarations, its body, or the body of any `func` reachable from it. Use is transitive over the call graph.
2. A kernel using a gated feature is **excluded** from every artifact where that feature is unavailable: not emitted, no metadata symbol there.
3. A `func` is emitted into an artifact if any kernel emitted there reaches it; unreached `func`s are dropped.
4. A kernel excluded from **every** declared artifact is a **verification error**, not a silent drop.
5. Availability is recorded per kernel in metadata; the launcher returns `GVIR_ERR_UNAVAILABLE` for a kernel the active backend lacks.

This is the only gating mechanism; there is no module-wide rejection.

### 4.4 Vectors

`vec[T,N]`, `N ∈ {2,3,4}`, `T` a non-`i1` scalar.

* `sizeof(vec[T,N]) = next_pow2(N) * sizeof(T)`, aligned to the same. `vec[f32,3]` is **16 bytes, 16-byte aligned**.
* **Element 3 of a `vec[T,3]` is padding**: `extract`/`insert` with `k = 3` on a width-3 vector is a verification error.
* Arithmetic is elementwise; it scalarizes where there is no vector ALU.
* Comparisons yield `vec[i1,N]` (§4.5).

### 4.5 Predicate vectors

`vec[i1,N]` is producible only by a vector comparison and is value-only.

**Legal as:** a value binding; an operand to `select`, `extract`, `and`/`or`/`xor`/`not`, and `any`/`all` (after `extract`); a `func` parameter or return type.

**Illegal as:** stored or loaded, `bitcast` source or destination, a `struct`/`array` member, a kernel parameter, a `const` type, or written via the `vec-type` production. To materialize one in memory, `select` it into `vec[i8,N]` or `vec[i32,N]`.

### 4.6 `submask`

Opaque per-subgroup lane mask, produced by `ballot`, consumed by `mask_*`. Legal only as a value binding, an operand to a `mask_*`/`ballot` opcode, and a `func` parameter or return type. It may not be stored, loaded, `bitcast`, placed in a struct or array, used as a kernel parameter, or used as a `const` type.

### 4.7 Aggregates

`struct ident` and `array[T,N]` are **memory-only** and never held in a named value. Fields lay out in declaration order, naturally aligned, padded to the largest member alignment. Arrays have no inter-element padding. Layout is identical on all backends and defined here, not inherited from a C ABI.

Structs are legal as kernel parameters (§6.2), `alloca`/`group` types, and pointees. Arrays are legal as `alloca`/`group` types and pointees, but not as kernel parameters or `func` return types.

---

## 5. Address Spaces

| Space | Lifetime | Shared by |
| --- | --- | --- |
| `global` | Launch | Whole grid |
| `group` | Group | One group |
| `private` | Thread | One thread |
| `constant` | Launch | Whole grid, read-only |

* `bitcast` between differently-qualified pointers is a verification error, as is comparing them.
* No generic pointer in v1. A value's space is fixed at first binding and never reassigned (§7.3).
* A `store`, `memset`, `memcopy` destination, or any atomic through `ptr[constant]` is a verification error.
* Function parameters may take pointers in any space; the space is part of the signature.

---

## 6. Kernels and Functions

### 6.1 Kernels

Dispatchable entry points. They implicitly return `void`; `return` inside a kernel takes no operand.

| Attribute | Meaning | Enforcement |
| --- | --- | --- |
| `group_size X,Y,Z` | Exact required group shape | Normative; a different launch shape is UB (§12.7) |
| `max_group_size N` | Upper bound on `X*Y*Z` | Normative |
| `min_groups_per_unit N` | Occupancy hint | **Advisory**; never affects semantics |
| `subgroup_size N` | Require a specific subgroup width | Normative; gated (§4.3, §9.2) |
| `dynamic_group name [align N]` | Launch-sized group allocation | Binds `name : ptr[group]` in the entry block; at most one per kernel |

On `msl`, `group_size` lowers to a thread-count bound only and the host launcher enforces the exact shape at dispatch. A kernel with neither `group_size` nor `max_group_size` has no compile-time shape contract; the host supplies group dimensions at launch in all cases.

### 6.2 Kernel parameters

**Permitted:** `ptr[global]`, `ptr[constant]`, integer and float scalars, vectors, structs by value.
**Not permitted:** `ptr[group]` (use `group` decls or `dynamic_group`), `ptr[private]`, `array`, `submask`, `vec[i1,N]`, `void`.

Parameters are numbered `0..N-1` in declaration order. That index — not a byte offset — is the portable identity of a parameter.

### 6.3 Packed kernarg layout (all backends)

Explicit arguments lay out sequentially in declaration order, each at the next offset satisfying its natural alignment. Pointers occupy 8 bytes; structs use §4.7 layout; vectors use §4.4 layout.

**Hidden trailer.** After the last explicit argument, at its next natural offset, on **every** backend:

| Hidden field | Type | Present when |
| --- | --- | --- |
| `dynamic_group_size` | `i32` | the kernel declares `dynamic_group` |

The buffer is aligned to `max(8, largest member alignment)`; trailing padding rounds size up to that alignment.

On `msl` this layout is realized as a single argument buffer at `[[buffer(0)]]` whose offsets and padding are **defined by this section**, not by Metal's struct rules. It requires **Argument Buffers Tier 2**; pipeline creation fails on Tier 1 and the launcher reports `GVIR_ERR_UNAVAILABLE`.

### 6.4 Functions

Device-side helpers. Direct calls only, to previously-defined `func`s. Recursion, mutual recursion, indirect calls, and taking a function's address are illegal. `readonly` asserts the function writes through no pointer reachable from its arguments; violating it is UB (§12.10).

**Return types:** integer and float scalars, `vec`, `vec[i1,N]`, `ptr[*]`, `submask`, `void`. Not `struct` or `array` (§4.7) — an aggregate result must be written through a pointer parameter.

Functions may not contain `group` declarations and may not use `dynamic_group` names not passed in as parameters.

### 6.5 Resource limits

Checked at lowering time. Exceeding a limit is a **lowering error** for that artifact — not UB, not a gated exclusion.

| Resource | ptx | amdgcn | msl |
| --- | --- | --- | --- |
| Static `group`, and static + `dynamic_group` | 48 KiB default, higher opt-in per arch | 64 KiB per workgroup | 32 KiB |
| Entry-block `alloca` total | implementation scratch limit | implementation scratch limit | implementation scratch limit |

Keep static `group` at or under **32 KiB** to lower everywhere. `dynamic_group` sizing is a host responsibility; the launcher rejects an oversized request with `GVIR_ERR_RESOURCES`.

---

## 7. Blocks, Control Flow, and the Join Convention

### 7.1 Blocks

An unlabelled entry block followed by labelled blocks, each ending in exactly one terminator. Labels are body-scoped. **The entry block cannot be branched to.** The CFG must be **reducible**: every cycle has exactly one entry block.

**Forward progress is not assumed.** A non-terminating loop is well defined; it hangs. Backends may not delete or reorder around a side-effect-free loop on the assumption that it terminates.

### 7.2 Merge annotations

* A block whose terminator is `br_if` or `switch` with **more than one distinct successor** carries exactly one `merge` or `loop_merge`, on the line immediately after its label.
* `merge L` — the selection headed here reconverges at `L`.
* `loop_merge Lexit, Lcontinue` — this block is a loop header; `Lexit` is where control goes when the loop ends, `Lcontinue` is the latch target.
* `Lexit` must be dominated by the header; `Lcontinue` must dominate the back-edge source.
* A block may be the continue target of at most one loop and the merge target of at most one selection. Merge blocks are not shared between two selections, but a loop's `Lcontinue` may serve as the `merge` target of selections nested in that loop's body.
* `return` and `unreachable` may appear inside a construct without reaching its merge block.

```text
loop_head:
  loop_merge loop_exit, loop_latch
  c = slt.i32 i, n
  br_if c, loop_body, loop_exit

loop_body:
  merge loop_latch          // permitted: loop_latch is this loop's continue target
  d = eq.i32 i, 7
  br_if d, skip, work
```

### 7.3 Join Convention

No phi nodes. Values merge across blocks by same-name assignment.

1. `name = op ...` binds on first occurrence and updates thereafter, in any block.
2. The first assignment permanently fixes the type; parameters count as entry-block assignments. **Address space is part of a pointer's type** — a name bound to `ptr[group]` can never become `ptr[global]`.
3. Reading a name is valid only if every path from the entry block assigns it first; otherwise a verification error. There is no `undef` at the IR level.
4. Loop-carried values need no special form: assign before the loop, reassign in the body.
5. `submask` and `vec[i1,N]` bindings follow the same rules but **may not cross a `barrier`**.

### 7.4 Uniformity

Control flow is *not* required to be uniform. `barrier` and the subgroup collectives are the only constructs with uniformity requirements (§10).

---

## 8. Memory

### 8.1 `alloca`

```text
p = alloca.f32
q = alloca.array[f32, 64] align 16
```

Legal **only in the entry block, before any other instruction**, and only with a statically-sized type. Yields `ptr[private]`. The sum of a kernel's allocas is its reported per-thread scratch requirement.

### 8.2 Group memory

```text
kernel reduce(out ptr[global], n i32) group_size 256,1,1 dynamic_group spill:
  group tile array[f32, 256] align 16
  group flag i32
```

`group` declarations are kernel-scoped and statically sized; **zero-initialization is not guaranteed**. Naming one in an operand position yields its address as `ptr[group]`.

`dynamic_group name` declares one additional allocation sized at launch. Its byte size is read via the `dynamic_group_size` builtin (§9.1), which reads the hidden kernarg field of §6.3. Access past that size is UB (§12.9). On `msl` the dynamic allocation is a separate threadgroup-space parameter, not reachable through the argument buffer; only its length travels there.

### 8.3 Access

* `load.<T> ptr` → value. `store.<T> ptr, value` (**destination first**).
* `load_vol` / `store_vol` — volatile, *not* atomic.
* `memcopy dst, src, len` — operands must not overlap (§12.4). `memmove` is overlap-safe. `memset dst, byte, len`.
* `index.ptr p, offset` — byte pointer arithmetic, `i64` offset, wraps normally, keeps `p`'s space.
* `field.ptr p, k` — address of field `k` (literal index) of the pointed-to struct.
* `extract.<T> v, k` / `insert.<T> v, k, x` / `splat.<T> x` / `shuffle.<T> a, b, mask...`.

`align N` may be attached to `load`, `store`, `load_vol`, `store_vol`, and atomics; absent, natural alignment is assumed. Violating either is UB (§12.3).

**Uninitialized reads** of `private` or `group` memory yield an unspecified but *frozen* value. Not poison, no UB propagation.

---

## 9. Execution Builtins

Builtins take no operands. Positional and extent builtins accept an optional `.x`/`.y`/`.z`.

**Result widths.** Dimension-suffixed → `i32`. Unsuffixed positional or extent → **`i64`**. `threads_per_subgroup`, `subgroups_per_group`, `dynamic_group_size` → `i32`.

**Linearization (normative).** For components `(px,py,pz)` and corresponding extent `(ex,ey,ez)`, the unsuffixed positional form is `px + py*ex + pz*ex*ey`; the unsuffixed extent form is `ex*ey*ez`. Both computed in `i64`.

### 9.1 Table

| Positional | Range |
| --- | --- |
| `thread_in_grid` | `[0, threads_per_grid)` |
| `thread_in_group` | `[0, threads_per_group)` |
| `group_in_grid` | `[0, groups_per_grid)` |
| `thread_in_subgroup` | `[0, threads_per_subgroup)` |
| `subgroup_in_group` | `[0, subgroups_per_group)` |

| Extent | Value |
| --- | --- |
| `threads_per_group` | Launched group dimensions |
| `groups_per_grid` | Launched grid dimensions, in groups |
| `threads_per_grid` | Componentwise `groups_per_grid × threads_per_group` |
| `threads_per_subgroup` | Hardware subgroup width (no suffix) |
| `subgroups_per_group` | `ceil(threads_per_group / threads_per_subgroup)` (no suffix) |
| `dynamic_group_size` | Bytes provisioned for the `dynamic_group` allocation (no suffix) |

`thread_in_subgroup`, `subgroup_in_group`, `threads_per_subgroup`, `subgroups_per_group`, and `dynamic_group_size` reject all dimension suffixes.

### 9.2 Subgroup width

`threads_per_subgroup` is a **runtime value, not a constant**: 32 on NVIDIA, 64 on AMD CDNA, 32 or 64 on RDNA (wave32 default), runtime-determined on Apple.

`subgroup_size N` requests a specific width. It selects wave-size mode on `amdgcn`; on `ptx` it is checked against the fixed width 32 and rejected if `N ≠ 32`; on `msl` it is not expressible and is therefore gated (§4.3). Code needing a specific width on Apple must query `threads_per_subgroup` and branch.

---

## 10. Synchronization, Atomics, and Collectives

### 10.1 Barriers

```text
barrier.group           // execution: group, memory: group  (default)
barrier.group, grid     // execution: group, memory: grid
barrier.subgroup, none  // execution only, no memory ordering
```

`barrier.<exec-scope>[, <mem-scope>]`. Execution scope is `subgroup` or `group`. Memory scope defaults to the execution scope and may be `none`, `subgroup`, `group`, or `grid`. Both scopes require **uniform reachability** within their execution scope; non-uniform arrival is UB (§12.8).

### 10.2 Atomics

```text
old = atomic_add.i32 p, v, group
old = cmpxchg.i32 p, expected, desired, grid
fence group, release
```

Every atomic carries a **scope** (`subgroup`, `group`, `grid`) as its final operand. **Atomics are `relaxed` in v1** and carry no ordering operand; ordering is expressed separately with `fence`:

```text
// release store                 // acquire load
fence group, release             x = atomic_load.i32 p, group
atomic_store.i32 p, v, group     fence group, acquire
```

Opcodes: `atomic_load`, `atomic_store`, `atomic_add`, `atomic_sub`, `atomic_and`, `atomic_or`, `atomic_xor`, `atomic_xchg`, `atomic_umin`, `atomic_umax`, `atomic_smin`, `atomic_smax`, `cmpxchg`, `fence`.

* **`cmpxchg.<T> p, expected, desired, scope` yields the old value at `p`**, not a success flag. Test with `eq.<T> old, expected`. The exchange happens iff the old value compared equal; spurious failure does not occur.
* Legal on `i32`, `i64`, `ptr[*]`. `atomic_add` additionally on `f32`, and on `f64` where available (§4.3).
* **Natural alignment is mandatory**; misalignment is UB (§12.3).
* Legal on `global` and `group` pointers; illegal on `constant` and `private`.
* `fence scope, ordering` with `relaxed`, `acquire`, `release`, `acqrel`, or `seqcst`. `fence relaxed` is a no-op.

### 10.3 Subgroup collectives

All require **uniform reachability**: every thread in the subgroup must reach the same collective. Non-uniform arrival is UB (§12.8).

| Opcode | Signature | Notes |
| --- | --- | --- |
| `shuffle.<T>` | `v, lane` → `T` | `lane` need not be uniform |
| `shuffle_xor.<T>` | `v, mask` → `T` | Butterfly |
| `shuffle_up.<T>` / `shuffle_down.<T>` | `v, delta` → `T` | Shifted-out lanes return the source value |
| `broadcast.<T>` | `v, lane` → `T` | `lane` must be uniform |
| `broadcast_first.<T>` | `v` → `T` | Value from the lowest active lane |
| `any` / `all` | `i1` → `i1` | |
| `ballot` | `i1` → `submask` | |
| `sub_add`/`sub_min`/`sub_max`/`sub_and`/`sub_or`/`sub_xor` `.<T>` | `v` → `T` | Full-subgroup reduction |

`submask` ops: `mask_count m` → `i32`, `mask_test m, lane` → `i1`, `mask_first m` → `i32`, `mask_empty m` → `i1`; constants `mask_lt`, `mask_le`, `mask_gt`, `mask_ge`, `mask_eq`.

`sub_add.<T>` on floating-point `T` is **not** order-stable: reduction order is unspecified and may differ between backends and between runs. Under `strict` the individual additions remain IEEE; only their order is unspecified.

---

## 11. Instructions

### 11.1 Integer

`add`, `sub`, `mul`, `neg`, `abs`, `udiv`, `sdiv`, `urem`, `srem`, `umulh`, `smulh`, `umin`, `umax`, `smin`, `smax`. Wrapping is modulo 2^N and always defined.

* `sdiv` truncates toward zero; `srem` takes the sign of the dividend, so `(a/b)*b + a%b == a`.
* **Division and remainder by zero, and `sdiv`/`srem` of `INT_MIN` by `-1`, yield `0`.**

### 11.2 Bitwise and shifts

`and`, `or`, `xor`, `not`, `shl`, `lshr`, `ashr`, `rotl`, `rotr`, `ctlz`, `cttz`, `popcnt`, `brev`, `bswap`.

* Shift and rotate counts mask to the operand bit width.
* **`ctlz`/`cttz` of zero yield the operand bit width.**

### 11.3 Float

`add`, `sub`, `mul`, `div`, `neg`, `abs`, `sqrt`, `fma`, `min`, `max`, `floor`, `ceil`, `round`, `round_even`, `trunc_f`, `copysign`, `isnan`, `isinf`.

Under `float_profile strict`: IEEE-754-2019, round-to-nearest-ties-to-even. `fma` is the only contracted operation and must be written explicitly.

* **`min`/`max` are NaN-quieting** (IEEE `minNum`/`maxNum`): one NaN operand returns the other; two return a quiet NaN.
* **`min(+0.0,-0.0)` and `max(+0.0,-0.0)` may return either operand** — the sign of a zero result is unspecified.
* **`round` rounds half away from zero; `round_even` rounds half to even.**

### 11.4 Comparisons

Integer: `eq`, `ne`, `ult`, `ule`, `ugt`, `uge`, `slt`, `sle`, `sgt`, `sge`.
Float: `oeq`, `one`, `olt`, `ole`, `ogt`, `oge`, `ord`, `ueq`, `une`, `unord`.
Pointer: `eq.ptr`, `ne.ptr`, `ult.ptr`, … — same address space only.
On a `vec[T,N]` operand the result is `vec[i1,N]`.

### 11.5 Conversions

`trunc.<iN>`, `sext.<iN>`, `zext.<iN>`, `fptrunc.<fN>`, `fpext.<fN>`, `stoint.<iN>`, `utoint.<iN>`, `inttos.<fN>`, `inttou.<fN>`, `bitcast.<T>`.

**Float-to-int is saturating and total** — one family, not two:

| Input | Result |
| --- | --- |
| In range | Value rounded toward zero |
| `> destination max`, or `+Inf` | Destination maximum |
| `< destination min`, or `-Inf` | Destination minimum |
| `NaN` | `0` |

`bitcast` requires identical bit widths and is illegal on `submask`, on `vec[i1,N]`, and between pointers of differing address spaces.

### 11.6 Approximate math (`float_profile bounded` only)

`rcp`, `rsqrt`, `sin`, `cos`, `exp2`, `log2`, `tanh`. Illegal under `strict`. `bounded` also enables FMA contraction (`mul`+`add` → FMA). The profile is module-wide.

Accuracy floors for `f32`:

| Opcode | Bound | Domain |
| --- | --- | --- |
| `rcp` | 1.0 ULP | all finite non-zero |
| `rsqrt` | 2.0 ULP | `x > 0` |
| `exp2` | 2.0 ULP | all finite |
| `log2` | 3.0 ULP | `x > 0` |
| `sin`, `cos` | absolute error ≤ 2⁻²⁰ | `|x| ≤ π` |
| `tanh` | 8.0 ULP | all finite |

Outside a listed domain the result is unspecified but finite-or-NaN, not UB. `sin`/`cos` argument reduction quality is unspecified for large arguments.

### 11.7 Select, calls, control

`select.<T> c, a, b` yields `a` where `c` is true, else `b`. For scalar `T`, `c` is `i1`. For `T = vec[U,N]`, `c` is `i1` (whole-vector) or `vec[i1,N]` (elementwise). `T` may be any value type including `ptr[*]` (same space both arms) and `submask`. **Both arms are evaluated**; `select` is not control flow and has no short-circuit guarantee.

`call f, a, b, ...` — direct only. `br`, `br_if`, `switch`, `return`, `unreachable`.

---

## 12. Exhaustive Undefined Behavior

Exactly **10** triggers. Nothing else in this specification is undefined.

**§12.1** Accessing memory outside a live object's bounds.
**§12.2** Using a `ptr[private]` derived from `alloca` after its thread exits.
**§12.3** Violating declared or natural alignment, including atomic natural alignment.
**§12.4** Overlapping `memcopy` operands.
**§12.5** A data race: concurrent access, at least one write, at least one non-atomic, without sufficient barrier ordering or atomic scope.
**§12.6** Executing an `unreachable` instruction.
**§12.7** Launching a kernel with a group shape contradicting its `group_size` or `max_group_size`. (Argument count/type mismatch is *not* here — the generated launcher is typed, making that a host compile-time error.)
**§12.8** Reaching a `barrier` or a subgroup collective non-uniformly across its scope.
**§12.9** Accessing the `dynamic_group` allocation beyond the byte count provisioned at launch.
**§12.10** Writing through any pointer reachable from the arguments of a `readonly` function.

§12.7 and §12.9 are host-contract conditions; `.gvir` is device-only and cannot check them.

### 12.11 Explicitly *not* UB

Integer wraparound; masked shift and rotate counts; integer division or remainder by zero (yields `0`); `ctlz`/`cttz` of zero (yields bit width); all IEEE float results including NaN and Inf; float-to-int out of range (saturates; NaN yields `0`); the sign of a zero result from `min`/`max` (unspecified); floating-point `sub_add` reduction order (unspecified); `index.ptr`/`field.ptr` address wraparound; reading uninitialized `private` or `group` memory (frozen unspecified value); a non-terminating loop; non-uniform control flow generally; pointer comparison within an address space.

---

## 13. Conformance

A backend conforms if, for every module it accepts, every observable result is one this document permits. "Observable" means the final contents of `global` memory and the values passed to atomics visible at `grid` scope.

Conformance is established by a **normative differential test suite**:

* **Semantic core.** Every §11 opcode over every legal type, including every boundary case §12.11 pins down. All backends agree bit for bit, or within an explicitly-unspecified result set.
* **Layout.** For every kernel signature shape, the kernarg buffer for all three backends must be byte-identical (§6.3), checked by comparing buffers.
* **Control flow.** Every merge shape §7.2 permits — multi-exit loops, `switch` with shared default, `return` from a nested construct — structurized for all backends and run with divergent inputs.
* **Gating.** Each §4.3 feature produces exactly the expected artifact set and metadata, and `GVIR_ERR_UNAVAILABLE` on an excluded backend.
* **Uniformity.** Negative tests that the verifier rejects statically-detectable §12.8 violations; where detection is impossible, the suite documents it as such.

An implementation passing semantic core and layout but not the others is *partially conforming* and must say which groups it fails.