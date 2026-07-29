# `gpu/lower/ptx`

```go
import lower "github.com/vertex-language/vvm/gpu/lower/ptx"
```

Lowers a verified `.gvir` device module (`ir/gvir.Module`) to a structured PTX IR module (`gpu/ir/ptx.Module`). It produces **one artifact**: `.gvir` declares at most one `ptx` arch, and PTX is JIT-forward, so a single module covers everything at or above the declared floor (§3).

The result is IR, not text. Print it with `gpu/ir/ptx/encoding/text`, rewrite it, or embed it — this package makes no formatting decisions.

## Usage

```go
// Initialize lowering with default options
res, err := lower.Lower(m) // m: *gvir.Module, already ir/verify'd
if err != nil {
    log.Fatal(err) // a §1.1 lowering error: this artifact fails
}

// Inspect kernels excluded due to gating
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}

// Print to PTX text
src, _ := text.Print(res.Module)
```

> **Naming Note:** This is `package ptx`, and it imports `gpu/ir/ptx` under that same name. Go only qualifies *imported* identifiers, so every `ptx.` in these files refers to the IR package; this package's own identifiers are always unqualified.

## 1. Layout & Architecture

The lowering process is split logically by instruction class and module lifecycle:

| File | Responsibility |
| --- | --- |
| **`ptx.go`** | Entry point (`Lower`, `LowerOptions`), `Options`/`Result` definitions, arch and ISA selection, and the module-level `lowerer` state. |
| **`value.go`** | The §7.3 Join Convention: name → register binding, vector lane assignments, pointee tracking, and operand materialization. |
| **`callable.go`** | Kernel and func lowering: kernarg → `.param`, `group` → `.shared`, `dynamic_group` → `.extern .shared`, and tuning directives. |
| **`cfg.go`** | Block emission order, label binding, and terminators. (Merge annotations are dropped since PTX needs no structured control flow). |
| **`isel.go`** | Instruction selection: arithmetic, bitwise, float, comparisons, conversions, `select`, and `call`. |
| **`isel_mem.go`** | `alloca`, `load`/`store`, `index.ptr`/`field.ptr`, `memcopy`/`memset`, and the vector opcodes (`extract`, `insert`, `splat`, `swizzle`). |
| **`isel_sync.go`** | Synchronization (`barrier`, `fence`), atomics, subgroup collectives (`shuffle`, `vote`, `broadcast`), and `submask` operations. |
| **`builtin.go`** | §9 execution builtins, including normative i64 linearizations for thread/group coordinates. |
| **`gating.go`** | §4.3 capability gating: per-kernel feature use over the call graph, kernel exclusion, and the §9.2 `subgroup_size` check. |
| **`types.go`** | Type mapping (`gvir.Type` → `ptx.Type`), address spaces, register widths, and zero-extension invariants. |
| **`consts.go`** | Module `const`s → `.const` variables, plus scalar-inlining logic. |
| **`half.go`** | `f16`/`bf16` literal bit patterns (PTX takes 16-bit float immediates as bit patterns). |

There is no register allocator and no scratch-register discipline of the x86_64 kind: PTX is a virtual-register ISA and `ptxas` allocates. The lowering layer hands out as many virtual registers as instruction selection wants.

## 2. Value Model

**One name, one register.** §7.3 merges values across blocks by same-name assignment, which maps perfectly to a virtual register written more than once. `value.define` binds a name to a register on first assignment and returns the same register on every later assignment. There are **no phi nodes to insert**, and loop-carried values need no special form.

**Vectors are lane vectors.** PTX has no vector registers (modifiers like `.v2`/`.v4` are a property of `ld`/`st` operand arity, not of a type). Therefore, a `vec[T,N]` value allocates `N` scalar registers, and every arithmetic opcode is unrolled and emitted `N` times. A `vec[T,3]` value keeps exactly three registers; the padding lane (§4.4) is never materialized.

**`submask` is `.b32`.** PTX's warp is 32 lanes wide, so the opaque lane mask (§4.6) is modeled as a single `b32` register. Instructions like `mask_lt`/`mask_le` lower directly to `%lanemask_lt`, etc.

**Pointee tracking.** `.gvir` pointers carry an address space but no pointee type, so `field.ptr p, k` cannot be lowered from the pointer's type alone. The `value` struct tracks a best-effort `pointee`, propagated from `alloca`, `group` declarations, `field.ptr`, and `index.ptr`. A `field.ptr` through a generic pointer (where provenance is lost) returns a strict lowering error rather than a guess.

