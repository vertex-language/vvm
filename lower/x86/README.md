# lower/x86

`github.com/vertex-language/vvm/lower/x86`

Lowers a verified `vir.Module` (arch `"x86"`, i.e. 32-bit/IA-32) into concrete machine code: a `Program` of function bytes, global data, and relocations, ready for an object writer. This package assumes its input already passed `vir.Verify` — it does not re-check §9 obligations itself.

```go
import "github.com/vertex-language/vvm/lower/x86"
```

---

## Package layout

```
lower/x86/
├── x86.go            Package doc, Program/Func/Global/Fixup, Lower() entry point,
│                      the lowerer struct and its symbol-kind table
├── isel.go            Instruction selection: one vir.Function's body -> []Inst
│                      (fnLower), operand load/store helpers, inline-asm and
│                      syscall dispatch, calls and terminators
├── asm.go             Inline-assembly lowering shared across dialects:
│                      LowerBlock, the asmDialect/SymbolResolver interfaces,
│                      the curated per-mnemonic instruction table (lowerMnemonic),
│                      jump/condition-code mapping, register/immediate resolution
├── asm_dialects.go     The two concrete asmDialects: intelDialect and attDialect
│                      (memory-operand grammars, operand reordering, size suffixes)
├── frame.go            Frame: EBP-relative stack layout, BuildFrame
├── encode.go           assemble: prologue/epilogue, OSlot resolution, and the
│                      final translation into isa/x86/encoder's Inst/Opr
├── callconv.go         PlanCall/ArgSlot: cdecl outgoing-argument-area layout
├── syscall.go          SyscallConvention: per-target-OS syscall trap ABI
├── layout.go           Layout: i386 SysV aggregate size/alignment/field-offset rules
├── globals.go           lowerGlobal/dataw: global initializer -> data bytes + fixups
└── opr.go              Opr/Inst: the pre-encoding instruction vocabulary
                       instruction selection and inline-asm lowering share
```

---

## Design: one flat package

Instruction selection, ABI/frame layout, slot resolution, syscall conventions, and inline-assembly lowering are all facets of a single lowering pipeline that runs in a fixed order and shares one `Opr`/`Inst` vocabulary (`opr.go`). They live in one package deliberately — splitting them into separate `mcode`/`abi`/`regalloc`/`syscallabi`/`inlineasm` packages bought no real independence, and the only visible effect was several copies of the same `isa/x86` register constants re-exported under new names.

Register identity, condition codes, and ModRM/SIB facts come directly from `isa/x86` (`isax86.REAX`, `isax86.CondE`, ...) — nothing here re-declares them. `isa/x86`'s own README explains the split that *is* load-bearing: ISA fact vs. lowering decision. That one stays.

This package knows x86 instruction encoding and the i386 System V cdecl ABI; it knows nothing about ELF/COFF/Mach-O — that's `object`/`objectwriter`'s job.

---

## `Lower`

```go
func Lower(m *vir.Module) (*Program, error)
```

