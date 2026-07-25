# Vertex IR / VVM — Proposed Overview: Unified CPU/GPU Flow

**Status:** Proposal (not yet implemented)
**Scope:** Additive extension only. No existing grammar, opcode, or package is modified or removed.

---

## 0. Motivating Principle

VVM's original thesis was never "CPU vs. GPU" — it was "CPU vs. 1000 flavors of runtime bloat vs. what the CPU actually wanted." This proposal applies the same thesis to accelerators: a strict, no-runtime-bloat path from a `gpu fn` down to a launchable kernel, with the same one-opcode-one-meaning discipline §1 already applies to the CPU side.

Nothing here changes VVM's core guarantee. CPU-only modules with no `gpu fn` are byte-for-byte unaffected — this is a pure superset.

---

## 1. New Grammar: `gpu fn`

A new function-level keyword, parallel to (not replacing) ordinary `fn`:

```
gpu-fn-def := "export"? "gpu" "fn" ident "(" param-list? ")" type gpu-fn-attr* ":"
              entry-block block* "end"

gpu-fn-attr := "noreturn" | "inline" | "noinline" | "cold"
```

**Rules:**

- A `gpu fn` body may only `call` other `gpu fn`s or device-side `extern` declarations known to `gpu/lower`  — never an ordinary `fn`, never a host `extern "c"` group. Checked at verify time, same shape as existing `readonly`/`noreturn` call-site rules in §4.2.
- An ordinary `fn` may never directly `call` a `gpu fn`. The only entry point is `call.gpu` (§3).
- `entry`/`extern_c` (§2.2) do not apply to `gpu fn` — kernels are resolved by name at runtime via the device module loader, not by the CPU linker, so CPU symbol-naming overrides are meaningless here. Many `gpu fn`s may exist per module (no `entry`-style "at most one" restriction).
- `byval`/`sret` param attrs are **not** legal on `gpu fn` params in this proposal (open question, §7).

**Type restrictions inside a `gpu fn` body:**

- No new types are introduced. `iN`, `fN`, `ptr`, `vec[T,N]` all remain valid.
- `valist`, `array[T,N]`-as-named-value, and TLS `global` access remain illegal inside `gpu fn`, same as they are in `fn` today — no new exemption is proposed.
- A `ptr` parameter to a `gpu fn` denotes a **device-resident** address once inside the kernel body. It is never valid to pass a host `alloca` pointer directly across this boundary without going through the `call.gpu` expansion (§3) — this is a semantic fact about the boundary, not a new type.

---

## 2. Builtin Ops (Kernel-Local, `valist`-style Opacity)

New op family, legal **only** inside `gpu fn` bodies, illegal elsewhere (verification error otherwise — same enforcement pattern as `va_start`/`va_arg`/`va_end` being illegal outside variadic functions):

```
gpu-builtin := "tid" "." dim | "bid" "." dim | "bdim" "." dim | "gdim" "." dim
dim         := "x" | "y" | "z"
```

- `tid.x` / `tid.y` / `tid.z` — thread index within block.
- `bid.x/y/z` — block index within grid.
- `bdim.x/y/z` — block dimensions.
- `gdim.x/y/z` — grid dimensions.

Each yields `i32`. These are **not** `extern` calls (no linkable symbol backs them) and **not** ordinary opcodes (§1's "one behavior per opcode, no target-dependent semantics" would be violated by an op meaningless on CPU targets) — they follow the `valist` precedent instead: an opaque, deliberately-restricted builtin category, gated to a specific function-attribute context, checked at verify time.

**Explicitly out of scope for this proposal:** `__shared__`-equivalent memory (a second address space) and `__syncthreads()`-equivalent barriers. Both require design work beyond an additive attribute/op-family — see §7.

---

## 3. `call.gpu` — Kernel Launch

New instruction form, parallel to `call` / `call.<fnsig>` / `tailcall` / `tailcall.<fnsig>`:

```
inst := ... | ident? "=" "call" "." "gpu" ident
             gx "," gy "," gz "," bx "," by "," bz "," shared
             "," operand-list?
```

where `gx,gy,gz,bx,by,bz,shared` are `i32` operands (grid dims, block dims, shared-mem bytes) and the trailing `operand-list` is the kernel's actual argument list (matching the target `gpu fn`'s param types).

**Semantics — `call.gpu` is sugar, not a primitive.** It macro-expands, at `lower` time, into an ordinary sequence of `extern` calls against the device driver:

