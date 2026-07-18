# linker/elf

ELF64 linker sub-package for `github.com/vertex-language/vvm/linker`.

This package emits the ELF container format. ELF is used by several
`os` values from the VIR target grammar (§10.2) — `linux`, `freebsd`,
`netbsd`, `openbsd`, `android`, and `none` (bare-metal ELF, e.g. custom
loaders) — this package doesn't care which one you pick; it only cares
that the *format* is ELF. Mach-O and PE live in sibling packages
(`linker/macho`, `linker/pe`) and are selected by the OS, not by this
package.

Per-arch codegen lives in subpackages (below), mirroring the layout
already used by `lower/` and `object/` elsewhere in this repo.

## Import

```go
import "github.com/vertex-language/vvm/linker/elf"

// blank-import whichever arch backends you need registered:
import (
    _ "github.com/vertex-language/vvm/linker/elf/x86_64"
    _ "github.com/vertex-language/vvm/linker/elf/aarch64"
)
```

---

## Quick start

```go
t, err := elf.ParseTarget("x86_64-linux-gnu")
if err != nil {
    log.Fatal(err)
}

l := elf.NewLinker(t)
if !l.Supported() {
    log.Fatalf("%s: no codegen backend registered (blank-import its subpackage)", t)
}
l.SetEntryPoint("_start")

l.AddObject("main.o", mainBytes)
l.AddArchive("libc.a", libcBytes)
l.AddDynamicLibrary("libc.so.6", libcSOBytes)

out, err := l.Link()
if err != nil {
    log.Fatal(err)
}
os.WriteFile("a.out", out, 0755)
```

---

## Folder layout

```
linker/elf/
├── README.md
├── target.go        // Target, ParseTarget, OS/ABI/Tier, Valid()
├── registry.go       // Patcher/PLTPatcher factory registries, Supported()
├── linker.go         // Linker struct, NewLinker, Link() pipeline
├── builder.go        // Builder, Emit
├── layout.go         // Layout, MergeSections, AssignLayout, ResolveSymbolAddresses
├── gc.go             // dead-section elimination
├── dynamic.go        // PLT/GOT scaffolding, hash/version section builders
├── notes.go          // .note.* builders
├── object.go         // ParseObject
├── archive.go        // ParseArchive
├── shared.go         // ParseSharedLib
├── patch.go           // Patcher interface, PatchFunc adapter, PatchAll
├── reader.go           // bounds-checked reader, struct offsets
├── constants.go        // ELF64 constants
├── symtab.go            // SymbolTable, resolution rules
├── types.go              // Arch consts, Object/Section/Symbol/Reloc types
│
├── x86_64/    // patch.go, plt.go, register.go — implemented
├── aarch64/   // patch.go, plt.go, register.go — implemented (LE only, see TODO)
│
└── (not yet implemented — see "Upcoming arch support" below)
    arm/, x86/, riscv32/, riscv64/, powerpc/, powerpc64/,
    mips/, mips64/, loongarch64/, s390x/
```

**Endianness is folded into `Target`, not the directory tree.** `armeb`
and `aarch64_be` don't get their own subpackages — the relocation math
is identical to `arm`/`aarch64`, only the byte order of the final write
differs. `ParseTarget` resolves this at parse time:

```go
t, _ := elf.ParseTarget("aarch64_be-linux-gnu")
// t.Arch == elf.ArchARM64, t.BigEndian == true
```

Each arch subpackage's registered `PatcherFactory`/`PLTPatcherFactory`
receives the full `Target` (not just the arch), so it can read
`t.BigEndian` and pick the right `binary.ByteOrder` — see `aarch64/patch.go`.

---

## Target

`Target` is the same `(arch, os, abi)` triple as VIR's own `target`
declaration (§10, §10.6), plus an optional feature tier (§10.4) — a
`target` line in `.vir` source and a `Target` value in Go are always
the same string in both directions.

