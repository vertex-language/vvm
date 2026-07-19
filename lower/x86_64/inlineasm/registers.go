// Package inlineasm lowers verified vir inline-asm blocks (Intel/AT&T) into
// the same mcode.Inst stream isel targets — exactly one encoder for both.
package inlineasm

import (
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/lower/x86_64/mcode"
)

// physToReg maps the vir register table's PhysicalSlot spelling to this
// backend's mcode.Reg.
var physToReg = map[string]mcode.Reg{
	"RAX": mcode.RAX, "RBX": mcode.RBX, "RCX": mcode.RCX, "RDX": mcode.RDX,
	"RSI": mcode.RSI, "RDI": mcode.RDI, "RBP": mcode.RBP, "RSP": mcode.RSP,
	"R8": mcode.R8, "R9": mcode.R9, "R10": mcode.R10, "R11": mcode.R11,
	"R12": mcode.R12, "R13": mcode.R13, "R14": mcode.R14, "R15": mcode.R15,
}

// Register resolves a dialect-spelled register name (an AT&T '%' prefix, if
// still present, is stripped defensively) against vir's own register table
// for arch, returning this backend's physical register and its bit width.
//
// NOTE: vir.X86RegisterTable currently only carries the 64-bit and 32-bit
// spellings (rax/eax, ...) — no 8/16-bit sub-registers (al/ax) — so those
// are rejected here (ok == false) until that table grows. TODO.
func Register(arch, name string) (mcode.Reg, int, bool) {
	name = strings.TrimPrefix(name, "%")
	tbl := vir.RegisterTableForArchitecture(arch)
	if tbl == nil {
		return mcode.RNone, 0, false
	}
	row, ok := tbl[name]
	if !ok || row.Class != vir.RegisterClassGeneralPurpose {
		return mcode.RNone, 0, false
	}
	r, ok := physToReg[row.PhysicalSlot]
	if !ok {
		return mcode.RNone, 0, false
	}
	return r, row.WidthBits, true
}

func widthBytes(bits int) int {
	if bits == 64 {
		return 8
	}
	return 4 // only 32/64-bit rows exist today (see NOTE above)
}