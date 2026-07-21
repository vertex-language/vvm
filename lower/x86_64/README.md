# lower/x86_64

`github.com/vertex-language/vvm/lower/x86_64`

Lowers a verified `vir.Module` (arch `"x86_64"`, i.e. AMD64) into concrete machine code: a `Program` of function bytes, global data, and relocations, ready for an object writer. This package assumes its input already passed `vir.Verify` — it does not re-check §9 obligations itself.

```go
import "github.com/vertex-language/vvm/lower/x86_64"
```

---

## Package layout

```
lower/x86_64/
├── x86_64.go     Package doc, Program/Func/Global, Lower() entry point,
│                 the lowerer struct and its symbol-kind table
├── isel.go       Instruction selection: one vir.Function's body -> []Inst
│                 (fnLower/lowerFunc), operand load/store helpers, inline-asm
│                 and syscall dispatch, calls and terminators
├── asm.go        Inline-assembly lowering shared across dialects: LowerBlock,
│                 the resolveOperand/operand intermediate shape, the curated
│                 per-mnemonic instruction table (lowerLine), jump/condition
│                 -code dispatch
├── intel.go      The Intel asmDialect: memory-operand grammar
│                 ([base(+index*scale)(+disp)]), already dst-first
├── att.go        The AT&T asmDialect: size-suffix stripping, src-first ->
│                 dst-first operand reordering, disp(base) memory grammar
├── frame.go      Frame: RBP-relative stack layout, BuildFrame
├── encode.go     assemble: prologue/epilogue, KSlot resolution, and the
│                 final translation into isa/x86_64/encoder's Inst/Opr
├── callconv.go   PlanCall/Call: SysV AMD64 outgoing-argument-area layout
├── syscall.go    SyscallConvention: per-target-OS syscall trap ABI
├── layout.go     Layout: SysV AMD64 aggregate size/alignment/field-offset rules
├── registers.go  Register: dialect-spelled name -> isa/x86_64.Reg + width
├── globals.go    lowerGlobal/dataw: global initializer -> data bytes + fixups
└── opr.go        Opr/Inst: the pre-encoding instruction vocabulary
```

---

## Design: one flat package

Instruction selection, ABI/frame layout, slot resolution, syscall conventions, and inline-assembly lowering are all facets of a single lowering pipeline that runs in a fixed order and shares one `Opr`/`Inst` vocabulary (`opr.go`). They live in one package deliberately, for the same reason `lower/x86` does: splitting them into separate packages buys no independence, only duplicate copies of `isa/x86_64`'s register constants under new names.

Register identity, condition codes, and REX/ModRM/SIB facts come directly from `isa/x86_64` (`isax86_64.RAX`, `isax86_64.CondE`, ...) — nothing here re-declares them. This package knows x86-64 instruction selection, the System V AMD64 ABI (frame layout, call-argument layout, struct/array layout), inline-assembly lowering, and per-target-OS syscall conventions; it knows nothing about ELF/Mach-O/PE — that's `object`/`objectwriter`'s job.

---

## `Lower`

```go
func Lower(m *vir.Module) (*Program, error)
```

`m` must have passed `vir.Verify`, and if `m.Target` is set, its `Arch` must be `"x86_64"`. `Lower` walks `m.Globals` (via `lowerGlobal`) and `m.Functions` (via `lowerFunc`) in declaration order and returns a self-contained `Program`:

```go
type Program struct {
    Funcs   []Func
    Globals []Global
}

type Func struct {
    Name   string
    Code   []byte
    Align  uint32
    Export bool
    Fixups []encoder.Fixup
}
```

`Func.Fixups`/`Global.Fixups` are `isa/x86_64/encoder`'s relocation vocabulary directly (no alias hop, unlike `lower/x86`), so downstream object writers import both `lower/x86_64` and `isa/x86_64/encoder`.

```go
m := vir.NewModule("add_example")
m.SetTarget("x86_64", "linux", "gnu")
// ... build m via vir's FunctionBuilder ...
if err := vir.Verify(m); err != nil {
    panic(err)
}
prog, err := x86_64.Lower(m)
```

---

## Pipeline (per function)

