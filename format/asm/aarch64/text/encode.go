// Package text renders a lowered aarch64.Program as a human-readable A64
// assembly listing — arrow 6 of the README taxonomy.
//
// Because Program carries finished machine bytes (deliberately — the seam
// stays minimal), this is implemented as a disassembler over exactly the
// encoding subset lower/aarch64 emits: fixed 4-byte little-endian words
// (in BOTH archs — A64 code is never big-endian, lower/aarch64/arch.go),
// move-wide (movz/movn/movk), add/sub imm and register/extended forms,
// logical register forms, MADD/MSUB and the mul-high family, DP-2-source
// (shifts, div), DP-1-source (clz/rbit/rev), bitfield extend aliases,
// csel/csinc, the scaled/unscaled load-store classes, register-offset
// byte transfers, ldar/stlr, ldaxr/stlxr, stp/ldp pre/post-indexed frame
// pairs, branches (b, b.cond, cbz/cbnz, bl, blr, br, ret), and the fixed
// misc words (dmb ish, clrex, brk). Fixup sites are annotated with their
// symbols and kinds; unrecognized words degrade to `.word` lines rather
// than failing, so the listing stays useful if the encoder grows ahead of
// this printer. Never an input format.
package text

import (
	"fmt"
	"sort"
	"strings"

	aarch64 "github.com/vertex-language/vvm/lower/aarch64"
)

