// Package text renders a lowered arm.Program as a human-readable A32
// assembly listing (UAL-style) — arrow 6 of the README taxonomy.
//
// Because Program carries finished machine bytes (deliberately — the seam
// stays minimal), this is implemented as a disassembler over exactly the
// encoding subset lower/arm emits: fixed 4-byte little-endian/big-endian
// words (Program.Arch), cond AL everywhere except conditional branches and
// movcc, data-processing register/rotated-immediate forms, movw/movt
// pairs, the LDR/STR word/byte and halfword classes, ldrex/strex, and the
// fixed misc words (dmb, clrex, udf, push/pop, bx/blx). Fixup sites are
// annotated with their symbols and kinds; unrecognized words degrade to
// `.word` lines rather than failing, so the listing stays useful if the
// encoder grows ahead of this printer. Never an input format.
//
// Register spellings, condition-code numbering, the rotated-immediate
// decoder, the PC-relative branch bias, and the opcode<->mnemonic
// correspondence for data-processing/shift ops are looked up from isa/arm
// — the same facts isa/arm/encoder's Encode (which lower/arm's assemble
// drives) uses to emit bytes — so this decoder can't silently drift out
// of agreement with what lower/arm actually produces. There is no mcode
// tier under lower/arm any more; isa/arm plus isa/arm/encoder are the
// single source of truth for every ISA fact this package needs.
//
// isa/arm's opcode tables (DPOps, ShiftOps) are, like isa/x86_64's,
// mnemonic-keyed only with no opcode->mnemonic direction — nothing under
// isa/arm needed the reverse before now. This package builds its own
// small reverse indices once at init time, directly from isa/arm's
// forward tables, rather than hand-duplicating opcode/mnemonic pairs a
// second time. The fixed Base* instruction words in isa/arm/opcodes.go
// are used directly as the comparison targets for every masked-word
// match below, so a bit position or opcode value can't drift between
// encoder and decoder even though this package's own field masks (which
// register bits vary per instruction shape) are necessarily maintained
// by hand — the same split isa/x86_64's text package draws between
// isa-sourced mnemonic tables and its own independent ModRM-walking
// logic.
package text

