package x86_64

import isax86_64 "github.com/vertex-language/vvm/isa/x86_64"

// ArgRegs is the SysV AMD64 integer/pointer argument register order.
var ArgRegs = [6]isax86_64.Reg{
	isax86_64.RDI, isax86_64.RSI, isax86_64.RDX,
	isax86_64.RCX, isax86_64.R8, isax86_64.R9,
}

type Call struct {
	NumRegArgs   int
	NumStackArgs int
	StackBytes   int32
}

func PlanCall(numArgs int) Call {
	nReg := numArgs
	if nReg > len(ArgRegs) {
		nReg = len(ArgRegs)
	}
	nStack := numArgs - nReg
	total := int32((8*nStack + 8*nReg + 15) &^ 15)
	return Call{NumRegArgs: nReg, NumStackArgs: nStack, StackBytes: total}
}

func (c Call) StageOffset(i int) int32 {
	if i >= c.NumRegArgs {
		return int32(8 * (i - c.NumRegArgs))
	}
	return int32(8*c.NumStackArgs + 8*i)
}