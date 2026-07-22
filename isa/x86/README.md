# isa/x86

```go
import isax86 "github.com/vertex-language/vvm/isa/x86"
```

Static, data-only description of the IA-32 (x86) instruction set: register
identity, condition codes, ModRM/SIB bit layout, and the opcode<->mnemonic
correspondence.

Two things import this package:

- **`isa/x86/encoder`** (`github.com/vertex-language/vvm/isa/x86/encoder`)
  — the generic assembler. It turns a fully-resolved `Inst` stream into
  machine bytes using the tables and packing helpers here.
- **`format/asm/x86/text`** — the debug disassembler/printer. It decodes
  bytes and looks up mnemonics using the same tables, in reverse.

Nothing in this package has control flow of consequence. It's constants,
lookup tables, and small pure functions, plus the reverse-index maps built
once in `init()` (`condByName`, `aluByName`, `aluByMR`, and so on). If a
change here needs a design decision rather than a citation, it probably
belongs somewhere else.

## What belongs here

The test: **is this a fact about the IA-32 machine, true regardless of what
any particular compiler chooses to emit?**

That includes facts nobody currently exercises. `AluOps` lists `adc` and
`sbb` even though no lowering in this repository emits them, and
`Group3Op`'s comment is explicit that the table's job is naming encodings,
not describing how each one's operand is used. `CondName`/`ParseCond` round
-trip the *canonical* mnemonic spelling only — the synonym constants
(`CondC`, `CondZ`, ...) are Go-level conveniences for call sites, not a
second textual vocabulary, and deliberately aren't accepted by `ParseCond`.

Concretely, this package owns:

- Register identity and naming (`registers.go`): the eight encodable GPRs,
  `RNone`, width-indexed name tables, and the `ByteAddressable` irregularity
  (AH/CH/DH/BH occupy indices 4-7 instead of the low byte of ESP/EBP/ESI/EDI).
- Condition codes (`condcodes.go`): the tttn encoding shared by `jcc`/`setcc`
  /`cmovcc`, its canonical and synonym spellings, and the negate-by-XOR-1
  fact.
- ModRM/SIB bit layout (`encoding.go`): field packing/unpacking, the four
  irregular escape values (`RMSIB`, `RMDisp32`, `SIBNoIndex`, `SIBNoBase`)
  and *why* they're irregular, scale-factor encoding, and the disp8-fits
  check.
- Opcode/mnemonic tables (`opcodes.go`): the ALU group, shift/rotate group,
  and group-3 (F6/F7) tables, plus the three-way `imul` opcode split that's
  a genuine property of the ISA (one mnemonic, three unrelated encodings).

## What doesn't belong here

- **Anything that's a choice rather than a fact.** Which encoding an
  instruction selector picks for a given IR node, how operands get
  allocated to registers, whether to prefer the imm8 or imm32 ALU form for
  a given constant — that's `lower/x86` and `isa/x86/encoder`'s `Encode`,
  not this package. This package supplies the menu; it doesn't order from
  it.
- **Unresolved/symbolic state.** `isa/x86/encoder`'s `Opr` is described as
  "never a not-yet-placed value" on purpose — a not-yet-resolved slot is a
  `lower/x86` concept (its own near-identical `Inst` type) that only
  collapses to this package's shapes once every slot is a real register or
  `[ebp+disp]` operand.
- **Printer formatting policy.** `format/asm/x86/text` may reuse
  `CondName`/`reg32`-style tables, but decisions about disassembly output
  format, column layout, or diagnostic verbosity live in the printer, not
  here.
- **Anything with control flow of consequence.** If a proposed addition
  needs a conditional beyond "is this input in range" or "default narrow
  width to 4", it's saying something about *how to use* an ISA fact, which
  means it belongs one layer up.

## Diagnostic behavior

Lookup functions here are total, not partial: `CondName` returns `"?"` for
an out-of-range code, `Reg.Name` returns `"?"` for a non-GPR, and `String()`
methods never panic. These are called from disassembly/diagnostic paths,
and a printer trying to describe a malformed or unsupported operand should
say "there isn't one" rather than take the process down.