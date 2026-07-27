# `gvir_spec.md`

# Vertex GPU IR — Language Specification (v1.0)

**File Extensions:** `.gvir` (text), `.gvbyte` (binary).

Vertex GPU IR is a device-only compute-kernel intermediate representation for SIMT hardware. It targets NVIDIA (via PTX), AMD (via amdgcn), and Apple (via MSL) from a single module.

`.gvir` is a **sibling** of Vertex IR `.vir` v2.0, not a subset. It borrows the lexical conventions, the block/terminator shape, the Join Convention, and the exhaustive-UB discipline because those transfer cleanly. It does **not** inherit `.vir`'s type system, opcode table, memory model, or trap semantics — GPUs differ enough that silent inheritance would be a lie. Every place the two diverge is listed in `gvir_arch.md` §9.

---

## 1. Scope & Design Principles

* **SIMT, device-only.** No host entry point, no host calls, no host globals, no I/O, no allocation, no recursion.
* **Three backends, one semantics.** An opcode means the same thing on all targets or it is not in the IR. Where hardware genuinely differs (subgroup width), the difference is *named and queryable*, never silent.
* **Explicit address spaces.** There is no generic/flat pointer in v1. Every pointer's space is part of its type.
* **Annotated CFG.** Blocks are flat and labelled, but every divergent construct declares its reconvergence point. This makes MSL structurization and amdgcn EXEC-mask expansion mechanical passes rather than analyses.
* **Static footprint.** Per-thread private memory and per-group shared memory are both statically bounded per kernel (dynamic group memory excepted, and it is explicitly declared). No unbounded stacks.
* **Traps do not exist.** GPUs have no cheap per-lane fault. Everything `.vir` traps on (such as division by zero) is provided deterministic fallback semantics to eliminate safety overhead in the frontend compiler.
* **Exhaustive UB.** 10 triggers, enumerated in §12. Nothing else is undefined.

---

## 2. Module Grammar & Order

Modules are line-oriented with no separators or continuations. Indentation is conventional. Sections appear in strict order to permit one-pass verification.

1. **Header** — `module ident` (exactly once)
2. **Targets** — `target ...` (exactly once)
3. **Float profile** — `float_profile ...` (optional, defaults to `strict`)
4. **Declarations** — `struct` → `const`
5. **Definitions** — `func` → `kernel`

```text
module          := module-header
                   target-decl
                   float-profile-decl?
                   struct-decl*
                   const-decl*
                   func-def*
                   kernel-def*

module-header   := "module" ident
target-decl     := "target" backend ("," backend)*
backend         := "ptx" arch-list?
                 | "amdgcn" arch-list
                 | "msl" arch-list?
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
vec-type        := "vec" "[" (int-type | float-type) "," vec-width "]"
vec-width       := "2" | "3" | "4" | "8" | "16"
array-type      := "array" "[" type "," int-literal "]"
ptr-type        := "ptr" "[" addr-space "]"
addr-space      := "global" | "group" | "private" | "constant"

kernel-def      := "kernel" ident "(" kparam-list? ")" kernel-attr* ":"
                   group-decl*
                   entry-block block* "end"
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

func-def        := "func" ident "(" param-list? ")" type func-attr* ":"
                   entry-block block* "end"
param-list      := param ("," param)*
param           := ident type
func-attr       := "inline" | "noinline" | "readonly"

entry-block     := alloca-line* body-line* terminator
block           := label-line merge-decl? body-line* terminator
label-line      := ident ":"
merge-decl      := "merge" label
                 | "loop_merge" label "," label
alloca-line     := ident "=" "alloca" "." type ("align" int-literal)?
body-line       := inst | loc-line
loc-line        := "loc" string-literal int-literal int-literal?

inst            := ident "=" op operand-list? align-clause?
                 | op operand-list? align-clause?
                 | builtin-inst
                 | barrier-inst
builtin-inst    := ident "=" builtin-name dim-suffix?
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
                 | "return" operand?
                 | "unreachable"

operand         := ident | literal | type | ordering | scope
literal         := int-literal | float-literal | bool-literal | "null"
int-literal     := "-"? [0-9]+
float-literal   := "-"? [0-9]+ "." [0-9]+ ("e" "-"? [0-9]+)? | "NaN" | "Inf" | "-Inf"
bool-literal    := "true" | "false"

```

