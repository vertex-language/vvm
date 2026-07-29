# gpu/lower/amdtx

The `amdtx` package lowers a verified `.gvir` device module (`ir/gvir.Module`) into a structured AMDTX IR module (`gpu/ir/amdtx.Module`). Because `.gvir` provides no JIT fallback for `amdgcn`, the compiler generates one distinct artifact per declared architecture.

The resulting output is intermediate representation (IR), not text. It can be verified and printed using `gpu/ir/amdtx/encoding/text`.

## Usage & Options

The backend exposes primary entry points to handle architecture-specific lowering:

* **`Lower`**: Lowers the module for a single specified architecture using default options.
* **`LowerAll`**: Iterates through the module's target backend and returns a result for every declared architecture in declaration order.
* **`LowerOptions`**: Allows passing an `Options` struct to control artifact generation. Currently, setting `Debug: true` emits the `.file` table and `.loc` directives for debugging.

```go
import lower "github.com/vertex-language/vvm/gpu/lower/amdtx"

// Example: Lowering a verified module for a specific target
res, err := lower.Lower(m, "gfx942")
if err != nil {
    log.Fatal(err)
}

// Log any kernels dropped due to capability gating
for _, x := range res.Excluded {
    log.Printf("kernel %s excluded: %s unavailable on %s", x.Kernel, x.Feature, res.Arch)
}

// Print the verified IR
src, err := text.Print(res.Module)
```

## Core Design Principles

### 1. Register & Value Model

* **Everything is a VGPR**: To allow downstream passes to handle scalarization and EXEC-mask expansion, all values are treated as virtual vector registers.
* **Lane Vectors**: The compiler does not use vector registers; instead, it uses lane vectors where a `vec[T,N]` value occupies `N` distinct registers.
* **Zero-Extension Invariant**: Sub-dword values (`i8`, `i16`) are stored zero-extended in 32-bit registers, ensuring that wrapping operations natively respect modulo constraints.

### 2. Pointers & Memory

* **No Generic Space**: Every pointer explicitly carries its address space (`global`, `constant`, `group`, or `private`), mapping directly to specific AMDTX load/store mnemonics.
* **Address Registers**: Every pointer value acts as a `.vgpr.b64` register. For shared and private memory, the backend simply addresses the low dword slice.
* **Kernel Arguments**: Kernel parameters are accessed via `%kernarg_ptr` and loaded into VGPRs using `s_load` during the prologue phase.

### 3. Callable Entities & Control Flow

* **Kernels**: The compiler translates packed kernarg layouts into `.param` declarations. If a kernel requests a specific `subgroup_size`, it dictates the `.wave` width (32 or 64) for the entire module; conflicting wave requests within the same module will result in a lowering error.
* **Functions**: Since AMDTX 1.0 does not define a standard call ABI, functions that return a value are injected with a synthetic trailing formal parameter to carry the result.
* **Control Flow**: The backend enforces structured control flow. Annotated `.gvir` control flow graphs are rebuilt into structured regions (`if`, `loop`, `breakif`), and common patterns like early returns inside divergent regions are automatically refactored.

## Architecture Breakdown

* **Initialization & Options (`amdtx.go`)**: Serves as the entry point, manages module-level states, handles target options, and enforces a single `.wave` width per module.
* **Functions & Kernels (`callable.go`)**: Manages kernel definitions, parses kernarg layouts, executes `s_load` prologues, tracks segment offsets, and handles synthetic function returns.
* **Capability Gating (`gating.go`)**: Evaluates feature usage across the call graph, excluding kernels that use unsupported features on the target architecture.
* **Control Flow & Guarding (`cfg.go`, `uniform.go`)**: Rebuilds the CFG into a structured region tree and dictates guard forms (e.g., using `%scc` for uniform conditions to keep `s_barrier` legal).
* **Instruction Selection (`isel*.go`)**: Maps arithmetic, memory operations, fences, and subgroup builtins to their correct AMDTX equivalents.

## Current Limitations (Todos)

The following valid `.gvir` instructions fall back to generating errors at `Lower` time because they are not yet fully supported:

* **Scalarization**: Uniform values currently live in VGPRs, which is costly.
* **Math Sequences**: Integer division (`udiv`, `sdiv`, `urem`, `srem`) and IEEE-compliant float sequences (`div`, `sqrt`, `tanh`) are not yet emitted.
* **64-Bit Operations**: 64-bit integer multiplies, byte swaps, and collectives require unwritten split sequences.
* **Memory Loops**: `memcopy`, `memmove`, and `memset` thread loops are currently unimplemented.