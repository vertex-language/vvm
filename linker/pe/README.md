# linker/pe — Portable Executable linker (Windows/UEFI-native naming)

PE sub-package for `github.com/vertex-language/vvm/linker`. This package
emits the PE32+ container format used by Windows and UEFI. Naming mirrors
what MSVC's `link.exe` and the mingw-w64 toolchain actually use — a
`Target` in this package is a target triple, not a generic ELF/Mach-O-shaped
struct wearing a PE hat.

## Import

```go
import "github.com/vertex-language/vvm/linker/pe"

// blank-import whichever arch backends you need registered:
import (
    _ "github.com/vertex-language/vvm/linker/pe/x64"
    _ "github.com/vertex-language/vvm/linker/pe/aarch64"
    _ "github.com/vertex-language/vvm/linker/pe/arm64ec"
)
```

---

## Quick start

```go
t, err := pe.ParseTarget("x86_64-pc-windows-msvc")
if err != nil {
    log.Fatal(err)
}

l := pe.NewLinker(t)
if !l.Supported() {
    log.Fatalf("%s: no codegen backend registered (blank-import its subpackage)", t)
}
l.SetEntryPoint("mainCRTStartup")

l.AddObject("main.obj", mainBytes)
l.AddImportLibrary("kernel32.lib", kernel32Bytes)

out, err := l.Link()
os.WriteFile("a.exe", out, 0755)
```

---

## Target

A `Target` is `(Arch, OS, ABI)`, parsed from and printed as the same triple
shape `clang --target=` / mingw-w64 prefixes use:

```go
type Target struct {
    Arch Arch // ArchX86_64, ArchI686, ArchARM, ArchAArch64, ArchARM64EC
    OS   OS   // OSWindows, OSUEFI
    ABI  ABI  // ABINone, ABIMSVC, ABIGNU
}

func ParseTarget(s string) (Target, error) // "x86_64-pc-windows-gnu", "aarch64-unknown-uefi"
func (t Target) String() string            // round-trips ParseTarget
func (t Target) Valid() error
```

`String()` picks the right shape per OS — UEFI targets have no ABI
component (`<arch>-unknown-uefi`), Windows targets do
(`<arch>-pc-windows-<abi>`).

### What's valid (`arch` × OS/ABI)

| `Arch` | `windows-msvc` | `windows-gnu` | `unknown-uefi` |
|---|---|---|---|
| `x86_64` | ✓ | ✓ | ✓ |
| `i686` | ✓ | ✓ | ✓ |
| `aarch64` | ✓ | ✓ | ✓ |
| `arm` (legacy WoA32) | ✓ (only ABI it supports) | — | — |
| `arm64ec` | ✓ (only ABI it supports — no mingw-w64 arm64ec convention exists) | — | — |

`Valid()` checks the triple is a real, tooling-recognized combination —
not whether *this build* has codegen for it. `Linker.Supported()` answers
that, same split `ParseTarget`/`link.exe` availability makes in practice.

`arm64ec` deliberately shares `x86_64`'s `Arch.machine()` value
(`IMAGE_FILE_MACHINE_AMD64`) — an EC ("Emulation Compatible") image is a
real x64 PE image with ARM64-native code regions, not a distinct machine
type in the header. The distinction only matters at the object-relocation
level (see `arm64ec` subpackage below).

---

## Linker

```go
l := pe.NewLinker(t)
l.SetOutputType(pe.OutputExec)   // OutputExec | OutputPIE | OutputShared
l.SetEntryPoint("mainCRTStartup")
l.SetSubsystem(pe.SubsystemWindowsGUI)
l.SetOutputName("mydll.dll")     // export directory's own Name, OutputShared only
l.SetMinOSVersion(6, 1)
l.AddLibraryPath(`C:\libs`)

l.AddObject("foo.obj", data)
l.AddArchive("libbar.a", data)
l.AddImportLibrary("kernel32.lib", data)
l.AddDynamicLibrary("user32.dll", data)

out, err := l.Link()
```

`Linker.Supported()` reports whether a codegen backend is registered for
`Target.Arch` — i.e. whether the relevant subpackage has been
blank-imported. `Link()` fails fast with a clear error if it hasn't.

`NewLinker` seeds `Entry` from whatever `RegisterDefaultEntryPoint` the
arch's `init()` registered (falling back to `"mainCRTStartup"` if none is
registered), and seeds `Subsystem` from `defaultSubsystem(t)` —
`SubsystemEFIApplication` for UEFI targets, `SubsystemWindowsCUI`
otherwise.

