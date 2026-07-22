# x86 (IA-32) lowering backend

`import lowerx86 "github.com/vertex-language/vvm/lower/x86"`

This package lowers a verified `vir.Module` into 32-bit x86 (IA-32) machine
code: instruction bytes, symbols, and unresolved fixups. It is a pure
backend — it has no knowledge of object file formats, linking, or the
frontend — and it assumes its input has already passed `vir.Verify` and,
for multi-file modules, `importer.Rewrite` (cross-module references must
already be erased into plain calls/symbols/inline literals before `Lower`
sees them).

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
    Fixups []Fixup
}

type Global struct {
    Name   string
    Data   []byte // nil for zero (BSS-style) storage
    Size   uint32 // may exceed len(Data) for zero fill
    Align  uint32
    Export bool
    TLS    bool
    Fixups []Fixup
}
```

`Lower` rejects a module whose declared target architecture isn't `"x86"`.
Globals are lowered first (`lowerGlobal`, in `globals.go`), then functions
(`lowerFunc`, in `isel.go`), each independently and in module order.

## Package layout

| File          | Responsibility |
|---------------|----------------|
| `x86.go`      | `Lower` entry point, `Program`/`Func`/`Global` types, module-wide symbol/callable indexing |
| `layout.go`   | `Layout`: size, alignment, and struct field offsets under the Intel386 psABI |
| `callconv.go` | `LayoutArgs`/`PlanCall`: the single, shared argument-area layout rule for both call sites and callees |
| `frame.go`    | `Frame`/`BuildFrame`: per-function stack layout (incoming args, saved registers, locals) |
| `isel.go`     | Instruction selection — the `vir.Instruction`/`vir.Terminator` → `Inst` translation |
| `opr.go`      | `Opr`/`Inst`: this package's pre-encoding operand and instruction representation |
| `encode.go`   | Prologue/epilogue emission, slot resolution, and final translation to `encoder.Inst` |
| `globals.go`  | Global initializer lowering into raw bytes + fixups |
| `syscall.go`  | Per-OS syscall calling conventions (`linux`, `freebsd`) |

## ABI and layout

Types are sized and aligned per the Intel386 C ABI (`Layout`, in
`layout.go`). The rule most likely to surprise someone arriving from
x86-64: **scalar alignment is capped at 4 bytes**. An `i64` or `f64` is
8 bytes wide but only 4-byte aligned — this is not a simplification, it's
what the psABI specifies, and computing struct layouts any other way would
not be binary-compatible with anything else on the platform.

Struct layout is memoized per name in `Layout.cache`; the same cache doubles
as a cycle guard, so a struct that (transitively) contains itself by value
is reported as an error rather than recursing to a stack overflow.

## Calling convention

All stack-passed arguments and parameters go through one routine,
`LayoutArgs` (`callconv.go`), used by both `PlanCall` (caller side) and
`BuildFrame` (callee side) so the two can never drift apart:

- Every argument occupies a whole number of 4-byte words (`ArgWordBytes`),
  in declaration order, with no gaps.
- The one exception is a `byval[S]` argument, which takes its struct's real
  size rounded up to 4 bytes — matching how the psABI treats structs passed
  on the stack, and making the copy a plain word-aligned `rep movsb`.
- Arguments past a callee's declared parameter list (a variadic call's
  unnamed tail) get one flat word each.

`PlanCall` additionally rounds the total reservation up to `StackAlign`
(16 bytes) — not the sum of slot sizes — so a caller doing
`sub esp, n` / `call` / `add esp, n` leaves esp's alignment exactly as it
found it.

### Stack alignment

`StackAlign = 16` exists entirely for what this backend *calls*, not for
itself — there's no SSE codegen and no value here wider than 4 bytes in a
slot. Any libc built with a modern compiler expects `(%esp + 4) ≡ 0 (mod 16)`
at a call's entry point (per the Intel386 psABI) and will fault on a
misaligned `movaps`. `Frame.alignLocal` and `PlanCall`'s rounding are both
in service of this one invariant.

## Stack frame

`Frame` (`frame.go`) lays out one function from high to low address:

```
[ebp+8+…]  incoming arguments   (via LayoutArgs)
[ebp+4]    return address
[ebp+0]    saved ebp
[ebp-4]    saved ebx
[ebp-8]    saved esi
[ebp-12]   saved edi
[ebp-16…]  local slots, Frame.Local bytes, one per named value
```

`BuildFrame` assigns every named IR value a slot and computes parameter
offsets via the same `LayoutArgs` call site uses. `Frame.Local` is always
`≡ 12 (mod 16)`, so that after the prologue's pushes (16 bytes, no net
change mod 16) and the `sub esp, Local`, esp lands on a 16-byte boundary —
at a cost of at most 12 dead bytes per frame, including leaf functions.

`Frame.ParamEnd` gives the offset one byte past a named parameter, for
`va_start`; it must be computed from the actual layout rather than
`ParamBase + 4*(i+1)`, which is only correct when no preceding parameter is
`byval`.

## Instruction selection

`isel.go` walks each function's blocks and lowers every `vir.Instruction`
and `vir.Terminator` to this package's `Inst`/`Opr` types (`opr.go`).

Key invariants:

- **Value width set**: only `{i1, i8, i16, i32}` plus `ptr`/`valist` can
  live in a named slot (`checkValueType`). `i64`/`i128` need register
  pairs and floats need an x87/SSE path — neither is implemented, so both
  return a `todo` error.
- **Zero-extension invariant**: a value of type `iN` always occupies a
  full 4-byte slot with the upper `32-N` bits zero. Only signed consumers
  (`sdiv`, `srem`, `ashr`, signed compares, `sext`) sign-extend, via a
  destructive `sext32` on a scratch copy that's never written back.
  Producers restore the invariant with `maskTo` after anything that could
  carry into the upper bits.
- **Type fixation**: `typeFunc` computes each named value's fixed type in
  one forward pass (per §4.3 of the language spec — the first assignment,
  including parameters, fixes a name's type permanently).

Selected areas worth knowing about:

- **Division** (`selDivide`): 32-bit `idiv`/`div` naturally traps (`#DE`)
  on a zero divisor and on `INT_MIN / -1`. Narrower signed widths need an
  explicit check first, since sign-extension can make `INT_MIN/-1`
  spuriously representable at 32 bits.