**Lexical:** identifiers `[A-Za-z_][A-Za-z0-9_]*`, no sigils. Comments `//` to end of line. Flat module-wide namespace, no shadowing, declare-before-use, no forward references.

**Note on string literals.** `.gvir` has none. There is no printf, no debug string, no symbol reference that needs one — except `loc`, which is the sole production accepting a string.

---

## 3. Targets

```text
target ptx[sm_80, sm_90], amdgcn[gfx1100, gfx942], msl[metal32]

```

| Backend | Arch list | Meaning |
| --- | --- | --- |
| `ptx` | optional | Minimum SM per listed arch; omitted means `sm_70` and JIT-forward. PTX ships as text and the driver JITs, so pinning is a choice. |
| `amdgcn` | **required** | One artifact per `gfx` target. The target is baked into the ELF; there is no JIT fallback, so there is nothing to default to. |
| `msl` | optional | Metal language floor: `metal30`, `metal31`, `metal32` (default), `metal40`, `metal41`. MSL has no in-source target-architecture concept — GPU-family gating happens at pipeline creation — so this gates language features only. |

A module emits one artifact per `(backend, arch)` pair. Arch identifiers are validated against a table maintained outside the grammar; the IR never accepts an alias (`gfx11`, `sm90`, `metal3.2` are all rejected).

---

## 4. Types

### 4.1 Scalars

* **Integers:** `i1` (boolean), `i8`, `i16`, `i32`, `i64`. No `i128` — no backend has it.
* **Floats:** `f16`, `bf16`, `f32`, `f64`.
* **Pointers:** always 64-bit, on every backend, always explicitly space-qualified.

### 4.2 Float availability

| Type | ptx | amdgcn | msl | Rule |
| --- | --- | --- | --- | --- |
| `f16` | ✅ | ✅ | ✅ | Always legal. |
| `f32` | ✅ | ✅ | ✅ | Always legal. |
| `bf16` | sm_80+ | gfx90a+ | `metal31`+ | Legal only if every declared `(backend, arch)` supports it. |
| `f64` | ✅ | ✅ | ❌ | **Illegal per-kernel/function.** Using `f64` operations or parameters in a kernel/function fails lowering for `msl` targets specifically without invalidating non-MSL kernels in the same module. |

### 4.3 Vectors

`vec[T, N]` with `N ∈ {2, 3, 4, 8, 16}` and `T` scalar (not `i1`, not `submask`). Arbitrary widths are rejected: only these lower to something on all three backends.

* **Size and alignment:** `sizeof(vec[T,N]) = N * sizeof(T)`, aligned to `sizeof(T) * next_pow2(N)`. So `vec[f32,3]` is **12 bytes, 16-byte aligned** — matching MSL `float3`, not `packed_float3`.
* **Semantics:** vector arithmetic is elementwise. On backends without vector ALUs (ptx, amdgcn) it scalarizes; `vec` there is a layout and convenience construct, not a performance promise. Only MSL has genuine vector ALU ops.
* Comparisons on `vec[T,N]` yield `vec[i1,N]`.

### 4.4 `submask`

An opaque per-subgroup lane mask, produced by `ballot` and consumed by the `mask_*` opcodes. It exists because subgroup width is 32 on RDNA and NVIDIA, 64 on CDNA, and opaque on Metal — an `i32` or `i64` ballot result would be a portability trap.

`submask` is legal **only** as a value binding, an operand to a `mask_*`/`ballot` opcode, and a `func` parameter or return type. It may not be stored, loaded, `bitcast`, placed in a struct or array, used as a kernel parameter, or used as a `const` type.

### 4.5 Aggregates

`struct ident` and `array[T, N]` are **memory-only**; they are never held in a named value. Struct fields are laid out in declaration order, naturally aligned, padded to the largest member alignment. Arrays have no inter-element padding. Layout is identical across all backends — gvir defines it, it is not inherited from a C ABI.

Structs are legal as kernel parameters (see §6.2), as `alloca`/`group` types, and as pointees.

---

## 5. Address Spaces

| Space | Lifetime | Shared by | ptx | amdgcn | msl |
| --- | --- | --- | --- | --- | --- |
| `global` | Launch | Whole grid | `.global` | `global_*` | `device` |
| `group` | Group | One group | `.shared` | LDS / `ds_*` | `threadgroup` |
| `private` | Thread | One thread | `.local` | scratch | `thread` |
| `constant` | Launch | Whole grid, read-only | `.const` | read-only global | `constant` |

