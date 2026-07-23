// decode.go
package text

import (
	"fmt"
	"strings"

	isaarm "github.com/vertex-language/vvm/isa/arm"
	lowerarm "github.com/vertex-language/vvm/lower/arm"
)

// decodeInst decodes one instruction word at byte offset off. fx, if
// non-nil, is the fixup recorded at off: a pending relocation's field is
// still zero/placeholder in Code (a downstream object writer fills it in
// later — see arm.go's Fixup doc), so where a fixup applies, decodeInst
// prints the symbol it names instead of decoding the placeholder bits.
// usedFixup reports whether it did, so the caller doesn't also print a
// redundant reloc comment.
//
// The branches below are ordered specific-pattern-first so that a word
// matching more than one mask (data-processing's bit-27:26==00 space
// contains bx/blx/clz/mul/mulLong/halfword/movw/movt as sub-patterns) is
// claimed by its real encoding before the data-processing catch-all at
// the bottom gets a look. See encode.go's `one` switch, which this
// mirrors in reverse.
func decodeInst(off, w uint32, fx *lowerarm.Fixup) (text string, usedFixup bool) {
	cc := byte(w >> 28 & 0xF)

	switch {
	case w&0x0FFFFFFF == 0x07F000F0: // ud
		return line("udf" + condSuffix(cc)), false

	case w&0x0FFFFFF0 == 0x012FFF10: // bx
		return line("bx"+condSuffix(cc), regName(byte(w&0xF))), false

	case w&0x0FFFFFF0 == 0x012FFF30: // blx (register)
		return line("blx"+condSuffix(cc), regName(byte(w&0xF))), false

	case w&0x0FFF0FF0 == 0x016F0F10: // clz
		return line("clz"+condSuffix(cc), regName(byte(w>>12&0xF)), regName(byte(w&0xF))), false

	case w&0x0E000000 == 0x0A000000: // b/bl
		return decodeBranch(off, w, cc, fx)

	case w&0x0F000000 == 0x0F000000: // svc/swi
		return line("svc"+condSuffix(cc), fmt.Sprintf("#%d", w&0xFFFFFF)), false

	case w&0x0FF00000 == 0x03000000: // movw
		return decodeMovwt(w, cc, "movw", fx)

	case w&0x0FF00000 == 0x03400000: // movt
		return decodeMovwt(w, cc, "movt", fx)

	case w&0x0FC000F0 == 0x00000090: // mul/mla
		return decodeMul(w, cc), false

	case w&0x0F8000F0 == 0x00800090: // umull/umlal/smull/smlal
		return decodeMulLong(w, cc), false

	case w&0x0E000090 == 0x00000090 && (w>>5)&3 != 0: // ldrh/strh/ldrsb/ldrsh
		return decodeHalfword(w, cc), false

	case w&0x0C000000 == 0x04000000: // ldr/str/ldrb/strb
		return decodeSingleTransfer(w, cc), false

	case w&0x0E000000 == 0x08000000: // push/pop/ldm*/stm*
		return decodeBlockTransfer(w, cc), false

	case w&0x0C000000 == 0x00000000: // the sixteen data-processing ops
		return decodeDataProc(w, cc), false
	}
	return "", false // unrecognized: caller prints a .word line
}

func decodeBranch(off, w uint32, cc byte, fx *lowerarm.Fixup) (string, bool) {
	mnem := "b"
	if w&0x01000000 != 0 {
		mnem = "bl"
	}
	mnem += condSuffix(cc)
	if fx != nil {
		return line(mnem, fx.Symbol), true
	}
	// Local labels are fully resolved by encoder.Encode before Code is
	// returned (only a symbol target leaves a fixup), so the field here
	// is a real word offset and the target is a real address in this
	// listing.
	wordOff := isaarm.DecodeBranchImm24(w)
	target := int64(off) + 8 + int64(wordOff)*4
	return line(mnem, fmt.Sprintf("0x%x", target)), false
}

func decodeMovwt(w uint32, cc byte, mnem string, fx *lowerarm.Fixup) (string, bool) {
	rd := regName(byte(w >> 12 & 0xF))
	full := mnem + condSuffix(cc)
	if fx != nil {
		tag := "lo16"
		if mnem == "movt" {
			tag = "hi16"
		}
		return line(full, rd, fmt.Sprintf("#:%s:%s", tag, fx.Symbol)), true
	}
	imm16 := w>>16&0xF<<12 | w&0xFFF
	return line(full, rd, fmt.Sprintf("#%d", imm16)), false
}

