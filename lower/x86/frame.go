package x86

import "github.com/vertex-language/vvm/ir/vir"

// Frame layout (grows down):
//
//	[ebp+8+4i]  incoming cdecl arguments (4-byte slots; callee may write them)
//	[ebp+4]     return address
//	[ebp]       saved EBP
//	[ebp-4..-12] saved EBX, ESI, EDI (always saved; the spiller uses them)
//	[ebp-16-4k] one 4-byte home slot per vir value
//
// ESP may move below the slot area (calls, dynamic alloca); everything is
// EBP-relative, and the epilogue restores ESP from EBP, which is what makes
// per-iteration alloca (§4) safe.
const savedRegBytes = 12

type frame struct {
	off   map[string]int32 // value name -> EBP-relative offset
	local int32            // bytes to subtract in the prologue
}

func buildFrame(f *vir.Func, insts []minst) *frame {
	fr := &frame{off: map[string]int32{}}
	for i, p := range f.Params {
		fr.off[p.Name] = int32(8 + 4*i)
	}
	n := int32(0)
	for _, in := range insts {
		for _, o := range []opr{in.d, in.s} {
			if o.k == oSlot {
				if _, ok := fr.off[o.slot]; !ok {
					fr.off[o.slot] = -(savedRegBytes + 4 + 4*n)
					n++
				}
			}
		}
	}
	fr.local = 4 * n
	return fr
}