package aarch64

import "github.com/vertex-language/vvm/ir/vir"

// Frame layout (grows down):
//
//	[fp+16+8k]  incoming stack arguments (9th argument onward)
//	[fp+8]      saved LR (x30)
//	[fp]        saved FP (x29)
//	[fp-8-8k]   one 8-byte home slot per vir value
//
// Every parameter, and every value first produced by an asm out-binding,
// gets its own home slot — the zero-extended-slot invariant holds
// uniformly (unlike lower/arm).
type Frame struct {
	Off   map[string]int32
	Local int32
}

// BuildFrame scans every OSlot operand a lowered function body references
// (ordinary instructions and inline-asm in/out bindings alike — both
// produce OSlot operands the same way) and assigns each a home offset.
func BuildFrame(f *vir.Function, insts []Inst) *Frame {
	fr := &Frame{Off: map[string]int32{}}
	n := int32(0)
	alloc := func(name string) {
		if _, ok := fr.Off[name]; !ok {
			n++
			fr.Off[name] = -8 * n
		}
	}
	for _, p := range f.Params {
		alloc(p.Name)
	}
	for _, in := range insts {
		for _, o := range []Opr{in.D, in.S, in.T, in.X} {
			if o.Kind == OSlot {
				alloc(o.Slot)
			}
		}
	}
	fr.Local = (8*n + 15) &^ 15 // AAPCS64: SP 16-aligned at call boundaries
	return fr
}