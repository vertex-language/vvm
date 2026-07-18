# objectfile

`github.com/vertex-language/vvm/objectfile`

Assembles relocatable object files from raw section bytes, symbol definitions,
and relocation records. Supports ELF64 (Linux, \*BSD, freestanding), COFF
(Windows), Mach-O (Darwin), and raw flat binary. No external dependencies —
only the Go standard library.

---

## Installation

```sh
go get github.com/vertex-language/vvm/objectfile
```

---

## Import paths

Import whichever format sub-package matches your target. There is no
top-level `objectfile` package to import — each format is fully independent:

```go
import "github.com/vertex-language/vvm/objectfile/elf"    // Linux, *BSD, freestanding
import "github.com/vertex-language/vvm/objectfile/coff"   // Windows
import "github.com/vertex-language/vvm/objectfile/macho"  // Darwin
import "github.com/vertex-language/vvm/objectfile/flat"   // raw binary, no header
```

---

## Design: no shared types

Every format package declares its **own** `Target`/`Arch`/`OS`, `Section`,
`Symbol`, `Reloc`, and `RelocKind` — sized to exactly what that format's
binary layout needs. `elf.Reloc.Addend` writes straight into `r_addend`;
`coff.Reloc.Addend` and `macho.Reloc.Addend` get patched into `Code` before
writing and the on-disk record carries zero. These are genuinely different
runtime behaviors, so they get genuinely different types — a single shared
`Reloc` type flattens that difference into a doc comment reminding you which
format you're currently thinking about, which is worse.

Two consequences worth knowing before you use this package:

- **`elf.Section` and `coff.Section` are not interchangeable**, even though
  they look similar. Code written against one does not compile against the
  other without translation.