```go
type Target struct {
    Arch      Arch // e_machine-derived; ArchX86_64, ArchARM64, …
    OS        OS
    ABI       ABI
    Tier      Tier      // optional, zero value = no tier restriction
    BigEndian bool      // derived from the parsed arch string (armeb, aarch64_be)
}

func ParseTarget(s string) (Target, error)   // "x86_64-linux-gnu[avx2]"
func (t Target) String() string              // round-trips ParseTarget
func (t Target) WithTier(tier Tier) Target
func (t Target) Valid() error                // arch/os/abi cross-check, no registry lookup
```

`ParseTarget` only accepts canonical spellings (§10.5) — `armbe`,
`arm64`, `amd64`, `x64`, `i686` etc. are rejected exactly as the `.vir`
verifier rejects them in a `target-decl`. There's no alias table in this
package; alias resolution belongs at the CLI/build layer, one step
before it reaches `ParseTarget`.

`Valid()` checks the triple is a real combination but does **not** check
whether this build has codegen for it — that's `Linker.Supported()`.

### What's valid for ELF (`os` ∈ ELF-format OSes)

| `arch` | `linux` | `freebsd`/`netbsd`/`openbsd` | `android` | `none` |
|---|---|---|---|---|
| `x86_64` | `gnu`, `musl` | `gnu` | `gnu` | — |
| `x86` | `gnu`, `musl` | `gnu` | `gnu` | — |
| `arm` | `eabi`, `eabihf` | `eabi`, `eabihf` | `eabi`, `eabihf` | `eabi`, `eabihf` |
| `armeb` | `eabi`, `eabihf` | — | — | `eabi`, `eabihf` |
| `aarch64` | `gnu`, `musl` | `gnu` | `gnu` | — |
| `aarch64_be` | `gnu`, `musl` | — | — | — |
| `riscv32`, `riscv64` | `gnu`, `musl` | `gnu` (riscv64 only) | — | — |
| `powerpc`, `powerpc64`, `powerpc64le` | `gnu`, `musl` | — | — | — |
| `mips32`, `mips32el`, `mips64`, `mips64el` | `gnu`, `musl` | — | — | — |
| `loongarch64` | `gnu`, `musl` | — | — | — |
| `s390x` | `gnu`, `musl` | — | — | — |

`msvc`, `macho`, `aapcs64` are never valid here — `Valid()` rejects them
against any ELF-format `os` regardless of `arch`, since those three ABIs
belong to non-ELF formats per §10.3. `os=none` accepts `tls` only when
the selected `Tier` supplies a TLS convention (§1.2 rule 7); `Valid()`
surfaces that as an error rather than deferring it to codegen.

### What's actually implemented (`Supported()`)

`Valid()` and `Supported()` are deliberately different questions —
a target can be a real, spec-legal triple with no registered backend:

```go
t, _ := elf.ParseTarget("riscv64-linux-gnu")
l := elf.NewLinker(t)

if !l.Supported() {
    log.Fatalf("%s: no codegen backend registered", t)
}
```

Right now `Supported()` is `true` only for `x86_64` and `aarch64`
(little-endian). See **Upcoming arch support** below.

### Adding a new arch

Same shape every time:

```go
// linker/elf/riscv64/patch.go
package riscv64

func patchRISCV64(data []byte, off int, rtype uint32, P, S uint64, A int64) error { ... }

// linker/elf/riscv64/plt.go
package riscv64

type pltPatcher struct{}
func (pltPatcher) HeaderSize() int { return ... }
func (pltPatcher) EntrySize() int  { return ... }
func (pltPatcher) PatchPLT(plt, gotPLT, relaPLT []byte, pltBase, gotBase uint64, syms []elf.PLTEntry) { ... }

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
    elf.RegisterDefaultInterp(elf.ArchRISCV64, func(t elf.Target) string {
        if t.ABI == elf.ABIMusl {
            return "/lib/ld-musl-riscv64.so.1"
        }
        return "/lib/ld-linux-riscv64-lp64d.so.1"
    })
    elf.RegisterSearchDirs(elf.ArchRISCV64, func(t elf.Target) []string {
        return []string{"/lib/riscv64-linux-gnu", "/usr/lib/riscv64-linux-gnu", "/usr/lib", "/lib"}
    })
}
```

