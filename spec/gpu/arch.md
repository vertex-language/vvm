# Vertex GPU IR — Architecture & Host Integration

Companion to `gvir_spec.md`. Covers the compilation pipeline, the `.vir` host bridge, the generated symbol ABI, per-backend lowering, and the v1 scope boundary. §6 is the normative home for backend mapping; the spec defers to it.

The backend set is closed: NVIDIA, AMD, Apple. Nothing here is parameterised over a hypothetical fourth vendor.

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
   ┌────┴───────────────┬─────────────────┬─────────────┐
   ▼                    ▼                 ▼             ▼
  ptx IR             amdtx IR          amdtx IR       msl IR
   │                    │                 │             │
   ▼                    ▼                 ▼             ▼
 .ptx text       gfx1100 hsaco     gfx942 hsaco   .metal text
```

| gvir backend | Package | Model |
| --- | --- | --- |
| `ptx` | `gpu/ir/ptx` | Virtual registers, flat, symbolic labels; text is the wire format |
| `amdgcn` | `gpu/ir/amdtx` | Virtual registers, **structured** (`if`/`else`, `loop`/`breakif`); lowers further to physical amdgcn + ELF |
| `msl` | `gpu/ir/msl` | Named variables, **statement tree**, no labels, no `goto`; text is the wire format |

Two of the three require structured control flow, which is why `.gvir` carries merge annotations (spec §7.2). One of the three — amdgcn — additionally decides *lowering strategy* from divergence, which is why `.gvir` carries a normative uniformity analysis (spec §7.4) rather than leaving divergence to the backend.

**Artifact count.** `ptx` and `msl` produce exactly one artifact each; their arch lists are compilation floors, not multipliers. PTX is JIT-forward above its `.target`, and MSL ships as source compiled at load, so a second artifact for a higher arch would be a duplicate. Only `amdgcn` fans out, because a hsaco is bound to one `gfx` target with no JIT fallback.

---

## 2. Compilation Pipeline

GPU compilation runs to completion before host compilation begins. A `.gvir` failure prevents `.vir` compilation from starting.

```text
                   ┌─────────────────────────────────────────────────┐
  vecmath.gvir ──► │ parse → verify → gate → structurize → lower×N    │
                   └─────────────────────────────────────────────────┘
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

The example declares `target ptx[sm_80], amdgcn[gfx1100, gfx942], msl[metal31]` and yields **four** artifacts plus one generated `.vir` fragment describing all of them.

### 2.1 Passes

| Pass | Class of failure | Notes |
| --- | --- | --- |
| **parse** | verification error | Single pass; strict section order permits it |
| **verify** | verification error | Everything in §7, including the uniformity analysis |
| **gate** | gating error | Spec §4.3; per-artifact, so it cannot run in the single pass |
| **structurize** | — | Cannot fail; the merge annotations guarantee constructibility |
| **lower** | lowering error | Per artifact; resource limits (spec §6.5) land here |

**Uniformity analysis** is part of verify, not a separate stage, because spec §7.4 makes non-uniform arrival at a barrier or collective a verification error rather than UB. Its per-value classification is retained and handed to lowering — the `amdgcn` backend consumes it directly (§6.5). Discarding it and recomputing per backend is a defect: the three backends must agree on divergence, and the only way to guarantee that is one analysis with one result.

**Gating** runs after verification because it is per-artifact. It walks the call graph for transitive feature use per kernel, excludes kernels from artifacts that cannot support them, drops unreachable `func`s, and errors on a kernel excluded everywhere. The resulting availability set feeds §4.3.

**Structurization** converts the annotated CFG to a construct tree. `ptx` flattens it straight back to branches and labels. `amdtx` maps selections to `if`/`else` and loops to `loop`/`breakif`, expanded downstream into `s_and_saveexec` / `s_cbranch_execz` / `s_or_exec`. `msl` maps them to `If`/`For`/`While`. Multi-exit loops use a dispatch-variable fallback.

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

