# VVM — Vertex Virtual Machine

`vvm` is a compilation framework for **CPU and GPU compute**, built around two strictly-scoped intermediate representations: **Vertex IR** for host code and **Vertex GPU IR** for device kernels.

CPU targets are ahead-of-time (AOT) — compilation ends at a finished native binary. GPU targets end at vendor-toolchain source instead; for PTX, that source is then JIT-compiled by NVIDIA's own driver at load time, outside `vvm`'s pipeline.

Host modules lower to native CPU instructions and link into finished binaries — no external assembler, no external linker. Device modules lower to the vendor toolchains' own input languages: PTX, AMD `amdtx`, and Metal Shading Language.

The two are separate pipelines with separate IRs, separate codecs, and separate entry points. Neither is a subset or a special case of the other.

---

## The Two IRs

| | Host | Device |
| --- | --- | --- |
| **IR package** | `ir/vir` | `ir/gvir` |
| **Text format** | `.vir` | `.gvir` |
| **Binary format** | `.vbyte` | — (spec defines no byte encoding) |
| **Unit of code** | functions, globals, imports, links | kernels, funcs, structs, consts |
| **Compiles to** | one native binary | one artifact per declared target |
| **Ends at** | a runnable executable or shared library | PTX / amdtx / MSL source for a vendor toolchain |

`gvir` is the **device-only sibling** of `vir`, not a restricted version of it. It has no host surface at all — no entry point, no globals, no imports, no links, no TLS, no namespaces. It has things `vir` doesn't: mandatory address spaces on every pointer, an annotated CFG carrying each divergent construct's reconvergence point, value-only types (`i1`, `submask`) that are representable but never storable, and per-artifact capability gating.

Nothing is shared between the two — not the lexer, not the grammar, not a common "module" abstraction. A change to one is not a change to the other.

**Host-side format details:**

* **`.vbyte` (binary):** portable, typed, pre-parsed. Decoding costs no lexing or parsing, giving AOT pipelines a zero-startup-cost baseline.
* **`.vir` (text):** human-readable counterpart, identical grammar, meant for hand-authoring and diffing.
* **Lossless round-tripping:** `text.Decode → binary.Encode → binary.Decode → text.Encode` always lands back on the same canonical `.vir` text.

**Device-side:** `.gvir` is text-only today — the Vertex GPU IR spec defines no byte encoding, so there's no `gvbyte/binary` sibling to `gvbyte/text`. `Decode → Encode` still round-trips to the same canonical text, though it's a weaker check than the host side's, since there's no second serialization to disagree with.

All entry points sniff the input's own bytes to decide which IR they're holding — `VBYT` magic for `.vbyte`, a leading `module` keyword for `.vir`, a leading `version` keyword for `.gvir`. File extensions are a convention for humans, never a routing decision.

---

## Two Pipelines

The shapes are genuinely different, and that difference drives most of the API design below: **host builds converge, device builds fan out.**

### Host: many modules in, one binary out

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
     │  cpu/lower/<arch>.Lower
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

### Device: one module in, N artifacts out

```
.gvir
     │  format/gvbyte/text.Decode
     ▼
*gvir.Module  (unverified — see "Not Yet Implemented")
     │
     │  one pass per declared artifact, in gvir's canonical order:
     │  ptx, then amdgcn archs in declaration order, then msl
     ▼
gpu/lower/{ptx,amdtx,msl}.Lower
     │  capability gating (§4.3) drops unsupportable kernels per artifact
     ▼
gpu/ir/{ptx,amdtx,msl}.Module  +  Result.Excluded
     │  gpu/ir/<backend>/encoding/text.Print
     ▼
.ptx / .amdtx / .metal source  ──►  ptxas / AMD assembler / metal frontend
```

A device build ends at vendor-toolchain **source**, not at bytes anything can execute. There's no device linker because there's nothing to link: `.gvir` has no imports and no cross-module references, so a device build takes exactly one module.

No package imports "up" either chain. `ir/vir` doesn't know `ir/verify` exists; `gpu/lower` doesn't know `vvm` exists. The top-level `vvm` package is the one place allowed to import all of them at once.

