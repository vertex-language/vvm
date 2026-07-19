package inlineasm

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/lower/x86/mcode"
	"github.com/vertex-language/vvm/ir/vir"
)

// physicalSlot maps a vir.RegisterInfo's canonical physical-slot name (§4
// register table shape) onto this 32-bit backend's Reg. Registers whose
// slot has no 32-bit-mode encoding (r8..r15) are simply absent.
var physicalSlot = map[string]mcode.Reg{
	"RAX": mcode.REAX, "RCX": mcode.RECX, "RDX": mcode.REDX, "RBX": mcode.REBX,
	"RSP": mcode.RESP, "RBP": mcode.REBP, "RSI": mcode.RESI, "RDI": mcode.REDI,
}

// resolveRegister looks a register token up in vir's own x86 register
// table — the same table the verifier already checked it against (§9.35) —
// and maps it onto this backend's physical Reg. It defensively strips a
// leading '%' in case the AT&T sigil survived into the IR.
func resolveRegister(name string) (r mcode.Reg, widthBits int, err error) {
	name = strings.TrimPrefix(name, "%")
	info, ok := vir.RegisterTableForArchitecture("x86")[name]
	if !ok {
		return mcode.RNone, 0, fmt.Errorf("asm: register %q is not in the x86 register table (§9.35)", name)
	}
	pr, ok := physicalSlot[info.PhysicalSlot]
	if !ok {
		return mcode.RNone, 0, fmt.Errorf("asm: register %q (physical %s) has no 32-bit encoding; this backend only lowers arch \"x86\"", name, info.PhysicalSlot)
	}
	return pr, info.WidthBits, nil
}