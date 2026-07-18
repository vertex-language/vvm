# linker/pe — PE32+ linker (Windows/COFF-native naming)

PE32+ sub-package for `github.com/vertex-language/vvm/linker`. This
package emits the container format used by the Windows loader and by
UEFI firmware. ELF and Mach-O live in sibling packages (`linker/elf`,
`linker/macho`) and are selected by `os`, not by this package — several
`os` values route here (`windows`, `uefi`), and this package doesn't
care which one you pick; it only cares that the *format* is PE32+.
**Every image this package produces is PE32+ (`IMAGE_NT_OPTIONAL_HDR64_MAGIC`)
— there is no 32-bit PE support anywhere in the parsers or the emitter.**

Naming mirrors what `link.exe`, `dumpbin`, and `clang-cl` actually
print — `/MACHINE:X64`, not `/MACHINE:AMD64`. A `Target` in this
package reads the same way `clang-cl -target aarch64-pc-windows-msvc`
reads.

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
l.SetSubsystem(pe.SubsystemWindowsCUI)

l.AddObject("main.obj", mainBytes)
l.AddArchive("libcmt.lib", libcmtBytes)
l.AddDynamicLibrary("kernel32.dll", kernel32Bytes)

out, err := l.Link()
os.WriteFile("program.exe", out, 0o755)
```

---

## Target

```go
type Target struct {
    Arch Arch // ArchX86_64, ArchI686, ArchARM, ArchAArch64, ArchARM64EC
    OS   OS   // OSWindows, OSUEFI
    ABI  ABI  // ABIMSVC, ABIGNU — meaningless (zero value) under OSUEFI
}

func ParseTarget(s string) (Target, error) // "aarch64-pc-windows-msvc"
func (t Target) String() string            // round-trips ParseTarget
func (t Target) Valid() error
```

`ParseTarget` splits the triple on `-`; the first component must be a
known `Arch` spelling, and the remaining components are scanned for the
literal tokens `windows`, `uefi`, `msvc`, `gnu` (anything else, like the
`pc` in `x86_64-pc-windows-msvc`, is silently ignored — there's no strict
positional grammar). `String()` always reconstructs the canonical form:
`<arch>-pc-windows-<abi>`, or `<arch>-unknown-uefi` when `OS == OSUEFI`.

`Arch` is spelled the way `clang-cl -target` / `rustc --print
target-list` spell it (`aarch64`, not `arm64`) — same convention
`linker/elf` already uses. It resolves to Microsoft's own `/MACHINE:`
name and COFF `IMAGE_FILE_MACHINE_*` constant at emit time:

| `Arch` (triple spelling) | `/MACHINE:` | Final-image `IMAGE_FILE_MACHINE_*` |
|---|---|---|
| `x86_64` | `X64` | `AMD64` (`0x8664`) |
| `i686` | `X86` | `I386` (`0x14C`) |
| `arm` | `ARM` | `ARMNT` (`0x1C4`) |
| `aarch64` | `ARM64` | `ARM64` (`0xAA64`) |
| `arm64ec` | `ARM64EC` | `AMD64` (`0x8664`) — see note below |

**`arm64ec` is not a distinct final-image machine type.** `Arch.machine()`
maps both `ArchX86_64` and `ArchARM64EC` to `IMAGE_FILE_MACHINE_AMD64`.
A linked ARM64EC EXE/DLL's real ARM64EC-ness is meant to be signaled via
CHPE metadata in `IMAGE_DIRECTORY_ENTRY_LOAD_CONFIG`, not the header's
machine field — and this package does not emit that metadata (see Known
limitations).

### What's valid (`Target.Valid()`)

`Valid()` switches on `t.OS` and then `t.Arch`:

| `arch` | `windows` + `msvc` | `windows` + `gnu` | `uefi` |
|---|---|---|---|
| `x86_64` | ✓ | ✓ | ✓ |
| `i686` | ✓ | ✓ | ✓ |
| `arm` | ✓ (msvc only — "legacy WoA32 only supports msvc abi") | — | — |
| `aarch64` | ✓ | ✓ | ✓ |
| `arm64ec` | ✓ (msvc only — "no mingw-w64 arm64ec convention exists") | — | — |

Under `OSWindows`, `ArchX86_64`/`ArchI686`/`ArchAArch64` require `ABIMSVC`
or `ABIGNU` (any other value errors); `ArchARM` and `ArchARM64EC` each
hard-require `ABIMSVC`. Under `OSUEFI`, only `ArchX86_64`, `ArchI686`,
and `ArchAArch64` are accepted — `ArchARM` and `ArchARM64EC` are rejected
outright ("arch %s has no UEFI convention"). `Valid()` checks the triple
is a real combination but not whether *this build* has codegen for it —
`Linker.Supported()` answers that.

---

## Linker

```go
l := pe.NewLinker(t)
l.SetOutputType(pe.OutputExec)         // OutputExec | OutputPIE | OutputShared
l.SetEntryPoint("mainCRTStartup")
l.SetSubsystem(pe.SubsystemWindowsCUI) // see Subsystem table below
l.SetDLLName("mylib.dll")              // stored, but currently unused — see Known limitations
l.SetMinOSVersion(6, 2)                // sets BOTH the OS version and the subsystem version
l.AddLibraryPath("/opt/lib")