No edits to `linker.go`, `builder.go`, `registry.go`, or any other
arch's files — registration is purely additive via `init()`.

---

## Linker

```go
l := elf.NewLinker(t)
l.SetOutputType(elf.OutputExec)    // OutputExec | OutputPIE | OutputShared
l.SetEntryPoint("_start")
l.SetInterp("/lib64/ld-linux-x86-64.so.2") // override the auto-detected default
l.SetSoname("libfoo.so.1")         // shared library only
l.SetRpath("/usr/local/lib")
l.AddLibraryPath("/opt/lib")       // searched first, ahead of auto-detected dirs
l.SetSysroot("/custom/sysroot")    // explicit override; otherwise auto-probed

l.AddObject("foo.o", data)
l.AddArchive("libbar.a", data)
l.AddDynamicLibrary("libc.so.6", data)
l.AddSONeeded("libm.so.6")

out, err := l.Link()
```

### Default interpreter and system search dirs are target-derived

Each arch subpackage registers its own default interpreter and search
dirs (see `RegisterDefaultInterp` / `RegisterSearchDirs` in `registry.go`);
the core package has no hardcoded per-arch paths.

| Target | Default interpreter |
|---|---|
| `x86_64-linux-gnu` | `/lib64/ld-linux-x86-64.so.2` |
| `x86_64-linux-musl` | `/lib/ld-musl-x86_64.so.1` |
| `aarch64-linux-gnu` | `/lib/ld-linux-aarch64.so.1` |
| `aarch64-linux-musl` | `/lib/ld-musl-aarch64.so.1` |
| others | none set automatically — `SetInterp` required, or link statically |

`AddLibraryPath` entries are searched first, then any active sysroot
prefix, then the target's registered search dirs unprefixed — project-
resolved paths always win over whatever the host happens to have.

### Sysroot auto-detection

Native builds (target arch+os matches host) skip probing and use
absolute system paths directly. Cross builds probe a small, fixed set
of conventional Debian/Ubuntu-style sysroot locations
(`/usr/<triple>`, `/usr/local/<triple>`) and use the first that exists:

```go
l := elf.NewLinker(t) // host linux/amd64, t = aarch64-linux-gnu:
                       // probes /usr/aarch64-linux-gnu automatically
```

This is intentionally conservative — it does not shell out to a
cross-`gcc` to ask it for its own sysroot. `SetSysroot` overrides it
explicitly whenever the probe guesses wrong or the toolchain layout is
nonstandard.

### Output types

| Constant | Description |
|---|---|
| `OutputExec` | Position-dependent executable (base `0x400000`) |
| `OutputPIE` | Position-independent executable |
| `OutputShared` | Shared library (`.so`) |

---

## Parsers

```go
obj, err := elf.ParseObject("foo.o", data)
ar, err  := elf.ParseArchive("libfoo.a", data, elf.ParseObject)
lib, err := elf.ParseSharedLib("libfoo.so", data)
```

`ParseArchive` accepts a `parseObject` callback so you can substitute
your own object parser. Members are parsed lazily via
`ArchiveMember.Object()`.

All three parsers currently require `EI_DATA == ELFDATA2LSB` (little-
endian input) regardless of the output `Target` — see **Upcoming arch
support** for what that means for `aarch64_be`/`armeb` today.

---

## Low-level pipeline