---

## Repository Layout

| Path | Description |
| --- | --- |
| **`ir/vir/`** | The host data model and append-only construction API. |
| **`ir/gvir/`** | The device data model — kernels, address spaces, annotated CFG, capability tables, kernarg layout. |
| **`ir/verify/`** | Semantic validation. Host coverage is complete; device coverage is not yet implemented. |
| **`importer/`** | Host-only: resolves cross-module dependencies, checks qualified references, rewrites them away before lowering. |
| **`format/`** | Codecs — `vbyte/{binary,text}` for host, `gvbyte/text` for device, plus debug-only disassembly printers for already-lowered machine code. |
| **`isa/`** | Static, data-only CPU instruction set descriptions — registers, condition codes, opcode tables — and the generic assemblers built on them. |
| **`cpu/lower/`** | Host instruction selection: a verified `vir` module in, a machine-code `Program` out, one package per architecture. |
| **`gpu/lower/`** | Device lowering: a `gvir` module in, a target IR module out, one package per backend. |
| **`gpu/ir/`** | Structured in-memory IRs for PTX, `amdtx`, and MSL, each with its own canonical text printer. |
| **`object*/`** | Host-only: lowered programs → generic sections → real format-specific object files. |
| **`linker/`** | Host-only: complete, independent linkers for ELF, Mach-O, and PE, each with its own per-arch codegen registry. |
| **`crt/`** | Synthesizes raw C-runtime process-entry stubs for native execution. |
| **`spec/`** | The complete Vertex IR and Vertex GPU IR specifications — grammar, type system, memory model, ABI. |
| **`cmd/vvm`** | The CLI. |

---

## Supported Targets

### Host

Triples follow `arch-os-abi[tiers]`, with no vendor field. Current end-to-end support:

* **`x86_64`:** fully linked, end-to-end, for ELF, Mach-O, PE, and Flat.
* **`aarch64`:** fully linked, end-to-end, for ELF, Mach-O, PE, and Flat.
* **`x86`:** Flat is fully linked. ELF *object* bytes can be produced, but no ELF linker backend is registered for it yet — you'd need to feed the object bytes to an external linker.
* **`arm` / `armeb`:** Flat only, for now — there's no ELF path (object or linked) registered for 32-bit ARM yet.

### Device

Backends are selected as `backend[:arch]`. Every device target a build can produce is fixed by the module's own mandatory target section — the CLI can narrow that set, never extend it.

| Backend | Target | Output | Artifacts per module |
| --- | --- | --- | --- |
| `ptx` | NVIDIA PTX | `<module>.ptx` | One — PTX is JIT-forward and covers everything at or above the declared floor |
| `amdgcn` | AMD GCN/RDNA | `<module>.<arch>.amdtx` | **One per declared architecture** — no JIT fallback |
| `msl` | Apple Metal | `<module>.metal` | One — the declared arch is a language floor, not a binary target |

Arch aliases (`sm90`, `gfx11`, `metal3.2`) are recorded specifically so they can be **rejected with a useful message**, never silently rewritten to their canonical spellings.

---

## Installation

```sh
GOPROXY=direct go install github.com/vertex-language/vvm/cmd/vvm@latest
```

This builds the `vvm` binary from the latest tagged release and places it in your `$GOBIN` (or `$GOPATH/bin`). Make sure that directory is on your `PATH`.

---

## Quick Start — Host

### `vvm run` — compile and execute immediately

**`add.vir`**:

```vir
module main

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

### Calling into libc

**`main.vir`**:

```vir
module main
target x86_64 linux gnu

global fmt array[i8, 14] = "%d + %d = %d\n\x00"

link shared "c"
extern "c":
    fn printf(fmt ptr, ...) i32
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

### Multiple files, linked via the import graph

```sh
$ vvm build math.vir main.vir -o myapp
```

