# `cpu/lower`

```go
import "github.com/vertex-language/vvm/cpu/lower/{aarch64,arm,x86,x86_64}"
```

This directory contains the four machine-code backends for Vertex IR: `aarch64` (A64, `aarch64`/`aarch64_be`), `arm` (A32, `arm`/`armeb`), `x86` (IA-32), and `x86_64` (AMD64, System V or Microsoft x64). Each backend independently lowers a verified `ir/vir.Module` to a `Program` of `Func`s and `Global`s — raw bytes plus relocation `Fixup`s. Object-file emission, symbol tables, and relocation application are all handled downstream of every backend here; none of these packages do it themselves.

Every backend exposes the same shape:

```go
prog, err := <pkg>.Lower(module) // module: *vir.Module, already vir.Verify'd
if err != nil {
    // module is valid IR this backend can't lower yet, or targets the wrong arch
    log.Fatal(err)
}
```

`err` never means invalid IR — `vir.Verify` already ruled that out upstream. It means either an arch mismatch or a valid construct this particular backend hasn't implemented (see each package's §"Missing Against the Spec").

For opcode semantics, see the top-level IR spec (`README.md`). This document only compares the four backends' architecture; each package's own `README.md` is authoritative for its details.

---

## 1. Shared Design

All four backends share a design that this directory-level doc treats as fixed:

* **No register allocator.** Named IR values live exclusively in frame slots on every target. Instruction selection uses a small, fixed set of scratch registers per architecture and never keeps an IR value live in a register across an `Inst` boundary.
* **Static frame pointer.** Every backend keeps its frame pointer (`x29`/`r11`/`ebp`/`rbp`) fixed after the prologue, so the epilogue can always restore the stack pointer directly from it rather than arithmetically undoing the prologue's stack adjustment. This is what lets a dynamically-sized `alloca` move the stack pointer at runtime without breaking function return.
* **Shared caller/callee argument layout.** Each backend has one `LayoutArgs`/`PlanCall` pair used by both call sites and callees (`BuildFrame`), so a caller and callee can never disagree about where an argument lives.
* **A `Lower`-time TODO list, not a runtime one.** Each backend's unsupported constructs are still valid IR — they simply aren't lowered yet — and are rejected with an error at `Lower` time rather than miscompiled.

---

## 2. Layout Comparison

| | `aarch64` | `arm` | `x86` | `x86_64` |
| --- | --- | --- | --- | --- |
| Word size | 64-bit | 32-bit | 32-bit | 64-bit |
| ABI | AAPCS64 | AAPCS32 | Intel386 C ABI | SysV AMD64 / Microsoft x64 |
| Frame pointer | `x29`, static | `r11`, static | `ebp`, static | `rbp`, static |
| Frame growth | upward from fp | downward from fp | downward from fp | downward from fp |
| Register allocator | none | none | none | none |
| Division | hardware (`sdiv`/`udiv`), trap-checked | **software** shift-subtract loop | hardware (`idiv`/`div`) | hardware (`idiv`/`div`) |
| `i128` | unsupported | unsupported | unsupported (even `i64` is) | unsupported |
| Own `FixupKind` | yes, superset | yes, superset | no — aliases encoder's | no — uses `enc.Fixup` directly |
| `isel_call.go`/`isel_va.go`/`typefix.go` split out | yes | yes | no (folded into `isel.go`) | yes |

`x86` is the outlier in three ways worth noting: it's the only 32-bit backend with hardware division (no `divide.go`), the only backend small enough to fold call/vararg/typefix logic into one `isel.go`, and the only backend that rejects `i64` as a named value (not just `i128`) — a register-pair limitation the 64-bit backends don't share.

---

## 3. Frame Shapes

Each frame keeps the same three ideas — a frame-record anchor, positive-reach locals where the ISA rewards it, and a fixed frame pointer — but the direction and cost model differ:

* **`aarch64`** grows **upward**: locals sit at `[fp+16…]`, keeping every local offset positive so it can use the scaled unsigned imm12 form (32760-byte reach) instead of the 256-byte unscaled imm9 form.
* **`arm`, `x86`, `x86_64`** grow **downward** from the frame pointer, with saved registers and locals at negative offsets.
* **Displacement caps differ by ISA:** `arm` locals are capped at 4092 bytes (12-bit signed `[fp, #-off]` immediate); `aarch64` frames are capped at 4095×8 bytes (scaled imm12); `x86`/`x86_64` have no comparable immediate-encoding cap described, but `x86_64`'s `Frame.Local` is constrained mod 16 for alignment instead.
* **Variadic save areas** exist only where the register convention needs one: `aarch64`'s GP save area, `arm`'s `push {r0-r3}` (placed *before* `push {fp, lr}` specifically so it lands directly below incoming stack args), and `x86_64`'s SysV-only 48-byte GP save area (Windows variadic functions are rejected outright — see §7). `x86`'s IA-32 convention is stack-only and needs no save area at all.
* **`x86_64` alone splits on OS**: Microsoft x64 reserves 32 bytes of caller-owned "shadow space" above the return address that SysV has no equivalent of, so the first incoming stack argument sits at a different `rbp`-relative offset per convention (`rbp+48` vs `rbp+16`).
* **16-byte call alignment** is only relevant to the two backends that can call into libc code using SSE: `x86` pads `Frame.Local` to keep the incoming-call alignment even though its own codegen never needs it, and `x86_64` keeps `Frame.Local ≡ 8 (mod 16)` for the same reason.