// Encode produces the debug listing for a lowered program. Instruction
// words are read little-endian unconditionally; only global Data honors
// the program's byte order (annotated in the header for the reader).
func Encode(p *aarch64.Program) ([]byte, error) {
	var w strings.Builder
	dataOrder := "LE"
	if p.Arch.Big() {
		dataOrder = "BE"
	}
	fmt.Fprintf(&w, "// vvm debug listing — A64 (%s, lower/aarch64 subset), code LE / data %s, not assemblable input\n",
		p.Arch, dataOrder)
	for i := range p.Funcs {
		writeFunc(&w, &p.Funcs[i])
	}
	for i := range p.Globals {
		writeGlobal(&w, &p.Globals[i], p.Arch.Big())
	}
	return []byte(w.String()), nil
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func writeFunc(w *strings.Builder, f *aarch64.Func) {
	tag := ""
	if f.Export {
		tag = " export"
	}
	fmt.Fprintf(w, "\nfn %s:%s  // size=%d align=%d fixups=%d\n",
		f.Name, tag, len(f.Code), f.Align, len(f.Fixups))

	fx := map[int]aarch64.Fixup{}
	for _, x := range f.Fixups {
		fx[int(x.Offset)] = x
	}
	pos := 0
	for ; pos+4 <= len(f.Code); pos += 4 {
		word := uint32(f.Code[pos]) | uint32(f.Code[pos+1])<<8 |
			uint32(f.Code[pos+2])<<16 | uint32(f.Code[pos+3])<<24
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
// Decoder — exactly the lower/aarch64 encoding subset
// ---------------------------------------------------------------------------

// xr / wr name registers in X/W spelling; encoding 31 prints per context.
func xr(n uint32) string {
	switch n {
	case 29:
		return "x29"
	case 30:
		return "x30"
	case 31:
		return "xzr"
	}
	return fmt.Sprintf("x%d", n)
}

func xsp(n uint32) string {
	if n == 31 {
		return "sp"
	}
	return xr(n)
}

func wr(n uint32) string {
	if n == 31 {
		return "wzr"
	}
	return fmt.Sprintf("w%d", n)
}

// rn picks the X or W spelling from the sf bit.
func rn(sf bool, n uint32) string {
	if sf {
		return xr(n)
	}
	return wr(n)
}

var ccName = [16]string{
	"eq", "ne", "hs", "lo", "mi", "pl", "vs", "vc",
	"hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
}

func fixupRef(fx aarch64.Fixup) string {
	return fmt.Sprintf("%s<%s%+d>", fx.Symbol, fx.Kind, fx.Addend)
}

func decodeWord(w uint32, pos int, fx map[int]aarch64.Fixup) (string, bool) {
	sf := w>>31 == 1

	// Fixed words first.
	switch w {
	case 0xD503201F:
		return "nop", true
	case 0xD5033BBF:
		return "dmb ish", true
	case 0xD5033F5F:
		return "clrex", true
	case 0xD65F03C0:
		return "ret", true
	case 0xA9BF7BFD:
		return "stp x29, x30, [sp, #-16]!", true
	case 0xA8C17BFD:
		return "ldp x29, x30, [sp], #16", true
	case 0x910003FD:
		return "mov x29, sp", true
	case 0x910003BF:
		return "mov sp, x29", true
	}

	switch {
	case w&0xFFE0001F == 0xD4200000: // brk #imm16
		return fmt.Sprintf("brk #%d", w>>5&0xFFFF), true

	case w&0xFFFFFC1F == 0xD63F0000:
		return "blr " + xr(w>>5&0x1F), true
	case w&0xFFFFFC1F == 0xD61F0000:
		return "br " + xr(w>>5&0x1F), true

	// Move-wide: movz/movn/movk.
	case w&0x7F800000 == 0x52800000 || w&0x7F800000 == 0x12800000 || w&0x7F800000 == 0x72800000:
		op := "movz"
		switch w >> 29 & 3 {
		case 0:
			op = "movn"
		case 3:
			op = "movk"
		}
		rd, imm, hw := w&0x1F, w>>5&0xFFFF, w>>21&3
		s := fmt.Sprintf("%s %s, #0x%x", op, rn(sf, rd), imm)
		if hw != 0 {
			s += fmt.Sprintf(", lsl #%d", hw*16)
		}
		if x, ok := fx[pos]; ok {
			s += "  // " + fixupRef(x)
		}
		return s, true

	// Branches.
	case w&0x7C000000 == 0x14000000: // b / bl
		op := "b"
		if w>>31 == 1 {
			op = "bl"
		}
		if x, ok := fx[pos]; ok {
			return op + " " + fixupRef(x), true
		}
		rel := int32(w<<6) >> 4 // sign-extended imm26 words -> bytes
		return fmt.Sprintf("%s 0x%x", op, pos+int(rel)), true
	case w&0xFF000010 == 0x54000000: // b.cond
		rel := int32(w<<8) >> 11 << 2
		return fmt.Sprintf("b.%s 0x%x", ccName[w&0xF], pos+int(rel)), true
	case w&0x7F000000 == 0x34000000 || w&0x7F000000 == 0x35000000: // cbz/cbnz
		op := "cbz"
		if w>>24&1 == 1 {
			op = "cbnz"
		}
		rel := int32(w<<8) >> 11 << 2
		return fmt.Sprintf("%s %s, 0x%x", op, rn(sf, w&0x1F), pos+int(rel)), true

	// Exclusives / acquire-release (size class in bits 31:30).
	case w&0x3FFFFC00 == 0x08DFFC00:
		return fmt.Sprintf("ldar %s, [%s]", ldSzReg(w), xsp(w>>5&0x1F)), true
	case w&0x3FFFFC00 == 0x089FFC00:
		return fmt.Sprintf("stlr %s, [%s]", ldSzReg(w), xsp(w>>5&0x1F)), true
	case w&0x3FFFFC00 == 0x085FFC00:
		return fmt.Sprintf("ldaxr %s, [%s]", ldSzReg(w), xsp(w>>5&0x1F)), true
	case w&0x3FA0FC00 == 0x0800FC00:
		return fmt.Sprintf("stlxr %s, %s, [%s]", wr(w>>16&0x1F), ldSzReg(w), xsp(w>>5&0x1F)), true

	// MADD/MSUB family.
	case w&0x7FE08000 == 0x1B000000: // madd
		if w>>10&0x1F == 31 {
			return fmt.Sprintf("mul %s, %s, %s", rn(sf, w&0x1F), rn(sf, w>>5&0x1F), rn(sf, w>>16&0x1F)), true
		}
		return fmt.Sprintf("madd %s, %s, %s, %s", rn(sf, w&0x1F), rn(sf, w>>5&0x1F), rn(sf, w>>16&0x1F), rn(sf, w>>10&0x1F)), true
	case w&0x7FE08000 == 0x1B008000: // msub
		return fmt.Sprintf("msub %s, %s, %s, %s", rn(sf, w&0x1F), rn(sf, w>>5&0x1F), rn(sf, w>>16&0x1F), rn(sf, w>>10&0x1F)), true
	case w&0xFFE0FC00 == 0x9B407C00:
		return fmt.Sprintf("smulh %s, %s, %s", xr(w&0x1F), xr(w>>5&0x1F), xr(w>>16&0x1F)), true
	case w&0xFFE0FC00 == 0x9BC07C00:
		return fmt.Sprintf("umulh %s, %s, %s", xr(w&0x1F), xr(w>>5&0x1F), xr(w>>16&0x1F)), true
	case w&0xFFE0FC00 == 0x9B207C00:
		return fmt.Sprintf("smull %s, %s, %s", xr(w&0x1F), wr(w>>5&0x1F), wr(w>>16&0x1F)), true
	case w&0xFFE0FC00 == 0x9BA07C00:
		return fmt.Sprintf("umull %s, %s, %s", xr(w&0x1F), wr(w>>5&0x1F), wr(w>>16&0x1F)), true

	// DP 2-source: udiv/sdiv/lslv/lsrv/asrv/rorv.
	case w&0x7FE0F000 == 0x1AC00000 && w>>10&0x3F <= 0xB:
		names := map[uint32]string{0x2: "udiv", 0x3: "sdiv", 0x8: "lsl", 0x9: "lsr", 0xA: "asr", 0xB: "ror"}
		if nm, ok := names[w>>10&0x3F]; ok {
			return fmt.Sprintf("%s %s, %s, %s", nm, rn(sf, w&0x1F), rn(sf, w>>5&0x1F), rn(sf, w>>16&0x1F)), true
		}
		return "", false

	// DP 1-source: rbit/rev16/rev/clz.
	case w&0x7FFFF000 == 0x5AC00000:
		var nm string
		switch w >> 10 & 0x3F {
		case 0x0:
			nm = "rbit"
		case 0x1:
			nm = "rev16"
		case 0x2:
			if sf {
				nm = "rev32"
			} else {
				nm = "rev"
			}
		case 0x3:
			nm = "rev"
		case 0x4:
			nm = "clz"
		default:
			return "", false
		}
		return fmt.Sprintf("%s %s, %s", nm, rn(sf, w&0x1F), rn(sf, w>>5&0x1F)), true

	// csel / csinc (cset alias).
	case w&0x7FE00C00 == 0x1A800000:
		return fmt.Sprintf("csel %s, %s, %s, %s", rn(sf, w&0x1F), rn(sf, w>>5&0x1F), rn(sf, w>>16&0x1F), ccName[w>>12&0xF]), true
	case w&0x7FE00C00 == 0x1A800400:
		if w>>5&0x1F == 31 && w>>16&0x1F == 31 {
			return fmt.Sprintf("cset %s, %s", rn(sf, w&0x1F), ccName[(w>>12&0xF)^1]), true
		}
		return fmt.Sprintf("csinc %s, %s, %s, %s", rn(sf, w&0x1F), rn(sf, w>>5&0x1F), rn(sf, w>>16&0x1F), ccName[w>>12&0xF]), true

	// Bitfield (ubfm/sbfm + aliases).
	case w&0x7F800000 == 0x53000000 || w&0x7F800000 == 0x13000000:
		signedBF := w>>29&3 == 0
		immr, imms := w>>16&0x3F, w>>10&0x3F
		rd, rs := w&0x1F, w>>5&0x1F
		width := uint32(32)
		if sf {
			width = 64
		}
		switch {
		case immr == 0 && imms == 7 && !signedBF:
			return fmt.Sprintf("uxtb %s, %s", rn(sf, rd), wr(rs)), true
		case immr == 0 && imms == 15 && !signedBF:
			return fmt.Sprintf("uxth %s, %s", rn(sf, rd), wr(rs)), true
		case immr == 0 && imms == 7 && signedBF:
			return fmt.Sprintf("sxtb %s, %s", rn(sf, rd), wr(rs)), true
		case immr == 0 && imms == 15 && signedBF:
			return fmt.Sprintf("sxth %s, %s", rn(sf, rd), wr(rs)), true
		case immr == 0 && imms == 31 && signedBF && sf:
			return fmt.Sprintf("sxtw %s, %s", xr(rd), wr(rs)), true
		case imms == width-1 && !signedBF:
			return fmt.Sprintf("lsr %s, %s, #%d", rn(sf, rd), rn(sf, rs), immr), true
		case imms == width-1 && signedBF:
			return fmt.Sprintf("asr %s, %s, #%d", rn(sf, rd), rn(sf, rs), immr), true
		case !signedBF && imms+1 == immr:
			return fmt.Sprintf("lsl %s, %s, #%d", rn(sf, rd), rn(sf, rs), width-immr), true
		case !signedBF && immr == 0:
			return fmt.Sprintf("ubfx %s, %s, #0, #%d", rn(sf, rd), rn(sf, rs), imms+1), true
		case signedBF && immr == 0:
			return fmt.Sprintf("sbfx %s, %s, #0, #%d", rn(sf, rd), rn(sf, rs), imms+1), true
		}
		return "", false

	// Add/sub immediate (mov to/from SP prints via the fixed words above).
	case w&0x1F000000 == 0x11000000:
		names := [4]string{"add", "adds", "sub", "subs"}
		nm := names[w>>29&3]
		imm := w >> 10 & 0xFFF
		shift := ""
		if w>>22&1 == 1 {
			shift = ", lsl #12"
		}
		rd, rs := w&0x1F, w>>5&0x1F
		if nm == "subs" && rd == 31 {
			return fmt.Sprintf("cmp %s, #%d%s", rn(sf, rs), imm, shift), true
		}
		if nm == "adds" && rd == 31 {
			return fmt.Sprintf("cmn %s, #%d%s", rn(sf, rs), imm, shift), true
		}
		dst, src := rn(sf, rd), rn(sf, rs)
		if nm == "add" || nm == "sub" { // imm form treats 31 as SP
			dst, src = xsp(rd), xsp(rs)
			if !sf {
				dst, src = wr(rd), wr(rs)
			}
		}
		return fmt.Sprintf("%s %s, %s, #%d%s", nm, dst, src, imm, shift), true

	// Logical immediate (only the and_sp mask form is emitted).
	case w&0x7F800000 == 0x12400000 && sf:
		immr, imms := w>>16&0x3F, w>>10&0x3F
		if imms == 63-(64-immr)+0 || true { // decode generically enough
			// Reconstruct the -2^k mask this backend emits.
			k := 64 - immr
			_ = k
		}
		return fmt.Sprintf("and %s, %s, #<bitmask immr=%d imms=%d>", xsp(w&0x1F), xsp(w>>5&0x1F), immr, imms), true

	// Add/sub extended register (SP-capable forms).
	case w&0x7FE0FC00 == 0x0B206000 || w&0x7FE0FC00 == 0x4B206000:
		nm := "add"
		if w>>30&1 == 1 {
			nm = "sub"
		}
		return fmt.Sprintf("%s %s, %s, %s, uxtx", nm, xsp(w&0x1F), xsp(w>>5&0x1F), xr(w>>16&0x1F)), true

	// Logical / add-sub shifted register (plain, no shift emitted).
	case w&0x1F200C00&0x1F200000 == 0x0A000000 && w>>10&0x3F == 0:
		return decodeLogicalReg(w, sf)
	case w&0x7F200C00 == 0x0B000000 && w>>10&0x3F == 0:
		return decodeAddSubReg(w, sf)
	case w&0x7F200C00 == 0x4B000000 && w>>10&0x3F == 0:
		return decodeAddSubReg(w, sf)
	case w&0x7F200C00 == 0x2B000000 && w>>10&0x3F == 0:
		return decodeAddSubReg(w, sf)
	case w&0x7F200C00 == 0x6B000000 && w>>10&0x3F == 0:
		return decodeAddSubReg(w, sf)

	// Register-offset byte transfers.
	case w&0xFFE0FC00 == 0x38606800:
		return fmt.Sprintf("ldrb %s, [%s, %s]", wr(w&0x1F), xsp(w>>5&0x1F), xr(w>>16&0x1F)), true
	case w&0xFFE0FC00 == 0x38206800:
		return fmt.Sprintf("strb %s, [%s, %s]", wr(w&0x1F), xsp(w>>5&0x1F), xr(w>>16&0x1F)), true

	// Scaled unsigned-offset loads/stores.
	case w&0x3F400000 == 0x39000000 || w&0x3F400000 == 0x39400000:
		return decodeLdStImm(w)
	// Unscaled (ldur/stur).
	case w&0x3F600C00 == 0x38000000 || w&0x3F600C00 == 0x38400000:
		return decodeLdStUnscaled(w)
	}
	return "", false
}

func ldSzReg(w uint32) string {
	if w>>30&3 == 3 {
		return xr(w & 0x1F)
	}
	return wr(w & 0x1F)
}

func decodeLogicalReg(w uint32, sf bool) (string, bool) {
	names := [4]string{"and", "orr", "eor", "ands"}
	nm := names[w>>29&3]
	if w>>21&1 == 1 {
		switch nm {
		case "and":
			nm = "bic"
		case "orr":
			nm = "orn"
		default:
			return "", false
		}
	}
	rd, rs, rm := w&0x1F, w>>5&0x1F, w>>16&0x1F
	if nm == "orr" && rs == 31 {
		return fmt.Sprintf("mov %s, %s", rn(sf, rd), rn(sf, rm)), true
	}
	if nm == "orn" && rs == 31 {
		return fmt.Sprintf("mvn %s, %s", rn(sf, rd), rn(sf, rm)), true
	}
	return fmt.Sprintf("%s %s, %s, %s", nm, rn(sf, rd), rn(sf, rs), rn(sf, rm)), true
}

func decodeAddSubReg(w uint32, sf bool) (string, bool) {
	names := [4]string{"add", "adds", "sub", "subs"}
	nm := names[w>>29&3]
	rd, rs, rm := w&0x1F, w>>5&0x1F, w>>16&0x1F
	if nm == "subs" && rd == 31 {
		return fmt.Sprintf("cmp %s, %s", rn(sf, rs), rn(sf, rm)), true
	}
	if nm == "adds" && rd == 31 {
		return fmt.Sprintf("cmn %s, %s", rn(sf, rs), rn(sf, rm)), true
	}
	if nm == "sub" && rs == 31 {
		return fmt.Sprintf("neg %s, %s", rn(sf, rd), rn(sf, rm)), true
	}
	return fmt.Sprintf("%s %s, %s, %s", nm, rn(sf, rd), rn(sf, rs), rn(sf, rm)), true
}

var szName = [4]string{"b", "h", "", ""}

func decodeLdStImm(w uint32) (string, bool) {
	size := w >> 30 & 3
	load := w>>22&1 == 1
	op := "str"
	if load {
		op = "ldr"
	}
	op += szName[size]
	rt := wr(w & 0x1F)
	if size == 3 {
		rt = xr(w & 0x1F)
	}
	off := (w >> 10 & 0xFFF) << size
	base := xsp(w >> 5 & 0x1F)
	if off == 0 {
		return fmt.Sprintf("%s %s, [%s]", op, rt, base), true
	}
	return fmt.Sprintf("%s %s, [%s, #%d]", op, rt, base, off), true
}

func decodeLdStUnscaled(w uint32) (string, bool) {
	size := w >> 30 & 3
	load := w>>22&1 == 1
	op := "stur"
	if load {
		op = "ldur"
	}
	if size < 2 {
		op += szName[size]
	}
	rt := wr(w & 0x1F)
	if size == 3 {
		rt = xr(w & 0x1F)
	}
	disp := int32(w<<11) >> 23
	base := xsp(w >> 5 & 0x1F)
	if disp == 0 {
		return fmt.Sprintf("%s %s, [%s]", op, rt, base), true
	}
	return fmt.Sprintf("%s %s, [%s, #%d]", op, rt, base, disp), true
}

// ---------------------------------------------------------------------------
// Globals
// ---------------------------------------------------------------------------

func writeGlobal(w *strings.Builder, g *aarch64.Global, be bool) {
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

	fxs := append([]aarch64.Fixup(nil), g.Fixups...)
	sort.Slice(fxs, func(i, j int) bool { return fxs[i].Offset < fxs[j].Offset })
	pos, fi := 0, 0
	for pos < len(g.Data) {
		if fi < len(fxs) && int(fxs[fi].Offset) == pos {
			x := fxs[fi]
			fmt.Fprintf(w, "  .quad %s%+d  // %s\n", x.Symbol, x.Addend, x.Kind)
			pos += 8 // all aarch64 data fixups are 64-bit fields (FixupAbs64)
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