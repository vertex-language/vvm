package x86_64

import "fmt"

// resolveSlots is the register allocator, in its current spill-everything
// form: every vir value already has a dedicated 8-byte stack slot
// (buildFrame), and isel materializes operands through caller-saved scratch
// registers, so all this pass does is rewrite oSlot operands into
// RBP-relative memory operands.
//
// This is deliberately the correct-first baseline (README: same TODO culture
// as lower/x86). A real allocator — linear scan over live ranges, with
// RBX/R12–R15 joining the allocatable set and the slot becoming the spill
// home rather than the only home — replaces this function without touching
// isel's output contract. TODO.
func resolveSlots(insts []minst, fr *frame) error {
	fix := func(o *opr) error {
		if o.k != oSlot {
			return nil
		}
		d, ok := fr.off[o.slot]
		if !ok {
			return fmt.Errorf("regalloc: value %q has no frame slot", o.slot)
		}
		*o = opr{k: oMem, base: rRBP, disp: d}
		return nil
	}
	for i := range insts {
		if err := fix(&insts[i].d); err != nil {
			return err
		}
		if err := fix(&insts[i].s); err != nil {
			return err
		}
	}
	return nil
}