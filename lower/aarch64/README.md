# aarch64 (A64) lowering backend

`import lowera64 "github.com/vertex-language/vvm/lower/aarch64"`

Lowers a verified `vir.Module` into 64-bit ARM (A64, "AArch64 state")
machine code. Pure backend: no object-format, linker, or frontend knowledge,
and it assumes `vir.Verify` and — for multi-file modules — `importer.Rewrite`
have already run. Serves both `aarch64` and `aarch64_be`; endianness affects
only how `globals.go` lays out data words, since A64 instruction fetch is
little-endian regardless.

## Entry point

```go
func Lower(m *vir.Module) (*Program, error)
```

`Lower` rejects a module whose target arch isn't `aarch64`/`aarch64_be`.
Globals first, then functions, each independently and in module order.

## Package layout

| File | Responsibility |
|---|---|
| `aarch64.go` | `Lower`, `Program`/`Func`/`Global`, `Fixup`, module-wide `index`, §6.3 symbol naming |
| `layout.go` | `Layout`: AAPCS64 size/alignment/field offsets |
| `callconv.go` | `LayoutArgs`/`PlanCall`: the one argument-placement rule |
| `frame.go` | `Frame`/`BuildFrame`, `checkValueType` |
| `typefix.go` | `typeFunc`/`resultType`: one-pass type fixation |
| `opr.go` | `Opr`/`Inst`, the pre-encoding representation |
| `isel.go` | Instruction selection |
| `isel_call.go` | Calls, tailcalls, terminators |
| `isel_va.go` | `va_start`/`va_arg`/`va_end` and the `valist` layout |
| `globals.go` | Global initializers to bytes + fixups |
| `syscall.go` | Per-OS syscall conventions |
| `encode.go` | Prologue/epilogue, slot resolution, encoder handoff |

## Relocations

This backend defines its own `Fixup`, like `lower/arm` and `lower/x86` and
unlike `lower/x86_64`. The reason is the same one `lower/arm` gives:
`isa/aarch64/encoder`'s eleven kinds are all *instruction-word bit-field*
patches, because the encoder only ever emits instructions. A
`global g ptr = addr f` needs a whole 64-bit **data** word relocated, which
that vocabulary cannot name — hence `FixupAbs64`, with the other eleven
translated one-for-one in an explicit switch.

## ABI and layout