`isel.go`'s `lowerFunc` runs the stages in this fixed order:

1. **Type fixation** (`typeFunc`) — mirrors the verifier's result-type computation for the operators this backend actually supports (integers/pointers up to 64 bits; wider or float/vector types are rejected here with an explicit TODO error), including asm `out` bindings.
2. **Parameter spill** — register-passed parameters (the first six, per `ArgRegs`) are moved into their home slots up front, since the scratch set they arrive in is live-in only at function entry and dead everywhere else; stack parameters already have a home at `[rbp+16+8k]` and need no spill.
3. **Instruction selection** (`selInst`/`selTerm`/`selAsm`/`selCall`/`selTailCall`) — walks every block and line, emitting `Inst`s over the RAX/RCX/RDX/RSI/RDI/R10 scratch set. Every named vir value is materialized through its own 8-byte stack slot (`KSlot`) rather than kept live across instructions — no cross-instruction register residency.
4. **Frame building** (`frame.go`'s `BuildFrame`) — assigns every `KSlot` operand a distinct 8-byte RBP-relative home and every stack parameter its incoming offset.
5. **Assembly** (`encode.go`'s `assemble`) — wraps the body in a prologue/epilogue, expands the `epi_ret`/`epi_jmp_sym`/`epi_jmp_r` pseudo-ops, resolves every `KSlot` to its concrete `[rbp+off]` operand, and hands the result to `isa/x86_64/encoder` for byte emission.

## `Opr`/`Inst` (`opr.go`)

`Opr` is the operand vocabulary instruction selection builds against: `KReg`, `KImm`, `KSym` (a RIP-relative symbol address, +imm addend), `KMem` (`[base+disp]`), `KRIP` (`[rip+sym+disp]`), and `KSlot` — a vir value's not-yet-placed stack home, the one addition over `isa/x86_64/encoder.Opr` and deliberately absent there. `resolveSlots` in `encode.go` is what erases the difference before final assembly; reaching `toEncoderOpr` with an unresolved `KSlot` is reported as an error ("regalloc bug"), not panicked.

## ABI

**System V AMD64.** First six integer/pointer arguments in RDI/RSI/RDX/RCX/R8/R9 (`callconv.go`'s `ArgRegs`); remaining arguments on the stack in 8-byte slots, `PlanCall` rounding the total staged area up to 16 bytes. Result in RAX. Variadic calls zero RAX before the `call` per the SysV convention for the (unused, here) vector-register-count signal.

**Frame layout** (`frame.go`) grows down from `[rbp+16+8k]` incoming stack args, past the return address and saved RBP, to one 8-byte home slot per vir value at `[rbp-8-8k]`. RSP stays 16-byte aligned throughout.

**Callee-saved registers are never touched.** The prologue only pushes RBP; RBX/R12–R15 don't need saving because the scratch set instruction selection uses (RAX/RCX/RDX/RSI/RDI/R10) is entirely caller-saved, and every named value lives in its own slot rather than a register across instructions.

**Code model:** non-PIC-clean by construction — globals/function addresses are always materialized RIP-relatively (`KSym`/`FixupPCRel32`-style), never as 64-bit absolute immediates, regardless of whether the module is actually built PIC.

**Aggregate layout** (`layout.go`) follows the System V AMD64 rules: fields at increasing offsets, each at its natural alignment (i128 aligns to 16), trailing padding to the largest field alignment.

**Syscalls** (`syscall.go`) are per-target-OS, though Linux and FreeBSD share the same `syscall`-instruction convention: number in RAX, up to six args in RDI/RSI/RDX/R10/R8/R9 — R10 rather than RCX, since the `syscall` instruction itself clobbers RCX/R11 — result in RAX. Windows is deliberately left unregistered (no stable documented user-mode `syscall` convention on x86-64 Windows), so `LookupSyscall("windows")` reports `ok == false` and the caller surfaces that as a lowering error.

## Inline assembly (`asm.go`, `intel.go`, `att.go`)

`LowerBlock` dispatches on `m.AsmDialect` (module-wide, per `ir/vir`'s design) to one of two dialects, funneling both into `canonicalOperands` so the rest of the pipeline sees a single dst-first shape:

- **Intel** (`intel.go`) — operands already in canonical `(dst, src)` order.
- **AT&T** (`att.go`) — `attReorder` flips the src-first convention to `(dst, src)` for every two-operand mnemonic (`isTwoOperandMnemonic`), and `stripSizeSuffix` drops the b/w/l/q suffix so the bare mnemonic is what's looked up against `asm.go`'s own tables. `parseATTMem` supports the same narrow `disp(reg)`/`(reg)` subset as Intel's memory grammar — no index/scale term yet (TODO).

Both dialects resolve into this package's own `operand` intermediate shape (`asm.go`) before `lowerLine` dispatches on the bare mnemonic to one of the curated per-shape helpers (`movForm`, `twoOperandForm`, `shiftForm`, `oneOperandForm`, ...) — a curated, non-exhaustive subset (mov/lea/test/imul/alu/shift/push/pop/inc-dec/not-neg-mul-div/syscall/nop) covering the common integer instructions that appear in real-world inline asm. Jump mnemonics (`jmp`, the `jcc` family) are dispatched directly since their operand is a bare local label, not a resolvable value.

## Known gaps

- **Narrow AT&T sub-registers (al/ax)** — `vir.RegisterTableForArchitecture` only carries 32/64-bit spellings today, so 8/16-bit register names are rejected in `Register` (TODO).
- **8/16-bit AT&T size suffixes** — the same width gap: `movb`/`movw` are recognized syntactically but the underlying register lookup rejects them.
- **Indexed/scaled AT&T memory operands** — `parseATTMem` rejects `disp(base,index,scale)` outright (TODO).
- **Symbolic immediates in inline asm** — `literalInt` only accepts int/bool literals; a bare symbol name as an immediate is rejected (TODO).
- **`mem, imm` ALU forms** in inline asm are rejected (`twoOperandForm`); only `reg, imm`/`reg, reg`/`reg, mem` are lowered.
- **`test` mem/imm forms** — only `reg, reg` is supported today (TODO).
- **i128 named values** — `checkValueType` rejects any int wider than 64 bits (register-pair support TODO).
- **Floats and vectors** — no SSE tier yet; every float/vector op returns an explicit "not lowered" error, including float bitcast and float compares.
- **Saturating arithmetic** (`*add_sat`/`*sub_sat`) and **bitrev** are unimplemented.
- **`sdiv`/`srem`** don't yet special-case narrow `INT_MIN / -1` (§6.1): the widened `idiv` wraps instead of trapping for e.g. i8 `-128/-1`.
- **`byval` struct arguments** aren't classified/passed yet (`selCall` rejects them explicitly, SysV classification TODO).
- **Narrow atomics** — `atomic_add`/`atomic_sub`/`atomic_xchg`/`atomic_and`/`atomic_or`/`atomic_xor` only support 32/64-bit widths.
- **`cmpxchg` narrower than 32 bits** (and i128, which would need `cmpxchg16b`) isn't lowered.
- **`f16` global initializers** aren't emitted (`globals.go`'s `dataw.lit`).
- **Frame-growing tail calls** — `selTailCall` only supports the case where the callee's stack-argument slots fit inside the caller's own incoming argument area; anything larger is rejected.
- **`popcnt`** is emitted unconditionally, with a noted TODO to gate it on a POPCNT-capable target tier (§10.4).

## Design notes

**Nothing here re-derives the register table.** `Register` (`registers.go`) looks tokens up in `vir.RegisterTableForArchitecture("x86_64")` — the same table `vir.Verify` already checked asm bindings against — and maps the result onto this backend's physical registers (`physToReg`); it exists to bridge vir's string-keyed table to `isa/x86_64`'s typed constants, not to re-declare them.

**The asm block boundary is a real barrier for free.** Per §9.41, an asm block is a full optimization/memory barrier. This package doesn't implement that property specially — it falls out because instruction selection emits the whole asm sequence as one indivisible span and every live value crosses the block boundary through its own memory slot rather than a register.

**Errors are reported, not panicked.** Anything that would indicate a bug in this package's own invariants (e.g. an unresolved `KSlot` reaching final assembly) still returns an `error` rather than panicking, so a caller can decide how to surface it.