* **No conversion.** `bitcast` between differently-qualified pointers is a verification error. So is comparing them.
* **No generic pointer** in v1. A value's space is fixed at its first binding and can never be reassigned to another space (§7.3).
* **`constant` is read-only.** A `store`, `memset`, `memcopy` destination, or any atomic through `ptr[constant]` is a verification error.
* **Function parameters** may take pointers in any space; the space is part of the signature.

---

## 6. Kernels and Functions

### 6.1 Kernels

Kernels are dispatchable entry points. They implicitly return `void`; `return` inside a kernel takes no operand.

**Attributes:**

| Attribute | Meaning | Enforcement |
| --- | --- | --- |
| `group_size X,Y,Z` | Exact required group shape | Normative. Launching a different shape is UB (§12.7). |
| `max_group_size N` | Upper bound on `X*Y*Z` | Normative. |
| `min_groups_per_unit N` | Occupancy hint | **Advisory.** Lowers to `.minnctapersm` / `amdgpu-waves-per-eu` / dropped on MSL. Never affects semantics. |
| `subgroup_size N` | Require a specific subgroup width | Normative where expressible; see §9.2. |
| `dynamic_group name [align N]` | Declares a launch-sized group allocation | Binds `name : ptr[group]` in the entry block. At most one per kernel. |

`group_size` is exactly representable on ptx (`.reqntid`) and amdgcn (`reqd_work_group_size`). On `msl` targets, backends emit the product as `max_total_threads_per_threadgroup`.

### 6.2 Kernel parameters

Permitted types: `ptr[global]`, `ptr[constant]`, integer and float scalars, vectors, and structs by value. Not permitted: `ptr[group]` (use `group` decls or `dynamic_group`), `ptr[private]`, `array`, `submask`, `void`.

Parameters are numbered `0..N-1` in declaration order. That index — not a byte offset — is the portable identity of a parameter:

| Backend | Binding model | Index maps to |
| --- | --- | --- |
| `ptx` | Packed kernarg buffer (§6.3) | Buffer offset |
| `amdgcn` | Packed kernarg buffer (§6.3) | Buffer offset |
| `msl` | **Synthesized Argument Buffer** | `[[buffer(0)]]` offset |

### 6.3 Packed kernarg layout (All Backends)

Arguments are laid out sequentially in declaration order, each at the next offset satisfying its natural alignment; the buffer as a whole is aligned to `max(8, largest member alignment)`. Trailing padding rounds the size up to the buffer alignment. Pointers occupy 8 bytes. Structs use §4.5 layout.

For `msl`, the compiler automatically generates a unified Metal Argument Buffer mapped to `[[buffer(0)]]`, matching HSA packed kernargs for layout parity across all backends.

### 6.4 Functions

Device-side helpers. Direct calls only, to previously-defined `func`s. Recursion, mutual recursion, indirect calls, and taking a function's address are all illegal. `readonly` asserts the function writes through no pointer reachable from its arguments; violating it is UB (§12.10).

Functions may return any type except `array` and `void`-in-operand-position. They may not contain `group` declarations and may not use `dynamic_group` names not passed in as parameters.

---

## 7. Blocks, Control Flow, and the Join Convention

### 7.1 Blocks

A function or kernel body is an unlabelled entry block followed by labelled blocks, each ending in exactly one terminator. Labels are body-scoped. The entry block cannot be branched to.

The CFG must be **reducible**: every cycle has exactly one entry block.

### 7.2 Merge annotations

Every divergent construct names its reconvergence point.

* A block whose terminator is `br_if` or `switch` with **more than one distinct successor** must carry exactly one `merge` or `loop_merge` declaration, on the line immediately after its label.
* `merge L` — the selection construct headed here reconverges at `L`.
* `loop_merge Lexit, Lcontinue` — this block is a loop header; `Lexit` is the block control reaches when the loop ends, `Lcontinue` is the latch target.
* Declared merge and continue blocks must be dominated appropriately: `Lexit` is dominated by the header, `Lcontinue` dominates the back-edge source, and merge blocks are not shared between two constructs.
* `return` and `unreachable` may appear inside a construct without reaching its merge block.