- **popcnt** is gated behind a declared `popcnt`/`sse4.2` target tier — it
  isn't baseline IA-32 and would `#UD` on hardware without it.
- **Calls** (`selCall`/`writeArgs`): arguments are written directly into
  the reserved outgoing area; `byval` arguments are copied with
  `rep movsb`.
- **Tailcalls** (`selTailCall`): outgoing arguments are staged below the
  current frame and then block-copied up into the incoming argument area,
  rather than written there directly — direct writes could destroy a
  still-unread parameter when argument `i` overlaps parameter `j > n`.
  `byval` is rejected on a tailcall path (the copy would need a frame
  that's about to be torn down).
- **Bulk ops** (`selBulk`): `memcopy`/`memset` use `rep movsb`/`rep stosb`
  directly (overlap is UB for `memcopy`); `memmove` picks direction at
  runtime based on pointer comparison.
- **Atomics** (`selAtomic`): restricted to 32-bit operands. `and`/`or`/`xor`
  have no locked form that returns the previous value, so they're lowered
  as a `cmpxchg` retry loop.

Not yet implemented (all surface as `todo(...)` errors, distinguishable
from a malformed-module error by the `(TODO)` suffix):

- Floats (`f16`/`f32`/`f64` arithmetic and conversions)
- Vectors (splat/extract/insert/shuffle, masked/gather/scatter, reductions)
- Saturating arithmetic
- `bitrev`
- `i64`/`i128` values

## Encoding

`encode.go` assembles a function: it expands `epi_ret`/`epi_jmp_sym`/
`epi_jmp_r` pseudo-ops into the real epilogue plus a `ret`/`jmp`, resolves
every `OSlot` operand to an `[ebp+off]` memory operand via `Frame.Offset`,
and translates to `encoder.Inst` for final machine-code emission.

The epilogue restores `esp` via `lea esp, [ebp-SavedRegBytes]` rather than
arithmetically undoing the prologue's `sub esp, Local`. These are only
equivalent if `esp` hasn't moved since the prologue ran, which a
dynamically-sized `alloca` (a runtime `sub esp, n` `Frame.Local` knows
nothing about) violates.

`toEncoderOpr`/`toEncoderInst` convert explicitly rather than by numeric
cast, even though the two `OprKind` enums are currently declared in the
same order — `OSlot` is the one variant with no encoder equivalent, and an
explicit switch fails loudly if either enum gains a case instead of
silently reinterpreting it.

## Syscalls

`syscall.go` provides per-OS register conventions for the `syscall` op:

- **linux**: arguments in `eax, ebx, ecx, edx, esi, edi, ebp`; `int 0x80`.
- **freebsd**: only the syscall number goes in `eax`; arguments are pushed
  on the stack above a 4-byte placeholder where a return address would sit
  for a normal call.

Both trap via `int 0x80` and return their result in `eax`.

## Errors

Two error shapes are deliberately distinct:

- A plain `fmt.Errorf` means the input violated an invariant this package
  is entitled to assume `vir.Verify` already checked — treat it as a bug
  upstream (or in this package).
- A `todo(...)`-wrapped error (suffixed `(TODO)`) means the module is
  valid but this backend doesn't lower that construct yet.