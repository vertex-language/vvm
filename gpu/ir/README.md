# gpu/ir

The `gpu/ir` tree provides structured, in-memory Intermediate Representations for GPU compute kernel languages. Each package models a specific target's textual grammar explicitly as a Go API, rather than treating kernel generation as string templating.

```go
import (
    "github.com/vertex-language/vvm/gpu/ir/amdtx"
    "github.com/vertex-language/vvm/gpu/ir/msl"
    "github.com/vertex-language/vvm/gpu/ir/ptx"

    amdtxtext "github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/text"
    msltext   "github.com/vertex-language/vvm/gpu/ir/msl/encoding/text"
    ptxtext   "github.com/vertex-language/vvm/gpu/ir/ptx/encoding/text"
)
```

## Packages

| Package | Target | Models |
|---|---|---|
| [`ptx`](./ptx) — `github.com/vertex-language/vvm/gpu/ir/ptx` | NVIDIA PTX | `.ptx` modules — `.entry` kernels, `.func` device functions, virtual registers, predicated instructions |
| [`msl`](./msl) — `github.com/vertex-language/vvm/gpu/ir/msl` | Apple Metal Shading Language | `.metal` files — `kernel`/`vertex`/`fragment` functions, structs, address spaces, function constants |
| [`amdtx`](./amdtx) — `github.com/vertex-language/vvm/gpu/ir/amdtx` | AMD GPU compute (virtual target) | `.amdtx` modules — `.kernel`/`.func` bodies, width-typed registers, structured control flow |

Each package delegates text formatting to a sibling `encoding/text` package (`ptx/encoding/text`, `msl/encoding/text`, `amdtx/encoding/text`), so the IR itself stays purely structural — building, walking, and rewriting a module never touches string output until `text.Print` is called.

## Shared Design Principles

These packages are built independently but converge on the same philosophy:

- **Grammar-driven.** Every exported symbol corresponds to a construct in the target's grammar. There's no ad-hoc convenience layer sitting on top of the real language — what you construct in Go is what gets printed.
- **No implicit inference.** None of these packages type-check operands or validate instruction semantics. That responsibility stays with the target's own toolchain (`ptxas`, the `metal` frontend, or the relevant AMD assembler) — each package is a structural model, not a verifier.
- **Editable bodies.** Instruction/statement containers support `.Append()`, `.InsertBefore()`, `.Replace()`, and `.Remove()`, so analysis and rewrite passes can manipulate a kernel body directly as Go data rather than re-parsing text.
- **Typed escape hatches.** New instructions, attributes, or scalar spellings appear in real-world shader/kernel languages faster than any typed package can track. Each package exposes an untyped passthrough (`Emit`, `Raw`/`RawDecl`/`RawAttr`, `Raw`/`RawBytes`) that still participates fully in walking, resolution, and printing.
- **One canonical printer.** `text.Print` is the only encoder per package — there's no secondary "quick and dirty" stringification path that could drift out of sync with the grammar model.

## Choosing a package

Pick the package matching your compilation target's ISA/language, not your source language — e.g. targeting AMD hardware through a virtual, GFX-version-agnostic kernel representation uses `amdtx`; targeting NVIDIA hardware directly at the PTX level uses `ptx`; targeting Apple GPUs through Metal source uses `msl`.

## Relationship to `gpu/lower`

These packages are output IRs, not entry points on their own — modules are normally produced by the backends in [`gpu/lower`](../lower) (`gpu/lower/amdtx`, `gpu/lower/msl`, `gpu/lower/ptx`), which translate a verified `.gvir` device module into one of these target IRs. See each package's own README for design rationale specific to that target and a full quick-start example of building a module by hand.