```text
loop_head:
  loop_merge loop_exit, loop_latch
  c = slt.i32 i, n
  br_if c, loop_body, loop_exit

loop_body:
  merge loop_latch
  d = eq.i32 i, 7
  br_if d, skip, work
...

```

### 7.3 Join Convention

No phi nodes. Values merge across blocks by same-name assignment.

1. `name = op ...` binds on first occurrence, updates thereafter, in any block.
2. The first assignment permanently fixes the type. Parameters count as entry-block assignments. **Address space is part of a pointer's type**: a name bound to `ptr[group]` can never be reassigned to `ptr[global]`.
3. Reading a name is valid only if every path from the entry block assigns it first. Reading a name unassigned on some path is a verification error — there is no `undef` at the IR level.
4. Loop-carried values need no special form: assign before the loop, reassign in the body.
5. `submask` bindings follow the same rules but may not cross a `barrier` — a mask is only meaningful for the subgroup membership that produced it.

### 7.4 Uniformity

Control flow is *not* required to be uniform. The `barrier` and subgroup-collective instructions are the only constructs with uniformity requirements (§10).

---

## 8. Memory

### 8.1 `alloca`

```text
p = alloca.f32
q = alloca.array[f32, 64] align 16

```

`alloca` is legal **only in the entry block**, only before any other instruction, and only with a statically-sized type. It yields `ptr[private]`. The sum of a kernel's entry-block allocas is reported as its per-thread scratch requirement.

### 8.2 Group memory

```text
kernel reduce(out ptr[global], n i32) group_size 256,1,1 dynamic_group spill:
  group tile array[f32, 256] align 16
  group flag i32

```

`group` declarations are kernel-scoped, statically sized, and zero-initialization is **not** guaranteed. Naming one in an operand position yields its address as `ptr[group]`.

`dynamic_group name` declares one additional allocation whose size is supplied at launch. Its size in bytes is queryable via the `dynamic_group_size` builtin (§9.1). Reads or writes past that size are UB (§12.9).

|  | Static `group` | `dynamic_group` |
| --- | --- | --- |
| `ptx` | `.shared .align N .b8 tile[1024]` | `extern .shared .align N .b8 name[]` |
| `amdgcn` | `group_segment_fixed_size` | Hidden dynamic-shared kernarg + LDS base |
| `msl` | `threadgroup float tile[256]` local | **Argument Buffer offset** |

### 8.3 Access

