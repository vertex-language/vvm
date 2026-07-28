# `gvir` — Vertex GPU IR

`package gvir` provides the in-memory representation of the `.gvir` device
compute-kernel IR. Its sole responsibility is to **construct and describe**
the IR; all semantic validation — block ordering, merge annotations, the
uniformity analysis, name binding, capability gating — is deferred to
`ir/verify`.

```go
import "github.com/vertex-language/vvm/ir/gvir"
```

## Scope & Capabilities

`gvir` is the device-only sibling of `vir`, not a subset of it. The
differences are structural, not cosmetic:

* **No host surface.** No entry point, globals, imports, links, TLS or
  namespaces. A module is kernels, funcs, structs and consts.
* **Explicit address spaces.** There is no generic pointer; the space is part
  of the type and part of a name's identity.
* **Annotated CFG.** Every divergent construct carries its reconvergence
  point, so `Block` has a `Merge` field rather than leaving structure implicit.
* **Value-only types.** `i1`, `vec[i1,N]` and `submask` are representable but
  never storable, and the type predicates say so separately.
* **Capability gating, not degradation.** The three gated features are data
  in `targets.go`, queried per artifact.

## File Structure

| File | Purpose |
| --- | --- |
| `module.go` | Core IR definitions (`Module`, `Struct`, `Const`, `Func`, `Kernel`, `Block`, `Merge`, `Instruction`, terminators). |
| `types.go` | The type system, address spaces, and the storable / value / kernel-param predicate families. |
| `opcode.go` | The closed opcode vocabulary, validated at package load via `opTable`. |
| `operand.go` | The operand grammar plus the ordering, scope and dimension vocabularies. |
| `builder.go` | A fluent, 1:1 API for constructing IR objects without enforcing validity. |
| `targets.go` | Backends, arch tables, artifact ordering, §4.3 gating data, and §6.5 resource limits. |
| `layout.go` | Struct, vector, array and kernarg layout — the byte-identical buffer §6.3 defines. |
| `float.go` | Literal formatting: dec-float grammar conformance and exact hex-float spelling. |

## Core IR Components

### 1. Types

* **Scalars:** `i1`, `i8`–`i64` (no `i128`); `f16`, `bf16`, `f32`, `f64`.
  `f16` and `bf16` are distinguished by `FloatType.Brain`, not by width.
* **Pointers:** always space-qualified. The zero-value `PtrType`, spelled
  `PtrWord`, is the bare `ptr` suffix word used by `index.ptr`, `field.ptr`
  and the pointer comparisons — never a value type.
* **Composites:** `vec[T,N]` for `N ∈ {2,3,4}`, plus memory-only `struct` and
  `array`.
* **Opaque:** `submask`, the per-subgroup lane mask, fieldless on purpose.

### 2. Opcodes

* Opcodes are typed integer constants; a mnemonic typo is a compile error.
* Suffix shape, arity, element-type constraint, result rule and behavioural
  flags (builtin, approx, collective, atomic, alignable) are registered once
  in `opTable`.
* `init()` panics if any constant lacks an entry, any name or opcode is
  registered twice, or a registration is internally inconsistent.
* `Opcode.ResultType` derives every mechanically-determined result type;
  `call`, `index` and `field` return `ok == false` because their results
  depend on operands, and `ir/verify` computes those.

### 3. Modules & Execution Flow

* A `Module` mirrors the mandatory section order: version, module, target,
  float profile, structs, consts, funcs, kernels.
* `Func` and `Kernel` share an embedded `Body` (one unlabelled entry block
  plus labelled blocks); `Body.BlockByLabel` never returns the entry block,
  because the entry block cannot be branched to.
* `Successors` deduplicates, so §7.2's "more than one distinct successor"
  test can be written directly against it.
* There are no phi nodes: values merge by same-name assignment (§7.3).

### 4. Targets & Layout

* Artifacts are ordered `ptx`, `amdgcn` archs in declaration order, then
  `msl` — the order the availability bitmask depends on.
* Aliases (`sm90`, `gfx11`, `metal3.2`) are recorded so the verifier can
  reject them with a useful message, never to rewrite them silently.
* `KernargLayout` is the single derivation of the argument buffer that the
  launcher generator, `ir/verify` and the differential suite all share.

---

## Builder Example

```go
m := gvir.NewModule("reduce")
m.SetTarget(gvir.PTX("sm_80"), gvir.AMDGCN("gfx90a", "gfx1100"), gvir.MSL("metal31"))
m.SetFloatProfile(true, false) // contract on, approx off

kb := m.DeclareKernel("sum",
    gvir.Param{Name: "out", Type: gvir.PtrGlobal},
    gvir.Param{Name: "in", Type: gvir.PtrGlobal},
    gvir.Param{Name: "n", Type: gvir.I32},
).GroupSize(256, 1, 1)

kb.Group("tile", gvir.ArrayType{Elem: gvir.F32, Len: 256}, 16)

i := kb.Builtin("i", gvir.OpThreadInGrid, gvir.DimNone)
p := kb.IndexPointer("p", gvir.Ident("in"), i)
v := kb.Load("v", gvir.F32, p)
s := kb.SubReduce("s", gvir.OpSubAdd, gvir.F32, v)
kb.Barrier(gvir.ExecGroup)
kb.AtomicRMW("old", gvir.OpAtomicAdd, gvir.F32,
    gvir.Ident("out"), s, gvir.ScopeGrid)
kb.Return()

layout, _ := m.KernargLayout(kb.Kernel) // byte-identical on all three backends
```