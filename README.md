# vvm — Vertex Virtual Machine & Compiler Framework

`vvm` is a high-performance execution engine and ahead-of-time (AOT) compilation framework built entirely around a single, strictly verified intermediate representation: **Vertex IR**. `vvm` ingests a binary, portable, typed bytecode (`.vbyte`) and lowers it directly to native CPU instructions — target-independent IR all the way down to a loader-ready object file, and now all the way through to a final linked binary.

---

## Core Architecture & Unified Bytecode

* **Unified Binary Bytecode (`.vbyte`):** The frontend contract exclusively targets `.vbyte`. Because it's a pre-parsed binary encoding, `vvm` skips lexing and text-to-structure translation entirely — a zero-startup-cost baseline for AOT compilation and future JIT tiering.
* **Hardware-Mapped, CPU-Only IR:** Vertex IR targets physical silicon directly. All types map to hardware register classes (`iN`, `fN`, `ptr`, `vec`). There is no embedded runtime, garbage collection, exceptions, or sandboxing. Heap allocation goes through ordinary `extern fn` calls (e.g. `malloc`); the only built-in allocation is stack-based (`alloca.ptr`).
* **Deterministic, One-Behavior Opcodes:** Every opcode enforces exactly one behavior. No flag-modified variants, no fast-math relaxations, no target-dependent semantics hiding behind an identical mnemonic.
* **Flat Control Flow & Join Convention:** Functions are built from flat, non-nested basic blocks. Instead of strict SSA phi nodes, values merge across blocks via same-name reassignment, checked by a forward must-analysis.
* **Inline Assembly as Structured Data:** An `asm` block is a first-class, dialect-aware body-line — bindings (`in`/`out`/`clobber`) flatly typed against a register table, code lines structurally verified, never a string template. The dialect (`intel`/`att`/`a32`/`t32`/`native`) is declared **once per module**, governing every `asm` block in every function.
* **Self-Contained Dependency Linking:** Modules declare their own `link` and `extern` dependencies natively, eliminating external linker flags and a separate `.o` toolchain step for authoring.
* **Verify Once, Trust Everywhere:** A module is strictly verified in a single pass at the top of the pipeline (`vir.Verify`). Every downstream stage — lowering, sectioning, object writing, linking — assumes the `vir.Module` it receives has already passed every invariant that stage would otherwise need to re-check.

---

## Repository Layout

```
vvm/
├── vvm.go, target.go, build.go, run.go,       top-level facade: Build/Run over the
│   dispatch.go, registry.go, result.go        whole pipeline for a given Target
│
├── cmd/vvm/                          the vvm CLI (`vvm run`, `vvm build`) — a thin
│                                     wrapper over the vvm package above
│
├── ir/vir/                          the IR: data model, construction API, verifier
│
├── format/
│   ├── vbyte/binary/                  .vbyte  — round-trip, the frontend boundary
│   ├── vbyte/text/                    .vir    — round-trip, human-readable
│   └── asm/{x86,x86_64,arm,aarch64}/text/   debug-only disassembly listings, encode-only
│
├── isa/{x86,x86_64,arm,aarch64}/    static instruction-set facts: registers, condition
│                                     codes, encoding primitives, opcode tables
│
├── lower/{x86,x86_64,arm,aarch64}/  vir.Module -> <arch>.Program (instruction selection,
│                                     ABI/frame layout, inline-asm lowering)
│
├── object/{x86,x86_64,arm,aarch64}/ <arch>.Program -> generic Section/Symbol/Reloc
│
├── objectwriter/{x86,x86_64,arm,aarch64}/  generic sections -> objectfile/<format> bytes
│
├── objectfile/{elf,coff,macho,flat}/       byte-level object-file encoders, no shared types
│
└── linker/{elf,macho,pe}/           .o file(s) -> final linked binary, one linker per format
```

Every directory except `ir/vir` fans out **per architecture or per format**, on purpose: `isa/<arch>`, `lower/<arch>`, `object/<arch>`, and `objectwriter/<arch>` hold nothing in common but a shared *shape*, not shared code, and `objectfile/<format>`/`linker/<format>` do the same across container formats. `ir/vir` is the one package everything else either produces or consumes.

The top-level `vvm` package is the one exception to "isolated stages": like `linker/<format>`, it deliberately straddles every stage at once, so a caller who just wants a binary out of a `vir.Module` doesn't have to wire stages 1–6 together by hand. `cmd/vvm` is a thin CLI shell over that package — it contains no pipeline logic of its own.

---

## Developer Workflows

There are three ways to use `vvm`, in increasing order of control:

