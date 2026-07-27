# `cpu/lower/x86`

```go
import "github.com/vertex-language/vvm/cpu/lower/x86"
```

This package lowers a verified Vertex IR module (`ir/vir.Module`, targeting `x86`) to 32-bit x86 (IA-32) machine code. It outputs a `Program` containing `Func`s (raw bytes and relocation `Fixup`s) and `Global`s (raw data and `Fixup`s). Object-file emission, symbol tables, and relocation application are handled downstream; this package solely handles lowering and returns a fixup list.

Inline assembly is not part of this package: it was removed from `ir/vir`'s data model and has no representation here to lower. The module must already have passed `vir.Verify` and, if it came from multiple source files, `importer.Rewrite` — cross-module references should already be erased into plain calls/symbols/inline literals by the time `Lower` sees it.

## Usage

```go
prog, err := x86.Lower(module) // module: *vir.Module, already vir.Verify'd
if err != nil {
    // module is valid IR this backend can't lower yet, or targets the wrong arch
    log.Fatal(err)
}
for _, f := range prog.Funcs {
    fmt.Println(f.Name, len(f.Code), "bytes,", len(f.Fixups), "fixups")
}
```

This document explains the package's architectural implementation. For specific opcode definitions, consult the top-level IR spec (`README.md`).

---

## 1. Layout

| File | Responsibility |
| --- | --- |
| `x86.go` | Entry point (`Lower`), `Program`/`Func`/`Global` structures, `FixupKind` aliases, and the module-wide `lowerer`/`callable` index. |
| `layout.go` | `Layout`: size, alignment, and struct-field offsets under the Intel386 C ABI. |
| `callconv.go` | `LayoutArgs`/`PlanCall`: shared, byval-aware argument-placement rules for callers and callees. |
| `frame.go` | `Frame`: per-function stack layout and `BuildFrame`. |
| `isel.go` | Instruction selection, scratch registers, the type-fixation pass, `call`/`tailcall`/`syscall` lowering, `va_start`/`va_arg`, and terminators. |
| `syscall.go` | `SyscallConvention`, containing one convention per OS (`linux`, `freebsd`). |
| `globals.go` | `global` initializers outputting data bytes and fixups. |
| `opr.go` | Pre-encoding `Opr`/`Inst`: `isa/x86/encoder` shapes and `OSlot`. |
| `encode.go` | Prologue/epilogue emission, `OSlot` resolution, `encoder.Inst` translation, and the final `encoder.Encode` call. |

Unlike the ARM/AArch64 backends, there is no separate `isel_call.go`, `isel_va.go`, or `typefix.go` — the stack-only IA-32 convention keeps call lowering, variadic access, and type fixation small enough to live directly in `isel.go`. There is likewise no `divide.go`: IA-32's `idiv`/`div` are baseline instructions, so division needs no software loop, only the trap-shaping described in §6.

---

## 2. Frame Shape

The frame grows downward from the frame pointer (`ebp`):

```text
[ebp+8+…]   incoming arguments   (laid out by LayoutArgs)
[ebp+4]     return address       (the caller's)
[ebp+0]     saved ebp
[ebp-4]     saved ebx
[ebp-8]     saved esi
[ebp-12]    saved edi
[ebp-16…]   local slots, Frame.Local bytes, one 4-byte slot per named value
```

* **Static Frame Pointer:** `ebp` stays fixed after the prologue, so the epilogue restores `esp` with `lea esp, [ebp-SavedRegBytes]` rather than arithmetically undoing the prologue's `sub esp, Local` — the two are only equivalent when `esp` hasn't moved in between, and a dynamically-sized `alloca` breaks that equivalence at runtime.
* **16-Byte Call Alignment:** `StackAlign` is 16, matching the Intel386 psABI requirement that `(%esp + 4)` be a multiple of 16 at a call's entry point. Nothing this backend emits for itself needs the alignment — there is no SSE codegen and no value wider than four bytes in a slot — but any libc it calls will use `movaps` on its own frame, and a misaligned `movaps` faults. `Frame.Local` is kept ≡ 12 (mod 16) (`alignLocal`) so `esp` lands ≡ 0 at the bottom of the frame, at the cost of up to 12 dead bytes per frame.
* **Tailcall Reuse:** The epilogue leaves `esp` at `ebp+4` after its pops, which is exactly where a tailcall's restaged outgoing arguments need to be for the callee to find them at `[esp+4+…]`.

---

## 3. Scratch Registers

Named values live exclusively in frame slots (no register allocator). Instruction selection uses six general-purpose registers:

* **`eax`, `ecx`, `edx`:** Caller-saved; general scratch and syscall/return conventions.
* **`ebx`, `esi`, `edi`:** Callee-saved under the ABI but free to clobber *within* this backend's own codegen — the prologue already saves and the epilogue restores all three, so nothing this package emits needs to treat them specially. `esi`/`edi` also double as the string-instruction operands for `memcopy`/`memmove`/`memset`.
* **`ebp`, `esp`:** Frame and stack pointers; never used as scratch.

No IR value survives in a register across an `Inst` boundary, so all six general-purpose registers are fair game within one instruction's lowering.

---

## 4. Fixup Vocabulary