### Linking against DLLs: three sources, one priority order

Unlike ELF/Mach-O, a PE import table entry doesn't strictly need the real
DLL on disk — an import library (`.lib`) carries author-chosen DLL names
per symbol independent of whether that DLL is ever parsed directly. Three
ways to declare a dependency:

```go
l.AddImportLibrary("kernel32.lib", data)                          // short-format .lib; DLL itself never needed
l.AddDynamicLibrary("user32.dll", data)                            // real parse: reads export directory, PE headers
l.AddDynamicLibrary("foo.dll", data, pe.WithImportName("FOO.dll")) // real parse, pinned import-table string
```

`SymbolTable.Ingest` (`symtab.go`) processes these in a fixed order —
objects, then import libraries, then direct DLL parses, then archives —
and only fills a symbol in from a later source if it's still undefined or
lazy. This means **an explicit import library always wins over a
same-named direct DLL parse**: if both `AddImportLibrary` and
`AddDynamicLibrary` could resolve a symbol, the import library's
author-supplied `DLLName` is what ends up in the import table, not
whatever `AddDynamicLibrary`'s parse would have produced.

For a direct DLL parse (`AddDynamicLibrary`), the import-table string
itself follows a fixed priority that never trusts the DLL's own internals:

1. `WithImportName(...)`, if supplied.
2. The literal `name` argument passed to `AddDynamicLibrary`.

The DLL's export directory does have its own self-reported `Name` field,
and `parseDLL` (`shared.go`) does read it — but only into `InternalName`,
kept for diagnostics. It is never consulted for the import-table string.
This was a real v1 bug: PE has no equivalent of ELF's `DT_SONAME`
convention where a target's self-report is authoritative, and treating it
as one meant a same-named-but-differently-pathed DLL could silently
override the name actually requested.

### DLL search directories

```go
dirs := pe.SearchDirs(pe.ABIMSVC) // registered per-ABI, not per-arch
```

Exported specifically so vvm's own link-dependency resolver can locate
real system DLLs on disk before handing their bytes to
`AddDynamicLibrary` — this package itself never walks the filesystem.
Both `x64`/`arm64ec`/`aarch64`'s `register.go` register the same
Windows system directories (`System32`, `SysWOW64`, `System`) for both
`ABIMSVC` and `ABIGNU`, since the ABI — not the arch — is what determines
which real DLLs a given toolchain expects to find.

### Exports (`OutputShared`)

```go
l.SetOutputType(pe.OutputShared)
l.SetOutputName("mydll.dll")
```

`ExportCandidates` (`export.go`) is the single source of truth for "what
counts as an export root": non-weak, strongly-or-tentatively defined,
COFF-external, with a real section name. `GC`'s own `OutputShared`
reachability roots use this exact same function — kept as one function
so the two can never silently diverge (see `gc.go`).

### Garbage collection

```go
pe.GC(layout, symtab, objects, outputType, entry)
```

Roots are either the entry symbol (`OutputExec`/`OutputPIE`) or every
export candidate (`OutputShared`). `.pdata`/`.xdata` (exception-handling
data used by both AMD64 and ARM64 unwind) are always kept regardless of
reachability — see `isEssentialSection`.

### Base relocations

```go
if collector, ok := patcher.(pe.BaseRelocCollector); ok {
    sites := collector.BaseRelocSites()
    // ... buildBaseRelocSection(sites, baseVA) ...
}
```

Only emitted for non-`OutputExec` builds (PIE/shared images actually need
to relocate). Each `Patcher.Apply` call that writes an `ADDR64`/absolute
64-bit pointer records the site itself — the patcher, not a separate pass,
is the source of truth for which VAs need a base relocation entry.

---

## Adding a new arch

```go
// linker/pe/i686/register.go
package i686

import "github.com/vertex-language/vvm/linker/pe"

func init() {
    pe.RegisterPatcher(pe.ArchI686, func(t pe.Target) pe.Patcher {
        return &i386Patcher{}
    })
    pe.RegisterImportPatcher(pe.ArchI686, func(t pe.Target) pe.ImportPatcher {
        return &i386ImportPatcher{}
    })
    pe.RegisterDefaultEntryPoint(pe.ArchI686, func(t pe.Target) string {
        return "mainCRTStartup"
    })
}
```

`Patcher.Apply(data, off, relType, P, S, A) error` applies one relocation
given the patch site's VA (`P`), the resolved symbol VA (`S`), and the
addend (`A`) extracted from the instruction bytes at object-parse time
(`object.go`'s `coffReadAddend` — COFF relocations are REL-style, so the
addend lives inline, not as a separate on-disk field the way ELF's RELA
convention stores it).

