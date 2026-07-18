# object

`github.com/vertex-language/vvm/object`

Translates a lowered `<arch>.Program` into a generic, container-agnostic description of sections, symbols, and relocations — one package per architecture, all sharing the same shape. This is the layer that answers "what does a `.o`-file builder need?" once per arch, independent of which container format (ELF/COFF/Mach-O/flat) ends up consuming it.

---

## Import paths

Import whichever arch package matches your target. There is no top-level `object` package — each arch is fully independent, same as `objectfile`'s formats:

```go
import x86 "github.com/vertex-language/vvm/object/x86"
import x86_64 "github.com/vertex-language/vvm/object/x86_64"
import arm "github.com/vertex-language/vvm/object/arm"
import aarch64 "github.com/vertex-language/vvm/object/aarch64"
```

---

## Package layout

```
object/
├── x86/
│   └── object.go   x86.Program -> Section/Symbol/Reloc; FixupKind -> RelocKind (R_386_*)
├── x86_64/
│   └── object.go   x86_64.Program -> Section/Symbol/Reloc; FixupKind -> RelocKind (R_X86_64_*)
├── arm/
│   └── object.go   arm.Program -> Section/Symbol/Reloc; FixupKind -> RelocKind (R_ARM_*)
└── aarch64/
    └── object.go   aarch64.Program -> Section/Symbol/Reloc; FixupKind -> RelocKind (R_AARCH64_*)
```

Each package declares `package object` and is imported with an arch alias at call sites. None imports `objectfile` or `objectwriter`; none imports another arch's `object` package.

---

## Design: no shared Program type, one shared Section shape

Unlike `objectfile` (which gives every format its own `Section`/`Symbol`/`Reloc` because the formats genuinely differ), every `object/<arch>` package declares an **identical** `Section`/`Symbol`/`Reloc` shape — the difference between arches is confined entirely to `RelocKind` and its mapping function:

```go
type Section struct {
    Kind    SectionKind
    Name    string
    Align   uint32
    Size    uint32 // total size; BSS kinds carry no Code, only Size
    Code    []byte
    Symbols []Symbol
    Relocs  []Reloc
}

type Symbol struct {
    Name   string
    Offset uint32
    Size   uint32
    Export bool
}

type Reloc struct {
    Offset uint32
    Symbol string
    Kind   RelocKind
    Addend int64
}

func FromProgram(p *<arch>.Program) []Section
```

The one piece of arch-specific knowledge each package adds is its `FixupKind -> RelocKind` mapping — documented per package as the exact ELF relocation code each kind corresponds to. Everything else — how sections get laid out, how fixups get rebased, how empty sections get dropped — is common logic repeated identically across all four packages.

---

## Core concepts

### `SectionKind`

The same six kinds in every arch package:

```go
const (
    SectionText SectionKind = iota
    SectionData
    SectionROData
    SectionBSS
    SectionTLSData
    SectionTLSBSS
)
```

### `RelocKind` — the one arch-specific piece

Each package's relocation vocabulary maps onto a specific ELF relocation shape:

```go
// object/x86
const (
    RelocPCRel32 RelocKind = iota // R_386_PC32: S+A-P
    RelocAbs32                     // R_386_32:   S+A
)

// object/x86_64
const (
    RelocPLT32 RelocKind = iota   // R_X86_64_PLT32: S+A-P, branch site
    RelocPCRel32                   // R_X86_64_PC32:  S+A-P, data reference
    RelocAbs64                     // R_X86_64_64:    S+A
)

// object/arm
const (
    RelocCall24 RelocKind = iota  // R_ARM_CALL:         BL, (S+A-P)>>2 into imm24
    RelocJump24                    // R_ARM_JUMP24:       B, same arithmetic
    RelocMovwAbs                   // R_ARM_MOVW_ABS_NC:  (S+A)&0xFFFF
    RelocMovtAbs                   // R_ARM_MOVT_ABS:     ((S+A)>>16)&0xFFFF
    RelocAbs32                     // R_ARM_ABS32:        S+A, data words
)

// object/aarch64
const (
    RelocCall26 RelocKind = iota  // R_AARCH64_CALL26
    RelocJump26                    // R_AARCH64_JUMP26
    RelocMovzG3                    // R_AARCH64_MOVW_UABS_G3
    RelocMovkG2                    // R_AARCH64_MOVW_UABS_G2_NC
    RelocMovkG1                    // R_AARCH64_MOVW_UABS_G1_NC
    RelocMovkG0                    // R_AARCH64_MOVW_UABS_G0_NC
    RelocAbs64                     // R_AARCH64_ABS64
)
```

