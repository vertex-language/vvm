package aarch64

import "github.com/vertex-language/vvm/ir/vir"

// Frame layout (grows down):
//
//	[fp+16+8k]  incoming stack arguments (9th argument onward)
//	[fp+8]      saved LR (x30)
//	[fp]        saved FP (x29)
//	[fp-8-8k]   one 8-byte home slot per vir value
//
// Prologue: stp x29, x30, [sp, #-16]!; mov x29, sp; sub sp, #local. The
// first eight parameters arrive in x0-x7 and are normalized (AAPCS64
// leaves narrow arguments' high bits unspecified) and spilled into
// ordinary home slots by isel-emitted stores at function entry; parameters
// 9+ are copied from their positive FP offsets into home slots at entry
// too — unlike lower/arm, every parameter has a home slot, so the
// zero-extended-slot invariant holds uniformly. local is rounded to 16 so
// SP keeps AAPCS64 16-byte alignment; everything is FP-relative and the
// epilogue restores SP from FP, which is what makes per-iteration alloca
// (§4) safe.
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
			fr.off[name] = -8 * n
		}
	}
	for _, p := range f.Params {
		alloc(p.Name) // all params spilled/copied to home slots at entry
	}
	for _, in := range insts {
		for _, o := range []opr{in.d, in.s, in.t, in.x} {
			if o.k == oSlot {
				alloc(o.slot)
			}
		}
	}
	fr.local = (8*n + 15) &^ 15 // AAPCS64: SP 16-aligned at call boundaries
	return fr
}