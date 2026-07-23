# arm (A32) lowering backend

`import lowerarm "github.com/vertex-language/vvm/lower/arm"`

Lowers a verified `vir.Module` into 32-bit ARM (A32, "ARM state") machine
code. Pure backend: no object-format, linker, or frontend knowledge, and it
assumes `vir.Verify` and — for multi-file modules — `importer.Rewrite` have
already run. Serves both `arm` and `armeb`; endianness affects only how
`globals.go` lays out data words, since BE-8 keeps the instruction stream
little-endian.

## Entry point

```go
func Lower(m *vir.Module) (*Program, error)
```

`Lower` rejects a module whose target arch isn't `arm`/`armeb`. Globals
first, then functions, each independently and in module order.

## Package layout

| File | Responsibility |
|---|---|
| `arm.go` | `Lower`, `Program`/`Func`/`Global`, `Fixup`, module-wide `index`, §6.3 symbol naming |
| `layout.go` | `Layout`: AAPCS32 size/alignment/field offsets |
| `callconv.go` | `LayoutArgs`/`PlanCall`: the one argument-placement rule |
| `frame.go` | `Frame`/`BuildFrame`, `checkValueType` |
| `typefix.go` | `typeFunc`/`resultType`: one-pass type fixation |
| `opr.go` | `Opr`/`Inst`, the pre-encoding representation |
| `isel.go` | Instruction selection |
| `isel_call.go` | Calls, tailcalls, terminators |
| `isel_va.go` | `va_start`/`va_arg`/`va_end` and the `valist` layout |
| `divide.go` | Software division and its §5.3 traps |
| `globals.go` | Global initializers to bytes + fixups |
| `syscall.go` | Per-OS syscall conventions |
| `encode.go` | Prologue/epilogue, slot resolution, encoder handoff |

## Relocations

This backend defines its own `Fixup`, like `lower/x86` and unlike
`lower/x86_64`. The reason is specific: `isa/arm/encoder`'s three kinds are
all *instruction-word bit-field* patches, because the encoder only ever
emits instructions. A `global g ptr = addr f` needs a whole 32-bit **data**
word relocated, which the encoder's vocabulary cannot name — hence
`FixupAbs32`, with the other three translated one-for-one in an explicit
switch.

## ABI and layout

AAPCS32. Unlike `lower/x86`'s Intel386 psABI there is **no 4-byte alignment
cap**: an `i64`/`f64` is 8-byte aligned and a struct's alignment is its
largest member's. Pointers are 4 bytes. Struct layout is memoized per name,
and the cache doubles as the by-value cycle guard.

`StackAlign = 8` — AAPCS's requirement at public interfaces, not a
concession to vector loads the way x86's 16 is.

## Calling convention

First four argument words in `r0–r3`, the rest on the stack, result in
`r0`. Everything goes through `LayoutArgs`, shared by `PlanCall` and
`BuildFrame`. 8-byte-aligned fundamental types round the register index up
to even and the stack offset up to 8 (implemented, though nothing that
needs it lowers yet).

## Stack frame

```
[fp+8+16+…]  incoming stack args      (variadic)
[fp+8 … 23]  r0-r3 vararg save area   (variadic only)
[fp+8+…]     incoming stack args      (non-variadic)
[fp+4]       saved lr
[fp+0]       saved fp
[fp-4 …]     local slots, one 4-byte slot per named value
```

Every named value lives in a slot and all computation happens in `r0–r3`
and `ip` — all caller-saved — so there is **nothing to preserve** beyond
`fp`/`lr`. The x86 backends' unconditional `ebx`/`esi`/`edi` save has no
counterpart here.

`Frame.Local` is rounded to 8 so `sp` stays aligned after the 8-byte push.
Slots are reached as `[fp, #-off]`, whose 12-bit displacement caps a frame
at 4092 bytes; a larger one is a `todo`.

### `valist` (`isel_va.go`)

One word — a pointer to the next variadic argument, i.e. the AAPCS
`va_list` exactly, against `lower/x86_64`'s 24-byte struct. That is
affordable because the variadic prologue pushes `r0–r3` *before* saving
`fp`/`lr`, putting the save area directly below the incoming stack args.
Argument word *i* then lives at `fp+8+4i` regardless of how it arrived, so
`va_start` is one `add`, `va_arg` is a post-indexed load, and `va_end` is a
no-op with no cursor state to reconcile.

`Frame.ParamEnd` is computed from the layout, never from `8+4*(i+1)`.

## Instruction selection

- **Value width set**: `{i1, i8, i16, i32}` plus `ptr`/`valist`. `i64`/`i128`
  need register pairs and floats need a VFP path — both `todo`.
- **Zero-extension invariant**: an `iN` fills its slot with the upper bits
  zero. `maskTo` restores it (`and` for 1/8, a `bic` pair for 16, since
  `0xFFFF` is not a modified immediate). Only signed consumers sign-extend,
  always into a scratch copy.
- **Shift counts** are masked explicitly to the operand width: A32's
  register shift zeroes at counts ≥ 32, which is *not* §4.1's rule.
- **Constants**: modified immediate → `mvn` of the complement → `movw`/`movt`
  pair. That last fallback makes **ARMv6T2 the effective baseline**; a
  literal pool would be the pre-v6T2 alternative and isn't implemented.
- **Division** (`divide.go`): no divide instruction exists in the encoder,
  and `__aeabi_idiv` would put a runtime under a no-runtime IR, so it is an
  inline restoring shift-subtract loop. Zero divisor and `INT_MIN / -1`
  (tested at the *operand's* width) emit `ud`.
- **`ctlz`** is one `clz`; the zero case falls out of the width adjustment
  for free. **`cttz`** isolates the low bit and subtracts, with an explicit
  zero case, because `rbit` isn't in the encoder.
- **`bswap`** is the eor/bic/ror sequence — no `rev` in the encoder either.
- **Bulk ops** are byte loops; `memmove` picks direction at runtime.
- **Tailcalls**: register-only. `ip` and `r0–r3` survive the epilogue, which
  touches only `sp`/`fp`/`lr`.
- **Syscalls**: EABI — number in `r7`, args `r0–r5`, `svc #0`. The sequence
  brackets itself with a push/pop because `r4`/`r5`/`r7` are callee-saved.

## Not yet implemented

All surface as `todo(...)` (suffixed `(TODO)`), distinct from a plain
`fmt.Errorf`, which means the input violated something `vir.Verify` should
have caught:

- Floats and vectors (arithmetic, conversions, `va_arg`, arguments)
- Saturating arithmetic, `popcnt`, `bitrev`
- `i64`/`i128` values
- `byval` on any call path, and arguments split across the register/stack
  boundary
- Stack-argument restaging on tailcalls
- TLS globals (needs `__aeabi_read_tp`)
- `switch` jump tables (compare chain only)

> **Encoder dependency.** Every atomic op is a `todo` for one reason:
> `isa/arm/encoder`'s instruction switch has no `ldrex`/`strex`/`dmb`, so
> there is nothing correct to emit. §5.1's atomics need all three
> (`cmpxchg` and the return-previous RMWs are `ldrex`/`strex` retry loops;
> `fence` is `dmb`). Flagged here rather than worked around, since the
> alternatives — a non-atomic sequence or a kernel helper call — would both
> be silently wrong.