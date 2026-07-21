# lower/aarch64

`github.com/vertex-language/vvm/lower/aarch64`

Lowers a verified `vir.Module` (arch `"aarch64"` or `"aarch64_be"` — 64-bit ARM, A64) into concrete machine code: a `Program` of function bytes, global data, and relocations, ready for an object writer. This package assumes its input already passed `vir.Verify` — it does not re-check §9 obligations itself.

```go
import "github.com/vertex-language/vvm/lower/aarch64"
```

---

## Package layout

```
lower/aarch64/
├── aarch64.go    Package doc, Arch ("aarch64" vs "aarch64_be"), Program/
│                 Func/Global, Lower() entry point, the lowerer struct and
│                 its symbol-kind table
├── isel.go       Instruction selection: one vir.Function's body -> []Inst
│                 (fnLower/lowerFunc), operand load/store helpers, calls
│                 (selCall/selTailCall), syscalls (selSyscall), overflow
│                 ops (selOverflow), and terminators
├── asm.go        Inline-assembly lowering: LowerBlock, the native-dialect-
│                 only curated mnemonic table (lowerAsmLine), in/out
│                 binding load/store
├── frame.go      Frame: FP-relative stack layout, BuildFrame
├── encode.go     assemble: prologue/epilogue, OSlot resolution, and the
│                 final translation into isa/aarch64/encoder's Inst/Opr
├── callconv.go   StageBytes/RegArgs: AAPCS64 outgoing-argument-area layout
├── syscall.go    syscallConvention: per-target-OS syscall trap ABI
├── layout.go     Layout: AAPCS64 aggregate size/alignment/field-offset rules
├── registers.go  physicalSlot/resolveReg: vir register-table name ->
│                 isa/aarch64/encoder.Reg
├── globals.go    lowerGlobal/dataw: global initializer -> data bytes + fixups
└── opr.go        Opr/Inst: the pre-encoding instruction vocabulary
```

---

## Design: one flat package

Instruction selection, AAPCS64 frame/call layout, slot resolution, syscall conventions, and inline-assembly lowering are all facets of a single lowering pipeline that runs in a fixed order and shares one `Opr`/`Inst` vocabulary (`opr.go`). They live in one package deliberately, for the same reason `lower/arm`, `lower/x86_64`, and `lower/x86` do: splitting them into separate packages buys no independence, only duplicate copies of `isa/aarch64`'s register constants under new names.

Register identity and condition codes come directly from `isa/aarch64/encoder` (`encoder.X0`, `encoder.FP`, `encoder.CondEQ`, ...) — nothing here re-declares them. This package knows A64 instruction selection and the AAPCS64 calling convention (frame layout, call-argument layout, struct/array layout); it knows nothing about ELF/Mach-O — that's `object`/`objectwriter`'s job.