```go
t, _ := elf.ParseTarget("x86_64-linux-gnu")

// 1. Parse inputs (see Parsers above)

// 2. Symbol resolution
symtab := elf.NewSymbolTable()
err = symtab.Ingest(objects, archives, shared)

// 3. Section merging
layout, err := elf.MergeSections(objects)

// 4. Dead-code elimination (before PLT injection — synthetic PLT
//    sections have no Pieces, so GC would delete them if run after)
elf.GC(layout, symtab, objects, outputType, entrySymbol)

// 5. PLT injection — sized per t's registered PLTPatcher
pltSyms := elf.CollectPLTSymbols(symtab, objects)
elf.InjectPLTSections(layout, pltSyms, t)

// 6. Address assignment
err = elf.AssignLayout(outputType, layout, baseVA) // baseVA=0 → default for outputType

// 7. Symbol address resolution
err = elf.ResolveSymbolAddresses(symtab, layout)

// 8. PLT patching — looked up from the registry for t
pp, ok := elf.LookupPLTPatcher(t)
elf.PatchPLT(pp, layout, pltSyms)

// 9. Relocation patching — same registry lookup
patcher, ok := elf.LookupPatcher(t)
err = elf.PatchAll(layout, symtab, objects, patcher)

// 10. Emit
out, err := elf.Emit(&elf.EmitRequest{Target: t, /* ... */})
```

`Linker.Link()` runs this exact sequence for you; the low-level calls
are exposed for callers that need to intervene between phases (e.g.
custom section injection between GC and layout).

### Key types

```go
type Layout struct { ... }
func (l *Layout) SectionByName(name string) (*MergedSection, bool)

type MergedSection struct {
    Name       string
    Flags      SectionFlags
    Data       []byte   // nil for BSS
    Size       uint64
    VAddr      uint64   // set by AssignLayout
    FileOffset uint64   // set by AssignLayout
}

func (t *SymbolTable) Lookup(name string) *TableSymbol
func (t *SymbolTable) All() []*TableSymbol

type TableSymbol struct {
    Name  string
    VAddr uint64   // set by ResolveSymbolAddresses
    Weak  bool
    ...
}
```

---

## Registry

Per-arch codegen is registered, not switched on:

```go
type PatcherFactory     func(t Target) Patcher
type PLTPatcherFactory  func(t Target) PLTPatcher
type DefaultInterpFunc  func(t Target) string
type SearchDirsFunc     func(t Target) []string

func RegisterPatcher(a Arch, f PatcherFactory)
func RegisterPLTPatcher(a Arch, f PLTPatcherFactory)
func RegisterDefaultInterp(a Arch, f DefaultInterpFunc)
func RegisterSearchDirs(a Arch, f SearchDirsFunc)

func LookupPatcher(t Target) (Patcher, bool)
func LookupPLTPatcher(t Target) (PLTPatcher, bool)
```

Factories receive the whole `Target`, not just `Arch` — this is what
lets `aarch64`'s single subpackage serve both `aarch64` and
`aarch64_be` by closing over `t.BigEndian` at lookup time, instead of
needing a second subpackage that duplicates the same relocation math.

---

## Builder

```go
b := elf.NewBuilder(t.Arch)
b.SetEntry("_start")
b.SetInterp("/lib64/ld-linux-x86-64.so.2")
b.AddNeeded("libc.so.6")

b.AddSection(elf.Section{
    Name:  ".text",
    Type:  elf.SHT_PROGBITS,
    Flags: elf.SHF_ALLOC | elf.SHF_EXECINSTR,
    Data:  codeBytes,
    Align: 16,
})
b.AddSymbol(elf.Symbol{
    Name:    "_start",
    Section: ".text",
    Global:  true,
    Type:    elf.STT_FUNC,
})

out, err := b.Emit()
```

For shared libraries: `b.SetShared()`.

`Builder` is purely a serialiser — by the time `Emit` runs (via
`Linker.Link()`), relocations and PLT stubs are already patched into
the sections it's given. It never dispatches on arch itself; the raw
`e_machine` value is just stamped into the header.

---

## Note section helpers

