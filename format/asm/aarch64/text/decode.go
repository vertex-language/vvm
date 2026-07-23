// decode.go
package text

import (
	"fmt"
	"strings"

	isaa64 "github.com/vertex-language/vvm/isa/aarch64"
	lowera64 "github.com/vertex-language/vvm/lower/aarch64"
)

// decodeInst decodes one instruction word at byte offset off. fx, if
// non-nil, is the fixup recorded at off: a pending relocation's field is
// still zero/placeholder in Code (a downstream object writer fills it in
// later), so where a fixup applies, decodeInst prints the symbol it names
// instead of decoding the placeholder bits. usedFixup reports whether it
// did, so the caller doesn't also print a redundant reloc comment.
//
// Because this is a debug listing scoped to the subset `lower/aarch64`
// emits, it structurally decodes the common control-flow and address 
// computations and allows unrecognized instructions (like data processing) 
// to gracefully degrade to `.word` hex encodings.
func decodeInst(off, w uint32, fx *lowera64.Fixup) (text string, usedFixup bool) {
	switch {
	case w == 0x00000000:
		return "udf", false

	case w == 0xD503201F:
		return "nop", false

	case w&0x7C000000 == 0x14000000: // b/bl
		return decodeBranch(off, w, fx)

	case w&0xFF000010 == 0x54000000: // b.cond
		return decodeBranchCond(off, w, fx)

	case w&0x7E000000 == 0x34000000: // cbz/cbnz
		return decodeCompareBranch(off, w, fx)

	case w&0x7E000000 == 0x36000000: // tbz/tbnz
		return decodeTestBranch(off, w, fx)

	case w&0xFFFF0000 == 0xD61F0000, w&0xFFFF0000 == 0xD63F0000, w&0xFFFF0000 == 0xD65F0000: // br/blr/ret
		return decodeBranchReg(w)

	case w&0x7F800000 == 0x12800000: // movz/movn/movk
		return decodeMovWide(w, fx)

	case w&0x9F000000 == 0x10000000: // adr/adrp
		return decodeAdr(off, w, fx)

	case w&0xFFE0001F == 0xD4000000: // svc
		return line("svc", fmt.Sprintf("#%d", (w>>5)&0xFFFF)), false

	case w&0xFFE0001F == 0xD4200000: // brk
		return line("brk", fmt.Sprintf("#%d", (w>>5)&0xFFFF)), false
	}

	return "", false // unrecognized: caller prints a .word line
}

func decodeBranch(off, w uint32, fx *lowera64.Fixup) (string, bool) {
	mnem := "b"
	if w&0x80000000 != 0 {
		mnem = "bl"
	}
	if fx != nil {
		return line(mnem, fx.Symbol), true
	}
	wordOff := isaa64.DecodeBranchImm26(w)
	// No A32 PC skew (+8) here: A64 branch targets are relative to the instruction itself.
	target := int64(off) + wordOff*4
	return line(mnem, fmt.Sprintf("0x%x", target)), false
}

func decodeBranchCond(off, w uint32, fx *lowera64.Fixup) (string, bool) {
	cc := byte(w & 0xF)
	mnem := "b." + isaa64.CondName(cc)
	if fx != nil {
		return line(mnem, fx.Symbol), true
	}
	wordOff := isaa64.DecodeBranchImm19(w)
	target := int64(off) + wordOff*4
	return line(mnem, fmt.Sprintf("0x%x", target)), false
}

func decodeCompareBranch(off, w uint32, fx *lowera64.Fixup) (string, bool) {
	mnem := "cbz"
	if w&0x01000000 != 0 {
		mnem = "cbnz"
	}
	sf := w>>31 != 0
	rt := regName(byte(w&0x1F), sf, false)
	if fx != nil {
		return line(mnem, rt, fx.Symbol), true
	}
	wordOff := isaa64.DecodeBranchImm19(w)
	target := int64(off) + wordOff*4
	return line(mnem, rt, fmt.Sprintf("0x%x", target)), false
}

func decodeTestBranch(off, w uint32, fx *lowera64.Fixup) (string, bool) {
	mnem := "tbz"
	if w&0x01000000 != 0 {
		mnem = "tbnz"
	}
	b5 := (w >> 31) & 1
	b40 := (w >> 19) & 0x1F
	bitNum := (b5 << 5) | b40
	rt := regName(byte(w&0x1F), b5 != 0, false)
	if fx != nil {
		return line(mnem, rt, fmt.Sprintf("#%d", bitNum), fx.Symbol), true
	}
	wordOff := isaa64.DecodeBranchImm14(w)
	target := int64(off) + wordOff*4
	return line(mnem, rt, fmt.Sprintf("#%d", bitNum), fmt.Sprintf("0x%x", target)), false
}

func decodeBranchReg(w uint32) (string, bool) {
	rn := regName(byte((w>>5)&0x1F), true, false)
	switch w & 0xFFFF0000 {
	case 0xD61F0000:
		return line("br", rn), false
	case 0xD63F0000:
		return line("blr", rn), false
	case 0xD65F0000:
		// Automatically alias x30/lr to bare `ret` mapping back to canonical conventions
		if rn == "x30" || rn == "lr" {
			return "ret", false
		}
		return line("ret", rn), false
	}
	return "", false
}

func decodeMovWide(w uint32, fx *lowera64.Fixup) (string, bool) {
	opc := (w >> 29) & 3
	sf := w>>31 != 0
	mnem := "?"
	switch opc {
	case 0:
		mnem = "movn"
	case 2:
		mnem = "movz"
	case 3:
		mnem = "movk"
	}
	rd := regName(byte(w&0x1F), sf, false)
	hw := (w >> 21) & 3
	imm16 := (w >> 5) & 0xFFFF

	if fx != nil {
		return line(mnem, rd, fx.Symbol), true
	}

	shift := hw * 16
	if shift == 0 {
		return line(mnem, rd, fmt.Sprintf("#%d", imm16)), false
	}
	return line(mnem, rd, fmt.Sprintf("#%d", imm16), fmt.Sprintf("lsl #%d", shift)), false
}

func decodeAdr(off, w uint32, fx *lowera64.Fixup) (string, bool) {
	mnem := "adr"
	isAdrp := w&0x80000000 != 0
	if isAdrp {
		mnem = "adrp"
	}
	rd := regName(byte(w&0x1F), true, false)
	if fx != nil {
		return line(mnem, rd, fx.Symbol), true
	}

	immlo := (w >> 29) & 3
	immhi := (w >> 5) & 0x7FFFF
	imm := (immhi << 2) | immlo
	simm := int64(imm)
	
	// Manual sign extension to 21 bits
	if simm&(1<<20) != 0 {
		simm |= ^((1 << 21) - 1)
	}

	if isAdrp {
		// ADRP computes page boundaries: clear bottom 12 bits of PC, then shift immediate up 12
		target := (int64(off) & ^int64(0xFFF)) + (simm << 12)
		return line(mnem, rd, fmt.Sprintf("0x%x", target)), false
	}
	target := int64(off) + simm
	return line(mnem, rd, fmt.Sprintf("0x%x", target)), false
}

func regName(r byte, is64 bool, isSP bool) string {
	var w isaa64.Width = isaa64.W32
	if is64 {
		w = isaa64.W64
	}
	return isaa64.Reg(r).Name(w, isSP)
}

func line(mnem string, operands ...string) string {
	if len(operands) == 0 {
		return mnem
	}
	return fmt.Sprintf("%-7s %s", mnem, strings.Join(operands, ", "))
}