See [Multi-Module Builds](#multi-module-builds--the-import-graph) below.

---

## Quick Start — Device

**`reduce.gvir`** declares its own targets, and that declaration is what a build fans out over:

```sh
# Every artifact the module declares
$ vvm build reduce.gvir
vvm: wrote reduce.ptx           (ptx:sm_80)
vvm: wrote reduce.gfx90a.amdtx  (amdgcn:gfx90a)
vvm: wrote reduce.gfx1100.amdtx (amdgcn:gfx1100)
vvm: wrote reduce.metal         (msl:metal31)

# Into a directory
$ vvm build reduce.gvir -o build/kernels/

# One backend — now -o naming a single file is unambiguous again
$ vvm build reduce.gvir --target ptx -o build/reduce.ptx
vvm: wrote build/reduce.ptx (ptx:sm_80)

# One arch within the backend that fans out
$ vvm build reduce.gvir --target amdgcn:gfx90a --debug -o build/

# Several, and only certain kernels
$ vvm build reduce.gvir --target ptx,msl --kernel sum --kernel scan -o build/
```

### `--target` selects, it never specifies

A `.gvir` target section is mandatory and multi-valued, so there's no "no declaration, pass one on the command line" case the way there is for a pure-compute `.vir` module. Asking for something the module didn't declare is an error, not a silent substitution:

```sh
$ vvm build reduce.gvir --target ptx:sm_90
vvm: ptx:sm_90 is not declared by this module
     (declared: ptx:sm_80, amdgcn:gfx90a, amdgcn:gfx1100, msl:metal31)

$ vvm build reduce.gvir --target amdgcn:gfx11
vvm: amdgcn:gfx11 is an alias spelling — write "gfx1100" (§3)
```

### `-o` is a directory unless the build produces exactly one artifact

```sh
$ vvm build reduce.gvir -o reduce.ptx
vvm: -o names a single file, but this build produces 4 artifacts
     (ptx:sm_80, amdgcn:gfx90a, amdgcn:gfx1100, msl:metal31)
     — pass a directory, or narrow with --target
```

A trailing separator (`-o build/`) means "directory" even if it doesn't exist yet; a bare `build` with no existing directory is a filename.

### Capability gating is reported, never silent

Gating (§4.3) drops a kernel from *one artifact* when the target can't support a feature it uses, and keeps going — whole-module judgments belong to a verifier, not to a backend. But an artifact quietly missing an entry point is exactly the kind of thing that should never be discovered at launch time, so every exclusion is reported:

```sh
$ vvm build reduce.gvir
vvm: wrote reduce.ptx (ptx:sm_80)
vvm: amdgcn:gfx1100: kernel "dsum" excluded — f64 atomics unavailable
vvm: wrote reduce.gfx1100.amdtx (amdgcn:gfx1100, 1 kernel(s) excluded)
```

Pass `--strict-gating` to make any exclusion a build failure, which is usually what you want in CI. It reports every exclusion at once rather than one per re-run.

### `vvm targets` — what does this file say it's for?

Works on both IRs, prints one target per line, builds nothing:

```sh
$ vvm targets reduce.gvir
ptx:sm_80
amdgcn:gfx90a
amdgcn:gfx1100
msl:metal31

$ vvm targets main.vir
x86_64-linux-gnu
```

Useful for build scripts that need the artifact set before deciding output paths.

### What device builds deliberately won't do

```sh
$ vvm run reduce.gvir
vvm: run is host-only — a .gvir device module has no entry point and no process
     image. Emit artifacts with `vvm build`, then assemble and launch them
     through the vendor toolchain (ptxas, the metal frontend, or your AMD
     assembler).

$ vvm build main.vir reduce.gvir -o app
vvm: cannot mix host and device modules in one build: main.vir is host
     (.vir/.vbyte), reduce.gvir is device (.gvir) — there is no host-side
     launch or link path for .gvir yet; build them separately.

$ vvm build a.gvir b.gvir
vvm: a device build takes exactly one .gvir module — .gvir has no imports and
     no cross-module graph to resolve
```

Host-only flags (`--root`, `--min-os-version`) against a `.gvir` input, and device-only flags (`--kernel`, `--debug`, `--strict-gating`) against a `.vir` input, are both errors rather than silently-ignored instructions.

---

## Library Usage

`vvm` imports as an ordinary Go library. Both IRs have append-only construction APIs, so neither pipeline requires text source.

### Host — build and run in-memory

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

The `printf`-calling version, built directly via the API:

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

`NewModule`/`FunctionBuilder` only ever append — nothing there validates a name collision, a type, or control flow. That's `ir/verify`'s job, and `vvm.BuildModule` runs it for you before lowering.

### Device — build artifacts in-memory

```go
package main

import (
	"fmt"
	"os"

	"github.com/vertex-language/vvm"
	"github.com/vertex-language/vvm/ir/gvir"
)

func main() {
	m := gvir.NewModule("reduce")
	m.SetTarget(gvir.PTX("sm_80"), gvir.AMDGCN("gfx90a", "gfx1100"), gvir.MSL("metal31"))
	m.SetFloatProfile(true, false) // contract on, approx off

	kb := m.DeclareKernel("sum",
		gvir.Param{Name: "out", Type: gvir.PtrGlobal},
		gvir.Param{Name: "in", Type: gvir.PtrGlobal},
		gvir.Param{Name: "n", Type: gvir.I32},
	).GroupSize(256, 1, 1)

	i := kb.Builtin("i", gvir.OpThreadInGrid, gvir.DimNone)
	p := kb.IndexPointer("p", gvir.Ident("in"), i)
	v := kb.Load("v", gvir.F32, p)
	s := kb.SubReduce("s", gvir.OpSubAdd, gvir.F32, v)
	kb.Barrier(gvir.ExecGroup)
	kb.AtomicRMW("old", gvir.OpAtomicAdd, gvir.F32, gvir.Ident("out"), s, gvir.ScopeGrid)
	kb.Return()

	arts, err := vvm.BuildDeviceModule(m, vvm.DeviceOptions{})
	if err != nil {
		panic(err)
	}
	for _, a := range arts {
		for _, x := range a.Excluded {
			fmt.Printf("%s:%s: %s\n", a.Backend, a.Arch, x)
		}
		os.WriteFile(a.Filename, a.Source, 0o644)
	}
}
```

`DeviceOptions` carries the same knobs the CLI exposes — `Select` (a `[]DeviceSelector`), `Kernels`, `Debug`, `StrictGating` — and every zero value means "full build, nothing special."

The kernarg buffer is derived once, in `gvir`, and is byte-identical across all three backends:

```go
layout, _ := m.KernargLayout(kb.Kernel)
```

That single derivation is shared by the launcher generator, `ir/verify`, and the differential suite — it's the one place the host and device sides have to agree on bytes.

---

## Multi-Module Builds & the Import Graph

*Host only.* Device modules have no imports at all.

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

Under the hood this runs a different sequence than the single-module path: `importer.NewSet` indexes every module, `ResolveImports` maps each `import` to a real module, `ir/verify.Verify` runs on each module individually, `CheckReferences` validates every qualified reference against the real target's real declaration, and `Rewrite` erases those qualified references entirely — `mathlib.square` becomes an ordinary mangled extern symbol before `cpu/lower/<arch>` ever sees it. `--root` tells `vvm` which module's `entry` function is the program's actual entry point.

---

## Design Principles

* **Two IRs, two pipelines, one boundary.** `vir` and `gvir` share no code and never meet in a build. The split is enforced at the front door — every entry point sniffs which IR it was handed and refuses the wrong one with a specific message, rather than letting a mismatched module fail somewhere deep in a lowering backend.
* **Strict package boundaries.** Each package only does its own job — lowering assumes verification already ran; `ir/vir` has no idea `ir/verify` exists; `gpu/ir` never touches string output until `text.Print` is called.
* **No shared types across boundaries.** ELF, Mach-O, and COFF relocations get genuinely different types, since what "addend" means differs by format. The three GPU backends' `Result` types stay distinct for the same reason — `amdtx`'s is per-arch and the others aren't. Never one struct with a comment explaining which convention currently applies.
* **Fail loudly.** Unmapped relocations, unresolved link dependencies, unregistered codegen backends, undeclared device targets, and alias arch spellings all return explicit, specific errors — never silently wrong, missing, or substituted output.
* **Additive registration.** New CPU architectures register themselves via `init()`, with zero edits to any shared file or switch statement.
* **Gating excludes, it doesn't degrade.** When a device target can't support a feature, the affected kernel is dropped from that one artifact and recorded, never silently rewritten into something with different semantics.
* **One canonical printer per format.** No secondary "quick and dirty" stringification path anywhere that could drift out of sync with the grammar model.

---

## Package Reference: A Closer Look

Packages not covered above in detail:

| Path | What it does |
| --- | --- |
| **`object/<arch>`** | `Program` → `[]Section` with `Symbol`/`Reloc`. Identical shape across all four arches; only `RelocKind` differs per package. |
| **`objectfile/<format>`** | The byte-level object-file encoder for ELF64, COFF, Mach-O `MH_OBJECT`, or raw flat. Each format owns its own `Section`/`Symbol`/`Reloc` types. |
| **`objectwriter/<arch>`** | The thin bridge between the two rows above: map section kind, map reloc kind, forward `Symbol`/`Addend` unchanged. No relocation arithmetic happens here — that was already decided upstream. |
| **`crt`** | Builds the raw process-entry sequence — staging `argc`/`argv`/`envp`, calling libc's `exit` or issuing a bare syscall — as machine code, since §4's instruction vocabulary deliberately has nothing to express "the register value before any parameter binding happened." |
| **`gpu/lower/<backend>`** | `.gvir` → target IR. Each package factors the same way: `{pkg}.go` for options, `callable.go` for kernel/function lowering, `cfg.go` for control-flow reconstruction, `isel*.go` for instruction selection, `gating.go` for §4.3 capability gating, `types.go` for type/address-space mapping. |
| **`gpu/ir/<backend>`** | Grammar-driven structural models of PTX, `amdtx`, and MSL. No type checking, no semantic validation — that's the vendor toolchain's job. Each exposes a typed escape hatch (`Emit`, `Raw`) that still participates fully in walking and printing. |
| **`testutils` / `foundation`** | Shared test helpers and low-level utilities used across the tree. |

`vvm`'s own top-level files:

| File | Role |
| --- | --- |
| `vvm.go` | Sniffs which IR the input holds; decodes for either pipeline; reads a host `target` declaration without running `Verify`. |
| `build.go` | The host single-module (no `import`) pipeline. |
| `graph.go` | The host multi-module (`import`-graph) pipeline. |
| `dispatch.go` | Routes a host `Target` to the right `lower` → `object` → `objectwriter` → `linker` combination. |
| `target.go` | Host `Target`, `ParseTarget`, and container-format derivation. |
| `entrypoint.go` | Decides the process entry symbol; synthesizes a `crt` stub for recognized `main()` shapes. |
| `linkdeps.go` | Resolves each module's `link` declarations against the chosen linker backend. |
| `codesign.go` | Re-signs linked Mach-O executables through `linker/macho/codesign`. |
| `registry.go` | Blank-imports the codegen backends this package ships wired up by default. |
| `run.go` | `Run`/`RunModule` — build for the host, execute in a temp file, stream the result back. |
| `gpu.go` | `BuildDevice`/`BuildDeviceModule`/`DeviceTargets` — the device pipeline and its options. |
| `gpudispatch.go` | Routes a `DeviceSelector` to the right `gpu/lower` → `gpu/ir/…/text` pair. |
| `gputarget.go` | `DeviceSelector`, selector parsing, and declared-artifact selection against a module's target section. |
| `artifact.go` | `Artifact` and `Exclusion` — what a device build hands back. |

---

## Verification, Memory Model & UB

*Host.* Vertex IR's verifier and memory model are deliberately narrow and explicit, not "best effort":

* **Strict semantics, minimal UB.** Integer overflow always wraps (never UB); shift counts always mask; floats always follow strict IEEE-754. There are exactly **10** ways to trigger undefined behavior — out-of-bounds access, misalignment, data races, and a handful of others — and everything outside that list is either defined or a deterministic trap.
* **Traps vs. UB are distinct.** Division by zero, `INT_MIN / -1`, and out-of-range float-to-int casts *trap* — they deterministically halt, and are never catchable, resumable, or removable by codegen. That's a different failure mode from the 10 UB cases, which codegen is free to assume never happen.
* **`valist` has linear-use rules baked into verification.** A varargs cursor must be `va_start`-initialized on every path before any `va_arg`/`va_end` reads it, and re-starting one without an intervening `va_end` is a verification error — compile-time, not runtime.
* **No pointer provenance games.** Pointers are addresses, full stop — alias analysis relies only on object bounds and reachability, which costs some optimization headroom in exchange for a memory model that fits in one paragraph.

*Device.* `gvir` pins the same kind of semantics rather than inheriting whatever the hardware does — division by zero forced to `0`, shift counts masked rather than left as UB, NaN-quieting `min`/`max` avoided — and each backend explicitly patches the spots where its target diverges. Values merge across blocks by same-name assignment (§7.3 Join Convention); there are no phi nodes on either side of the tree.

**Device verification is not yet implemented.** See below.

---

## Error Handling Philosophy, Concretely

"Fail loudly" isn't just a slogan — it shows up as specific, named errors at real seams:

* An arch/format combination with no coverage: `vvm: x86 has no objectwriter for this format (coverage: elf, flat only)`.
* A `link` dependency this package can't yet resolve for the chosen format: `linkdeps.go` refuses to silently drop it, and tells you to link it manually instead.
* A device target that isn't declared, or is spelled as an alias: rejected against the module's own declared list, with the canonical spelling named when there is one.
* Wrong IR for the pipeline: `run` on a `.gvir` file, host and device modules in one build, or a device-only flag on a host build all error out with the reason, not a generic parse failure.
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

The GPU side has no equivalent registry, deliberately: backend *is* format there, so there's no (arch × format) matrix to fill. Adding a device backend means a new `gpu/ir/<backend>` + `gpu/lower/<backend>` pair, a `gvir` backend constant, and one arm in `gpudispatch.go`.

---

## Not Yet Implemented

Tracked honestly rather than glossed over:

**Device**

* **`ir/verify` has no `gvir` coverage yet.** Name binding, block ordering, merge annotations, the §7.4 uniformity analysis, kernarg validation and §4.3 gating validation all belong there and none of them run today — `BuildDevice` hands a decoded-but-unverified module straight to `gpu/lower`. A malformed-but-parseable device module will fail inside a lowering backend, or not fail at all, instead of at a clean verification seam. This is the single largest gap in the tree.
* **No host↔device story.** No launcher generation, no runtime, no kernarg binding from host code, no build that consumes both IRs. The CLI refuses these combinations explicitly rather than half-supporting them.
* **64-bit operations:** `amdtx` and `ptx` both lack full 64-bit multiply/byte-swap support; `msl` lacks 64-bit `umulh`/`smulh`.
* **Bulk memory transfers:** `amdtx`'s `memcopy`/`memmove`/`memset` thread loops are unimplemented; `ptx` has `memcopy`/`memset` but not `memmove`.
* **No `.gvir` binary encoding.** The spec names none, so `gvbyte/text` is the whole codec tree. The directory is nested so a binary sibling could land beside it later without moving anything — not because one is planned.

**Host**

* Floating-point and vector codegen, on every backend.
* `i128` values, on every backend.
* Several atomics (return-previous RMWs, `cmpxchg` retry loops) on one or more arches.
* `riscv32/64`, `powerpc(64)`, `mips*`, `loongarch64`, `s390x` — valid `.vir` target triples per the spec, with no `cpu/lower/`, `object/`, or `linker` implementation yet.
* PE delay-load imports (`.didat`) and a real PE export directory.
* PE cross-compilation: `resolvePELinkDependencies` reads real DLL bytes off disk, so building for Windows from a non-Windows host fails with a "not found" error rather than producing a broken import table.
* `arm64e` pointer authentication and `arm64_32`'s ILP32 data model (both link end-to-end, but are documented non-conformances).

Each affected package's own README has the precise, current detail.

---

## License

MIT — see [LICENSE](./LICENSE).