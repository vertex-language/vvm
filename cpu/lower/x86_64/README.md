# `cpu/lower/x86_64`

```go
import "github.com/vertex-language/vvm/cpu/lower/x86_64"
```

This package lowers a verified Vertex IR module (`ir/vir.Module`, targeting `x86_64`) to 64-bit x86 (AMD64/Intel64) machine code, under either the System V AMD64 or Microsoft x64 calling convention. It outputs a `Program` containing `Func`s (raw bytes and relocation `Fixup`s) and `Global`s (raw data and `Fixup`s). Object-file emission, symbol tables, and relocation application are handled downstream; this package solely handles lowering and returns a fixup list.

## Usage

```go
prog, err := x86_64.Lower(module) // module: *vir.Module, already vir.Verify'd
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
| `x86_64.go` | Entry point (`Lower`), `Program`/`Func`/`Global` structures, and the module-wide `index`/callable lookup. |
| `layout.go` | `Layout`: size, alignment, and struct-field offsets. Identical between SysV and Microsoft x64 — `OS` only feeds `callconv.go`. |
| `callconv.go` | `LayoutArgs`/`PlanCall`: shared, `Layout.OS`-selected argument-placement rules for callers and callees, covering both SysV and Windows x64. |
| `frame.go` | `Frame`: per-function stack layout and `BuildFrame`. |
| `isel.go` | Instruction selection, scratch registers, the zero-extension invariant, and the bulk of arithmetic, float, and atomic opcodes. |
| `isel_call.go` | `call`/`tailcall` lowering and terminators. |
| `isel_va.go` | `va_start`/`va_arg` implementation (GP-only `valist`). |
| `syscall.go` | Linux and FreeBSD register conventions for the `syscall` op. |
| `globals.go` | `global` initializers outputting data bytes and fixups. |
| `opr.go` | Pre-encoding `Opr`/`Inst`: `isa/x86_64/encoder` shapes, `OSlot`, and the GP/XMM register-number aliasing. |
| `encode.go` | Prologue/epilogue emission, `OSlot` resolution, `encoder.Inst` translation, and the final `encoder.Encode` call. |
| `typefix.go` | Forward pass fixing named-value types and definition order before frame-building. |

Like the IA-32 backend and unlike ARM/AArch64, there is no `divide.go`: `idiv`/`div` are baseline AMD64 instructions, so division needs no software loop.

Unlike the IA-32 backend, this package does not define or re-export a package-level `FixupKind` at all — `Program.Func.Fixups` and `Program.Global.Fixups` are typed directly as `[]enc.Fixup` from `isa/x86_64/encoder`, and call sites reach for `enc.FixupAbs64` directly rather than through a local alias (see `globals.go`). There is no ARM/AArch64-style superset enum to keep in sync either, since a `global` initialized with `addr f` needs exactly one relocation kind — a full 64-bit data-word patch — and `enc.FixupAbs64` already names it.

---

## 2. Frame Shape

The frame grows downward from the frame pointer (`rbp`). The offset of the first incoming stack argument depends on the target OS, since Microsoft x64 reserves 32 bytes of caller-owned "shadow space" above the return address that SysV has no equivalent of:

```text
[rbp+48+…]  (windows) incoming stack arguments      \
[rbp+16+…]  (sysv)    incoming stack arguments       > via LayoutArgs
[rbp+8]     return address       (the caller's)
[rbp+0]     saved rbp
[rbp-8]     saved rbx
[rbp-16…]   saved r12-r15
[rbp-40]    end of saved-register block
[rbp-88…]   GP register save area  (variadic functions only, SysV targets only)
[rbp-…]     local slots, Frame.Local bytes, one 8-byte slot per named value
```

* **Static Frame Pointer:** `rbp` stays fixed after the prologue, so the epilogue restores `rsp` with `lea rsp, [rbp-SavedRegBytes]` rather than arithmetically undoing the prologue's `sub rsp, Local` — the two are only equivalent when `rsp` hasn't moved in between, and a dynamically-sized `alloca` breaks that equivalence at runtime.
* **16-Byte Call Alignment:** `StackAlign` is 16 on both conventions. `Frame.Local` is kept ≡ 8 (mod 16) — after `push rbp` (`rsp` → 0 mod 16) and five callee-saved pushes (−40 → 8 mod 16), `sub rsp, Local` lands `rsp` on a 16-byte boundary at the bottom of the frame iff `Local ≡ 8 (mod 16)`.
* **Callee-Saved Block:** `rbx`, `r12`–`r15` are pushed by the prologue and popped by the epilogue (`rbp`/`rsp` are handled separately by the dedicated push/`lea` sequences). This set is identical under both conventions — SysV and Microsoft x64 happen to agree on which GPRs are callee-saved for the registers this backend uses.
* **Windows Shadow Space:** `windowsShadowSpace` (32 bytes) is reserved by the *caller* on every Windows call regardless of argument count, sitting directly above the return address; a callee may (and real CRT-generated code does) spill its own register arguments there. SysV reserves nothing equivalent. `windowsIncomingArgBase` (48) and `sysvIncomingArgBase` (16) encode where each convention's first real stack argument begins relative to `rbp`.
* **Variadic Save Area:** SysV-only — Windows variadic functions are rejected outright by `BuildFrame` (see §9), so the 48-byte GP register save area and its `rbp`-relative offset (`Frame.SaveArea`) only ever exist under the SysV convention.

---

## 3. Scratch Registers

Named values live exclusively in frame slots (no register allocator).

* **`rax`, `rcx`, `rdx`, `rsi`, `rdi`, `r8`–`r10`:** Caller-saved; general scratch, argument staging, and (on Linux) syscall arguments.
* **`r11`:** Reserved as the indirect call/tailcall target register — loaded last, after every argument register, so it's the one register safe to clobber while arguments are already staged.
* **`rbx`, `r12`–`r15`:** Callee-saved under both ABIs but free to clobber *within* this backend's own codegen — the prologue already saves and the epilogue restores all five, so nothing this package emits needs to treat them specially.
* **`rbp`, `rsp`:** Frame and stack pointers; never used as scratch.
* **`xmm0`–`xmm7`:** Float scratch and the SysV/Windows floating-point argument registers. `opr.go` aliases the XMM constants (`RXMM0`, `RXMM1`, …) onto the same numeric values as the GP register constants (`RXMM0 = enc.RRAX`, and so on) — XMM and GP registers share the same 0–15 encoding slots in a ModRM byte, so this reuses the existing `Reg` numbering rather than introducing a parallel sixteen-register type.

No IR value survives in a register across an `Inst` boundary, so all scratch registers are fair game within one instruction's lowering.

---

## 4. Global and Symbol Addressing

Unlike the IA-32 backend, AMD64 code addresses globals RIP-relative rather than through an absolute 32-bit immediate: `loadOperand`'s fallback path and its global case both emit `lea reg, [rip+sym]` (`MemRIP` in `opr.go`), which `toEncoderOpr` (in `encode.go`) resolves through `enc.MemRIP`. `opr.go` also carries `MemAbs`/`MemIndexed` forms for absolute-symbol and scaled-index addressing, used by `selIndex`'s array-indexing path when the element size is a valid SIB scale (1/2/4/8).

---

## 5. Zero-Extension Invariant

A value of type `iN` lives in a full 8-byte slot. For `N < 32`, `maskTo` restores zero-extension with an explicit `and` after any operation that could carry into the upper bits. For `N == 32`, no explicit mask is needed: a 32-bit-wide `mov`/`add`/etc. natively zero-extends into the upper 32 bits of its 64-bit parent register (`maskArg`'s comment on this is explicit — this is relied on rather than re-derived at every call site). For `32 < N ≤ 64`, `widthOf` snaps the operand width up to a full 8-byte instruction, so the computation simply uses all 64 bits. Only signed consumers (`sdiv`/`srem`, `ashr`, signed compares, `sext`, `abs`) sign-extend, via `sext32` into a scratch copy that's never written back to a slot — and, notably, `sext32` only ever widens as far as 32 bits (it's a `shl`/`sar` pair sized 4), so a `sext` whose *destination* type is wider than 32 bits relies on `maskTo` being a no-op there rather than on any further explicit 64-bit sign-extension step.

---

## 6. Division and Trapping

AMD64's `idiv`/`div` are baseline instructions — no software loop is needed. At the instruction's own operating width (32 or 64 bits, chosen by `widthOf`), the hardware already raises `#DE` on a zero divisor and on `INT_MIN / -1`, satisfying the trap contract directly for `i32` and `i64`.

For narrower signed types (`i8`/`i16`/`i1`), `selDivide` sign-extends into a 32-bit scratch copy via `sext32` and then runs a 32-bit `idiv`. This differs from the IA-32/ARM/AArch64 backends' `selDivide`, which each add an explicit `cmp`/`cmp`/`jcc`/`ud2` guard ahead of the division specifically to catch a narrow `INT_MIN / -1` (e.g. `i8`'s `-128 / -1`) becoming a *representable* 32-bit quotient (`128`) that the hardware would silently compute instead of trapping on. No equivalent guard appears in this backend's `selDivide` — the zero-divisor case still traps correctly at every width (an extended zero divisor is zero at 32 bits too), but the narrow `INT_MIN / -1` case is unguarded here.

