# `gvir_arch.md` (Updated)

# Vertex GPU IR — Architecture & Host Integration

Companion to `gvir_spec.md`. This document covers the compilation pipeline, the `.vir` host bridge, the generated symbol ABI, backend mapping, and the v1 scope boundary.

---

## 1. Position in the Stack

```text
  frontend (any language)
        │
        ▼
  vecmath.gvir   ← portable, device-only, three-backend
        │
   parse → verify → structurize → per-backend lower
        │
   ┌────┴──────────────┬──────────────────┬─────────────┐
   ▼                   ▼                  ▼             ▼
  ptx IR            amdtx IR           amdtx IR       msl IR
   │                   │                  │             │
   ▼                   ▼                  ▼             ▼
 .ptx text      gfx1100 hsaco      gfx942 hsaco    .metal text

```

The three lowering targets are existing packages with their own documented shapes:

| gvir backend | Package | Model |
| --- | --- | --- |
| `ptx` | `gpu/ir/ptx` | Virtual registers, flat, symbolic labels, text is the wire format |
| `amdgcn` | `gpu/ir/amdtx` | Virtual registers, **structured** (`If`/`Else`/`End`, `Loop`/`BreakIf`), lowers further to physical amdgcn + ELF |
| `msl` | `gpu/ir/msl` | Named variables, **statement tree**, no labels, no `goto`, text is the wire format |

Two of the three want structured control flow. That asymmetry is the reason `.gvir` carries merge annotations (spec §7.2) rather than a bare CFG: it turns "run a relooper and hope" into a mechanical walk.

**Structurization pass.** Between verify and lower, the annotated CFG is converted to a construct tree. For `ptx` this tree is flattened straight back out to branches and labels. For `amdtx` each selection becomes `cb.If`/`cb.Else`/`cb.End` and each loop `cb.Loop`/`cb.BreakIf`, which the amdtx lowerer expands into `s_and_saveexec` / `s_cbranch_execz` / `s_or_exec`. For `msl` it becomes `cb.If`/`cb.For`/`cb.While` statements. Multi-exit loops that survive annotation are handled by a dispatch-variable fallback; the merge annotations guarantee this is always constructible.

---

## 2. Compilation Pipeline

GPU compilation runs to completion before host compilation begins. A `.gvir` failure prevents `.vir` compilation from ever starting.

```text
                        ┌───────────────────────────────────────────┐
  vecmath.gvir  ──────► │ parse → verify → structurize → lower×N     │
                        └───────────────────────────────────────────┘
                                          │
        ┌──────────────┬──────────────────┼──────────────────┬──────────────┐
        ▼              ▼                  ▼                  ▼              ▼
  vecmath.ptx   vecmath.gfx1100.hsaco  vecmath.gfx942.hsaco  vecmath.metal  metadata
    (text)          (ELF bytes)           (ELF bytes)          (text)       (JSON)
        └──────────────┴──────────────────┼──────────────────┴──────────────┘
                                          ▼
                              vecmath.gvir.vir   ← generated .vir fragment
                                          │
  app.vir  (gpu_module "vecmath.gvir")  ──┤
                                          ▼
                            ┌───────────────────────────┐
                            │  .vir verify → codegen     │
                            └───────────────────────────┘
                                          ▼
                                        app.o

```

One `.gvir` module produces one artifact per `(backend, arch)` pair, plus one generated `.vir` fragment describing all of them.

---

## 3. The `gpu_module` Host Interface

```vir
module app
target x86_64 linux gnu
gpu_module "vecmath.gvir"

```

`gpu_module` drives a sub-compilation and splices the resulting generated fragment into the module. It is an include-of-a-build-product.

### 3.1 Requirements on `.vir`

`gpu_module` requires **Vertex IR v2.1**. v2.0 has six numbered preamble slots and no production for this. The single addition is:

```text
module := module-header
          namespace-decl?
          target-decl?
          gpu-module-decl*        ← new, slot 3.5
          struct-decl*
          ...
gpu-module-decl := "gpu_module" string-literal

```

It sits after `target` and before `struct`, because the spliced fragment emits `const`s and `global`s and those must precede `link`/`extern`/`fn`. Multiple `gpu_module` lines are permitted. Symbol collisions between two GPU modules with the same `module` name are a build-time error.

### 3.2 Artifact form

