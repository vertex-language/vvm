# x86-64 (AMD64) lowering backend

`import lowerx64 "github.com/vertex-language/vvm/lower/x86_64"`

This package lowers a verified `vir.Module` into 64-bit x86-64 (AMD64 /
Intel 64, "long mode") machine code: instruction bytes, symbols, and
unresolved fixups. Like `lower/x86` it is a pure backend — it has no
knowledge of object file formats, linking, or the frontend — and it assumes
its input has already passed `vir.Verify` and, for multi-file modules,
`importer.Rewrite` (cross-module references must already be erased into
plain calls/symbols/inline literals before `Lower` sees them).

Inline assembly is not part of this package. It was removed from `ir/vir`'s
data model and has no representation here to lower.

## Entry point

```go
func Lower(m *vir.Module) (*Program, error)
```

`Program` is a self-contained description of the lowered module:

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

type Global struct {
    Name   string
    Data   []byte // nil for zero (BSS-style) storage
    Size   uint32 // may exceed len(Data) for zero fill
    Align  uint32
    Export bool
    TLS    bool
    Fixups []encoder.Fixup
}
```

`Lower` rejects a module whose declared target architecture isn't
`"x86_64"`. Globals are lowered first (`lowerGlobal`, in `globals.go`), then
functions (`lowerFunc`, in `isel.go`), each independently and in module
order.

`Fixup`/`FixupKind` come straight from `isa/x86_64/encoder` rather than
being redeclared here — the relocation vocabulary is the encoder's, and a
downstream object writer consumes the encoder's shapes directly. That
differs from the 32-bit backend, which defines its own `Fixup`.

## Package layout

| File            | Responsibility |
|-----------------|----------------|
| `x86_64.go`     | `Lower` entry point, `Program`/`Func`/`Global` types, module-wide symbol/callable indexing (`index`) |
| `layout.go`     | `Layout`: size, alignment, and struct field offsets under the x86-64 System V psABI |
| `callconv.go`   | `LayoutArgs`/`PlanCall`: the single, shared argument-placement rule (register + stack) for both call sites and callees |
| `frame.go`      | `Frame`/`BuildFrame`: per-function stack layout (incoming args, spilled register params, saved registers, locals, varargs save area) |
| `isel.go`       | Instruction selection — the `vir.Instruction` → `Inst` translation |
| `isel_call.go`  | Calls, tailcalls, and terminators |
| `isel_va.go`    | `va_start`/`va_arg`/`va_end` and this backend's `valist` layout |
| `typefix.go`    | `typeFunc`/`resultType`: one-pass type fixation and value ordering |
| `opr.go`        | `Opr`/`Inst`: this package's pre-encoding operand and instruction representation |
| `encode.go`     | Prologue/epilogue emission, slot resolution, and final translation to `encoder.Inst` |
| `globals.go`    | Global initializer lowering into raw bytes + fixups |
| `syscall.go`    | Per-OS syscall calling conventions (`linux`, `freebsd`) |

## ABI and layout

Types are sized and aligned per the x86-64 System V psABI (`Layout`, in
`layout.go`). The rule most likely to surprise someone arriving from the
IA-32 backend: **there is no 4-byte alignment cap**. An `i64`, `f64`, or
`ptr` is 8 bytes wide *and* 8-byte aligned, and a struct's alignment is its
largest field's — computing it the 32-bit way would not be binary-
compatible with anything else on the platform. Pointers are 8 bytes, not 4.

Struct layout is memoized per name in `Layout.cache`; the same cache doubles
as a cycle guard (`Layout.busy`), so a struct that (transitively) contains
itself by value is reported as an error rather than recursing to a stack
overflow.

## Calling convention

This is the biggest structural departure from `lower/x86`. IA-32 passes
everything on the stack; System V AMD64 passes the first six INTEGER-class
(integer/pointer) arguments in registers and returns scalars in `rax`:

- Integer/pointer args go in `rdi, rsi, rdx, rcx, r8, r9`, in declaration
  order (`IntArgRegs`). Once those six are used, further INTEGER args spill
  to the outgoing stack area, one 8-byte eightbyte each (`ArgWordBytes`).
- The scalar/pointer result comes back in `rax` (`IntRetReg`).

All argument placement — register *and* stack — goes through one routine,
`LayoutArgs` (`callconv.go`), used by both `PlanCall` (caller side) and
`BuildFrame` (callee side), so the two can never drift apart about which
argument lives in `%rsi` versus `[rsp+16]`. Each argument is described by an
`ArgSlot` (register vs. stack, offset, footprint).

`PlanCall` additionally rounds the outgoing stack reservation up to
`StackAlign` (16 bytes) — not the sum of slot sizes — so a caller doing
`sub rsp, n` / `call` / `add rsp, n` leaves rsp's alignment exactly as it
found it.

### Stack alignment

`StackAlign = 16`: the psABI requires `rsp ≡ 0 (mod 16)` at the point of a
`call`, so a callee sees `rsp ≡ 8 (mod 16)` on entry. `Frame.Local` is kept
`≡ 8 (mod 16)` (`roundUpTo8Mod16`) precisely so that after `push rbp`, the
five callee-saved pushes, and `sub rsp, Local`, rsp lands back on a 16-byte
boundary before any nested call.

### Classes implemented

`callconv.go` implements the **INTEGER** and **MEMORY** argument classes
only. Two SysV features are deliberately *not* implemented and surface as
`todo(...)` at their call sites rather than emitting wrong code:

- **SSE class** (floats/vectors in `xmm0..7`) — floats aren't lowered at
  all yet, so a float argument is a `todo`.
- **Small-struct register classification.** SysV splits a ≤16-byte struct
  into up to two INTEGER/SSE eightbytes passed in registers. This backend
  passes *every* `byval` aggregate in the MEMORY class — a whole stack copy
  — which is ABI-correct for large structs and a **documented non-
  conformance for small ones** (not C-binary-compatible). `byval` on a call
  path currently returns `todo`; the hook points in `callconv.go`/`selCall`
  are shaped so the register-classification path can be added without
  reshaping the argument layout.

## Stack frame

`Frame` (`frame.go`) lays out one function from high to low address:

```
[rbp+16+…]  incoming stack arguments  (7th+ INTEGER args, via LayoutArgs)
[rbp+8]     return address
[rbp+0]     saved rbp
[rbp-8]     saved rbx
[rbp-16…]   saved r12, r13, r14, r15
[rbp-48…]   GP register save area     (variadic functions only, 48 bytes)
[rbp-…]     local slots               (Frame.Local bytes, one 8-byte slot per value)
```

`BuildFrame` assigns every named IR value an 8-byte slot and computes
parameter offsets via the same `LayoutArgs` the call site uses. Incoming
*register* parameters get a home slot they're spilled into by the prologue,
so the rest of isel reads every value uniformly out of a slot — it never has
to care whether a parameter arrived in `%rdi` or on the stack.

`Frame.ParamEnd` gives the offset where the unnamed variadic tail begins on
the stack, for `va_start`; it's computed from the actual layout rather than
`16 + 8*(i+1)`, which is only correct when no preceding parameter is `byval`
or spilled from a register.

## Instruction selection

`isel.go` (plus `isel_call.go` and `isel_va.go`) walks each function's
blocks and lowers every `vir.Instruction` and `vir.Terminator` to this
package's `Inst`/`Opr` types (`opr.go`).

Key invariants:

- **Value width set**: only `{i1, i8, i16, i32, i64}` plus `ptr`/`valist`
  can live in a named slot (`checkValueType`). `i128` needs a register pair
  and floats/vectors need an SSE path — both return a `todo` error. (Unlike
  the IA-32 backend, `i64` *is* in the native set here — it's just a
  register.)
- **Zero-extension invariant**: a value of type `iN` occupies its slot with
  the upper bits zero. 32-bit operations auto-clear bits 32–63, so `maskTo`
  only needs to re-mask after operations on widths below 32. Signed
  consumers (`sdiv`/`srem`/`sar`/signed compares/`sext`) sign-extend a
  scratch copy via `sext32`, never written back to a value slot.
- **Type fixation**: `typeFunc` (`typefix.go`) computes each named value's
  fixed type in one forward pass (per §4.3 — the first assignment,
  parameters included, fixes a name's type permanently), and returns the
  definition order used to assign local slots.

Selected areas worth knowing about:

- **Division** (`selDivide`): `cqo` sign-extends `rax` into `rdx:rax` before
  a 64-bit `idiv`; `div` zeroes `rdx` first. As on IA-32, the native-width
  `idiv`/`div` naturally trap (`#DE`) on a zero divisor and on
  `INT_MIN / -1`; narrower signed widths sign-extend into the 32-bit form,
  which shares that trapping behavior.