1. Load the device module (from an embedded PTX `global`, §4) and resolve the kernel by name — once, cacheable across call sites targeting the same `gpu fn`.
2. For each `ptr` argument: allocate device memory, copy host→device (**unless** the parameter is inferred/declared write-only — see open question §7).
3. Configure and enqueue the launch with the given grid/block/shared operands.
4. Synchronize.
5. For each `ptr` argument written by the kernel: copy device→host.
6. Free device allocations (unless lifetime-hoisted out of a loop — open question, §7).

Scalar (`iN`/`fN`) arguments cross with no copy — packed directly into the launch parameter buffer.

`call.gpu` is deliberately **not** a `terminator` — it is a synchronous instruction (steps 1–6 complete before the next instruction runs). Fire-and-forget / stream-async variants are out of scope for this proposal (§7).

---

## 4. PTX Embedding — No Linker Changes Required

The kernel's compiled PTX text is embedded as an ordinary `global` byte-string constant, produced by the GPU compilation path and written back into the module before `lower` sees it:

```vir
global do_math_ptx array[i8, N] = "<ptx text>\x00"
```

This requires **zero changes** to `ir/vir`'s grammar, `object/`, `objectwriter/`, or any `linker/<format>` package — a `global` byte string flows through the existing single pipeline (`ir/verify` → `lower/<arch>` → `object/<arch>` → one linker pass) unmodified. The device driver `extern` calls (`cuModuleLoadData`, `cuLaunchKernel`, etc.) resolve exactly like the existing `printf` example — ordinary `extern "cuda": ... end` + `link shared "cuda"`.

No second `.o` file, no foreign-object linker input, no change to "no shelling out to `ld`" — confirmed as unnecessary once PTX rides as data rather than as a competing object file.

---

## 5. Package Layout

### 5.1 The `isa` vs `lower` split, and why GPU can't mirror it directly

On the CPU side, `isa/<arch>` and `lower/<arch>` are deliberately separate:

- `isa/<arch>` — *"Static, data-only instruction set descriptions — registers, condition codes, opcode/mnemonic tables"* — a fixed, real hardware ISA that exists independent of any compiler.
- `lower/<arch>` — *"Pure instruction selection: a verified module in, a machine-code Program out"* — consumes the `isa` tables to do selection.

That split exists because a real ISA (x86_64, aarch64) is a fact about silicon, knowable and tabulatable independent of any single compiler's choices. **PTX has no equivalent fact to tabulate.** NVIDIA doesn't expose a fixed hardware opcode/register table the way x86_64 does — PTX *is* the virtual, compiler-facing IR; the real SASS-level ISA is undocumented and reassembled per-GPU-generation by `ptxas`, entirely outside anything VVM touches. So `gpu/` doesn't get an `isa`-equivalent — it gets an **`ir`-equivalent**: a data model for the thing actually being emitted (PTX instructions, types, directives), mirroring the role `ir/vir` plays for VIR itself, not the role `isa/<arch>` plays for a real ISA.

This makes the two flows structurally parallel, but honestly distinguishable:

| CPU flow | GPU flow |
|---|---|
| `cpu/isa/x86_64` — real ISA tables (data-only) | `gpu/ir/ptx` — PTX node/instruction data model (data-only) |
| `cpu/lower/x86_64` — VIR → machine code, using `isa` tables | `gpu/lower/ptx` — VIR (`gpu fn`) → `gpu/ir/ptx` tree → PTX text |

`gpu/lower/ptx` doesn't hand-emit PTX text directly any more than `cpu/lower/x86_64` hand-emits machine code without consulting `isa/x86_64` — it builds a `gpu/ir/ptx` tree and that subpackage owns serializing it to `.ptx` text, exactly as `isa/<arch>` owns the tables `lower/<arch>` selects against.

### 5.2 Resulting layout

```
cpu/
    isa/              (moved from isa/ — real ISA tables)
        x86_64/
        aarch64/
        arm/
        x86/
    lower/            (moved from lower/ — VIR → machine code, per arch)
        x86_64/
        aarch64/
        arm/
        x86/

gpu/
    ir/
        ptx/          (new — PTX instruction/type/directive data model,
                        data-only, no VIR knowledge — mirrors ir/vir's role)
    lower/
        ptx/          (new — gpu fn (verified VIR) → gpu/ir/ptx tree →
                        .ptx text; consumes gpu/ir/ptx the same way
                        cpu/lower/<arch> consumes cpu/isa/<arch>)
```

