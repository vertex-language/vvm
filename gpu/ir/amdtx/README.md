# amdtx

The `amdtx` package provides a structured, in-memory Intermediate Representation (IR) for AMDTX modules — a virtual, target-shaped IR for AMD GPU compute kernels.

It models `.amdtx` translation units explicitly: the preamble (`.amdtx`, `.target`, `.wave`), the file table, module-scope `.global`/`.shared` objects, and `.kernel`/`.func` bodies. Text formatting lives in `amdtx/encoding/text`.

## Design Principles

- **Grammar-driven.** Every exported symbol corresponds to a construct in the AMDTX grammar (§3.3).
- **Widths, not types.** Registers carry `.bN`; interpretation lives in the mnemonic. `v_add_f32` and `v_add_u32` both take `.vgpr.b32`.
- **Width stated once.** `SLoad`, `GlobalLoad`, `DSStore` and friends derive their `_bN` suffix from the data register, so **V9** holds by construction. Mnemonics use GFX11-style naming on all targets (**P4**); lowering rewrites to GFX9 spelling.
- **Structured control flow is an item.** `If`, `Loop`, `BreakIf` and `ContinueIf` are IR nodes. EXEC-mask expansion belongs to lowering (**P2**), and `%exec` writes are rejected outside `raw` (**V15**).
- **Explicit synchronisation.** Adjacency conveys nothing (**P6**). Waits and fences are instructions you emit.
- **Typed escape hatches.** `Raw` and `RawBytes` pass through untouched but must declare defs, uses and clobbers (**P8**, **V39**).
- **Verify accepts or rejects; it never rewrites** (**P7**). Diagnostics carry the rule number.

## Quick Start

```go
package main

import (
	"log"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/text"
)

func main() {
	m := amdtx.NewModule(amdtx.V10, amdtx.GFX942, amdtx.Wave64)
	src := m.File("saxpy.hip")

	k := amdtx.NewKernel("saxpy")
	k.Linkage = amdtx.Visible
	k.ParamPtr("x", amdtx.Global, amdtx.ReadOnly)
	k.ParamPtr("y", amdtx.Global, amdtx.ReadWrite)
	k.Param("n", amdtx.B32)
	k.Param("alpha_bits", amdtx.B32)

	k.Launch.KernargSize = 24
	k.Launch.KernargAlign = 8
	k.Launch.MaxFlatWorkgroupSize = 256
	k.Launch.WavesPerEU = [2]int{1, 8}

	b := k.Body
	r := b.Regs

	karg := r.New(amdtx.Sgpr(amdtx.B128), "karg") // x, y
	scal := r.New(amdtx.Sgpr(amdtx.B64), "scal")  // n, alpha_bits
	s := r.Block(amdtx.Sgpr(amdtx.B32), "s", 8)
	v := r.Block(amdtx.Vgpr(amdtx.B32), "v", 8)
	vd := r.Block(amdtx.Vgpr(amdtx.B64), "vd", 2)
	p := r.Block(amdtx.Lane, "p", 2)

	b.Loc(src, 12, 3)
	b.SLoad(karg, amdtx.At(amdtx.KernargPtr))
	b.SLoad(scal, amdtx.At(amdtx.KernargPtr, 16))
	b.Waitcnt(amdtx.LGKM(0))

	b.Loc(src, 13, 3)
	b.Inst("s_mul_i32", s.At(0), amdtx.WgIdX, amdtx.Imm(256))
	b.Inst("v_add_u32", v.At(0), s.At(0), amdtx.TidX)
	b.Inst("v_cmp_gt_i32", p.At(0), scal.Dword(0), v.At(0))

	in := b.If(p.At(0)) // .lanemask guard: divergent by definition
	t := in.Then
	t.Inst("v_lshlrev_b64", vd.At(0), amdtx.Imm(2), v.At(0))
	t.Inst("v_add_co_u32", vd.At(0).Dword(0), karg.Dword(0), vd.At(0).Dword(0))
	t.Inst("v_addc_co_u32", vd.At(0).Dword(1), karg.Dword(1), vd.At(0).Dword(1))
	t.GlobalLoad(v.At(1), amdtx.At(vd.At(0)))
	t.Waitcnt(amdtx.VM(0))
	t.Inst("v_fma_f32", v.At(3), scal.Dword(1), v.At(1), v.At(2))
	t.GlobalStore(amdtx.At(vd.At(1)), v.At(3))

	b.EndPgm()
	m.Add(k)

	for _, d := range amdtx.Verify(m) {
		log.Println(d) // "saxpy[7]: error V9: access width .b64 does not match ..."
	}

	out, err := text.Print(m)
	if err != nil {
		log.Fatal(err)
	}
	log.Println(out)
}
```

## Verification coverage

`Verify` implements V1–V19, V21–V24, V26–V31, V33, V34, V37–V41, W1 and W2.

Deliberately out of scope:

| Rule | Owner | Why |
|---|---|---|
| V20 | lowering | Displacement ranges are per-encoding |
| V25 | lowering | Pinned encodings are checked after instruction selection |
| V32 | printer | `parse(print(m))` is a round-trip conformance test |
| V36 | structural | Hidden arguments are never represented in the IR |