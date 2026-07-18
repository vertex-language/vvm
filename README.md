# vvm — Vertex Virtual Machine & Compiler Framework

`vvm` is a high-performance execution engine and ahead-of-time (AOT) compilation framework built entirely around a single, strictly verified intermediate representation: **Vertex IR**. Designed as a unified bytecode toolchain, `vvm` ingests a binary, portable, typed bytecode (`.vbyte`) and lowers it directly to native CPU instructions. It takes a target-independent intermediate representation and carries it all the way down to a native, loader-ready object file — and now all the way through to a final linked binary.

---

## Core Architecture & Unified Bytecode

The framework is engineered to provide the absolute predictability of a native AOT binary while maintaining an intermediate representation structurally prepared for dynamic execution.

* **Unified Binary Bytecode (`.vbyte`):** The frontend contract exclusively targets `.vbyte`. Because it is a pre-parsed binary encoding, `vvm` skips lexing and text-to-structure translation entirely. This enables highly efficient AOT compilation and provides a zero-startup-cost baseline for future Just-In-Time (JIT) optimizations.
* **Hardware-Mapped, CPU-Only IR:** Vertex IR targets physical silicon directly. All types seamlessly map to hardware register classes (`iN`, `fN`, `ptr`, `vec`). There is no embedded runtime, garbage collection, exceptions, or sandboxing. Memory allocation is strictly stack-based (`alloca.ptr`), with heap allocations relying purely on standard `extern` calls (e.g., `malloc`).
* **Deterministic, One-Behavior Opcodes:** Every opcode enforces exactly one behavior. The framework actively rejects fast-math relaxations, flag-modified variants, and target-specific semantics hiding behind identical mnemonics.
* **Flat Control Flow & Join Convention:** Functions are built on structured, non-nested basic blocks. Instead of utilizing strict SSA phi nodes, values merge across blocks via same-name reassignment.
* **Self-Contained Dependency Linking:** Modules declare their own `link` and `extern` dependencies natively, completely eliminating external linker flags and the standard `.o` file toolchain requirements.
* **Verify Once, Trust Everywhere:** A module is strictly verified in a single pass at the top of the pipeline. Every subsequent downstream stage — lowering, generic sectioning, object writing, and linking — assumes the received IR has already passed these robust invariants.

---

## Developer Workflows

The `vvm` CLI exposes dual workflows tailored for the pre-parsed `.vbyte` foundation.

* **`vvm run`:** Quickly compiles the bytecode into a temporary native binary and executes it. The lack of parsing overhead makes this process fast enough to feel like interpreting a script, laying perfect groundwork for future tiered execution.
* **`vvm build`:** Compiles the module into a zero-dependency, statically linked executable. Developers can cross-compile by simply supplying a new `--target` flag against the identical `.vbyte` source.

---

## Installation

```sh
go get github.com/vertex-language/vvm
```

Each stage is contained in its own isolated package. Import only what your compiler pipeline requires:

```go
import (
    "github.com/vertex-language/vvm/ir/vir"
    "github.com/vertex-language/vvm/format/vbyte/text"
    "github.com/vertex-language/vvm/lower/x86_64"
    objx86_64 "github.com/vertex-language/vvm/object/x86_64"
    objw_x86_64 "github.com/vertex-language/vvm/objectwriter/x86_64"
    "github.com/vertex-language/vvm/objectfile/elf"
    "github.com/vertex-language/vvm/linker/elf"
    _ "github.com/vertex-language/vvm/linker/elf/x86_64"
)
```

---

## Quick Start: IR text to a linked native ELF executable

While `.vbyte` is the standard frontend binary boundary, `.vir` serves as its exact human-readable text equivalent, operating strictly as a debugging tool.

```vir
// add.vir — prints "7 + 35 = 42"
module add_example
target x86_64 linux gnu

global fmt array[i8, 14] = "%d + %d = %d\n\0"

extern :
    fn printf(f ptr, ...) i32
end

export fn main() i32:
    a = mov.i32 7
    b = mov.i32 35
    sum = add.i32 a, b
    r = call printf, fmt, a, b, sum
    return 0
end
```

