# ptx

The `ptx` package provides a structured, in-memory Intermediate Representation (IR) for NVIDIA PTX (Parallel Thread Execution) modules.

It models `.ptx` translation units explicitly — module directives, module-scoped variables, kernels (`.entry`), device functions (`.func`), and instruction bodies. The IR focuses purely on structure; all text formatting and generation logic is delegated to the `ptx/encoding/text` package.

## Design Principles

- **Grammar-driven.** Every exported symbol in the API corresponds directly to a construct in the PTX grammar. There are no convenience types drawn from Go's type system.
- **Constant enums.** Types, spaces, opcodes, and special registers are constant enums backed by internal tables, preventing invalid string manipulation.
- **Instructions are values.** An `Instr` is a struct holding an opcode, typed qualifiers, positional types, and operands. Canonical qualifier ordering is handled internally by the opcode table, so equivalent IR always prints byte-identically regardless of the order qualifiers are supplied.
- **Editable bodies.** An instruction `Body` supports standard array mutations — `.Append()`, `.InsertBefore()`, `.Replace()`, `.Remove()` — making analysis and rewrite passes straightforward in Go.
- **Explicit flow control.** Predication is applied directly to the returned instruction via `.If(p)` or `.IfNot(p)`. This ensures guards are attached to the exact instruction and cannot accidentally leak across labels.
- **No implicit inference.** The package does not type-check operand compatibility or infer rounding modes. `ptx.Verify` handles structural and version-gating validation, but `ptxas` remains the definitive verifier of record.

## Quick Start

The example below builds a module, declares variables, allocates registers, emits predicated instructions, and renders the final assembly text.

```go
package main

import (
	"log"

	"github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/gpu/ir/ptx/encoding/text"
)

func main() {
	// A module requires an ISA version, target architecture, and address size.
	m := ptx.NewModule(ptx.ISA93, ptx.SM90, ptx.Addr64)

	// NewKernel creates a .entry kernel with an empty instruction body.
	k := ptx.NewKernel("vector_add")
	k.Linkage = ptx.Visible

	// Kernel parameters.
	pA := k.Param("A", ptx.U64)
	pB := k.Param("B", ptx.U64)
	pC := k.Param("C", ptx.U64)
	pN := k.Param("n", ptx.U32)

	// Body and register file.
	b := k.Body
	r := b.Regs

	// Virtual registers mapped to PTX types.
	i, n, p := r.New(ptx.U32), r.New(ptx.U32), r.New(ptx.Pred)
	a, bb, c := r.New(ptx.U64), r.New(ptx.U64), r.New(ptx.U64)
	off := r.New(ptx.U64)
	va, vb := r.New(ptx.F32), r.New(ptx.F32)

	// Label for control flow.
	done := b.Label("done")

	// Thread ID: i = (blockIdx.x * blockDim.x) + threadIdx.x
	b.MovSReg(i, ptx.CtaIdX)
	b.Mad(ptx.U32, i, i, ptx.NTidX, ptx.TidX, ptx.MulLo)

	// Load array size and check bounds.
	b.Ld(ptx.U32, n, ptx.At(pN), ptx.ParamSpace)
	b.Setp(ptx.U32, ptx.Ge, p, i, n) // p = (i >= n)

	// Branch to 'done' if p is true. .If(p) guards the branch itself.
	b.Bra(done).If(p)

	// Byte offset: i * 4 (F32 element size)
	b.Mul(ptx.U32, off, i, ptx.Imm(4), ptx.MulWide)

	// Load base addresses from parameters.
	b.Ld(ptx.U64, a, ptx.At(pA), ptx.ParamSpace)
	b.Ld(ptx.U64, bb, ptx.At(pB), ptx.ParamSpace)
	b.Ld(ptx.U64, c, ptx.At(pC), ptx.ParamSpace)

	// Apply the byte offset to each base pointer.
	b.Add(ptx.U64, a, a, off)
	b.Add(ptx.U64, bb, bb, off)
	b.Add(ptx.U64, c, c, off)

	// Load, add, and store.
	b.Ld(ptx.F32, va, ptx.At(a), ptx.Global)
	b.Ld(ptx.F32, vb, ptx.At(bb), ptx.Global)
	b.Add(ptx.F32, va, va, vb)
	b.St(ptx.F32, ptx.At(c), va, ptx.Global)

	// Bind 'done' and return.
	b.Bind(done)
	b.Ret()

	m.Add(k)

	// Verify checks structural issues and version/target gating requirements.
	for _, diag := range ptx.Verify(m) {
		log.Println(diag)
	}

	// Render the module to PTX assembly text.
	src, err := text.Print(m)
	if err != nil {
		log.Fatal(err)
	}

	log.Println(src)
}
```

## Advanced Usage

### The `Emit` Escape Hatch

PTX introduces new instructions frequently. For instructions not yet strictly typed in the package (e.g. `mma`, `wgmma`, `cp.async`, `tensormap`), use the generic `Body.Emit` method:

```go
// Emitted instructions participate in predication, walking, and printing
// exactly like natively modelled instructions.
b.Emit("cp.async.ca.shared.global", []ptx.Operand{dst, src}, ptx.Imm(16)).If(p)
```

Instructions created via `Emit` use the generic canonical qualifier order and remain fully compliant with the package's formatting logic.

### Exact-Bits Floating Point

Float immediates are exact-bits only, to prevent invalid PTX rendering. `F32Imm` and `F64Imm` guarantee that values print in the `0f...` or `0d...` hex format, so special values like NaN and infinity convert to PTX text safely and losslessly.