The `"aarch64"`/`"aarch64_be"` split (`Arch.Big()`, `aarch64.go`) is the narrowest possible: both architectures share instruction semantics, AAPCS64, layout, and fixup shapes, and differ only in the byte order of global scalar data (`globals.go`'s `dataw.scalar`). Code is always little-endian in both — A64 instruction fetch is architecturally LE regardless of data endianness — so `assemble`/`encode.go` never consults `Big()`.

---

## `Lower`

```go
func Lower(m *vir.Module, arch Arch) (*Program, error)
```

`arch` must be `ArchAArch64` or `ArchAArch64BE`, and if `m.Target` is set, its `Arch` string must match (§10.6: the two must agree). `Lower` walks `m.Globals` (via `lowerGlobal`) and `m.Functions` (via `lowerFunc`) in declaration order and returns a self-contained `Program`:

```go
type Program struct {
    Arch    Arch
    Funcs   []Func
    Globals []Global
}

type Func struct {
    Name   string
    Code   []byte
    Align  uint32
    Export bool
    Fixups []Fixup
}
```

`Fixup`/`FixupKind` are a type alias of `isa/aarch64/encoder`'s relocation vocabulary (`FixupCall26`, `FixupMovzG3`/`FixupMovkG2`/`FixupMovkG1`/`FixupMovkG0`, `FixupAbs64`, ...) — `encoder.Encode` is the only thing that produces them, so this is a hop to the single source, not a hand-copied second table.

```go
m := vir.NewModule("add_example")
m.SetTarget("aarch64", "linux", "gnu")
// ... build m via vir's FunctionBuilder ...
if err := vir.Verify(m); err != nil {
    panic(err)
}
prog, err := aarch64.Lower(m, aarch64.ArchAArch64)
```

---

## Pipeline (per function)

`aarch64.go`'s `lowerFunc` runs the stages in this fixed order:

1. **Type fixation** (`isel.go`'s `typeFunc`) — mirrors the verifier's result-type computation for the operators this backend actually supports (integers/pointers up to 64 bits; wider or float/vector types are rejected here with an explicit TODO error), including asm `out` bindings (§6 rule 6: a first-seen out ident's type is inferred from its bound register's width).
2. **Parameter spill** — the first eight register-passed parameters (`R(encoder.Reg(i))`, normalized to their declared width) are stored into their home slots up front; stack parameters (9th onward) are loaded from `[fp+16+8k]` and spilled the same way. `byval`/`sret` parameters are rejected outright (AAPCS64 aggregate-passing + x8 indirect-result TODO).
3. **Instruction selection** (`selInst`/`selTerm`/`selCall`/`selTailCall`/`selSyscall`) — walks every block and line, emitting `Inst`s over the X0–X15 scratch set (X15 dedicated as `rIDX`, the loop index for memcopy/memset/memmove's byte-copy loops — a lowering choice, not an ISA fact, so it's declared in `isel.go` rather than `isa/aarch64`). Every named vir value is materialized through its own 8-byte stack slot (`Slot`) rather than kept live across instructions — no cross-instruction register residency.
4. **Frame building** (`frame.go`'s `BuildFrame`) — assigns every `OSlot` operand a distinct 8-byte FP-relative home; every parameter, and every value first produced by an asm out-binding, gets its own slot the same uniform way (unlike `lower/arm`, where only named vir values do).
5. **Assembly** (`encode.go`'s `assemble`) — splices the `stp_pre`/`mov_r_sp`/`sub_sp` prologue and its `mov_to_sp`/`ldp_post` epilogue counterpart in as ordinary instructions (`isa/aarch64/encoder` splices nothing itself), expands the `epi_ret`/`epi_jmp_sym`/`epi_jmp_r` pseudo-ops, resolves every `OSlot` to its concrete `[fp+off]` operand, and hands the result to `isa/aarch64/encoder` for byte emission.

## `Opr`/`Inst` (`opr.go`)

`Opr` is the operand vocabulary instruction selection builds against: `OReg`, `OImm` (optionally a symbol), `OMem` (`[Base+Disp]`), and `OSlot` — a vir value's not-yet-placed stack home, the one addition over `isa/aarch64/encoder.Opr` and deliberately absent there. `resolveOpr` in `encode.go` is what erases the difference before final assembly; a `Slot` with no entry in the built `Frame` is reported as an error (`"encode: value %q has no frame slot"`), not panicked. `Inst` also carries three function-exit markers — `epi_ret`, `epi_jmp_sym`, `epi_jmp_r` — that `isel.go` emits at every return/tailcall site instead of a bare `ret`/`b_sym`/`br_r`; `encode.go`'s `toEncoderInsts` is the only thing that expands one of these into the real epilogue followed by the plain exit instruction `isa/aarch64/encoder` actually knows about.

## ABI

**AAPCS64.** First eight integer/pointer arguments in X0–X7 (`callconv.go`'s `RegArgs`); remaining arguments on the stack in 8-byte slots, staged and reloaded by `selCall`/`selTailCall`, with `StageBytes` rounding the staged area up to 16 bytes. Result in X0. Caller cleans up the staged argument area.

**Frame layout** (`frame.go`) grows down from `[fp+16+8k]` incoming stack args, past the saved LR (X30) and saved FP (X29), to one 8-byte home slot per vir value at `[fp-8-4k]`. `Local` is rounded up to 16 bytes so SP stays AAPCS64 16-byte aligned at call boundaries.

**Callee-saved registers are never touched.** The prologue only pushes `{fp, lr}`; X19–X28 don't need saving because the scratch set instruction selection uses (X0–X15) is entirely caller-saved, and every named value lives in its own slot rather than a register across instructions.

**Tail calls** (`selTailCall`) support only the shape where all arguments fit in X0–X7; anything requiring stack argument slots is rejected (`"tailcall with %d args exceeds the x0-x7 register set"`).

**Aggregate layout** (`layout.go`) follows AAPCS64 §7.1: fields at increasing offsets, each at its natural alignment (capped at 16, AAPCS64's max fundamental alignment), trailing padding to the largest field alignment. `usize`/pointers are 8 bytes.

**Signed division traps INT_MIN / -1** (§6.1) on the 32- and 64-bit widths, unlike `lower/arm`/`lower/x86_64`/`lower/x86`: `sdiv`/`srem` explicitly compare the divisor against -1 and the dividend against the type's minimum value, trapping (`brk`) on that combination rather than silently wrapping. Division and remainder by zero also trap, on every width.

**Syscalls** (`syscall.go`) are per-target-OS: Linux and FreeBSD share the same convention — number in X8, up to six args in X0–X5, result in X0. `windows`/`none` are deliberately unregistered, so `lookupSyscall` reports `ok == false` and `selSyscall` surfaces that as an explicit lowering error.

## Inline assembly (`asm.go`)

`LowerBlock` only accepts `vir.DialectNative` — the only dialect `ir/vir/targets.go` registers for `aarch64`/`aarch64_be` — and rejects anything else outright, unlike the two-dialect (Intel/AT&T) split in `lower/x86_64`/`lower/x86`. Coverage is a curated mnemonic set, not the full A64 assembly language: `mov`/`mvn`/`neg`, `add`/`sub`/`and`/`orr`/`eor` (2-operand accumulate forms only — genuine 3-operand forms aren't supported yet), `mul`/`udiv`/`sdiv`, `cmp`, `ldr`/`str`, `b`/`cbz`/`cbnz`, `svc`, `brk`, `ret`, `nop`. Memory operands support only `reg` and `reg+disp`/`reg-disp` text — this package's own concrete grammar for `vir.AsmOperand.Memory`, parsed by `parseAsmMemory`. In-bindings are loaded before the block body and out-bindings stored after, both through the same `Slot` mechanism ordinary instruction selection uses, so no bridging is required between `asm.go` and `isel.go`. Asm-local labels are namespaced (`asmLabel`, `.asm.` prefix) so they can't collide with compiler-generated labels (§4 label isolation).

## Known gaps

- **`byval`/`sret` arguments** aren't lowered, at either function-definition or call-site position (AAPCS64 aggregate passing + x8 indirect-result TODO).
- **i128 named values** — `checkValueType` rejects any int wider than 64 bits (register-pair support TODO).
- **Floats and vectors** — no FP/SIMD tier yet; every float/vector op, including float bitcast and float compares, returns an explicit "not lowered" error.
- **Saturating arithmetic** (`*add_sat`/`*sub_sat`) is unimplemented.
- **`popcnt`** has no baseline scalar A64 instruction and is rejected outright (FEAT_CSSC `CNT` / NEON tier TODO, §10.4).
- **Narrow atomics** — `atomic_add`/`atomic_sub`/`atomic_and`/`atomic_or`/`atomic_xor`/`atomic_xchg` only support 32/64-bit widths.
- **`cmpxchg` narrower than 32 bits** isn't lowered.
- **TLS globals** — taking the address of a `TLS` global is rejected (`TPIDR_EL0` + TLS relocations TODO).
- **`f16` global initializers** aren't emitted (`globals.go`'s `dataw.lit`).
- **Frame-growing tail calls** — `selTailCall` only supports the case where every argument fits in X0–X7; anything requiring stack argument slots is rejected.
- **Inline-asm 3-operand forms** — `add`/`sub`/`and`/`orr`/`eor` in inline asm are 2-operand accumulate forms only.
- **Inline-asm indexed/offset memory grammar** — `parseAsmMemory` accepts only `reg`/`reg±disp`, no index-register or scaled forms.
- **Syscalls** — no lowering exists for `windows`/`none` targets.

## Design notes

**Nothing here re-derives the register table.** `resolveReg` (`registers.go`) looks tokens up in the module's `vir.RegisterTableForArchitecture` — the same table `vir.Verify` already checked asm bindings against — and maps the result onto this backend's physical registers (`physicalSlot`, `"X0".."X28"`, `"X29"→FP`, `"X30"→LR`, `"SP"`); it exists to bridge vir's string-keyed table to `isa/aarch64/encoder`'s typed constants, not to re-declare them.

**Errors are reported, not panicked.** Anything that would indicate a bug in this package's own invariants (e.g. an `OSlot` with no assigned frame offset reaching `resolveOpr`) still returns an `error` rather than panicking, so a caller can decide how to surface it.

**Every value gets its own slot, uniformly.** Unlike `lower/arm`, where only named vir values get frame slots, `BuildFrame` here allocates a slot for every function parameter and every asm out-binding identifier as well — the zero-extended-slot invariant holds across the whole pipeline, so `isel.go` and `asm.go` never need to special-case where a value's home came from.