1. **The `vvm` CLI** — `vvm run` / `vvm build`, for compiling and running `.vir`/`.vbyte` files from the shell.
2. **The `vvm` Go package** — `vvm.Build`/`vvm.Run`, for embedding the same two workflows in your own program.
3. **The pipeline sub-packages directly** — `ir/vir`, `lower/<arch>`, `object/<arch>`, `objectwriter/<arch>`, `linker/<format>` — for anything the first two don't expose, e.g. intervening between stages, or targeting an `(arch, format)` cell `vvm` doesn't route to yet.

All three ultimately run the same pipeline; (1) and (2) are conveniences over (3), not a different implementation.

* **`vvm run` / `vvm.Run`:** Compiles the module into a temporary native binary for the host platform and executes it immediately. No parsing overhead makes this fast enough to feel like interpreting a script.
* **`vvm build` / `vvm.Build`:** Compiles the module into a zero-dependency, statically linked executable. Cross-compile by supplying a different `--target` (CLI) or `vvm.Target` (Go) against the identical `.vir`/`.vbyte` source.

---

## Installation

```sh
go get github.com/vertex-language/vvm
```

### 1. The simple way — the `vvm` Go package

```go
package main

import (
    "os"

    "github.com/vertex-language/vvm"
)

func main() {
    src, _ := os.ReadFile("add.vir") // .vir or .vbyte — vvm.Build sniffs it

    out, err := vvm.Build(src, vvm.Target{Arch: "x86_64", OS: "linux", ABI: "gnu"})
    if err != nil {
        panic(err)
    }
    os.WriteFile("add", out, 0755)
    // chmod +x add && ./add   →  "7 + 35 = 42"
}
```

Or skip the binary entirely and just run it:

```go
res, err := vvm.Run(src) // builds for the host, executes, streams the result back
if err != nil {
    panic(err)
}
os.Stdout.Write(res.Stdout)
os.Exit(res.ExitCode)
```

`vvm.Build`/`vvm.Run` are the entire pipeline — decode, verify, lower, object, objectwriter, link — in one call each. Already holding a `*vir.Module` instead of source bytes? Use `vvm.BuildModule`/`vvm.RunModule` directly.

### 2. The normal way — the `vvm` CLI

```sh
GOPROXY=direct go install github.com/vertex-language/vvm/cmd/vvm@latest

vvm run add.vir
# → "7 + 35 = 42"

vvm build add.vir --target x86_64-linux-gnu -o add
./add
# → "7 + 35 = 42"

# cross-compile: same source, different --target
vvm build add.vir --target aarch64-macos-none --min-os-version 14.0 -o add_arm64
```

`vvm run` builds for the host and executes immediately — no output file, no `--target` needed, the same spirit as `go run`. `vvm build` writes a standalone linked binary and requires `--target` since there's no "host" default for something you're shipping. Run `vvm help` for the full flag list.

### 3. The manual way — pipeline sub-packages directly

Each pipeline stage also lives in its own isolated package — import only what your build needs:

```go
import (
    "github.com/vertex-language/vvm/ir/vir"
    "github.com/vertex-language/vvm/format/vbyte/text"
    "github.com/vertex-language/vvm/lower/x86_64"
    objx86_64 "github.com/vertex-language/vvm/object/x86_64"
    objw_x86_64 "github.com/vertex-language/vvm/objectwriter/x86_64"
    "github.com/vertex-language/vvm/objectfile/elf"
    linkelf "github.com/vertex-language/vvm/linker/elf"
    _ "github.com/vertex-language/vvm/linker/elf/x86_64" // blank-import the codegen backend
)
```

