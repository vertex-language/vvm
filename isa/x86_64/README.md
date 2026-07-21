# isa/x86_64

`github.com/vertex-language/vvm/isa/x86_64`

The static, data-only description of the x86-64 (AMD64) instruction set: register identity, condition-code numbering, REX/ModRM/SIB bit layout, and the opcode↔mnemonic correspondence. `isa/x86_64/encoder` (byte emission, see `encode.go`/`inst.go`) builds on it directly, as does `lower/x86_64` — both its instruction selection and its inline-asm mnemonic parser (`lower/x86_64/inlineasm`).

```go
import x86_64 "github.com/vertex-language/vvm/isa/x86_64"
```

There is no control flow of consequence in this package — only declarations. Unlike some sibling ISA packages, nothing here is built by an `init()`-time reverse index: `opcodes.go`'s tables (`ALUOpcodes`, `ShiftExt`, `Grp3Ext`, `Grp5Ext`) are already mnemonic-keyed map literals, and there's no opcode→mnemonic direction because nothing under this repo currently disassembles x86-64 — only `isa/x86_64/encoder` consumes these facts, on the way in.

---

## Package layout

```
isa/x86_64/
├── registers.go   Reg, the sixteen GPR constants (RAX..R15), RNone, and
│                  the width-indexed Name64/Name32/Name16/Name8 tables
├── condcodes.go   Cond* constants (Intel tttn encoding), CondMnemonics,
│                  CondName
├── encoding.go    PackModRM/UnpackModRM, PackSIB, PackREX, HiBit/LoBits,
│                  ModRM/SIB special-value constants, legacy prefix bytes
└── opcodes.go     ALUOpcodes/ShiftExt/Grp3Ext/Grp5Ext tables plus fixed
                   opcode/ext constants for forms outside a systematic group
```

---

## The test: ISA fact vs. lowering decision

Everything in this package is true of x86-64 independent of any particular compiler's choices — a register's 4-bit encoding, which REX bit extends which ModRM/SIB field, which condition-code number means "signed less than," which `/ext` digit selects `neg` under the `F6/F7` group. None of it depends on how `lower/x86_64` decides to allocate registers, build a frame, or select instructions.

The dividing line: if a fact would still be true even if `lower/x86_64` were deleted and rewritten from scratch with a completely different register-allocation strategy, it belongs here. If it's a decision *this compiler* makes about how to use those facts — which registers are scratch vs. callee-saved, how stack slots are laid out, which mnemonics an inline-asm table supports — it belongs in `lower/x86_64` instead.

One naming wrinkle worth knowing: `Grp3Ext` spells the one-operand, implicit-RAX:RDX group-3 forms `mul1`/`imul1` rather than the real assembly mnemonics `mul`/`imul`, precisely so this package's own table can't collide with the unrelated two/three-operand `imul` forms (`OpImulRM`, `OpImul3`) it also names. That disambiguation lives here, at the source of truth, rather than being translated at a downstream call site.

---

## Registers (`registers.go`)

`Reg` is a `byte`-sized general-purpose register identifier: `RAX`..`R15` (0-15), plus `RNone` (`0xFF`) as the "absent" sentinel used throughout `isa/x86_64/encoder` for optional base operands. Bit 3 of the encoding is the REX extension bit (selects r8-r15 when set); bits 0-2 are the raw ModRM/SIB field value.

Four width-indexed tables (`Name64`, `Name32`, `Name16`, `Name8`) give each register's assembly spelling at 64-, 32-, 16-, and 8-bit widths; `NameForWidth(r, width)` looks one up by byte width (1/2/4/8), defaulting to the 64-bit spelling for any other value. The 8-bit row always uses the REX-requiring `spl`/`bpl`/`sil`/`dil` spellings for RSP/RBP/RSI/RDI rather than the legacy REX-free `ah`/`ch`/`dh`/`bh` forms — this table has no representation of the high-byte registers at all, matching the fact that `isa/x86_64/encoder` never omits REX when addressing RSP/RBP/RSI/RDI at 8-bit width.

## Condition codes (`condcodes.go`)

