# `gpu/lower/msl`

```go
import lower "github.com/vertex-language/vvm/gpu/lower/msl"
```

The `msl` package lowers a verified `.gvir` device module (`ir/gvir.Module`) into an MSL (Metal Shading Language) IR module (`gpu/ir/msl.Module`).

This package is responsible for the translation from the architecture-independent `.gvir` representation to the `.msl` source-level representation, adhering to the semantic rules defined by the VVM specification (§11 and §12).

It produces **one artifact**: `.gvir` declares at most one `msl` arch, and the arch is a language floor rather than a binary target, so a single translation unit covers everything at or above it (§3).

The result is IR, not text. Print it with `gpu/ir/msl/encoding/text`.

```go
res, err := lower.Lower(m) // m: *gvir.Module, already ir/verify'd
if err != nil {
    log.Fatal(err) // a §1.1 lowering error: this artifact fails
}
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}
msl.Resolve(res.Module)
src, _ := text.Print(res.Module)
```

> **Note on Naming:** This is `package msl`, and it imports the `gpu/ir/msl` package under the same name. Because Go only qualifies *imported* identifiers, every instance of `msl.` in this package's source files refers to the IR package. Identifiers native to this package are always unqualified.

---

## Architecture Overview

The translation is handled by several components, organized logically by responsibility:

| Component | Responsibility |
| --- | --- |
| `msl.go` | Entry points (`Lower`, `LowerOptions`), `Options`/`Result` structures, target architecture versioning, struct layout computation, and constant emission. |
| `gating.go` | Implements §4.3 capability gating, walking the call graph to determine per-kernel feature usage and deciding which kernels must be excluded for the targeted MSL version. |
| `types.go` | Type mappings (`gvir.Type` to `msl.Type`), address space resolution, identifier mangling (avoiding MSL reserved keywords), and the MSL specific layout rules. |
| `values.go` | Implementation of the §7.3 Join Convention (values merge across blocks via same-name assignment). Handles operand materialization and pointee type tracking. |
| `callable.go` | High-level translation of kernels and functions, including the §6.3 packed argument buffer layout, `group` and `dynamic_group` memory declarations, and the function prologue. |
| `cfg.go` | The structurizer. Reconstructs structured control flow (`if`, `while`, `switch`) from `.gvir`'s annotated, reducible Control Flow Graph. |
| `isel.go` | Instruction selection for scalar and vector compute operations (arithmetic, bitwise, float semantics, comparisons, conversions). |
| `isel_mem.go` | Instruction selection for memory operations (`alloca`, `load`, `store`, pointer arithmetic, vector opcodes, and bulk memory transfers). |
| `isel_sync.go` | Translation of synchronization primitives: barriers, fences, atomics, subgroup collectives, and `submask` arithmetic. |
| `builtin.go` | Translation of §9 execution environment builtins (e.g., `ThreadPositionInGrid`), including the normative `i64` linearizations. |

### Note on Register Allocation

This backend implements **no register model**. MSL is a source language; the Metal compiler handles instruction selection, scheduling, and register allocation. This package's sole responsibility is generating a C++ program that guarantees the execution semantics defined by the `.gvir` specification.

---

## Semantic Implementation Details

### 1. The Value Model and Join Convention

**One name, one whole-body local.** §7.3 merges values across blocks by same-name assignment. To map this cleanly to C++, every `.gvir` binding is declared exactly once in a preamble at the top of the function. Every instruction is an assignment to its respective local variable.

* **No Phi Nodes:** This eliminates the need for Phi insertion or loop-carried state management.
* **Scope Clarity:** Declaring all variables in the outermost scope prevents issues where a variable first written inside an `if` block is inaccessible outside it.

**Untyped Pointers.** A `.gvir` pointer specifies an address space but lacks a pointee type. Because MSL requires typed pointers and prohibits direct integer-to-pointer casting, all pointers are lowered as byte pointers (`<space> uchar*`). Memory access is achieved via a template reinterpretation helper:

```cpp
template <typename T> inline device T *vv_at(device uchar *p) { return (device T *)p; }
```

An instruction like `load.f32 p` compiles to `*vv_at<float>(p)`. The address space determines which `vv_at` overload is selected.