The **declaration** sits at slot 3.5 — after `target`, before `struct` — so a one-pass `.vir` verifier knows the full set of contributed names before reaching any section that could reference them.

Multiple `gpu_module` lines are permitted. Symbol collisions between two GPU modules with the same `module` name are a build-time error.

### 3.2 Path resolution

First hit wins:

1. Relative to the directory containing the `.vir` file that names it.
2. Against each `-I` include path in declaration order.

An absolute path is used as given. A path resolving to more than one file under (2) is an error, not a silent first match.

### 3.3 Artifact form

| Backend | Emission | Alignment | Count |
| --- | --- | --- | --- |
| `ptx` | NUL-terminated byte string | 1 | 1 |
| `msl` | NUL-terminated byte string | 1 | 1 |
| `amdgcn` | Raw ELF bytes, **not** NUL-terminated | 4096 (page) | one per `gfx` arch |

MSL ships as **source text**, compiled by the Metal runtime at load. Precompiled `.metallib` is deferred (§9).

---

## 4. Generated Symbol ABI

Normative. `<M>` is the `.gvir` module identifier, `<K>` a kernel name, `<A>` a `gfx` arch identifier. Spec §2 forbids `__` inside identifiers, so `__` is an unambiguous separator: `<M>__kernel__<K>__group_size_x` has exactly one parse. Arch identifiers are already `ident`s, so no character substitution is performed.

### 4.1 Artifacts

| Symbol | `.vir` type | Meaning |
| --- | --- | --- |
| `<M>__ptx` | `global array[i8,N]` | PTX text, NUL-terminated |
| `<M>__ptx__len` | `const i64` | Byte length, excluding NUL |
| `<M>__msl` | `global array[i8,N]` | MSL text, NUL-terminated |
| `<M>__msl__len` | `const i64` | Byte length, excluding NUL |
| `<M>__amdgcn__<A>` | `global array[i8,N] align 4096` | hsaco ELF image |
| `<M>__amdgcn__<A>__len` | `const i64` | Byte length |

`ptx` and `msl` symbols are unindexed because each has exactly one artifact (§1).

### 4.2 Per-kernel metadata

| Symbol | Type | Meaning |
| --- | --- | --- |
| `<M>__kernel__<K>` | `global array[i8,N]` | Kernel name, NUL-terminated |
| `<M>__kernel__<K>__group_size_x/y/z` | `const i32` | From `group_size`; `0` if unset |
| `<M>__kernel__<K>__max_group_size` | `const i32` | `0` if unset |
| `<M>__kernel__<K>__subgroup_size` | `const i32` | From `subgroup_size`; `0` if unset |
| `<M>__kernel__<K>__group_bytes` | `const i32` | Static `group` total |
| `<M>__kernel__<K>__private_bytes` | `const i32` | Per-thread scratch |
| `<M>__kernel__<K>__kernarg_bytes` | `const i32` | Packed kernarg size (spec §6.3) |
| `<M>__kernel__<K>__dynamic_group` | `const i32` | `1` if the kernel declares `dynamic_group`, else `0` |

`kernarg_bytes` covers explicit arguments only. There is no hidden trailer on any backend (spec §6.3), so this value is a pure function of the parameter list and identical across all artifacts that contain the kernel.

`dynamic_group` exists so the host knows whether `dynamic_group_bytes` is meaningful before it calls a launcher.

### 4.3 Availability

| Symbol | Type | Meaning |
| --- | --- | --- |
| `<M>__backend_count` | `const i32` | Number of artifacts |
| `<M>__backend__<N>__id` | `const i32` | Backend/arch identity of artifact `N` |
| `<M>__kernel__<K>__available` | `const i64` | Bitmask; bit `N` set iff `<K>` exists in artifact `N` |

