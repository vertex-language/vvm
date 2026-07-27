# isa/aarch64

```go
import isaa64 "github.com/vertex-language/vvm/cpu/isa/aarch64"
import "github.com/vertex-language/vvm/cpu/isa/aarch64/encoder"
```

`isa/aarch64` is a static, data-only package describing the AArch64 (ARM64) instruction set. It defines register identities, condition fields, instruction-word layouts, immediate codecs, and opcode-to-mnemonic mappings.

It covers only the fixed-width 32-bit encodings for the AArch64 state. (A32 and T32/Thumb are handled separately in `isa/arm`.)

This package owns the **facts**. The sibling package, `isa/aarch64/encoder`, owns the **decisions** (operand placement, aliasing, fixups, byte emission). Nothing in this package emits a byte.

## Core Architectural Facts

These four principles dictate how the data model is structured:

- **No X31 GPR:** Register field 31 denotes either the zero register (`RZR`) or the stack pointer (`RSP`). The operand's role in the instruction dictates which is meant; both encode to 31.
- **Width is a per-instruction bit:** Registers do not have separate 32/64-bit identities. The 5-bit register field is flat; the instruction's `sf` bit dictates if it is a 64-bit (`x`) or 32-bit (`w`) operation.
- **The PC is not a GPR:** The program counter is never addressed via register fields. Only specific instructions (`ADR`/`ADRP`, branches) read it.
- **Conditionality is limited:** Unlike A32, only specific families (`B.cond`, conditional-select/compare) carry a condition. Conditions are a per-instruction nibble, not a universal field.

## Package Structure

### `registers.go`

Defines physical register slots (`Reg`).

- **Registers:** `R0`–`R30` (GPRs), `RZR`/`RSP` (slot 31), and `RNone` (absent-operand sentinel). `RFP` (x29) and `RLR` (x30) are provided as architectural aliases.
- **Width:** `Width` (`W32`/`W64`) represents the `sf` bit.
- **Methods:** Classify values (`IsGPR`, `IsSlot31`), extract fields (`Field`), and format canonical assembly spellings (`Name`, `String`).
- **Parsing:** `ParseReg` resolves string spellings (e.g., `xzr`, `wsp`, `lr`) into a register, width, and slot-31 role.

### `condcodes.go`

Defines the 4-bit `tttn` condition space.

- Includes codes `CondEQ` through `CondNV`.
- Provides string formatting (`CondName`), parsing (`ParseCond`), and negation (`NegateCond`). `CondHS` and `CondLO` are mapped as aliases to `CondCS` and `CondCC`.

### `encoding.go`

Handles instruction bit-packing and immediate codecs.

- **Register Placement:** Constants (`RdShift`, `RnShift`, etc.) and `PlaceR*` helpers to OR fields into standard positions independently.
- **Immediate Codecs:**
  - **Move-wide:** 16-bit immediate and 2-bit `hw` field (`MoveWideHW`, `MoveWideShift`).
  - **Add/Sub:** 12-bit optionally shifted immediate (`EncodeAddSubImm`, `FitsAddSubImm`, `DecodeAddSubImm`).
  - **Bitmask:** Logical immediates encoded via `N`, `immr`, `imms` (`EncodeBitmaskImm`, `FitsBitmaskImm`, `DecodeBitmaskImm`). Automatically rejects unrepresentable all-zeros/all-ones.
- **Branch Codecs:** Field width offsets (`imm26`, `imm19`, `imm14`) with `Fits*`, `Encode*`, and `Decode*` helpers. *(Note: Applying offsets to targets is left to the encoder.)*

### `opcodes.go`

Maps opcodes to mnemonics and defines fixed instruction word bases.

- **Shifts:** `ShiftLSL`, `LSR`, `ASR`, `ROR` and validation helpers (`ShiftAllowsROR`).
- **Opcode Families:** Tables mapping operations and their base formats:
  - `AddSubOp`: add/adds/sub/subs
  - `LogicalOp`: shifted-register logical ops
  - `MoveWideOp`: movn/movz/movk
  - `DataProc2Op`: variable-shift and divide

## Scope and Boundaries

This package serves strictly as an informational backend. It **does not**:

- Lower values that exceed codec limits (e.g., handling constants that require a literal pool).
- Pack full instruction words independently.
- Perform control flow, label patching, or fixups.

Those generation decisions are delegated to the `encoder` package, which consumes this package's tables to turn a resolved instruction stream into machine words.