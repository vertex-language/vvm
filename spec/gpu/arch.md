# Vertex GPU IR — Architecture & Host Integration

Companion to `gvir_spec.md`. Covers the compilation pipeline, the `.vir` host bridge, the generated symbol ABI, per-backend lowering, and the v1 scope boundary. §6 is the normative home for backend mapping; the spec defers to it.

---

## 1. Position in the Stack

```text
  frontend (any language)
        │
        ▼
  vecmath.gvir   ← portable, device-only, three-backend
        │
   parse → verify → gate → structurize → per-backend lower
        │
   ┌────┴──────────────┬──────────────────┬─────────────┐
   ▼                   ▼                  ▼             ▼
  ptx IR            amdtx IR           amdtx IR       msl IR
   │                   │                  │             │
   ▼                   ▼                  ▼             ▼
 .ptx text      gfx1100 hsaco      gfx942 hsaco    .metal text
```

| gvir backend | Package | Model |
| --- | --- | --- |
| `ptx` | `gpu/ir/ptx` | Virtual registers, flat, symbolic labels; text is the wire format |
| `amdgcn` | `gpu/ir/amdtx` | Virtual registers, **structured** (`If`/`Else`/`End`, `Loop`/`BreakIf`); lowers further to physical amdgcn + ELF |
| `msl` | `gpu/ir/msl` | Named variables, **statement tree**, no labels, no `goto`; text is the wire format |

Two of the three targets require structured control flow. That is why `.gvir` carries merge annotations (spec §7.2) rather than a bare CFG.

**Gating pass** (between verify and structurize). Applies spec §4.3: walks the call graph for transitive feature use per kernel, excludes kernels from artifacts that cannot support them, drops unreachable `func`s, and errors on a kernel excluded everywhere. The resulting per-kernel availability set feeds the metadata of §4.3 below.

**Structurization pass.** Converts the annotated CFG to a construct tree. `ptx` flattens it straight back to branches and labels. `amdtx` maps selections to `cb.If`/`cb.Else`/`cb.End` and loops to `cb.Loop`/`cb.BreakIf`, expanded downstream into `s_and_saveexec` / `s_cbranch_execz` / `s_or_exec`. `msl` maps them to `cb.If`/`cb.For`/`cb.While`. Multi-exit loops use a dispatch-variable fallback; the merge annotations guarantee it is always constructible.

---

## 2. Compilation Pipeline

GPU compilation runs to completion before host compilation begins. A `.gvir` failure prevents `.vir` compilation from starting.

```text
                   ┌────────────────────────────────────────────────┐
  vecmath.gvir ──► │ parse → verify → gate → structurize → lower×N   │
                   └────────────────────────────────────────────────┘
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
                            │  .vir verify → codegen    │
                            └───────────────────────────┘
                                          ▼
                                        app.o
```

One `.gvir` module produces one artifact per `(backend, arch)` pair plus one generated `.vir` fragment describing all of them. A gated-out kernel does not appear in the artifacts it was excluded from; its availability mask (§4.3) records this and the fragment still declares its launcher.

---

## 3. The `gpu_module` Host Interface

```vir
module app
target x86_64 linux gnu
gpu_module "vecmath.gvir"
```

`gpu_module` drives a sub-compilation and contributes the resulting fragment to the module — an include-of-a-build-product.

### 3.1 Requirements on `.vir`

Requires **Vertex IR v2.1**; v2.0 has six numbered preamble slots and no production for this. The single grammatical addition:

```text
module := module-header
          namespace-decl?
          target-decl?
          gpu-module-decl*        ← new, slot 3.5
          struct-decl*
          ...
gpu-module-decl := "gpu_module" string-literal
```

**`gpu_module` is a section-aware contribution, not a textual splice.** The fragment emits into four `.vir` sections; no single insertion point satisfies `.vir`'s own section ordering:

| Fragment emits | Goes into |
| --- | --- |
| `const` (lengths, metadata, availability masks) | const section |
| `global array[i8,N]` (artifact blobs) | global section |
| `extern` (vendor driver entry points) | extern section |
| `fn` (loaders, launchers) | fn section |

The **declaration** sits at slot 3.5 — after `target`, before `struct` — so a one-pass `.vir` verifier knows the full set of contributed names before reaching any section that could reference them. The contributions themselves route to their proper sections.