**Artifact order (normative):** `ptx` (if declared), then `amdgcn` archs in declaration order, then `msl` (if declared). At most 64 artifacts, which the bitmask width makes explicit and which no real target list approaches.

**Backend id encoding (normative):**

```text
id = (backend << 24) | arch_code
```

| Backend | `backend` | `arch_code` |
| --- | --- | --- |
| `ptx` | `1` | SM number (`sm_80` → `80`) |
| `amdgcn` | `2` | `gfx` digits parsed as hex (`gfx1100` → `0x1100`, `gfx90a` → `0x90a`) |
| `msl` | `3` | `major*10 + minor` (`metal31` → `31`) |

So `ptx[sm_80]` is `0x01000050` and `amdgcn[gfx1100]` is `0x02001100`. The encoding is computable in both directions without a side table, which is the point — a host that wants to log which artifact loaded should not need one.

### 4.4 Typed host launch interface

| Symbol | Type |
| --- | --- |
| `<M>__load` | `fn(device ptr) i32` |
| `<M>__unload` | `fn() i32` |
| `<M>__launch__<K>` | `fn(queue ptr, gx i32, gy i32, gz i32, bx i32, by i32, bz i32, dynamic_group_bytes i32, args...) i32` |
| `<M>__active_backend` | `fn() i32` |
| `<M>__subgroup_width` | `fn() i32` |
| `<M>__last_driver_error` | `fn() i64` |

**Dimension units.** `gx, gy, gz` are counts of **groups**, not threads; `bx, by, bz` are threads per group. The launcher always uses `dispatchThreadgroups:` on Metal, so the contract is uniform across backends.

**Shape checking.** If the kernel declares `group_size X,Y,Z`, passing anything other than `(X,Y,Z)` returns `GVIR_ERR_SHAPE`. If it declares `max_group_size N`, a product above `N` returns the same. This check is why the spec lists no UB trigger for group shape: the typed launcher is the only supported host path and it rejects before dispatch. Argument count and type mismatch produce no runtime code at all — the launcher is typed, so it is a `.vir` compile-time error.

**Dynamic group memory.** `dynamic_group_bytes` must be `0` for a kernel whose `<M>__kernel__<K>__dynamic_group` is `0`; a nonzero value returns `GVIR_ERR_RESOURCES`. For a kernel that declares one, the value is provisioned at dispatch and becomes the kernel's `dynamic_group_size` (§6.3). Exceeding the device budget returns `GVIR_ERR_RESOURCES`.

**Runtime queries.** `<M>__active_backend` returns the `id` (§4.3) of the artifact `<M>__load` selected, or `0` if not loaded. `<M>__subgroup_width` returns the device's subgroup width, or `0` if not loaded. The latter exists because spec §9.2 makes subgroup width a runtime value: a host sizing groups to a whole number of subgroups cannot get that number from compile-time metadata, and hardcoding 32 is wrong on MI300 and unknowable on Apple.

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
| `4` | `GVIR_ERR_RESOURCES` | `dynamic_group_bytes` invalid or beyond the device budget |
| `5` | `GVIR_ERR_DRIVER` | Vendor call failed; see `<M>__last_driver_error` |

`<M>__last_driver_error` returns the raw vendor code from the most recent failing call **on the calling thread**, or `0` if none.

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
| `global` | `.global` | `.global` | `device` |
| `group` | `.shared` | `.shared` (LDS) | `threadgroup` |
| `private` | `.local` | `.private` (scratch) | `thread` |
| `constant` | `.const` | `.constant` | `constant` |

Pointers are 64-bit in the IR on every backend. On `amdgcn` the `.shared` and `.private` hardware spaces are 32-bit; lowering narrows and widens at the boundary, and a `ptr[group]` or `ptr[private]` never escapes to a place that observes the difference — spec §5 forbids cross-space `bitcast` and comparison precisely so this narrowing is unobservable.

### 6.2 Builtins (spec §9.1)

