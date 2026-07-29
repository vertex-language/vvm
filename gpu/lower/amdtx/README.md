# `gpu/lower/amdtx`

```go
import lower "github.com/vertex-language/vvm/gpu/lower/amdtx"
```

Lowers a verified `.gvir` device module (`ir/gvir.Module`) to a structured AMDTX IR
module (`gpu/ir/amdtx.Module`). It produces **one artifact per declared `amdgcn`
arch** — `.gvir` §3 gives amdgcn no JIT fallback, so each `gfx` target is a
distinct module, and `LowerAll` returns one `Result` each.

The result is IR, not text. Print it with `gpu/ir/amdtx/encoding/text`, which runs
`amdtx.Verify` first and refuses to print an invalid module.

```go
res, err := lower.Lower(m, "gfx942") // m: *gvir.Module, already ir/verify'd
if err != nil {
    log.Fatal(err) // a §1.1 lowering error: this artifact fails
}
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}
src, err := text.Print(res.Module)
```

> **Naming.** This is `package amdtx`, and it imports `gpu/ir/amdtx` under that
> same name. Go only qualifies *imported* identifiers, so every `amdtx.` in these
> files is the IR package; this package's own identifiers are always unqualified.

---

## 1. Layout

| File | Responsibility |
| --- | --- |
| `amdtx.go` | Entry point (`Lower`, `LowerOptions`, `LowerAll`), `Options`/`Result`, arch → `.target`, `subgroup_size` → `.wave`, the module-level `lowerer`, `.file` table and `const` objects. |
| `gating.go` | §4.3 capability gating: per-kernel feature use over the call graph, kernel exclusion. |
| `types.go` | `gvir.Type` → register class and width, address spaces, memory mnemonics, the zero-extension invariant's helpers. |
| `value.go` | The §7.3 Join Convention: name → register binding, lane vectors, pointee tracking, literal materialization. |
| `callable.go` | Kernel and func lowering: kernarg → `.param` plus the `s_load` prologue, `group` → LDS offsets, `alloca` → private offsets, launch directives. |
| `cfg.go` | The structurizer: annotated reducible CFG → `if` / `loop` / `breakif` region tree. |
| `uniform.go` | The §7.4 analysis, re-derived over the region tree, used only to choose a guard form. |
| `emit.go` | Region tree → `amdtx` items; guard materialization; instruction dispatch. |
| `isel.go` | Arithmetic, bitwise, float, comparisons, conversions, `select`, `call`, and the §9 builtins. |
| `isel_mem.go` | `alloca`, `load`/`store`, `index`/`field`, and the vector opcodes. |
| `isel_sync.go` | Barriers, fences, atomics, subgroup collectives, `submask` operations. |

There is no register allocator and no EXEC-mask expansion here. AMDTX is a
virtual-register IR with structured control flow as a first-class item (**P2**);
allocation, the `accum_offset` split and EXEC save/restore are its own lowering
pipeline's obligations, downstream of this package.

---

## 2. Value Model

**Everything is a VGPR.** §7.3 merges values across blocks by same-name
assignment, which is exactly a virtual register written more than once.
`value.define` binds a name to a register on first assignment and returns the same
register thereafter, so there is no phi insertion and no loop-carried-value case
anywhere in this package. Rebinding a name at a different type is rejected rather
than silently reallocated.

Placing every value in a VGPR is a deliberate refusal to scalarize. Choosing SGPRs
for uniform values means choosing `s_*` opcodes for them, which means the
scalar/vector split leaks into instruction selection, operand legality (V6, V7),
and every `v_*` instruction's one-SGPR-source budget. AMDTX exists so that a pass
below us can make those decisions with physical registers in hand. The cost is
real — see the todos — and a scalarization pass is the single highest-value
addition to this backend.

**`i1` and `submask` are `.lanemask`.** §7.4 makes `.lanemask` the only honest
spelling of a per-lane boolean: `.sgpr.b64` would hard-code wave64 into the IR
(AMDTX §7.4), and the width follows `.wave` instead. `ballot` therefore costs a
register copy and nothing else.

