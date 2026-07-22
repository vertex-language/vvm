# lower/x86

```
github.com/vertex-language/vvm/lower/x86
```

Lowers a verified Vertex IR module (`*vir.Module`) targeting `arch = "x86"` into 32-bit IA-32 machine code: instruction bytes, data sections, symbols, and unresolved fixups. That's the whole job — this package never emits an object-file container (ELF/Mach-O/PE), never resolves external symbols, and never checks anything `ir/verify` already checked.

---

## Import path

```go
import lowerx86 "github.com/vertex-language/vvm/lower/x86"
```

---

## Preconditions this package assumes

`Lower` does not re-verify its input. By the time a module reaches here, the caller is expected to have already run:

1. **`vir.Verify`** — the module is internally well-formed: every opcode's arity/operand-constraint/result-rule holds, blocks are properly terminated, `valist` linear-use rules are satisfied, etc. (`ir/verify`.)
2. **`importer.Rewrite`**, if the module came from more than one source file — every cross-module `import`/qualified-ident reference has already been erased: `const` references are inlined literals, `fn`/`global` references are real mangled extern-style symbols. This package's `lookupCallable` and `kinds` map only ever look at the module's own declarations; they have no notion of `Import` or `Operand.Qualifier` at all.

`Lower` checks two things itself. The first is that `m.Target.Arch == "x86"` when a target is declared, since a module built for a different architecture is a caller error rather than a verification failure. The second is narrower but worth naming: `layout.go` detects a struct that contains itself by value and reports it. `ir/verify` should reject such a module first — but "upstream should have caught it" is not a reason for malformed input to blow the goroutine stack instead of producing a diagnostic.

---

## What isn't here

**No inline assembly.** Vertex IR's inline/native assembly support was removed from `ir/vir`'s data model entirely (see `ir/vir`'s `asm.md` for where it's headed instead). There is no `Instruction.Asm`, no `vir.AsmBlock`, no per-dialect operand parsing, no register-name table lookup in this package. If that support returns in some other form, it gets its own lowering path.

**No object-file emission.** `Func.Code`/`Global.Data` are raw bytes plus a `[]Fixup` list — turning that into an actual `.o`/`.obj` with sections, relocations in the target format's own encoding, and a symbol table is downstream of this package.

**No register allocation in the usual sense.** Every named IR value gets a fixed stack slot (`frame.go`); there is no attempt to keep hot values in registers across instructions. `REAX`/`RECX`/`REDX` are used as scratch within a single instruction's lowering and nothing survives in them across an `Inst` boundary. This is a correctness-first baseline, not a fast codegen strategy.

**No `mcode`, `abi`, or `regalloc` sub-packages.** Those splits were tried and bought no independence. The only sub-package split that survived is `isa/x86` vs. this package, and that one is load-bearing.

---

## Package layout

* **`x86.go`** — `Lower`, `Program`, `Func`, `Global`, `Fixup`/`FixupKind` (re-exported from `isa/x86/encoder`). Walks a module's globals and functions in order, builds the `lowerer`'s name→kind index and its `callables` index (call targets, whether locally defined or extern, with their return type, parameters, and variadic flag), and delegates per-global/per-function work to `globals.go`/`isel.go`.
* **`isel.go`** — `fnLower`, `lowerFunc`, `selInst`/`selTerm`/`selCall`/`selSyscall`/`selVaStart`/`selVaArg`/`selOverflow`/`selTailCall`. Instruction selection: one `vir.Instruction`/`vir.Terminator` at a time, translated into a flat `[]Inst` pseudo-instruction stream. Also owns `typeFunc` and `checkValueType`.
* **`callconv.go`** — `ArgSlot`, `LayoutArgs`, `PlanCall`, and the layout constants (`ArgWordBytes`, `ParamBase`, `StackAlign`). `LayoutArgs` is the single implementation of the argument layout; `PlanCall` is the call-site wrapper that adds stack-alignment padding.
* **`frame.go`** — `Frame`, `BuildFrame`, `SavedRegBytes`. Assigns every named IR value a fixed offset from `ebp` and computes incoming-parameter offsets *through `LayoutArgs`*, so caller and callee cannot disagree.
* **`encode.go`** — `assemble`, `prologue`/`epilogue`, `resolveSlot`, `toEncoderInst`. Takes `isel.go`'s `[]Inst`, expands `epi_ret`/`epi_jmp_sym`/`epi_jmp_r` into a real epilogue, resolves every `OSlot` to a concrete `[ebp+off]` operand, and hands the result to `isa/x86/encoder.Encode`.
* **`syscall.go`** — `SyscallConvention`, `syscallConventionFor`. Per-OS argument-register/trap-instruction tables (`linux`, `freebsd`) that `selSyscall` reads off `m.Target.OS`.
* **`layout.go`** — `Layout`, `newLayout`. Struct field offsets/sizes/alignment under the Intel386 C ABI, memoized, consulted by `isel.go`, `globals.go`, and `callconv.go`.
* **`globals.go`** — `lowerGlobal`, `dataw`. Emits a `global`'s initializer as raw bytes plus address fixups.
* **`opr.go`** — `Opr`, `OprKind`, `Inst`, and their constructors. This package's own pre-assembly instruction representation, one layer above `isa/x86/encoder.Inst`: `Opr` has an `OSlot` variant for an IR value's not-yet-resolved home, which the generic encoder's `Opr` deliberately has no concept of.

