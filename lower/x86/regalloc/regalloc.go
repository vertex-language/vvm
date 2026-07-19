// Package regalloc is the x86 register allocator, in its current
// spill-everything form: every vir value already has a dedicated stack slot
// (abi.BuildFrame), and instruction selection materializes operands
// through the EAX/ECX/EDX scratch set, so all this pass does is rewrite
// OSlot operands into EBP-relative memory operands.
//
// This is deliberately the correct-first baseline the pipeline allows us to
// start from. A real allocator (linear scan over live ranges, with EBX/ESI/
// EDI joining the allocatable set and the slot becoming the spill home
// rather than the only home) replaces this package without touching isel's
// or inlineasm's output contract. TODO.
package regalloc

import (
	"fmt"

	"github.com/vertex-language/vvm/lower/x86/abi"
	"github.com/vertex-language/vvm/lower/x86/mcode"
)

func ResolveSlots(insts []mcode.Inst, fr *abi.Frame) error {
	fix := func(o *mcode.Opr) error {
		if o.Kind != mcode.OSlot {
			return nil
		}
		off, ok := fr.Offset(o.Slot)
		if !ok {
			return fmt.Errorf("regalloc: value %q has no frame slot", o.Slot)
		}
		*o = mcode.Mem(mcode.REBP, off)
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