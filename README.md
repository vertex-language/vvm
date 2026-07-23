# VVM — Vertex Virtual Machine

`vvm` is a high-performance compute engine and ahead-of-time (AOT) compilation framework built around a single, strictly verified intermediate representation: **Vertex IR**. It lowers Vertex IR directly to native CPU instructions, emitting either loader-ready object files or fully linked, finalized binaries.

---

## Core Architecture & Unified Bytecode

`vvm` is built around a target-independent IR that skips text-to-structure translation entirely when it can.

* **`.vbyte` (Binary):** A portable, typed, pre-parsed binary encoding — the frontend boundary. Decoding it costs no lexing or parsing, giving AOT pipelines a zero-startup-cost baseline.
* **`.vir` (Text):** A human-readable counterpart sharing the identical grammar, meant for hand-authoring, code review, and diffing.
* **Lossless round-tripping:** `.vbyte` and `.vir` are two serializations of the same in-memory module — `text.Decode → binary.Encode → binary.Decode → text.Encode` always lands back on the same canonical `.vir` text it started from.
* **Auto-detection:** CLI and library entry points sniff the input file's magic bytes and accept either format without configuration.

---

## Compilation Pipeline

Every build runs a sequence of independently importable packages, each owning exactly one job:

* **Decode:** Reads `.vir` or `.vbyte` into an unverified structural module.
* **Verify & Resolve:** Applies single-module semantic checks (`ir/verify`) and, for multi-file builds, resolves cross-module imports (`importer`).
* **Lower:** Translates verified Vertex IR into machine code for a specific target architecture.
* **Write Object:** Maps lowered instructions into format-agnostic sections, then bridges them into container-specific object bytes (ELF/COFF/Mach-O/flat).
* **Link:** Produces the final native binary using `vvm`'s own linkers — no shelling out to `ld`, `link.exe`, or similar.

---

## Repository Layout

| Path | Description |
| --- | --- |
| **`ir/`** | The `vir` data model and append-only construction API, plus the `verify` package for semantic validation. |
| **`importer/`** | Resolves cross-module dependencies, checks qualified references, and rewrites them away before lowering. |
| **`format/`** | Codecs for `.vbyte` and `.vir`, plus debug-only disassembly printers for already-lowered machine code. |
| **`isa/`** | Static, data-only instruction set descriptions — registers, condition codes, opcode/mnemonic tables — and the generic assemblers built on top of them. |
| **`lower/`** | Pure instruction selection: a verified module in, a machine-code `Program` out, one package per architecture. |
| **`object*/`** | Translates lowered programs into generic sections, then assembles and bridges them into real format-specific object files. |
| **`linker/`** | Complete, independent linkers for ELF, Mach-O, and PE, each with its own per-arch codegen registry. |
| **`crt/`** | Synthesizes raw C-runtime process-entry stubs for native execution. |
| **`spec/`** | The complete Vertex IR language specification — grammar, type system, memory model, ABI. |
| **`cmd/vvm`** | The CLI for compiling and executing Vertex IR. |

---

## Design Principles

* **Strict Package Boundaries:** Each package only does its own job — lowering assumes verification already ran; `ir/vir` has no idea `ir/verify` exists.
* **No Shared Types Across Boundaries:** ELF, Mach-O, and COFF relocations get genuinely different types, since what "addend" means differs by format — never one struct with a comment explaining which convention currently applies.
* **Fail Loudly:** Unmapped relocations, unresolved link dependencies, and unregistered codegen backends return explicit, specific errors — never silently wrong or missing bytes.
* **Additive Registration:** New architectures register themselves via `init()`, with zero edits to any shared file or switch statement.

---

## Supported Targets

Target triples in `vvm` follow an `arch-os-abi[tiers]` format with no vendor field. Current end-to-end support:

* **`x86_64`:** Fully linked, end-to-end, for ELF, Mach-O, PE, and Flat.
* **`aarch64`:** Fully linked, end-to-end, for ELF, Mach-O, PE, and Flat.
* **`x86`:** Flat is fully linked. ELF *object* bytes can be produced, but no ELF linker backend is registered for it yet — you'd need to feed the object bytes to an external linker.
* **`arm` / `armeb`:** Flat only, for now — there's no ELF path (object or linked) registered for 32-bit ARM yet.

---

## Installation

Install the `vvm` CLI directly with `go install`:

```sh
GOPROXY=direct go install github.com/vertex-language/vvm/cmd/vvm@latest
```

This builds the `vvm` binary from the latest tagged release and places it in your `$GOBIN` (or `$GOPATH/bin`). Make sure that directory is on your `PATH` so the `vvm` command is available in your shell.

---

## Quick Start

### `vvm run` — compile and execute immediately

**`add.vir`**:

```vir
module add

target x86_64 linux gnu

export fn main() i32 entry:
    a = add.i32 7, 35
    return a
end
```

```sh
$ vvm run add.vir
$ echo $?
42
```

`vvm run` decodes, verifies, lowers, and links for the host target in one pass — as close to `go run` as a native AOT compiler gets.

### A full `main.vir` — calling into libc

