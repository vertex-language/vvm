# isa/x86_64

```go
import isax64 "github.com/vertex-language/vvm/cpu/isa/x86_64"
import "github.com/vertex-language/vvm/cpu/isa/x86_64/encoder"
```

`isa/x86_64` is a static, data-only package describing the x86-64 ("long mode") instruction set. It defines register identity, the REX prefix, condition codes, ModRM/SIB layout and immediate/displacement limits, and opcode-to-mnemonic mappings for the ALU, shift, and single-operand instruction groups.

It covers only 64-bit long-mode encoding. There is no 16/32-bit legacy-mode variant here, and no separate package for it: register width in long mode is a per-instruction choice (REX.W, the 0x66 prefix, or the default), not a different addressed register file.

This package owns the **facts**. The sibling package, `isa/x86_64/encoder`, owns the **decisions** (operand placement, REX-byte necessity for a given instruction, ModRM/SIB construction from a resolved operand, fixups, byte emission). Nothing in this package emits a byte.

## Core Architectural Facts

- **No X31-style register split, but a REX-gated one:** All sixteen GPRs and sixteen XMM registers are named directly (0-15 each); `Reg` packs both into one byte space (GPRs at 0-15, XMM at 16-31) plus `RNone` as the absent-operand sentinel. What *is* split is reachability: registers r8-r15 and xmm8-xmm15 need a REX prefix's extension bit to be addressed at all — `NeedsREXBit` reports this, and it's the fact the encoder's REX-omission logic is built on.
- **Width is a per-instruction, per-prefix decision:** There's no separate 32/64-bit register identity to track (contrast `isa/aarch64`, where the same flatness holds); instead an operand's width comes from REX.W (64-bit), the 0x66 operand-size override (16-bit), or the 32-bit default. `Reg.Name` takes a width and looks up the correct spelling table.
- **Byte operands are the irregular case:** Without a REX prefix, only `al/cl/dl/bl` plus the legacy high-byte registers `ah/ch/dh/bh` are byte-addressable, and r8-r15 aren't reachable as byte operands at all. With a REX prefix, the high-byte registers become unreachable and are replaced by `spl/bpl/sil/dil` — the same bit pattern means a different register depending on whether REX is present. `NameByte`/`ByteAddressable` encode this switch; a REX prefix must never be emitted spuriously, since doing so silently reclassifies an ah/ch/dh/bh operand.
- **Conditions are a shared 4-bit space, not a universal field:** Unlike `isa/arm`'s per-instruction top-nibble condition, x86-64's tttn condition code is a 4-bit value embedded differently per opcode family (0F 8x for Jcc, 0F 9x for SETcc, 0F 4x for CMOVcc) — there is no single `CondShift`/`SetCond` accessor here because there's no single field to place it in.

## Package Structure

### `registers.go`

Defines physical register slots (`Reg`).

- **Registers:** `RRAX`-`RR15` (GPRs 0-15) and `RXMM0`-`RXMM15` (16-31), plus `RNone`. `NumGPR` is 16.
- **Classification:** `IsGPR`, `IsXMM`, `NeedsREXBit` (true for r8-r15 and xmm8-xmm15 — the register needs the REX extension bit to be named), `Low3` (the 3-bit field value shared by a register and its REX-extended counterpart).
- **Naming:** `Name(widthBits)` selects among 64/32/16-bit GPR spelling tables (and XMM names, width-independent); `NameByte(rex bool)` returns the byte-register spelling, switching between the no-REX high-byte set (`ah/ch/dh/bh`) and the REX low-byte set (`spl/bpl/sil/dil`, ...); `ByteAddressable(rex bool)` reports whether a byte encoding is reachable at all in the given mode. `String` defaults to the 64-bit name.

### `rex.go`

Defines the REX prefix, a single byte with fixed high nibble `0x40` and four payload bits.

- **Constants:** `REXBase` (the fixed nibble), `REXW`/`REXR`/`REXX`/`REXB` (operand-size and register-field-extension bits).
- **`PackREX(w, r, x, b bool) byte`** builds the byte from its four bits; a bare `REXBase` is a legal no-op REX.
- **`IsREX(b byte) bool`** recognizes the prefix range `0x40-0x4F`. That range's reuse in long mode is also why the one-byte 32-bit-mode inc/dec short forms don't exist here — they collide with REX and must be spelled as ModRM group-5 forms instead.

### `condcodes.go`

Defines the 4-bit Intel `tttn` condition space, shared by Jcc/SETcc/CMOVcc regardless of which opcode range embeds it.

- Codes `CondO` through `CondG`, left as untyped constants so they drop directly into whichever byte each opcode family embeds them in.
- **Synonyms:** `CondC`/`CondZ`/`CondNA`/etc. name the same encodings as their flag-oriented or relational reading (e.g. `CondC == CondB == 2`); only the canonical set in `condName` round-trips through `ParseCond`/`CondName`.
- **`NegateCond(cc byte) byte`** flips the low bit — a single-bit operation, since the encoding deliberately pairs every condition with its negation in adjacent even/odd slots.
- **`CondName`/`ParseCond`** format and resolve the canonical mnemonic suffix (`"e"`, `"ge"`, ...).