`ImportPatcher.PatchImportThunks(thunk, iat, thunkBase, iatBase, syms)`
writes one arch-specific jump-stub per imported symbol into `.thunk` and
assigns each symbol's final `VAddr` to its stub — see the `x64`/`aarch64`
implementations for the two existing stub shapes (`jmp [rip+disp32]` vs
`ADRP`/`LDR`/`BR`). An `ImportPatcher` may additionally implement
`IATLayoutSetter` to receive the computed `*IATLayout` (DLL ordering and
per-symbol IAT slot) before `PatchImportThunks` runs.

`RegisterSearchDirs` is keyed by `ABI`, not `Arch` — register it once per
ABI your arch cares about (see the existing subpackages' `register.go`).

---

## Folder layout

```
linker/pe/
├── README.md
├── target.go        // Target, ParseTarget, Arch/OS/ABI, Valid()
├── registry.go       // Patcher/ImportPatcher/entry-point/search-dir factory registries
├── linker.go          // Linker struct, NewLinker, Link() pipeline
├── layout.go           // Layout, MergeSections, AssignLayout, ResolveSymbolAddresses
├── gc.go                // dead-section elimination
├── import.go             // IATLayout, idataGeom, fillImports — .idata/.iat geometry
├── importthunks.go        // ImportThunkEntry, ImportPatcher, CollectImportSymbols, InjectImportSections
├── importlib.go            // ParseImportLibrary — short-format .lib (IMPORT_OBJECT_HEADER)
├── export.go                // ExportCandidates, exportGeom, fillExportDirectory — .edata geometry
├── object.go                 // parseObject (COFF object, package-internal)
├── shared.go                  // parseDLL (real DLL export-directory parse, package-internal)
├── archive.go                  // rawArEntries (shared ar-container reader), ParseArchive
├── patch.go                     // Patcher/ImportPatcher/BaseRelocCollector interfaces, PatchAll
├── builder.go                    // emitPE — PE32+ header, section table, data directories
├── reader.go                      // bounds-checked little-endian reader
├── constants.go                    // PE/COFF magic numbers, data-directory indices, Subsystem
├── symtab.go                        // SymbolTable, resolution rules
├── types.go                          // Object/Section/Symbol/Reloc/SharedLib types
│
├── x64/       // register.go, patch.go, importthunks.go — implemented
├── aarch64/   // register.go, patch.go, importthunks.go — implemented
├── arm64ec/   // register.go, patch.go, importthunks.go — implemented; see limitations below
│
└── (unregistered — see below)
```

`arm64ec` gets its own subpackage rather than a flag on `aarch64`, same
reasoning as `macho`'s `arm64e`/`arm64_32` split: EC's object-level
relocations use the ARM64 numeric reloc types against ARM64EC-native
code, even though the final image's COFF `Machine` field and `.thunk`
encoding are AMD64/x64 (`Arch.machine()` returns `imageMachineAMD64` for
both `ArchX86_64` and `ArchARM64EC` — see `target.go`). Small per-arch
helpers (instruction encoders, thunk shapes) are duplicated verbatim
across `aarch64` and `arm64ec` rather than shared, matching each
subpackage's self-contained design.

---

## Known limitations

- **`arm64ec`**: no x64 shadow-space call-boundary adjustments and no CHPE
  metadata (`IMAGE_DIRECTORY_ENTRY_LOAD_CONFIG` range table) are emitted.
  Output links end-to-end, but tools/loaders that inspect CHPE-specific
  metadata will see what is effectively a plain x64 image.
- **`walkSharedDeps` is not implemented**: the linker relies entirely on
  explicit `AddDynamicLibrary`/`AddImportLibrary`/`AddArchive` calls —
  there is no automatic transitive-dependency walk of a DLL's own
  `Needed` list (`shared.go` does populate `SharedLib.Needed` from the
  import directory, but `Link()` never consults it to pull in further
  libraries automatically).
- **`ArchI686`** and **`ArchARM`** (legacy WoA32) are valid `Target`
  values per `Valid()`, but no subpackage in this tree registers a
  `Patcher`/`ImportPatcher` for either — `Linker.Supported()` returns
  `false` until a subpackage following the "Adding a new arch" pattern
  above is added and blank-imported.
- **No fat/universal equivalent**: PE has no multi-arch container the way
  Mach-O's fat binaries do; each `Link()` call produces a single
  single-arch image.