l.AddObject("foo.obj", data)
l.AddArchive("libbar.lib", data)
l.AddDynamicLibrary("kernel32.dll", data)
l.AddDLLNeeded("api-ms-win-crt-runtime-l1-1-0.dll") // explicit import-directory entry, no import lib supplied

out, err := l.Link()
```

`NewLinker` defaults: `OutputExec`; entry point from the arch's
registered default (`RegisterDefaultEntryPoint`), falling back to
`"mainCRTStartup"` if none is registered; subsystem from
`defaultSubsystem(t)`; `MajorOSVersion`/`MinorOSVersion` and
`MajorSubsystemVersion`/`MinorSubsystemVersion` all default to `6, 1`.

`Linker.Supported()` reports whether a codegen `Patcher`/`PLTPatcher` is
registered for `Target.Arch` — i.e. whether the relevant subpackage has
been blank-imported. `Link()` fails fast with a clear error if it hasn't.

**`SetMinOSVersion(major, minor)` couples the OS version and the
subsystem version** — it writes both pairs to the same values. There is
no separate setter to diverge them; if you need
`MajorOSVersion`/`MajorSubsystemVersion` to differ, you'll need to set
the fields on `Linker` directly (they're currently only reachable
through this one combined setter).

### Subsystem

```go
type Subsystem uint16

const (
    SubsystemWindowsGUI           Subsystem = 2
    SubsystemWindowsCUI           Subsystem = 3
    SubsystemEFIApplication       Subsystem = 10
    SubsystemEFIBootServiceDriver Subsystem = 11
    SubsystemEFIRuntimeDriver     Subsystem = 12
    SubsystemEFIROM               Subsystem = 13
)
```

These match the `IMAGE_SUBSYSTEM_*` values in `winnt.h` exactly.
`defaultSubsystem(t)` picks `SubsystemEFIApplication` when `t.OS ==
OSUEFI`, otherwise `SubsystemWindowsCUI`.

### Output types

| Constant | Description |
|---|---|
| `OutputExec` | Position-dependent `.exe`; `.reloc` never built; `IMAGE_FILE_RELOCS_STRIPPED` set |
| `OutputPIE` | Position-independent executable; `.reloc` emitted only if the patcher actually reported absolute-address write sites |
| `OutputShared` | `.dll`; `IMAGE_FILE_DLL` set; `.reloc` emitted under the same condition as `OutputPIE` |

`DYNAMIC_BASE`/`HIGH_ENTROPY_VA` are advertised in the optional header
only when `OutputType != OutputExec` **and** a `.reloc` section was
actually produced — a PIE or DLL with no absolute-address relocations
gets neither flag, even though it's non-`OutputExec`. `NX_COMPAT`/
`TERMINAL_SERVER_AWARE` are always set.

---

## Link pipeline

`Link()` runs, in order:

```
walkSharedDeps          — transitive DLL dependency walk
                          (api-ms-win-* / ext-ms-win-* API Sets skipped)
    ↓