There is no `asm.go` and no `asm_dialects.go` in this package.

---

## Calling convention

A flat, self-imposed cdecl-like convention — this isn't dictated by `ir/vir` (§7.1's `abi` token space doesn't require one specific x86 32-bit ABI), it's this backend's own consistent choice, with the parts that interoperate with the platform taken from the Intel386 psABI.

* Arguments occupy the stack in declaration order, each taking a whole number of **4-byte words**: `i8`/`i16`/`i32`/`ptr` all take exactly one, regardless of declared width. This is what lets `PlanCall` and `BuildFrame` agree on layout without sharing per-argument type information.
* `byval[S]` arguments are the one exception: they take the struct's real size rounded up to 4 — matching the psABI's rule that structs passed on the stack are rounded to a multiple of four — and are copied into the outgoing argument area with a `rep movsb`.
* Callee reads its own parameters starting at `[ebp+8]`. `[ebp]` holds the saved `ebp`, `[ebp+4]` the return address. **The offset of parameter *i* is not `8+4*i` in general** — see below.
* Return value in `EAX` (or nothing, for `void`/`sret`-style).
* Callee-saved: `EBX`, `ESI`, `EDI`, `EBP` (all four pushed in the prologue). Caller-saved: `EAX`, `ECX`, `EDX`.
* **`esp` is 16-byte aligned at every call instruction.** The psABI states this as "`(%esp + 4)` is a multiple of 16 when control is transferred to the function entry point." Nothing this backend emits for itself needs it — there's no SSE codegen here — but a libc built with any modern compiler will use `movaps` on its own frame, and `movaps` on a misaligned address faults. `alignLocal` keeps `Frame.Local ≡ 12 (mod 16)` so the frame bottom lands on a boundary, and `PlanCall` rounds its reservation to 16 so a `sub esp, n` / `add esp, n` pair around a call leaves the alignment untouched. Cost: at most 12 bytes of dead stack per frame.

### One layout, two sides

`LayoutArgs` is the only place the argument layout is computed. `PlanCall` calls it at the call site; `BuildFrame` calls it for the callee's own parameters. They used to be separate implementations that agreed only for functions with no `byval` parameter — `PlanCall` expanded `byval` to its real size while `BuildFrame` placed every parameter at `[ebp+8+4*i]` — which silently misplaced every parameter declared after a `byval` one, at both ends. Anything that changes the layout has to change `LayoutArgs`, and there is nowhere else to change it.

The corollary for consumers: **compute parameter offsets from `Frame`, never from an index.** `Frame.Offset(name)` gives a parameter's address; `Frame.ParamEnd(name)` gives where the next argument starts; `Frame.VarargsBase()` gives where the unnamed tail starts.

### Variadic arguments (§4.4)

Because every argument lands on the stack in declaration order with no gaps, a `valist` cursor on this backend is a plain pointer that starts just past the last named parameter and advances 4 bytes per `va_arg`:

* **`alloca.valist`** emits no code at all. Its result never gets a dynamic size, so the named value simply gets an ordinary frame slot the first time something writes to it.
* **`va_start.<fnsig> dst, last_named`** computes `lea eax, [ebp + fr.ParamEnd(last_named)]` and stores that into `dst`'s slot. It must ask the `Frame`, not recompute `8+4*(i+1)`: that formula is only correct when no preceding parameter is `byval`.
* **`va_arg.<T> src`** reads `[src]` as `T`, advances `src` by 4, and writes the advanced cursor back to `src`'s own slot.
* **`va_end src`** is a genuine no-op in codegen — there's no register-save area or ABI-mandated cleanup on this convention. It's still required at the source level (the language spec treats an unclosed `valist` across `return` as a verification error, since on some *other* target it could correspond to live frame state), it just happens to compile to nothing here.