- **There is no cross-package `Builder` interface.** `AddSection` takes a
  different concrete `Section` type in every package, so nothing can satisfy
  "the" builder interface generically — you pick the package for your target
  OS and use its types directly. See [Selecting a format at
  runtime](#selecting-a-format-at-runtime) below for the idiom this package
  expects instead.

The cost is accepted, real duplication — four packages each define their own
`SectionKind`, `Binding`, `SymbolKind`, string-table builder, and alignment
helpers. That's fine: each package stays readable top to bottom with no other
package open.

---

## Package layout

```
objectfile/
├── elf/
│   ├── target.go   elf.Arch, elf.OS, elf.Target, predefined targets
│   ├── types.go    elf.Section, elf.Symbol, elf.Reloc, elf.RelocKind
│   ├── elf.go      elf.File, NewFile, options, Serialize/WriteTo, relocType
│   ├── strtab.go   internal string-table builder (SHT_STRTAB)
│   └── write.go    the byte-level ELF64/ELF32 encoder
│
├── coff/
│   ├── target.go   coff.Arch, coff.OS, coff.Target, predefined targets
│   ├── types.go    coff.Section, coff.Symbol, coff.Reloc, coff.RelocKind
│   ├── coff.go     coff.File, NewFile, options, Serialize/WriteTo, relocType
│   ├── strtab.go   internal string-table builder (COFF string table)
│   └── write.go    the byte-level COFF encoder
│
├── macho/
│   ├── target.go   macho.Arch, macho.OS, macho.Target, predefined targets
│   ├── types.go    macho.Section, macho.Symbol, macho.Reloc, macho.RelocKind
│   ├── macho.go    macho.File, NewFile, options, Serialize/WriteTo
│   ├── strtab.go   internal string-table builder (LC_SYMTAB strings)
│   └── write.go    the byte-level Mach-O MH_OBJECT encoder
│
└── flat/
    ├── types.go    flat.Section, flat.Symbol (accepted, unused)
    ├── flat.go     flat.File, NewFile, SetBaseAddress, Serialize/WriteTo
    └── write.go    concatenates section bytes with tail-padding
```

`flat/` has no `target.go` (nothing in a flat image's byte layout varies by
arch or OS) and no `strtab.go` (no symbol table, so nothing to intern).

Nothing in any of these four packages imports another one of the four.

---

## Core concepts

Each package below has its own version of these; the shapes are similar by
convention, not by shared definition.

### Target *(elf, coff, macho — not flat)*

An `(Arch, OS)` pair that tells the package which machine encoding, section
names, and relocation type codes to use.

```go
elf.TargetLinuxAMD64
elf.TargetLinuxARM64
elf.TargetLinuxRISCV64
elf.TargetLinuxX86
elf.TargetFreestandingAMD64
elf.TargetFreestandingARM64
elf.TargetFreestandingRISCV64
elf.TargetFreestandingX86

coff.TargetWindowsAMD64
coff.TargetWindowsARM64

macho.TargetDarwinAMD64
macho.TargetDarwinARM64

// Or build your own within a package:
t := elf.Target{Arch: elf.ArchARM64, OS: elf.OSLinux}
```

`flat.NewFile()` takes no target — a flat binary is just concatenated,
pre-resolved bytes, so there's no machine encoding to select.

### Section

The fundamental unit of content, handed to `File.AddSection`. The format
package maps `Kind` to the correct platform-specific section name and flags
— you never write `.text` or `__TEXT,__text` yourself unless using
`SectionCustom`.

```go
type Section struct {
    Kind    SectionKind  // SectionText, SectionData, SectionROData, SectionBSS, …
    Custom  string       // non-empty only when Kind == SectionCustom
    Align   uint32       // alignment in bytes; 0 = format default for Kind
    Code    []byte       // raw bytes; nil/empty for BSS and zero-fill sections
    VSize   uint64       // virtual size; for BSS may exceed len(Code); 0 = len(Code)
    Symbols []Symbol
    Relocs  []Reloc      // not present on flat.Section — see flat below
    Flags   SectionFlags // FlagLinkOnce, FlagNoDeadStrip
}
```

`flat.Section` has no `Relocs` field at all — flat binary forbids
relocations outright, so a flat section carrying one is not a representable
value, rather than a runtime check inside `WriteTo`.

### Symbol

```go
type Symbol struct {
    Name    string
    Offset  uint32     // byte offset from start of Section.Code
    Size    uint32     // 0 = unknown / not specified
    Binding Binding     // BindingLocal, BindingGlobal, BindingWeak
    Kind    SymbolKind  // SymFunc, SymData, SymSection
}
```

`coff.Symbol` additionally has `DLLExport bool` — set it to have a
`/EXPORT:<name>` linker directive emitted into a synthetic `.drectve`
section at `Serialize` time. No other package has this field.

### Reloc

```go
type Reloc struct {
    Offset uint32    // byte offset within Section.Code where the fixup applies
    Symbol string     // target symbol name; need not be defined in this object
    Kind   RelocKind
    Addend int64      // encoding is format-driven — see each package's section below
}
```

Set `Reloc.Addend` to the logical addend and let the format package decide
how it's encoded:

- **ELF** writes it straight into `r_addend`; `Code` is never touched.
- **COFF** and **Mach-O** patch it into `Code` at `Reloc.Offset` before
  writing, and the on-disk relocation record carries zero.

`flat.Section` has no `Reloc` type at all, matching its lack of a `Relocs`
field.

---

## Format packages

### `elf` — ELF64/ELF32 relocatable object

Supports `TargetLinux*` and `TargetFreestanding*`. Produces `ELFCLASS64`
little-endian `ET_REL` objects for AMD64, ARM64, and RISC-V64; `ArchX86`
produces `ELFCLASS32`.

```go
f := elf.NewFile(elf.TargetLinuxAMD64)

// Options — set before the first AddSection call.
f.SetOSABI(elf.OSABI_Linux)   // default: OSABI_None
                               // also: OSABI_FreeBSD, OSABI_OpenBSD, OSABI_Standalone
f.EnableDWARF(true)           // reserved; not yet implemented in build64/build32
f.EnableGNUStack(false)       // omit .note.GNU-stack (default: true)

f.AddSection(sec)
b, err := f.Serialize()
```

Relocation sections use `SHT_RELA` (explicit addends). `FlagLinkOnce` emits
an `SHT_GROUP` COMDAT group keyed on the section's first global or weak
symbol.

### `coff` — COFF relocatable object

Supports `TargetWindowsAMD64` and `TargetWindowsARM64`. Emits a raw `.obj`
with no MS-DOS stub and no Optional Header.

```go
f := coff.NewFile(coff.TargetWindowsAMD64)

// Options — set before the first AddSection call.
f.SetSubsystem(coff.SubsystemConsole)  // default
                                         // also: SubsystemUnknown, SubsystemNative,
                                         //       SubsystemWindows, SubsystemEFI,
                                         //       SubsystemBootApp

f.AddSection(sec)
b, err := f.Serialize()
```

COFF uses implicit addends — `Serialize` patches `Reloc.Addend` into `Code`
before writing and records zero in the relocation table entry.
`FlagLinkOnce` marks the section `IMAGE_SCN_LNK_COMDAT` with an
`IMAGE_COMDAT_SELECT_ANY` auxiliary record, keyed on the section's first
`BindingGlobal` symbol, whose name also becomes the section header name.
`Symbol.DLLExport = true` causes a synthetic `.drectve` section carrying
`/EXPORT:<name>` to be appended.

### `macho` — Mach-O MH_OBJECT

Supports `TargetDarwinAMD64` and `TargetDarwinARM64`. Produces 64-bit
little-endian `MH_OBJECT` files.

```go
f := macho.NewFile(macho.TargetDarwinARM64)

// Options — set before the first AddSection call.
f.SetMinOS(macho.MacOS, 14, 0)  // emit LC_BUILD_VERSION (default: omitted)
                                 // platform: MacOS, IOS, TVOS, WatchOS, VisionOS
f.EnableCodesignReserve(true)   // reserved; not yet implemented in build

f.AddSection(sec)
b, err := f.Serialize()
```

Mach-O uses implicit addends. `FlagLinkOnce` marks every global/weak symbol
in the section `N_WEAK_DEF`. `SectionCustom` takes a `"segment,section"`
name (e.g. `"__DATA,__objc_classlist"`); `Serialize` returns an error if the
format is malformed (missing comma).

### `flat` — raw binary

No header, no symbol table, no relocation records. `flat.Section` has no
`Relocs` field, so a flat section carrying one is not constructible.
Symbols are accepted on `Section.Symbols` for call-site symmetry with the
other packages but are silently discarded — flat binary emits no symbol
table.

```go
f := flat.NewFile()
f.SetBaseAddress(0x7C00)  // default: 0x0000 — informational only, does not alter layout

f.AddSection(sec)
b, err := f.Serialize()
```

`SectionBSS` emits `VSize` zero bytes directly into the output — unlike the
other three formats, which reserve the size in a header and emit no file
bytes, flat binary has no loader to do that zero-filling for it.

---

## Usage examples

### Simple function — ELF (x86-64)

```go
package main

import (
    "os"

    "github.com/vertex-language/vvm/objectfile/elf"
)

func main() {
    code := []byte{
        0x48, 0xc7, 0xc0, 0x2a, 0x00, 0x00, 0x00, // mov rax, 42
        0xc3,                                       // ret
    }

    f := elf.NewFile(elf.TargetLinuxAMD64)
    f.AddSection(elf.Section{
        Kind:  elf.SectionText,
        Align: 16,
        Code:  code,
        Symbols: []elf.Symbol{
            {
                Name:    "answer",
                Offset:  0,
                Size:    uint32(len(code)),
                Binding: elf.BindingGlobal,
                Kind:    elf.SymFunc,
            },
        },
    })

    b, err := f.Serialize()
    if err != nil {
        panic(err)
    }
    os.WriteFile("answer.o", b, 0644)
}
```

### Calling an external symbol — PC-relative relocation (ELF)

```go
// Emits: call puts; ret
code := []byte{
    0xe8, 0x00, 0x00, 0x00, 0x00, // CALL rel32  ← relocation placeholder
    0xc3,                          // RET
}

f := elf.NewFile(elf.TargetLinuxAMD64)
f.AddSection(elf.Section{
    Kind:  elf.SectionText,
    Align: 16,
    Code:  code,
    Symbols: []elf.Symbol{
        {Name: "greet", Offset: 0, Size: uint32(len(code)),
            Binding: elf.BindingGlobal, Kind: elf.SymFunc},
    },
    Relocs: []elf.Reloc{
        {
            Offset: 1,             // first byte of the rel32 field
            Symbol: "puts",
            Kind:   elf.RelocPCRel32,
            Addend: -4,            // ELF RELA: displacement relative to end of insn
        },
    },
})
```

### COMDAT — one copy across translation units (ELF)

```go
f := elf.NewFile(elf.TargetLinuxAMD64)
f.AddSection(elf.Section{
    Kind:  elf.SectionText,
    Align: 16,
    Code:  inlineBytes,
    Flags: elf.FlagLinkOnce,
    Symbols: []elf.Symbol{
        {Name: "inline_max", Offset: 0, Size: uint32(len(inlineBytes)),
            Binding: elf.BindingGlobal, Kind: elf.SymFunc},
    },
})
```

### DLL export (COFF / Windows)

```go
f := coff.NewFile(coff.TargetWindowsAMD64)
f.AddSection(coff.Section{
    Kind:  coff.SectionText,
    Align: 16,
    Code:  fnBytes,
    Symbols: []coff.Symbol{
        {Name: "MyExport", Offset: 0, Size: uint32(len(fnBytes)),
            Binding: coff.BindingGlobal, Kind: coff.SymFunc,
            DLLExport: true},
    },
})
```

### Constructor registration and custom section (Mach-O)

```go
f := macho.NewFile(macho.TargetDarwinARM64)
f.SetMinOS(macho.MacOS, 14, 0)

// constructor pointer — dyld calls my_init before main
f.AddSection(macho.Section{
    Kind:  macho.SectionInitArray,
    Align: 8,
    Code:  make([]byte, 8),
    Relocs: []macho.Reloc{
        {Offset: 0, Symbol: "my_init", Kind: macho.RelocAbs64},
    },
})

// ObjC class list
f.AddSection(macho.Section{
    Kind:   macho.SectionCustom,
    Custom: "__DATA,__objc_classlist",
    Align:  8,
    Code:   classListBytes,
    Relocs: []macho.Reloc{
        {Offset: 0, Symbol: "_OBJC_CLASS_$_MyClass", Kind: macho.RelocAbs64},
    },
})
```

### Raw flat binary — x86 real-mode boot sector

```go
f := flat.NewFile()
f.SetBaseAddress(0x7C00)

bootsector := buildBootSector() // all references already encoded in the bytes
bootsector[510], bootsector[511] = 0x55, 0xAA

f.AddSection(flat.Section{
    Kind:  flat.SectionText,
    Align: 1,
    Code:  bootsector,
})

b, _ := f.Serialize()
os.WriteFile("boot.bin", b, 0644)
```

---

## Selecting a format at runtime

There's no shared `Target` or `Builder` type to switch on generically —
`elf.Section`, `coff.Section`, and `macho.Section` are different types, so
whatever builds your sections already has to know which package it's
targeting. A typical caller looks like this: pick the package first, then
build that package's own `Section` values.

```go
func writeObject(os string, arch string) ([]byte, error) {
    switch os {
    case "linux", "freestanding":
        f := elf.NewFile(elfTargetFor(arch))
        f.AddSection(buildELFSections(arch)...)
        return f.Serialize()
    case "windows":
        f := coff.NewFile(coffTargetFor(arch))
        f.AddSection(buildCOFFSections(arch)...)
        return f.Serialize()
    case "darwin":
        f := macho.NewFile(machoTargetFor(arch))
        f.AddSection(buildMachOSections(arch)...)
        return f.Serialize()
    default:
        return nil, fmt.Errorf("unsupported OS %q", os)
    }
}
```

The planned `link/` package is where this per-format adaptation is meant to
live for real: one small adapter per `(arch, format)` pair, each translating
an architecture-specific `Section` into the target format's own `Section`
type. It's the only package allowed to import more than one of `elf`,
`coff`, `macho`, and `flat` at once.

---

## Section kind mapping

| `SectionKind`       | ELF              | COFF                                    | Mach-O                                             |
|---------------------|------------------|------------------------------------------|-----------------------------------------------------|
| `SectionText`       | `.text`          | `.text`                                  | `__TEXT,__text`                                     |
| `SectionData`       | `.data`          | `.data`                                  | `__DATA,__data`                                      |
| `SectionROData`     | `.rodata`        | `.rdata`                                 | `__TEXT,__const`                                     |
| `SectionBSS`        | `.bss`           | `.bss`                                   | `__DATA,__bss`                                        |
| `SectionUnwind`     | `.eh_frame`      | `.pdata` (pair with `SectionCustom(".xdata")`) | `__TEXT,__unwind_info` (pair with `SectionCustom("__TEXT,__eh_frame")`) |
| `SectionInitArray`  | `.init_array`    | `.CRT$XCU`                               | `__DATA,__mod_init_func`                             |
| `SectionFiniArray`  | `.fini_array`    | `.CRT$XTZ`                               | `__DATA,__mod_term_func`                             |
| `SectionTLS` (init) | `.tdata`         | `.tls`                                   | `__DATA,__thread_data`                               |
| `SectionTLS` (zero) | `.tbss`          | `.tls$ZZZ`                               | `__DATA,__thread_bss`                                |
| `SectionCustom`     | as given         | as given                                 | as given (`"seg,sect"`)                              |

`SectionBSS` and zero-fill `SectionTLS` carry no file bytes in ELF, COFF, or
Mach-O — only the virtual size is written to the section header. `flat` is
the exception: `SectionBSS` there emits `VSize` zero bytes into the file
itself, since flat has no header to carry a reservation. `flat` also has no
concept of a section *name* — `Kind` there only decides whether the section
is BSS-like (zero-filled) or emits `Code` as-is.

---

## Relocation kind mapping

### ELF — explicit addends (`SHT_RELA`)

`Reloc.Addend` flows directly into `r_addend`. `Code` is never patched.

| `RelocKind`       | AMD64                  | ARM64                                  | i386             | RISC-V64              |
|-------------------|------------------------|------------------------------------------|------------------|--------------------------|
| `RelocAbs64`      | `R_X86_64_64`          | `R_AARCH64_ABS64`                        | —                | `R_RISCV_64`             |
| `RelocAbs32`      | `R_X86_64_32`          | `R_AARCH64_ABS32`                        | `R_386_32`       | `R_RISCV_32`             |
| `RelocPCRel32`    | `R_X86_64_PC32`        | —                                          | `R_386_PC32`     | —                        |
| `RelocPLT32`      | `R_X86_64_PLT32`       | —                                          | —                | —                        |
| `RelocGOTLoad`    | `R_X86_64_GOTPCREL`    | —                                          | `R_386_GOT32`    | —                        |
| `RelocPCRel26`    | —                      | `R_AARCH64_CALL26`                       | —                | —                        |
| `RelocADRPage21`  | —                      | `R_AARCH64_ADR_PREL_PG_HI21`             | —                | —                        |
| `RelocAddOff12`   | —                      | `R_AARCH64_ADD_ABS_LO12_NC`             | —                | —                        |
| `RelocGOTPage21`  | —                      | `R_AARCH64_ADR_GOT_PAGE`                | —                | —                        |
| `RelocGOTOff12`   | —                      | `R_AARCH64_LD64_GOT_LO12_NC`           | —                | —                        |
| `RelocRISCVCall`  | —                      | —                                          | —                | `R_RISCV_CALL_PLT`      |
| `RelocRISCVHI20`  | —                      | —                                          | —                | `R_RISCV_HI20`           |
| `RelocRISCVLO12I` | —                      | —                                          | —                | `R_RISCV_LO12_I`         |
| `RelocRISCVLO12S` | —                      | —                                          | —                | `R_RISCV_LO12_S`         |
| `RelocTLSGD`      | `R_X86_64_TLSGD`       | `R_AARCH64_TLSGD_ADR_PAGE21`            | —                | `R_RISCV_TLS_GD_HI20`   |
| `RelocTLSIE`      | `R_X86_64_GOTTPOFF`    | `R_AARCH64_TLSIE_ADR_GOTTPREL_PAGE21`  | —                | `R_RISCV_TLS_GOT_HI20`  |
| `RelocTLSLE`      | `R_X86_64_TPOFF32`     | `R_AARCH64_TLSLE_ADD_TPREL_LO12_NC`    | —                | `R_RISCV_TPREL_HI20`    |

### COFF — implicit addends

`Serialize` patches `Reloc.Addend` into `Code` before writing; the on-disk
record carries zero. Note: for AMD64 `RelocPCRel32`, the encoder internally
adds 4 to the addend before patching, to compensate for COFF's `REL32`
definition already subtracting 4 (`P+4`) — callers still just set the
logical addend and don't need to account for this themselves.

| `RelocKind`      | AMD64                      | ARM64                      |
|------------------|----------------------------|------------------------------|
| `RelocAbs64`     | `IMAGE_REL_AMD64_ADDR64`   | `IMAGE_REL_ARM64_ADDR64`   |
| `RelocAbs32`     | `IMAGE_REL_AMD64_ADDR32`   | `IMAGE_REL_ARM64_ADDR32`   |
| `RelocPCRel32`   | `IMAGE_REL_AMD64_REL32`    | —                            |
| `RelocPLT32`     | `IMAGE_REL_AMD64_REL32`    | `IMAGE_REL_ARM64_BRANCH26` |
| `RelocPCRel26`   | —                            | `IMAGE_REL_ARM64_BRANCH26` |
| `RelocIAT`       | `IMAGE_REL_AMD64_ADDR32NB` | `IMAGE_REL_ARM64_ADDR32NB` |
| `RelocAddr32NB`  | `IMAGE_REL_AMD64_ADDR32NB` | `IMAGE_REL_ARM64_ADDR32NB` |
| `RelocTLSIE`     | `IMAGE_REL_AMD64_SECREL`   | `IMAGE_REL_ARM64_SECREL`   |

### Mach-O — implicit addends

`Serialize` patches `Code` in place; relocation entries carry zero
(zero-addend relocations are skipped entirely as an optimization — unlike
COFF's encoder, which patches every relocation regardless of addend value).

| `RelocKind`      | AMD64                    | ARM64                            |
|------------------|--------------------------|-------------------------------------|
| `RelocAbs64`     | `X86_64_RELOC_UNSIGNED`  | `ARM64_RELOC_UNSIGNED`             |
| `RelocPCRel32`   | `X86_64_RELOC_BRANCH`    | —                                    |
| `RelocGOTLoad`   | `X86_64_RELOC_GOT_LOAD`  | —                                    |
| `RelocPCRel26`   | —                          | `ARM64_RELOC_BRANCH26`             |
| `RelocADRPage21` | —                          | `ARM64_RELOC_PAGE21`               |
| `RelocAddOff12`  | —                          | `ARM64_RELOC_PAGEOFF12`            |
| `RelocGOTPage21` | —                          | `ARM64_RELOC_GOT_LOAD_PAGE21`      |
| `RelocGOTOff12`  | —                          | `ARM64_RELOC_GOT_LOAD_PAGEOFF12`   |
| `RelocTLSGD`     | `X86_64_RELOC_TLV`       | `ARM64_RELOC_TLVP_LOAD_PAGE21`     |

---

## Design notes

**No external dependencies.** The entire module imports nothing outside the
Go standard library.

**Format, not architecture, drives the sub-package split.** `elf` covers
every Linux and freestanding target across all four supported architectures.
`coff` covers Windows. `macho` covers Darwin. Architecture differences
(`e_machine`/`cpu_type`, relocation encodings, alignment defaults) are
internal parameters within each format package, not separate packages.

**No shared `Section`/`Reloc`/`Builder` types, on purpose.** Each package's
`Reloc.Addend` means exactly what that format's binary layout says it means
— `r_addend` for ELF, an implicit patch into `Code` for COFF and Mach-O —
with no doc comment required to remember which convention currently applies.
The cost is real, accepted duplication across four packages; the
alternative is a shared type whose fields mean different things depending
on which format you're currently thinking about.

**`flat` is not a lesser-featured version of the other three.** It has its
own `Section` type with no `Relocs` field, because flat binary forbids
relocations as a matter of format, not policy — there's nothing to check at
runtime because there's nothing to construct.

**Options are always set before the first `AddSection` call.** OSABI,
subsystem, min-OS version, and base address are all read at `Serialize`
time from whatever the `File`'s fields hold then, not snapshotted per
section.