Multiple `gpu_module` lines are permitted. Symbol collisions between two GPU modules with the same `module` name are a build-time error.

### 3.2 Path resolution

First hit wins:

1. Relative to the directory containing the `.vir` file that names it.
2. Against each `-I` include path in declaration order.

An absolute path is used as given. A path resolving to more than one file under (2) is an error, not a silent first match.

### 3.3 Artifact form

| Backend | Emission | Alignment |
| --- | --- | --- |
| `ptx` | NUL-terminated byte string | 1 |
| `msl` | NUL-terminated byte string | 1 |
| `amdgcn` | Raw ELF bytes, **not** NUL-terminated | 4096 (page) |

MSL ships as **source text**, compiled by the Metal runtime at load. Precompiled `.metallib` is deferred (§8).

---

## 4. Generated Symbol ABI

Normative. `<M>` is the `.gvir` module identifier, `<K>` a kernel name, `<A>` a target arch identifier. Spec §2 forbids `__` inside identifiers, so `__` is an unambiguous separator: `<M>__kernel__<K>__group_size_x` has exactly one parse. Arch identifiers are already `ident`s (spec §3), so no character substitution is performed.

### 4.1 Artifacts

| Symbol | `.vir` type | Meaning |
| --- | --- | --- |
| `<M>__ptx` | `global array[i8,N]` | PTX text, NUL-terminated |
| `<M>__ptx__len` | `const i64` | Byte length, excluding NUL |
| `<M>__msl` | `global array[i8,N]` | MSL text, NUL-terminated |
| `<M>__msl__len` | `const i64` | Byte length, excluding NUL |
| `<M>__amdgcn__<A>` | `global array[i8,N] align 4096` | hsaco ELF image |
| `<M>__amdgcn__<A>__len` | `const i64` | Byte length |

### 4.2 Per-kernel metadata

| Symbol | Type | Meaning |
| --- | --- | --- |
| `<M>__kernel__<K>` | `global array[i8,N]` | Kernel name, NUL-terminated |
| `<M>__kernel__<K>__group_size_x/y/z` | `const i32` | From `group_size`; `0` if unset |
| `<M>__kernel__<K>__max_group_size` | `const i32` | `0` if unset |
| `<M>__kernel__<K>__group_bytes` | `const i32` | Static `group` total |
| `<M>__kernel__<K>__private_bytes` | `const i32` | Per-thread scratch |
| `<M>__kernel__<K>__kernarg_bytes` | `const i32` | Packed kernarg size including hidden trailer (spec §6.3) |

### 4.3 Availability

| Symbol | Type | Meaning |
| --- | --- | --- |
| `<M>__backend_count` | `const i32` | Number of `(backend, arch)` artifacts |
| `<M>__backend__<N>__id` | `const i32` | Backend/arch identity of artifact `N` |
| `<M>__kernel__<K>__available` | `const i64` | Bitmask; bit `N` set iff `<K>` exists in artifact `N` |

Bit ordering follows artifact order in the generated metadata JSON: `ptx` archs, then `amdgcn` archs, then `msl` archs, each in declaration order.

### 4.4 Typed host launch interface

| Symbol | Type |
| --- | --- |
| `<M>__load` | `fn(device ptr) i32` |
| `<M>__unload` | `fn() i32` |
| `<M>__launch__<K>` | `fn(queue ptr, gx i32, gy i32, gz i32, bx i32, by i32, bz i32, dynamic_group_bytes i32, args...) i32` |
| `<M>__last_driver_error` | `fn() i64` |

**Dimension units.** `gx, gy, gz` are counts of **groups**, not threads; `bx, by, bz` are threads per group. The launcher always uses `dispatchThreadgroups:` on Metal, so the contract is uniform across backends. If the kernel declares `group_size X,Y,Z`, passing anything other than `(X,Y,Z)` returns `GVIR_ERR_SHAPE` rather than invoking spec §12.7.

**Queue.** `queue` is the backend's ordering primitive; `null` is always legal and means the implementation default.

| Backend | `queue` is | `null` means |
| --- | --- | --- |
| `ptx` | `CUstream` | the default stream |
| `amdgcn` | `hsa_queue_t*` | a lazily created per-device queue owned by the runtime |
| `msl` | `MTLCommandQueue*` | a lazily created per-device queue owned by the runtime |