## 3. Zero-Extension Invariant

A value of type `iN` lives in a register of width `max(16, N)` holding the value **zero-extended**. PTX 8-bit registers are usable only by `ld`, `st`, and `cvt`, so `i8` is promoted to a 16-bit register exactly as `nvcc` does (`%rs`).

- Wrapping operations on `i8` (`add`, `sub`, `mul`, `neg`, `not`, `shl`, `rotl`, `rotr`, `brev`, `bswap`) are always followed by `and.b16 d, d, 255` to restore the invariant. Thus, §11.1's "wrapping is modulo 2^N" is literally true at 8 bits.
- Signed consumers (`sdiv`, `srem`, `ashr`, `abs`, `smin`/`smax`, `s*` compares) sign-extend an `i8` operand via `cvt.s16.s8` into a scratch copy that is never written back to the value's primary register.
- `i16`, `i32`, and `i64` need no masking: their values perfectly fill their registers.

## 4. Address Spaces and Parameters

`.gvir` has no generic pointer, so no `cvta` (convert address) appears in the kernel body: every `ld`/`st` carries the space qualifier taken from the pointer's own type (§5).

The *one exception* is the kernel prologue. A `ptr[global]` or `ptr[constant]` kernel argument arrives in `.param` space as a **generic** address, so the prologue emits the standard generic-to-specific pair:

```text
ld.param.u64        %rd1, [out];
cvta.to.global.u64  %rd1, %rd1;
```

After this, the value is treated as a space-specific address for the rest of the body.

| `.gvir` Space | PTX state space | Binding Mechanism |
| --- | --- | --- |
| `global` | `.global` | kernel param + `cvta.to.global` |
| `constant` | `.const` | kernel param + `cvta.to.const` |
| `group` | `.shared` | `group` decl → function-scope `.shared .b8 name[bytes]` |
| `group` (dynamic) | `.shared` | module-scope `.extern .shared .b8 $dyn_smem[]` |
| `private` | `.local` | `alloca` → function-scope `.local .b8 name[bytes]` |

### Dynamic Group Size

`dynamic_group` collapses to **one** module-scope extern array shared by every kernel that declares one (`$dyn_smem[]`). PTX has a single dynamic shared window per launch, so per-kernel dynamic arrays are treated as aliases of it. `OpDynamicGroupSize` lowers simply to `%dynamic_smem_size`.

### Struct Parameters

By-value aggregate parameters are forbidden in named values (§4.7). Thus, struct parameters are lowered as `.param .align A .b8 s[size]` and are immediately copied chunk-by-chunk into a `.local` buffer during the prologue. The `.gvir` name is then bound to that `ptr[private]`, allowing `field.ptr` offsets to work safely.

## 5. Control Flow

Merge annotations (§7.2) are **dropped**. They exist because other backends (like SPIR-V) require structured control flow; PTX does not. A reducible annotated CFG is already a legal PTX branch graph. When `Options.Comments` is true, merge points are re-emitted as trailing comments for readability, but they have no semantic effect.

- `br` → `bra`
- `br_if` → Predication is attached directly to the branch instruction itself (`@%p bra Then; bra Else;`), ensuring guards never leak past labels.
- `switch` → Emitted as a dense `setp` compare chain followed by predicated `@%p bra` instructions, finishing with a `bra default`.
- `return` → In a kernel, simply `ret;`. In a function, the return value is sequentially stored into the `.param` slots before `ret;`.
- `unreachable` → `trap;` — executing unreachable code is §12.6 UB, and a hardware trap is the least surprising realization of it.

## 6. Semantic Corners

These are places where PTX's default hardware behavior and `.gvir`'s strict pinned behavior disagree, requiring the emission of instruction sequences rather than a single 1:1 opcode.

