package arm

import "github.com/vertex-language/vvm/ir/vir"

// Frame layout (grows down):
//
//	[fp+8+4k]   incoming stack arguments (5th argument onward)
//	[fp+4]      saved LR
//	[fp]        saved FP (r11)
//	[fp-4-4k]   one 4-byte home slot per vir value
//
// Prologue: push {fp, lr}; mov fp, sp; sub sp, #local. The first four
// parameters arrive in r0-r3 and are spilled into ordinary home slots by
// isel-emitted stores at function entry; parameters 5+ are addressed
// directly at their positive FP offsets. local is rounded to 8 so SP keeps
// AAPCS 8-byte alignment; everything is FP-relative and the epilogue
// restores SP from FP, which is what makes per-iteration alloca (§4) safe.
type frame struct {
	off   map[string]int32 // value name -> FP-relative offset
	local int32            // bytes to subtract in the prologue
}

func buildFrame(f *vir.Func, insts []minst) *frame {
	fr := &frame{off: map[string]int32{}}
	n := int32(0)
	alloc := func(name string) {
		if _, ok := fr.off[name]; !ok {
			n++
			fr.off[name] = -4 * n
		}
	}
	for i, p := range f.Params {
		if i < 4 {
			alloc(p.Name) // spilled from r0-r3 at entry
		} else {
			fr.off[p.Name] = int32(8 + 4*(i-4))
		}
	}
	for _, in := range insts {
		for _, o := range []opr{in.d, in.s, in.t, in.x} {
			if o.k == oSlot {
				alloc(o.slot)
			}
		}
	}
	fr.local = (4*n + 7) &^ 7 // AAPCS: SP 8-aligned at call boundaries
	return fr
}