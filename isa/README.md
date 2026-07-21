# isa

`github.com/vertex-language/vvm/isa`

Umbrella for the four architecture-description packages that `lower/<arch>` and `isa/<arch>/encoder` build on. There is no code at this level — `isa` is a directory, not a package — just the four sibling packages below, which share a design but have no import relationship to each other:

```
isa/
├── x86/       IA-32 (x86, 32-bit)
├── x86_64/    AMD64 (x86-64, 64-bit)
├── arm/       A32 (32-bit ARM)
└── aarch64/   A64 (64-bit ARM)
```

Each package is the static, data-only description of one instruction set: register identity, condition-code numbering, the bit-packed layout of its addressing-mode encodings, and the opcode↔mnemonic correspondence. None contains control flow of consequence — only declarations, plus (in three of the four) a handful of mechanical reverse-index maps built once in `init()`. Nothing in any of the four encodes or decodes an instruction stream itself; that's `isa/<arch>/encoder`'s job on the way in, and either a disassembler or nothing yet on the way out.

---

## Shared design

**One flat package per architecture**, mirroring the same choice `lower/<arch>` makes one layer up: register tables, condition codes, encoding primitives, and opcode tables live together rather than split into separate packages, because (per each README) splitting bought no independence.

**The test: ISA fact vs. lowering decision.** Every one of the four READMEs states the same dividing line in the same words: a fact belongs in `isa/<arch>` if it would still be true even if the matching `lower/<arch>` were deleted and rewritten from scratch with a completely different register-allocation strategy. A decision *this compiler* makes about how to use those facts — which registers are scratch vs. callee-saved, how stack slots are laid out, which mnemonics an inline-asm table supports — belongs in `lower/<arch>` instead. `isa/x86` is where the split originates; the other three READMEs each point back to it (directly or via `isa/arm`) as the precedent.

**Consumers, not internal structure, decide the shape of a table.** Where a downstream consumer treats a value as a plain byte (`isa/x86_64/encoder`'s `Inst.CC`, `isa/arm/encoder`'s `Inst.CC`), the corresponding condition-code constants are left untyped rather than wrapped in a named type. Where a consumer needs a mechanical reverse index and nothing hand-maintained twice, that index is built once in `init()` (`isa/x86`, `isa/arm`, `isa/aarch64`) rather than duplicated by hand — `isa/x86_64` is the exception, since its tables are already mnemonic-keyed map literals with no opcode→mnemonic direction needed by any current consumer.

**Encoders build on these packages directly; instruction selection does too.** `isa/x86/encoder`, `isa/x86_64/encoder`, `isa/arm/encoder`, and `isa/aarch64/encoder` each consume their matching package for byte emission. `lower/x86`, `lower/x86_64`, and (once written) `lower/arm` and `lower/aarch64` consume the same facts for instruction selection, mapping vir's string-keyed register table onto the physical registers named here rather than re-declaring register identity themselves.

---

## Package layout

The four packages settle on the same four-file split, though not every file exists in every package in the same form:

| | `registers.go` | `condcodes.go` | `encoding.go` | `opcodes.go` |
|---|---|---|---|---|
| **x86** | `Reg` (REAX–REDI), `RNone`, width-indexed `reg32`/`reg16`/`reg8` | `Cond*` (untyped), `condName`, `CondName()` | `PackModRM`/`UnpackModRM`, `PackSIB`/`UnpackSIB`, `ScaleBits`, legacy prefix bytes | `AluOp`/`ShiftOp`/`Group3Op`, `init()`-built reverse indexes |
| **x86_64** | `Reg` (RAX–R15), `RNone`, width-indexed `Name64`/`Name32`/`Name16`/`Name8` | `Cond*` (untyped), `CondMnemonics`, `CondName` | `PackModRM`/`UnpackModRM`, `PackSIB`, `PackREX`, `HiBit`/`LoBits`, special-value constants | `ALUOpcodes`/`ShiftExt`/`Grp3Ext`/`Grp5Ext` as map literals, plus fixed `Op*` constants |
| **arm** | `Reg` (R0–R10, FP/IP/SP/LR/PC), `RNone`, single `regName` | `Cond*` (untyped), `condName`, `CondName()` | `PackImm12`/`UnpackImm12`, `SplitImm16`, `PCBias` | `DPOp`/`ShiftOp`, `init()`-built reverse indexes, fixed `Base*` words |
| **aarch64** | `Reg` (X0–X30), shared SP/ZR encoding-31, FP/LR/IP0/IP1/PR aliases, width-indexed `XNames`/`WNames` | `Cond*` (untyped), `CondNames`, `CondMnemonics` (`init()`-built), `Invert()` | `Sf`/`Idx64`/`SizeBits`, `PackBFM`, `PackPair` | `DPImmOpcodes`/`DPRegOpcodes`/`DP2Opcodes`/`DP1Opcodes`, `LdClasses`/`StClasses`, fixed `Op*` words |

---

## Where the four diverge

**Register width.** `x86` and `arm` are 32-bit only; `x86_64` and `aarch64` are 64-bit. `x86` and `x86_64` both carry sub-register width tables (down to 8-bit); `arm` has none, since A32 GPRs have no sub-register forms; `aarch64` has two (`Xn`/`Wn`), since A64's narrowest addressable width is 32 bits.

**Byte-register naming is where the x86 pair actually disagree with each other**, not just vary in degree: `x86`'s 8-bit table uses the REX-free `AH`/`CH`/`DH`/`BH` spellings for encodings 4-7, since this backend never emits a REX prefix; `x86_64`'s 8-bit table uses the REX-requiring `spl`/`bpl`/`sil`/`dil` spellings for the same register numbers and has no representation of the high-byte forms at all, since `isa/x86_64/encoder` never omits REX at 8-bit width. Neither table is wrong — they describe different encodings of registers that happen to share a number.

**Reverse-index construction.** `x86`, `arm`, and `aarch64` build their `by*`/`*ByName` maps once in `init()` from a small ordered slice held as the source of truth. `x86_64` skips this: its opcode tables are already `map[string]...` literals, and — per its own README — nothing under the repo currently disassembles x86-64, so there's no opcode→mnemonic direction to index in the first place.

**Disassembly support exists only for x86.** `format/asm/x86/text` is the one consumer reading `isa/x86`'s reverse-index maps in the decode direction; the other three packages' `By*`/reverse-index machinery (where present) currently serves only their own `init()`-time construction and any future decoder, not one that exists yet.

**One mnemonic collision, solved two different ways.** `isa/x86`'s `Group3Op` uses the real mnemonics `mul`/`imul` for the one-operand group-3 forms, leaving `lower/x86/mcode` to rename them `mul32`/`imul32` at its own call site to avoid colliding with the unrelated multi-operand `imul`. `isa/x86_64`'s `Grp3Ext` instead spells the equivalent forms `mul1`/`imul1` in the table itself, so the disambiguation lives at the source of truth rather than being translated downstream. Both READMEs cite the other's choice explicitly as the deliberate point of contrast.

**Encoding-31 dual meaning is unique to aarch64.** Only `isa/aarch64` has a register encoding whose meaning (`SP` vs `ZR`) is architecturally context-dependent rather than fixed; `XName`/`WName` take an explicit `isSP` argument to resolve it, a shape none of the other three packages needs.

**Endianness is named nowhere in `isa/<arch>` itself.** `arm`/`armeb` and `aarch64`/`aarch64_be` are `lower/<arch>` concerns (per the `lower` package's own README) — none of the four `isa` packages carries an endianness parameter, since byte order isn't a fact about instruction encoding at this level.