# lower/x86_64

Lowers verified `vir.Module`s to 64-bit x86 (AMD64) machine code.

```
vir.Module -> x86_64.Program
```

This package (plus its `mcode`/`abi`/`regalloc`/`syscallabi`/`inlineasm` helpers)
knows x86-64 instruction encoding and the System V AMD64 ABI. It knows
nothing about object file formats (ELF/Mach-O/PE) — that's a downstream
concern.

## ABI

System V AMD64:
- Integer/pointer arguments 1–6 in RDI, RSI, RDX, RCX, R8, R9; arguments
  beyond that on the stack, 8-byte slots, first stack argument at the
  lowest address.
- Caller reserves and tears down the argument-staging area per call site
  (`abi.PlanCall`); register args are staged above stack args so argument
  evaluation can clobber scratch registers freely before the final loads.
- Result returned in RAX (RDX:RAX for wide results is not yet used — see
  Coverage).
- RAX/RCX/RDX/RSI/RDI/R8–R11 are caller-saved and used as the isel scratch
  set; the current allocator never keeps a value live across a call, so no
  callee-saved registers are used yet (see `regalloc` notes below).
- RBP is the frame pointer; RSP stays 16-byte aligned at every call site
  (`push rbp` plus 16-aligned local/frame sizes keep this invariant — see
  `abi.Frame`).

## Code model

Position-independent by construction, not by flag:
- Function and global addresses are always materialized RIP-relatively
  (`lea reg, [rip+sym]`), never as absolute immediates.
- Calls and intra-function jumps are 32-bit PC-relative
  (`FixupPCRel32Call` / `FixupPCRel32`).
- `FixupAbs64` is used only for absolute 64-bit pointers in global
  initializers (e.g. `&global` stored in another global's data).

An object writer maps these onto `R_X86_64_PLT32`/`R_X86_64_PC32`/
`R_X86_64_64`-shaped relocations.

## Coverage

- The integer/pointer subset of Vertex IR (`i1`..`i64`, pointers).
- Inline assembly — both Intel and AT&T dialects, curated mnemonic set (see
  `inlineasm`).
- Syscalls, per target-OS trap convention (see `syscallabi`).
- Atomics: load/store, add/sub/xchg via `lock xadd`/`xchg`, and/or/xor via
  a `lock cmpxchg` retry loop, plus `cmpxchg` itself, all at 32-bit/64-bit
  widths.

**Not yet implemented** (rejected with explicit errors, tier work TODO):
- Floats and vectors (SSE tier)
- i128 named values (register-pair lowering)
- Saturating arithmetic and `bitrev`
- Sub-32-bit atomics
- `byval` struct argument passing (SysV classification)
- Tail calls that would need to grow the caller's stack-argument area
- Trapping narrow `sdiv`/`srem` on the INT_MIN/-1 case (currently wraps
  instead of trapping — see `isel.go`)
- Indexed/scaled inline-asm memory operands (`[base+index*scale+disp]`)
- 8/16-bit sub-register operands in inline asm (`al`/`ax` spellings)

## Directory layout

```
x86_64/
├── x86_64.go       # package doc; Program/Func/Global; Fixup re-exports
├── isel.go         # Lower(): entry point, instruction selection
├── abi/
│   ├── callconv.go # PlanCall: SysV argument-staging-area layout
│   ├── frame.go    # Frame/BuildFrame: RBP-relative slot assignment
│   └── layout.go   # struct/type size & alignment (SysV AMD64 rules)
├── mcode/
│   ├── inst.go     # Inst/Opr, Reg, Fixup/FixupKind, condition codes
│   └── encode.go   # Encode(): Inst stream -> x86-64 machine bytes (REX/ModRM/SIB)
├── regalloc/
│   └── regalloc.go # ResolveSlots: KSlot -> RBP-relative KMem (spill-everything)
├── syscallabi/
│   ├── syscallabi.go # Convention type + registry/Lookup
│   ├── linux.go       # Linux x86-64 `syscall` convention
│   ├── freebsd.go     # FreeBSD x86-64 `syscall` convention
│   └── windows.go     # deliberately unregistered — no stable user-mode convention
└── inlineasm/
    ├── common.go     # shared per-mnemonic semantics, jcc table, LowerBlock()
    ├── att.go        # AT&T dialect (src-first; reorders to canonical dst,src)
    ├── intel.go       # Intel dialect (already dst-first; no reordering)
    └── registers.go  # vir register table -> mcode.Reg mapping
```

## Package roles at a glance

| Package      | Responsibility |
|--------------|----------------|
| `x86_64`     | Top-level `Lower()`, instruction selection over vir instructions/terminators, data-initializer emission for globals. |
| `abi`        | SysV AMD64 struct/type layout, argument-staging-area planning per call site, stack frame slot assignment. |
| `mcode`      | The x86-64-shaped pseudo-instruction stream (`Inst`/`Opr`) and its encoder — the single REX/ModRM/SIB encoder and relocation model shared by isel and inline asm. |
| `regalloc`   | Current baseline allocator: spill-everything. Rewrites every `KSlot` operand to an RBP-relative memory operand. A real allocator (linear scan, RBX/R12–R15 joining the allocatable set) can replace this without touching isel's or inlineasm's output contract. |
| `syscallabi` | Per-target-OS syscall trap convention (register assignment, trap instruction, result register). |
| `inlineasm`  | Lowers verified inline asm blocks (Intel/AT&T) into the same `mcode.Inst` stream isel uses, so there's exactly one encoder for both. |

## Notes

- Register allocation is deliberately minimal today
  (`regalloc.ResolveSlots`): every vir value gets its own 8-byte stack slot,
  and instruction selection routes everything through RAX/RCX/RDX (and
  RSI/RDI/R8–R11 for calls, memcopy-family ops, and syscalls) as scratch.
  This is a correctness-first baseline meant to be swapped out later.
- Values narrower than 64 bits are kept zero-extended in their home slots
  between operations (`fnLower.norm`); sign-extended materialization is
  requested explicitly per use via the `signed` argument to `load`.
- `syscallabi` for Windows is intentionally left unregistered: there's no
  stable, documented user-mode syscall convention on x86-64 Windows, so
  `Lookup("windows")` reports `ok == false` and the caller surfaces that as
  an explicit lowering error.
- Inline asm and isel share exactly one encoder (`mcode.Encode`): both
  produce `mcode.Inst` streams, and `regalloc`/`mcode` don't know or care
  which package emitted a given instruction.