### `FromProgram`

```go
p, err := x86_64.Lower(m)
if err != nil {
    return err
}
secs := objx86_64.FromProgram(p)
```

What it does, in order:

1. **Functions concatenate into one `.text` section.** Each function lands at its own alignment, with arch-native NOP padding filling the gaps (`0x90` on x86/x86_64; the A32/A64 NOP word — serialized in the program's byte order — on arm/aarch64). A `Symbol` is recorded at the function's offset; its fixups are rebased from function-relative to section-relative offsets and translated to `Reloc`s.
2. **Globals route by storage class.** Zero-initialized globals go to `.bss` (`.tbss` for TLS); initialized globals go to `.data` (`.tdata` for TLS), with bytes and fixups copied and rebased the same way as function fixups.
3. **Empty sections are dropped.** Only sections with nonzero size or at least one symbol appear in the returned slice.

```go
for _, s := range secs {
    fmt.Println(s.Name, s.Kind, len(s.Relocs), "relocs")
}
// .text text 2 relocs
// .data data 0 relocs
```

---

## Byte order — handled per arch, not uniformly

`arm` and `aarch64` diverge here because AArch32 and AArch64 diverge for real:

```go
// object/arm — NOP padding follows Program.Arch's byte order
nop := []byte{0x00, 0x00, 0xA0, 0xE1}       // little-endian
if p.Arch.Big() {
    nop = []byte{0xE1, 0xA0, 0x00, 0x00}     // BE-8: instruction bytes swapped
}

// object/aarch64 — always little-endian; A64 instruction words are
// architecturally little-endian in both aarch64 and aarch64_be
nop := []byte{0x1F, 0x20, 0x03, 0xD5}
```

`arm`'s package doc notes that `link` must honor `Program.Arch`'s byte order when applying relocations too, including BE-8's text-swap and `$a`/`$d` mapping-symbol requirements. `aarch64`'s package doc notes the opposite: only 64-bit *data* fields (`RelocAbs64` sites in data sections) follow `Program.Arch`'s byte order downstream — there is no BE-8 step and no mapping-symbol requirement for code.

---

## Usage example — x86-64 end to end

```go
p, err := x86_64.Lower(verifiedModule)
if err != nil {
    return err
}

secs := objx86_64.FromProgram(p)
for _, s := range secs {
    for _, r := range s.Relocs {
        fmt.Printf("%s+0x%x -> %s (%s, addend %d)\n",
            s.Name, r.Offset, r.Symbol, r.Kind, r.Addend)
    }
}
// .text+0xa -> fmt (pcrel32, addend -4)
// .text+0x1a -> printf (plt32, addend -4)
```

These `Section`/`Reloc` values are exactly what `objectwriter/x86_64` consumes next, mapping `object.SectionKind`/`object.RelocKind` onto `elf.SectionKind`/`elf.RelocKind` (or COFF's, or Mach-O's) and forwarding `Symbol`/`Addend` unchanged.

---

## Design notes

**Format-agnostic on purpose.** This package never decides how a section, symbol, or relocation gets serialized into bytes on disk — that's `objectfile/<format>`'s job, and it doesn't happen here at all.

**Never binds arch to format.** Choosing which container format a given arch's sections end up in — and the `SectionKind`/`RelocKind` → format-specific-type mapping that requires — is `objectwriter/<arch>`'s job, not this package's.

**Never resolves symbols across objects.** Every `Reloc.Symbol` passes through untouched, whether or not it's defined in the same `Program`. Cross-object resolution belongs to `linker` alone.

**One package per arch, same shape, on purpose — unlike `objectfile`.** `objectfile` gives every format its own types because the on-disk encodings genuinely differ (explicit vs. implicit addends, different symbol tables entirely). Here, the four arches produce the *same kind* of thing — a generic section list — so sharing the shape and varying only `RelocKind` avoids the duplication `objectfile` accepts for a different, real reason.