---

## 7. Calling Convention

`callconv.go` implements both the System V AMD64 and Microsoft x64 conventions behind one shared `LayoutArgs`/`PlanCall`, selected by `Layout.OS` so callers (`PlanCall`) and callees (`BuildFrame`) can never disagree about where an argument lives.

* **Registers:** SysV uses six integer argument registers (`rdi`, `rsi`, `rdx`, `rcx`, `r8`, `r9`) with a *separate* running count for `xmm0`–`xmm7` float arguments. Windows x64 uses only four slots (`rcx`, `rdx`, `r8`, `r9`), shared *positionally* across integer and float arguments alike — argument index `i` is always register slot `i`, whichever class it is, and a float argument is mirrored into both its integer-numbered register and the same-numbered XMM register for the benefit of unprototyped/variadic callees.
* **Variadic AL Hint:** On a SysV variadic call, the number of vector registers used is loaded into `rax` before the `call`, per the ABI's requirement that a variadic callee be told how many `xmm` registers it can trust. Windows has no equivalent hint.
* **`byval`:** Implemented as a single `argClass` — `classMemory` — a real, stack-passed copy at the struct's rounded-up size, made with `rep movsb`. This is exactly ABI-correct for large structs but is a documented non-conformance for small ones: SysV's actual eightbyte-classification (splitting a ≤16-byte struct across up to two integer registers) is not implemented, and Windows's actual convention (any `byval` struct over 8 bytes is passed *by reference*, never copied inline) is not implemented either — both targets get the same SysV-shaped inline-stack-copy approximation.
* **`byval` on `tailcall`:** Rejected outright — the copy would need to land in a frame that's about to be reused. More broadly, *any* stack-passed tailcall argument (not just `byval`) is currently a `todo` on both the direct and indirect (`fnsig`) tailcall paths; only register-only tailcalls are implemented today.
* **Syscalls:** `syscall.go` defines one register list per OS for the `syscall` op — Linux (`rax` = number; `rdi`, `rsi`, `rdx`, `r10`, `r8`, `r9` = up to six args, with `r10` standing in for `rcx` since the `syscall` instruction itself clobbers `rcx`) and FreeBSD (same registers as an ordinary SysV call: `rdi`, `rsi`, `rdx`, `rcx`, `r8`, `r9`; the carry flag signals error, and only the register-argument path is modeled). `selSyscall` loads arguments before the syscall number, right-to-left, so a value already sitting in `rax` isn't clobbered before it's read. Note: the `syscall` instruction encoding (`0F 05`) is not yet wired into the encoder's own op switch, so `selSyscall`'s output is a real, localized dependency away from actually encoding — not a design gap in this package.

