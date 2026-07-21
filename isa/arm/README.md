# isa/arm

`github.com/vertex-language/vvm/isa/arm`

The static, data-only description of the A32 (32-bit ARM) instruction set: register identity, condition-code numbering, rotated-immediate and shift bit encodings, and the opcode↔mnemonic correspondence for data-processing ops. `isa/arm/encoder` (the generic A32 assembler, see `encode.go`/`inst.go`) builds on it directly, as will `lower/arm`'s instruction selection once it exists.

```go
import isaarm "github.com/vertex-language/vvm/isa/arm"
```

There is no control flow of consequence in this package — only declarations, plus a couple of mechanical reverse-index maps built once in `init()`. Nothing here decodes or encodes an instruction stream; that's `isa/arm/encoder`'s job on the way in, and a future disassembler's on the way out.

---

## Package layout

```
isa/arm/
├── registers.go   Reg, the sixteen GPR constants (R0..R10, RFP/RIP/RSP/RLR/RPC),
│                  RNone, and the regName spelling table
├── condcodes.go   Cond* constants (A32 cond field, bits 31:28), condName,
│                  CondName()
├── encoding.go    PackImm12/UnpackImm12 (rotated-immediate Operand2),
│                  SplitImm16 (MOVW/MOVT halves), PCBias
└── opcodes.go     DPOp/ShiftOp tables plus their By-lookup accessors (built
                   from reverse-index maps in init()), and the fixed Base*
                   instruction words for single-shape forms
```

---

## The test: ISA fact vs. lowering decision

Everything in this package is true of A32 independent of any particular compiler's choices — a register's encoding number, which `tttn` bit pattern means "unsigned higher," which data-processing opcode selects `bic`, how a rotated immediate is packed. None of it depends on how a future `lower/arm` decides to allocate registers, build a frame, or select instructions.

The dividing line: if a fact would still be true even if `lower/arm` were deleted and rewritten from scratch with a completely different register-allocation strategy, it belongs here. If it's a decision *this compiler* makes about how to use those facts — which registers are scratch vs. callee-saved, how stack slots are laid out, which mnemonics an inline-asm table supports — it belongs in `lower/arm` instead, the same split `isa/x86` and `isa/x86_64` draw against their own `lower/<arch>` packages.

`isa/arm/encoder` already leans on this split even without `lower/arm` existing yet: it's a generic pseudo-instruction assembler with no notion of a stack frame or calling convention — a caller wanting a prologue/epilogue builds it out of ordinary `push`/`mov`/`sub`/`pop`/`b` `Inst` values itself. `RIP` (r12) being reserved as encoder-internal immediate-materialization scratch is one such caller-facing convention; it's real, but it's `isa/arm/encoder`'s convention, not an A32 fact, so it's documented there rather than here.

---

## Registers (`registers.go`)

`Reg` is a `byte`-sized physical A32 general-purpose register identifier: `R0`..`R10` (0-10), `RFP` (11, frame pointer), `RIP` (12, intra-procedure-call scratch), `RSP` (13, stack pointer), `RLR` (14, link register), `RPC` (15, program counter), plus `RNone` (`0xFF`) as the "absent" sentinel.

The single `regName` table gives each register's canonical assembler spelling. Indices 11-15 use their AAPCS/assembler names (`fp`/`ip`/`sp`/`lr`/`pc`) rather than `r11`-`r15` — the same convention every A32 assembler and disassembler follows, mirroring how `isa/x86`'s `reg32` table spells encoding 4 `"esp"` rather than a numbered form. `(Reg).String()` looks up that spelling, for diagnostics; there's no width parameter here the way `isa/x86`/`isa/x86_64` need one, since A32 GPRs don't have sub-register width forms.

## Condition codes (`condcodes.go`)

The fifteen `Cond*` constants (`CondEQ`..`CondAL`) are the A32 cond field carried in bits 31:28 of every conditionally executed instruction word. They're left as untyped constants rather than a distinct `Cond` type, matching how both the encoder (an `Inst.CC` byte) and a future decoder (a fetched word's top nibble) actually use them — as plain byte values. `CondName(cc)` returns the mnemonic suffix (e.g. `0x0` → `"eq"`, so a conditional branch prints `beq`, a conditional move `moveq`); `condName`'s sixteenth entry is the empty string, reserved for the `AL`-adjacent encoding this backend never emits a suffix for.

## Encoding primitives (`encoding.go`)

`PackImm12`/`UnpackImm12` convert to and from A32's rotated-immediate `Operand2` form — an 8-bit value rotated right by an even count — the shape every data-processing instruction's immediate operand accepts; `PackImm12` reports `ok == false` when no rotation makes the value fit. `SplitImm16` splits a 16-bit value into the `imm4:imm12` halves that `MOVW`/`MOVT` each pack into their own instruction word, so materializing a full 32-bit immediate takes one call per half. `PCBias` is the fixed 8-byte lead A32's PC reads ahead of the currently executing instruction — the bias every PC-relative fixup (branch offsets, symbol-relative `MOVW`/`MOVT` pairs) measures from.

## Opcode tables (`opcodes.go`)

- **`DPOps`** — the ten data-processing mnemonics this backend names (`and`/`eor`/`sub`/`rsb`/`add`/`tst`/`cmp`/`cmn`/`orr`/`bic`), each carrying its 4-bit opcode field (bits 24:21) and a `Cmp` flag marking the compare-only forms (`tst`/`cmp`/`cmn`) that always set flags and never write a destination register. Looked up by name via `DPByName`.
- **`ShiftOps`** — the four shift/rotate mnemonics (`lsl`/`lsr`/`asr`/`ror`), each just its 2-bit shift-type field (bits 6:5), shared by the register-shifted-register form and by standalone shift instructions. `ShiftByName`.

Both follow the same shape: a small ordered slice as the source of truth, with a `*ByName` map existing purely as a mechanical, `init()`-built reverse index.

The remaining `Base*` constants are fixed A32 instruction words — `mov`/`mvn`/`movw`/`movt`/`movcc` in their several shapes, `mul`/`mls`/`umull`/`smull`/`udiv`/`sdiv`, `clz`/`rbit`/`rev`/`uxtb`/`uxth`/`sxtb`/`sxth`, the `ldr`/`str` family (word, byte, halfword, signed, register-offset), `ldrex`/`strex`/`clrex`/`dmb`, branches (`b`/`bcc`/`bl`/`blx`/`bx`), `push`/`pop` (`stmdb`/`ldmia` with writeback), and `udf` — for forms outside a systematic group, each with its register-field bit positions noted in a comment. That placement is itself an ISA fact (true for any A32 assembler), just not one that fits a uniform `Name`-keyed table the way the DP/shift forms above do, since each instruction's field layout differs.