SymbolTable.Ingest      — objects → shared libs → archives (repeated until no
                          new member is extracted) → error on any strong
                          undefined reference from an object file
    ↓
MergeSections           — combine same-named input sections
    ↓
CollectPLTSymbols       — shared symbols actually referenced by a relocation
    ↓
GC                      — dead-section elimination (see below)
    ↓
[only if any PLT symbols exist]
InjectPLTSections       — append synthetic .plt / .got.plt
computeIATLayout        — group PLT symbols into per-DLL IAT slot ranges
injectIdata             — append a placeholder .idata section, sized by
                          computeIdataGeom
    ↓
AssignLayout            — assign VAddrs and advisory file offsets
    ↓
ResolveSymbolAddresses  — fill VAddr on every defined symbol
    ↓
[only if any PLT symbols exist]
PatchPLT                — write import-thunk stubs into .plt / .got.plt
fillImports             — write the import directory / ILT / hint-name
                          table / DLL-name area into .idata, mirror the
                          IAT slots
    ↓
PatchAll                — apply all COFF relocations, via the registered Patcher
    ↓
[only if OutputType != OutputExec]
buildBaseRelocSection   — collected from the Patcher's BaseRelocSites(),
                          appended as .reloc via AppendAllocSection
    ↓
emitPE                  — serialise DOS stub, headers, sections
```

Note that address assignment (`AssignLayout`) happens *before* the
import thunks and import directory are patched, and relocation patching
(`PatchAll`) happens *before* `.reloc` is built — `.reloc` is
necessarily a post-pass, since it's derived from the absolute writes
`PatchAll` itself performed.

---

## Parsing

```go
obj, err := pe.parseObject(name, data)   // unexported; reached via AddObject
ar, err  := pe.ParseArchive(name, data, parseObject)
lib, err := pe.parseDLL(name, data)      // unexported; reached via AddDynamicLibrary
```

### Objects (COFF)

`parseObject` reads a plain COFF object: machine type (only `AMD64`,
`ARM64`, `I386`, `ARMNT` are recognized — anything else errors),
sections, symbols (including auxiliary-record skipping, so `SymIdx`
values used by relocations stay aligned with the raw COFF symbol table
plus the leading nil sentinel), and relocations.

- Sections named `.drectve`, `.llvm_addrsig`, `.llvm.call-graph-profile`,
  or carrying the `IMAGE_SCN_LNK_INFO`/`IMAGE_SCN_LNK_REMOVE`
  characteristics, are parsed for bookkeeping but marked `Skip` and
  contribute no data or relocations.
- Section alignment is decoded from characteristics bits 20–23; field
  `0` means 16-byte alignment (COFF's default), otherwise `1 <<
  (field-1)`.
- **Inline addend extraction is only implemented for `AMD64` and
  `ARM64`** (`coffReadAddend`): the addend is read out of the section
  bytes at the relocation site, zeroed in place, and stored on
  `ObjectReloc.Addend`. `I386`/`ARMNT` objects parse successfully (the
  machine check accepts them) but their relocations always carry a
  zero addend — there's no codegen backend for those machines yet
  either, so this hasn't mattered in practice.
- `AMD64` and `ARM64` happen to reuse the same small relocation-type
  integers (e.g. `RelAMD64Addr64 == RelARM64Addr32 == 1`), so
  `coffReadAddend` branches on `machine` before switching on `relType`
  — a single combined switch isn't possible in Go without duplicate
  `case` values.

### Archives (`.lib`)

`ParseArchive` reads the common ar container (`!<arch>\n` magic,
60-byte member headers terminated by `` `\n ``), resolves the GNU-style
long-name table (`//` member) for names over 16 bytes, and recognizes
**both** GNU/SysV-style (`/`, `/SYM64/`) and BSD/Darwin-style
(`__.SYMDEF`, `__.SYMDEF_64`) symbol-index members for 32-bit and
64-bit symbol tables respectively. If none of those members are present
— or produce an empty index — `ParseArchive` falls back to eagerly
parsing every member's object and scanning its defined global/weak
symbols itself, so archives without any symbol table still resolve
correctly, just without the fast path.