---

## 8. Floating Point

Unlike its IA-32/ARM/AArch64 siblings, this backend implements most scalar floating-point computation directly with SSE2 scalar instructions, moving values between GP registers and `xmm0`/`xmm1` via `movq_to_xmm`/`movd_to_xmm` (picking the 32- or 64-bit move by whether the type is `F32` or `F64`):

* Arithmetic (`add`/`sub`/`mul`), `sqrt`, and NaN-aware `min`/`max` (which special-cases equal operands to preserve `-0.0`'s sign via a bitwise `and`/`or`, and propagates NaN via an `add` when either operand compares unordered with itself) are all implemented.
* Conversions — `fpromote`/`fdemote`, `sfromint`/`ufromint`, `stoint`/`utoint`, and the saturating `stoint_sat`/`utoint_sat` (which clamp against the destination range and separately detect the "indefinite" `INT64_MIN` result `cvtt*2si` produces on overflow or NaN) — are implemented.
* `copysign`, `floor`/`ceil`/`trunc`/`nearest` (via `roundsd`/`roundss` with the appropriate rounding-mode immediate) are implemented.
* `fma` is a **non-conforming approximation**: it computes `a*b+c` as a separate multiply followed by a separate add (two roundings), not a true single-rounding fused multiply-add — this backend has no AVX/FMA3 codegen.
* Float **global initializers** are the one piece not yet implemented (`todo("float global initializer")`) — this is the mirror image of the IA-32 backend, which supports float globals but not float computation; this backend is the other way around.
* Vector/SIMD types and operations have no implementation at all (see §9).

---

## 9. `valist` Layout

This backend's `va_list` is 24 bytes and GP-only:

```text
+0   gp_offset          (u32)  byte offset into the GP save area for the next arg
+8   overflow_arg_area  (ptr)  next stack vararg
+16  reg_save_area      (ptr)  base of the 6-register GP save area
```

Because `fp_offset` (the real SysV `va_list`'s second field, at bytes 4–8) is simply never written or read — float varargs are a `todo` — `overflow_arg_area` still lands at byte offset 8, exactly where it sits in a real SysV `va_list`. So although this backend never populates the float half, the layout it does write is byte-compatible with the platform's actual `va_list` for GP/pointer access, unlike the ARM/AArch64 backends' single-pointer `va_list`, which is a documented non-conformance by design.

`va_start` sets `gp_offset` to `NamedGP*8`, points `overflow_arg_area` at `rbp+ParamEnd`, and points `reg_save_area` at the prologue's save block. `va_arg` compares `gp_offset` against 48 (six 8-byte GP registers) to choose between the register-save path and the stack-overflow path, advancing either cursor by 8 regardless of the argument's actual declared width (narrowing happens after the read via `maskTo`).

---

## 10. Missing Against the Spec (Todos)

The following valid IR constructs are not yet supported and will trigger an error at `Lower` time:

* **Floating Point:** Only global initializers for `f32`/`f64` are unimplemented; scalar computation is otherwise implemented (see §8), with `fma` as a documented two-rounding approximation rather than a true fused op.
* **Integers:** `i128` (and anything over 64 bits) is rejected — it would need a register pair throughout selection, which this backend's slot model doesn't support.
* **Vectors:** No vector value type, no vector operations (`splat`, `extract`, `insert`, `shuffle`, `gather`, `scatter`, reductions), and no masked loads/stores.
* **ABI / Calling Convention:** Windows variadic functions are rejected entirely in `BuildFrame` — the SysV GP-register-save-area mechanism this package implements (§9) is SysV-specific, and a real Windows variadic convention would need its own, not-yet-written prologue/`va_list` shape. `byval` is rejected on any tailcall; any stack-passed tailcall argument (direct or indirect) is a `todo`. Small-struct-in-register classification (SysV eightbyte splitting; Windows pass-by-reference for `byval` over 8 bytes) is not implemented — every `byval` argument gets a whole inline stack copy instead.
* **Bit Manipulation:** `popcnt` is rejected unless the module's target declares a `popcnt` or `sse4.2` feature tier. `bitrev` and `bswap`, by contrast, *are* implemented here (bit-reversal via a three-stage nibble/pair/single-bit swap-and-shift sequence, after a `bswap` for byte order) — unlike the IA-32 backend, which leaves `bitrev` unimplemented.
* **Division:** No explicit narrow-width (`i8`/`i16`/`i1`) signed `INT_MIN / -1` trap guard, unlike the IA-32/ARM/AArch64 `selDivide`s (see §6).
* **Memory / Globals:** Thread-local (`TLS`) globals carry a `TLS` flag through to `Global`, but no `%fs`-relative addressing form appears in this package's own lowering.
* **Atomics:** `and`/`or`/`xor` atomics compile to a `lock cmpxchg` retry loop, since AMD64 has no locked and/or/xor that returns the previous value — otherwise atomics are supported at whatever width (1/2/4/8 bytes) the value type calls for, with no narrower-width restriction of the kind the IA-32 backend imposes.