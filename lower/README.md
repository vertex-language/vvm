# lower

`github.com/vertex-language/vvm/lower`

Umbrella for the four architecture backends that turn a verified `vir.Module` into concrete machine code. There is no code at this level — `lower` is a directory, not a package — just the four sibling packages below, which share a design and a pipeline shape but have no import relationship to each other:

```
lower/
├── x86/       32-bit x86 (IA-32), cdecl
├── x86_64/    64-bit x86 (AMD64), System V AMD64
├── arm/       32-bit ARM (A32), AAPCS
└── aarch64/   64-bit ARM (A64), AAPCS64
```

Each package is independently importable and self-contained: given a `*vir.Module` that has already passed `vir.Verify`, each exposes a `Lower` function returning a `Program` of function bytes, global data, and relocations, ready to hand to an object writer. None of the four re-checks the verifier's §9 obligations, and none knows anything about object-file formats (ELF/COFF/Mach-O/PE) — that's `object`/`objectwriter`'s job.

---

## Shared design

All four backends are built the same way, deliberately:

**One flat package per architecture.** Instruction selection, ABI/frame layout, slot resolution, and (where applicable) syscall conventions and inline-assembly lowering all live together rather than being split into separate `mcode`/`abi`/`regalloc`-style packages. Across all four READMEs the same rationale is given: splitting them bought no real independence, and the only effect was duplicated copies of the same `isa/<arch>` register constants re-exported under new names.

**Register/condition-code facts come from `isa/<arch>`, never re-declared.** Each backend maps vir's string-keyed register table (`vir.RegisterTableForArchitecture`) onto its own physical registers, but the underlying identity — register numbers, condition codes, ModRM/SIB or REX or AAPCS encoding facts — is imported directly from the matching `isa/<arch>` package.

**A `Slot` operand kind bridges instruction selection and frame layout.** Every backend's `Opr` vocabulary adds one thing on top of its `isa/<arch>/encoder.Opr` counterpart: an unplaced stack-slot operand (`OSlot` in x86/arm/aarch64, `KSlot` in x86_64). Every named vir value is materialized through its own slot rather than kept live across instructions — none of the four backends does cross-instruction register residency. A later assembly stage resolves every slot to a concrete frame-relative operand before final encoding.

**A fixed, five-stage-or-fewer pipeline per function:** type fixation (mirroring the verifier's result-type rules, rejecting what the backend doesn't support with an explicit TODO error) → optional parameter spill → instruction selection → frame building → assembly (prologue/epilogue splicing, slot resolution, handoff to `isa/<arch>/encoder`).

**Errors, not panics.** An unresolved slot reaching final assembly, or any other violation of a backend's own invariants, is treated as a bug in that package and reported as an `error` rather than panicked, leaving it to the caller to decide how to surface it.

**`Fixup`/`FixupKind` come from `isa/<arch>/encoder`.** x86, arm, and aarch64 expose a single-hop type alias so downstream consumers only need to import the `lower/<arch>` package; x86_64 uses `encoder.Fixup` directly without an alias, so its consumers import both `lower/x86_64` and `isa/x86_64/encoder`.

---

## Where the four diverge

| | `x86` | `x86_64` | `arm` | `aarch64` |
|---|---|---|---|---|
| Width | 32-bit | 64-bit | 32-bit | 64-bit |
| ABI | cdecl | System V AMD64 | AAPCS | AAPCS64 |
| Frame pointer | EBP | RBP | FP (R11) | FP (X29) |
| Register args | none (all stack) | 6, RDI/RSI/RDX/RCX/R8/R9 | 4, R0–R3 | 8, X0–X7 |
| Callee-saved regs | always pushed regardless of use | untouched — scratch set is fully caller-saved | untouched — scratch set is fully caller-saved | untouched — scratch set is fully caller-saved |
| Inline asm | two dialects (Intel/AT&T) | two dialects (Intel/AT&T) | not lowered at all | one dialect (native only) |
| Syscalls | per-target-OS, Linux/FreeBSD differ | per-target-OS, Linux/FreeBSD share convention; Windows unregistered | not lowered for any OS | per-target-OS, Linux/FreeBSD share convention; Windows/none unregistered |
| `sdiv`/`srem` on INT_MIN/-1 | wraps (§6.1 trap not yet implemented) | wraps (§6.1 trap not yet implemented) | wraps (§6.1 trap not yet implemented) | traps via explicit compare + `brk` |
| Endianness variation | none | none | `arm`/`armeb`, instruction words + data | `aarch64`/`aarch64_be`, data only (code always LE) |
| Slot allocation scope | named vir values only | named vir values only | named vir values only | uniformly every parameter and asm out-binding too |

`aarch64` is the odd one out on signed-division trapping (§6.1) and on giving every value — not just named ones — a uniform frame slot; the other three currently share the same "wraps instead of traps" gap and the arm-style "only named values get slots" scheme.

Two of the four (`arm`, `aarch64`) also carry a big/little-endian split as their only extra axis of variation, threaded through as a single boolean rather than a separate code path; the x86 pair have no such split.

---

## Known gaps, in common

Every backend currently rejects, rather than lowers:
- integers wider than its native width (i64/i128 depending on backend)
- floats and vectors (no x87/SSE/VFP/NEON/FP-SIMD tier anywhere yet)
- saturating arithmetic and bitrev
- `f16` global initializers
- tail calls whose arguments don't fit the callee's own incoming argument area/registers