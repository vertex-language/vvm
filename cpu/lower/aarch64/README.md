# `cpu/lower/aarch64`

```go
import "github.com/vertex-language/vvm/cpu/lower/aarch64"
```

This package lowers a verified Vertex IR module (`ir/vir.Module`, targeting `aarch64` or `aarch64_be`) to A64 machine code. It outputs a `Program` containing `Func`s (raw bytes and relocation `Fixup`s) and `Global`s (raw data and `Fixup`s). Object-file emission, symbol tables, and relocation application are handled downstream; this package solely handles lowering and returns a fixup list.

## Usage

```go
prog, err := aarch64.Lower(module) // module: *vir.Module, already vir.Verify'd
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
| `aarch64.go` | Entry point (`Lower`), `Program`/`Func`/`Global` structures, custom `FixupKind`, and module-wide `index`. |
| `layout.go` | `Layout`: size, alignment, and field offsets under AAPCS64. |
| `callconv.go` | `LayoutArgs`/`PlanCall`: Shared argument-placement rules for callers and callees. |
| `frame.go` | `Frame`: Per-function stack layout and `BuildFrame`. |
| `isel.go` | Instruction selection, scratch registers, constant materialization, and the `iN` switch. |
| `isel_call.go` | `call`/`tailcall` lowering and terminators. |
| `isel_va.go` | `va_start`/`va_arg`/`va_end` implementation. |
| `syscall.go` | `syscall.<T>`, containing one `syscallConv` per OS. |
| `globals.go` | `global` initializers outputting data bytes and fixups. |
| `opr.go` | Pre-encoding `Opr`/`Inst`: `isa/aarch64/encoder` shapes and `OSlot`. |
| `encode.go` | Prologue/epilogue emission, `OSlot` resolution, `encoder.Inst` translation, and the final `encoder.Encode` call. |
| `typefix.go` | Forward pass to fix named value types and definition orders before frame-building. |

---

## 2. Frame Shape

The frame grows upward from the frame pointer, keeping the frame record at the bottom:

```text
[fp + FrameBytes + SaveArea + …]  incoming stack args (variadic)
[fp + FrameBytes … +63]           GP save area x0-x7  (variadic, register convention only)
[fp + FrameBytes + …]             incoming stack args (non-variadic)
[fp + 16 …]                       local slots
[fp + 8]                          saved lr  (x30)
[fp + 0]                          saved fp  (x29)   == sp after the prologue
```

* **Positive Offsets:** Every local offset is positive, allowing locals to use the scaled unsigned imm12 addressing form (up to 32760 reach) instead of the unscaled imm9 form (256 reach).
* **Static Frame Pointer:** `x29` remains static after the prologue, allowing the epilogue to always safely restore `sp` directly from `x29`.
* **Variadic Conventions:** Supports the standard register-save-area convention and the stack-only tail convention. A single-word `valist` cursor handles both because the save area sits directly below incoming stack arguments.

---

## 3. Scratch Registers

Named values live exclusively in frame slots (no register allocator). Instruction selection uses a fixed set of scratch registers:

* **`x9-x12` (RegA-RegD):** General scratch.
* **`x16` (IP0 / RegAddr):** Addresses, large-frame arithmetic, and call targets.
* **`x17` (IP1 / RegAux):** Secondary addresses (indirect tailcall targets, cmpxchg).
* **`v13` (RegFA):** FP/SIMD scratch (stack-passed float arguments).

Only `fp` and `lr` are saved during execution.

---

## 4. Fixup Vocabularies

The `isa/aarch64/encoder` strictly outputs instructions and uses `FixupKind` for instruction-word bit-field patches. Because global pointers require full 64-bit data word relocations, this package defines its own `FixupKind` (a superset including `FixupAbs64`). It safely translates encoder vocabularies via an explicit switch (`fromEncoderKind`) to catch divergences.

---

## 5. Zero-Extension Invariant

Values of type `iN` occupy frame slots with the upper 64-N bits strictly zeroed. Loads use a zero-extending form, and `maskTo` restores this invariant after any narrowing operation. Signed consumers sign-extend a scratch copy and never write the extended value back into a slot.

---

## 6. Trapping Semantics

A64's `sdiv`/`udiv` instructions do not automatically trap on a zero divisor or `INT_MIN / -1`. To uphold the IR's trap contract, `selDivide` explicitly emits branches to `udf` to check these conditions.

---

## 7. `valist` Layout

The IR's `valist` uses a target-defined layout. This backend uses a single pointer for performance. While this aligns with the stack-varargs convention, it diverges from the base register convention's standard C `va_list`. This is a documented non-conformance, as the IR only requires opcode correctness within the module itself.

---

## 8. Missing Against the Spec (Todos)

The following valid IR constructs are not yet supported and will trigger an error at `Lower` time:

* **Floating Point:** Missing register materialization for literals, float `global` initializers, float `bitcast`, conversions (float to/from int), comparisons, and various math functions (`sqrt`, `fma`, `copysign`, `floor`, `ceil`, `trunc`, `nearest`, `min`, `max`).
* **Vectors:** Missing SIMD/FP argument-register classes, vector value types, vector operations (`splat`, `extract`, `insert`, `shuffle`, `gather`, `scatter`, reductions), masked loads/stores, vector comparisons, and `popcnt` (no SIMD path implemented).
* **Integers:** `i128` is unsupported because it requires a register pair throughout selection.
* **ABI / Calling Convention:** `byval[S]` is unimplemented for all sizes. `tailcall` with stack-passed outgoing arguments is rejected to prevent clobbering incoming parameters during frame reuse.
* **Memory / Globals:** Reading `tls global` values, static `alloca` alignments over 16 bytes, and frames exceeding the scaled-imm12 reach (4095 * 8 bytes) are currently rejected.