**Vectors are lane vectors.** amdgcn has no vector registers — width lives in the
register *tuple*, which is a memory-access concept, not an arithmetic one — so a
`vec[T,N]` value is `N` registers and every elementwise opcode is emitted `N`
times. `vec[T,3]` keeps three registers; the padding lane (§4.4) is never
materialized, which is consistent with `extract`/`insert` at `k = 3` being a
verification error.

**Pointee tracking.** `.gvir` pointers carry an address space but no pointee type,
so `field.ptr p, k` cannot be lowered from the pointer's type alone. `value`
carries a best-effort `pointee`, propagated from `alloca`, `group` declarations and
`field.ptr`/`index.ptr`. A `field.ptr` through a pointer whose provenance is a
kernel parameter is a lowering error with an explicit message (§9).

---

## 3. Zero-Extension Invariant

A value of type `iN` with `N < 32` lives in a `.vgpr.b32` register holding the
value **zero-extended**. amdgcn's 16-bit ALU forms are a separate operand-size
world with their own register-half addressing, and using them would put a second
representation of `i16` into `value` for no semantic gain.

* Wrapping operations on `i8`/`i16` (`add`, `sub`, `mul`, `neg`, `not`, `shl`,
  `rotl`, `rotr`, `brev`, `bswap`) are followed by `v_and_b32 d, mask, d`. §11.1's
  "wrapping is modulo 2^N and always defined" is therefore literally true at 8 and
  16 bits, not approximately.
* Signed consumers (`sdiv`, `srem`, `ashr`, `abs`, `smin`/`smax`, the `s*`
  compares, `sext`, `inttos`) sign-extend an operand with `v_bfe_i32 t, x, 0, N`
  into a scratch copy that is never written back to the value's own register.
* Shift and rotate counts are masked to `N-1` first. At `N = 32` and `N = 64` the
  hardware already masks, so nothing is emitted (§11.2 agrees with the ISA here).

---

## 4. Address Spaces and Pointers

`.gvir` has no generic pointer, so no `.generic` space and no aperture dispatch
appears anywhere: every access carries the space taken from the pointer's type.

| `.gvir` | AMDTX | Access | Bound by |
| --- | --- | --- | --- |
| `global` | `.global` | `global_load_*` / `global_store_*` | kernel param via the `s_load` prologue |
| `constant` | `.constant` | `global_load_*` | kernel param (see todos: not scalarized) |
| `group` | `.shared` | `ds_load_*` / `ds_store_*` | `group` decl → byte offset into the group segment |
| `private` | `.private` | `scratch_load_*` / `scratch_store_*` | `alloca` → byte offset into the private segment |

**Every pointer value is a `.vgpr.b64` register.** AMDTX §6 gives `.shared` and
`.private` 32-bit pointers, but §6.3 and §4.7 make a stored pointer 8 bytes on
every backend, and `IsStorable` admits `ptr[group]`. Keeping one representation and
slicing `%p[0]` for the LDS and scratch address operands satisfies both: the
register file agrees with the memory image, and V8's "vector memory requires a VGPR
address register" holds by construction.

`group` and `dynamic_group` declarations become byte offsets materialized with
`v_mov_b32`, not module-scope `.shared` objects. Addressing a named object would
need a relocation model AMDTX 1.0 does not define; an offset needs none, and
`.group_segment_size` / `.dynamic_group_segment` record the rest.

---

## 5. Control Flow

`.gvir` §7.2 exists because two of the three backends require structured control
flow. AMDTX is one of them, so the merge annotations are not dropped here the way
they are on PTX — they are the input to `cfg.go`, which rebuilds regions:

* a block carrying `loop_merge Lexit, Lcontinue` opens a `loop`;
* a block carrying `merge L` with two distinct successors opens an `if`;
* a branch to the innermost `Lexit` is a `breakif`, to `Lcontinue` or the header a
  loop back-edge;
* `switch` becomes a chain of equality `if`s. `brx.idx`-style jump tables have no
  structured spelling at all, and the chain is what a sparse case list lowers to
  anyway.

Two peepholes carry their weight, because both cover idioms every kernel has:

* **`if c { break } rest` → `breakif c; rest`.** AMDTX has no unconditional
  `break`, and an unconditional break inside a divergent region is exactly the
  construct that needs the mask arithmetic AMDTX's own structurizer performs. The
  hoisted form is the one AMDTX is designed to consume.
* **`if c { return } else { X } rest` → `if ¬c { X; rest }`.** The bounds-check
  early return is the most common shape in a kernel and is usually divergent;
  `s_endpgm` terminates the whole wave, so it must not appear under a divergent
  guard. Nesting the continuation is the transformation that makes it correct.

Anything else — an early `return` in the middle of a divergent region, a
multi-level break, an early `continue` into a non-empty continue block — is a
lowering error with an explicit message rather than something approximate.

Non-terminating loops are emitted as written; nothing here deletes or reorders
around a side-effect-free cycle (§7.1).

---

## 6. Uniformity and the Guard Form

`uniform.go` re-derives the §7.4 analysis over the region tree. It is normative and
deterministic, so re-deriving it must produce the same classification `ir/verify`
did; the region tree supplies control dependence for free, which is why it is a
hundred lines here and not a dominance frontier computation.

It is consulted for one decision. A group-uniform condition becomes

```text
s_cmp_lg_u64 %scc, %p, 0;      // wave64; s_cmp_lg_u32 under .wave 32
if .uniform %scc { ... }
```

and a divergent one stays a `.lanemask` guard, lowering to EXEC save/and/restore.
Testing the ballot against zero is exact rather than conservative: `v_cmp_*` writes
zero into inactive lanes, so for a condition that is uniform across the active
lanes the mask is non-zero exactly when the condition holds.

This is what keeps `s_barrier` legal. §7.4 already guarantees a barrier's enclosing
selections are group-uniform; lowering those selections to `%scc` guards means
**V21** — `s_barrier` must not appear inside divergent control flow — holds without
a single special case in `isel_sync.go`.

---

## 7. Capability Gating

`gating.go` implements §4.3 rules 1–4 for one artifact. A kernel's feature use is
collected transitively over the call graph — signature, `group` declarations, every
instruction suffix, and every reachable `func`'s signature and body — and checked
against `gvir.Supports(BackendAMDGCN, arch, f)`.

* An excluded kernel is **not emitted** and appears in `Result.Excluded`. It is not
  an error: §4.3 rule 4 makes exclusion from *every* artifact a gating error, and
  that is a whole-module judgement `ir/verify` makes, not one this backend can.
* A `func` is emitted only if some emitted kernel reaches it (rule 3).
* Only `bf16` is ever actually gated here: `f64` and `subgroup_size` are available
  on every `gfx*` in the table.

**`.wave` is module-scope; `subgroup_size` is per-kernel.** AMDTX P1 fixes one
processor and one wave width per module, so kernels declaring different
`subgroup_size` values cannot share an artifact — that is a lowering error naming
both kernels. `subgroup_size 32` on a target without `wavefrontsize32` is a
lowering error too (§9.2 makes the attribute select wave-size mode, and V5 would
reject the module anyway). With no attribute, the target's default wave is used.

**Arch coverage.** `.gvir`'s arch table is larger than AMDTX's §4 processor table —
`gfx906`, `gfx908`, `gfx940`, `gfx1010`, `gfx1031`, `gfx1032`, `gfx1101`, `gfx1102`
are valid `.gvir` archs with no AMDTX row. Lowering one is an error naming the
missing row, because AMDTX §4.2 makes adding a processor a spec revision (a table
row, a counter profile, an inline-constant profile, an encoding table and a MACH
value), not something a backend may improvise.

---

## 8. Synchronization

`.gvir` atomics are relaxed and express ordering only through `fence` (§10.2),
which is precisely AMDTX's model (§12.4), so this maps across without an ordering
lattice in between:

| `.gvir` scope | AMDTX scope |
| --- | --- |
| `subgroup` | `.wavefront` |
| `group` | `.workgroup` |
| `grid` | `.agent` |

`fence relaxed` emits nothing — it is a no-op by §10.2, and AMDTX **V38** rejects a
bare `fence`. A `barrier.<exec>, <mem>` emits `fence .acq_rel <scope>` ahead of
`s_barrier`, letting AMDTX expand the cache maintenance rather than picking
`buffer_wbl2`/`buffer_inv`/cache-policy bits here (§8.3 says exactly this: code
needing portable ordering should use `fence`). `barrier.subgroup` emits no
execution barrier: a wave is synchronized by construction.