func decodeMul(w uint32, cc byte) string {
	ssuf := ""
	if w>>20&1 != 0 {
		ssuf = "s"
	}
	rd, rn := regName(byte(w>>16&0xF)), regName(byte(w>>12&0xF))
	rs, rm := regName(byte(w>>8&0xF)), regName(byte(w&0xF))
	if w>>21&1 != 0 { // mla: Rd := Rm*Rs+Rn
		return line("mla"+condSuffix(cc)+ssuf, rd, rm, rs, rn)
	}
	return line("mul"+condSuffix(cc)+ssuf, rd, rm, rs)
}

func decodeMulLong(w uint32, cc byte) string {
	u, a := w>>22&1 != 0, w>>21&1 != 0
	ssuf := ""
	if w>>20&1 != 0 {
		ssuf = "s"
	}
	rdhi, rdlo := regName(byte(w>>16&0xF)), regName(byte(w>>12&0xF))
	rs, rm := regName(byte(w>>8&0xF)), regName(byte(w&0xF))
	mnem := "umull"
	switch {
	case !u && a:
		mnem = "umlal"
	case u && !a:
		mnem = "smull"
	case u && a:
		mnem = "smlal"
	}
	return line(mnem+condSuffix(cc)+ssuf, rdlo, rdhi, rm, rs)
}

func decodeHalfword(w uint32, cc byte) string {
	load, p, u := w>>20&1 != 0, w>>24&1 != 0, w>>23&1 != 0
	imm, wb := w>>22&1 != 0, w>>21&1 != 0
	rn, rd := byte(w>>16&0xF), byte(w>>12&0xF)
	sbit, hbit := w>>6&1 != 0, w>>5&1 != 0

	mnem := "?"
	switch {
	case !sbit && hbit && load:
		mnem = "ldrh"
	case !sbit && hbit && !load:
		mnem = "strh"
	case sbit && !hbit:
		mnem = "ldrsb"
	case sbit && hbit:
		mnem = "ldrsh"
	}

	var off string
	if imm {
		mag := w>>8&0xF<<4 | w&0xF // hi nibble : lo nibble, per ldrhStrh
		off = fmt.Sprintf("#%s%d", sign(u), mag)
	} else {
		off = sign(u) + regName(byte(w&0xF))
	}
	return line(mnem+condSuffix(cc), regName(rd), memOperand(regName(rn), off, p, wb))
}

func decodeSingleTransfer(w uint32, cc byte) string {
	i, p, u := w>>25&1 != 0, w>>24&1 != 0, w>>23&1 != 0
	byteOp, wb, load := w>>22&1 != 0, w>>21&1 != 0, w>>20&1 != 0
	rn, rd := byte(w>>16&0xF), byte(w>>12&0xF)

	mnem := "str"
	if load {
		mnem = "ldr"
	}
	if byteOp {
		mnem += "b"
	}

	var off string
	if i {
		shiftAmt, shiftType := byte(w>>7&0x1F), byte(w>>5&3)
		off = sign(u) + shiftedReg(byte(w&0xF), shiftType, shiftAmt, false)
	} else {
		off = fmt.Sprintf("#%s%d", sign(u), w&0xFFF)
	}
	return line(mnem+condSuffix(cc), regName(rd), memOperand(regName(rn), off, p, wb))
}

func decodeBlockTransfer(w uint32, cc byte) string {
	p, u, wb, load := w>>24&1 != 0, w>>23&1 != 0, w>>21&1 != 0, w>>20&1 != 0
	rn, list := byte(w>>16&0xF), w&0xFFFF

	// push/pop are the sp-based special cases pushPop emits; anything
	// else on r13 (or wb==false) is a plain block transfer instead.
	if rn == 13 && wb {
		if load && !p && u {
			return line("pop"+condSuffix(cc), regList(list))
		}
		if !load && p && !u {
			return line("push"+condSuffix(cc), regList(list))
		}
	}

	mode, ok := blockModeName(p, u)
	if !ok {
		mode = "??"
	}
	mnem := "stm" + mode
	if load {
		mnem = "ldm" + mode
	}
	base := regName(rn)
	if wb {
		base += "!"
	}
	return line(mnem+condSuffix(cc), base, regList(list))
}

