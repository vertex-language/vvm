package x86

import "github.com/vertex-language/vvm/ir/vir"

type ArgSlot struct {
	Offset int
	ByVal  string
}

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