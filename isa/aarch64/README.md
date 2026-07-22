# isa/aarch64

​```go
import isaa64 "github.com/vertex-language/vvm/isa/aarch64"
​```

Static, data-only description of the AArch64 (ARM64, "A64") instruction
set: register identity, the condition field, instruction-word layout, the
move-wide and bitmask immediate codecs, and the opcode<->mnemonic
correspondence.

This is the 64-bit counterpart to `isa/arm`. It covers the **A64** encoding
only — not A32 ("ARM state") and not T32/Thumb, which are distinct
instruction sets with their own encodings and live in `isa/arm`. A64 is
little-endian for instruction fetch; data endianness is a memory property,
not one of these tables.

Two things import this package:

- **`isa/aarch64/encoder`** — the generic assembler. It turns a fully-
  resolved `Inst` stream into machine words using the tables and helpers
  here.
- **`format/asm/aarch64/text`** — the debug disassembler/printer. It decodes
  words and looks up mnemonics using the same tables, in reverse.

Nothing here has control flow of consequence: constants, lookup tables,
small pure functions, and the reverse-index maps built once in `init()`. If
a change needs a design decision rather than a citation, it belongs
somewhere else.

## What belongs here

The test: **is this a fact about the A64 machine, true regardless of what
any particular compiler chooses to emit?**

What makes A64 different from the `isa/arm` (A32) sibling, and drives most
of what this package pins down:

- **There is no X31.** The 5-bit register field takes values 0-31, but 31
  is not a general register: depending on the instruction it denotes either
  the zero register (`RZR`, reading as 0, discarding writes) or the stack
  pointer (`RSP`). Both spell to encoding 31 — which one is meant is a
  per-operand-role fact of the instruction, not a property of the value.
  So `Reg.Name` takes a `sp` argument, exactly as `isa/x86_64`'s `NameByte`
  takes a REX flag: the correct spelling of encoding 31 depends on context
  the register value alone doesn't carry.
- **Width is a per-instruction bit, not a register number.** Every GPR has
  a 64-bit (`x0`-`x30`) and a 32-bit (`w0`-`w30`) view of the *same*
  register, selected by the `sf` bit; a `w` write zeroes the upper half.
  The register field is a flat five bits with no REX-style low-3/high-bit
  split, so `Reg.Name` takes a `Width`, and there is deliberately nothing
  resembling `isa/x86_64`'s `Low3`/`NeedsREXBit`.
- **The PC is not a general register.** Unlike A32's `r15`, the program
  counter never appears in a register field; only `ADR`/`ADRP` and the
  branches compute PC-relative values. There is deliberately no `RPC`.
- **The condition field is not universal.** Unlike A32, where bits 31:28 of
  nearly every instruction are a condition, A64 conditionality is confined
  to `B.cond`, the conditional-select family (`csel`/`cset`/...), and the
  conditional-compare family (`ccmp`/`ccmn`). So the condition codes here
  are a per-instruction nibble, like x86's, not a field on the whole
  instruction set.
- **Immediates come in three scattered/shaped forms.** The move-wide
  immediate is a 16-bit value placed at one of four halfword positions
  (`hw`); the arithmetic immediate is a 12-bit value optionally shifted left
  12; the logical immediate is a *bitmask* — a single run of ones within a
  2/4/8/16/32/64-bit element, replicated across the register, or its inverse
  (`EncodeBitmaskImm`/`DecodeBitmaskImm`). The bitmask form is the A64 analog
  of A32's rotated modified immediate: the question "can the machine carry
  this constant inline?" with a representable set that is scattered, not a
  contiguous range.

Concretely, this package owns register identity and naming (`registers.go`),
the condition field and its canonical/synonym spellings (`condcodes.go`),
instruction-word layout — the fixed 4-byte width, register-field positions,
the move-wide `hw` codec, the shifted-12 arithmetic-immediate check, the
bitmask-immediate codec, and the branch word-offset codecs for the three
field widths (`encoding.go`) — and the opcode/mnemonic tables: the logical
group with its negated variants and `N` bit, the move-wide group, the
barrel-shift types, the data-processing two-source operations, and the
add/sub `op`/`S` naming (`opcodes.go`).

That includes facts nobody currently exercises: `LogicalOps` lists `eon` and
the flag-setting `bics` whether or not any lowering emits them, because a
disassembler must still name them. `CondName`/`ParseCond` round-trip the
*canonical* spelling only — the synonyms (`hs`/`lo`) are call-site
conveniences, not a second textual vocabulary, matching how `isa/arm` and
the x86 packages treat their synonym constants.

## What doesn't belong here

- **Anything that's a choice rather than a fact.** How to build a constant
  that fits neither `EncodeBitmaskImm` nor a single move-wide (a
  `MOVZ`/`MOVK` sequence vs. a literal-pool load), whether to reach a global
  by `ADRP`+add or a literal load, which of the add/sub/logical operand
  forms to select, whether encoding 31 should be `sp` or `xzr` for a given
  operand — that's `lower/aarch64` and the encoder's `Encode`, not this
  package. This package supplies the menu; it doesn't order from it.
- **Unresolved/symbolic state.** A not-yet-placed operand is a
  `lower/aarch64` concept that only collapses to this package's shapes once
  every field is a real register or immediate.
- **Printer formatting policy.** `format/asm/aarch64/text` may reuse these
  tables, but disassembly output format, whether to print an omitted `LSL
  #0`, and whether to prefer `lr` over `x30` are printer decisions.
- **Anything with control flow of consequence.** If a proposed addition
  needs a conditional beyond "is this input in range", it's saying something
  about *how to use* an ISA fact, and belongs one layer up.

## Diagnostic behavior

Lookup functions are total, not partial: `CondName` and `ShiftName` return
`"?"` for an out-of-range code, `Reg.Name` returns `"?"` for `RNone` or an
out-of-range value, and `String()` methods never panic. They are called from
disassembly/diagnostic paths, where describing a malformed operand should
say "there isn't one" rather than take the process down. The immediate
*decoders* are the one honest exception to "total": a bitmask field has
genuinely-reserved encodings, so `DecodeBitmaskImm` reports `ok=false`
rather than inventing a value — but it still never panics.