package arm

import "fmt"

// resolveSlots is the register allocator, in its current spill-everything
// form: every vir value has a dedicated FP-relative stack slot (buildFrame),
// and isel materializes operands through the r0-r3/r12 scratch set, so all
// this pass does is rewrite oSlot operands into FP-relative memory operands.
//
// Same deliberate correct-first baseline as lower/x86: a linear-scan
// allocator (r4-r10 joining the allocatable set, the slot becoming the
// spill home) replaces this function without touching isel's output
// contract. TODO.
func resolveSlots(insts []minst, fr *frame) error {
	fix := func(o *opr) error {
		if o.k != oSlot {
			return nil
		}
		d, ok := fr.off[o.slot]
		if !ok {
			return fmt.Errorf("regalloc: value %q has no frame slot", o.slot)
		}
		*o = opr{k: oMem, base: rFP, disp: d}
		return nil
	}
	for i := range insts {
		for _, o := range []*opr{&insts[i].d, &insts[i].s, &insts[i].t, &insts[i].x} {
			if err := fix(o); err != nil {
				return err
			}
		}
	}
	return nil
}