```go
package main

import (
    "os"

    "github.com/vertex-language/vvm/format/vbyte/text"
    "github.com/vertex-language/vvm/ir/vir"
    "github.com/vertex-language/vvm/lower/x86_64"
    objx86_64 "github.com/vertex-language/vvm/object/x86_64"
    objw_x86_64 "github.com/vertex-language/vvm/objectwriter/x86_64"
    "github.com/vertex-language/vvm/objectfile/elf"

    linkelf "github.com/vertex-language/vvm/linker/elf"
    _ "github.com/vertex-language/vvm/linker/elf/x86_64" // blank-import the codegen backend
)

func main() {
    src, _ := os.ReadFile("add.vir")

    m, err := text.Decode(src)            // .vir text -> unverified *vir.Module
    check(err)

    err = vir.Verify(m)                   // the one place invariants are enforced
    check(err)

    p, err := x86_64.Lower(m)             // verified Module -> x86_64.Program
    check(err)

    secs := objx86_64.FromProgram(p)      // Program -> generic sections/symbols/relocs

    b, err := objw_x86_64.ToELF(secs, elf.TargetLinuxAMD64) // real .o bytes
    check(err)
    os.WriteFile("add.o", b, 0644)

    // Stage 6: hand the .o off to the linker to produce a final executable.
    t, err := linkelf.ParseTarget("x86_64-linux-gnu")
    check(err)

    l := linkelf.NewLinker(t)
    l.SetEntryPoint("_start")
    l.AddObject("add.o", b)
    // A real build would also add libc (crt startup objects + libc.so/.a);
    // omitted here for brevity.

    out, err := l.Link()
    check(err)

    os.WriteFile("add", out, 0755)
    // chmod +x add && ./add   →  "7 + 35 = 42"
}

func check(err error) {
    if err != nil {
        panic(err)
    }
}
```

The resulting `add.o` is a genuine, loader-parseable ELF64 relocatable object — inspect it with `readelf -a add.o` or `objdump -dr add.o`. Swapping the format writer for `objw_x86_64.ToMachO`, `ToCOFF`, or `ToFlat`, and the linker import for `linker/macho` or `linker/pe`, retargets the same lowered `Program` all the way to a linked Mach-O or PE32+ binary.

---

## The Compilation Pipeline

The framework relies on two anchor in-memory types:

| Type | Package | Represents |
| --- | --- | --- |
| `vir.Module` | `ir/vir` | A verified Vertex IR program — target-independent unless a target is specifically declared. |
| `<arch>.Program` | `lower/<arch>` | The identical program translated into a target-specific, machine-level lowered representation. |

All other packages in the framework facilitate conversion to or from these types:

| Stage | Conversion | Package | Status |
| --- | --- | --- | --- |
| 1 | `.vbyte` bytes ↔ `vir.Module` | `format/vbyte/binary` | round-trip |
| 2 | `.vir` text ↔ `vir.Module` | `format/vbyte/text` | round-trip |
| 3 | `vir.Module` to `<arch>.Program` | `lower/<arch>` | one-way (x86, x86_64, arm, aarch64) |
| 4 | `<arch>.Program` to generic sections | `object/<arch>` | one-way |
| 5 | generic sections to `.o` bytes | `objectwriter/<arch>` (binds `objectfile`) | one-way |
| 6 | `.o` file(s) to final binary | `linker/{elf,macho,pe}` | one-way — see per-format README for arch coverage |

**The Golden Round-Trip:** Generating distributable bytecode or a debug dump of an unlowered module executes stages 1–2 in reverse. Because `vvm` accepts either source as input, the verified `vir.Module` enforces a canonical `.vir` fixpoint property:

`text.Decode → binary.Encode → binary.Decode → text.Encode == canonical .vir fixpoint`

---

## The Linker (`linker/`)

Stage 6 is now implemented as three independent, format-specific sub-packages under `linker/` — one each for ELF, Mach-O, and PE32+. There is no shared `package linker` at the top of that directory; `os` selects the right sub-package at the call site:

| `os` | Import |
|---|---|
| `linux`, `freebsd`, `netbsd`, `openbsd`, `android`, `none` | `github.com/vertex-language/vvm/linker/elf` |
| `macos`, `ios`, `ios-simulator`, `maccatalyst`, `tvos`, `watchos`, `watchos-simulator`, `bridgeos`, `driverkit`, `visionos`, `visionos-simulator` | `github.com/vertex-language/vvm/linker/macho` |
| `windows`, `uefi` | `github.com/vertex-language/vvm/linker/pe` |

