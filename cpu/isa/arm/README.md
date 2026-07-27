# isa/arm

```go
import isaarm "github.com/vertex-language/vvm/cpu/isa/arm"
import "github.com/vertex-language/vvm/cpu/isa/arm/encoder"
```

`isa/arm` is a static, data-only package describing the 32-bit ARM (A32 / "ARM state") instruction set. It defines register identity, condition codes, instruction-word layout, immediate codecs, and opcode-to-mnemonic mappings.

It covers only the fixed-width 32-bit "ARM" encoding. T32/Thumb and A64/AArch64 are separate instruction sets with their own encodings and are not handled here (AArch64 lives in `isa/aarch64`). This package serves both the `arm` (little-endian) and `armeb` (big-endian) targets: endianness is a property of how data words are laid out in memory, not of these tables, so a single description covers both.

This package owns the **facts**. The sibling package, `isa/arm/encoder`, owns the **decisions** (operand placement, aliasing, fixups, byte emission). Nothing in this package emits a byte.

## Core Architectural Facts

Three principles drive almost everything in this data model:

- **Every instruction is conditional:** Bits 31:28 of nearly every A32 encoding are a 4-bit condition field checked against the CPSR flags; an instruction whose condition fails retires with no effect. Conditions are therefore not a per-opcode nibble but a field on the whole instruction set — the opposite arrangement from `isa/aarch64`, where only specific families carry a condition.
- **Register fields are a flat four bits:** All sixteen GPRs are named by a contiguous 4-bit field wherever a register appears; there is no split of a low-3 field from a separate high bit, and no separate 32/64-bit identity to track.
- **The PC is a general register:** r15 *is* the program counter and may appear directly in most register fields. Reading it yields the current instruction's address plus 8 (a pipeline artifact), and writing it branches.

## Package Structure

### `registers.go`

Defines physical register slots (`Reg`).

- **Registers:** `R0`–`R15`, the sixteen GPRs, encoded directly by their register number. `RNone` is the absent-operand sentinel.
- **Aliases:** `RSP` (r13), `RLR` (r14), and `RPC` (r15) are provided, each with a different architectural status — `RPC` is a hardware fact, `RLR` partly so (BL/BLX write it, and it's banked per mode), `RSP` pure software convention.
- **Methods:** Classify values (`IsGPR`), extract fields (`Field`), and format canonical assembly spellings (`Name`, `String`).
- **Parsing:** `ParseReg` resolves string spellings (e.g., `r0`, `sp`, `lr`, `pc`) into a register.

### `condcodes.go`

Defines the 4-bit `tttn` condition space.

- Includes codes `CondEQ` through `CondNV`.
- Provides string formatting (`CondName`), parsing (`ParseCond`), and negation (`NegateCond`) via a single bit flip, exploiting the even/odd pairing of each condition with its complement. `CondHS`/`CondLO` are mapped as aliases to `CondCS`/`CondCC`.
- `CondNV` (0b1111) is named for decoding purposes only: from ARMv5 the architecture reclaims that condition space for unconditional instructions, so it is not an emittable condition.

### `encoding.go`

Handles instruction bit-packing and immediate codecs.

- **Condition Field:** `CondShift`, `SetCond`, and `Cond` place and read the universal top-nibble condition field — the one field accessor that's universal across the set, since register/operand positions otherwise vary by format.
- **Modified Immediates:** The "Operand2" immediate form — an 8-bit value rotated right by an even amount — via `EncodeModImm`, `FitsModImm`, and `DecodeModImm`. The representable set is scattered (any byte value, at any even rotation) rather than a contiguous range; a value that doesn't fit must be built some other way (MOVW/MOVT or a literal pool), which is lowering work done one layer up.
- **Branch Codecs:** The signed 24-bit word-offset field used by B/BL (`BranchImm24Bits`, `FitsBranchImm24`, `EncodeBranchImm24`, `DecodeBranchImm24`). *(Note: applying the PC's +8 read-offset to a target address is left to the encoder.)*

### `opcodes.go`

Maps opcodes to mnemonics and defines fixed instruction word bases.

- **Data-Processing Group:** `DataProcOps`, the sixteen-entry table (and/eor/sub/rsb/add/adc/sbc/rsc/tst/teq/cmp/cmn/orr/mov/bic/mvn) sharing one encoding family but differing in operand shape — whether the op writes `Rd` (`WritesRd`), reads `Rn` (`UsesRn`), or forces the S bit (`ForcesS`). Looked up by `DataProcByName`/`DataProcByOpcode`.
- **Shifts:** `ShiftLSL`, `LSR`, `ASR`, `ROR`, with `ShiftName`/`ParseShift` (accepting the `asl`→`lsl` synonym). RRX and the LSR/ASR `#0` special cases are documented but deliberately left unhandled here, since they're immediate-form rewrites rather than distinct shift types.
- **Single Data Transfer Bits:** `LSBitL/W/B/U/P`, the five one-bit modifiers of the LDR/STR encoding.
- **Block Data Transfer Modes:** `BlockModes`, the four (P,U) addressing modes (`ia`/`ib`/`da`/`db`) alongside their load/store-dependent stack-oriented aliases (full/empty, ascending/descending). Resolved by `BlockModeByGeneric`.

## Scope and Boundaries

This package serves strictly as an informational backend. It **does not**:

- Lower values that exceed codec limits (e.g., constants requiring a MOVW/MOVT pair or a literal pool).
- Pack full instruction words independently.
- Perform control flow, label patching, or fixups.
- Apply the PC's +8 read-offset when turning a branch target into a field.

Those generation decisions are delegated to the `encoder` package, which consumes this package's tables to turn a resolved instruction stream into machine words.