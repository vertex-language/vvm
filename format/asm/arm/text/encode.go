// Package text renders a lowered arm.Program as a human-readable A32
// assembly listing (UAL-style) — arrow 6 of the README taxonomy.
//
// Because Program carries finished machine bytes (deliberately — the seam
// stays minimal), this is implemented as a disassembler over exactly the
// encoding subset lower/arm emits: fixed 4-byte little-endian words, cond
// AL everywhere except conditional branches and movcc, data-processing
// register/rotated-immediate forms, movw/movt pairs, the LDR/STR word/byte
// and halfword classes, ldrex/strex, and the fixed misc words (dmb, clrex,
// udf, push/pop, bx/blx). Fixup sites are annotated with their symbols and
// kinds; unrecognized words degrade to `.word` lines rather than failing,
// so the listing stays useful if the encoder grows ahead of this printer.
// Never an input format.
package text

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"

	arm "github.com/vertex-language/vvm/lower/arm"
)

// Encode produces the debug listing for a lowered program, reading
// instruction words in the program's byte order (Program.Arch).
func Encode(p *arm.Program) ([]byte, error) {
	var w strings.Builder
	fmt.Fprintf(&w, "// vvm debug listing — A32 (%s, lower/arm subset), UAL syntax, not assemblable input\n", p.Arch)
	for i := range p.Funcs {
		writeFunc(&w, &p.Funcs[i], p.Arch.Big())
	}
	for i := range p.Globals {
		writeGlobal(&w, &p.Globals[i])
	}
	return []byte(w.String()), nil
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func writeFunc(w *strings.Builder, f *arm.Func, big bool) {
	tag := ""
	if f.Export {
		tag = " export"
	}
	fmt.Fprintf(w, "\nfn %s:%s  // size=%d align=%d fixups=%d\n",
		f.Name, tag, len(f.Code), f.Align, len(f.Fixups))

	fx := map[int]arm.Fixup{}
	for _, x := range f.Fixups {
		fx[int(x.Offset)] = x
	}
	pos := 0
	for ; pos+4 <= len(f.Code); pos += 4 {
		var word uint32
		if big {
			word = uint32(f.Code[pos])<<24 | uint32(f.Code[pos+1])<<16 |
				uint32(f.Code[pos+2])<<8 | uint32(f.Code[pos+3])
		} else {
			word = uint32(f.Code[pos]) | uint32(f.Code[pos+1])<<8 |
				uint32(f.Code[pos+2])<<16 | uint32(f.Code[pos+3])<<24
		}
		text, ok := decodeWord(word, pos, fx)
		if !ok {
			text = fmt.Sprintf(".word 0x%08x", word)
		}
		fmt.Fprintf(w, "  %08x  %08x  %s\n", pos, word, text)
	}
	for ; pos < len(f.Code); pos++ { // never emitted; defensive
		fmt.Fprintf(w, "  %08x  %02x        db 0x%02x\n", pos, f.Code[pos], f.Code[pos])
	}
}

// ---------------------------------------------------------------------------
// Decoder — exactly the lower/arm encoding subset
// ---------------------------------------------------------------------------

var regName = [16]string{
	"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"r8", "r9", "r10", "fp", "ip", "sp", "lr", "pc",
}

// Condition suffixes; AL prints empty, per convention.
var ccName = [16]string{
	"eq", "ne", "hs", "lo", "mi", "pl", "vs", "vc",
	"hi", "ls", "ge", "lt", "gt", "le", "", "nv",
}

var dpName = [16]string{
	"and", "eor", "sub", "rsb", "add", "adc", "sbc", "rsc",
	"tst", "teq", "cmp", "cmn", "orr", "mov", "bic", "mvn",
}

var shiftName = [4]string{"lsl", "lsr", "asr", "ror"}

func fixupRef(fx arm.Fixup) string {
	return fmt.Sprintf("%s<%s%+d>", fx.Symbol, fx.Kind, fx.Addend)
}

// shifterReg renders the register form of a dp operand 2 (bits 11:0).
func shifterReg(w uint32) string {
	rm := regName[w&0xF]
	ty := shiftName[w>>5&3]
	if w&0x10 != 0 { // shift by register
		return fmt.Sprintf("%s, %s %s", rm, ty, regName[w>>8&0xF])
	}
	amt := w >> 7 & 0x1F
	if amt == 0 && w>>5&3 == 0 { // plain register, no shift
		return rm
	}
	return fmt.Sprintf("%s, %s #%d", rm, ty, amt)
}

// rotImm decodes the rotated-immediate form of operand 2.
func rotImm(w uint32) uint32 {
	return bits.RotateLeft32(w&0xFF, -int(w>>8&0xF)*2)
}

func immStr(v uint32) string {
	if int32(v) < 0 || v > 0xFFFF {
		return fmt.Sprintf("#0x%x", v)
	}
	return fmt.Sprintf("#%d", v)
}

func decodeWord(w uint32, pos int, fx map[int]arm.Fixup) (string, bool) {
	cond := w >> 28
	cc := ccName[cond]
	body := w & 0x0FFFFFFF

	// Fixed misc words first (some live in the cond=1111 space).
	switch w {
	case 0xF57FF05B:
		return "dmb ish", true
	case 0xF57FF01F:
		return "clrex", true
	}
	if cond == 0xF {
		return "", false
	}

	switch {
	case body&0x0FF000F0 == 0x07F000F0: // udf
		return fmt.Sprintf("udf #%d", (body>>8&0xFFF)<<4|body&0xF), true

	case body&0x0FFF0000 == 0x092D0000: // stmdb sp!, {...}
		return "push " + regList(w), true
	case body&0x0FFF0000 == 0x08BD0000: // ldmia sp!, {...}
		return "pop " + regList(w), true

	case body&0x0FFFFFF0 == 0x012FFF30:
		return "blx " + regName[w&0xF], true
	case body&0x0FFFFFF0 == 0x012FFF10:
		return "bx " + regName[w&0xF], true

	case body&0x0FF0F0F0 == 0x0730F010: // udiv rd, rn, rm
		return fmt.Sprintf("udiv %s, %s, %s", regName[w>>16&0xF], regName[w&0xF], regName[w>>8&0xF]), true
	case body&0x0FF0F0F0 == 0x0710F010:
		return fmt.Sprintf("sdiv %s, %s, %s", regName[w>>16&0xF], regName[w&0xF], regName[w>>8&0xF]), true

	case body&0x0FFF0FF0 == 0x016F0F10:
		return fmt.Sprintf("clz %s, %s", regName[w>>12&0xF], regName[w&0xF]), true
	case body&0x0FFF0FF0 == 0x06FF0F30:
		return fmt.Sprintf("rbit %s, %s", regName[w>>12&0xF], regName[w&0xF]), true
	case body&0x0FFF0FF0 == 0x06BF0F30:
		return fmt.Sprintf("rev %s, %s", regName[w>>12&0xF], regName[w&0xF]), true
	case body&0x0FFF0FF0 == 0x06EF0070:
		return fmt.Sprintf("uxtb %s, %s", regName[w>>12&0xF], regName[w&0xF]), true
	case body&0x0FFF0FF0 == 0x06FF0070:
		return fmt.Sprintf("uxth %s, %s", regName[w>>12&0xF], regName[w&0xF]), true
	case body&0x0FFF0FF0 == 0x06AF0070:
		return fmt.Sprintf("sxtb %s, %s", regName[w>>12&0xF], regName[w&0xF]), true
	case body&0x0FFF0FF0 == 0x06BF0070:
		return fmt.Sprintf("sxth %s, %s", regName[w>>12&0xF], regName[w&0xF]), true

	case body&0x0FF00FFF == 0x01900F9F: // ldrex rt, [rn]
		return fmt.Sprintf("ldrex %s, [%s]", regName[w>>12&0xF], regName[w>>16&0xF]), true
	case body&0x0FF00FF0 == 0x01800F90: // strex rd, rt, [rn]
		return fmt.Sprintf("strex %s, %s, [%s]", regName[w>>12&0xF], regName[w&0xF], regName[w>>16&0xF]), true

	case body&0x0FE000F0 == 0x00000090: // mul rd, rn, rm
		return fmt.Sprintf("mul %s, %s, %s", regName[w>>16&0xF], regName[w&0xF], regName[w>>8&0xF]), true
	case body&0x0FF000F0 == 0x00600090: // mls rd, rn, rm, ra
		return fmt.Sprintf("mls %s, %s, %s, %s",
			regName[w>>16&0xF], regName[w&0xF], regName[w>>8&0xF], regName[w>>12&0xF]), true
	case body&0x0FE000F0 == 0x00800090: // umull rdlo, rdhi, rn, rm
		return fmt.Sprintf("umull %s, %s, %s, %s",
			regName[w>>12&0xF], regName[w>>16&0xF], regName[w&0xF], regName[w>>8&0xF]), true
	case body&0x0FE000F0 == 0x00C00090:
		return fmt.Sprintf("smull %s, %s, %s, %s",
			regName[w>>12&0xF], regName[w>>16&0xF], regName[w&0xF], regName[w>>8&0xF]), true

	case body&0x0FF00000 == 0x03000000: // movw
		return movwt("movw", w, pos, fx), true
	case body&0x0FF00000 == 0x03400000: // movt
		return movwt("movt", w, pos, fx), true

	case body&0x0E000000 == 0x0A000000: // b / bl (± cond)
		op := "b"
		if w&0x01000000 != 0 {
			op = "bl"
		}
		if x, ok := fx[pos]; ok {
			return op + cc + " " + fixupRef(x), true
		}
		rel := int32(w<<8) >> 6 // sign-extended imm24 words -> bytes
		return fmt.Sprintf("%s%s 0x%x", op, cc, pos+8+int(rel)), true

	// Halfword / signed transfers (imm8 form): bits 27:25 = 000, 1SH1.
	case body&0x0E400090 == 0x00400090 && body&0x60 != 0:
		op := ""
		l, sh := w>>20&1, w>>5&3
		switch {
		case l == 1 && sh == 1:
			op = "ldrh"
		case l == 0 && sh == 1:
			op = "strh"
		case l == 1 && sh == 2:
			op = "ldrsb"
		case l == 1 && sh == 3:
			op = "ldrsh"
		default:
			return "", false
		}
		disp := int32(w>>8&0xF)<<4 | int32(w&0xF)
		if w&0x00800000 == 0 { // U bit clear: subtract
			disp = -disp
		}
		return fmt.Sprintf("%s %s, %s", op, regName[w>>12&0xF], memStr(w, disp)), true

	// Word/byte transfers, immediate offset.
	case body&0x0E000000 == 0x04000000:
		op := xferName(w)
		disp := int32(w & 0xFFF)
		if w&0x00800000 == 0 {
			disp = -disp
		}
		return fmt.Sprintf("%s %s, %s", op, regName[w>>12&0xF], memStr(w, disp)), true

	// Word/byte transfers, register offset (only the no-shift byte forms).
	case body&0x0E000FF0 == 0x06000000:
		op := xferName(w)
		return fmt.Sprintf("%s %s, [%s, %s]", op,
			regName[w>>12&0xF], regName[w>>16&0xF], regName[w&0xF]), true

	// Data processing (register and rotated-immediate forms).
	case body&0x0C000000 == 0x00000000:
		return decodeDP(w, cc)
	}
	return "", false
}

func movwt(op string, w uint32, pos int, fx map[int]arm.Fixup) string {
	rd := regName[w>>12&0xF]
	imm := (w >> 16 & 0xF) << 12 | w&0xFFF
	if x, ok := fx[pos]; ok {
		half := "#:lower16:"
		if op == "movt" {
			half = "#:upper16:"
		}
		return fmt.Sprintf("%s %s, %s%s  // %s", op, rd, half, fixupRef(x), x.Kind)
	}
	return fmt.Sprintf("%s %s, #0x%x", op, rd, imm)
}

func xferName(w uint32) string {
	l, b := w>>20&1, w>>22&1
	switch {
	case l == 1 && b == 1:
		return "ldrb"
	case l == 0 && b == 1:
		return "strb"
	case l == 1:
		return "ldr"
	}
	return "str"
}

func memStr(w uint32, disp int32) string {
	base := regName[w>>16&0xF]
	if disp == 0 {
		return fmt.Sprintf("[%s]", base)
	}
	sign, v := "+", disp
	if disp < 0 {
		sign, v = "-", -disp
	}
	return fmt.Sprintf("[%s, #%s0x%x]", base, sign, v)
}

func decodeDP(w uint32, cc string) (string, bool) {
	op := w >> 21 & 0xF
	name := dpName[op]
	s := ""
	if w&0x00100000 != 0 && op != 0x8 && op != 0xA && op != 0xB { // tst/cmp/cmn imply S
		s = "s"
	}
	rn, rd := regName[w>>16&0xF], regName[w>>12&0xF]
	var op2 string
	if w&0x02000000 != 0 {
		op2 = immStr(rotImm(w))
	} else {
		op2 = shifterReg(w)
	}
	switch op {
	case 0xD, 0xF: // mov / mvn: two operands; shifted mov prints as its shift
		if op == 0xD && w&0x02000000 == 0 {
			if ty := w >> 5 & 3; w&0x10 != 0 || w>>7&0x1F != 0 || ty != 0 {
				rm := regName[w&0xF]
				if w&0x10 != 0 {
					return fmt.Sprintf("%s%s %s, %s, %s", shiftName[ty], cc, rd, rm, regName[w>>8&0xF]), true
				}
				return fmt.Sprintf("%s%s %s, %s, #%d", shiftName[ty], cc, rd, rm, w>>7&0x1F), true
			}
		}
		return fmt.Sprintf("%s%s%s %s, %s", name, cc, s, rd, op2), true
	case 0x8, 0xA, 0xB: // tst / cmp / cmn: no Rd
		return fmt.Sprintf("%s%s %s, %s", name, cc, rn, op2), true
	case 0x5, 0x6, 0x7, 0x9: // adc/sbc/rsc/teq: never emitted
		return "", false
	}
	return fmt.Sprintf("%s%s%s %s, %s, %s", name, cc, s, rd, rn, op2), true
}

func regList(w uint32) string {
	var names []string
	for i := 0; i < 16; i++ {
		if w&(1<<i) != 0 {
			names = append(names, regName[i])
		}
	}
	return "{" + strings.Join(names, ", ") + "}"
}

// ---------------------------------------------------------------------------
// Globals
// ---------------------------------------------------------------------------

func writeGlobal(w *strings.Builder, g *arm.Global) {
	tags := ""
	if g.Export {
		tags += " export"
	}
	if g.TLS {
		tags += " tls"
	}
	fmt.Fprintf(w, "\nglobal %s:%s  // size=%d align=%d\n", g.Name, tags, g.Size, g.Align)
	if g.Data == nil {
		fmt.Fprintf(w, "  .zero %d\n", g.Size)
		return
	}

	fxs := append([]arm.Fixup(nil), g.Fixups...)
	sort.Slice(fxs, func(i, j int) bool { return fxs[i].Offset < fxs[j].Offset })
	pos, fi := 0, 0
	for pos < len(g.Data) {
		if fi < len(fxs) && int(fxs[fi].Offset) == pos {
			fx := fxs[fi]
			fmt.Fprintf(w, "  .long %s%+d  // %s\n", fx.Symbol, fx.Addend, fx.Kind)
			pos += 4 // all arm data fixups are 32-bit fields (FixupAbs32)
			fi++
			continue
		}
		end := len(g.Data)
		if fi < len(fxs) {
			end = int(fxs[fi].Offset)
		}
		writeBytes(w, g.Data[pos:end], pos)
		pos = end
	}
	if int(g.Size) > len(g.Data) {
		fmt.Fprintf(w, "  .zero %d\n", int(g.Size)-len(g.Data))
	}
}

func writeBytes(w *strings.Builder, b []byte, base int) {
	for len(b) > 0 {
		// Compress an all-zero tail of useful length.
		if allZero(b) && len(b) >= 8 {
			fmt.Fprintf(w, "  .zero %d\n", len(b))
			return
		}
		n := len(b)
		if n > 8 {
			n = 8
		}
		var hex, ascii strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				hex.WriteString(", ")
			}
			fmt.Fprintf(&hex, "0x%02x", b[i])
			if b[i] >= 0x20 && b[i] < 0x7F {
				ascii.WriteByte(b[i])
			} else {
				ascii.WriteByte('.')
			}
		}
		fmt.Fprintf(w, "  .byte %-46s // %04x %q\n", hex.String(), base, ascii.String())
		b = b[n:]
		base += n
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}