- **Globals** are reached RIP-relative (`MemRIP`) — the position-independent
  long-mode idiom, one byte shorter than an absolute reference. Absolute
  addressing is available (`MemAbs`, via the SIB no-base form) but not the
  default.
- **`popcnt`** is gated behind a declared `popcnt`/`sse4.2` target tier —
  it isn't baseline and would `#UD` on hardware without it.
- **Calls** (`selCall`): stack arguments are written first (they don't
  clobber argument registers), then register arguments, so a value read into
  `%rdi` isn't overwritten before a later argument reads it. A variadic call
  zeroes `al` (it reports "0 vector registers used", correct as long as no
  float varargs are passed — which is a `todo`).
- **Tailcalls** (`selTailCall`): `byval` is rejected (the copy would need a
  frame that's about to be torn down); the epilogue is expanded and then a
  `jmp` replaces the `call`, so no return address is pushed. Stack-argument
  restaging is a `todo` — register-only tailcalls are the implemented case.
- **Bulk ops** (`selBulk`): `memcopy`/`memset` use `rep movsb`/`rep stosb`
  directly (overlap is UB for `memcopy`); `memmove` is a `todo`.
- **Atomics** (`selAtomic`): `atomic_load`/`store`, `atomic_add` (via
  `lock xadd`), and `fence` (`mfence`) are implemented; the return-previous
  `and`/`or`/`xor` and `cmpxchg` retry loops are `todo`.

### `valist` layout (`isel_va.go`)

`valist` is target-defined; this backend chooses a 24-byte, **GP-only**
layout — a subset of the C `va_list`:

```
+0   gp_offset          (u32)   byte offset into reg_save_area for the next GP arg
+8   overflow_arg_area  (ptr)   next stack vararg
+16  reg_save_area      (ptr)   base of the 6-GP save area (48 bytes)
```

`va_start` seeds `gp_offset` past the named GP args, points `overflow` at
`Frame.ParamEnd`, and points `reg_save_area` at the prologue's GP save
block. `va_arg` reads from the register save area while `gp_offset < 48`,
otherwise from the overflow area, advancing the appropriate cursor. `va_end`
is a no-op — the GP-only cursor holds no state needing cleanup. `va_arg` of
a float or vector is a `todo` (the XMM save area and `fp_offset` field are
omitted).

## Encoding

`encode.go` assembles a function: it prepends the prologue, expands
`epi_ret`/`epi_jmp_sym`/`epi_jmp_r` pseudo-ops into the real epilogue plus a
`ret`/`jmp`, resolves every `OSlot` operand to an `[rbp+off]` memory operand
via `Frame.Offset`, and translates to `encoder.Inst` for final machine-code
emission.

The prologue is `push rbp` / `mov rbp, rsp` / five callee-saved pushes /
`sub rsp, Local`, then spills incoming register parameters into their home
slots, and — for variadic functions — spills all six GP argument registers
into the save area.

The epilogue restores `rsp` via `lea rsp, [rbp-SavedRegBytes]` rather than
arithmetically undoing `sub rsp, Local`. These are only equivalent if `rsp`
hasn't moved since the prologue ran, which a dynamically-sized `alloca` (a
runtime `sub rsp, n` that `Frame.Local` knows nothing about) violates.