| Backend | Emission | Alignment |
| --- | --- | --- |
| `ptx` | NUL-terminated byte string | 1 |
| `msl` | NUL-terminated byte string | 1 |
| `amdgcn` | Raw ELF bytes, **not** NUL-terminated | 4096 (page) |

---

## 4. Generated Symbol ABI

Normative. `<M>` is the `.gvir` module identifier; `<K>` a kernel name; `<A>` a target arch identifier with any non-identifier characters replaced by `_`.

### 4.1 Artifacts

| Symbol | `.vir` type | Meaning |
| --- | --- | --- |
| `<M>__ptx` | `global array[i8,N]` | PTX text, NUL-terminated |
| `<M>__ptx__len` | `const i64` | Byte length, excluding NUL |
| `<M>__msl` | `global array[i8,N]` | MSL text, NUL-terminated |
| `<M>__msl__len` | `const i64` | Byte length, excluding NUL |
| `<M>__amdgcn__<A>` | `global array[i8,N] align 4096` | hsaco ELF image |
| `<M>__amdgcn__<A>__len` | `const i64` | Byte length |

### 4.2 Per-kernel Metadata

| Symbol | Type | Meaning |
| --- | --- | --- |
| `<M>__kernel__<K>` | `global array[i8,N]` | Kernel name, NUL-terminated |
| `<M>__kernel__<K>__group_size_x/y/z` | `const i32` | From `group_size`; `0` if unset |
| `<M>__kernel__<K>__max_group_size` | `const i32` | `0` if unset |
| `<M>__kernel__<K>__group_bytes` | `const i32` | Static `group` total |
| `<M>__kernel__<K>__private_bytes` | `const i32` | Per-thread scratch |

### 4.3 Typed Host Launch Interface

Instead of requiring manual offset calculations or unvalidated `void**` arrays, the `.vir` generator synthesizes a strongly typed launcher function per kernel.

| Symbol | Type | Meaning |
| --- | --- | --- |
| `<M>__launch__<K>` | `fn(stream ptr, gx i32, gy i32, gz i32, dynamic_shmem i32, args...) i32` | Device-agnostic launch entry point |

The launcher internally binds to the active vendor driver (CUDA, HIP, or Metal), packs the arguments into the standardized ABI buffer, and executes the kernel.

---

## 5. Host Launch

Because `gpu_module` synthesizes a typed launcher, host integration is dramatically simplified compared to raw driver APIs.

```text
module app
target x86_64 linux gnu
gpu_module "vecmath.gvir"

fn launch_vector_add(dout ptr, da ptr, db ptr, n i32) i32:
  // 1. Group shape calculation based on kernel requirements
  tx = vecmath__kernel__vector_add__group_size_x
  n1 = add.i32 n, tx
  n2 = sub.i32 n1, 1
  gx = udiv.i32 n2, tx

  // 2. Typed launch execution
  // Signature: (stream, grid_x, grid_y, grid_z, dynamic_group_bytes, args...)
  rc = call vecmath__launch__vector_add null, gx, 1, 1, 0, dout, da, db, n
  
  return rc
end

```

The generated `vecmath__launch__vector_add` automatically abstracts whether the backend relies on `cuLaunchKernel` (CUDA), `hsa_executable_get_symbol` (ROCr), or `MTLComputeCommandEncoder` (Metal).

---

## 6. Backend Mapping Summary

Where the abstraction is thin and where it is doing real work.

| gvir construct | ptx | amdgcn | msl | Cost |
| --- | --- | --- | --- | --- |
| Flat CFG + merge | Direct | Structurize → EXEC masks | Structurize → statements | **High on 2/3** |
| Address spaces | Direct | Direct | Direct | None |
| Builtins | Direct sreg | `v_mbcnt` sequences for lane id | Attributed params | Low |
| `barrier` | Free (implied) | `s_barrier` + `s_waitcnt` | `mem_flags` | Low |
| Atomic scopes | `.cta`/`.gpu` | `glc`/`slc`/scope bits | `mem_flags`, `relaxed` only | Low |
| `vec[T,N]` | Scalarize | Scalarize | Native | Low |
| Kernel args | Packed kernarg | Packed kernarg | **Synthesized Argument Buffer `[[buffer(0)]]**` | Low |
| `group_size` | `.reqntid` | `reqd_work_group_size` | Product only | Lossy on msl |
| Static `group` | `.shared` array | `group_segment_fixed_size` | `threadgroup` local | None |
| `dynamic_group` | `extern .shared` | Hidden kernarg | **Hidden buffer offset** | Low |
| `ballot`/`submask` | 32-bit mask | 32 **or 64**-bit mask | `simd_vote` | Opaque type earns its keep |
| Approx math | `.approx` variants | Native | `fast::` namespace | Low |
| `f64` | Native | Native | **Absent** | Kernel-gated |

