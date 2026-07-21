package x86

import "github.com/vertex-language/vvm/ir/vir"

// SavedRegBytes is the size of the always-saved callee-saved block beneath
// the frame pointer (EBX, ESI, EDI — see encode.go's prologue/epilogue).
const SavedRegBytes = 12

// Frame layout (grows down):
//
//	[ebp+8+4i]    incoming cdecl arguments (4-byte slots; callee may write them)
//	[ebp+4]       return address
//	[ebp]         saved EBP
//	[ebp-4..-12]  saved EBX, ESI, EDI (always saved; the spiller uses them)
//	[ebp-16-4k]   one 4-byte home slot per vir value
//
// ESP may move below the slot area (calls, dynamic alloca); everything is
// EBP-relative, and the epilogue restores ESP from EBP, which is what
// makes per-iteration alloca safe.
type Frame struct {
	off   map[string]int32 // value name -> EBP-relative offset
	Local int32            // bytes to subtract in the prologue
}

// Offset returns the EBP-relative offset assigned to a value's home slot.
func (fr *Frame) Offset(name string) (int32, bool) {
	off, ok := fr.off[name]
	return off, ok
}

// BuildFrame assigns every OSlot operand referenced in insts a distinct
// 4-byte home slot, and every function parameter its cdecl incoming offset.
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