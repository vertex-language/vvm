# isa/x86_64

```go
import isax64 "github.com/vertex-language/vvm/isa/x86_64"
```

Static, data-only description of the x86-64 (AMD64 / Intel 64, "long mode")
instruction set: register identity, the REX prefix, condition codes,
ModRM/SIB bit layout, and the opcode<->mnemonic correspondence.

Two things import this package:

- **`isa/x86_64/encoder`** (`github.com/vertex-language/vvm/isa/x86_64/encoder`)
  — the generic assembler. It turns a fully-resolved `Inst` stream into
  machine bytes using the tables and packing helpers here, computing a REX
  prefix from the operands where one is needed.
- **`format/asm/x86_64/text`** — the debug disassembler/printer. It decodes
  bytes and looks up mnemonics using the same tables, in reverse.

Nothing in this package has control flow of consequence. It's constants,
lookup tables, and small pure functions, plus the reverse-index maps built
once in `init()` (`condByName`, `aluByName`, `aluByMR`, and so on). If a
change here needs a design decision rather than a citation, it probably
belongs somewhere else.

## What belongs here

The test: **is this a fact about the x86-64 machine, true regardless of what
any particular compiler chooses to emit?**

That includes facts nobody currently exercises. `AluOps` lists `adc` and
`sbb` even though no lowering in this repository emits them, and
`Group3Op`'s comment is explicit that the table's job is naming encodings,
not describing how each one's operand is used. `CondName`/`ParseCond` round
-trip the *canonical* mnemonic spelling only — the synonym constants
(`CondC`, `CondZ`, ...) are Go-level conveniences for call sites, not a
second textual vocabulary, and deliberately aren't accepted by `ParseCond`.

Concretely, this package owns:

- Register identity and naming (`registers.go`): the sixteen encodable
  GPRs, `RNone`, width-indexed name tables, `Low3`/`NeedsREXBit` for
  splitting an encoding across the ModRM/SIB field and its REX bit, and the
  byte-register irregularity: without a REX prefix, indices 4-7 are always
  AH/CH/DH/BH; with one, they're reclassified as SPL/BPL/SIL/DIL. So
  `ByteAddressable` takes a REX argument: a byte operand on SPL/BPL/SIL/DIL
  *requires* a REX prefix, one on AH/CH/DH/BH *forbids* one, and the two can
  never share an instruction.
- The REX prefix (`rex.go`): the fixed `0x40` high nibble, the W/R/X/B
  payload bits (`REXW=8`, `REXR=4`, `REXX=2`, `REXB=1`), `PackREX`, and the
  fact that this range claims opcodes `0x40`-`0x4F` — which is *why* the
  one-byte `inc`/`dec` short forms don't exist and must be spelled with the
  group-5 ModRM forms instead.
- Condition codes (`condcodes.go`): the tttn encoding shared by `jcc`/`setcc`
  /`cmovcc`, its canonical and synonym spellings, and the negate-by-XOR-1
  fact.
- ModRM/SIB bit layout (`encoding.go`): field packing/unpacking, scale-
  factor encoding, the disp8-fits check, and the irregular escape values —
  including two whose meaning takes some care: `RMRIP` (`mod=00 rm=101`) is
  RIP-relative `[rip+disp32]`, while a true absolute address goes through
  the SIB no-base form (`SIBNoBase`) instead. An RBP/r13 base can never use
  `mod=00`, and RSP/r12 in `rm` always forces a SIB byte.
- Opcode/mnemonic tables (`opcodes.go`): the ALU group, shift/rotate group,
  and group-3 (F6/F7) tables, plus the three-way `imul` opcode split that's
  a genuine property of the ISA (one mnemonic, three unrelated encodings).

## What doesn't belong here

- **Anything that's a choice rather than a fact.** Which encoding an
  instruction selector picks, how operands get allocated to registers,
  whether to prefer the imm8 or imm32 ALU form, whether to reach a global
  by RIP-relative or absolute addressing, whether a 64-bit constant is worth
  a `movabs` or should be built up — that's `lower/x86_64` and the encoder's
  `Encode`, not this package. This package supplies the menu; it doesn't
  order from it.
- **Whether to emit a REX prefix.** *That* a REX prefix carries these four
  bits and claims this opcode range is a machine fact and lives here.
  *When* one is required for a given instruction — deciding it from the
  operands' widths and register numbers — is encoding logic and lives in the
  encoder (`rexNeed` in `encode.go`).
- **Unresolved/symbolic state.** The encoder's `Opr` is "never a not-yet-
  placed value" on purpose — a not-yet-resolved slot is a `lower/x86_64`
  concept (its own near-identical `Inst` type) that only collapses to this
  package's shapes once every slot is a real register or memory operand.
- **Printer formatting policy.** `format/asm/x86_64/text` may reuse
  `CondName`/`reg64`-style tables, but decisions about disassembly output
  format, column layout, or diagnostic verbosity live in the printer.
- **Anything with control flow of consequence.** If a proposed addition
  needs a conditional beyond "is this input in range" or "default width to
  8", it's saying something about *how to use* an ISA fact, which means it
  belongs one layer up.

## Diagnostic behavior

Lookup functions here are total, not partial: `CondName` returns `"?"` for
an out-of-range code, `Reg.Name` returns `"?"` for a non-GPR, and `String()`
methods never panic. These are called from disassembly/diagnostic paths,
and a printer trying to describe a malformed or unsupported operand should
say "there isn't one" rather than take the process down. Note `Reg.NameByte`
takes a REX flag for the same reason `ByteAddressable` does: the correct
byte spelling of indices 4-7 depends on whether the instruction being
described carries a REX prefix.