func blockModeName(p, u bool) (string, bool) {
	for _, m := range isaarm.BlockModes {
		if m.P == p && m.U == u {
			return m.Generic, true
		}
	}
	return "", false
}

func decodeDataProc(w uint32, cc byte) string {
	i := w>>25&1 != 0
	opcode := byte(w >> 21 & 0xF)
	s := w>>20&1 != 0
	rn, rd := regName(byte(w>>16&0xF)), regName(byte(w>>12&0xF))
	op2 := w & 0xFFF

	d, ok := isaarm.DataProcByOpcode(opcode)
	if !ok {
		return "" // all sixteen opcodes are tabulated; unreachable
	}

	var op2s string
	if i {
		op2s = fmt.Sprintf("#%d", isaarm.DecodeModImm(byte(op2>>8&0xF), byte(op2&0xFF)))
	} else {
		rm := byte(op2 & 0xF)
		if op2>>4&1 != 0 {
			op2s = shiftedReg(rm, byte(op2>>5&3), byte(op2>>8&0xF), true)
		} else {
			op2s = shiftedReg(rm, byte(op2>>5&3), byte(op2>>7&0x1F), false)
		}
	}

	ssuf := ""
	if s && !d.ForcesS { // tst/teq/cmp/cmn always set S; it's implicit, not printed
		ssuf = "s"
	}
	mnem := d.Name + condSuffix(cc) + ssuf

	switch {
	case d.ForcesS: // no Rd
		return line(mnem, rn, op2s)
	case !d.UsesRn: // mov/mvn: no Rn
		return line(mnem, rd, op2s)
	default:
		return line(mnem, rd, rn, op2s)
	}
}

// ---------------------------------------------------------------------------
// Formatting helpers.
// ---------------------------------------------------------------------------

func line(mnem string, operands ...string) string {
	if len(operands) == 0 {
		return mnem
	}
	return fmt.Sprintf("%-7s %s", mnem, strings.Join(operands, ", "))
}

// regName prefers the sp/lr/pc synonyms for r13-r15 over isa/arm's
// canonical r13/r14/r15 spelling — a printer decision the isa/arm README
// explicitly leaves to callers like this one.
func regName(r byte) string {
	switch r {
	case 13:
		return "sp"
	case 14:
		return "lr"
	case 15:
		return "pc"
	default:
		return isaarm.Reg(r).Name()
	}
}

func regList(mask uint32) string {
	var parts []string
	for i := 0; i < isaarm.NumGPR; i++ {
		if mask&(1<<uint(i)) != 0 {
			parts = append(parts, regName(byte(i)))
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// shiftedReg formats a barrel-shifter register operand: a plain register,
// an immediate shift (with the ROR/#0 case printed as rrx), or a
// register-specified shift.
func shiftedReg(rm, shiftType, amtOrReg byte, byReg bool) string {
	base := regName(rm)
	switch {
	case byReg:
		return fmt.Sprintf("%s, %s %s", base, isaarm.ShiftName(shiftType), regName(amtOrReg))
	case shiftType == isaarm.ShiftLSL && amtOrReg == 0:
		return base
	case shiftType == isaarm.ShiftROR && amtOrReg == 0:
		return base + ", rrx"
	default:
		return fmt.Sprintf("%s, %s #%d", base, isaarm.ShiftName(shiftType), amtOrReg)
	}
}

// memOperand formats a base+offset addressing-mode operand: pre-indexed
// "[Rn, off]" (with a trailing "!" for write-back) or post-indexed
// "[Rn], off", where post-indexed writes back architecturally with no "!".
func memOperand(base, offset string, pre, wback bool) string {
	if pre {
		s := fmt.Sprintf("[%s, %s]", base, offset)
		if wback {
			s += "!"
		}
		return s
	}
	return fmt.Sprintf("[%s], %s", base, offset)
}

func sign(u bool) string {
	if u {
		return ""
	}
	return "-"
}

// condSuffix omits "al": an assembler normally does, and isa/arm's
// CondName leaves that call to the printer (see its README).
func condSuffix(cc byte) string {
	if cc == isaarm.CondAL {
		return ""
	}
	return isaarm.CondName(cc)
}