`m` must have passed `vir.Verify`, and if `m.Target` is set, its `Arch` must be `"x86"`. `Lower` walks `m.Globals` (via `lowerGlobal`) and `m.Functions` (via `lowerFunc`) in declaration order and returns a self-contained `Program`:

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
    Fixups []Fixup
}
```

`Fixup`/`FixupKind` are a single-hop alias of `isa/x86/encoder`'s relocation vocabulary, so downstream object writers only need to import `lower/x86`.

```go
m := vir.NewModule("add_example")
m.SetTarget("x86", "linux", "gnu")
// ... build m via vir's FunctionBuilder ...
if err := vir.Verify(m); err != nil {
    panic(err)
}
prog, err := x86.Lower(m)
```

---

## Pipeline (per function)

`isel.go`'s `lowerFunc` runs the stages in this fixed order:

1. **Type fixation** (`typeFunc`) — mirrors the verifier's result-type computation for the operators this backend actually supports (integers/pointers up to 32 bits; wider or float/vector types are rejected here with an explicit TODO error), including asm `out` bindings.
2. **Instruction selection** (`selInst`/`selTerm`/`selAsm`/`selSyscall`/`selCall`) — walks every block and line, emitting `Inst`s over the EAX/ECX/EDX scratch set. Every named vir value is materialized through its own stack slot (`Slot`) rather than kept live across instructions — no cross-instruction register residency.
3. **Frame building** (`frame.go`'s `BuildFrame`) — assigns every `OSlot` operand a distinct 4-byte EBP-relative home and every parameter its cdecl incoming offset.
4. **Assembly** (`encode.go`'s `assemble`) — wraps the body in a prologue/epilogue, expands the `epi_ret`/`epi_jmp_sym`/`epi_jmp_r` pseudo-ops, resolves every `OSlot` to its concrete `[ebp+off]` operand, and hands the result to `isa/x86/encoder` for byte emission.

## `Opr`/`Inst` (`opr.go`)

`Opr` is the operand vocabulary instruction selection builds against: `OReg`, `OImm` (optionally a symbol + addend), `OMem` (`[Base(+Index*Scale)+Disp]`, or absolute), and `OSlot` — a vir value's not-yet-placed stack home, the one addition over `isa/x86/encoder.Opr` and deliberately absent there. `resolveSlot` in `encode.go` is what erases the difference before final assembly; reaching `toEncoderInst` with an unresolved `OSlot` is treated as a bug in this package, reported rather than panicked.

## ABI

**cdecl.** Arguments on the stack in 4-byte slots (byval structs take their own aligned size — see `callconv.go`'s `PlanCall`), first argument at the lowest address, caller cleans up, result in EAX. EAX/ECX/EDX are caller-saved; EBX/ESI/EDI/EBP are callee-saved and always pushed/popped by `encode.go`'s prologue/epilogue regardless of whether the function actually uses them (`SavedRegBytes` in `frame.go`).

**Code model:** non-PIC. Globals/function addresses are 32-bit absolute (`FixupAbs32`); calls and cross-function jumps are 32-bit PC-relative (`FixupPCRel32`).

**Aggregate layout** (`layout.go`) follows the i386 System V rules: fields at increasing offsets, each at its natural alignment, trailing padding to the largest field alignment — including the classic i386 quirk that 8-byte scalars (`i64`, `f64`) align to only 4 inside aggregates.

**Syscalls** (`syscall.go`) are per-target-OS: Linux passes all seven slots (sysno + up to six args) in eax/ebx/ecx/edx/esi/edi/ebp; FreeBSD passes only sysno in a register and pushes every argument cdecl-style beneath a dummy return-address placeholder, since its `int 0x80` entry point reads the stack as though it were a `call`. `os = none`/`uefi` without a wired convention lowers a `syscall` to `ud2` rather than failing, per §4's "unsupported natively" rule.

## Inline assembly (`asm.go`, `asm_dialects.go`)

`LowerBlock` only runs for `arch == "x86"` and dispatches on `m.AsmDialect` (module-wide, per `ir/vir`'s design) to one of two `asmDialect`s:

- **Intel** (`intelDialect`) — `(ptr-size "ptr")? [base(+index(*scale))?((+|-)disp)?]`, operands already in canonical `(dst, src)` order.
- **AT&T** (`attDialect`) — `disp?(base?(,index(,scale)?)?)`, operands in `(src, dst)` order that `Lower` swaps into canonical order (and, for 3-operand `imul $imm, src, dst`, reorders into `dst, src, imm`). AT&T mnemonics also carry an operand-size suffix (`stripATTSizeSuffix`) that this backend only accepts in its 32-bit (`l`) form.

Both dialects funnel into a single dialect-independent mnemonic table, `lowerMnemonic` — a curated, non-exhaustive subset (mov/alu/shift/mul-div/stack/inc-dec/bswap/int/nop/cdq) covering the common integer instructions that appear in real-world inline asm. Jump mnemonics (`jmp`, the `jcc` family via `jccTable`) are dispatched directly since their operand is a bare local label, not a resolvable value.

Every register and memory operand is width-checked against this backend's 32-bit-only lowering (`resolveRegister` rejects anything without a `physicalSlot` 32-bit encoding; `intelMemRe`/`attMemRe` reject `xmmword`/`ymmword`/`zmmword`-sized memory operands outright).

---

## Known gaps

Marked `TODO` at each call site rather than silently skipped:

- **i64/i128 named values** — `checkValueType` in `isel.go` rejects any int wider than 32 bits (register-pair support TODO).
- **Floats and vectors** — no x87/SSE tier yet; float ops, vector ops, and float bitcast/compare all return explicit "not lowered" errors.
- **Saturating arithmetic** (`uadd_sat`/`sadd_sat`/`usub_sat`/`ssub_sat`) and **bitrev** are unimplemented.
- **`sdiv`/`srem`** don't yet special-case narrow `INT_MIN / -1` (§6.1): the widened 32-bit `idiv` wraps instead of trapping for e.g. i8 `-128/-1`.
- **AT&T 8/16-bit suffixes** (`movb`/`movw` etc.) are recognized but rejected — only the 32-bit (`l`) forms lower today.
- **`f16` global initializers** aren't emitted (`globals.go`'s `dataw.lit`).
- **Full per-dialect mnemonic/operand-shape validation** (§9.38) is still just the curated table in `lowerMnemonic`, matching the verifier's own note that this check is structural (arity/label-scoping) only.
- **Frame-growing tail calls** — `selTailCall` only supports the case where the callee's argument bytes fit inside the caller's own incoming argument area; anything larger is rejected.

## Design notes

**Nothing here re-derives the register table.** `resolveRegister` looks tokens up in `vir.RegisterTableForArchitecture("x86")` — the same table `vir.Verify` already checked asm bindings against — and maps the result onto this backend's physical registers (`physicalSlot`); registers with no 32-bit encoding (r8..r15) are simply absent from that map.

**The block boundary is a real barrier for free.** Per §9.41, an asm block is a full optimization/memory barrier. This package doesn't implement that property specially — it falls out because instruction selection emits the whole asm sequence as one indivisible span and every live value crosses the block boundary through its own memory slot rather than a register.

**Errors are reported, not panicked.** Anything that would indicate a bug in this package's own invariants (e.g. an unresolved `OSlot` reaching final assembly) still returns an `error` rather than panicking, so a caller can decide how to surface it.