On `msl` the launcher creates, encodes, and commits one command buffer per call and does not wait for completion. Enqueue is asynchronous on all three backends; waiting is the host's job.

**Lifecycle.** `<M>__load` selects the artifact matching the supplied device, uploads or JITs it, and resolves every kernel. `device` is a `CUdevice`, `hsa_agent_t*`, or `MTLDevice*`; `null` selects the default device. For `amdgcn`, the device's `gfx` target is matched against the compiled arch list; a miss returns `GVIR_ERR_UNAVAILABLE`. Calling a launcher before `<M>__load` returns `GVIR_ERR_NOT_LOADED`.

**Thread safety.** `<M>__load` and `<M>__unload` are **not** reentrant and must not run concurrently with each other or with any launcher. Launchers may be called concurrently once load has returned, provided each call uses a distinct `queue` or the vendor queue is itself thread-safe.

**Return codes.** Vendor error spaces are not unifiable; the launcher returns a normative enum and stashes the raw vendor code.

| Value | Name | Meaning |
| --- | --- | --- |
| `0` | `GVIR_OK` | Enqueued successfully |
| `1` | `GVIR_ERR_NOT_LOADED` | `<M>__load` not called, or failed |
| `2` | `GVIR_ERR_UNAVAILABLE` | Kernel not in the active artifact, or no artifact matches the device |
| `3` | `GVIR_ERR_SHAPE` | Group shape contradicts `group_size` / `max_group_size` |
| `4` | `GVIR_ERR_RESOURCES` | `dynamic_group_bytes` exceeds the device budget, or occupancy cannot be satisfied |
| `5` | `GVIR_ERR_DRIVER` | Vendor call failed; see `<M>__last_driver_error` |

`<M>__last_driver_error` returns the raw vendor code from the most recent failing call **on the calling thread**, or `0` if none.

Argument count and type mismatch produce no runtime code: the launcher is typed, making it a `.vir` compile-time error. This is why spec §12.7 does not list it as UB.

---

## 5. Host Launch

```vir
module app
target x86_64 linux gnu
gpu_module "vecmath.gvir"

fn run(dout ptr, da ptr, db ptr, n i32) i32:
  rc = call vecmath__load null
  br_if_ne rc, 0, fail

  // Group shape comes from the kernel's own declared requirement.
  tx = vecmath__kernel__vector_add__group_size_x
  n1 = add.i32 n, tx
  n2 = sub.i32 n1, 1
  gx = udiv.i32 n2, tx

  // (queue, grid_x/y/z, group_x/y/z, dynamic_group_bytes, args...)
  rc = call vecmath__launch__vector_add null, gx, 1, 1, tx, 1, 1, 0,
                                        dout, da, db, n
  return rc

fail:
  return rc
end
```

---

## 6. Backend Mapping

Normative lowering reference for `gvir_spec.md`.

### 6.1 Address spaces (spec §5)

| Space | ptx | amdgcn | msl |
| --- | --- | --- | --- |
| `global` | `.global` | `global_*` | `device` |
| `group` | `.shared` | LDS / `ds_*` | `threadgroup` |
| `private` | `.local` | scratch | `thread` |
| `constant` | `.const` | read-only global | `constant` |

### 6.2 Builtins (spec §9.1)

| Builtin | ptx | amdgcn | msl |
| --- | --- | --- | --- |
| `thread_in_grid` | `ctaid*ntid+tid` | `wgid*size+workitem_id` | `[[thread_position_in_grid]]` |
| `thread_in_group` | `%tid` | `v_workitem_id_x/y/z` | `[[thread_position_in_threadgroup]]` |
| `group_in_grid` | `%ctaid` | `%wgid` | `[[threadgroup_position_in_grid]]` |
| `thread_in_subgroup` | `%laneid` | `v_mbcnt_lo/hi` | `[[thread_index_in_simdgroup]]` |
| `subgroup_in_group` | derived | derived | `[[simdgroup_index_in_threadgroup]]` |

### 6.3 Group memory (spec §8.2)