### Shared libraries (import libraries)

`parseDLL` reads a real PE32+ image (`MZ`, then `PE\0\0`, then a PE32+
optional header only — `parseDLL` errors on the 32-bit optional-header
magic) and extracts:
- the export directory's own name as `Soname`, and every named export
  as a `SharedExport` (ordinal recorded as `"@<ordinal>"` in `Version`);
- the import directory's list of needed DLL names as `Needed`.

There's no COFF import-library (`.lib` wrapping a `.dll`) format parsed
separately — a "shared library" input here is expected to be the actual
`.dll` bytes.

---

## Symbol resolution

Classical left-to-right Unix semantics, same as `linker/elf`:
1. object files define the initial set;
2. shared libraries fill in anything still undefined or lazy;
3. archives are pulled in a loop until no member resolves a currently-undefined
   strong symbol;
4. any name that's still `kindUndefined`, non-weak, and was referenced by
   an object file errors out as `undefined reference to %q`.

`TableSymbol.Kind` has five values (`kindUndefined`, `kindLazy`,
`kindShared`, `kindCommon`, `kindDefined`) though nothing in this
package currently constructs a `kindLazy` symbol — it's handled
identically to `kindUndefined` wherever it's checked, reserved for a
future lazy-binding path.

---

## Dead-section elimination (`GC`)

Roots are chosen by output type:
- **`OutputShared`**: every symbol that is non-weak, strongly defined
  (not a tentative common), and has a real section — i.e. there's no
  actual export-directory filter (see Known limitations), so this is
  "keep everything strongly defined," not "keep only what's exported."
- **`OutputExec`/`OutputPIE`**: just the entry symbol.

If none of the chosen roots actually resolve to a section present in
the `Layout`, `GC` returns without touching anything (a defensive
no-op, not an error). Otherwise it does a section-level BFS: from each
reachable `MergedSection`, it follows every relocation whose target
falls inside that section to the symbol it references (resolved either
locally, via the input object's own section table, or globally via the
`SymbolTable`), marking the referenced section reachable in turn. Kept
sections are: every non-`SecAlloc` section (debug info etc.,
unconditionally kept), every reached section, and `.pdata`/`.xdata`
regardless of reachability — those two carry Windows x64 SEH data that
nothing directly relocates against but the OS/debugger still needs at
runtime.

---

## Import thunks (PLT / IAT)

For each shared symbol actually referenced by a relocation
(`CollectPLTSymbols`, in stable first-seen order), the linker reserves
a 16-byte thunk slot in `.plt` and an 8-byte slot in `.got.plt`
(`pltEntrySize`/`gotEntrySize`), grouped per-DLL with a null-terminator
entry after each DLL's group, following 3 reserved header slots
(`gotReserved`) at the start of `.got.plt`. `computeIATLayout` computes
the per-DLL slot ranges once; `computeIdataGeom`/`fillImports` build the
import directory, ILT, hint/name table, and DLL-name area from that same
layout, so the pre-layout size estimate and the post-layout byte fill
can never disagree.

Per-arch thunk *encoding* (the actual bytes written into `.plt`) is
supplied by whatever `PLTPatcher` the target's subpackage registers —
this package only fixes the slot sizes and grouping, not the
instruction sequence.

**Delay-load imports (`.didat`) are out of scope** — only ordinary,
eagerly-bound `.idata` imports are ever built;
`IMAGE_DIRECTORY_ENTRY_DELAY_IMPORT` is never populated.

---

## Base relocations

