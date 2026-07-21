# isa/x86

`github.com/vertex-language/vvm/isa/x86`

The static, data-only description of the IA-32 (x86, 32-bit) instruction set: register identity, condition codes, ModRM/SIB bit layout, and the opcode↔mnemonic correspondence. Both `lower/x86` (instruction selection) and `isa/x86/encoder` (byte emission) build on it, as does the debug disassembler in `format/asm/x86/text`.

```go
import isax86 "github.com/vertex-language/vvm/isa/x86"
```

There is no control flow of consequence in this package — only declarations, plus a handful of mechanical reverse-index maps built once in `init()`. Nothing here encodes or decodes an instruction stream; that's `isa/x86/encoder`'s job on the way in and `format/asm/x86/text`'s on the way out.

---

## Package layout

```
isa/x86/
├── registers.go   Reg, the eight GPR constants (REAX..REDI), RNone, and
│                  the width-indexed reg32/reg16/reg8 name tables
├── condcodes.go   Cond* constants (Intel tttn 4-bit encoding), condName,
│                  CondName()
├── encoding.go    PackModRM/UnpackModRM, PackSIB/UnpackSIB, ScaleBits,
│                  legacy prefix byte constants
└── opcodes.go     AluOp/ShiftOp/Group3Op tables plus their By-lookup
                   accessors, built from reverse-index maps in init()
```

---

## The test: ISA fact vs. lowering decision

Everything in this package is true of IA-32 independent of any particular compiler's choices — a register's encoding number, which condition code bit pattern means "signed less than," which `/ext` digit selects `neg` under the `0xF7` group. None of it depends on how `lower/x86` decides to allocate registers, build a frame, or select instructions.

The dividing line: if a fact would still be true even if `lower/x86` were deleted and rewritten from scratch with a completely different register-allocation strategy, it belongs here. If it's a decision *this compiler* makes about how to use those facts — which registers are scratch vs. callee-saved, how stack slots are laid out, which mnemonics a curated inline-asm table supports — it belongs in `lower/x86` instead. `lower/x86`'s own README points back to this one for the same reason: the split is deliberately the only one of its kind in the `lower/<arch>` packages that's actually load-bearing, everything else (`mcode`/`abi`/`regalloc`-style splits) having been tried and found to buy no independence.

One naming wrinkle this discipline produces: `Group3Ops` uses the real assembly mnemonics `mul`/`imul` for the one-operand, EDX:EAX-writing group-3 forms, even though `lower/x86/mcode` internally spells these `mul32`/`imul32` to disambiguate from the unrelated two/three-operand `imul` it also emits. That disambiguation is `mcode`'s own routing concern, translated at its call site into the real mnemonic before it ever reaches this package's table — this package doesn't know or care that the ambiguity exists downstream.

---

## Registers (`registers.go`)

`Reg` is a `byte`-sized physical IA-32 GPR identifier: `REAX`..`REDI` (0-7, matching the ModRM/SIB encoding order), plus `RNone` (`0xFF`) as the "absent" sentinel used throughout `isa/x86/encoder` and `lower/x86` for optional base/index operands.

Three width-indexed tables (`reg32`, `reg16`, `reg8`) give each register's assembly spelling at 32-, 16-, and 8-bit widths; `(Reg).Name(widthBits)` looks one up, defaulting to the 32-bit spelling for any width other than 8 or 16. Byte-register naming is the one irregular case worth knowing: indices 4-7 name AH/CH/DH/BH rather than a low byte of ESP/EBP/ESI/EDI — reaching SPL/BPL/SIL/DIL would require a REX prefix, which this 32-bit-only backend never emits, so that distinction simply isn't representable here. `(Reg).String()` is the width-free 32-bit spelling, for diagnostics that don't care about operand width (e.g. naming which physical register an inline-asm binding resolved to).

## Condition codes (`condcodes.go`)

The sixteen `Cond*` constants (`CondO`..`CondG`) are Intel's 4-bit `tttn` encoding, shared verbatim across `0F 8x` (`jcc`), `0F 9x` (`setcc`), and `0F 4x` (`cmovcc`). They're left as untyped constants rather than a distinct `Cond` type, matching how both `isa/x86/encoder` (an `Inst.CC` byte) and the disassembler (a decoded ModRM/opcode nibble) actually use them — as plain byte values, not a type worth wrapping. `CondName(cc)` returns the mnemonic suffix (e.g. `4` → `"e"`, so `jcc` prints `je`, `setcc` prints `sete`, `cmovcc` prints `cmove`).

## Encoding primitives (`encoding.go`)

`PackModRM`/`UnpackModRM` and `PackSIB`/`UnpackSIB` convert between a byte and its three bit-packed fields (`mod/reg/rm` and `scale/index/base` respectively) — pure bit arithmetic, used by both the encoder assembling bytes and the disassembler pulling them back apart. `ScaleBits` maps a SIB scale factor (1, 2, 4, or 8 — 0 and 1 both mean "no scaling," and share the same 2-bit encoding) to its packed field value, reporting `ok == false` for anything else. The legacy prefix byte constants (`Prefix66`, `PrefixF0`, `PrefixF3`) round out the set of encoding-level facts both consumers need named rather than left as bare hex literals.

## Opcode tables (`opcodes.go`)

Three parallel tables, each with a `Name`-keyed and opcode-keyed reverse index built in `init()`:

- **`AluOp`** — the six two-operand ALU instructions (`add`/`or`/`and`/`sub`/`xor`/`cmp`), each carrying its MR opcode (`r/m, r`), RM opcode (`r, r/m`), and its `/ext` digit under the `0x81 r/m,imm32` group. Looked up by name (`AluByName`) or by any of its three opcode forms (`AluByMR`/`AluByRM`/`AluByExt`).
- **`ShiftOp`** — the five shift/rotate mnemonics (`rol`/`ror`/`shl`/`shr`/`sar`), each just a `/ext` digit shared by the `0xC0`/`0xC1` (immediate-count) and `0xD2`/`0xD3` (count-in-CL) opcode groups. `ShiftByName`/`ShiftByExt`.
- **`Group3Op`** — the six single-operand instructions under the `0xF7` group ("group 3" in Intel's manual): `not`/`neg`/`mul`/`imul`/`div`/`idiv`, each just a `/ext` digit. `Group3ByName`/`Group3ByExt`.

All three follow the same shape deliberately: a small ordered slice as the source of truth (readable, easy to diff), with the `by*` maps existing purely as a mechanical, `init()`-built reverse index rather than a second hand-maintained source.