import (
	"fmt"
	"sort"
	"strings"

	isaarm "github.com/vertex-language/vvm/isa/arm"
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
// Naming and reverse-index tables — sourced from isa/arm
// ---------------------------------------------------------------------------

// regName is isa/arm.Reg's own spelling (r0-r10, fp/ip/sp/lr/pc) — no
// local table, so this listing's register numbering can never drift from
// isa/arm/encoder's.
func regName(n byte) string { return isaarm.Reg(n).String() }

// ccSuffix is isa/arm.CondName's mnemonic suffix, with one presentation
// choice layered on top: isa/arm's own condName table spells CondAL as
// "al" (so CondName is a faithful mnemonic lookup for every encodable
// value), but UAL convention omits the suffix for the unconditional case,
// and every fixed Base* word this package matches against bakes in
// cond=AL for exactly that reason. bcc/movcc, the only two ops that ever
// carry a variable cond field, never encode CondAL in practice (an
// unconditional branch/mov uses "b"/"mov_r" instead) — so this override
// only ever fires for the plain, always-AL instruction classes below.
func ccSuffix(cond byte) string {
	if cond == isaarm.CondAL {
		return ""
	}
	return isaarm.CondName(cond)
}

// dpByCode and shiftByCode are this package's reverse indices over
// isa/arm's mnemonic-keyed DPOps/ShiftOps tables, built once at init time
// so they can't drift from the forward direction isa/arm/encoder's dp()
// and shift cases consume. dpByCode's unfilled slots (codes 0x5/0x6/0x7/
// 0x9 — adc/sbc/rsc/teq) and the two codes DPOps never carries at all
// (0xD/0xF — mov/mvn, handled below because they're two-operand and
// shared with the shift mnemonics and movcc) are exactly the gaps
// isa/arm/opcodes.go's own doc comment describes.
var (
	dpByCode    [16]isaarm.DPOp
	shiftByCode [4]string
)

func init() {
	for _, d := range isaarm.DPOps {
		dpByCode[d.Code] = d
	}
	for _, s := range isaarm.ShiftOps {
		shiftByCode[s.Code] = s.Name
	}
}

func fixupRef(fx arm.Fixup) string {
	// fx.Kind is arm.FixupKind (= isa/arm/encoder.FixupKind), which
	// already knows how to render itself (inst.go's FixupKind.String());
	// no local table needed.
	return fmt.Sprintf("%s<%s%+d>", fx.Symbol, fx.Kind, fx.Addend)
}

// ---------------------------------------------------------------------------
// Decoder — exactly the lower/arm encoding subset
// ---------------------------------------------------------------------------

// shifterReg renders the register form of a dp operand 2 (bits 11:0).
func shifterReg(w uint32) string {
	rm := regName(byte(w & 0xF))
	ty := shiftByCode[w>>5&3]
	if w&0x10 != 0 { // shift by register
		return fmt.Sprintf("%s, %s %s", rm, ty, regName(byte(w>>8&0xF)))
	}
	amt := w >> 7 & 0x1F
	if amt == 0 && w>>5&3 == 0 { // plain register, no shift
		return rm
	}
	return fmt.Sprintf("%s, %s #%d", rm, ty, amt)
}

func immStr(v uint32) string {
	if int32(v) < 0 || v > 0xFFFF {
		return fmt.Sprintf("#0x%x", v)
	}
	return fmt.Sprintf("#%d", v)
}

func decodeWord(w uint32, pos int, fx map[int]arm.Fixup) (string, bool) {
	// Fixed misc words first — dmb/clrex live in the cond=1111 ("never")
	// space, matching isa/arm/encoder's unconditional emission of these
	// (opcodes.go's BaseDMB/BaseCLREX), so they're checked against the
	// full word before cond is even examined.
	switch w {
	case isaarm.BaseDMB:
		return "dmb ish", true
	case isaarm.BaseCLREX:
		return "clrex", true
	}

	cond := byte(w >> 28)
	if cond == 0xF {
		return "", false
	}
	cc := ccSuffix(cond)
	body := w & 0x0FFFFFFF

	switch {
	case body&0x0FF000F0 == isaarm.BaseUDF&0x0FF000F0: // udf
		return fmt.Sprintf("udf%s #%d", cc, (body>>8&0xFFF)<<4|body&0xF), true

	case body&0x0FFF0000 == isaarm.BasePUSH&0x0FFF0000: // stmdb sp!, {...}
		return "push" + cc + " " + regList(w), true
	case body&0x0FFF0000 == isaarm.BasePOP&0x0FFF0000: // ldmia sp!, {...}
		return "pop" + cc + " " + regList(w), true

	case body&0x0FFFFFF0 == isaarm.BaseBLXR&0x0FFFFFF0:
		return "blx" + cc + " " + regName(byte(w&0xF)), true
	case body&0x0FFFFFF0 == isaarm.BaseBXR&0x0FFFFFF0:
		return "bx" + cc + " " + regName(byte(w&0xF)), true

	case body&0x0FF0F0F0 == isaarm.BaseUDIV&0x0FF0F0F0: // udiv rd, rn, rm
		return fmt.Sprintf("udiv%s %s, %s, %s", cc, regName(byte(w>>16&0xF)), regName(byte(w&0xF)), regName(byte(w>>8&0xF))), true
	case body&0x0FF0F0F0 == isaarm.BaseSDIV&0x0FF0F0F0:
		return fmt.Sprintf("sdiv%s %s, %s, %s", cc, regName(byte(w>>16&0xF)), regName(byte(w&0xF)), regName(byte(w>>8&0xF))), true

	case body&0x0FFF0FF0 == isaarm.BaseCLZ&0x0FFF0FF0:
		return fmt.Sprintf("clz%s %s, %s", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF))), true
	case body&0x0FFF0FF0 == isaarm.BaseRBIT&0x0FFF0FF0:
		return fmt.Sprintf("rbit%s %s, %s", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF))), true
	case body&0x0FFF0FF0 == isaarm.BaseREV&0x0FFF0FF0:
		return fmt.Sprintf("rev%s %s, %s", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF))), true
	case body&0x0FFF0FF0 == isaarm.BaseUXTB&0x0FFF0FF0:
		return fmt.Sprintf("uxtb%s %s, %s", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF))), true
	case body&0x0FFF0FF0 == isaarm.BaseUXTH&0x0FFF0FF0:
		return fmt.Sprintf("uxth%s %s, %s", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF))), true
	case body&0x0FFF0FF0 == isaarm.BaseSXTB&0x0FFF0FF0:
		return fmt.Sprintf("sxtb%s %s, %s", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF))), true
	case body&0x0FFF0FF0 == isaarm.BaseSXTH&0x0FFF0FF0:
		return fmt.Sprintf("sxth%s %s, %s", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF))), true

	case body&0x0FF00FFF == isaarm.BaseLDREX&0x0FF00FFF: // ldrex rt, [rn]
		return fmt.Sprintf("ldrex%s %s, [%s]", cc, regName(byte(w>>12&0xF)), regName(byte(w>>16&0xF))), true
	case body&0x0FF00FF0 == isaarm.BaseSTREX&0x0FF00FF0: // strex rd, rt, [rn]
		return fmt.Sprintf("strex%s %s, %s, [%s]", cc, regName(byte(w>>12&0xF)), regName(byte(w&0xF)), regName(byte(w>>16&0xF))), true

	case body&0x0FE000F0 == isaarm.BaseMUL&0x0FE000F0: // mul rd, rn, rm
		return fmt.Sprintf("mul%s %s, %s, %s", cc, regName(byte(w>>16&0xF)), regName(byte(w&0xF)), regName(byte(w>>8&0xF))), true
	case body&0x0FF000F0 == isaarm.BaseMLS&0x0FF000F0: // mls rd, rn, rm, ra
		return fmt.Sprintf("mls%s %s, %s, %s, %s", cc,
			regName(byte(w>>16&0xF)), regName(byte(w&0xF)), regName(byte(w>>8&0xF)), regName(byte(w>>12&0xF))), true
	case body&0x0FE000F0 == isaarm.BaseUMULL&0x0FE000F0: // umull rdlo, rdhi, rn, rm
		return fmt.Sprintf("umull%s %s, %s, %s, %s", cc,
			regName(byte(w>>12&0xF)), regName(byte(w>>16&0xF)), regName(byte(w&0xF)), regName(byte(w>>8&0xF))), true
	case body&0x0FE000F0 == isaarm.BaseSMULL&0x0FE000F0:
		return fmt.Sprintf("smull%s %s, %s, %s, %s", cc,
			regName(byte(w>>12&0xF)), regName(byte(w>>16&0xF)), regName(byte(w&0xF)), regName(byte(w>>8&0xF))), true

	case body&0x0FF00000 == isaarm.BaseMOVW&0x0FF00000: // movw
		return movwt("movw", w, pos, fx), true
	case body&0x0FF00000 == isaarm.BaseMOVT&0x0FF00000: // movt
		return movwt("movt", w, pos, fx), true

	case body&0x0E000000 == isaarm.BaseBcc&0x0E000000: // b / bl (± cond)
		op := "b"
		if w&0x01000000 != 0 {
			op = "bl"
		}
		if x, ok := fx[pos]; ok {
			return op + cc + " " + fixupRef(x), true
		}
		rel := int32(w<<8) >> 6 // sign-extended imm24 words -> bytes
		return fmt.Sprintf("%s%s 0x%x", op, cc, pos+int(isaarm.PCBias)+int(rel)), true

	// Halfword / signed transfers (imm8 form): shares its class-selector
	// bits with BaseLDRH; sub-decoded below by the L/S1/S0 bits real A32
	// packs into bits 20 and 6:5.
	case body&0x0E400090 == isaarm.BaseLDRH&0x0E400090 && body&0x60 != 0:
		l, sh := w>>20&1, w>>5&3
		var op string
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
		return fmt.Sprintf("%s%s %s, %s", op, cc, regName(byte(w>>12&0xF)), memStr(w, disp)), true

	// Word/byte transfers, immediate offset.
	case body&0x0E000000 == isaarm.BaseLDR&0x0E000000:
		op := xferName(w)
		disp := int32(w & 0xFFF)
		if w&0x00800000 == 0 {
			disp = -disp
		}
		return fmt.Sprintf("%s%s %s, %s", op, cc, regName(byte(w>>12&0xF)), memStr(w, disp)), true

	// Word/byte transfers, register offset (only the no-shift byte forms
	// this backend emits — BaseLDRBR/BaseSTRBR).
	case body&0x0E000FF0 == isaarm.BaseLDRBR&0x0E000FF0:
		op := xferName(w)
		return fmt.Sprintf("%s%s %s, [%s, %s]", op, cc,
			regName(byte(w>>12&0xF)), regName(byte(w>>16&0xF)), regName(byte(w&0xF))), true

	// Data processing (register and rotated-immediate forms) — the
	// catch-all for everything with bits 27:26 == 00 that didn't match a
	// more specific shape above (mul/clz/... all also live in that space
	// but were matched first by their tighter, all-fields-fixed masks).
	case body&0x0C000000 == 0x00000000:
		return decodeDP(w, cond)
	}
	return "", false
}