Any `Patcher` implementing `BaseRelocCollector` has its accumulated
absolute-address write sites (`BaseRelocSites()`) collected after
`PatchAll` (only for `OutputType != OutputExec`) and grouped into
4KB-page blocks by `buildBaseRelocSection`. **Only `IMAGE_REL_BASED_DIR64`
entries are ever emitted** — this package only supports 64-bit absolute
relocations, which matches its PE32+-only scope. A page block's entry
count is padded to even with a zero (`IMAGE_REL_BASED_ABSOLUTE`) filler
entry when needed. The resulting `.reloc` section is placed via
`Layout.AppendAllocSection` with `SecDiscard` set, 4-byte aligned,
contiguous after the highest already-allocated VAddr.

---

## Layout and emission

`MergeSections` groups same-named input sections, respecting each
input section's own alignment. `AssignLayout` buckets merged sections
into RX / RO / RW groups (in that order) plus a non-allocatable group,
and tiles virtual addresses **with no gaps** — the NT loader validates
during image-section creation that each section's `VirtualAddress`
equals the previous section's `VirtualAddress` plus its page-rounded
`VirtualSize`; a hole is rejected with `ERROR_BAD_EXE_FORMAT` (Win32
193) before any code runs, so `AssignLayout` advances by the
page-rounded size rather than the raw size.

**`MergedSection.FileOffset`, as set by `AssignLayout`, is advisory
only.** The actual file is serialized by `emitPE` in `builder.go`,
which re-derives every section's file offset from scratch in address
order, packed densely at `peFileAlign` (`0x200`) starting right after
the headers — it never reads `ms.FileOffset`. This split exists because
`.reloc`'s final size (and therefore where later sections must land in
the file) isn't known until after `PatchAll` has run, well after
`AssignLayout`.

`emitPE` computes `AddressOfEntryPoint` by looking `req.Entry` up in the
symbol table; **if the entry symbol isn't found (or resolves to VAddr
0), the field is silently left at `0`** rather than erroring — there's
no validation that the requested entry point actually exists by the
time the image is emitted.

---

## Registry

Per-arch codegen is registered, not switched on:

```go
type PatcherFactory    func(t Target) Patcher
type PLTPatcherFactory func(t Target) PLTPatcher

func RegisterPatcher(a Arch, f PatcherFactory)
func RegisterPLTPatcher(a Arch, f PLTPatcherFactory)
func RegisterDefaultEntryPoint(a Arch, f func(t Target) string) // "mainCRTStartup" vs mingw-w64's entry
func RegisterSearchDirs(a ABI, f func() []string)               // note: keyed by ABI, not Arch

func LookupPatcher(t Target) (Patcher, bool)
func LookupPLTPatcher(t Target) (PLTPatcher, bool)
```

`RegisterSearchDirs`/`lookupSearchDirs` are keyed by `ABI`, not `Arch` —
DLL search-path conventions (System32/SysWOW64-style vs. a mingw-w64
sysroot layout) are an ABI property in this package's model, shared
across every arch under that ABI.

### Adding a new arch

```go
// linker/pe/arm64ec/register.go
package arm64ec

import "github.com/vertex-language/vvm/linker/pe"

func init() {
    pe.RegisterPatcher(pe.ArchARM64EC, func(t pe.Target) pe.Patcher {
        return arm64ecPatcher{}
    })
    pe.RegisterPLTPatcher(pe.ArchARM64EC, func(t pe.Target) pe.PLTPatcher {
        return arm64ecPLTPatcher{}
    })
}
```

No edits to `linker.go`, `builder.go`, or any other arch's files.

---

## Folder layout