---

## 4. Scratch Registers

| | Caller-saved scratch | Reserved for addressing/indirect targets | Callee-saved, but free to clobber internally |
| --- | --- | --- | --- |
| `aarch64` | `x9-x12` (RegA-RegD), `v13` (RegFA, float) | `x16` (IP0/RegAddr), `x17` (IP1/RegAux) | none — only `fp`/`lr` saved |
| `arm` | `r0-r3` | `r12` (IP) | none — only `fp`/`lr` saved |
| `x86` | `eax`, `ecx`, `edx` | — | `ebx`, `esi`, `edi` (also string-op operands) |
| `x86_64` | `rax`, `rcx`, `rdx`, `rsi`, `rdi`, `r8`-`r10`, `xmm0`-`xmm7` | `r11` (indirect call/tailcall target) | `rbx`, `r12`-`r15` |

`aarch64` and `arm` compute entirely in caller-saved registers and save nothing but the frame-record pair (`fp`/`lr`); `x86` and `x86_64` instead unconditionally save a block of callee-saved registers in the prologue and then treat them as ordinary scratch for the rest of codegen. `x86_64` is unique in aliasing its XMM float-scratch constants onto the same numeric encoding slots as its GP constants (`RXMM0 = enc.RRAX`), reusing one `Reg` type instead of introducing a second.

---

## 5. Fixup Vocabularies

All four backends need one relocation kind their underlying `isa/<arch>/encoder` can't express: a full-width absolute *data-word* relocation for a `global` initialized with `addr f` (the encoder itself only patches instruction-word bit fields). How each backend closes that gap differs:

* **`aarch64`, `arm`:** define their own superset `FixupKind` (adding `FixupAbs64`/`FixupAbs32` respectively) and translate the encoder's vocabulary into it through an explicit switch (`fromEncoderKind`/`fromEncoderFixup`), so a new encoder-side kind fails loudly instead of being silently renumbered.
* **`x86`:** has no gap to bridge in the first place — `isa/x86/encoder` already defines `FixupAbs32`, which is exactly a 32-bit data-word relocation on a 32-bit target, so `FixupKind`/`Fixup` are plain re-exported aliases with no second enum.
* **`x86_64`:** doesn't even define a package-level `FixupKind` — `Fixups` fields are typed directly as `[]enc.Fixup`, and call sites reach for `enc.FixupAbs64` directly, since a 64-bit data-word relocation is exactly what `addr f` needs and the encoder already names it.

---

## 6. Zero-Extension Invariant

Every backend maintains the same invariant — an `iN` value's slot has its upper unused bits zeroed, loads zero-extend, and `maskTo` restores the invariant after any operation that could disturb it, with signed consumers sign-extending into a scratch copy that's never written back — but the slot width and the cost of restoring it differ:

* **`aarch64`, `arm`, `x86`:** the slot is the architecture's native word (8, 4, and 4 bytes respectively) and `maskTo` runs after any narrowing op.
* **`x86_64`:** the slot is always 8 bytes, but for `N == 32` no explicit mask is needed at all — a 32-bit `mov`/`add`/etc. natively zero-extends into the parent 64-bit register, so the backend relies on that rather than re-deriving it per call site. `sext32` only ever widens as far as 32 bits, so a `sext` to a wider destination type relies on `maskTo` being a no-op there rather than any further explicit 64-bit sign-extension.

---

## 7. Division and Trapping