**`main.vir`**:

```vir
module main

target x86_64 linux gnu

global fmt array[i8, 14] = "%d + %d = %d\n\x00"

link shared "c"
extern "c":
    fn printf(ptr, ...) i32
end

export fn main() i32 entry:
    sum = add.i32 7, 35
    r = call printf, fmt, 7, 35, sum
    return 0
end
```

```sh
$ vvm build main.vir -o main
vvm: wrote main (x86_64-linux-gnu)
$ ./main
7 + 35 = 42
```

### `vvm build` — multiple files, linked via the import graph

```sh
$ vvm build math.vir main.vir -o myapp
```

See [Multi-Module Builds](#multi-module-builds--the-import-graph) below for a worked example of what `math.vir`/`main.vir` actually look like.

---

## Library Usage

`vvm` can be imported as a standard Go library to build, verify, and run modules directly in-memory via the `ir/vir` append-only construction API — no `.vir` text source required.

**Building and running `add.vir` natively in Go:**

```go
package main

import (
	"fmt"

	"github.com/vertex-language/vvm"
	"github.com/vertex-language/vvm/ir/vir"
)

func main() {
	m := vir.NewModule("add")
	m.SetTarget("x86_64", "linux", "gnu")

	fb := m.DeclareFunction("main", nil, vir.I32, true /* entry */)
	sum := fb.Add("sum", vir.I32, vir.IntLiteral(7), vir.IntLiteral(35))
	fb.Return(sum)

	res, err := vvm.RunModule(m)
	if err != nil {
		panic(err)
	}
	fmt.Println(res.ExitCode) // 42
}
```

**The `printf`-calling version built directly via the API:**

```go
m := vir.NewModule("main")
m.SetTarget("x86_64", "linux", "gnu")

fmtGlobal := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 14},
	vir.InitByteString{Data: []byte("%d + %d = %d\n\x00")})

fb := m.DeclareFunction("main", nil, vir.I32, true /* entry */)
sum := fb.Add("sum", vir.I32, vir.IntLiteral(7), vir.IntLiteral(35))
fb.Call("r", "printf", vir.Ident(fmtGlobal.Name), vir.IntLiteral(7), vir.IntLiteral(35), sum)
fb.Return(vir.IntLiteral(0))

bin, err := vvm.BuildModule(m, vvm.Target{Arch: "x86_64", OS: "linux", ABI: "gnu"})
if err != nil {
	panic(err)
}
os.WriteFile("main", bin, 0o755)
```

`NewModule`/`FunctionBuilder` only ever append to the structure — nothing here validates a name collision, a type, or control flow. That's `ir/verify`'s job, and `vvm.BuildModule` runs it for you before lowering.

---

## Multi-Module Builds & the Import Graph

Modules reference each other with `import`, and cross-module calls are qualified with `module.symbol`:

**`mathlib.vir`:**

```vir
module mathlib

export fn square(x i32) i32:
    r = mul.i32 x, x
    return r
end
```

**`main.vir`:**

```vir
module main
import "mathlib"

target x86_64 linux gnu

export fn main() i32 entry:
    r = call mathlib.square, 6
    return r
end
```

```sh
$ vvm build mathlib.vir main.vir --root main -o myapp
$ ./myapp; echo $?
36
```

Under the hood this runs a slightly different sequence than the single-module path: `importer.NewSet` indexes every module, `ResolveImports` maps each `import` to a real module, `ir/verify.Verify` runs on each module individually, `CheckReferences` validates every qualified reference against the real target's real declaration, and `Rewrite` erases those qualified references entirely — `mathlib.square` becomes an ordinary mangled extern symbol before `lower/<arch>` ever sees it. `--root` tells `vvm` which module's `entry` function to treat as the program's actual entry point.

---

## Under the Hood: The Full Pipeline

```
.vir / .vbyte
     │  format/vbyte/{text,binary}.Decode
     ▼
*vir.Module  (unverified)
     │
     ├─ single module ─── ir/verify.Verify
     └─ import graph ───── importer: ResolveImports → Verify (each) → CheckReferences → Rewrite
     ▼
verified *vir.Module(s)
     │  lower/<arch>.Lower
     ▼
*Program  (code, symbols, unresolved fixups)
     │  object/<arch>.FromProgram
     ▼
[]object.Section  (generic — same shape across every arch)
     │  objectwriter/<arch>.To{ELF,COFF,MachO,Flat}
     ▼
relocatable object bytes ── Flat? ──► done, no linker involved
     │  linker/{elf,macho,pe}.Linker.Link
     ▼
finished native binary
```

No package imports "up" the chain — `ir/vir` doesn't know `ir/verify` exists, and none of `lower`, `object`, `objectwriter`, or `linker` import each other. The top-level `vvm` package is the one place allowed to import all of them at once.

---

## Package Reference: A Closer Look

Packages not covered above in detail:

| Path | What it does |
| --- | --- |
| **`object/<arch>`** | `Program` → `[]Section` with `Symbol`/`Reloc`. Identical shape across all four arches; only `RelocKind` differs per package. |
| **`objectfile/<format>`** | The actual byte-level object-file encoder for ELF64, COFF, Mach-O `MH_OBJECT`, or raw flat. Each format owns its own `Section`/`Symbol`/`Reloc` types — no shared struct pretends they mean the same thing. |
| **`objectwriter/<arch>`** | The thin bridge between the two rows above: map section kind, map reloc kind, forward `Symbol`/`Addend` unchanged. No relocation arithmetic happens here — that was already decided upstream. |
| **`crt`** | Builds the raw process-entry sequence — staging `argc`/`argv`/`envp`, calling libc's `exit` or issuing a bare syscall — as machine code, since §4's instruction vocabulary has deliberately nothing to express "the register value before any parameter binding happened." |
| **`testutils` / `foundation`** | Shared test helpers and low-level utilities used across the tree. |

`vvm`'s own top-level files, for anyone reading this repo's root package directly:

| File | Role |
| --- | --- |
| `vvm.go` | Sniffs `.vbyte` vs `.vir`; reads an in-file `target` declaration without running `Verify`. |
| `build.go` | The single-module (no `import`) pipeline. |
| `graph.go` | The multi-module (`import`-graph) pipeline. |
| `dispatch.go` | Routes a `Target` to the right `lower` → `object` → `objectwriter` → `linker` combination. |
| `target.go` | `Target`, `ParseTarget`, and container-format derivation. |
| `entrypoint.go` | Decides the process entry symbol; synthesizes a `crt` stub for recognized `main()` shapes. |
| `linkdeps.go` | Resolves each module's `link` declarations against the chosen linker backend. |
| `registry.go` | Blank-imports the codegen backends this package ships wired up by default. |
| `run.go` | `Run`/`RunModule` — build for the host, execute in a temp file, stream the result back. |

---

## Verification, Memory Model & UB

Vertex IR's verifier and memory model are deliberately narrow and explicit, not "best effort":

* **Strict semantics, minimal UB.** Integer overflow always wraps (never UB); shift counts always mask; floats always follow strict IEEE-754. There are exactly **10** ways to trigger undefined behavior — out-of-bounds access, misalignment, data races, and a handful of others — and everything outside that list is either defined or a deterministic trap.
* **Traps vs. UB are distinct.** Division by zero, `INT_MIN / -1`, and out-of-range float-to-int casts *trap* — they deterministically halt, and are never catchable, resumable, or removable by codegen. That's a different failure mode from the 10 UB cases above, which codegen is free to assume never happen.
* **`valist` has linear-use rules baked into verification.** A varargs cursor must be `va_start`-initialized on every path before any `va_arg`/`va_end` reads it, and re-starting one without an intervening `va_end` is a verification error — not a runtime concern, a compile-time one.
* **No pointer provenance games.** Pointers are addresses, full stop — alias analysis relies only on object bounds and reachability, which costs some optimization headroom in exchange for a memory model that fits in one paragraph.

---

## Error Handling Philosophy, Concretely

"Fail loudly" isn't just a design slogan — it shows up as specific, named errors at real seams:

* An arch/format combination with no coverage: `vvm: x86 has no objectwriter for this format (coverage: elf, flat only)`.
* A `link` dependency this package can't yet resolve for the chosen format: `linkdeps.go` refuses to silently drop it, and tells you to link it manually instead.
* A lowering backend distinguishes two error shapes on purpose: a plain `fmt.Errorf` means the input violated something `Verify` should already have caught (a bug, upstream or in that package); a `todo(...)`-suffixed error means the module is valid — this backend just doesn't lower that construct yet.

---

## Extending `vvm`: Adding a New Target Architecture

Every `linker/<format>` package adds architectures the same way — a small subpackage that registers itself in `init()`, with no edits to any shared file:

```go
// linker/elf/riscv64/register.go
package riscv64

import "github.com/vertex-language/vvm/linker/elf"

func init() {
	elf.RegisterPatcher(elf.ArchRISCV64, func(t elf.Target) elf.Patcher {
		return elf.PatchFunc(patchRISCV64)
	})
	elf.RegisterPLTPatcher(elf.ArchRISCV64, func(t elf.Target) elf.PLTPatcher {
		return pltPatcher{}
	})
}
```

Blank-import it (in your own code, or added to `registry.go`) and `Linker.Supported()` flips to `true` for that arch — nothing else in `linker/elf` changes.

---

## Not Yet Implemented

Tracked honestly rather than glossed over:

* Floating-point and vector codegen, on every backend.
* `i128` values, on every backend.
* Several atomics (return-previous RMWs, `cmpxchg` retry loops) on one or more arches.
* `riscv32/64`, `powerpc(64)`, `mips*`, `loongarch64`, `s390x` — valid `.vir` target triples per the spec, with no `lower/`, `object/`, or `linker` implementation yet.
* PE delay-load imports (`.didat`) and a real PE export directory.
* `arm64e` pointer authentication and `arm64_32`'s ILP32 data model (both link end-to-end, but are documented non-conformances).

Each affected package's own README has the precise, current detail.

---

## License

MIT — see [LICENSE](./LICENSE).