| Builtin | ptx | amdgcn | msl |
| --- | --- | --- | --- |
| `thread_in_grid` | `ctaid*ntid+tid` | `wgid*size+tid` | `[[thread_position_in_grid]]` |
| `thread_in_group` | `%tid` | `%tid.{x,y,z}` | `[[thread_position_in_threadgroup]]` |
| `group_in_grid` | `%ctaid` | `%wgid.{x,y,z}` | `[[threadgroup_position_in_grid]]` |
| `thread_in_subgroup` | `%laneid` | `v_mbcnt_lo/hi` | `[[thread_index_in_simdgroup]]` |
| `subgroup_in_group` | derived | derived | `[[simdgroup_index_in_threadgroup]]` |
| `threads_per_subgroup` | constant 32 | `.wave` value | `[[threads_per_simdgroup]]` |

On `gfx90a`, `gfx942`, and `gfx1100` the three work-item IDs arrive **packed into one VGPR**, 10 bits each. AMDTX presents them as three independent values and lowering emits the unpack; the `.gvir` backend must not assume three separate initial registers.

### 6.3 Group memory and the dynamic length (spec §8.2)

|  | Static `group` | `dynamic_group` |
| --- | --- | --- |
| `ptx` | `.shared .align N .b8 tile[1024]` | `extern .shared .align N .b8 name[]` |
| `amdgcn` | `.shared` object, `.group_segment_size` | `.dynamic_group_segment` flag; LDS base past the static block |
| `msl` | `threadgroup float tile[256]` local | `threadgroup uchar name [[threadgroup(0)]]`, host-sized via `setThreadgroupMemoryLength:atIndex:0` |

**`dynamic_group_size` sources the length natively on each backend.** Spec §6.3 deletes the hidden kernarg trailer, so the value comes from:

| Backend | Source |
| --- | --- |
| `ptx` | `%dynamic_smem_size` special register |
| `amdgcn` | `hidden_dynamic_lds_size` in the code-object-V5 implicit block; lowering MUST source the offset from the LLVM definition, never from a constant written here |
| `msl` | A `constant uint&` at `[[buffer(1)]]`, written by the launcher |

The `msl` path needs a binding because Metal exposes no query for a `[[threadgroup(n)]]` array's length. It is a **separate buffer binding, not a field in the argument buffer**, which is what preserves the byte-identical kernarg layout the spec §13 conformance suite checks. `[[buffer(1)]]` is present only when the kernel declares `dynamic_group`; a kernel without one binds nothing there and `dynamic_group_size` folds to the constant `0`.

### 6.4 Barriers (spec §10.1)

| Backend | Execution | Memory |
| --- | --- | --- |
| `ptx` | `bar.sync 0` / `bar.warp.sync` | group ordering implied by `bar.sync`; grid adds `membar.gl` |
| `amdgcn` | `s_barrier` | `waitcnt lgkmcnt(0)` and/or `vmcnt(0)`, plus `fence` cache ops for grid |
| `msl` | `threadgroup_barrier` / `simdgroup_barrier` | `mem_flags::mem_threadgroup` / `mem_device` |

A barrier is a scheduling point on all three backends; only whether memory ordering needs separate instructions varies. AMDTX **V21** rejects `s_barrier` under divergent control flow, which spec §7.4 guarantees cannot occur in a verified module — see §6.5.

### 6.5 Uniformity and control flow

This is the mapping the spec's uniformity analysis exists to serve, and it is the one place where a purely semantic IR would not have been lowerable.

**amdgcn.** AMDTX derives uniformity from the guard operand's register class: a `.lanemask` guard is divergent and lowers to EXEC save/and/restore; an `.sgpr.b32` or `%scc` guard is uniform and lowers to `s_cbranch_scc*`. The class is a *lowering decision*, and getting it wrong is not a performance issue but a correctness one — AMDTX **V21** statically rejects `s_barrier` inside a divergent region. A backend that conservatively put every `br_if` condition in a `.lanemask` would make every conditional `barrier.group` a hard verification failure downstream, for a module `.gvir` considers legal.