Each sub-package ships its own `Target`/`ParseTarget` (spelled the way that
format's own native tooling spells it — VIR's triple grammar for ELF, a
Clang/`vtool` triple for Mach-O, `link.exe`/`clang-cl` naming for PE), its
own `Linker`/`Builder`, its own `Patcher`/`PLTPatcher` registry, and its own
set of arch subpackages registered via blank-import. Adding an arch to one
format never touches another, and none of the three formats import each
other or anything at the `linker/` top level.

Full docs, quick-start snippets, and the per-arch `Supported()` matrix live
in each sub-package's own README:

- [`linker/elf/README.md`](linker/elf/README.md) — ELF64, VIR target
  grammar, arch registry, GC, dynamic-linking helpers
- [`linker/macho/README.md`](linker/macho/README.md) — Mach-O, Apple triple
  grammar, universal binaries, `linker/macho/codesign`
- [`linker/pe/README.md`](linker/pe/README.md) — PE32+, COFF object/archive
  parsing, import thunks, base relocations
- [`linker/README.md`](linker/README.md) — the one-page router between the
  three above

---

## Extended Design Principles

* **No Shared Types Across Format Boundaries:** While `objectfile/elf.Section` and `objectfile/coff.Section` may seem similar, they are structurally distinct. Each container package sizes its own `Section`/`Symbol`/`Reloc` structs to exactly match the byte layout demands of the format, deliberately avoiding shared flattening. The same principle now extends into `linker/`: `linker/elf`, `linker/macho`, and `linker/pe` each define their own `Target`, `Layout`, and `Patcher` types rather than sharing a facade.
* **Fail Loudly, Never Guess:** Lowering adapters, the verifier, and object adapters are programmed to immediately reject unsupported elements with explicit error names. Silent miscompilations and approximate fallbacks are forbidden. The linker sub-packages follow the same discipline — e.g. `Linker.Supported()` fails fast with a clear error rather than silently falling back to an unregistered codegen path.
* **Strict Dependency Boundaries:** To enforce modularity, `lower/<arch>` only imports `ir/vir`. `object/<arch>` relies on neither `objectfile` nor `objectwriter`. Only `objectwriter/<arch>` and the `linker/*` packages act as integration bridges — and even among the linker sub-packages, `elf`, `macho`, and `pe` do not import one another.

---

## Current Status & Support

| Stage | x86 | x86_64 | arm | aarch64 |
| --- | --- | --- | --- | --- |
| `lower/<arch>` | ✅ | ✅ | ✅ | ✅ |
| `object/<arch>` | ✅ | ✅ | ✅ | ✅ |
| `objectwriter` — ELF | ✅ | ✅ | — (blocked) | ✅ (no MOVZ/MOVK) |
| `objectwriter` — COFF | — | ✅ | — | ✅ |
| `objectwriter` — Mach-O | — | ✅ | — | ✅ |
| `objectwriter` — flat | ✅ | ✅ | ✅ | ✅ |
| `linker/elf` | — (no 32-bit patcher yet) | ✅ | — (no 32-bit patcher yet) | ✅ (little-endian only) |
| `linker/macho` | n/a (never valid for Mach-O) | ✅ | n/a (never valid for Mach-O) | ✅ (`arm64`; `arm64e`/`arm64_32` registered but not yet spec-correct — see `linker/macho` Known limitations) |
| `linker/pe` | — (no 32-bit patcher yet) | ✅ (`/MACHINE:X64`) | — (not yet ported to this pipeline) | ✅ (`/MACHINE:ARM64`; `arm64ec` registered but not CHPE-correct — see `linker/pe` Known limitations) |

*All specific pipeline gaps (e.g., inline `asm`, sub-32-bit atomic RMW, vector lowering, 32-bit codegen for the linker backends, PE export directories, `arm64e` PAC signing) are managed as explicit `TODO` markers within their precise call sites, or called out under each linker sub-package's own "Known limitations" section. Detailed rationale is provided in each sub-package's README.*

---

## Further Reading

* [`docs/ir.md`](https://www.google.com/search?q=docs/ir.md) — The comprehensive Vertex IR language specification covering grammar, the memory model, target/ABI tables, and verifier obligations (§1–§11).
* [`docs/arch.md`](https://www.google.com/search?q=docs/arch.md) — Architectural overview of the AOT-first execution engine and the synergy between `vvm run` and `vvm build`.
* [`linker/README.md`](linker/README.md) — router into the three format-specific linker sub-packages now that stage 6 is implemented.

## License

MIT — see [LICENSE](https://www.google.com/search?q=LICENSE)