### 2. Signedness

In `.gvir`, signedness is defined by the opcode (e.g., `sdiv` vs `udiv`). In MSL, signedness is defined by the *type*. Integer types in this backend always map to the signed MSL variant. When an unsigned operation (like `udiv`) is encountered, the operands are cast to their unsigned twin using `as_type`, the operation is performed, and the result is cast back:

```cpp
// udiv.i32 a, b
as_type<int>(as_type<uint>(a) / as_type<uint>(b))
```

### 3. Memory Spaces and Arguments

| `.gvir` Space | MSL Space | Bound By |
| --- | --- | --- |
| `global` | `device` | Argument buffer field |
| `constant` | `constant` | Argument buffer field |
| `group` | `threadgroup` | `group` decl (local) or `dynamic_group` (parameter) |
| `private` | `thread` | `alloca` (function-scope local) |

* **Argument Buffer:** The §6.3 packed layout is explicitly constructed as an MSL struct with manual padding to guarantee byte-for-byte layout compatibility. It is bound to `[[buffer(0)]]`.
* **Dynamic Group Size:** Since Metal does not expose the threadgroup allocation length natively to the shader, this backend accepts the byte count via a backend-private value passed at `[[buffer(1)]]`.

### 4. Control Flow Structurization

Because MSL (C++) lacks arbitrary `goto` or fallbacks, `.gvir`'s §7.2 merge annotations are strictly enforced:

* `loop_merge Lexit, Lcontinue` becomes `while (true) { ... }`. A conditional exit at the header simply becomes `if (c) { body } else { break; }`.
* `merge L` on a `br_if` becomes `if` / `else`.
* `merge L` on a `switch` becomes a C++ `switch`.
* `unreachable` is emitted as `return` with a comment, which conforms to the UB requirements of §12.6 while keeping the shader well-formed.

### 5. Semantic Divergence Handling

Where Metal's default behavior diverges from `.gvir`'s strict definitions, this backend injects explicit handling:

* **Division by Zero:** Metal leaves division by zero undefined; `.gvir` pins it to `0`. The denominator is checked and the result is forced to `0` if needed.
* **Shift Counts:** Shift counts equal to or exceeding the bit width are UB in C++; `.gvir` masks them. The bitwise mask (`& (N-1)`) is explicitly emitted.
* **Min/Max:** MSL's `min`/`max` do not quiet NaNs. The backend explicitly uses `fmin`/`fmax` which align with IEEE `minNum`/`maxNum`.
* **Float-to-Int:** Metal's conversion is undefined out of bounds. The backend clamps inputs to exact powers of two to ensure saturating conversion.

### 6. Synchronization and Atomics

* **Relaxed Atomics:** `.gvir` atomics are strictly relaxed. MSL conforms directly by executing all atomics with `memory_order_relaxed`.
* **Fences:** `fence relaxed` is a no-op. Any stronger ordering (acquire/release) emits `atomic_thread_fence` with `memory_order_seq_cst`, ensuring compliance by over-constraining the memory model.

---

## Capability Gating and Feature Support

`gating.go` enforces the rules of §4.3. On Metal, two features are universally unsupported and immediately cause kernel exclusion if used:

1. **`f64` (Double-Precision Float):** No Apple GPU supports hardware `f64`.
2. **Explicit Subgroup Sizes:** Metal does not support declaring a fixed subgroup size.

When a kernel requires these features, it is excluded from the MSL artifact (`Result.Excluded`). This is not a fatal error; `ir/verify` handles whole-module judgments regarding whether a kernel must be supported by *some* backend.

## Missing / Unimplemented Features (TODOs)

* **Builtins Inside `func`:** Currently, §9 builtins can only be accessed from entry point kernels, as threading them through normal function calls requires altering the MSL calling convention.
* **64-bit `umulh`/`smulh`:** `mulhi` is only 32-bit in MSL. 64-bit high multiplication requires a manual multi-instruction sequence.
* **Atomics on Pointers:** MSL does not have an atomic pointer type, preventing direct support for atomics operating on pointer values.
* **Over-alignment:** Alignments above 16 bytes for `alloca` and `group` cannot be expressed because MSL lacks `alignas` in this context.