`toEncoderOpr`/`toEncoderInst` convert explicitly rather than by numeric
cast, even though the two `OprKind` enums are declared in the same order —
`OSlot` is the one variant with no encoder equivalent, and an explicit
switch fails loudly if either enum gains a case instead of silently
reinterpreting it.

## Syscalls

`syscall.go` provides per-OS register conventions for the `syscall` op via
the long-mode `syscall` instruction (`0F 05`), not `int 0x80`:

- **linux**: number in `rax`; arguments in `rdi, rsi, rdx, r10, r8, r9`
  (`r10` replaces `rcx`, which the `syscall` instruction clobbers); result
  in `rax`.
- **freebsd**: number in `rax`; arguments in the normal SysV call registers
  (`rdi, rsi, rdx, rcx, r8, r9`); result in `rax`, with the carry flag
  signaling error. Only the register-argument path is modeled.

> **Encoder dependency.** The `syscall` op emitted here needs a one-line
> `case "syscall": e.u8(0x0F, 0x05)` in the encoder's instruction switch.
> The `syscall` opcode isn't in the provided `isa/x86_64/encoder` switch
> yet, so syscall-using functions won't encode until that case is added.
> Using `int 0x80` instead would be wrong for 64-bit userland, so the
> correct instruction is emitted and the dependency is flagged here rather
> than worked around.

## Errors

Two error shapes are deliberately distinct, exactly as in `lower/x86`:

- A plain `fmt.Errorf` means the input violated an invariant this package is
  entitled to assume `vir.Verify` already checked — treat it as a bug
  upstream (or in this package).
- A `todo(...)`-wrapped error (suffixed `(TODO)`) means the module is valid
  but this backend doesn't lower that construct yet.

Not yet implemented (all surface as `todo(...)`):

- Floats (`f16`/`f32`/`f64` arithmetic and conversions)
- Vectors (splat/extract/insert/shuffle, masked/gather/scatter, reductions)
- Saturating arithmetic
- `bitrev`, and `ctlz`/`cttz`
- `i128` values
- `memmove`
- Return-previous atomic `and`/`or`/`xor` and `cmpxchg` retry loops
- `byval` arguments on any call path (MEMORY-class copy + small-struct
  register classification)
- Stack-argument restaging on tailcalls
- Float/vector `va_arg`