package abi

import "github.com/vertex-language/vvm/lower/x86_64/mcode"

// ArgRegs is the SysV AMD64 integer/pointer argument register order.
var ArgRegs = [6]mcode.Reg{mcode.RDI, mcode.RSI, mcode.RDX, mcode.RCX, mcode.R8, mcode.R9}

// Call is the planned argument-area layout for one call site (§7.2). The
// caller stages register args above stack args in one reserved area so
// that argument evaluation (which may clobber scratch registers) is fully
// separated from the final register loads.
type Call struct {
	NumRegArgs   int
	NumStackArgs int
	StackBytes   int32 // total reserved bytes, kept 16-aligned
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

// StageOffset returns arg i's staging offset relative to the reserved
// area's low address ([rsp+0..]): stack args occupy exactly the slots the
// SysV callee expects; register args are staged just above them.
func (c Call) StageOffset(i int) int32 {
	if i >= c.NumRegArgs {
		return int32(8 * (i - c.NumRegArgs))
	}
	return int32(8*c.NumStackArgs + 8*i)
}