Lowering therefore reads the spec §7.4 classification directly:

| §7.4 result for the condition | AMDTX guard class | Optional annotation |
| --- | --- | --- |
| Uniform at `group` | `.sgpr.b32` / `%scc` | `.uniform` |
| Uniform at `subgroup` only | `.sgpr.b32` | `.uniform` |
| Not uniform | `.lanemask` | `.divergent` |

Emitting the explicit `.uniform` / `.divergent` annotation is recommended: AMDTX treats it as an assertion checked against the operand class, so it converts a lowering bug into a verifier diagnostic instead of miscompiled EXEC arithmetic.

Because spec §7.4 makes a barrier under a non-uniform condition a `.gvir` verification error, **V21** can never fire on output from a conforming lowering. If it does, the bug is here, not in the user's module.

**ptx.** Uniformity is not required for correctness. Lowering may set `.uni` on `bra` for a group-uniform condition; nothing depends on it.

**msl.** Uniformity is not required. Statements nest; the compiler handles reconvergence.

**Cross-lane operations.** `shuffle`, `shuffle_xor`, and `shuffle_up`/`down` take runtime lane operands. On `amdgcn` these lower to `ds_bpermute_b32`, not `v_readlane_b32` — AMDTX **V22** restricts `v_readlane` and the `v_permlane*` family to lane indices below the active wave width, which a runtime operand cannot guarantee. `broadcast` with a uniform lane may use `v_readlane_b32`, since spec §7.4 proves the index uniform, and `broadcast_first` maps to `v_readfirstlane_b32`.

**Structured vs explicit form.** AMDTX forbids mixing structured regions and explicit labels within one body (**V26**) and forbids branching across a region boundary (**V30**). The structurizer emits one form per body — structured wherever the construct tree permits, which the merge annotations guarantee for everything except the multi-exit-loop dispatch-variable fallback.

### 6.6 Float literals and profile

**Literals.** Spec §2 requires exact, correctly-rounded literal conversion, so lowering emits bit patterns, never re-formatted decimal:

| Backend | Form |
| --- | --- |
| `ptx` | `0f` + 8 hex digits / `0d` + 16 hex digits — the only form the package offers, so NaN and the infinities are exact by construction |
| `amdgcn` | C99 hex-float, or an inline constant where the value is one |
| `msl` | Decimal for ordinary finite values; **`INFINITY`, `-INFINITY`, and `NAN` for the non-finite ones** |

The `msl` case is a required special-case, not a preference: the package's float formatter appends `.0` to any output containing no `.`, `e`, or `E`, which turns `+Inf` into `+Inf.0` and `NaN` into `NaN.0` — invalid Metal, emitted silently. Lowering MUST NOT route a non-finite value through the plain float constructor. Where a bit pattern must be exact rather than merely correct, `as_type<float>(0x7f800000u)` through the raw-expression hatch is the fallback.

**Profile.** The two spec §11.6 flags are independent and map separately:

| Flag | ptx | amdgcn | msl |
| --- | --- | --- | --- |
| `contract` | permit `fma` formation | permit `v_fma_*` formation | permit default contraction |
| neither | emit `mul`, `add` unfused | emit unfused | suppress contraction |
| `approx` | `.approx` qualifier variants | native transcendentals | `fast::` namespace |

`fma` written explicitly in the IR is always a single rounding and is unaffected by `contract`.

### 6.7 Everything else

