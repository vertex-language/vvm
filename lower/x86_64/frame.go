package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// Frame layout (grows down):
//
//	[rbp+16+8k] incoming stack arguments (args 7+; 8-byte slots)
//	[rbp+8]     return address
//	[rbp]       saved RBP
//	[rbp-8-8k]  one 8-byte home slot per vir value
//
// RSP stays 16-byte aligned throughout (§ see callconv.go/isel.go).
type Frame struct {
	Off   map[string]int32
	Local int32
}

func BuildFrame(f *vir.Function, insts []Inst) *Frame {
	fr := &Frame{Off: map[string]int32{}}
	for i, p := range f.Params {
		if i >= len(ArgRegs) {
			fr.Off[p.Name] = int32(16 + 8*(i-len(ArgRegs)))
		}
	}
	n := int32(0)
	for _, in := range insts {
		for _, o := range []Opr{in.D, in.S} {
			if o.K == KSlot {
				if _, ok := fr.Off[o.Slot]; !ok {
					fr.Off[o.Slot] = -(8 + 8*n)
					n++
				}
			}
		}
	}
	fr.Local = (8*n + 15) &^ 15
	return fr
}