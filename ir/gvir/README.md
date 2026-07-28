# `gvir` - GPU Intermediate Representation (v1.0)

`package gvir` provides the in-memory representation of the `.gvir` GPU Intermediate Representation. Its role is strictly to **construct and describe** the IR; all semantic validation (e.g., typing, merge annotations, Join Convention) is deferred to `ir/verify`.

```go
import "github.com/vertex-language/vvm/ir/gvir"
```

## Scope & Capabilities

A `.gvir` module is a strictly device-side translation unit.

* **No Host Abstractions:** It lacks host entry points, globals, linkage, externs, and cross-module imports.
* **Flat Namespace:** The namespace is flat, reserving the `__` prefix exclusively for the host symbol ABI.
* **Restricted Control Flow:** There are no trap semantics, indirect calls, tail calls, or function-pointer mechanisms.

## File Structure

| File | Purpose |
| --- | --- |
| `module.go` | Core module components (`Module`, `Kernel`, `Func`, `Block`, terminators, merge decls). |
| `types.go` | The type system (scalars, pointers, composites, opaque types) and equality predicates. |
| `opcode.go` | The closed opcode vocabulary and `opTable`, rigorously checked at load time. |
| `builtins.go` | Execution builtins and their contextual result widths. |
| `layout.go` | Universal, backend-agnostic layout rules, struct/kernarg sizing, and memory budgets. |
| `targets.go` | Supported backends, architecture capability gating, and resource limits. |
| `operand.go` | The operand grammar, defining identifiers, literals, orderings, and scopes. |
| `builder.go` | Fluent, 1:1 API for constructing IR objects without semantic validation. |
| `float.go` | Formatting rules ensuring every finite float literal contains a decimal or exponent. |

## Core IR Components

### 1. Types & Pointers

* **Scalars:** Integers range from `i1` to `i64` (no `i128`). Floats (`f16`, `bf16`, `f32`, `f64`) are uniquely keyed by *kind* rather than bit width to handle differing capabilities.
* **Pointers:** Pointers are **always** address-space qualified (e.g., `global`, `group`, `private`, `constant`).
* **Opaque Types:** The `submask` type represents an opaque per-subgroup lane mask and is a fully valid value type. Predicate vectors (`vec[i1,N]`) have no memory representation.

### 2. Opcodes & Builtins

* **Opcodes:** Opcodes are strictly typed integer constants. Atomics are strictly relaxed in v1 (ordering requires a `fence`), and instructions like `store` take the destination as their first operand.
* **Builtins:** Execution builtins take no operands. Result widths depend on the suffix: `.x`/`.y`/`.z` yields `i32`, while unsuffixed positional/extent forms yield a linearized `i64`. Note: `threads_per_subgroup` is always a runtime value.

### 3. Layout Constraints

* **Universal Consistency:** Layout is defined explicitly by the IR and remains identical across every backend (PTX, AMDGCN, MSL).
* **Sizing Rules:** `i1` is one byte, and all pointers are 8 bytes. Vector alignments are padded to the next power of two.
* **Group Memory:** Static group memory size is bounded; exceeding limits (`48 KiB` for PTX, `64 KiB` for AMDGCN, `32 KiB` for MSL) triggers a lowering error.

### 4. Targets & Capability Gating

* **Backends:** Supports `ptx` (defaulting to `sm_70`), `amdgcn` (requires an explicit arch), and `msl` (defaulting to `metal32`).
* **Gating:** Hardware features (`bf16`, `f64`, `subgroup_size`) are gated per-artifact, not globally. For example, `f64` is universally rejected on MSL.

---

## Builder Example

The `gvir` builder API constructs kernels, allocates storage, and maps control flow. Here is an example of a simple `saxpy` kernel:

```go
m := gvir.NewModule("saxpy")
m.AddTarget(gvir.PTX)                        // omitted arch defaults to sm_70
m.AddTarget(gvir.AMDGCN, "gfx90a", "gfx942") // mandatory archs for amdgcn
m.AddTarget(gvir.MSL, "metal32")
m.SetFloatProfile(gvir.ProfileStrict)

kb := m.DeclareKernel("saxpy",
    gvir.Param{Name: "y", Type: gvir.PtrGlobal},
    gvir.Param{Name: "x", Type: gvir.PtrGlobal},
    gvir.Param{Name: "a", Type: gvir.F32},
    gvir.Param{Name: "n", Type: gvir.I64},
)
kb.GroupSize(256, 1, 1)

i := kb.Builtin("i", gvir.OpThreadInGrid, gvir.DimNone) // i64, linearized
c := kb.Emit("c", gvir.OpULt, gvir.I64, i, gvir.Ident("n"))
kb.Merge("done")                                        // Marks block to handle two successors
kb.BrIf(c, "body", "done")

kb.Label("body")
off := kb.Emit("off", gvir.OpMul, gvir.I64, i, gvir.IntLiteral(4))
px := kb.IndexPointer("px", gvir.Ident("x"), off)       // byte arithmetic
py := kb.IndexPointer("py", gvir.Ident("y"), off)
xv := kb.Load("xv", gvir.F32, px)
yv := kb.Load("yv", gvir.F32, py)
r := kb.Fma("r", gvir.F32, gvir.Ident("a"), xv, yv)
kb.Store(gvir.F32, py, r)                               // destination first
kb.Br("done")

kb.Label("done")
kb.Return()                                             // kernels return void, taking no operand
```