```
linker/pe/
├── README.md
├── target.go     // Target, ParseTarget, Arch/OS/ABI, Valid()
├── registry.go   // Patcher/PLTPatcher factory registries, Supported()
├── linker.go     // Linker struct, NewLinker, Link() pipeline
├── builder.go    // emitPE: header, sections, data directories
├── layout.go     // Layout, MergeSections, AssignLayout, ResolveSymbolAddresses
├── gc.go         // dead-section elimination
├── dynamic.go    // PLT/GOT scaffolding
├── import.go     // IATLayout, .idata geometry + fill, base-reloc builder
├── object.go     // parseObject (COFF)
├── archive.go    // ParseArchive (GNU/SysV + BSD-style symbol tables)
├── shared.go     // parseDLL (export/import directory parsing)
├── patch.go      // Patcher interface, PatchAll
├── reader.go     // bounds-checked little-endian reader
├── constants.go  // COFF/PE constants, subsystem/dll-char values
├── symtab.go     // SymbolTable, resolution rules
├── types.go      // Object/Section/Symbol/Reloc types
│
├── x64/       // patch.go, plt.go, register.go — implemented
├── aarch64/   // patch.go, plt.go, register.go — implemented
├── arm64ec/   // patch.go, plt.go, register.go — registered; see Known limitations
│
└── (not yet implemented — see "Upcoming arch support" below)
    x86/, arm/
```

---

## Known limitations

- **No PE export directory is ever emitted, for any output type.**
  `Linker.SetDLLName`/`EmitRequest.DLLName` are plumbed all the way
  through to `emitPE`, but `emitPE` never writes an
  `IMAGE_DIRECTORY_ENTRY_EXPORT` (`dirExport`) entry or an export table
  — `DLLName` is currently inert. `GC`'s `OutputShared` root selection
  (see above) also doesn't restrict itself to a real export list, since
  none exists. Practically: **an `OutputShared` build today produces a
  structurally valid DLL that other binaries cannot actually import
  symbols from.** Treat `OutputShared` as "emits `IMAGE_FILE_DLL` and
  the right base-relocation/ASLR bits," not as "produces a usable
  import library counterpart."
- **`arm64ec`**: thunks and relocation patching are wired up and will
  link end-to-end, but two things are missing:
  1. The ARM64EC calling-convention adjustments (x64-shadow-space
     reservation at EC/x64 call boundaries) aren't applied yet.
  2. The CHPE metadata block (`IMAGE_DIRECTORY_ENTRY_LOAD_CONFIG`'s
     CHPE redirection/range tables) isn't emitted, so the output is a
     structurally valid x64 image (`Machine == 0x8664`) that the OS
     loader and tools like Task Manager or `dumpbin` will not recognize
     as ARM64EC at all.
- **ARM64X (hybrid) images**: out of scope. This package emits
  single-machine images only.
- **`i686`/`arm` (32-bit) codegen**: the object parser accepts these
  machine types and section-alignment/skip logic works for them, but no
  `Patcher`/`PLTPatcher` is registered anywhere in this package, and
  inline-addend extraction (`coffReadAddend`) doesn't handle them
  either — `Linker.Supported()` will report `false` for these targets
  until a subpackage is added.
- **Entry-point validation**: `emitPE` silently emits
  `AddressOfEntryPoint = 0` if the configured entry symbol can't be
  resolved, rather than erroring.

## Upcoming arch support

| Arch | Blocked on |
|---|---|
| `x86` (32-bit, `i686`) | 32-bit patcher + PLT stub encodings not yet written |
| `arm` (32-bit, legacy Windows-on-ARM, `/MACHINE:ARM` → `ARMNT`) | patcher not yet ported to this pipeline |

## Retired / out-of-scope

| Item | Status |
|---|---|
| `IMAGE_FILE_MACHINE_ARM64X` (`0xA64E`) | Hybrid-only marker value, never a standalone codegen target. |
| `IMAGE_FILE_MACHINE_EBC` (`0xEBC`, EFI Byte Code) | No codegen backend planned. |
| 32-bit PE (`IMAGE_NT_OPTIONAL_HDR32_MAGIC`) | Not parsed or emitted anywhere in this package. |
| PE export directory | Not emitted — see Known limitations. |
| Delay-load imports (`.didat`) | Not emitted; see Import thunks above. |
| Authenticode / catalog signing | No sibling package yet (unlike `macho/codesign`). |