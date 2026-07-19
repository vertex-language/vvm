# lower/x86

Lowers verified `vir.Module`s to 32-bit x86 (IA-32) machine code.

```
vir.Module -> x86.Program
```

This package (plus its `mcode`/`abi`/`regalloc`/`syscallabi`/`inlineasm` helpers)
knows x86 instruction encoding and the i386 System V cdecl ABI. It knows
nothing about object file formats (ELF/COFF/Mach-O) — that's a downstream
concern.

## ABI

cdecl:
- Arguments on the stack in 4-byte slots, first argument at the lowest address.
- Caller cleans up the stack.
- Result returned in EAX.
- EAX/ECX/EDX are caller-saved; EBX/ESI/EDI/EBP are callee-saved.
- EBP is the frame pointer.

## Code model

Non-PIC.
- Globals/function addresses are 32-bit absolute (`FixupAbs32`).
- Calls and cross-function jumps are 32-bit PC-relative (`FixupPCRel32`).

An object writer maps these onto `R_386_32`/`R_386_PC32`-shaped relocations.

## Coverage

- The integer/pointer subset of Vertex IR.
- Inline assembly — both Intel and AT&T dialects, curated mnemonic set (see `inlineasm`).
- Syscalls, per target-OS trap convention (see `syscallabi`).

**Not yet implemented** (rejected with explicit errors, tier work TODO):
- Floats
- Vectors
- i64/i128 named values
- Saturating arithmetic
- `bitrev`

## Directory layout

```
x86/
├── x86.go          # package doc; Program/Func/Global; Fixup re-exports
├── isel.go         # Lower(): entry point, instruction selection
├── abi/
│   ├── callconv.go # PlanCall: cdecl argument-area layout
│   ├── frame.go    # Frame/BuildFrame: EBP-relative slot assignment
│   └── layout.go   # struct/type size & alignment (i386 SysV rules)
├── mcode/
│   ├── inst.go     # Inst/Opr, Reg, Fixup/FixupKind, condition codes
│   └── encode.go   # Encode(): Inst stream -> IA-32 machine bytes
├── regalloc/
│   └── regalloc.go # ResolveSlots: OSlot -> EBP-relative OMem (spill-everything)
├── syscallabi/
│   ├── syscallabi.go # Convention type + registry/Lookup
│   ├── linux.go       # Linux i386 int 0x80 convention
│   ├── freebsd.go     # FreeBSD i386 int 0x80 convention
│   └── windows.go     # deliberately unregistered — no stable user-mode convention
└── inlineasm/
    ├── inlineasm.go # LowerBlock(): vir.AsmBlock -> []mcode.Inst
    ├── att.go        # AT&T dialect (src-first; reorders to canonical dst,src)
    ├── intel.go      # Intel dialect (already dst-first; no reordering)
    ├── common.go     # shared per-mnemonic semantics, jcc table
    └── registers.go  # vir register table -> mcode.Reg mapping
```

## Package roles at a glance

| Package      | Responsibility |
|--------------|----------------|
| `x86`        | Top-level `Lower()`, instruction selection over vir instructions/terminators, data-initializer emission for globals. |
| `abi`        | i386 SysV struct/type layout, cdecl call-site argument planning, stack frame slot assignment. |
| `mcode`      | The x86-shaped pseudo-instruction stream (`Inst`/`Opr`) and its encoder — the single ModRM/SIB encoder and relocation model shared by isel and inline asm. |
| `regalloc`   | Current baseline allocator: spill-everything. Rewrites every `OSlot` operand to an EBP-relative memory operand. A real allocator can replace this without touching isel's or inlineasm's output contract. |
| `syscallabi` | Per-target-OS syscall trap convention (register assignment, trap instruction, result register). |
| `inlineasm`  | Lowers verified inline asm blocks (Intel/AT&T) into the same `mcode.Inst` stream isel uses, so there's exactly one encoder for both. |

## Notes

- Registration for register allocation is deliberately minimal today
  (`regalloc.ResolveSlots`): every vir value gets its own stack slot, and
  instruction selection routes everything through EAX/ECX/EDX as scratch.
  This is a correctness-first baseline meant to be swapped out later.
- `syscallabi` for Windows is intentionally left unregistered: there's no
  stable, documented `int 0x80`/`sysenter` convention for user-mode
  syscalls on Windows, so `Lookup("windows")` reports `ok == false` and the
  caller surfaces that as an explicit lowering error.