AAPCS64. No 4-byte alignment cap (`lower/x86`'s Intel386 rule); pointers are
8 bytes and 8-byte aligned; a struct's alignment is its largest member's.
Struct layout is memoized per name, and the cache doubles as the by-value
cycle guard.

`StackAlign = 16`. Unlike x86's 16 this is not a concession to vector loads
and not merely a public-interface requirement the way AAPCS32's 8 is: the
machine faults on an sp-relative access while sp is misaligned, so it is an
invariant every instant of execution, not just at call boundaries.

## Calling convention

First eight integer/pointer arguments in `x0–x7`, the rest on the stack one
eightbyte each, result in `x0`. An `sret[S]` pointer travels in `x8`,
AAPCS64's dedicated indirect-result register, and consumes no argument
register. Everything goes through `LayoutArgs`, shared by `PlanCall` and
`BuildFrame`.

### Two variadic conventions

`callconv.go` implements both, selected by the target:

- **Base standard** — unnamed arguments are placed exactly like named ones,
  so `va_start` needs a register save area.
- **Stack-passed variadics** — §7.1's `aapcs64` ABI token ("AArch64 variant
  with stack-passed variadics") and every Mach-O target route the *unnamed
  tail* entirely to the stack. Named parameters are unaffected and no save
  area exists.

Both are real conventions on real silicon, so the choice is a target fact
rather than a preference, and it is settled once, in `LayoutArgs`.

## Stack frame

```
[fp + FrameBytes + 64 + …]  incoming stack args      (variadic, register conv.)
[fp + FrameBytes … +63]     x0-x7 save area          (variadic, register conv.)
[fp + FrameBytes + …]       incoming stack args      (otherwise)
[fp + 16 …]                 local slots, one 8-byte slot per named value
[fp + 8]                    saved lr
[fp + 0]                    saved fp                 == sp after the prologue
```

**The frame record sits at the bottom of the frame, not the top.** AAPCS64
fixes the record's contents and requires `x29` to point at it, but expressly
leaves its position within the frame unspecified — and putting it lowest is
what makes every local offset positive. That is not cosmetic: a negative
`[x29, #-off]` can only use the unscaled signed imm9 (LDUR/STUR) form, which
would cap a frame at 256 bytes, where the positive scaled imm12 reaches
32760. Frames past that are a `todo`.

Every computation happens in `x9–x12` and `x16`/`x17` — all caller-saved or
IP scratch — so there is **nothing to preserve** beyond `fp`/`lr`. `x19–x28`
are never touched and the x86 backends' unconditional `ebx`/`esi`/`edi` save
has no counterpart, exactly as in `lower/arm`.

`Frame.ParamEnd` is computed from the layout, never from `FrameBytes + 8*(i+1)`.

### `valist` (`isel_va.go`)

One word — a pointer to the next variadic argument — against
`lower/x86_64`'s 24-byte struct, and for the same reason `lower/arm` can
afford it: the variadic prologue pushes `x0–x7` *before* saving `fp`/`lr`,
putting the save area directly below the incoming stack args. Argument
eightbyte *i* then lives at `ArgBase + 8i` regardless of how it arrived, so
`va_start` is one `add`, `va_arg` a post-indexed load, and `va_end` a no-op
with no cursor state to reconcile.

This is deliberately **not** the AAPCS64 C `va_list`, a 32-byte struct with
separate GP and SIMD cursors. Under the stack-varargs convention it happens
to coincide with the platform's own one-word list; under the base standard it
does not, so a `valist` built here cannot be handed to a C `vprintf`. §3
makes the layout target-defined precisely to allow this, but it is a
**documented non-conformance** for C interop, not an oversight.

## Instruction selection

- **Value width set**: `{i1, i8, i16, i32, i64}` plus `ptr`/`valist`. Unlike
  both 32-bit backends `i64` is native — it is just a register. `i128` needs
  a register pair and floats/vectors an FP/SIMD path; both `todo`.
- **Zero-extension invariant**: an `iN` fills its slot with the upper bits
  zero. A 32-bit operation clears bits 63:32 for free, so `maskTo` is a bare
  `mov w, w` at 32 and an `and` with a bitmask immediate below it —
  `0x1`/`0xFF`/`0xFFFF` are all directly encodable, where A32 needs a `bic`
  pair for `0xFFFF`. Only signed consumers sign-extend, always into a scratch
  copy never written back.
- **Shift counts** are masked explicitly below 32 bits: A64's variable shifts
  mask modulo the *datasize* (32/64), which is §4.1's rule only when the
  value's width is the datasize.
- **Constants**: bitmask `orr` → single `movz`/`movn` → `movz`/`movk` chain.
  The encoder refuses `mov` of an immediate precisely because choosing among
  these is a lowering decision; this is where it gets made.
- **Division**: the sharpest divergence from x86. A64's `sdiv`/`udiv` **do
  not trap** — zero divisor yields 0, `INT_MIN / -1` yields `INT_MIN`, both
  quietly — so every check §4.1 and §5.3 require is emitted explicitly, with
  `INT_MIN / -1` tested at the *operand's* width, branching to `udf`. Without
  it the IR's trap semantics simply would not hold. Remainder is
  `sdiv`+`msub`.
- **`ctlz`/`cttz`** plant a sentinel bit rather than branching on zero:
  cheaper, and it cannot perturb a nonzero input. **`bitrev`** is one `rbit`
  plus a shift — implemented here, unlike either 32-bit sibling. **`bswap`**
  is `rev`/`rev16`.
- **Globals** are reached `adrp` + `add :lo12:`, the position-independent
  idiom and the only form the encoder names.
- **Bulk ops** are byte loops; `memmove` picks direction at runtime.
- **Tailcalls**: register-only. `x0–x7` survive the epilogue, which touches
  only `sp`/`x29`/`x30` and, for an oversized frame, `x16`.
- **Syscalls**: `svc #0`, number in `x8` on both supported OSes — not in an
  argument register, which is what leaves all of `x0–x7` for arguments.
  linux takes six arguments, freebsd eight with the carry flag signalling
  error.

## Not yet implemented

All surface as `todo(...)` (suffixed `(TODO)`), distinct from a plain
`fmt.Errorf`, which means the input violated something `vir.Verify` should
have caught:

- Floats and vectors (arithmetic, conversions, `va_arg`, arguments) — the
  whole SIMD&FP register class
- Saturating arithmetic; `popcnt` (A64's `cnt` is a SIMD instruction with no
  GP form)
- `i128` values
- `byval` on any call path — both AAPCS64 halves: ≤16-byte register
  classification, and the >16-byte case, which is a caller-allocated copy
  passed **indirectly by pointer**, not laid flat on the stack the way SysV
  x86-64 does it
- Stack-argument restaging on tailcalls
- TLS globals; frames beyond the 32760-byte scaled-offset reach
- `switch` jump tables (compare chain only)

> **Encoder dependency.** Every atomic op is a `todo` for one reason:
> `isa/aarch64/encoder`'s instruction switch has no `ldxr`/`stxr`/`ldar`/
> `stlr`/`dmb`, so there is nothing correct to emit. §5.1's atomics need the
> exclusives (`cmpxchg` and the return-previous RMWs are `ldxr`/`stxr` retry
> loops) and `dmb` for `fence`. Flagged here rather than worked around: a
> non-atomic sequence would be silently wrong.