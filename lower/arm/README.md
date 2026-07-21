# lower/arm

`github.com/vertex-language/vvm/lower/arm`

Lowers a verified `vir.Module` (arch `"arm"` or `"armeb"` — 32-bit ARM, A32) into concrete machine code: a `Program` of function bytes, global data, and relocations, ready for an object writer. This package assumes its input already passed `vir.Verify` — it does not re-check §9 obligations itself.

```go
import "github.com/vertex-language/vvm/lower/arm"
```

---

## Package layout

```
lower/arm/
├── arm.go        Package doc, Program/Func/Global, Lower() entry point,
│                 the lowerer struct and its symbol-kind table
├── arch.go       Arch: "arm" (little-endian) vs "armeb" (big-endian) — the
│                 one axis the two A32 targets differ on
├── isel.go       Instruction selection: one vir.Function's body -> []Inst
│                 (fnLower/lowerFunc), operand load/store helpers, calls
│                 (selCall/selTailCall) and terminators, plus global
│                 initializer lowering (lowerGlobal/dataw)
├── frame.go      Frame: FP-relative stack layout, BuildFrame
├── encode.go     assemble: prologue/epilogue, OSlot resolution, and the
│                 final translation into isa/arm/encoder's Inst/Opr
├── layout.go     Layout: AAPCS aggregate size/alignment/field-offset rules
└── opr.go        Opr/Inst: the pre-encoding instruction vocabulary
```

---

## Design: one flat package

Instruction selection, AAPCS frame/call layout, and slot resolution are all facets of a single lowering pipeline that runs in a fixed order and shares one `Opr`/`Inst` vocabulary (`opr.go`). They live in one package deliberately, for the same reason `lower/x86_64` and `lower/x86` do: splitting them into separate packages buys no independence, only duplicate copies of `isa/arm`'s register constants under new names.

Register identity and condition codes come directly from `isa/arm/encoder` (`encoder.R0`, `encoder.RFP`, `encoder.CondEQ`, ...) — nothing here re-declares them. This package knows A32 instruction selection and the AAPCS calling convention (frame layout, call-argument layout, struct/array layout); it knows nothing about ELF/Mach-O — that's `object`/`objectwriter`'s job.

The `"arm"`/`"armeb"` split (`arch.go`) is the narrowest possible: both architectures share instruction semantics, AAPCS, layout, and fixup shapes, and differ only in the byte order of instruction words and multi-byte global scalars, threaded through as a single `big bool` into `assemble` and `dataw`.

---

## `Lower`

```go
func Lower(m *vir.Module, arch Arch) (*Program, error)
```

`arch` must be `ArchARM` or `ArchARMEB`, and if `m.Target` is set, its `Arch` string must match. `Lower` walks `m.Globals` (via `lowerGlobal`) and `m.Functions` (via `lowerFunc`) in declaration order and returns a self-contained `Program`:

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

`Fixup`/`FixupKind` are a type alias of `isa/arm/encoder`'s relocation vocabulary — `encoder.Encode` is the only thing that produces them, so this is a hop to the single source, not a hand-copied second table.

```go
m := vir.NewModule("add_example")
m.SetTarget("arm", "linux", "gnueabihf")
// ... build m via vir's FunctionBuilder ...
if err := vir.Verify(m); err != nil {
    panic(err)
}
prog, err := arm.Lower(m, arm.ArchARM)
```

---

## Pipeline (per function)

`isel.go`'s `lowerFunc` runs the stages in this fixed order:

1. **Type fixation** (`typeFunc`) — mirrors the verifier's result-type computation for the operators this backend actually supports (integers/pointers up to 32 bits; wider or float/vector types are rejected here with an explicit TODO error).
2. **Parameter spill** — the first four register-passed parameters (`R(encoder.Reg(i))`) are stored into their home slots up front; stack parameters (5th onward) already have a home at `[fp+8+4k]` and need no spill.
3. **Instruction selection** (`selInst`/`selTerm`/`selCall`/`selTailCall`) — walks every block and line, emitting `Inst`s over the R0–R3 (plus IP as a scratch/loop register) set. Every named vir value is materialized through its own 4-byte stack slot (`Slot`) rather than kept live across instructions — no cross-instruction register residency. Inline asm is rejected outright (`block %s: inline asm not lowered on arm`) — there is no dialect-lowering tier here, unlike `lower/x86_64`/`lower/x86`.
4. **Frame building** (`frame.go`'s `BuildFrame`) — assigns every `OSlot` operand a distinct 4-byte FP-relative home and every register-passed/stack parameter its incoming offset.
5. **Assembly** (`encode.go`'s `assemble`) — wraps the body in a prologue/epilogue, expands the `epi_ret`/`epi_jmp_sym`/`epi_jmp_r` pseudo-ops, resolves every `OSlot` to its concrete `[fp+off]` operand, and hands the result to `isa/arm/encoder` for byte emission in the requested byte order.

## `Opr`/`Inst` (`opr.go`)

`Opr` is the operand vocabulary instruction selection builds against: `OReg`, `OImm` (optionally a symbol + addend), `OMem` (`[Base+Disp]` or `[Base+Index]`), and `OSlot` — a vir value's not-yet-placed stack home, the one addition over `isa/arm/encoder.Opr` and deliberately absent there. `resolveOpr` in `encode.go` is what erases the difference before final assembly; a `Slot` with no entry in the built `Frame` is reported as an error (`"arm: value %q has no frame slot"`), not panicked.

## ABI

**AAPCS.** First four integer/pointer arguments in R0–R3; remaining arguments on the stack in 4-byte slots, staged and reloaded by `selCall`/`selTailCall` (`callconv` logic lives inline in `isel.go`, not a separate file). Result in R0. Caller cleans up the staged argument area.

**Frame layout** (`frame.go`) grows down from `[fp+8+4k]` incoming stack args, past the saved LR and saved FP, to one 4-byte home slot per vir value at `[fp-4-4k]`. `Local` is rounded up to 8 bytes so SP keeps AAPCS 8-byte alignment.

**Callee-saved registers are never touched.** The prologue only pushes `{fp, lr}`; R4–R11 don't need saving because the scratch set instruction selection uses (R0–R3, IP) is entirely caller-saved, and every named value lives in its own slot rather than a register across instructions.

**Tail calls** (`selTailCall`) support only the shape where all arguments fit in R0–R3; anything requiring stack argument slots is rejected (`"tailcall with %d args exceeds the r0-r3 register set"`).

**Aggregate layout** (`layout.go`) follows AAPCS: fields at increasing offsets, each at its natural alignment (capped at 8, AAPCS's max fundamental alignment), trailing padding to the largest field alignment. `usize`/pointers are 4 bytes.

**Syscalls** are not lowered on this backend: the `syscall` op returns an explicit "no syscall ABI convention yet" error rather than picking one.

## Known gaps

- **Inline assembly** isn't lowered at all — any block containing an asm line is rejected outright, unlike `lower/x86_64`/`lower/x86`'s dialect-lowering tier.
- **`byval`/`sret` arguments** aren't lowered, at either function-definition or call-site position (AAPCS aggregate passing TODO).
- **i64/i128 named values** — `checkValueType` rejects any int wider than 32 bits (register-pair support TODO).
- **Floats and vectors** — no VFP/NEON tier yet; every float/vector op, including float bitcast and float compares, returns an explicit "not lowered" error.
- **Saturating arithmetic** (`*add_sat`/`*sub_sat`) is unimplemented.
- **`popcnt`** has no scalar A32 instruction and is rejected outright (NEON `vcnt` tier TODO, §10.4).
- **`sdiv`/`srem`** don't yet special-case narrow `INT_MIN / -1` (§6.1): the widened 32-bit `sdiv` wraps instead of trapping for e.g. i8 `-128/-1`.
- **Narrow atomics** — `atomic_add`/`atomic_sub`/`atomic_and`/`atomic_or`/`atomic_xor`/`atomic_xchg` only support 32-bit widths.
- **`cmpxchg` narrower than 32 bits** isn't lowered.
- **`f16` global initializers** aren't emitted (`isel.go`'s `dataw.lit`).
- **Frame-growing tail calls** — `selTailCall` only supports the case where every argument fits in R0–R3; anything requiring stack argument slots is rejected.
- **Syscalls** — no lowering exists yet for any target OS.

## Design notes

**Nothing here re-derives the register table.** Register operands are built directly against `isa/arm/encoder`'s exported `R0`..`RPC`/`CondEQ`..`CondAL` constants at the point of use (`opr.go`'s header comment) — this package declares no register or condition constant of its own, the same drift risk `isa/x86_64`'s README describes fixing relative to `lower/x86`.

**Errors are reported, not panicked.** Anything that would indicate a bug in this package's own invariants (e.g. an `OSlot` with no assigned frame offset reaching `resolveOpr`) still returns an `error` rather than panicking, so a caller can decide how to surface it.

**Byte order is the only axis of variation.** `Arch.Big()` (`arch.go`) is threaded into `assemble` (instruction word order) and `dataw.scalar` (global scalar order); every other stage of the pipeline — instruction selection, frame layout, ABI — is identical between `"arm"` and `"armeb"`.