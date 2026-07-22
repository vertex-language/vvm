# isa/arm

​```go
import isaarm "github.com/vertex-language/vvm/isa/arm"
​```

Static, data-only description of the 32-bit ARM (A32, "ARM state")
instruction set: register identity, the universal condition field,
instruction-word layout, the modified-immediate encoding, and the
opcode<->mnemonic correspondence.

This is the AArch32 counterpart to `isa/aarch64`. It covers the **A32**
encoding only — not T32/Thumb and not A64, which are distinct instruction
sets. It serves both the `arm` (little-endian) and `armeb` (big-endian)
Go targets: endianness governs how data words sit in memory, not these
tables, so one description covers both.

Two things import this package:

- **`isa/arm/encoder`** — the generic assembler. It turns a fully-resolved
  `Inst` stream into machine words using the tables and helpers here.
- **`format/asm/arm/text`** — the debug disassembler/printer. It decodes
  words and looks up mnemonics using the same tables, in reverse.

Nothing here has control flow of consequence: constants, lookup tables,
small pure functions, and the reverse-index maps built once in `init()`. If
a change needs a design decision rather than a citation, it belongs
somewhere else.

## What belongs here

The test: **is this a fact about the A32 machine, true regardless of what
any particular compiler chooses to emit?**

What makes A32 different from the x86 packages, and drives most of what this
package pins down:

- **Every instruction is conditional.** Bits 31:28 are a 4-bit condition
  field (`condcodes.go`) checked against the CPSR flags on nearly every
  encoding. Unlike x86, where a condition nibble belongs only to
  jcc/setcc/cmovcc, here it is a property of the whole instruction set, so
  `SetCond`/`Cond` in `encoding.go` are the one field accessor that is
  universal. The tttn-style negate-by-XOR-1 holds, with one wrinkle x86
  lacks: `al`/`nv` (14/15) are the final pair, so `NegateCond(CondAL)` is
  the reserved `nv` code, not a usable "never" (see `CondNV`).
- **Register fields are a flat four bits.** All sixteen GPRs are named by a
  contiguous 4-bit field (`Reg.Field`), with no REX-style low-3/high-bit
  split — there is deliberately nothing here resembling `isa/x86_64`'s
  `Low3`/`NeedsREXBit`.
- **The PC is a register.** `r15` *is* the program counter and appears
  directly in register fields (`RPC`); `r14`/`RLR` is the hardware link
  register; `r13`/`RSP` is the stack pointer by convention. `registers.go`
  records which of those roles are architectural and which are convention.
- **Immediates are rotated bytes.** A data-processing immediate is an 8-bit
  value rotated right by an even amount (`EncodeModImm`/`DecodeModImm`),
  the A32 analog of x86's `FitsImm32` — but the representable set is
  scattered, not a contiguous range.

Concretely, this package owns register identity and naming (`registers.go`),
the condition field and its canonical/synonym spellings (`condcodes.go`),
instruction-word layout — width, condition-field position, the
modified-immediate codec, and the branch word-offset codec (`encoding.go`) —
and the opcode/mnemonic tables: the sixteen data-processing operations, the
barrel-shifter shift types, the load/store flag bits, and the LDM/STM
addressing modes with their stack-name synonyms (`opcodes.go`).

That includes facts nobody currently exercises: `DataProcOps` lists all
sixteen operations including `rsc` and the test-only `teq` whether or not
any lowering emits them, because a disassembler must still name them.
`CondName`/`ParseCond` round-trip the *canonical* spelling only — the
synonyms (`hs`/`lo`, `sp`/`lr`/`pc`, `asl`) are call-site conveniences, not
a second textual vocabulary, matching how the x86 packages treat their
synonym constants.

## What doesn't belong here

- **Anything that's a choice rather than a fact.** How to build a constant
  that fails `EncodeModImm` (a MOV/MOVT pair vs. a literal-pool load),
  which addressing mode to pick, how the +8 PC-read offset turns a target
  into a branch field — that's `lower/arm` and the encoder's `Encode`, not
  this package. This package supplies the menu; it doesn't order from it.
- **Unresolved/symbolic state.** A not-yet-placed operand is a `lower/arm`
  concept that only collapses to this package's shapes once every field is
  a real register or immediate.
- **Printer formatting policy.** `format/asm/arm/text` may reuse these
  tables, but disassembly output format, whether to print `al`, and whether
  to prefer `sp`/`lr`/`pc` over `r13`/`r14`/`r15` are printer decisions.
- **Anything with control flow of consequence.** If a proposed addition
  needs a conditional beyond "is this input in range", it's saying
  something about *how to use* an ISA fact, and belongs one layer up.

## Diagnostic behavior

Lookup functions are total, not partial: `CondName` and `ShiftName` return
`"?"` for an out-of-range code, `Reg.Name` returns `"?"` for a non-GPR, and
`String()` methods never panic. They are called from disassembly/diagnostic
paths, where describing a malformed operand should say "there isn't one"
rather than take the process down.