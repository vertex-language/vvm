package arm

import "github.com/vertex-language/vvm/ir/vir"

// Frame layout (grows down):
//
//	[fp+8+4k]   incoming stack arguments (5th argument onward)
//	[fp+4]      saved LR
//	[fp]        saved FP (r11)
//	[fp-4-4k]   one 4-byte home slot per vir value
//
// Prologue/epilogue are built by encode.go's assemble, not here. Local is
// rounded to 8 so SP keeps AAPCS 8-byte alignment.
type Frame struct {
	Off   map[string]int32
	Local int32
}

func BuildFrame(f *vir.Function, insts []Inst) *Frame {
	fr := &Frame{Off: map[string]int32{}}
	n := int32(0)
	alloc := func(name string) {
		if _, ok := fr.Off[name]; !ok {
			n++
			fr.Off[name] = -4 * n
		}
	}
	for i, p := range f.Params {
		if i < 4 {
			alloc(p.Name)
		} else {
			fr.Off[p.Name] = int32(8 + 4*(i-4))
		}
	}
	for _, in := range insts {
		for _, o := range []Opr{in.D, in.S, in.T, in.X} {
			if o.Kind == OSlot {
				alloc(o.Slot)
			}
		}
	}
	fr.Local = (4*n + 7) &^ 7
	return fr
}