* `load.<T> ptr` → value. `store.<T> ptr, value` (**destination first**).
* `load_vol` / `store_vol` — volatile operations. Not atomic.
* `memcopy dst, src, len` — operands must not overlap (UB #4). `memmove` is overlap-safe. `memset dst, byte, len`.
* `index.ptr p, offset` — byte pointer arithmetic, `i64` offset, wraps normally, result keeps `p`'s address space.
* `field.ptr p, k` — address of field `k` of the struct pointed to by `p`. `k` is a literal index.
* `extract.<T> v, k` / `insert.<T> v, k, x` / `splat.<T> x` / `shuffle.<T> a, b, mask...` for vectors.

`align N` may be attached to `load`, `store`, `load_vol`, `store_vol`, and atomics. Absent, natural alignment for the type is assumed; violating either is UB (§12.3).

**Uninitialized reads** of `private` or `group` memory yield an unspecified but *frozen* value. Not poison, no UB propagation.

---

## 9. Execution Builtins

All builtins take no operands and yield `i32`. Positional and extent builtins accept an optional `.x`, `.y`, or `.z` suffix. Without a suffix, positional builtins yield a **flattened linear index** and extent builtins yield the **product** of all three components.

### 9.1 Table

| Positional | Range | ptx | amdgcn | msl |
| --- | --- | --- | --- | --- |
| `thread_in_grid` | `[0, threads_per_grid)` | `ctaid*ntid+tid` | `wgid*size+tid` | `[[thread_position_in_grid]]` |
| `thread_in_group` | `[0, threads_per_group)` | `%tid` | `v_mbcnt`/tid | `[[thread_position_in_threadgroup]]` |
| `group_in_grid` | `[0, groups_per_grid)` | `%ctaid` | `%wgid` | `[[threadgroup_position_in_grid]]` |
| `thread_in_subgroup` | `[0, threads_per_subgroup)` | `%laneid` | `v_mbcnt_lo/hi` | `[[thread_index_in_simdgroup]]` |
| `subgroup_in_group` | `[0, subgroups_per_group)` | derived | derived | `[[simdgroup_index_in_threadgroup]]` |

| Extent | Value |
| --- | --- |
| `threads_per_group` | Launched group dimensions |
| `groups_per_grid` | Launched grid dimensions |
| `threads_per_grid` | Componentwise `groups_per_grid × threads_per_group` |
| `threads_per_subgroup` | Hardware subgroup width (no suffix) |
| `subgroups_per_group` | `ceil(threads_per_group / threads_per_subgroup)` (no suffix) |
| `dynamic_group_size` | Bytes provisioned for the `dynamic_group` allocation (no suffix) |

`thread_in_subgroup`, `subgroup_in_group`, `threads_per_subgroup`, `subgroups_per_group`, and `dynamic_group_size` reject all dimension suffixes.

### 9.2 Subgroup width

`threads_per_subgroup` is a **runtime value**, not a constant. It is 32 on RDNA and NVIDIA, 64 on CDNA (and 32 on RDNA in wave32 mode), and Metal exposes it at runtime.

`subgroup_size N` on a kernel requests a specific width. It lowers to a wave-size mode selection on amdgcn, is checked-and-rejected against the target on ptx (always 32), and is advisory on msl.

---

## 10. Synchronization, Atomics, and Collectives

### 10.1 Barriers

```text
barrier.group           // execution: group, memory: group  (default)
barrier.group, grid     // execution: group, memory: grid
barrier.subgroup, none  // execution only, no memory ordering

```

`barrier.<exec-scope>[, <mem-scope>]`. The execution scope is `subgroup` or `group`. The memory scope defaults to the execution scope and may be `none`, `subgroup`, `group`, or `grid`.

| Backend | Execution | Memory |
| --- | --- | --- |
| `ptx` | `bar.sync 0` | Implied — `bar.sync` is a full CTA memory barrier |
| `amdgcn` | `s_barrier` | Requires `s_waitcnt lgkmcnt(0)` and/or `vmcnt(0)` |
| `msl` | `threadgroup_barrier` | Explicit `mem_flags::mem_threadgroup` / `mem_device` |

Both require **uniform reachability** within their execution scope. Non-uniform arrival is UB (§12.8).

### 10.2 Atomics

```text
old = atomic_add.i32 p, v, group, relaxed
ok  = cmpxchg.i32 p, expected, desired, grid, acqrel, acquire
fence group, release

```

Every atomic carries a **scope** (`subgroup`, `group`, `grid`) immediately before its ordering operand(s).

Opcodes: `atomic_load`, `atomic_store`, `atomic_add`, `atomic_sub`, `atomic_and`, `atomic_or`, `atomic_xor`, `atomic_xchg`, `atomic_umin`, `atomic_umax`, `atomic_smin`, `atomic_smax`, `cmpxchg`, `fence`.

* Legal on `i32`, `i64`, and `ptr[*]`. `atomic_add` additionally on `f32` (and `f64` where supported).
* **Natural alignment is mandatory.** Misalignment is UB.
* Legal on `global` and `group` pointers. Illegal on `constant` and `private`.

### 10.3 Subgroup collectives

All require **uniform reachability**: every thread in the subgroup must reach the same collective. Non-uniform arrival is UB (§12.8).

| Opcode | Signature | Notes |
| --- | --- | --- |
| `shuffle.<T>` | `v, lane` → `T` | `lane` need not be uniform |
| `shuffle_xor.<T>` | `v, mask` → `T` | Butterfly |
| `shuffle_up.<T>` / `shuffle_down.<T>` | `v, delta` → `T` | Shifts out-of-range lanes return source value |
| `broadcast.<T>` | `v, lane` → `T` | `lane` must be uniform |
| `broadcast_first.<T>` | `v` → `T` | Value from lowest active lane |
| `any` / `all` | `i1` → `i1` |  |
| `ballot` | `i1` → `submask` |  |
| `sub_add` / `sub_min` / `sub_max` / `sub_and` / `sub_or` / `sub_xor` `.<T>` | `v` → `T` | Full-subgroup reduction |

`submask` operations: `mask_count m` → `i32`, `mask_test m, lane` → `i1`, `mask_first m` → `i32`, `mask_empty m` → `i1`, plus constants `mask_lt`, `mask_le`, `mask_gt`, `mask_ge`, `mask_eq`.

---

## 11. Instructions

### 11.1 Integer

`add`, `sub`, `mul`, `neg`, `abs`, `udiv`, `sdiv`, `urem`, `srem`, `umulh`, `smulh`, `umin`, `umax`, `smin`, `smax`.

Wrapping is modulo 2^N and always defined. **Division and remainder by zero, and `sdiv`/`srem` of `INT_MIN` by `-1`, yield `0**` — they are defined, deterministic non-trapping operations.

### 11.2 Bitwise and shifts

`and`, `or`, `xor`, `not`, `shl`, `lshr`, `ashr`, `rotl`, `rotr`, `ctlz`, `cttz`, `popcnt`, `brev`, `bswap`.

Shift counts mask to the operand bit width. Never UB.

### 11.3 Float

`add`, `sub`, `mul`, `div`, `neg`, `abs`, `sqrt`, `fma`, `min`, `max`, `floor`, `ceil`, `round`, `trunc_f`, `copysign`, `isnan`, `isinf`.

Under `float_profile strict`: IEEE-754-2019, round-to-nearest-ties-to-even. `fma` is the only contracted operation and must be written explicitly.

### 11.4 Comparisons

Integer: `eq`, `ne`, `ult`, `ule`, `ugt`, `uge`, `slt`, `sle`, `sgt`, `sge`.
Float: `oeq`, `one`, `olt`, `ole`, `ogt`, `oge`, `ord`, `ueq`, `une`, `unord`.
Pointer: `eq.ptr`, `ne.ptr`, `ult.ptr`, etc. — same address space only.

### 11.5 Conversions

Destination-explicit: `trunc.<iN>`, `sext.<iN>`, `zext.<iN>`, `fptrunc.<fN>`, `fpext.<fN>`, `stoint.<iN>`, `utoint.<iN>`, `stoint_sat.<iN>`, `utoint_sat.<iN>`, `inttos.<fN>`, `inttou.<fN>`, `bitcast.<T>`.

**Non-saturating float-to-int out of range (including ±Inf and NaN) yields a clamped minimum/maximum value** of the destination type. It does not trap and is not left unspecified.

`bitcast` requires identical bit widths and is illegal on `submask` or pointers of differing address spaces.

### 11.6 Approximate math (`float_profile bounded` only)

`rcp`, `rsqrt`, `sin`, `cos`, `exp2`, `log2`, `tanh`. Illegal under `strict`.

`bounded` enables FMA contraction (`mul` + `add` → FMA).

Accuracy floors for `f32`:

| Opcode | Bound | Domain |
| --- | --- | --- |
| `rcp` | 1.0 ULP | all finite non-zero |
| `rsqrt` | 2.0 ULP | `x > 0` |
| `exp2` | 2.0 ULP | all finite |
| `log2` | 3.0 ULP | `x > 0` |
| `sin`, `cos` | absolute error ≤ 2⁻²⁰ | ` |
| `tanh` | 8.0 ULP | all finite |

### 11.7 Calls and control

`call f, a, b, ...` — direct only. `br`, `br_if`, `switch`, `return`, `unreachable`.

---

## 12. Exhaustive Undefined Behavior

There are exactly **10** triggers for UB. Nothing else in this specification is undefined.

1. Accessing memory outside a live object's bounds.
2. Using a `ptr[private]` derived from `alloca` after its thread exits.
3. Violating declared or natural alignment, including atomic natural alignment.
4. Overlapping `memcopy` operands.
5. A data race: concurrent access, at least one write, at least one non-atomic, without sufficient barrier ordering or atomic scope.
6. Executing an `unreachable` instruction.
7. Launching a kernel with mismatched argument count or types, or with a group shape contradicting `group_size`/`max_group_size`.
8. Reaching a `barrier` or a subgroup collective non-uniformly across its scope.
9. Accessing the `dynamic_group` allocation beyond the byte count provisioned at launch.
10. Writing through any pointer reachable from the arguments of a `readonly` function.

### 12.1 Explicitly *not* UB

Integer wraparound on `add`/`sub`/`mul`/`neg`/`abs`; masked shift counts; **integer division or remainder by zero (yields `0`)**; all IEEE float results including NaN and Inf; **non-saturating float-to-int out of range (yields clamped max/min)**; `index.ptr`/`field.ptr` address wraparound; reading uninitialized `private` or `group` memory (frozen unspecified value); non-uniform control flow generally; pointer comparison within an address space.