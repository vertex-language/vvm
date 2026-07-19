// Package regalloc is the current baseline register allocator: spill
// everything. It rewrites every mcode.KSlot operand into an RBP-relative
// memory operand, using the frame abi.BuildFrame already assigned. A real
// allocator — linear scan over live ranges, with RBX/R12–R15 joining the
// allocatable set — replaces this function without touching isel's or
// inlineasm's output contract. TODO.
package regalloc

import (
	"fmt"

	"github.com/vertex-language/vvm/lower/x86_64/abi"
	"github.com/vertex-language/vvm/lower/x86_64/mcode"
)

func ResolveSlots(insts []mcode.Inst, fr *abi.Frame) error {
	fix := func(o *mcode.Opr) error {
		if o.K != mcode.KSlot {
			return nil
		}
		d, ok := fr.Off[o.Slot]
		if !ok {
			return fmt.Errorf("regalloc: value %q has no frame slot", o.Slot)
		}
		*o = mcode.Opr{K: mcode.KMem, Base: mcode.RBP, Disp: d}
		return nil
	}
	for i := range insts {
		if err := fix(&insts[i].D); err != nil {
			return err
		}
		if err := fix(&insts[i].S); err != nil {
			return err
		}
	}
	return nil
}