# `gpu/lower`

This directory contains the backend lowering passes that translate a verified `.gvir` device module (`ir/gvir.Module`) into architecture-specific IR. Each subpackage targets one family of hardware/runtime and produces its own structured IR module, never text directly — printing is delegated to a corresponding `encoding/text` package.

```go
import (
    amdtx "github.com/vertex-language/vvm/gpu/lower/amdtx"
    msl   "github.com/vertex-language/vvm/gpu/lower/msl"
    ptx   "github.com/vertex-language/vvm/gpu/lower/ptx"
)
```

## Backends

| Package | Target | Output IR | Artifacts per module |
| --- | --- | --- | --- |
| [`amdtx`](./amdtx) | AMD GCN/RDNA (`amdgcn`) | `gpu/ir/amdtx.Module` | One per declared architecture (no JIT fallback) |
| [`msl`](./msl) | Apple Metal Shading Language | `gpu/ir/msl.Module` | One (arch is a language floor, not a binary target) |
| [`ptx`](./ptx) | NVIDIA PTX | `gpu/ir/ptx.Module` | One (PTX is JIT-forward, covers everything at/above the floor) |

Each package exposes the same basic entry-point shape:

* **`Lower`** — lowers with default options (single-arch backends take just the module; `amdtx` additionally takes an architecture string since it must emit one artifact per target).
* **`LowerOptions`** — accepts an `Options` struct for controlling artifact generation (e.g. `amdtx`'s `Debug: true` for `.file`/`.loc` emission).
* **`amdtx`** also exposes **`LowerAll`**, iterating every declared architecture in declaration order.

All three return a `Result` carrying the lowered module and an `Excluded` list of kernels dropped by capability gating:

```go
res, err := lower.Lower(m)
if err != nil {
    log.Fatal(err) // a lowering error: this artifact fails outright
}
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}
src, _ := text.Print(res.Module)
```

## Shared Design Principles

Although the three backends target very different execution models, they implement the same `.gvir` semantics and share a common architectural shape:

* **Value model — §7.3 Join Convention.** All three merge same-name values across blocks by binding a name once (a VGPR, an MSL local, or a PTX virtual register) and reusing it on every later assignment. None require phi-node insertion or special loop-carried-value handling.
* **No generic pointer space.** Every pointer explicitly carries its `.gvir` address space (`global`, `constant`, `group`, `private`), which each backend maps to its own native memory space or addressing convention — VGPR-addressed memory for `amdtx`, typed `vv_at<T>` reinterpretation over byte pointers for `msl`, and space-qualified `ld`/`st` (plus the generic-to-specific `cvta` prologue pair) for `ptx`.
* **Capability gating (§4.3).** Each backend has a dedicated `gating.go` that walks the call graph to determine per-kernel feature usage and quietly excludes kernels that can't be supported on the target, logging them to `Result.Excluded` rather than failing the whole module — whole-module judgments are reserved for `ir/verify`.
* **Control flow structurization.** `.gvir`'s annotated CFG (with §7.2 merge annotations) is handled differently per target: `amdtx` and `msl` rebuild it into structured regions (`if`/`loop`/`switch`, or C++ equivalents) since their targets require structured control flow; `ptx` drops merge annotations entirely and emits a direct branch graph, since a reducible annotated CFG is already legal PTX.
* **Semantic divergence handling.** Each backend explicitly patches spots where the target hardware/language's default behavior diverges from `.gvir`'s pinned semantics — division by zero forced to `0`, shift counts masked rather than left as UB, NaN-quieting `min`/`max` avoided, and so on.
* **Common file layout.** Each package factors its logic the same way: an entry-point file (`{pkg}.go`) for options/state, `callable.go` for kernel/function lowering, `cfg.go` (+ helper files) for control-flow reconstruction, `isel*.go` files for instruction selection, `gating.go` for capability gating, and `types.go` for type/address-space mapping.

## Key Differences

* **Register model:** `amdtx` and `ptx` are virtual-register architectures (everything is a VGPR / one name → one register, with downstream tools — or `ptxas` — handling allocation). `msl` implements **no register model** at all; it emits C++ source and leaves instruction selection and register allocation entirely to the Metal compiler.
* **Artifact count:** `amdtx` fans out into one artifact per declared architecture; `msl` and `ptx` each produce a single artifact covering a floor version.
* **Zero-extension invariant:** `amdtx` and `ptx` both pin sub-dword (`i8`/`i16`) values as zero-extended in wider registers, though the mechanics differ (`ptx` promotes `i8` to 16-bit registers and re-masks after wrapping ops). `msl` has no equivalent concern since it relies on native C++ integer types.

## Current Gaps

Each backend documents its own TODOs (see individual READMEs), but common unimplemented areas across two or more backends include:

* **64-bit operations** — `amdtx` and `ptx` both lack full 64-bit multiply/byte-swap support; `msl` similarly lacks 64-bit `umulh`/`smulh`.
* **Bulk memory transfers** — `amdtx`'s `memcopy`/`memmove`/`memset` thread loops are unimplemented; `ptx` has `memcopy`/`memset` but not `memmove`.

For target-specific details, semantics tables, and full TODO lists, see each subpackage's README: [`amdtx`](./amdtx), [`msl`](./msl), [`ptx`](./ptx).