|  | Static `group` | `dynamic_group` |
| --- | --- | --- |
| `ptx` | `.shared .align N .b8 tile[1024]` | `extern .shared .align N .b8 name[]`, length from hidden kernarg |
| `amdgcn` | `group_segment_fixed_size` | LDS base past the static block, length from hidden kernarg |
| `msl` | `threadgroup float tile[256]` local | `threadgroup uchar name [[threadgroup(0)]]`, host-sized via `setThreadgroupMemoryLength:atIndex:0`, length from the hidden argument-buffer field |

On `msl` the dynamic allocation is a separate threadgroup-space parameter, not reachable through the argument buffer; only its length travels there.

### 6.4 Barriers (spec §10.1)

| Backend | Execution | Memory |
| --- | --- | --- |
| `ptx` | `bar.sync 0` / `bar.warp.sync` | group ordering implied by `bar.sync`; grid adds `membar.gl` |
| `amdgcn` | `s_barrier` | `s_waitcnt lgkmcnt(0)` and/or `vmcnt(0)`, plus cache ops for grid |
| `msl` | `threadgroup_barrier` / `simdgroup_barrier` | explicit `mem_flags::mem_threadgroup` / `mem_device` |

A barrier is a scheduling point on all three backends; only whether memory ordering needs separate instructions varies.

### 6.5 Everything else

| gvir construct | ptx | amdgcn | msl | Cost |
| --- | --- | --- | --- | --- |
| Flat CFG + merge | Direct | Structurize → EXEC masks | Structurize → statements | **High on 2/3** |
| Atomic scope | `.cta` / `.gpu` | `glc`/`slc`/scope bits | `mem_threadgroup` / `mem_device` | Low |
| Atomic ordering | via `fence` | via `fence` | via `fence` | **Relaxed RMW only** |
| `vec[T,N]` | Scalarize | Scalarize | Native | Low |
| Kernel args | Packed kernarg | Packed kernarg | Argument buffer at `[[buffer(0)]]` | Low; Tier 2 required |
| `group_size` | `.reqntid` | `reqd_work_group_size` | Product as `max_total_threads_per_threadgroup` + host check | Low |
| `min_groups_per_unit` | `.minnctapersm` | `amdgpu-waves-per-eu` | Dropped | None (advisory) |
| `dynamic_group_size` | Hidden kernarg | Hidden kernarg | Hidden argument-buffer field | None |
| `ballot` / `submask` | 32-bit mask | 32 **or 64**-bit mask | `simd_vote` | Opaque type earns its keep |
| Approx math | `.approx` variants | Native | `fast::` namespace | Low |
| `f64` | Native | Native | **Absent** | Gated |
| `bf16` | sm_80+ | gfx90a+ | metal31+ | Gated |
| `subgroup_size` | Checked = 32 | Wave-size mode | **Absent** | Gated |

### 6.6 Uneven edges

Four places where the abstraction is not free, in descending cost order:

* **Control flow** — costs a structurization pass on `amdgcn` and `msl`. Merge annotations are the compromise: the frontend already knows its merge points.
* **Atomic ordering** — Metal exposes relaxed atomics only, so v1 restricts RMW to relaxed and routes ordering through `fence` (spec §10.2), costing a separate instruction where it would otherwise fold in. Per-operation ordering is a v2 candidate (§8).
* **Gated types and attributes** — `f64`, `bf16`, and `subgroup_size` each vanish on at least one target. Gating makes the loss per-artifact and visible in metadata rather than per-module and fatal, but some kernels do not run everywhere and the host must check.
* **Subgroup width** — a runtime value, not a constant. Code hardcoding 32 is wrong on MI300.

---

## 7. Verification Summary

The verifier runs once, single-pass, before gating and lowering.

* Version line, section order, declare-before-use, flat namespace, no shadowing, no `__` in identifiers.
* Type well-formedness: `vec` widths, `vec[i1,N]` and `submask` value-only placement, `align` literal power-of-two in range.
* Address-space rules: no cross-space `bitcast` or comparison, no writes through `constant`.
* CFG reducibility; merge annotation presence, dominance, and non-sharing (including the loop-continue exception of spec §7.2).
* Join Convention: definite assignment, type fixation, address-space fixation, `submask` / `vec[i1,N]` barrier-crossing.
* `alloca` entry-block-only and statically sized.
* Call graph acyclicity; no indirect calls; no address-of-function.
* Kernel parameter type restrictions; kernarg layout computable including hidden trailer.
* Function return types are value types (no aggregates).
* Atomic type/space/scope legality; `fence` ordering legality; approximate opcodes gated on `float_profile`.
* Builtin dimension-suffix legality and result-width correctness.
* `dynamic_group` at most once per kernel.

