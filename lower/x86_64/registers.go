package x86_64

import (
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	isax86_64 "github.com/vertex-language/vvm/isa/x86_64"
)

// physToReg maps the vir register table's PhysicalSlot spelling to
// isa/x86_64's Reg values. Every value here is isax86_64.RAX etc. directly
// — this map exists to bridge vir's string-keyed table to isa/x86_64's
// typed constants, not to re-declare them.
var physToReg = map[string]isax86_64.Reg{
	"RAX": isax86_64.RAX, "RBX": isax86_64.RBX, "RCX": isax86_64.RCX, "RDX": isax86_64.RDX,
	"RSI": isax86_64.RSI, "RDI": isax86_64.RDI, "RBP": isax86_64.RBP, "RSP": isax86_64.RSP,
	"R8": isax86_64.R8, "R9": isax86_64.R9, "R10": isax86_64.R10, "R11": isax86_64.R11,
	"R12": isax86_64.R12, "R13": isax86_64.R13, "R14": isax86_64.R14, "R15": isax86_64.R15,
}

// Register resolves a dialect-spelled register name (an AT&T '%' prefix, if
// still present, is stripped defensively) against vir's own register table
// for arch, returning this backend's physical register and its bit width.
//
// NOTE: vir.X86RegisterTable currently only carries the 64-bit and 32-bit
// spellings (rax/eax, ...) — no 8/16-bit sub-registers (al/ax) — so those
// are rejected here (ok == false) until that table grows. TODO.
func Register(arch, name string) (isax86_64.Reg, int, bool) {
	name = strings.TrimPrefix(name, "%")
	tbl := vir.RegisterTableForArchitecture(arch)
	if tbl == nil {
		return isax86_64.RNone, 0, false
	}
	row, ok := tbl[name]
	if !ok || row.Class != vir.RegisterClassGeneralPurpose {
		return isax86_64.RNone, 0, false
	}
	r, ok := physToReg[row.PhysicalSlot]
	if !ok {
		return isax86_64.RNone, 0, false
	}
	return r, row.WidthBits, true
}

func widthBytes(bits int) int {
	if bits == 64 {
		return 8
	}
	return 4 // only 32/64-bit rows exist today (see NOTE above)
}