### 6.1 Where the abstraction is not perfectly even

Two main places:

**Control flow.** gvir is flat-with-annotations because that is the natural shape coming *down* from an SSA frontend, and it is the shape `.vir` already has. It costs a structurization pass on two of three backends. Annotations are the compromise: the frontend already knows its merge points, so recording them is nearly free, and the backends get them for nothing.

**Subgroup width** is a runtime value, not a constant. It is 32 on RDNA/NVIDIA, 64 on CDNA, and compiler-chosen on Metal. Code that hardcodes 32 is wrong on MI300 and gvir refuses to help it be wrong.

*(Note: Kernel arguments are no longer disjointed. MSL natively utilizes a synthesized Argument Buffer mapped to `[[buffer(0)]]`, achieving direct ABI parity with HSA packed kernargs.)*

---

## 7. Verification Summary

The verifier runs once, single-pass, before any lowering.

* Section order, declare-before-use, flat namespace, no shadowing.
* Type well-formedness: `vec` widths, `bf16` target gating, `submask` placement.
* Address-space rules: no cross-space `bitcast` or comparison, no writes through `constant`.
* CFG reducibility; merge annotation presence, dominance, and non-sharing.
* Join Convention: definite assignment, type fixation, address-space fixation, `submask` barrier-crossing.
* `alloca` entry-block-only and statically sized.
* Call graph acyclicity; no indirect calls; no address-of-function.
* Kernel parameter type restrictions; kernarg layout computable.
* Atomic type/space/ordering/scope legality; approximate opcodes gated on `float_profile`.
* Builtin dimension-suffix legality.
* `dynamic_group` at most once per kernel.

---

## 8. Not in v1

| Feature | Why deferred |
| --- | --- |
| **Matrix/tensor fragments** | Modelling them needs a type-system extension for opaque distributed values, not a new opcode class. The `submask` type is a small rehearsal for that machinery. |
| **Async copy** | Requires a commit/wait ordering system orthogonal to the atomic scopes, plus mbarrier-like objects. No portable core exists yet. |
| **Subgroup scans** | All three platforms are constructible from v1 shuffles; a portable opcode with matching performance across all three needs more design than v1 has. |
| **Generic/flat pointers** | Requires a space-tagged pointer representation and space-inference at every dereference. Explicit spaces cover the compute-kernel surface. |
| **`system` atomic scope** | Host-visible atomics need coherent memory guarantees that differ sharply across the three platforms. |
| **Textures, samplers, images** | Graphics-adjacent, enormous surface, and the three backends' models barely overlap. Out of scope for a *compute* IR. |
| **Printf / logging** | Requires a hidden buffer argument and host-side format decoding. |

---

## 9. Relationship to Vertex IR

`.gvir` is a sibling of `.vir` v2.0, sharing conventions but not semantics.

| Aspect | `.vir` v2.0 | `.gvir` v1.0 | Why |
| --- | --- | --- | --- |
| Pointers | Untyped `ptr`, width = target `usize` | Space-qualified, always 64-bit | No generic pointer on GPUs |
| Integers | `i1`–`i128` | `i1`–`i64` | No 128-bit integers on any GPU |
| Floats | `f16`/`f32`/`f64` | + `bf16`, `f64` gated | ML workloads; Metal has no double |
| Div by zero | **Trap** | **Defined (yields `0`)** | No cheap per-lane fault |
| Float→int OOR | **Trap** | **Defined (clamped max/min)** | Same |
| `trap` | Present | **Absent** | Same |
| `alloca` | Per-execution, loops | Entry-block only, static | No dynamic per-thread stack |
| CFG | Reducible, unannotated | Reducible + **annotations** | Backends need structure |
| Contraction | `fma` only, explicit | Permitted under `bounded` | GPUs contract by default |
| Calls | Direct, indirect, `tailcall`, `syscall` | **Direct only** | No function pointers, no syscalls |
| Globals | `global`, `tls` | **None** (only `const`/`group`) | No mutable module state |
| Exports | `link`, `extern` | **None** | Artifacts are vendor blobs |
| Variadics | `valist` | **Absent** | No varargs ABI |
| UB count | 10 | 10 | Exhaustive, hardware-aligned |