**Naming rule (unchanged from prior draft):** the GPU-side package is named after the target *representation* (`ptx`), not the vendor (`cuda`) — mirroring `cpu/lower/<arch>` being named after ISAs, never vendors (no `lower/intel`, no `lower/apple`). This leaves room for `gpu/ir/amdgcn` + `gpu/lower/amdgcn`, or `gpu/ir/spirv` + `gpu/lower/spirv`, to register later the same additive way `linker/elf/riscv64` does today — without ever touching `gpu/ir/ptx` or `gpu/lower/ptx`.

**Both moves — `isa/` → `cpu/isa/` and `lower/` → `cpu/lower/` — are import-path-breaking renames**, done together as part of establishing the `cpu/` namespace opposite `gpu/`. This supersedes the previous draft's position of leaving `lower/` unrenamed; symmetry with the new explicit `cpu/isa/` + `gpu/ir/` split makes a half-renamed `lower/` (unqualified) sitting next to `gpu/lower/` (qualified) inconsistent enough to be worth the break.

**New top-level package**, following the existing "only the top-level orchestrator imports everything" rule (`vvm.go` is the one place allowed to import all of `lower`, `object`, `objectwriter`, `linker`):

```
accel/     — owns gpu fn → gpu/lower/ptx dispatch, call.gpu macro-expansion,
             and embedding the resulting PTX global back into the module
             before cpu/lower ever runs.
```

`ir/vir` and `ir/verify` remain unaware `accel`, `gpu/ir`, or `gpu/lower` exist, same as they're unaware `ir/verify` exists today — strict package boundaries preserved. Neither `gpu/ir/ptx` nor `gpu/lower/ptx` imports anything from `cpu/isa` or `cpu/lower` — the two trees are siblings, not dependents.

---

## 6. Repository Snapshot (Current State)

```
cmd/  crt/  format/  gpu/  importer/  ir/  isa/  linker/
lower/  object/  objectfile/  objectwriter/  os/  spec/  testutils/
```

`gpu/` and `os/` already exist as scaffolding directories ahead of this proposal being finalized. This document defines what belongs inside `gpu/` (`ir/ptx`, `lower/ptx`) and confirms `isa/` and `lower/` are slated to move under a new `cpu/` root to sit symmetrically opposite it.

---

## 7. Open Questions (Not Resolved by This Proposal)

1. **Parameter direction (in/out/inout).** Plain `ptr` carries no directionality. `call.gpu`'s expansion needs to know which arguments need copy-in, copy-out, both, or neither. Options: static inference from kernel body (works for simple bodies, unsound for aliased/conditional writes), or explicit frontend-level annotation on `gpu fn` params. Must be resolved above VIR — VIR's type system has no slot for it.
2. **Device-allocation lifetime.** Naive expansion re-allocates/frees device memory on every `call.gpu`. Hoisting allocation out of loops is a real optimization but requires the expansion to reason about loop structure, not just a single call site.
3. **Shared memory (`__shared__`-equivalent).** Requires a second address space — a genuinely new type qualifier on `ptr`/`alloca`, not an additive attribute. This is the one piece that argues directly with §1's "memory model fits in one paragraph" principle and needs its own design pass.
4. **Thread synchronization (`__syncthreads()`-equivalent).** Not an `extern` (no link-time symbol), not an ordinary op (no meaning pre-launch), not an existing terminator kind. Needs its own instruction category.
5. **Async / stream-based launches.** `call.gpu` as proposed is synchronous (launch+sync+copy every time). A fire-and-forget or explicit-stream variant is a legitimate future need but is left out to keep this proposal's semantics unambiguous.
6. **`byval`/`sret` legality on `gpu fn` params.** Currently proposed as illegal; revisit if a real frontend needs struct-by-value kernel args.
7. **`os/` directory's role.** Present in the current tree but undefined by this proposal — out of scope here; noted so it isn't silently assumed to be part of the GPU flow.

---

## 8. What Explicitly Does Not Change

- §1's CPU-only, no-runtime, strict-semantics principles are unaffected for every module that contains no `gpu fn`.
- No existing opcode, terminator, or type is modified.
- `ir/vir` and `ir/verify` remain fully unaware GPU concepts exist.
- The linker's "no shelling out, no foreign object ingestion" stance is preserved — PTX travels as `global` data, never as a second `.o`.