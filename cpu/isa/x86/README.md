# isa/x86

```go
import isax86 "github.com/vertex-language/vvm/cpu/isa/x86"
import "github.com/vertex-language/vvm/cpu/isa/x86/encoder"
```

`isa/x86` is a static, data-only package describing the IA-32 (x86) instruction set. It defines register identity, condition codes, ModRM/SIB bit layout, and the opcode-to-mnemonic correspondence that both the generic encoder (`isa/x86/encoder`) and the debug disassembler (`format/asm/x86/text`) build on.

It covers only the classic 32-bit encodings reachable without a REX prefix. Reaching r8–r15, or any 64-bit operand form, is out of scope for this backend entirely — not deferred to a sibling package the way A32/T32/AArch64 split across `isa/arm` and `isa/aarch64`.

This package owns the **facts**. The sibling package, `isa/x86/encoder`, owns the **decisions** (operand placement, ModRM/SIB construction, fixups, byte emission). Nothing in this package encodes or decodes an instruction stream — there is no control flow of consequence here, only declarations, plus mechanical reverse-index maps built once in `init()`.

## Core Architectural Facts

- **Eight GPRs, no REX:** Only `EAX`–`EDI` (encodings 0–7) are reachable. `RNone` is the absent-operand sentinel for optional base/index operands and is never itself encodable.
- **Byte registers are irregular:** Encodings 0–3 (`AL`/`CL`/`DL`/`BL`) have a low-byte spelling; encodings 4–7 name `AH`/`CH`/`DH`/`BH` instead of the low byte of `ESP`/`EBP`/`ESI`/`EDI`. Addressing "the low byte of esi" is not representable in 32-bit mode at all, so nothing may silently substitute one for the other.
- **ModRM escapes collide with real registers:** `rm==4` (mod≠11) means "a SIB byte follows," occupying `ESP`'s slot — hence `[esp+disp]` always needs SIB. `rm==5` with `mod==00` means "no base, disp32 follows," occupying `EBP`'s slot — hence an `EBP` base can never use `mod==00` and always carries at least a `disp8`, even at displacement zero. The same pair of escapes recurs in the SIB byte's index and base fields.
- **Conditions are a shared 4-bit space:** The `tttn` encoding underlies `Jcc` (`0F 8x`), `SETcc` (`0F 9x`), and `CMOVcc` (`0F 4x`) alike, and pairs every condition with its negation in adjacent even/odd slots.

## Package Structure

### `registers.go`

Defines physical register slots (`Reg`).

- **Registers:** Values 0-7 are the eight GPRs in ModRM/SIB encoding order; RNone is the "absent" sentinel for optional base/index operands and is never encodable.
- **Width tables:** reg32/reg16/reg8 are the three width-indexed name tables for the eight GPR encodings, with byte-register naming as the documented irregular case.
- **Methods:** IsGPR reports whether r names one of the eight encodable GPRs; ByteAddressable reports whether r has a low-byte spelling reachable without a REX prefix; `Name`/`String` format canonical assembly spellings, returning `"?"` for non-GPR values rather than panicking, since naming is a diagnostic path.

### `condcodes.go`

Defines the 4-bit `tttn` condition space (Intel's `0F 8x` jcc / `0F 9x` setcc / `0F 4x` cmovcc encoding).

- Codes `CondO` through `CondG`, left as untyped constants to match how both the encoder (`Inst.CC`) and the printer (a decoded opcode nibble) treat them as plain bytes.
- **Synonyms:** Alternate flag-oriented spellings (`CondC`, `CondZ`, `CondNA`, etc.) map onto the same sixteen codes — `CondC` and `CondB` both encode as 2 — since a call site may read better in the carry/zero vocabulary than the relational one.
- **Formatting/parsing:** `CondName`/`ParseCond` round-trip through the canonical mnemonic suffixes only; the Go-level synonyms are not a second textual vocabulary.
- **Negation:** `NegateCond` is a single bit flip, exploiting the even/odd pairing of each condition with its complement — the operation an instruction selector needs whenever it inverts a two-way branch.

### `encoding.go`

Handles ModRM/SIB bit-packing and immediate-fit checks.

- **ModRM fields:** `ModIndir`/`ModDisp8`/`ModDisp32`/`ModReg`, plus `PackModRM`/`UnpackModRM`.
- **The four irregular escapes:** `RMSIB`, `RMDisp32`, `SIBNoIndex`, `SIBNoBase` — each documented as occupying a real register's encoding, which is what makes the corresponding addressing form otherwise unrepresentable.
- **SIB fields:** `PackSIB`/`UnpackSIB`, and `ScaleBits`/`ScaleFactor` converting between a SIB scale factor (1/2/4/8, with 0 accepted as a synonym for 1) and its 2-bit field.
- **Legacy prefixes:** `Prefix66`/`Prefix67`/`PrefixF0`/`PrefixF2`/`PrefixF3`, named rather than left as bare hex.
- **Immediate fit checks:** `FitsDisp8` and `FitsImm8` — whether a value fits the shorter sign-extended 8-bit forms (`disp8` addressing; the `0x83`/`0x6B` imm8 opcode variants) in place of their 32-bit counterparts.

### `opcodes.go`

Maps opcodes to mnemonics and defines fixed instruction word bases, organized by encoding-shaped group rather than by semantics:

- **Two-operand ALU group:** `AluOps`, the eight-entry table (add/or/adc/sbb/and/sub/xor/cmp) sharing the MR/RM/accumulator/imm-group opcode shape, keyed by `/ext` digit. adc and sbb are included for disassembly completeness even though no lowering in this repository emits them — these are facts about the machine, not a menu of what the compiler happens to use. Looked up by name, MR/RM/Acc opcode byte, or `/ext`.
- **Shift/rotate group ("group 2"):** `ShiftOps`, the seven mapped members (rol/ror/rcl/rcr/shl/shr/sar) across the imm8/CL/implicit-1 opcode forms. `/ext` 6 is deliberately unnamed, since its undocumented shl-alias behavior isn't an architectural fact.
- **Single-operand group ("group 3"):** `Group3Ops`, the seven named members (test/not/neg/mul/imul/div/idiv) sharing one r/m-operand encoding shape despite differing read/write behavior; `HasImm` marks the one member (`test`) carrying a trailing immediate. `/ext` 1 is left unnamed as an undocumented alias of `test`.
- **The three-way imul split:** documented and resolved by arity rather than by inventing spellings — the widening one-operand form (`0xF7 /5`), the two-operand form (`0x0F 0xAF`), and the three-operand immediate form (`0x69`/`0x6B`) are three distinct encodings that all happen to share the mnemonic "imul."

## Scope and Boundaries

This package serves strictly as an informational backend. It **does not**:

- Reach any register, operand width, or encoding that requires a REX prefix (r8–r15, 64-bit operands).
- Choose between equivalent encodings (e.g., which imm8/imm32/accumulator form to emit) — that selection is `encoder`'s job.
- Pack full ModRM/SIB bytes independently, or resolve labels and relocations.
- Emit a single byte of machine code.

Those generation decisions are delegated to the `encoder` package, which consumes this package's tables to turn a resolved `Inst`/`Opr` stream into IA-32 machine bytes.