| gvir construct | ptx | amdgcn | msl | Cost |
| --- | --- | --- | --- | --- |
| Flat CFG + merge | Direct | Structurize → EXEC masks | Structurize → statements | **High on 2/3** |
| Uniformity | `.uni` hint only | **Guard register class** | Unused | Analysis is mandatory |
| Atomic scope | `.cta` / `.gpu` | cache-policy modifiers | `mem_threadgroup` / `mem_device` | Low |
| Atomic ordering | via `fence` | via `fence` | via `fence` | **Relaxed RMW only** |
| `vec[T,N]` | Scalarize | Scalarize | Native | Low |
| Kernel args | Packed kernarg | Packed kernarg | Argument buffer at `[[buffer(0)]]` | Low; Tier 2 required |
| `group_size` | `.reqntid` | `.reqd_workgroup_size` | Product as `max_total_threads_per_threadgroup` + host check | Low |
| `max_group_size` | `.maxntid` | `.max_flat_workgroup_size` | `max_total_threads_per_threadgroup` | Low |
| `dynamic_group_size` | `%dynamic_smem_size` | COV5 implicit block | `[[buffer(1)]]` | None |
| `func` | `.func` or inlined | **Always inlined** | Plain function | None |
| `ballot` / `submask` | 32-bit mask | `.lanemask`, wave-width | `simd_vote` | Opaque type earns its keep |
| Approx math | `.approx` variants | Native | `fast::` namespace | Low |
| `f64` | Native | Native | **Absent** | Gated |
| `bf16` | sm_80+ | gfx90a+ | metal31+ | Gated |
| `subgroup_size` | Checked = 32 | `.wave` selection | **Absent** | Gated |

`func` is inlined unconditionally on `amdgcn` — AMDTX 1.0 defines no calling convention, no stack frame, and no call ABI — which is why spec §6.4 has no `inline`/`noinline` attribute to honour. `readonly` is consumed as an alias fact during inlining, not emitted.

Without `subgroup_size`, `amdgcn` uses the target's default wave width: 64 on `gfx900`/`gfx90a`/`gfx942`, 32 on `gfx1030`/`gfx1100`. This is observable through `threads_per_subgroup`, so it is normative, not an implementation choice.

### 6.8 Uneven edges

Three places where the abstraction is not free, in descending cost order:

* **Control flow and divergence** — costs a structurization pass on `amdgcn` and `msl`, plus a uniformity analysis that all three must agree on. Merge annotations and the §7.4 classification are the compromise: the frontend already knows both, and recovering them in the backend is strictly harder.
* **Atomic ordering** — Metal exposes relaxed atomics only, so RMW is relaxed everywhere and ordering routes through `fence`, costing a separate instruction where it would otherwise fold in. Per-operation ordering is a v2 candidate (§9).
* **Gated types and attributes** — `f64`, `bf16`, and `subgroup_size` each vanish on at least one target. Gating makes the loss per-artifact and visible in metadata rather than per-module and fatal, but some kernels do not run everywhere and the host must check.

Subgroup width is no longer on this list. It is a runtime value with a runtime query (`<M>__subgroup_width`), which is the whole of the fix.

---

## 7. Verification Summary

The verifier runs once, single-pass, before gating and lowering.

* Version line, section order, declare-before-use, flat namespace, no shadowing, no `__` in identifiers.
* Type well-formedness: `vec` widths, value-only placement of `i1`, `vec[i1,N]`, and `submask`, `align` literal power-of-two in range.
* Float literal convertibility: correctly-rounded decimal, well-formed hex-float.
* Address-space rules: no cross-space `bitcast` or comparison, no writes through `constant`.
* CFG reducibility; merge annotation presence on every cycle entry and every multi-successor selection, dominance, and non-sharing.
* Join Convention: definite assignment, type fixation, address-space fixation, `submask` / `vec[i1,N]` barrier-crossing.
* **Uniformity** (spec §7.4): classification of every value, then the three obligations — `barrier` control dependences uniform at its execution scope, collective control dependences uniform at `subgroup`, `broadcast` lane uniform at `subgroup`.
* `alloca` entry-block-only and statically sized.
* Call graph acyclicity; no indirect calls; no address-of-function.
* Kernel parameter type restrictions; kernarg layout computable.
* Function return types are value types (no aggregates).
* Atomic type/space/scope legality; `fence` ordering legality; approximate opcodes gated on `float_profile approx`.
* Builtin dimension-suffix legality and result-width correctness.
* `dynamic_group` at most once per kernel.
* Declared arch floors at or above `sm_70` / `gfx900` / `metal30`.