- **Division and Remainder (§11.1):** PTX leaves division by zero undefined; `.gvir` requires `0`. Every `udiv`/`sdiv`/`urem`/`srem` evaluates the divisor: if `0`, it is replaced with a safe `1` under a predicate, and the final output is forced to `0` under that same predicate.
- **`ctlz`/`cttz` of zero (§11.2):** Count-leading-zeros of `0` is computed as `clz.b32(x) - (32 - N)` for `N < 32`, yielding the correct `N` bit-width without branches. `cttz` applies `brev` first, then `clz`, bounded by `min.u32`.
- **Shift and Rotate Counts (§11.2):** PTX clamps counts; `.gvir` masks them. Shift amounts are explicitly `and`-ed with `N-1` before shifting.
- **`min`/`max` (§11.3):** PTX's `min.f32` (without the `.NaN` qualifier) maps cleanly to IEEE `minNum`, so the qualifier is deliberately omitted.
- **Rounding (§11.3):** PTX has no half-away-from-zero mode. `round` emits: `t = trunc(x); if |x - t| >= 0.5 then t + copysign(1, x) else t`.
- **Float-to-Int (§11.5):** Saturated casting is required, but `cvt.rzi` with an integer destination already saturates and maps NaN to `0` natively.
- **Atomics (§10.2):** The IR expresses ordering strictly through `fence`. Consequently, *every* atomic is emitted as `atom.relaxed.<scope>`. PTX has no `atom.sub`, so subtraction lowers to `neg` followed by `atom.add`. `cmpxchg` maps to `atom.cas`, which naturally yields the old value instead of a success flag.
- **Reductions (§10.3):** Subgroup collectives (`sub_*`) lower into a five-step `shfl.sync.down` tree followed by a broadcast from lane 0. Because `.gvir` guarantees inactivity only comes from unpopulated trailing lanes (§9.2), the active set is always a perfect prefix, making this tree exact and robust.
- **Barriers (§10.1):** `barrier.group` maps to `bar.sync 0`; `barrier.subgroup` is `bar.warp.sync <activemask>`. If a barrier requires a tighter memory scope, pre-fences like `membar.gl` or `membar.cta` are injected automatically.

## 7. Capability Gating

`gating.go` strictly implements §4.3 capabilities over the entire artifact call graph.

1. **Rule 1:** A kernel uses a gated feature if it appears in its signature, `group` declarations, its body, or the body of any func reachable from it.
2. **Rule 2:** Kernels using unavailable features are quietly **excluded** from emission and logged to `Result.Excluded`.
3. **Rule 3:** A function is only emitted if an emitted kernel reaches it.
4. **Rule 4:** Exclusion is a whole-module judgement reserved for `ir/verify`; this backend simply prunes them.

**Note:** `subgroup_size N` with `N ≠ 32` is an immediate **lowering error**, as PTX warp widths are hardcoded to 32. It triggers a failure rather than exclusion.

## 8. Float Profile

`float_profile` (§11.6) semantics act globally, with defaults set to `off`:

- **`contract` (off):** Means strict IEEE representation with no fusion. This package never implicitly fuses instructions. However, `ptxas` contracts by default, so if contraction is disallowed, the module embeds `.pragma "nofma";` to halt assembler-level contraction.
- **`approx`:** Gates mathematical approximations (`rcp`, `rsqrt`, `sin`, `cos`, `exp2`, `log2`, `tanh`). They lower into their `.approx.f32` PTX counterparts. Attempting to emit one without `approx` enabled in the profile is a strict lowering error.
- **Explicit `fma`:** Always lowers to `fma.rn`. It implies a single rounding step by definition, and the explicit `.rn` qualifier is required by the PTX specification.

## 9. Missing Against the Spec (Todos)

Valid IR that this backend does not yet handle (these will throw strict `Lower` errors rather than compiling bad PTX):

- **`memmove`:** `memcopy` and `memset` map well to forward byte loops, but `memmove` needs a direction test and a potential backward loop.
- **`field.ptr` through an untyped pointer:** Lowerable only when the pointee is recoverable via provenance (like an `alloca` or `group` declaration). Accessing offsets on raw generic kernel arguments lacks the structural metadata to deduce field sizes.
- **`bswap` on `i64`:** 8/16/32-bit width swaps are implemented (identity, shift pairs, and `prmt`), but the 64-bit form requires a complex split/`prmt`/swap/join sequence that is not yet implemented.
- **`bf16` arithmetic below `sm_90`:** `.gvir` technically allows `bf16` at `sm_80`, where PTX supports conversions but not math. Emitting native arithmetic on `sm_80` causes an assembler rejection, and automatic upcasting to `f32` would trigger non-conforming double-rounding.
- **`f16`/`bf16` vector packing:** Packed arithmetic (e.g., `f16x2`) is never naturally formed. A `vec[f16,2]` falls back to two separate scalar `f16` ops.
- **Non-constant `align` interactions:** Memory alignments (`align N`) are forwarded to `ld`/`st`/atomics blindly. Nothing dynamically verifies them against the pointer's provenance at runtime.