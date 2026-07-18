# objectwriter

`github.com/vertex-language/vvm/objectwriter`

The bridge between `object/<arch>`'s generic sections and `objectfile/<format>`'s container-specific types. One package per architecture; each package holds one file per valid `(arch, format)` cell, and each file does exactly three mechanical things: map `SectionKind`, map `RelocKind`, forward `Symbol`/`Addend` unchanged.

---

## Import paths

Import whichever arch package matches your target. There is no top-level `objectwriter` package — each arch is fully independent:

```go
import objw_x86 "github.com/vertex-language/vvm/objectwriter/x86"
import objw_x86_64 "github.com/vertex-language/vvm/objectwriter/x86_64"
import objw_arm "github.com/vertex-language/vvm/objectwriter/arm"
import objw_aarch64 "github.com/vertex-language/vvm/objectwriter/aarch64"
```

---

## Package layout

```
objectwriter/
├── x86/                package x86
│   ├── elf.go            object/x86      -> objectfile/elf   (ArchX86, ELFCLASS32)
│   └── flat.go           object/x86      -> objectfile/flat
│
├── x86_64/              package x86_64
│   ├── elf.go             object/x86_64   -> objectfile/elf   (ArchAMD64)
│   ├── coff.go             object/x86_64   -> objectfile/coff  (TargetWindowsAMD64)
│   ├── macho.go            object/x86_64   -> objectfile/macho (TargetDarwinAMD64)
│   └── flat.go             object/x86_64   -> objectfile/flat
│
├── arm/                 package arm
│   └── flat.go            object/arm      -> objectfile/flat  (elf.go withheld — see below)
│
└── aarch64/             package aarch64
    ├── elf.go              object/aarch64  -> objectfile/elf   (ArchARM64)
    ├── coff.go              object/aarch64  -> objectfile/coff  (TargetWindowsARM64)
    ├── macho.go             object/aarch64  -> objectfile/macho (TargetDarwinARM64)
    └── flat.go              object/aarch64  -> objectfile/flat
```

Each package is named to match its folder (`package x86_64`, `package aarch64`, ...) rather than sharing one name, so import sites read naturally with a short alias. Alongside `linker`, this is the only tree permitted to import both `object/<arch>` and `objectfile/<format>` in the same file — everywhere else stays on one side of that boundary.

---

## Coverage matrix

| arch | elf | coff | macho | flat |
|---|---|---|---|---|
| `x86` | ✅ | — (no 32-bit COFF target) | — (Mach-O is 64-bit only) | ✅ |
| `x86_64` | ✅ | ✅ | ✅ (no plain RIP-rel data reloc) | ✅ |
| `arm` | — (blocked, see below) | — | — | ✅ |
| `aarch64` | ✅ (no MOVZ/MOVK reloc kinds yet) | ✅ (same gap) | ✅ (same gap) | ✅ |

---

## Design: the shape every `To<Format>` follows

```go
func ToELF(secs []object.Section, target elf.Target) ([]byte, error)
func ToCOFF(secs []object.Section, target coff.Target) ([]byte, error)
func ToMachO(secs []object.Section, target macho.Target) ([]byte, error)
func ToFlat(secs []object.Section, base uint64) ([]byte, error)
```

Every adapter walks the same three steps per section: map `object.SectionKind` to the target format's section kind, map every `object.RelocKind` on that section, and copy `Symbol`/`Offset`/`Size`/`Addend` straight across. No relocation arithmetic, no layout decisions, no symbol resolution happens in this package — those were already decided by the two packages being bridged (`object/<arch>` chose the relocation shape; `objectfile/<format>` decides how that shape gets encoded).

```go
p, err := x86_64.Lower(m)
if err != nil {
    return err
}
secs := objx86_64.FromProgram(p)

b, err := objw_x86_64.ToELF(secs, elf.TargetLinuxAMD64)
if err != nil {
    return err
}
os.WriteFile("add.o", b, 0644)
```

### Section conversion

```go
func convertSectionELF(s object.Section) (elf.Section, error) {
    kind, err := sectionKindELF(s.Kind)
    if err != nil {
        return elf.Section{}, fmt.Errorf("objectwriter/x86: section %q: %w", s.Name, err)
    }
    es := elf.Section{Kind: kind, Align: s.Align}
    if isBSSLike(s.Kind) {
        es.VSize = uint64(s.Size) // BSS/TLS-BSS: reserve, no bytes
    } else {
        es.Code = s.Code
    }
    for _, sym := range s.Symbols {
        es.Symbols = append(es.Symbols, elf.Symbol{
            Name: sym.Name, Offset: sym.Offset, Size: sym.Size,
            Binding: bindingELF(sym.Export), Kind: symKindELF(s.Kind),
        })
    }
    for _, r := range s.Relocs {
        rk, err := relocKindELF(r.Kind)
        if err != nil {
            return elf.Section{}, fmt.Errorf("objectwriter/x86: section %q: %w", s.Name, err)
        }
        es.Relocs = append(es.Relocs, elf.Reloc{
            Offset: r.Offset, Symbol: r.Symbol, Kind: rk, Addend: r.Addend,
        })
    }
    return es, nil
}
```