// decodeDP covers isa/arm's ten DPOps mnemonics plus mov/mvn (opcode
// 0xD/0xF), which DPOps deliberately omits — they're two-operand and
// share their encoding with the shift mnemonics lsl/lsr/asr/ror
// (isa/arm.BaseMOVR, varying only the shift-type/amount fields that are
// otherwise zero for a plain "mov") and with the movcc pseudo-op
// (isa/arm.BaseMOVCCI/BaseMOVCCR, cond variable instead of baked AL).
// adc/sbc/rsc/teq (opcodes 0x5/0x6/0x7/0x9) have no isa/arm.DPOps entry
// either — this backend's dp() can't reach them — so dpByCode reports
// them as unknown here too.
func decodeDP(w uint32, cond byte) (string, bool) {
	cc := ccSuffix(cond)
	op := w >> 21 & 0xF
	rn, rd := regName(byte(w>>16&0xF)), regName(byte(w>>12&0xF))
	var op2 string
	if w&0x02000000 != 0 {
		op2 = immStr(isaarm.UnpackImm12(w & 0xFFF))
	} else {
		op2 = shifterReg(w)
	}

	switch op {
	case 0xD, 0xF: // mov / mvn
		name := "mov"
		if op == 0xF {
			name = "mvn"
		}
		if op == 0xD && w&0x02000000 == 0 { // register-form mov: maybe really a shift
			if ty := w >> 5 & 3; w&0x10 != 0 || w>>7&0x1F != 0 || ty != 0 {
				rm := regName(byte(w & 0xF))
				if w&0x10 != 0 { // shift by register
					return fmt.Sprintf("%s%s %s, %s, %s", shiftByCode[ty], cc, rd, rm, regName(byte(w>>8&0xF))), true
				}
				return fmt.Sprintf("%s%s %s, %s, #%d", shiftByCode[ty], cc, rd, rm, w>>7&0x1F), true
			}
		}
		return fmt.Sprintf("%s%s %s, %s", name, cc, rd, op2), true
	}

	d := dpByCode[op]
	if d.Name == "" {
		return "", false
	}
	if d.Cmp { // tst / cmp / cmn: no Rd, S is implied
		return fmt.Sprintf("%s%s %s, %s", d.Name, cc, rn, op2), true
	}
	s := ""
	if w&0x00100000 != 0 {
		s = "s"
	}
	return fmt.Sprintf("%s%s%s %s, %s, %s", d.Name, cc, s, rd, rn, op2), true
}

func movwt(op string, w uint32, pos int, fx map[int]arm.Fixup) string {
	rd := regName(byte(w >> 12 & 0xF))
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
	base := regName(byte(w >> 16 & 0xF))
	if disp == 0 {
		return fmt.Sprintf("[%s]", base)
	}
	sign, v := "+", disp
	if disp < 0 {
		sign, v = "-", -disp
	}
	return fmt.Sprintf("[%s, #%s0x%x]", base, sign, v)
}

func regList(w uint32) string {
	var names []string
	for i := 0; i < 16; i++ {
		if w&(1<<i) != 0 {
			names = append(names, regName(byte(i)))
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