```go
data := elf.BuildBuildID(sha1Digest)                 // .note.gnu.build-id
data := elf.BuildABITag(3, 0, 0)                      // .note.ABI-tag
data := elf.BuildGNUProperty(elf.GNU_PROPERTY_X86_FEATURE_1_IBT | elf.GNU_PROPERTY_X86_FEATURE_1_SHSTK)
data := elf.BuildNoteSection([]elf.Note{
    {Name: "GNU", Type: elf.NT_GNU_BUILD_ID, Desc: id},
})
```

---

## Dynamic linking helpers

```go
sorted, perm := elf.SortGNUHashSyms(names)
hashData := elf.BuildGNUHash(sorted, symOffset)          // .gnu.hash
hashData := elf.BuildSysVHash(allSymNames)                // .hash, index 0 must be ""
data := elf.BuildVersionSym([]uint16{0, 1, 2, ...})       // .gnu.version
data := elf.BuildVersionNeed([]elf.VersionNeed{
    {Library: "libc.so.6", Versions: []string{"GLIBC_2.17", "GLIBC_2.34"}},
}, dynstrOffsetFunc)                                       // .gnu.version_r
```

---

## Section flags

| Constant | sh_flags bit | Meaning |
|---|---|---|
| `SHF_ALLOC` | `0x2` | Occupies memory at runtime |
| `SHF_WRITE` | `0x1` | Writable |
| `SHF_EXECINSTR` | `0x4` | Executable |
| `SHF_TLS` | `0x400` | Thread-local storage |
| `SHF_MERGE` | `0x10` | Mergeable |
| `SHF_STRINGS` | `0x20` | Null-terminated strings |

Program headers `PT_PHDR`, `PT_INTERP`, `PT_LOAD`, `PT_DYNAMIC`, `PT_TLS`,
and `PT_GNU_STACK` are synthesised automatically. Use `Builder.AddSegment`
for `PT_GNU_RELRO`, `PT_NOTE`, `PT_GNU_EH_FRAME`, `PT_GNU_PROPERTY`, and
any custom entries.

---

## TODO — upcoming arch support

These arches are `Valid()` per the table above but not yet
`Supported()` — no subpackage exists for them yet:

| Arch | Blocked on |
|---|---|
| `arm` / `armeb` (32-bit) | 32-bit patcher + PLT stub encodings not yet written |
| `x86` (32-bit) | 32-bit patcher + PLT stub encodings not yet written |
| `riscv32`, `riscv64` | relocation math ported from prior flat-file version, needs re-verification against the psABI before landing as a subpackage |
| `powerpc`, `powerpc64` (`le`/`be`) | not started |
| `mips32`, `mips32el`, `mips64`, `mips64el` | not started |
| `loongarch64` | not started |
| `s390x` | not started |

Additionally:

- **Big-endian input parsing.** `object.go` / `shared.go` hard-reject
  any `EI_DATA` other than `ELFDATA2LSB`. `aarch64_be`/`armeb` can
  currently only be an *output* target fed from little-endian object
  files — real BE toolchain support needs `reader.go` generalized to a
  selectable byte order, threaded through every struct-offset read in
  the parsers. Not started.
- **`aarch64_be` PLT bytes.** The `aarch64/plt.go` PLT0/stub instruction
  templates are literal little-endian encodings copied in directly;
  a true BE target needs each instruction word byte-swapped before
  the copy, not just the GOT/RELA field writes (which already respect
  `Target.BigEndian`). Not started.
- **`Tier`.** Currently a bare string tag with no enforced semantics
  beyond `os=none` requiring a non-empty one for TLS. Needs a real
  design pass (what a tier actually gates, e.g. `avx2`) before it's
  load-bearing.
- **Sysroot probing.** Checks two conventional paths
  (`/usr/<triple>`, `/usr/local/<triple>`) and gives up; doesn't shell
  out to a cross-toolchain to ask for its actual sysroot. Fine for
  Debian/Ubuntu-packaged cross-gcc layouts, not guaranteed elsewhere.