`SectionTLSData`/`SectionTLSBSS` both fold onto the target format's single `SectionTLS` kind — the format packages don't distinguish TLS-init from TLS-zero the way `object` does; they use `VSize > len(Code)` for the zero-fill tail instead. `export` becomes `BindingGlobal` vs. `BindingLocal`; a section's `Kind` alone decides `SymFunc` vs. `SymData` (only `SectionText` produces `SymFunc`).

### Unmapped relocations fail loudly

When a `(arch, format)` reloc has no counterpart on the far side, the adapter returns an explicit error rather than approximating silently:

```go
func relocKindCOFF(k object.RelocKind) (coff.RelocKind, error) {
    switch k {
    case object.RelocCall26, object.RelocJump26:
        return coff.RelocPCRel26, nil
    case object.RelocAbs64:
        return coff.RelocAbs64, nil
    case object.RelocMovzG3, object.RelocMovkG2, object.RelocMovkG1, object.RelocMovkG0:
        return 0, fmt.Errorf(
            "coff/arm64 has no MOVW/MOVK-style relocation kind yet in objectfile/coff")
    }
    return 0, fmt.Errorf("unmapped reloc kind %v for coff/arm64", k)
}
```

The one **documented exception** is AArch64 `Call26`/`Jump26` collapsing onto a single relocation code per format (`elf.RelocPCRel26`, `coff.RelocPCRel26`, `macho.RelocPCRel26`) — a knowing, arithmetic-identical approximation, not a silent gap. `B` and `BL` compute the same `(S+A-P)>>2` into the same 26-bit field; the two ELF relocation numbers only diverge in PLT-stub-insertion semantics for undefined externals, which doesn't affect locally-resolved branches. Each `elf.go`/`coff.go`/`macho.go` in `aarch64/` documents this explicitly at the top of the file.

### `flat` adapters reject relocations outright

`flat.Section` has no `Relocs` field — flat binary forbids relocations by construction — so every `ToFlat` checks and rejects up front rather than silently dropping:

```go
func ToFlat(secs []object.Section, base uint64) ([]byte, error) {
    f := flat.NewFile()
    f.SetBaseAddress(base)
    for _, s := range secs {
        if len(s.Relocs) > 0 {
            return nil, fmt.Errorf(
                "objectwriter/x86: section %q: flat output cannot carry relocations (%d found); "+
                    "resolve them first or target elf instead",
                s.Name, len(s.Relocs))
        }
        fs := flat.Section{Align: s.Align, Kind: flatSectionKind(s.Kind)}
        if isBSSLike(s.Kind) {
            fs.VSize = uint64(s.Size)
        } else {
            fs.Code = s.Code
        }
        f.AddSection(fs)
    }
    return f.Serialize()
}
```

`flat.Section.Symbols` is accepted for call-site symmetry with the other three formats but never populated here — the flat encoder discards it anyway.

---

## Why `arm` has no `elf.go`

Two real gaps in `objectfile/elf` block it, not an oversight in this package:

1. `elf.Arch` has no `ArchARM` entry — only `AMD64`/`ARM64`/`RISCV64`/`X86` — so there's no `e_machine` value to select for AArch32.
2. `objectfile/elf`'s encoder hardcodes little-endian output throughout, but `object/arm`'s `Program.Arch` carries an explicit big-endian variant (`armeb`) that a real target needs honored.

Until `objectfile/elf` grows both, ARM32 can only reach `flat` here — a dead end for anything needing external symbol resolution, since flat forbids relocations outright. This is documented in `arm/flat.go`'s package comment, and the same file's `ToFlat` error message points back to it.

---

## Why some formats are missing per arch

Not every `(arch, format)` cell is a gap to fill — some don't exist by construction:

- **`x86` has no `coff.go`** — there is no 32-bit Windows COFF target in `objectfile/coff`.
- **`x86` has no `macho.go`** — Mach-O is 64-bit only; `objectfile/macho` has no i386 target.
- **`x86_64`'s `macho.go` has no plain RIP-relative data relocation** — see that file's package comment for the specific gap.

---

## Design notes

**Three mechanical steps, nothing more.** `objectwriter` never decides relocation arithmetic (that's `lower/<arch>`'s job, already baked into `object.RelocKind`) and never decides byte-level encoding (that's `objectfile/<format>`'s job). If a conversion ever needs to *compute* something rather than *look up* a mapping, that logic belongs in one of the two packages being bridged, not here.

**Fail loudly on gaps, same culture as `vir.Verify`.** An unmapped `RelocKind` is a compile-time-discoverable limitation, not a corrupted `.o` file waiting to happen. Every adapter returns an error naming exactly which kind has no counterpart, rather than picking the "closest" existing code.

**One documented approximation, everywhere else exact.** The AArch64 `Call26`/`Jump26` collapse is the only place this package knowingly maps two logically distinct things onto one output code — and it's called out in the file, not left implicit.

**`linker` is the only other package allowed to straddle `object`/`objectfile`.** Unlike `objectwriter`, which never looks past a single translation unit, `linker` is where multi-object symbol resolution happens — a `Reloc.Symbol` that isn't defined locally passes straight through `objectwriter` untouched, into `objectfile`'s own undefined-external handling (COFF's `externalRefs`, ELF's `externalSymbols`, Mach-O's `externalSymbols`).