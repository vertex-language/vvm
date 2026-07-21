# isa/aarch64

`github.com/vertex-language/vvm/isa/aarch64`

The static, data-only description of the A64 (64-bit ARM) instruction set: register identity, condition-code numbering, and the bit-layout/opcode↔mnemonic tables a generic assembler needs to turn an instruction stream into machine words. `isa/aarch64/encoder` (byte emission, see `encode.go`/`inst.go`) builds on it directly, as will `lower/aarch64`'s instruction selection once it exists.

```go
import isaaarch64 "github.com/vertex-language/vvm/isa/aarch64"
```

There is no control flow of consequence in this package — only declarations, plus a single mechanical reverse-index map built once in `init()`. Nothing here encodes or decodes an instruction stream; that's `isa/aarch64/encoder`'s job on the way in, and a future disassembler's on the way out.

---

## Package layout

```
isa/aarch64/
├── registers.go   Reg, the thirty-one GPR constants (X0..X30), the shared
│                  SP/ZR encoding-31 value, FP/LR/IP0/IP1/PR aliases, and
│                  the width-indexed XNames/WNames tables
├── condcodes.go   Cond* constants (A64 4-bit cond field), CondNames,
│                  CondMnemonics, CondName-equivalent lookup, Invert()
├── encoding.go    Sf/Idx64/SizeBits width helpers, the BFM base words plus
│                  PackBFM, and PackPair for STP/LDP
└── opcodes.go     DPImmOpcodes/DPRegOpcodes/DP2Opcodes/DP1Opcodes,
                   LdClasses/StClasses, and the fixed Op* words for forms
                   outside a systematic group
```

---

## The test: ISA fact vs. lowering decision

Everything in this package is true of A64 independent of any particular compiler's choices — a register's 5-bit encoding, which `cond` field value means "unsigned higher," which `/opcode` subfield selects `udiv` under the data-processing-2-source group, how an `STP`/`LDP` pair instruction packs its scaled displacement. None of it depends on how a future `lower/aarch64` decides to allocate registers, build a frame, or select instructions.

The dividing line: if a fact would still be true even if `lower/aarch64` were deleted and rewritten from scratch with a completely different register-allocation strategy, it belongs here. If it's a decision *this compiler* makes about how to use those facts — which registers are scratch vs. callee-saved, how stack slots are laid out, which mnemonics an inline-asm table supports — it belongs in `lower/aarch64` instead, the same split `isa/x86`, `isa/x86_64`, and `isa/arm` draw against their own `lower/<arch>` packages.

`isa/aarch64/encoder` already leans on this split even without `lower/aarch64` existing yet: `Encode` turns a fully resolved `Inst` stream into machine words with no prologue/epilogue splicing of its own — a caller wanting a frame builds it out of ordinary `stp_pre`/`mov_r_sp`/`sub_sp`/`mov_to_sp`/`ldp_post`/`ret` `Inst` values itself, the same way `isa/arm/encoder` treats prologue construction as the caller's concern rather than the assembler's.

---

## Registers (`registers.go`)

`Reg` is a `byte`-sized physical A64 general-purpose register encoding, 0-31. `X0`..`X30` name the thirty-one addressable GPRs directly; encoding 31 is architecturally context-dependent rather than a fixed register, so `SP` and `ZR` are declared as the same value — which meaning applies is determined by which field of which instruction carries it, not by anything this package decides.

`FP` and `LR` alias `X29`/`X30`, the AAPCS64 names — and for `LR` specifically, more than a naming convention: `BL`/`BLR` write it implicitly and a bare `RET` reads it implicitly, so that fact is true of the architecture itself. `IP0`/`IP1`/`PR` alias `X16`/`X17`/`X18`, the intra-procedure-call scratch registers and platform register AAPCS64 reserves; whether this compiler's own pipeline actually uses them that way is a `lower/aarch64` decision, not one this package makes.

`XNames`/`WNames` give each register's 64-bit (`Xn`) and 32-bit (`Wn`) assembly spelling for encodings 0-30. Encoding 31's spelling depends on which of SP/ZR applies at the call site, so it isn't folded into these arrays — `XName(r, isSP)`/`WName(r, isSP)` handle it explicitly, picking `sp`/`wsp` or `xzr`/`wzr` accordingly.

## Condition codes (`condcodes.go`)

The sixteen `Cond*` constants (`CondEQ`..`CondNV`) are the condition-code field carried by `B.cond`, the `CSET`/`CSEL`/`CSINC` family, and `CCMP`/`CCMN`. `CondNames` gives the mnemonic suffix for each value; `CondMnemonics` is the reverse index, built once in `init()`, so a future inline-asm parser reads it directly rather than hand-typing a second copy of the same table — a duplication this package's own doc comment calls out as the failure mode `isa/x86_64`'s README documents for its `jccTable`.

`Invert(cc)` returns the complementary condition — a fixed architectural pairing where bit 0 of the 4-bit field toggles sense for every pair below `AL`/`NV`, not a policy choice. `AL` and `NV` have no complement and are returned unchanged.

## Encoding primitives (`encoding.go`)

