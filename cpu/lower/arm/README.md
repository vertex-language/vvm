# `cpu/lower/arm`

```go
import "github.com/vertex-language/vvm/cpu/lower/arm"
```

This package lowers a verified Vertex IR module (`ir/vir.Module`, targeting `arm` or `armeb`) to A32 machine code. It outputs a `Program` containing `Func`s (raw bytes and relocation `Fixup`s) and `Global`s (raw data and `Fixup`s). Object-file emission, symbol tables, and relocation application are handled downstream; this package solely handles lowering and returns a fixup list.

## Usage

```go
prog, err := arm.Lower(module) // module: *vir.Module, already vir.Verify'd
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
| `arm.go` | Entry point (`Lower`), `Program`/`Func`/`Global` structures, custom `FixupKind`, and module-wide `index`. |
| `layout.go` | `Layout`: size, alignment, and struct-field offsets under AAPCS32. |
| `callconv.go` | `LayoutArgs`/`PlanCall`: shared argument-placement rules for callers and callees. |
| `frame.go` | `Frame`: per-function stack layout and `BuildFrame`. |
| `isel.go` | Instruction selection, scratch registers, constant materialization, and the zero-extension invariant. |
| `isel_call.go` | `call`/`tailcall` lowering and terminators. |
| `isel_va.go` | `va_start`/`va_arg`/`va_end` implementation. |
| `divide.go` | Software `sdiv`/`udiv`/`srem`/`urem` — a shift-subtract loop, since A32's base ISA has no divide instruction. |
| `syscall.go` | `syscall.<T>`, containing one `syscallConv` per OS. |
| `globals.go` | `global` initializers outputting data bytes and fixups. |
| `opr.go` | Pre-encoding `Opr`/`Inst`: `isa/arm/encoder` shapes and `OSlot`. |
| `encode.go` | Prologue/epilogue emission, `OSlot` resolution, `encoder.Inst` translation, and the final `encoder.Encode` call. |
| `typefix.go` | Forward pass to fix named value types and definition orders before frame-building. |

---

## 2. Frame Shape

The frame grows downward from the frame pointer (`r11`), which A32 has no architectural requirement for but AAPCS conventionally reserves:

```text
[fp+8+16+…]  incoming stack arguments      (variadic)
[fp+8 … +23] r0-r3 vararg save area        (variadic only)
[fp+8+…]     incoming stack arguments      (non-variadic)
[fp+4]       saved lr
[fp+0]       saved fp
[fp-4 …]     local slots, Frame.Local bytes, one 4-byte slot per value
```

* **Static Frame Pointer:** `fp` (`r11`) stays fixed after the prologue, so the epilogue always restores `sp` directly from `fp` via `mov sp, fp` — never by arithmetically undoing the prologue's `sub sp, #Local`, since that equivalence breaks the moment a dynamically sized `alloca` moves `sp` at runtime.
* **Variadic Convention:** The prologue's `push {r0-r3}` runs *before* `push {fp, lr}`, landing the four argument registers directly below the incoming stack arguments. That placement is what lets this backend's `va_list` be a single pointer: argument word *i* lives at `fp+8+4i` whether it arrived in a register or on the stack, and `va_start` is one `add`.
* **12-bit Displacement Cap:** Every slot is addressed as `[fp, #-off]`, whose immediate field is 12 bits of magnitude plus sign, so `Frame.Local` is rejected once it exceeds 4092 bytes.

---

## 3. Scratch Registers

Named values live exclusively in frame slots (no register allocator). Instruction selection uses a fixed set of registers, all caller-saved under AAPCS:

* **`r0-r3`:** Argument/return registers, also used as general scratch between instructions.
* **`r12` (IP):** The AAPCS intra-procedure scratch register — never an argument register, so it's the one register safe to clobber while argument registers are already loaded (e.g. materializing an indirect call target last).
* **`r11` (FP):** Frame pointer; every local slot is addressed off it.

Only `fp` and `lr` are saved during execution — this backend computes entirely in caller-saved registers, so unlike the x86 backends there is no unconditional callee-saved-register save in the prologue.

---

## 4. Fixup Vocabulary

The `isa/arm/encoder` strictly outputs instructions and uses `FixupKind` for instruction-word bit-field patches (`FixupPCRel24`, `FixupMovwAbs`, `FixupMovtAbs`). Because a `global` initialized with `addr f` needs a whole 32-bit *data* word relocated — something the encoder has no way to name — this package defines its own superset `FixupKind` adding `FixupAbs32`. Translation from the encoder's vocabulary happens explicitly in `fromEncoderFixup`, so a new encoder kind fails loudly instead of being silently renumbered.

---

## 5. Zero-Extension Invariant

A value of type `iN` always occupies a full 4-byte slot with the upper `32-N` bits zero. Loads use a zero-extending form (`ldrb`/`ldrh`/`ldr`), and `maskTo` restores the invariant after any operation that could carry into the upper bits. Only signed consumers (`sdiv`, `srem`, `ashr`, signed compares) sign-extend, and always into a scratch copy that's never written back to a slot.

---

## 6. Software Division

A32's base instruction set has no divide — `SDIV`/`UDIV` are an ARMv7 extension absent from `isa/arm/encoder`'s switch entirely — and a call to `__aeabi_idiv` would introduce a runtime support library under an IR designed to need none. So `selDivide` synthesizes the quotient inline with a restoring shift-subtract loop (`emitUDivCore`), with sign handling and zero/`INT_MIN / -1` trap checks layered around it using the permanently-undefined `ud` encoding.

---

## 7. `valist` Layout

This backend's `va_list` is a single pointer to the next variadic argument — the AAPCS `va_list` exactly, and a fraction of the x86-64 backend's larger GP/overflow/save-area struct. This is possible because the prologue pushes `r0-r3` immediately below the incoming stack arguments, making the whole argument list one contiguous run of words with no register/stack boundary to track separately.

---

## 8. Missing Against the Spec (Todos)

The following valid IR constructs are not yet supported and will trigger an error at `Lower` time:

* **Floating Point:** Missing float literals, float `global` initializers, and float comparisons.
* **Integers:** `i128` is unsupported (needs a register pair throughout selection).
* **ABI / Calling Convention:** `byval` parameters are rejected in the prologue; `byval` arguments are unimplemented on both call and tailcall paths; multi-register and split (register/stack) arguments are rejected; multi-word stack arguments are unimplemented; `tailcall` with any stack-passed outgoing arguments is rejected outright (restaging would overwrite an incoming argument area the call may still be reading, in a frame about to be torn down).
* **Memory / Globals:** Thread-local globals are rejected (would need `__aeabi_read_tp`); frames exceeding the 12-bit `[fp, #-off]` displacement (4092 bytes) are rejected.
* **Atomics:** `atomic.*`, `cmpxchg`, and `fence` are all rejected — none of `LDREX`/`STREX`/`DMB` are in `isa/arm/encoder`'s instruction switch.