package aarch64

import (
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// physicalSlot maps a vir AArch64RegisterTable PhysicalSlot ("X0".."X30",
// "SP") to the encoder.Reg encoding it names.
var physicalSlot = map[string]encoder.Reg{"SP": encoder.SP}

func init() {
	for i := 0; i <= 28; i++ {
		physicalSlot["X"+strconv.Itoa(i)] = encoder.Reg(i)
	}
	physicalSlot["X29"] = encoder.FP
	physicalSlot["X30"] = encoder.LR
}

// resolveReg looks up a vir register name (as written in an asm binding
// or operand, e.g. "x0" or "w0") against the module's register table and
// returns its encoder.Reg encoding plus its bound width in bits.
func resolveReg(name string, table map[string]vir.RegisterInfo) (encoder.Reg, int, bool) {
	info, ok := table[strings.ToLower(name)]
	if !ok {
		return 0, 0, false
	}
	r, ok := physicalSlot[info.PhysicalSlot]
	if !ok {
		return 0, 0, false
	}
	return r, info.WidthBits, true
}