The sixteen `Cond*` constants (`CondO`..`CondG`) are Intel's 4-bit `tttn` encoding, shared verbatim across `0F 8x` (`jcc`), `0F 9x` (`setcc`), and `0F 4x` (`cmovcc`). `CondMnemonics` maps every mnemonic-suffix spelling assemblers accept — including redundant aliases such as `jc`/`jnae` both meaning `CondB`, or `jz`/`je` both meaning `CondE` — to its condition-code number; `CondName` is the inverse, canonical (non-aliased) direction, for printing rather than parsing a `jCC`/`setCC`/`cmovCC` mnemonic. Before this package existed, `lower/x86_64/inlineasm/common.go` kept `CondMnemonics`'s exact contents as its own unexported table, purely to parse condition-code mnemonics — duplicating a fact the encoder already depended on, with nothing to catch the two drifting apart.

## Encoding primitives (`encoding.go`)

`PackModRM`/`UnpackModRM` convert a ModRM byte to and from its three fields (`mod`/`reg`/`rm`); `reg` and `rm` are taken as their low 3 bits only, since REX.R/B extension is a separate concern folded in by the caller via `HiBit`. `PackSIB` assembles a SIB byte the same way, using the same low-3-bits convention. `HiBit(r)` extracts a register's REX extension bit (bit 3, selecting r8-r15); `LoBits(r)` extracts its 3-bit ModRM/SIB field value with that bit stripped off — callers fold `HiBit` into whichever of REX.R/X/B corresponds to the field the register occupies. `PackREX(w, r, x, b)` assembles a REX prefix byte from those four bits.

The mod-field constants (`ModDisp0`, `ModDisp8`, `ModDisp32`, `ModReg`) and the rm/SIB special-value constants (`RMNeedsSIB`, `RMRipOrDisp32`, `SIBNoIndex`, `SIBBaseEscape`) name the x86-64 addressing quirks `isa/x86_64/encoder`'s `memBody` relies on together: rm==4 (RSP/R12) always forces a SIB byte, mod==0 with rm==5 means `[RIP+disp32]` rather than `[RBP/R13]` bare, and an absolute `[disp32]` needs the SIB base-escape encoding. The legacy prefix byte constants (`PrefixOperandSize`, `PrefixLock`, `PrefixRepne`, `PrefixRep`) round out the set of encoding-level facts the encoder needs named rather than left as bare hex literals.

## Opcode tables (`opcodes.go`)

- **`ALUOpcodes`** (`map[string]ALUOp`) — the six two-operand ALU instructions (`add`/`or`/`and`/`sub`/`xor`/`cmp`), each an `ALUOp{MR, RM, Ext byte}`: its MR opcode (`r/m, reg`), RM opcode (`reg, r/m`), and its `/ext` digit under the `80`/`81`/`83` (`r/m, imm`) group.
- **`ShiftExt`** (`map[string]byte`) — the `/ext` digit for `rol`/`ror`/`shl`/`shr`/`sar`, shared by the `C0`/`C1` (immediate-count) and `D0`-`D3` (by-1 / by-CL) opcode groups.
- **`Grp3Ext`** (`map[string]byte`) — the `/ext` digit for the `F6`/`F7` group: `not`, `neg`, and the implicit-RAX:RDX one-operand `mul1`/`imul1`/`div`/`idiv` forms.
- **`Grp5Ext`** (`map[string]byte`) — the `/ext` digit this encoder uses from the `FE`/`FF` group: register/memory `inc`/`dec`. (`FF`'s other extensions — `call`/`jmp`/`push r/m` — are named individually as `OpCallRegExt`/`OpJmpRegExt` below, since this encoder only ever reaches them via register operands.)

The remaining `Op*` constants are fixed opcode bytes and `/ext` digits for forms outside a systematic group — `mov` in its several shapes, `movzx`/`movsx`/`movsxd`, `lea`, `imul`'s two/three-operand forms, `jcc`/`setcc`/`cmovcc` base opcodes (added to a condition code), `push`/`pop` base opcodes (folded with a register's low 3 bits), and single-purpose instructions like `ret`, `ud2`, `syscall`, `bsr`/`bsf`/`bswap`, `xchg`, the `lock_`-prefixed RMW forms, `mfence`, `popcnt`, and the string-op opcodes (`rep_movsb`/`rep_stosb`).