`Sf(sz)` returns the "sf" (size flag) bit, positioned at its fixed bit 31, for a 32-bit (`sz==4`) or 64-bit (`sz==8`) data-processing form. `Idx64(sz)` picks which half of a `[W-form, X-form]` opcode-table entry (as used by `DPImmOpcodes`, `DPRegOpcodes`, `DP1Opcodes`) applies for a given size — the two are separate helpers because not every caller needs a full instruction word back, just an index. `SizeBits(sz)` packs a load/store-exclusive/ordered access size (1/2/4/8 bytes) into its fixed 2-bit `size` field at bits `[31:30]`, returning an error for anything else.

The `OpUBFMW`/`OpUBFMX`/`OpSBFMW`/`OpSBFMX` base words are the bitfield-move family that `UXTB`/`UXTH`/`SXTB`/`SXTH`/`SXTW` and the `LSR`/`LSL`/`ASR`-by-immediate pseudo-ops are all specific `(immr, imms)` encodings of. `PackBFM` lays out one such instruction word from a base plus the two 6-bit shift/width fields and the usual `Rn`/`Rd` register fields; picking which `(immr, imms)` pair a given pseudo-op needs is left to the caller, since that depends on the pseudo-op and operand width rather than anything true of the bit layout itself. `PackPair` lays out one `STP`(pre-index)/`LDP`(post-index) 64-bit pair instruction word from a base, the pre-scaled 7-bit displacement, and the `Rt`/`Rt2`/`Rn` register fields.

## Opcode tables (`opcodes.go`)

- **`DPImmOpcodes`** (`map[string][2]uint32`) — the data-processing-immediate `add`/`adds`/`sub`/`subs` family, `[W-form, X-form]` base words; the 12-bit unsigned immediate and `Rn`/`Rd` are filled in by the caller.
- **`DPRegOpcodes`** (`map[string][2]uint32`) — the data-processing-register (shifted) `add`/`adds`/`sub`/`subs`/`and`/`orr`/`eor`/`bic` family, `[W-form, X-form]` base words; `Rm`/`Rn`/`Rd` filled in by the caller.
- **`OpAddExtX`/`OpSubExtX`** — the extended-register `ADD`/`SUB` form used for SP-relative addresses that overflow the immediate form's 24-bit range. X-form only, since this compiler never needs a W-form SP computation.
- **`OpMovReg`/`OpMovRegX`/`OpMvnReg`/`OpMvnRegX`/`OpNegReg`/`OpNegRegX`** — `mov_r`/`mvn`/`neg`'s shifted-register forms with `Rn` fixed to `ZR`, named directly as a mechanical specialization of the `DPRegOpcodes`/`DP1Opcodes`-shaped instructions rather than composed at every call site.
- **`DP2Opcodes`** (`map[string]uint32`) — the 3-bit opcode subfield shared by the data-processing-2-source family (`udiv`/`sdiv`/`lslv`/`lsrv`/`asrv`/`rorv`); the shared base word (`OpDP2Base`) is separate since it also carries the sf bit.
- **`DP1Opcodes`** (`map[string][2]uint32`) — the data-processing-1-source family (`rbit`/`rev16`/`rev`/`clz`), `[W-form, X-form]` base words.
- The 3-source multiply family (`OpMul`/`OpMSub`/`OpSMulH`/`OpUMulH`/`OpSMull`/`OpUMull`) — fixed base words with `Rd`/`Rn`/`Rm`/`Ra` filled in by the caller. `MUL` is `MADD` with `Ra=ZR` baked into the base word rather than named as a separate opcode.
- **`OpCSet`/`OpCSel`** — `CSET` is `CSINC Rd, ZR, ZR, invert(cond)` with a fixed base word; `CSEL` carries the sf bit itself via `Sf`.
- **`LdStClass`** and **`LdClasses`/`StClasses`** (`map[int]LdStClass`, keyed by access size 1/2/4/8) — each entry pairs one integer load or store's scaled unsigned-immediate form with its unscaled (`LDUR`-style) form and the scale the scaled form's immediate is a multiple of.
- **`OpLdrbReg`/`OpStrbReg`** — byte load/store, register-offset form (`LDRB`/`STRB Wt, [Xn, Xm]`).
- The load-acquire/store-release/exclusive family (`OpLdar`/`OpStlr`/`OpLdaxr`/`OpStlxr`/`OpClrex`/`OpDmb`) — the shared 2-bit size field is packed separately via `SizeBits` rather than baked into each constant.
- Branch/system fixed words (`OpB`/`OpBL`/`OpBCond`/`OpCBase`/`OpBLR`/`OpBR`/`OpRet`/`OpSVC`/`OpBrk`) — `B`/`BL`/`B.cond`/`CBZ`/`CBNZ` leave their label/symbol-relative field for a fixup or patch to fill in; `RET`'s implicit-`X30` form and `SVC`/`BRK`'s fixed encodings need nothing further.
- **`OpMovnX`/`OpMovzX`/`OpMovkX`** — `MOVZ`/`MOVN`/`MOVK`, X-form only, since this compiler never needs a W-form 64-bit-immediate sequence. The 2-bit `hw` shift-amount field and 16-bit immediate are filled in by the caller.
- **`OpSTPPre64`/`OpLDPPost64`** — the `STP`(pre-index)/`LDP`(post-index) pair used to save/restore FP/LR as ordinary instructions, since the encoder doesn't splice a frame in automatically; `imm7 = disp/8`, packed by `PackPair`.