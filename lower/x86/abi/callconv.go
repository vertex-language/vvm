package abi

import "github.com/vertex-language/vvm/ir/vir"

// ArgSlot is one outgoing argument's placement within the cdecl argument
// area built by a call site: Offset is its byte offset from the lowest
// address (first argument), and ByVal names the struct being copied when
// the argument is passed byval (§7.2); "" for ordinary scalar arguments.
type ArgSlot struct {
	Offset int
	ByVal  string
}

// PlanCall lays out the argument area for a direct or indirect call: every
// scalar argument takes one 4-byte slot, byval structs take their aligned
// size, and the first argument sits at the lowest address (cdecl, §7.2).
// structSize resolves a byval struct's total (already-aligned) size.
func PlanCall(params []vir.Param, argCount int, structSize func(name string) (int, error)) ([]ArgSlot, int, error) {
	slots := make([]ArgSlot, argCount)
	total := 0
	for i := 0; i < argCount; i++ {
		byval := ""
		if i < len(params) {
			byval = params[i].ByVal
		}
		slots[i] = ArgSlot{Offset: total, ByVal: byval}
		if byval != "" {
			sz, err := structSize(byval)
			if err != nil {
				return nil, 0, err
			}
			total += roundUp(sz, 4)
		} else {
			total += 4
		}
	}
	return slots, total, nil
}