Two checks run **after** verification because both are per-artifact rather than per-module:

* **Gating** (spec §4.3) — transitive feature use, artifact exclusion, and the error case of a kernel excluded everywhere.
* **Resource limits** (spec §6.5) — static `group` and scratch totals against each target's capacity.

---

## 8. Vendor Package Constraints

The three backend packages have documented defects and deliberate non-guarantees. Lowering must be written around them; none is a reason to weaken `.gvir`.

**All three: the vendor toolchain is the verifier of record.** `ptxas`, the AMDGPU assembler, and the Metal frontend are the real checkers. The packages' own `Verify` passes are fast structural pre-passes, not substitutes. Per spec §13, a verified `.gvir` module whose lowered output draws a vendor diagnostic is a compiler defect — so lowering may not treat a package `Verify` pass as evidence of correctness.

**msl — do not rely on `Resolve`.** The name-hygiene pass is the least complete part of that package: parameter renaming does not rewrite uses, nested block scopes are never visited, rewriting is unbounded relative to the declaration, and three expression positions are missed entirely. The `.gvir` backend MUST generate globally unique names for every declaration it emits and MUST NOT depend on shadow resolution. `.gvir`'s flat, no-shadowing namespace (spec §2) makes this cheap — names are already unique on the way in.

**msl — `Verify` does not descend into nested blocks** for variable rules, and unlisted scalar spellings and raw attributes pass unchecked. Neither is load-bearing for `.gvir`, which emits only modelled types, but neither may be relied on to catch a lowering bug.

**msl — array types must reach the printer through a declarator.** An array as a template argument, pointee, or struct field type reached any other way prints C-invalid text. `group` arrays and `alloca` arrays lower to declarators, which is the only correct path.

**ptx — do not use named registers.** `RegFile.Named` checks only its own name map, which generated registers never enter, so `Named(U32, "r1")` silently collides with the generated `%r1`. Lowering uses `New`/`NewN` exclusively; `.gvir` value names live in `loc` and comments, not in register spellings.

**ptx — `Verify` does not descend into nested blocks,** so anything emitted through `Invoke`'s call sequence is unverified and labels bound inside a block do not enter the bound set. `.gvir` emits no calls through that path on `ptx` (functions are direct calls or inlined), but a future lowering that does must not assume the sequence was checked.

**ptx — `Emit` sets no types and no vector qualifier.** Anything reached through the escape hatch spells its type specifiers and any `.vN` into the mnemonic string. Prefer modelled opcodes; every §11 opcode has one.

**amdtx — `raw` and `rawbytes` must declare defs, uses, and clobbers.** They are optimisation barriers and an undeclared write is rejected where provable. Lowering should not need them; if it does, the declaration is mandatory, not advisory.

**amdtx — VGPR and AGPR share one 512-entry budget on CDNA2 and CDNA3.** Occupancy reporting in metadata must count the sum against 512, never each against 256.

---

## 9. Not in v1