`FixupKind` and `Fixup` are plain aliases of `isa/x86/encoder`'s types — `FixupPCRel32` and `FixupAbs32` are re-exported directly rather than translated through an explicit switch. This is a deliberate departure from the ARM/AArch64 backends, which define their own superset `FixupKind` for a fixup the encoder can't name (a `global` initialized with `addr f`). On x86, `FixupAbs32` already exists in the encoder's own vocabulary — a full 32-bit data-word relocation is exactly what an absolute address needs on this word size — so there is no gap to bridge and no second enum to keep in sync.

---

## 5. Zero-Extension Invariant

A value of type `iN` always occupies a full 4-byte slot with the upper `32-N` bits zero. Loads use a zero-extending form (`movzx`), and `maskTo` restores the invariant after any operation that could carry into the upper bits. Only signed consumers (`sdiv`/`srem`, `ashr`, signed compares, `sext`, `abs`) sign-extend via `sext32`, and always into a scratch copy that's never written back to a slot.

---

## 6. Division and Narrow-Width Trapping

IA-32's `idiv`/`div` are baseline instructions — no software loop is needed, unlike the ARM backend. At 32 bits the hardware already raises `#DE` (a deterministic, uncatchable halt) on both a zero divisor and `INT_MIN / -1`, satisfying the trap contract directly.

At narrower widths (`i8`/`i16`/`i1`) this stops being true for the signed forms: after sign-extension to 32 bits, `(-128)/(-1)` is a perfectly representable `128`, so the hardware would silently compute the wrong thing instead of trapping. `selDivide` therefore emits an explicit `cmp`/`cmp`/`jcc`/`ud2` check ahead of `idiv` whenever `bits < 32` and the operation is signed. The zero-divisor case still traps in hardware at every width, since the extended divisor is zero exactly when the narrow one is, so no separate check is needed there.

---

## 7. Calling Convention

`callconv.go` defines the one layout both call sites and callees use (`LayoutArgs`, routed through by `PlanCall` for callers and `BuildFrame` for callees), so the two cannot drift apart. Every argument occupies a whole number of 4-byte words in declaration order with no gaps, except a `byval[S]` argument, which takes its struct's real size rounded up to 4 — matching the Intel386 psABI's treatment of stack-passed structs and making the copy a plain `rep movsb` into a word-aligned destination.

`byval` is fully implemented on ordinary calls (`writeArgs` copies the struct with `rep_movsb`) but rejected outright on `tailcall`: the copy would have to land in a frame that's about to be reused, and a tailcall's restaged arguments are staged below the frame and block-copied up specifically to avoid an earlier argument clobbering a later one still being read from the incoming area. An indirect call through a `fnsig` cannot pass `byval` at all, since a `fnsig` records parameter types but no byval attribution.

Every syscall convention is one `SyscallConvention` in `syscall.go`: Linux passes the call number and up to six arguments in `eax`/`ebx`/`ecx`/`edx`/`esi`/`edi`/`ebp` and traps via `int 0x80`; FreeBSD passes only the call number in `eax` and pushes the arguments (plus a placeholder word) on the stack ahead of the same trap. Because `ebp` doubles as this backend's frame pointer, the register convention loads it last — through a still-valid frame reference — and restores it immediately after the trap.

---

## 8. `valist` Layout

This backend's `va_list` is a single 4-byte pointer to the next variadic argument, kept as an ordinary slot value with no distinguished representation. `va_start` computes it as `ebp + ParamEnd(last_named_param)` rather than `ParamBase + 4*(i+1)`, since that formula is only correct when no earlier parameter is `byval`. Every variadic argument occupies one flat word regardless of its declared width — narrowing happens after the read, not before — so `va_arg` is always a 4-byte load followed by a 4-byte cursor advance.

---

## 9. Missing Against the Spec (Todos)

The following valid IR constructs are not yet supported and will trigger an error at `Lower` time:

* **Floating Point:** No float value type can be held in a slot at all (`f16`/`f32`/`f64` are rejected as a named-value type), so every float operation — arithmetic, comparisons, conversions, `sqrt`/`fma`/`copysign`/`floor`/`ceil`/`trunc`/`nearest`/`min`/`max` — is unimplemented. Float *global initializers* are a partial exception: `f32`/`f64` literals are emitted directly into data (`math.Float32bits`/`Float64bits`); only `f16` global initializers remain unimplemented.
* **Integers:** `i64` and `i128` are both unsupported as named values (need a register pair throughout selection) — narrower than the ARM/AArch64 backends, which hold `i64` in a slot. `i128` *is* accepted as a global initializer's type (`leInt` sign-extends past 64 bits for it) even though it can never be a value this backend computes with.
* **Vectors:** No vector value type, no vector operations (`splat`, `extract`, `insert`, `shuffle`, `gather`, `scatter`, reductions), and no masked loads/stores — there is no vector tier implemented at all.
* **ABI / Calling Convention:** `tailcall` rejects any `byval` argument outright, for the frame-reuse reason in §7.
* **Bit Manipulation:** `popcnt` is rejected unless the module's target declares a `popcnt` or `sse4.2` feature tier (it isn't baseline IA-32, and emitting it unconditionally would fault with `#UD` on machines this backend otherwise targets). `bitrev` has no x86 instruction and no expansion yet.
* **Saturating Arithmetic:** `uadd_sat`/`sadd_sat`/`usub_sat`/`ssub_sat` are not lowered.
* **Atomics:** Only 32-bit atomics are lowered; narrower-width atomic ops are rejected. `and`/`or`/`xor` atomics compile to a `lock cmpxchg` retry loop since IA-32 has no locked and/or/xor that returns the previous value.