`va_arg` only supports destination types this backend can already hold in a slot — scalar `iN` (≤32 bits) and `ptr`.

---

## Frame and epilogue

From high address to low:

```
[ebp+8+…]  incoming arguments        (caller's, laid out by LayoutArgs)
[ebp+4]    return address            (caller's)
[ebp+0]    saved ebp
[ebp-4]    saved ebx
[ebp-8]    saved esi
[ebp-12]   saved edi                  <- SavedRegBytes
[ebp-16…]  local slots, Frame.Local bytes
```

The epilogue is `lea esp, [ebp-12]` followed by four pops, **not** `add esp, Local`. The two are only equivalent when `esp` hasn't moved since the prologue, and a dynamically-sized `alloca.ptr` lowers to a runtime `sub esp, n` that `Frame.Local` knows nothing about. With the arithmetic form, that left `esp` low by `n`, the four pops restored garbage into `edi`/`esi`/`ebx`/`ebp`, and `ret` jumped to a bogus address. The `lea` form is correct whatever `esp` has been doing, encodes in the same three bytes, and doesn't need `Frame.Local` at all — which is why `epilogue()` no longer takes the `Frame`.

It leaves `esp` at `ebp+4` after the pops, so a tailcall's outgoing arguments — written into the incoming argument area at `[ebp+8+…]` — are exactly where the callee will look for them at `[esp+4+…]`.

---

## Type support

What can appear as a named IR value on this backend today (`checkValueType`):

| Type | Supported | Notes |
|---|---|---|
| `iN`, N ≤ 32 | yes | `i1`/`i8`/`i16`/`i32` in a 4-byte slot, narrowed on load per `szOf`. |
| `iN`, N > 32 | no | `i64`/`i128` need register pairs — not implemented. |
| `ptr` | yes | 4 bytes. |
| `valist` | yes (backend-internal) | Opaque at the IR level; a plain 4-byte cursor here. |
| `fN` | no | No x87/SSE codegen path yet — every float op returns `"not lowered on x86 (TODO)"`. |
| `vec[T,N]` | no | No vector tier implemented. |
| `struct`/`array` | n/a | Never a named value (`IsAggregate`) — accessed only through `field.ptr`/`index.ptr`, laid out per `layout.go`. |

`layout.go` still knows sizes and alignments for the unsupported types, because globals may be declared with them even when no instruction can hold one. The i386 rule that most often surprises: **scalar alignment is capped at 4 bytes.** `i64` and `f64` are 8 bytes with 4-byte alignment. That's the psABI, not a shortcut — struct layouts computed with 8-byte alignment would not be binary compatible with anything else on the platform.

Opcodes still unimplemented: `uadd_sat`/`sadd_sat`/`usub_sat`/`ssub_sat`, `bitrev`, atomic RMW ops narrower than 32 bits, `cmpxchg` narrower than 32 bits, and tailcalls needing a larger outgoing frame than the caller's own incoming one. Each fails with a `(TODO)`-suffixed error rather than silently miscompiling.

---

## Known limitations

* **No register allocation.** Every value round-trips through its stack slot between instructions. Correct, not fast.
* **`alloca.ptr`'s dynamic-size stack bump isn't reflected in `Frame.Local`.** It no longer matters for correctness — the epilogue restores `esp` from `ebp` — but `Frame.Local` genuinely doesn't describe total stack usage, so nothing should treat it as a bound.
* **A dynamic `alloca.ptr` breaks call alignment.** `sub esp, n` with a runtime `n` can leave `esp` off a 16-byte boundary for any call made afterwards. Rounding the runtime size up to `StackAlign` in `isel.go` would fix it; nothing currently does.
* **Every non-`byval` parameter is a flat 4 bytes.** This is intentional and load-bearing. It now lives in exactly one place (`LayoutArgs`), so changing it is a single edit rather than three that have to stay in sync.
* **No frame-growing tailcalls.** `selTailCall` rejects a tailcall whose outgoing argument bytes exceed `Frame.ArgBytes`, since reusing the caller's frame can't grow it.