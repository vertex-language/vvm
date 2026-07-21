package x86

import "github.com/vertex-language/vvm/ir/vir"

const SavedRegBytes = 12

type Frame struct {
	off   map[string]int32
	Local int32
}

func (fr *Frame) Offset(name string) (int32, bool) {
	off, ok := fr.off[name]
	return off, ok
}

func BuildFrame(f *vir.Function, insts []Inst) *Frame {
	fr := &Frame{off: map[string]int32{}}
	for i, p := range f.Params {
		fr.off[p.Name] = int32(8 + 4*i)
	}
	n := int32(0)
	assign := func(o Opr) {
		if o.Kind == OSlot {
			if _, ok := fr.off[o.Slot]; !ok {
				fr.off[o.Slot] = -(SavedRegBytes + 4 + 4*n)
				n++
			}
		}
	}
	for _, in := range insts {
		assign(in.D)
		assign(in.S)
	}
	fr.Local = 4 * n
	return fr
}