Two checks run **after** verification because both are per-artifact rather than per-module:

* **Gating** (spec §4.3) — transitive feature use, artifact exclusion, and the error case of a kernel excluded everywhere.
* **Resource limits** (spec §6.5) — static `group` and scratch totals against each target's capacity.

---

## 8. Not in v1

| Feature | Why deferred |
| --- | --- |
| Matrix/tensor fragments | Needs a type-system extension for opaque distributed values, not a new opcode class |
| Async copy | Needs a commit/wait system orthogonal to atomic scopes, plus mbarrier-like objects |
| Subgroup scans | Constructible from v1 shuffles; a portable opcode with matching performance needs more design |
| Ordering on atomic RMW | Metal exposes relaxed only; `fence` covers the same ground portably |
| `vec` widths 8 and 16 | No native MSL form above width 4; split-and-recombine everywhere is a layout convenience, not a type |
| NaN-propagating `min`/`max` | Quieting form is the hardware primitive; propagating is a frontend compare-and-select |
| `frem` / `fmod` | No hardware primitive on any target; belongs in a library |
| Generic/flat pointers | Needs space-tagged pointers and space inference at every dereference |
| `system` atomic scope | Host-visible atomics need coherence guarantees that differ sharply across platforms |
| Grid-wide barrier / cooperative groups | Needs an occupancy-aware launch contract the host interface does not model |
| Textures, samplers, images | Graphics-adjacent; the three backends' models barely overlap |
| Printf / logging | Needs a hidden buffer argument and host-side format decoding |
| Per-function `float_profile` | Needs a scoping and inlining-interaction story |
| `.gvbyte` binary encoding | A serialization format is a compatibility commitment; text suffices for v1 |
| Precompiled `.metallib` / cubin | Both require shipping vendor toolchains in the build |
| Debug info beyond `loc` | Line/column is the portable subset; variable-location formats diverge sharply |

---

## 9. Relationship to Vertex IR

`.gvir` v1.0 is a sibling of `.vir` v2.1. Every row describes `.gvir` **the language**; the generated host fragment (§3.1) is ordinary `.vir` and does emit `global`, `extern`, `link`, and `fn`. The "None"/"Absent" entries are about what a `.gvir` source file may contain, not what the bridge produces.

| Aspect | `.vir` v2.1 | `.gvir` v1.0 | Why |
| --- | --- | --- | --- |
| Pointers | Untyped `ptr`, width = target `usize` | Space-qualified, always 64-bit | No generic pointer on GPUs |
| Integers | `i1`–`i128` | `i1`–`i64` | No 128-bit integers on any GPU |
| Floats | `f16`/`f32`/`f64` | + `bf16`; `f64` and `bf16` gated | ML workloads; Metal has no double |
| Vectors | Arbitrary width | 2, 3, 4 only | Width 4 is the MSL ceiling |
| Div by zero | **Trap** | **Defined (yields `0`)** | No cheap per-lane fault |
| Float→int OOR | **Trap** | **Defined (saturates; NaN → `0`)** | Same |
| `trap` | Present | **Absent** | Same |
| `alloca` | Per-execution, loops | Entry-block only, static | No dynamic per-thread stack |
| CFG | Reducible, unannotated | Reducible + **annotations** | Backends need structure |
| Forward progress | Assumed | **Not assumed** | Persistent-kernel idioms are legitimate |
| Contraction | `fma` only, explicit | Permitted under `bounded` | GPUs contract by default |
| Atomic ordering | Full C++-style set on RMW | **Relaxed RMW + `fence`** | Metal exposes relaxed only |
| Calls | Direct, indirect, `tailcall`, `syscall` | **Direct only** | No function pointers, no syscalls |
| Aggregate returns | Permitted | **Absent** | Aggregates are memory-only |
| Globals | `global`, `tls` | **None** (only `const`/`group`) | No mutable module state |
| Exports | `link`, `extern` | **None** | Artifacts are vendor blobs |
| Variadics | `valist` | **Absent** | No varargs ABI |
| UB count | 10 | 10 | Exhaustive, hardware-aligned |