| | Mechanism | Zero-divisor trap | `INT_MIN / -1` trap |
| --- | --- | --- | --- |
| `aarch64` | hardware `sdiv`/`udiv` (doesn't trap on its own) | explicit `udf` branch in `selDivide` | explicit `udf` branch in `selDivide` |
| `arm` | **software** shift-subtract loop (`emitUDivCore`) — A32 baseline has no divide instruction | explicit check, `ud` encoding | explicit check, `ud` encoding |
| `x86` | hardware `idiv`/`div`; traps `#DE` natively at 32 bits | native `#DE` at every width | native at 32 bits; explicit `cmp`/`cmp`/`jcc`/`ud2` guard added for `i8`/`i16`/`i1` |
| `x86_64` | hardware `idiv`/`div`; traps `#DE` natively at 32/64 bits | native `#DE` at every width | native at `i32`/`i64`; **no guard** for narrow (`i8`/`i16`/`i1`) signed division — a documented gap unlike the other three backends |

`arm` is the only backend that needs a software division routine at all, since A32's base ISA has no divide instruction. `x86_64` is the only backend that leaves the narrow-width `INT_MIN / -1` case unguarded — its `selDivide` sign-extends narrow operands into a 32-bit scratch and runs a 32-bit `idiv` without the extra compare/trap sequence the other three backends add for exactly this case.

---

## 8. Calling Conventions and `valist`

* **`aarch64`, `arm`:** `va_list` is a single pointer, which is architecturally exact for `arm` (matches the real AAPCS `va_list`) but a documented non-conformance for `aarch64` (diverges from the real register/stack-split C `va_list`) — acceptable because the IR only needs opcode correctness within its own module, not ABI interop.
* **`x86`:** `va_list` is likewise a single 4-byte pointer, computed from the last named parameter's end rather than a fixed per-argument formula, since a `byval` parameter can shift that formula.
* **`x86_64`:** the outlier — a real 24-byte, GP-only `va_list` (`gp_offset`/`overflow_arg_area`/`reg_save_area`) that is byte-compatible with the platform's actual SysV `va_list` for GP/pointer access, because `fp_offset` (float varargs) is simply never populated. This is the only backend whose `valist` is ABI-real rather than a documented simplification.
* **`byval`:** `x86` and `x86_64` both implement it as a real stack-passed copy (`rep movsb`) — `x86_64`'s version is a documented non-conformance for small structs, since it skips SysV eightbyte-register-classification and Windows's pass-by-reference convention, applying the same inline-copy shape to both OSes. `arm` rejects `byval` entirely on both call paths. All four backends reject `byval` (or any stack-passed argument, on `x86_64`) on `tailcall`, for the same reason: the copy would land in a frame that's about to be reused.
* **Syscalls:** each backend's `syscall.go` holds one calling convention per OS (`aarch64`/`arm`/`x86_64` cover Linux and/or FreeBSD; `x86` covers Linux via `int 0x80` and FreeBSD's stack-based convention).

---

## 9. Floating Point and Vectors

Float and vector support is the widest point of divergence:

* **`aarch64`, `arm`:** essentially no scalar float codegen yet (register materialization, globals, conversions, comparisons, and math functions are all still `todo`), and no vector tier at all.
* **`x86`:** no float value type can be held in a slot at all — every float *operation* is unimplemented — but float *global initializers* are a partial exception (`f32`/`f64` literals are emitted directly into data). No vector tier.
* **`x86_64`:** the inverse of `x86` — scalar float computation is mostly implemented directly with SSE2 (arithmetic, `sqrt`, NaN-aware `min`/`max`, conversions, `copysign`, rounding), with `fma` as a documented two-rounding approximation (no AVX/FMA3 codegen). The one gap is float global initializers, which remain `todo`. No backend implements any vector type or operation (`splat`, `extract`, `insert`, `shuffle`, `gather`, `scatter`, reductions, masked loads/stores).

---

## 10. Cross-Backend Feature Matrix

| Feature | `aarch64` | `arm` | `x86` | `x86_64` |
| --- | --- | --- | --- | --- |
| Scalar float arithmetic | ✗ | ✗ | ✗ | ✓ |
| Float global initializers | ✗ | ✗ | ✓ (`f32`/`f64`) | ✗ |
| Vectors/SIMD | ✗ | ✗ | ✗ | ✗ |
| `i64` as named value | ✓ | ✓ | ✗ | ✓ |
| `i128` | ✗ | ✗ | ✗ (global-init only) | ✗ |
| `byval` (ordinary call) | ✗ | ✗ | ✓ | ✓ (approximate) |
| `byval`/stack args on `tailcall` | ✗ | ✗ | ✗ | ✗ |
| Thread-local globals | ✗ (read) | ✗ | — | flagged, not addressed |
| Atomics | — | ✗ (no `LDREX`/`STREX` in encoder) | 32-bit only | 1–8 byte, no width restriction |
| `popcnt` | ✗ | — | feature-gated | feature-gated |
| `bitrev` | — | — | ✗ | ✓ |
| Software division | ✗ | ✓ (only backend that needs it) | ✗ | ✗ |
| Narrow signed div-trap guard | ✓ | ✓ | ✓ | ✗ (documented gap) |

Each package's own `README.md`, §"Missing Against the Spec," is the authoritative, current list — this table is a snapshot for cross-backend comparison, not a substitute.