See [Quick Start](#quick-start-ir-text-to-a-linked-native-elf-executable) below for the full manual walkthrough — it's exactly what `vvm.Build`/the CLI do internally, just spelled out stage by stage, useful when you need to intervene between stages or reach a target the top-level package doesn't cover yet.

---

## Quick Start: IR text to a linked native ELF executable

`.vbyte` is the standard frontend binary boundary; `.vir` is its exact human-readable text equivalent, used here for readability.

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

    m, err := text.Decode(src) // .vir text -> unverified *vir.Module
    check(err)

    err = vir.Verify(m) // the one place invariants are enforced
    check(err)

    p, err := x86_64.Lower(m) // verified Module -> x86_64.Program
    check(err)

    secs := objx86_64.FromProgram(p) // Program -> generic sections/symbols/relocs

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

The resulting `add.o` is a genuine, loader-parseable ELF64 relocatable object — inspect it with `readelf -a add.o` or `objdump -dr add.o`. Swapping the format writer for `objw_x86_64.ToMachO`, `ToCOFF`, or `ToFlat`, and the linker import for `linker/macho` or `linker/pe`, retargets the identical lowered `Program` to a linked Mach-O or PE32+ binary instead.

---

## The Compilation Pipeline

Two anchor in-memory types carry a program through the whole framework:

| Type | Package | Represents |
| --- | --- | --- |
| `vir.Module` | `ir/vir` | A verified Vertex IR program — target-independent unless a `target-decl` is present. |
| `<arch>.Program` | `lower/<arch>` | The same program translated into a target-specific, machine-level lowered representation. |

`isa/<arch>` underlies stage 3 (and `format/asm/<arch>/text`'s listings): register identity, condition codes, and encoding primitives that are facts about the *silicon*, not decisions this compiler makes — kept separate from `lower/<arch>`'s instruction-selection and ABI choices on the same test used consistently across all four architectures.

| Stage | Conversion | Package | Status |
| --- | --- | --- | --- |
| 1 | `.vbyte` bytes ↔ `vir.Module` | `format/vbyte/binary` | round-trip |
| 2 | `.vir` text ↔ `vir.Module` | `format/vbyte/text` | round-trip |
| 3 | `vir.Module` → `<arch>.Program` | `lower/<arch>` | one-way (x86, x86_64, arm, aarch64) |
| 4 | `<arch>.Program` → generic sections | `object/<arch>` | one-way |
| 5 | generic sections → `.o` bytes | `objectwriter/<arch>` (binds `objectfile`) | one-way |
| 6 | `.o` file(s) → final binary | `linker/{elf,macho,pe}` | one-way — see per-format README for arch coverage |
| — | lowered `Program` → human-readable disassembly | `format/asm/<arch>/text` | debug-only, encode-only, never an input format |
| — | stages 1–6 end to end, one call | `vvm` (top-level package) / `cmd/vvm` (CLI) | dispatches by `Target`; falls back to an explicit error for unsupported `(arch, format)` cells rather than guessing |

**The Golden Round-Trip:** generating distributable bytecode or a debug dump of an unlowered module runs stages 1–2 in reverse. Because `vvm` accepts either serialization as input, the verified `vir.Module` enforces a canonical `.vir` fixpoint:

```
text.Decode → binary.Encode → binary.Decode → text.Encode == canonical .vir fixpoint
```

This round-trip covers inline-asm body lines and the module-scoped `AsmDialect` field identically in both codecs.

---

## Inline Assembly

Inline assembly is a dedicated `asm` block, not a templated opcode — an ordinary body-line that reads like real assembly from a manual, structurally verified line by line:

```vir
module syscall_example
target x86_64 linux gnu
asmdialect intel

export fn exit(code i32) void:
    asm :
        in  rdi = code
        clobber rcx, r11
    code:
        mov rax, 60
        syscall
    end
    unreachable
end
```

* **Dialect is module-scoped, never per block.** `Module.SetAsmDialect` sets the syntax for every `asm` block in the file; `vir.Verify` confirms it's legal for the module's declared architecture. `.vbyte` format version 3 carries `AsmDialect` as a single header field for the same reason.
* **Bindings are flat and typed.** `in`/`out`/`clobber` bind IR values to physical registers up front; `out` participates in the same type-fixation and definite-assignment passes as any other value producer.
* **Structural, not semantic, verification.** The verifier checks register-table membership, width agreement, binding well-formedness, and asm-local label scoping. Full per-dialect mnemonic/operand-shape legality (§9.38) and barrier semantics are explicitly deferred, marked `TODO` at their call sites rather than silently skipped.
* **Two directions, never confused.** `format/vbyte/{binary,text}` parse and print `vir.AsmBlock` as unlowered data. `format/asm/<arch>/text` is a completely unrelated, encode-only debug listing for an already-*lowered* `<arch>.Program` — neither package imports the other.
* **Lowering coverage varies by arch:** `x86`/`x86_64` support both Intel and AT&T; `aarch64` supports `native` only; `arm` does not lower inline asm at all yet.

---

## The Linker (`linker/`)

Stage 6 is three independent, format-specific sub-packages — there is no shared `package linker` at the top; `os` selects the right one at the call site:

| `os` | Import |
|---|---|
| `linux`, `freebsd`, `netbsd`, `openbsd`, `android`, `none` | `github.com/vertex-language/vvm/linker/elf` |
| `macos`, `ios`, `ios-simulator`, `maccatalyst`, `tvos`, `watchos`, `watchos-simulator`, `bridgeos`, `driverkit`, `visionos`, `visionos-simulator` | `github.com/vertex-language/vvm/linker/macho` |
| `windows`, `uefi` | `github.com/vertex-language/vvm/linker/pe` |

Each sub-package ships its own `Target`/`ParseTarget` (spelled the way that format's own native tooling spells it — VIR's triple grammar for ELF, a Clang/`vtool` triple for Mach-O, `link.exe`/`clang-cl` naming for PE), its own `Linker`/`Builder`, its own `Patcher`/`PLTPatcher` registry, and its own set of arch subpackages registered via blank-import. Adding an arch to one format never touches another, and the three formats never import each other. The top-level `vvm` package's own `Target` type is deliberately separate from all three — it's the router's input shape, translated into whichever format-specific `Target` the selected backend actually wants (see `dispatch.go`), not a fourth shared type.

Full docs live in each sub-package's own README:

- [`linker/elf/README.md`](linker/elf/README.md) — ELF64, VIR target grammar, arch registry, GC, dynamic-linking helpers
- [`linker/macho/README.md`](linker/macho/README.md) — Mach-O, Apple triple grammar, universal binaries, `linker/macho/codesign`
- [`linker/pe/README.md`](linker/pe/README.md) — PE32+, COFF object/archive parsing, import thunks, base relocations
- [`linker/README.md`](linker/README.md) — the one-page router between the three above

---

## Extended Design Principles

* **No shared types across format boundaries.** `objectfile/elf.Section` and `objectfile/coff.Section` look similar but are structurally distinct — each container package sizes its own `Section`/`Symbol`/`Reloc` to exactly the byte layout its format demands, deliberately avoiding a flattening shared type. The same principle extends into `linker/`: `linker/elf`, `linker/macho`, and `linker/pe` each define their own `Target`, `Layout`, and `Patcher` rather than sharing a facade.
* **Fail loudly, never guess.** Lowering adapters, the verifier, and object adapters immediately reject unsupported elements with explicit error names — no silent miscompilation, no approximate fallback. `objectwriter`'s adapters follow the same culture as `vir.Verify`: an unmapped `RelocKind` is a compile-time-discoverable limitation, not a corrupted `.o` file waiting to happen. `linker.Supported()` fails fast rather than silently falling back to an unregistered codegen path. The top-level `vvm` package follows the same rule at one more remove: an `(arch, format)` cell it doesn't route to yet is a named error from `vvm.Build`, not a silent fallback to a different target.
* **ISA fact vs. lowering decision.** A fact belongs in `isa/<arch>` if it would still be true even if the matching `lower/<arch>` were deleted and rewritten with a completely different register-allocation strategy; a decision this compiler makes about *how* to use those facts belongs in `lower/<arch>` instead.
* **Strict dependency boundaries.** `lower/<arch>` imports only `ir/vir`. `object/<arch>` imports neither `objectfile` nor `objectwriter`. `objectwriter/<arch>` and the `linker/*` sub-packages straddle the `object`/`objectfile` boundary; the top-level `vvm` package is the one place permitted to import across *all* of these boundaries at once, since routing by `Target` is its entire job. Everywhere else stays on one side.
* **Degrade, don't fail, in debug output only.** `format/asm/<arch>/text` decoders are deliberately lenient about unrecognized bytes — an unrecognized instruction word degrades to a raw `.word`/`db` line rather than failing the whole listing. This leniency is unique to the debug-listing path; every other stage in the pipeline fails loudly instead.

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
| inline `asm` lowering | ✅ (Intel/AT&T) | ✅ (Intel/AT&T) | — (not lowered) | ✅ (`native` only) |
| `vvm` package / CLI routing | ✅ (elf, flat only) | ✅ (elf, coff, macho) | — (blocked, same as objectwriter) | ✅ (elf, coff, macho) |

*All specific pipeline gaps (sub-32-bit atomic RMW, vector lowering, `f16` global initializers, tail calls whose args don't fit the callee's incoming argument area, 32-bit codegen for the linker backends, PE export directories, `arm64e` PAC signing, and the inline-asm gaps above) are managed as explicit `TODO` markers at their precise call sites, or called out under each sub-package's own "Known limitations"/"Known gaps" section. The `vvm` package's own routing gaps mirror `objectwriter`'s coverage matrix directly — it cannot reach further than the stages beneath it.*

---

## Further Reading

* [`docs/ir.md`](docs/ir.md) — the full Vertex IR language specification: grammar, memory model, target/ABI tables, inline assembly, and verifier obligations (§1–§11)
* [`ir/vir/README.md`](ir/vir/README.md) — the IR data model, construction API, and `Verify`'s coverage/known gaps
* [`format/README.md`](format/README.md) — `.vbyte`/`.vir` round-trip codecs and the `asm/<arch>/text` debug listings
* [`lower/README.md`](lower/README.md), [`isa/README.md`](isa/README.md) — backend design shared across the four architectures
* [`object/README.md`](object/README.md), [`objectwriter/README.md`](objectwriter/README.md), [`objectfile/README.md`](objectfile/README.md) — the object-file pipeline, one layer at a time
* [`linker/README.md`](linker/README.md) — router into the three format-specific linker sub-packages
* `cmd/vvm` — the CLI; `vvm help` for usage

## License

MIT — see [LICENSE](LICENSE)