### `encoding.go`

Handles ModRM/SIB bit-packing, the encoding's irregular field values, and immediate/displacement fit checks.

- **ModRM:** `PackModRM`/`UnpackModRM` and the `Mod*` constants (`ModIndir`, `ModDisp8`, `ModDisp32`, `ModReg`).
- **SIB:** `PackSIB`/`UnpackSIB`, plus `ScaleBits`/`ScaleFactor` for the 2-bit scale field (0 and 1 are synonyms).
- **Irregular field values:** `RMSIB` (rm=4 means "SIB follows," inherited from IA-32 and why rsp/r12 always need a SIB byte); `RMRIP` (rm=5 with mod=0, which was absolute disp32 in 32-bit mode but is RIP-relative `[rip+disp32]` in long mode — the single biggest semantic break from `isa/x86`, and why rbp/r13 can never use mod=0); `SIBNoIndex` (index=4, not REX.X-extendable — a real difference from a base, where the same bit pattern under REX.X names r12); `SIBNoBase` (base=5 with mod=0, giving the absolute `[disp32]` form RIP-relative addressing no longer provides).
- **Legacy/mandatory prefixes:** `Prefix66` (operand size, now selecting 16-bit against a 32-bit default), `Prefix67` (address size, now 64→32), `PrefixF0`/`PrefixF2`/`PrefixF3` (LOCK/REPNE/REP, the latter two doubling as mandatory prefixes for some SSE opcodes).
- **Fit checks:** `FitsDisp8`, `FitsImm8` (unchanged widths from IA-32), and `FitsImm32` — the widest immediate any instruction but `movabs` accepts; a value failing it needs the imm64 form or a register load.

### `opcodes.go`

Maps opcodes to mnemonics for the ALU, shift, and single-r/m-operand groups. It does **not** cover SSE scalar arithmetic, move/convert, or REX.W-only extended instructions (`movzx`/`bswap`/`popcnt`/etc.) — those opcode bytes are embedded directly at the encoder call sites rather than tabled here, since each is a single fixed encoding rather than a member of a shared-shape family.

- **Two-operand ALU group:** `AluOp` (MR/RM/accumulator-short-form opcode bytes plus the `/ext` digit shared by the `0x80`/`0x81`/`0x83` immediate-group opcodes). `AluOps` is the full eight-entry table (add/or/adc/sbb/and/sub/xor/cmp) in `/ext` order — adc/sbb are included as decodable facts about the machine even though no lowering in this repository emits them. Looked up by name, MR/RM/Acc opcode byte, or `/ext` via `AluByName`/`AluByMR`/`AluByRM`/`AluByAcc`/`AluByExt`.
- **Shift/rotate group ("group 2"):** opcode bytes for imm8/CL/implicit-1 count forms (`ShiftImm8`, `ShiftCL`, `ShiftOne`, and their byte-operand variants); `ShiftOps` covers seven of the eight `/ext` slots (rol/ror/rcl/rcr/shl/shr/sar) — `/ext` 6 is deliberately unnamed since it isn't an architectural fact, only a common undocumented alias of shl.
- **Single-r/m-operand group ("group 3"):** `Group3` (`0xF7`)/`Group3Byte` (`0xF6`) opcode bytes; `Group3Op` records each member's `/ext` digit and whether it carries a trailing immediate (`HasImm`, true only for `test`) — the group is encoding-uniform (one r/m operand, mnemonic from ModRM.reg) despite not, and deliberately not, being uniform in read/write behavior across not/neg/mul/imul/div/idiv/test. `/ext` 1 is left unnamed as group 2's `/ext` 6 is.
- **The imul ambiguity:** three distinct imul shapes share one mnemonic on IA-32 — the widening one-operand group-3 form (`0xF7 /5`), the same-width two-operand form (`0x0F 0xAF`), and the three-operand immediate form (`0x69`/`0x6B`). This package resolves it by arity rather than inventing spellings: `Group3ByName("imul")` is unambiguously the one-operand form, since only that one appears in the group-3 table; the other two are plain constants (`Imul2Esc`/`Imul2Op`, `Imul3Imm32`/`Imul3Imm8`) for the encoder to select by operand count.

## Scope and Boundaries

This package serves strictly as an informational backend. It **does not**:

- Decide when a REX prefix is needed for a given instruction, or build one from resolved operands (that's `rexNeed` in the encoder).
- Choose ModRM/SIB field values for a resolved operand (register vs. memory, base/index/disp shape); it only defines what the fields mean and how to pack/unpack them.
- Lower values that exceed codec limits (e.g., a 64-bit immediate that doesn't fit a sign-extended imm32 field must go through `movabs`, which is encoder-level operand selection, not something this package performs).
- Pack full instruction words or apply RIP-relative addend arithmetic independently.
- Perform control flow, label patching, or fixups.

Those generation decisions are delegated to the `encoder` package, which consumes this package's tables to turn a resolved instruction stream into machine words.