| Feature | Why deferred |
| --- | --- |
| Matrix/tensor fragments | Needs a type-system extension for opaque distributed values, not a new opcode class |
| Async copy | Needs a commit/wait system orthogonal to atomic scopes, plus mbarrier-like objects |
| Subgroup scans | Constructible from v1 shuffles; a portable opcode with matching performance needs more design |
| Ordering on atomic RMW | Metal exposes relaxed only; `fence` covers the same ground portably |
| Multi-arch `ptx` / `msl` artifacts | PTX is JIT-forward and MSL is source; a second artifact would be a duplicate. Revisit only if a vendor ships a genuinely non-forward-compatible text format |
| `vec` widths 8 and 16 | No native MSL form above width 4; split-and-recombine everywhere is a layout convenience, not a type |
| NaN-propagating `min`/`max` | Quieting form is the hardware primitive; propagating is a frontend compare-and-select |
| `frem` / `fmod` | No hardware primitive on any target; belongs in a library |
| Generic/flat pointers | Needs space-tagged pointers and space inference at every dereference |
| `system` atomic scope | Host-visible atomics need coherence guarantees that differ sharply across platforms |
| Grid-wide barrier / cooperative groups | Needs an occupancy-aware launch contract the host interface does not model |
| Occupancy control | `min_groups_per_unit` was advisory, dropped on Metal, and mapped to a min/max pair on AMD. A real version needs a portable model, not a hint |
| Textures, samplers, images | Graphics-adjacent; the three backends' models barely overlap |
| Printf / logging | Needs a hidden buffer argument and host-side format decoding |
| Per-flag `float_profile` scoping | Function-level `contract` / `approx` needs a scoping and inlining-interaction story |
| `.gvbyte` binary encoding | A serialization format is a compatibility commitment; text suffices for v1 |
| Precompiled `.metallib` / cubin | Both require shipping vendor toolchains in the build |
| Debug info beyond `loc` | Line/column is the portable subset; variable-location formats diverge sharply |

---

## 10. Relationship to Vertex IR

`.gvir` v1.0 is a sibling of `.vir` v2.1. Every row describes `.gvir` **the language**; the generated host fragment (§3.1) is ordinary `.vir` and does emit `global`, `extern`, `link`, and `fn`. The "None"/"Absent" entries are about what a `.gvir` source file may contain, not what the bridge produces.

| Aspect | `.vir` v2.1 | `.gvir` v1.0 | Why |
| --- | --- | --- | --- |
| Pointers | Untyped `ptr`, width = target `usize` | Space-qualified, always 64-bit | No generic pointer on GPUs |
| Integers | `i1`–`i128` | `i8`–`i64` storable; `i1` value-only | No 128-bit integers on any GPU; no portable `sizeof(i1)` |
| Floats | `f16`/`f32`/`f64` | + `bf16`; `f64` and `bf16` gated | ML workloads; Metal has no double |
| Vectors | Arbitrary width | 2, 3, 4 only | Width 4 is the MSL ceiling |
| Div by zero | **Trap** | **Defined (yields `0`)** | No cheap per-lane fault |
| Float→int OOR | **Trap** | **Defined (saturates; NaN → `0`)** | Same |
| `trap` | Present | **Absent** | Same |
| `alloca` | Per-execution, loops | Entry-block only, static | No dynamic per-thread stack |
| CFG | Reducible, unannotated | Reducible + **annotations** | Backends need structure |
| Divergence | N/A | **Statically classified** | One backend picks its lowering from it and rejects the wrong choice |
| Forward progress | Assumed | **Not assumed** | Persistent-kernel idioms are legitimate |
| Contraction | `fma` only, explicit | Opt-in via `contract` | GPUs contract by default |
| Volatile access | Present | **Absent** | Unobservable under §13; atomics express the real intent |
| Inlining control | Present | **Absent** | One backend inlines unconditionally |
| Atomic ordering | Full C++-style set on RMW | **Relaxed RMW + `fence`** | Metal exposes relaxed only |
| Calls | Direct, indirect, `tailcall`, `syscall` | **Direct only** | No function pointers, no syscalls |
| Aggregate returns | Permitted | **Absent** | Aggregates are memory-only |
| Globals | `global`, `tls` | **None** (only `const`/`group`) | No mutable module state |
| Exports | `link`, `extern` | **None** | Artifacts are vendor blobs |
| Variadics | `valist` | **Absent** | No varargs ABI |
| UB count | 10 | **8** | Two conditions moved to typed-launcher checks and static verification |