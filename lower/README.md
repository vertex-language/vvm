# `lower` — Package Tree

Machine-code generation: one package per target architecture, each converting a verified `vir.Module` into that architecture's own `Program` — instruction bytes, symbols, and unresolved fixups. This is the only place in the repository that knows real instruction encodings; nothing here understands object-file formats.

## Layout

```
lower/
├── x86/       // 32-bit x86 (IA-32), cdecl, non-PIC
├── x86_64/    // 64-bit x86, System V AMD64 ABI, PIC-clean small model
├── arm/       // 32-bit ARM (A32), little/big-endian
└── aarch64/   // 64-bit ARM (A64), little/big-endian data
```

Each arch package is self-contained and follows the same internal shape:

| File | Role |
|---|---|
| `program.go` | Package doc (ABI, code model, coverage) + `Program`, `Func`, `Global`, `Fixup`, `FixupKind` |
| `layout.go` | Sizes, alignments, and struct field offsets for the arch's ABI |
| `minst.go` | The pre-encoding pseudo-instruction form: registers, immediates, symbols, value slots |
| `isel.go` | `Lower(*vir.Module) (*Program, error)` — instruction selection into a `minst` stream |
| `frame.go` | Stack frame layout: value-slot homes, saved registers, incoming-argument area |
| `regalloc.go` | Register allocation — currently spill-everything |
| `encode.go` | `minst` stream → final machine bytes, fixup list, label patching |

Each package imports only `ir/vir`. No package under `lower/` imports `object`, `objectfile`, or `objectwriter` — a real dependency boundary, not just convention.

## `Program`: the contract every arch honors

Every `Program` is exactly bytes, symbols, and fixups — nothing else:

```go
type Program struct {
    Funcs   []Func
    Globals []Global
}

type Func struct {
    Name   string
    Code   []byte
    Align  uint32
    Export bool
    Fixups []Fixup
}

type Global struct {
    Name   string
    Data   []byte // nil for zero (BSS-style) storage
    Size   uint32
    Align  uint32
    Export bool
    TLS    bool
    Fixups []Fixup
}

type Fixup struct {
    Offset uint32
    Symbol string
    Kind   FixupKind
    Addend int64
}
```

`FixupKind` is each arch's own vocabulary (`x86.FixupPCRel32`/`FixupAbs32`; `x86_64` splits call sites from data references and adds `FixupAbs64`; `arm`/`aarch64` follow the same pattern). The addend is stored both in the `Fixup` and written into the field itself, so REL-style (implicit-addend) and RELA-style (explicit-addend) downstream consumers both work without rewriting bytes.

## The pipeline, stage by stage

**`isel.go`** walks a verified function's blocks in order and selects each `vir.Inst`/`Terminator` into one or more `minst`s, using a small, fixed set of scratch registers (e.g. x86: EAX/ECX/EDX; x86_64: RAX/RCX/RDX plus the argument registers). It also runs a `typeFunc` pre-pass that mirrors `vir.Verify`'s result-type computation for the subset each backend supports — since the input is already verified, this pass can assume lookups succeed and is only reconstructing types, not validating them.

**`frame.go`** assigns every named value a fixed-offset home slot relative to the frame pointer (EBP on x86, RBP on x86_64/ARM/AArch64 as applicable), plus incoming-argument offsets for stack-passed parameters. Slot size and alignment obligations are arch-specific (e.g. x86_64 keeps RSP 16-byte aligned; ARM/AArch64 have their own frame conventions).

**`regalloc.go`** is deliberately the simplest correct baseline: every value already has a slot from `frame.go`, so this pass just rewrites the placeholder slot operands (`oSlot`) into real base+displacement memory operands. A real allocator — linear scan over live ranges, promoting callee-saved registers into the allocatable set — replaces this function later without touching `isel`'s output contract.

**`encode.go`** turns the final `minst` stream into machine bytes: opcode/ModRM/SIB selection (x86 families), fixed-width instruction words (ARM/AArch64), label-to-offset patching for intra-function branches, and fixup emission for anything referencing an external or not-yet-placed symbol.

## Coverage and gaps

Each backend rejects what it doesn't yet lower with an explicit, loud error rather than guessing or silently miscompiling — the same culture as `vir.Verify`. Current gaps are consistent across architectures:

- Floating-point values and operations (no x87/SSE/NEON lowering yet).
- Vector types and operations (tier-gated; awaiting feature-tier tables).
- Saturating arithmetic (`uadd_sat`, etc.) and `bitrev`.
- Inline `asm`.
- Sub-32-bit atomic read-modify-write.
- Integer widths beyond what a backend's registers hold in one piece (e.g. `i64` on x86, `i128` everywhere) — register-pair lowering is future work.
- `byval` struct argument passing on x86_64 (needs the full SysV INTEGER/MEMORY classification algorithm).

Known correctness caveats, also marked at their call sites:

- Narrow signed division (`sdiv`/`srem` on widths below the register width) should trap on `INT_MIN / -1` but currently wraps instead, because the value is computed at the wider register width before truncation.

## What doesn't belong here

Nothing in this tree understands sections, symbol tables, or relocation entries as a container-format concept — that translation is `object/<arch>`'s job. Nothing here decides ELF/COFF/Mach-O byte layout — that's `objectfile/<format>`. And nothing here resolves symbols across multiple compiled objects — that's `linker`. A `lower/<arch>` package's job ends the moment it has produced a `Program`.