**Waits are conservative.** P6 is explicit that adjacency conveys nothing, so every
load is followed immediately by its counter wait (`lgkmcnt(0)` for LDS and scalar
memory, `vmcnt(0)` for vector memory), and stores are flushed with
`waitcnt_vscnt 0` on GFX10/GFX11 before a fence or barrier. This is correct and
slow. It is written this way rather than left to AMDTX's `autowait` pass because
that pass "MUST NOT weaken an existing explicit wait" (§12.3) — which means a
wait-sinking pass belongs here, above it, not below.

---

## 9. Missing Against the Spec (Todos)

Valid IR this backend does not yet lower; each returns an error at `Lower` time
rather than emitting something approximate.

* **Scalarization.** Every value is a VGPR (§2). Uniform values in SGPRs and `s_*`
  selection for them is the largest single improvement available, and the
  uniformity analysis it needs is already here.
* **Integer division.** `udiv`, `sdiv`, `urem`, `srem`. amdgcn has no integer
  divide instruction at all; the correct lowering is the reciprocal sequence plus
  §11.1's guards for division by zero and `INT_MIN / -1`. Nothing is emitted
  instead.
* **`div` and `sqrt` on floats.** `v_rcp_f32` and `v_sqrt_f32` are ~1 ULP
  approximations; §11.3 requires IEEE results, which needs the
  `v_div_scale`/`v_div_fmas`/`v_div_fixup` sequence and a Newton refinement
  respectively. Emitting the approximations under a strict float profile would be
  silently non-conforming.
* **`tanh`.** Even under `approx` there is no amdgcn instruction; the 8.0 ULP
  bound needs a polynomial.
* **64-bit integer `mul`, `bswap`, and the 64-bit `round`.** The 32-bit forms are
  implemented (`v_mul_lo_u32`, `v_perm_b32`, and the trunc/copysign expansion);
  the 64-bit forms need split sequences.
* **`memcopy`, `memmove`, `memset`.** All three are per-thread byte loops (§8.3).
  The loop is emittable as a structured region, but the direction test `memmove`
  needs and the alignment fast paths are unwritten.
* **Sub-dword and aggregate kernel parameters.** AMDTX `.param` widths are
  multiples of 32 (§5), so an `i8`, `i16`, `f16` or `bf16` parameter cannot be
  declared without changing the §6.3 packed offsets, and §19.2 defers aggregate
  kernarg layout to AMDTX v1.1 outright. The two layouts are computed
  independently and compared; a divergence is an error, never a different buffer.
* **`dynamic_group_size`.** The byte count lives in the code-object V5 implicit
  argument block, whose layout AMDTX §11.3 deliberately reproduces nowhere and
  sources from LLVM. Reading it needs that table.
* **`groups_per_grid` with a non-power-of-two group size.** The AQL packet carries
  the grid in work-items, so the builtin is a division; it is a shift when the
  kernel declares a power-of-two `group_size` and unimplemented otherwise.
* **`mask_lt`/`mask_le`/`mask_gt`/`mask_ge`/`mask_eq`.** These are per-lane
  `submask` values, and amdgcn has no `%lanemask_*` special registers to read them
  from. Materializing them per lane conflicts with `submask` living in a
  `.lanemask`; both halves need doing together.
* **64-bit collectives.** `shuffle*` and the `sub_*` reductions are implemented for
  32-bit element types via `ds_bpermute_b32`; the 64-bit forms need the paired
  sequence.
* **Cross-function uniformity.** A `func`'s parameters are treated as divergent, so
  a `barrier` inside a `func` under a caller-uniform condition is rejected. AMDTX
  inlines every call anyway, so the fix is to analyze after inlining or to
  propagate argument uniformity per call site.
* **Inline-constant peepholes.** Literals outside `[-16, 64]` are materialized with
  `v_mov_b32` at every use rather than folded into the instruction's literal slot
  (§8.1) or hoisted, which costs a register and an instruction per use.