// Package text renders a lowered aarch64.Program as a human-readable A64
// assembly listing — arrow 6 of the README taxonomy.
//
// Because Program carries finished machine bytes (deliberately — the seam
// stays minimal), this is implemented as a disassembler over exactly the
// encoding subset lower/aarch64 emits: fixed 4-byte little-endian words
// (in BOTH archs — A64 code is never big-endian, lower/aarch64/arch.go),
// move-wide (movz/movn/movk, X-form only), add/sub imm and register/
// extended forms, logical register forms, MADD/MSUB and the mul-high
// family, DP-2-source (shifts, div), DP-1-source (clz/rbit/rev/rev16),
// bitfield extend aliases, csel/csinc/cset, the scaled/unscaled
// load-store classes, register-offset byte transfers, ldar/stlr,
// ldaxr/stlxr, stp/ldp pre/post-indexed frame pairs, branches (b, b.cond,
// cbz/cbnz, bl, blr, br, ret), and the fixed misc words (dmb ish, clrex,
// svc, brk). Fixup sites are annotated with their symbols and kinds;
// unrecognized words degrade to `.word` lines rather than failing, so the
// listing stays useful if the encoder grows ahead of this printer. Never
// an input format.
//
// Register spellings, condition-code mnemonics, and the opcode<->mnemonic
// correspondence are looked up from isa/aarch64 — the same facts
// isa/aarch64/encoder uses to emit bytes — so this decoder can't silently
// drift out of agreement with what the encoder actually produces.
// isa/aarch64's opcode tables (DPImmOpcodes, DPRegOpcodes, DP1Opcodes,
// DP2Opcodes, LdClasses, StClasses) are, like isa/x86_64's, mnemonic-keyed
// only with no opcode->mnemonic direction — nothing under isa/aarch64
// needed the reverse before now. This package builds its own reverse
// indices once at init time, directly from isa/aarch64's forward tables,
// rather than hand-duplicating opcode/mnemonic pairs a second time — the
// same approach isa/x86_64's text package takes for its ALUOpcodes/
// ShiftExt/Grp3Ext/Grp5Ext reverse indices.
//
// A few instruction shapes have no isa/aarch64 constant at all, because
// lower/aarch64's own encoder inlines them as bare literals rather than
// naming them (see isa/aarch64/encoder/encode.go's "and_sp" case) — the
// same situation isa/x86_64's text package documents for 0x81/0xC0/0xC1/
// 0xF7/0xFF. Those are called out individually below rather than silently
// treated as isa-sourced facts.
//
// Everything else here (the word-walking state machine, field-extraction
// masks for a given instruction shape, PC-relative displacement bias) is
// this package's own independent traversal, hand-maintained the same way
// isa/arm's and isa/x86_64's text packages maintain their own ModRM/
// operand-field masks — deliberately not shared with the encoder's own
// control flow, which never needs to invert a mask to recover an operand.
package text

import (
	"fmt"
	"sort"
	"strings"

	isaaarch64 "github.com/vertex-language/vvm/isa/aarch64"
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
// Naming — sourced from isa/aarch64
// ---------------------------------------------------------------------------

// reg picks the X or W spelling for register n from the sf bit, deferring
// to isa/aarch64.XName/WName for encoding 31's SP-vs-ZR ambiguity (the
// same facts isa/aarch64/encoder's own operand emission relies on) rather
// than a locally re-declared register table.
func reg(sf bool, n uint32, isSP bool) string {
	r := isaaarch64.Reg(n)
	if sf {
		return isaaarch64.XName(r, isSP)
	}
	return isaaarch64.WName(r, isSP)
}

func regX(n uint32, isSP bool) string { return isaaarch64.XName(isaaarch64.Reg(n), isSP) }
func regW(n uint32, isSP bool) string { return isaaarch64.WName(isaaarch64.Reg(n), isSP) }

func fixupRef(fx aarch64.Fixup) string {
	// fx.Kind is aarch64.FixupKind (= isa/aarch64/encoder.FixupKind),
	// which already knows how to render itself (inst.go's
	// FixupKind.String()); no local table needed.
	return fmt.Sprintf("%s<%s%+d>", fx.Symbol, fx.Kind, fx.Addend)
}

// ---------------------------------------------------------------------------
// Reverse indices — built once at init from isa/aarch64's forward,
// mnemonic-keyed opcode tables, so this decoder's opcode<->mnemonic
// correspondence can't drift from isa/aarch64/encoder's.
// ---------------------------------------------------------------------------

var (
	// dpImmByWord: masked ADD/SUB (immediate) base word -> mnemonic
	// (add/adds/sub/subs). Mask 0xFF800000 keeps sf/op/S and the fixed
	// 100010 group bits, clearing shift(1)/imm12(12)/Rn(5)/Rd(5).
	dpImmByWord = map[uint32]string{}

	// dpRegByWord: masked ADD/SUB/AND/ORR/EOR/BIC (shifted register,
	// no shift emitted) base word -> mnemonic. Mask 0xFFE00000 keeps
	// sf/opc/S/the fixed group bits and (for the logical family) the N
	// bit that distinguishes bic from and, clearing Rm(5)/imm6 or
	// shift-type/Rn(5)/Rd(5).
	dpRegByWord = map[uint32]string{}

	// dp1ByWord: masked RBIT/REV16/REV/CLZ base word -> mnemonic. Mask
	// 0xFFFFFC00 keeps sf and the fixed opcode bits (including which of
	// the two DP1Opcodes[op] forms — W or X — matched), clearing Rn(5)/
	// Rd(5).
	dp1ByWord = map[uint32]string{}

	// dp2ByOpcode: DP2Opcodes' 6-bit opcode subfield -> mnemonic
	// (udiv/sdiv/lslv/lsrv/asrv/rorv).
	dp2ByOpcode = map[uint32]string{}
)

func init() {
	for name, pair := range isaaarch64.DPImmOpcodes {
		dpImmByWord[pair[0]&0xFF800000] = name
		dpImmByWord[pair[1]&0xFF800000] = name
	}
	for name, pair := range isaaarch64.DPRegOpcodes {
		dpRegByWord[pair[0]&0xFFE00000] = name
		dpRegByWord[pair[1]&0xFFE00000] = name
	}
	for name, pair := range isaaarch64.DP1Opcodes {
		dp1ByWord[pair[0]&0xFFFFFC00] = name
		dp1ByWord[pair[1]&0xFFFFFC00] = name
	}
	for name, code := range isaaarch64.DP2Opcodes {
		dp2ByOpcode[code] = name
	}
}

// displayDP2 translates isa/aarch64.DP2Opcodes' *v-suffixed register-shift
// mnemonics (lslv/lsrv/asrv/rorv — the form the opcode field actually
// names) back to the UAL alias assembly prints for a register-count shift
// (lsl/lsr/asr/ror); udiv/sdiv are unchanged. The same kind of
// isa-fact-to-display-spelling translation isa/x86_64's text package does
// in displayGrp3 for mul1/imul1 -> mul/imul.
func displayDP2(mnem string) string {
	switch mnem {
	case "lslv":
		return "lsl"
	case "lsrv":
		return "lsr"
	case "asrv":
		return "asr"
	case "rorv":
		return "ror"
	}
	return mnem
}

// ldstEntry pairs one LdClasses/StClasses entry's byte size with the
// mnemonic this decoder should print for it.
type ldstEntry struct {
	op   string
	size int
}

var (
	// scaledByBase/unscaledByBase: masked LDR/STR base word -> entry.
	// Mask 0xFFC00000 keeps size(2)/the fixed group bits/L(1), clearing
	// imm12(12)/Rn(5)/Rt(5) for the scaled forms and imm9(9)/Rn(5)/Rt(5)
	// (plus two always-zero bits) for the unscaled (LDUR/STUR) forms —
	// every base word in both isa/aarch64.LdClasses and isa/aarch64.
	// StClasses already has zero in those low 22 bits, so the mask is a
	// no-op against the table's own values and only does work against a
	// live instruction word.
	scaledByBase   = map[uint32]ldstEntry{}
	unscaledByBase = map[uint32]ldstEntry{}
)

func init() {
	for size, c := range isaaarch64.LdClasses {
		scaledByBase[c.Scaled&0xFFC00000] = ldstEntry{"ldr", size}
		unscaledByBase[c.Unscaled&0xFFC00000] = ldstEntry{"ldur", size}
	}
	for size, c := range isaaarch64.StClasses {
		scaledByBase[c.Scaled&0xFFC00000] = ldstEntry{"str", size}
		unscaledByBase[c.Unscaled&0xFFC00000] = ldstEntry{"stur", size}
	}
}

func scaleOf(size int) uint32 {
	c, ok := isaaarch64.LdClasses[size]
	if !ok {
		c = isaaarch64.StClasses[size]
	}
	return c.Scale
}

// sizeSuffix is this printer's own mnemonic-spelling convention (not an
// isa/aarch64 fact — LDR/STR's size field maps to b/h/<nothing> for
// byte/halfword/word-or-doubleword transfers).
func sizeSuffix(size int) string {
	switch size {
	case 1:
		return "b"
	case 2:
		return "h"
	}
	return ""
}

// csincBase is the general CSINC Rd,Rn,Rm,cond base word (Rn/Rm variable,
// unlike isa/aarch64.OpCSet, which fixes both to ZR for the CSET alias).
// isa/aarch64 doesn't name this general form separately since
// lower/aarch64's encoder only ever needs the two fixed specializations
// (OpCSet for cset, and this one — derived, not re-declared — for plain
// csinc); clearing OpCSet's baked-in Rn=Rm=ZR fields recovers it exactly.
var csincBase = isaaarch64.OpCSet &^ (0x1F << 16) &^ (0x1F << 5)

// andSPBase is AND (immediate), N=1, X-form, with Rn=Rd=SP baked in as
// zero (filled in by the caller) — lower/aarch64's encoder inlines this
// literal directly (isa/aarch64/encoder/encode.go's "and_sp" case) rather
// than naming it in isa/aarch64, the same bare-literal situation
// isa/x86_64's text package documents for 0x81/0xC0/0xC1/0xF7/0xFF.
const andSPBase uint32 = 0x92400000

// ---------------------------------------------------------------------------
// Decoder — exactly the lower/aarch64 encoding subset
// ---------------------------------------------------------------------------

func decodeWord(w uint32, pos int, fx map[int]aarch64.Fixup) (string, bool) {
	sf := w>>31 == 1

	// Fixed whole-word instructions.
	switch w {
	case isaaarch64.OpDmb:
		return "dmb ish", true
	case isaaarch64.OpClrex:
		return "clrex", true
	case isaaarch64.OpRet:
		return "ret", true
	}
	// NOP has no isa/aarch64 constant because lower/aarch64 never emits
	// one: asm.go's "nop" case lowers to zero instructions, so no NOP
	// byte pattern can ever reach this decoder.

	switch {
	case w&0xFFE0001F == isaaarch64.OpBrk&0xFFE0001F:
		return fmt.Sprintf("brk #%d", w>>5&0xFFFF), true
	case w&0xFFE0001F == isaaarch64.OpSVC&0xFFE0001F:
		return fmt.Sprintf("svc #%d", w>>5&0xFFFF), true

	case w&0xFFFFFC1F == isaaarch64.OpBLR&0xFFFFFC1F:
		return "blr " + regX(w>>5&0x1F, false), true
	case w&0xFFFFFC1F == isaaarch64.OpBR&0xFFFFFC1F:
		return "br " + regX(w>>5&0x1F, false), true

	// Move-wide: movz/movn/movk — X-form only, since lower/aarch64 never
	// needs a W-form 64-bit-immediate sequence (isa/aarch64/opcodes.go's
	// own doc comment on OpMovnX/OpMovzX/OpMovkX).
	case w&0xFF800000 == isaaarch64.OpMovnX&0xFF800000,
		w&0xFF800000 == isaaarch64.OpMovzX&0xFF800000,
		w&0xFF800000 == isaaarch64.OpMovkX&0xFF800000:
		op := "movz"
		switch w & 0xFF800000 {
		case isaaarch64.OpMovnX & 0xFF800000:
			op = "movn"
		case isaaarch64.OpMovkX & 0xFF800000:
			op = "movk"
		}
		rd, imm, hw := w&0x1F, w>>5&0xFFFF, w>>21&3
		s := fmt.Sprintf("%s %s, #0x%x", op, regX(rd, false), imm)
		if hw != 0 {
			s += fmt.Sprintf(", lsl #%d", hw*16)
		}
		if x, ok := fx[pos]; ok {
			s += "  // " + fixupRef(x)
		}
		return s, true

	// Branches.
	case w&0x7C000000 == isaaarch64.OpB&0x7C000000:
		op := "b"
		if w>>31 == 1 {
			op = "bl"
		}
		if x, ok := fx[pos]; ok {
			return op + " " + fixupRef(x), true
		}
		rel := int32(w<<6) >> 4 // sign-extended imm26 words -> bytes
		return fmt.Sprintf("%s 0x%x", op, pos+int(rel)), true
	case w&0xFF000010 == isaaarch64.OpBCond&0xFF000010:
		rel := int32(w<<8) >> 11 << 2
		return fmt.Sprintf("b.%s 0x%x", isaaarch64.CondNames[w&0xF], pos+int(rel)), true
	case w&0x7F000000 == isaaarch64.OpCBase&0x7F000000:
		op := "cbz"
		if w>>24&1 == 1 {
			op = "cbnz"
		}
		rel := int32(w<<8) >> 11 << 2
		return fmt.Sprintf("%s %s, 0x%x", op, reg(sf, w&0x1F, false), pos+int(rel)), true

	// Exclusives / acquire-release (size class in bits 31:30).
	case w&0x3FFFFC00 == isaaarch64.OpLdar&0x3FFFFC00:
		return fmt.Sprintf("ldar %s, [%s]", exclReg(w), regX(w>>5&0x1F, true)), true
	case w&0x3FFFFC00 == isaaarch64.OpStlr&0x3FFFFC00:
		return fmt.Sprintf("stlr %s, [%s]", exclReg(w), regX(w>>5&0x1F, true)), true
	case w&0x3FFFFC00 == isaaarch64.OpLdaxr&0x3FFFFC00:
		return fmt.Sprintf("ldaxr %s, [%s]", exclReg(w), regX(w>>5&0x1F, true)), true
	case w&0x3FA0FC00 == isaaarch64.OpStlxr&0x3FA0FC00:
		return fmt.Sprintf("stlxr %s, %s, [%s]", regW(w>>16&0x1F, false), exclReg(w), regX(w>>5&0x1F, true)), true

	// MUL/MSUB (this backend never emits general MADD with a nonzero Ra,
	// nor the general MUL-via-MADD alias detection an older revision of
	// this printer performed — "mul" and "msub" are their own directly
	// emitted Inst ops; see isa/aarch64/encoder/encode.go).
	case w&0xFFE0FC00 == isaaarch64.OpMul&0xFFE0FC00:
		return fmt.Sprintf("mul %s, %s, %s", reg(sf, w&0x1F, false), reg(sf, w>>5&0x1F, false), reg(sf, w>>16&0x1F, false)), true
	case w&0xFFE0FC00 == isaaarch64.OpMSub&0xFFE0FC00:
		return fmt.Sprintf("msub %s, %s, %s, %s", reg(sf, w&0x1F, false), reg(sf, w>>5&0x1F, false), reg(sf, w>>16&0x1F, false), reg(sf, w>>10&0x1F, false)), true
	case w&0xFFE0FC00 == isaaarch64.OpSMulH&0xFFE0FC00:
		return fmt.Sprintf("smulh %s, %s, %s", regX(w&0x1F, false), regX(w>>5&0x1F, false), regX(w>>16&0x1F, false)), true
	case w&0xFFE0FC00 == isaaarch64.OpUMulH&0xFFE0FC00:
		return fmt.Sprintf("umulh %s, %s, %s", regX(w&0x1F, false), regX(w>>5&0x1F, false), regX(w>>16&0x1F, false)), true
	case w&0xFFE0FC00 == isaaarch64.OpSMull&0xFFE0FC00:
		return fmt.Sprintf("smull %s, %s, %s", regX(w&0x1F, false), regW(w>>5&0x1F, false), regW(w>>16&0x1F, false)), true
	case w&0xFFE0FC00 == isaaarch64.OpUMull&0xFFE0FC00:
		return fmt.Sprintf("umull %s, %s, %s", regX(w&0x1F, false), regW(w>>5&0x1F, false), regW(w>>16&0x1F, false)), true

	// DP 2-source: udiv/sdiv/lslv/lsrv/asrv/rorv.
	case w&0x7FE0FC00 == isaaarch64.OpDP2Base&0x7FE0FC00:
		if nm, ok := dp2ByOpcode[w>>10&0x3F]; ok {
			return fmt.Sprintf("%s %s, %s, %s", displayDP2(nm), reg(sf, w&0x1F, false), reg(sf, w>>5&0x1F, false), reg(sf, w>>16&0x1F, false)), true
		}
		return "", false

	// DP 1-source: rbit/rev16/rev/clz.
	case dp1ByWord[w&0xFFFFFC00] != "":
		nm := dp1ByWord[w&0xFFFFFC00]
		return fmt.Sprintf("%s %s, %s", nm, reg(sf, w&0x1F, false), reg(sf, w>>5&0x1F, false)), true

	// mvn — ORN with Rn fixed to ZR. isa/aarch64.DPRegOpcodes has no
	// "orn" entry at all (lower/aarch64 never emits general ORN), so
	// unlike mov/neg below this can't fall out of the generic logical/
	// add-sub reverse-map lookup; it needs its own exact match against
	// the dedicated OpMvnReg/OpMvnRegX constants.
	case w&0xFFE0FFE0 == isaaarch64.OpMvnReg, w&0xFFE0FFE0 == isaaarch64.OpMvnRegX:
		return fmt.Sprintf("mvn %s, %s", reg(sf, w&0x1F, false), reg(sf, w>>16&0x1F, false)), true

	// csel / csinc (cset alias).
	case w&0x7FE00C00 == isaaarch64.OpCSel&0x7FE00C00:
		return fmt.Sprintf("csel %s, %s, %s, %s", reg(sf, w&0x1F, false), reg(sf, w>>5&0x1F, false), reg(sf, w>>16&0x1F, false), isaaarch64.CondNames[w>>12&0xF]), true
	case w&0x7FE00C00 == csincBase&0x7FE00C00:
		if w>>5&0x1F == 31 && w>>16&0x1F == 31 {
			return fmt.Sprintf("cset %s, %s", reg(sf, w&0x1F, false), isaaarch64.CondNames[isaaarch64.Invert(byte(w>>12&0xF))]), true
		}
		return fmt.Sprintf("csinc %s, %s, %s, %s", reg(sf, w&0x1F, false), reg(sf, w>>5&0x1F, false), reg(sf, w>>16&0x1F, false), isaaarch64.CondNames[w>>12&0xF]), true

	// Bitfield (ubfm/sbfm + aliases). Coarse family match ignores both sf
	// (bit31) and N (bit22): UBFM/SBFM's N bit architecturally always
	// equals sf, so folding them together is safe, and lets one mask
	// (0x7F800000) recognize both the W- and X-form base words
	// isa/aarch64.OpUBFMW/OpUBFMX and OpSBFMW/OpSBFMX name.
	case w&0x7F800000 == isaaarch64.OpUBFMW&0x7F800000 || w&0x7F800000 == isaaarch64.OpSBFMW&0x7F800000:
		signedBF := w&0x7F800000 == isaaarch64.OpSBFMW&0x7F800000
		immr, imms := w>>16&0x3F, w>>10&0x3F
		rd, rs := w&0x1F, w>>5&0x1F
		width := uint32(32)
		if sf {
			width = 64
		}
		switch {
		case immr == 0 && imms == 7 && !signedBF:
			return fmt.Sprintf("uxtb %s, %s", reg(sf, rd, false), regW(rs, false)), true
		case immr == 0 && imms == 15 && !signedBF:
			return fmt.Sprintf("uxth %s, %s", reg(sf, rd, false), regW(rs, false)), true
		case immr == 0 && imms == 7 && signedBF:
			return fmt.Sprintf("sxtb %s, %s", reg(sf, rd, false), regW(rs, false)), true
		case immr == 0 && imms == 15 && signedBF:
			return fmt.Sprintf("sxth %s, %s", reg(sf, rd, false), regW(rs, false)), true
		case immr == 0 && imms == 31 && signedBF && sf:
			return fmt.Sprintf("sxtw %s, %s", regX(rd, false), regW(rs, false)), true
		case imms == width-1 && !signedBF:
			return fmt.Sprintf("lsr %s, %s, #%d", reg(sf, rd, false), reg(sf, rs, false), immr), true
		case imms == width-1 && signedBF:
			return fmt.Sprintf("asr %s, %s, #%d", reg(sf, rd, false), reg(sf, rs, false), immr), true
		case !signedBF && imms+1 == immr:
			return fmt.Sprintf("lsl %s, %s, #%d", reg(sf, rd, false), reg(sf, rs, false), width-immr), true
		case !signedBF && immr == 0:
			return fmt.Sprintf("ubfx %s, %s, #0, #%d", reg(sf, rd, false), reg(sf, rs, false), imms+1), true
		case signedBF && immr == 0:
			return fmt.Sprintf("sbfx %s, %s, #0, #%d", reg(sf, rd, false), reg(sf, rs, false), imms+1), true
		}
		return "", false

	// AND (immediate), N=1, X-form, Rn=Rd=SP — the -2^k stack-alignment
	// mask and_sp emits (see andSPBase above). immr encodes k as
	// (64-k)&63, so this reconstructs the actual immediate rather than
	// leaving it as an opaque placeholder.
	case w&0xFFC00000 == andSPBase && w&0x1F == 31 && w>>5&0x1F == 31:
		immr := w >> 16 & 0x3F
		k := uint32(0)
		if immr != 0 {
			k = 64 - immr
		}
		mask := ^uint64(0) << k
		return fmt.Sprintf("and sp, sp, #0x%x", mask), true

	// Add/sub immediate. imm==0 with either operand SP is the MOV
	// (to/from SP) alias — decoded generically here rather than as two
	// hardcoded fixed words, so it recognizes the alias for any register
	// pair, not just the frame-pointer/SP case the prologue happens to
	// use.
	case dpImmByWord[w&0xFF800000] != "":
		nm := dpImmByWord[w&0xFF800000]
		imm := w >> 10 & 0xFFF
		shift := ""
		if w>>22&1 == 1 {
			shift = ", lsl #12"
		}
		rd, rs := w&0x1F, w>>5&0x1F
		if nm == "subs" && rd == 31 {
			return fmt.Sprintf("cmp %s, #%d%s", reg(sf, rs, false), imm, shift), true
		}
		if nm == "adds" && rd == 31 {
			return fmt.Sprintf("cmn %s, #%d%s", reg(sf, rs, false), imm, shift), true
		}
		dst, src := reg(sf, rd, true), reg(sf, rs, true) // imm form treats 31 as SP
		if nm == "add" && imm == 0 && shift == "" && (rd == 31 || rs == 31) {
			return fmt.Sprintf("mov %s, %s", dst, src), true
		}
		return fmt.Sprintf("%s %s, %s, #%d%s", nm, dst, src, imm, shift), true

	// Add/sub extended register (SP-capable forms).
	case w&0x7FE0FC00 == isaaarch64.OpAddExtX&0x7FE0FC00, w&0x7FE0FC00 == isaaarch64.OpSubExtX&0x7FE0FC00:
		nm := "add"
		if w&0x7FE0FC00 == isaaarch64.OpSubExtX&0x7FE0FC00 {
			nm = "sub"
		}
		return fmt.Sprintf("%s %s, %s, %s, uxtx", nm, regX(w&0x1F, true), regX(w>>5&0x1F, true), regX(w>>16&0x1F, false)), true

	// Register-offset byte transfers. Checked ahead of the generic
	// scaled/unscaled load-store lookup below: OpLdrbReg/OpStrbReg's
	// masked value collides with LdClasses[1].Unscaled/StClasses[1].
	// Unscaled under the coarser 0xFFC00000 mask, so the more specific
	// register-offset shape must win first.
	case w&0xFFE0FC00 == isaaarch64.OpLdrbReg&0xFFE0FC00:
		return fmt.Sprintf("ldrb %s, [%s, %s]", regW(w&0x1F, false), regX(w>>5&0x1F, true), regX(w>>16&0x1F, false)), true
	case w&0xFFE0FC00 == isaaarch64.OpStrbReg&0xFFE0FC00:
		return fmt.Sprintf("strb %s, [%s, %s]", regW(w&0x1F, false), regX(w>>5&0x1F, true), regX(w>>16&0x1F, false)), true

	// STP (pre-index)/LDP (post-index), 64-bit pair — decoded generically
	// from isa/aarch64.OpSTPPre64/OpLDPPost64 rather than matching only
	// the exact fp/lr/-16/16 words the frame prologue happens to use, so
	// any stp_pre/ldp_post the encoder emits decodes correctly.
	case w&0xFFC00000 == isaaarch64.OpSTPPre64&0xFFC00000:
		return pairText("stp", w, true), true
	case w&0xFFC00000 == isaaarch64.OpLDPPost64&0xFFC00000:
		return pairText("ldp", w, false), true

	// Logical / add-sub shifted register (plain, no shift emitted).
	case dpRegByWord[w&0xFFE00000] != "":
		nm := dpRegByWord[w&0xFFE00000]
		switch nm {
		case "add", "adds", "sub", "subs":
			return decodeAddSubReg(w, sf, nm)
		default: // and, orr, eor, bic
			return decodeLogicalReg(w, sf, nm)
		}

	// Scaled unsigned-offset loads/stores.
	case scaledByBase[w&0xFFC00000].op != "":
		e := scaledByBase[w&0xFFC00000]
		rt := reg(e.size == 8, w&0x1F, false)
		base := regX(w>>5&0x1F, true)
		off := (w >> 10 & 0xFFF) * scaleOf(e.size)
		op := e.op + sizeSuffix(e.size)
		if off == 0 {
			return fmt.Sprintf("%s %s, [%s]", op, rt, base), true
		}
		return fmt.Sprintf("%s %s, [%s, #%d]", op, rt, base, off), true

	// Unscaled (ldur/stur).
	case unscaledByBase[w&0xFFC00000].op != "":
		e := unscaledByBase[w&0xFFC00000]
		rt := reg(e.size == 8, w&0x1F, false)
		base := regX(w>>5&0x1F, true)
		disp := int32(w<<11) >> 23
		op := e.op
		if e.size < 4 {
			op += sizeSuffix(e.size)
		}
		if disp == 0 {
			return fmt.Sprintf("%s %s, [%s]", op, rt, base), true
		}
		return fmt.Sprintf("%s %s, [%s, #%d]", op, rt, base, disp), true
	}
	return "", false
}

func exclReg(w uint32) string {
	if w>>30&3 == 3 {
		return regX(w&0x1F, false)
	}
	return regW(w&0x1F, false)
}

func pairText(op string, w uint32, pre bool) string {
	rt := regX(w&0x1F, false)
	rn := regX(w>>5&0x1F, true)
	rt2 := regX(w>>10&0x1F, false)
	raw := int32(w >> 15 & 0x7F)
	if raw&0x40 != 0 {
		raw |= ^int32(0x7F)
	}
	disp := raw * 8
	if pre {
		return fmt.Sprintf("%s %s, %s, [%s, #%d]!", op, rt, rt2, rn, disp)
	}
	return fmt.Sprintf("%s %s, %s, [%s], #%d", op, rt, rt2, rn, disp)
}

// decodeLogicalReg covers and/orr/eor/bic, plus the mov alias (orr with
// Rn=ZR) — mvn's ORN-with-Rn=ZR counterpart has no generic "orn" entry to
// fall out of (see the dedicated OpMvnReg/OpMvnRegX case in decodeWord),
// but orr does appear in isa/aarch64.DPRegOpcodes, so mov falls out of
// this generic path exactly the way lower/aarch64's own encoder produces
// it (mov_r's base word is literally OpMovReg = the orr base with Rn
// fixed to ZR).
func decodeLogicalReg(w uint32, sf bool, nm string) (string, bool) {
	rd, rs, rm := w&0x1F, w>>5&0x1F, w>>16&0x1F
	if nm == "orr" && rs == 31 {
		return fmt.Sprintf("mov %s, %s", reg(sf, rd, false), reg(sf, rm, false)), true
	}
	return fmt.Sprintf("%s %s, %s, %s", nm, reg(sf, rd, false), reg(sf, rs, false), reg(sf, rm, false)), true
}

// decodeAddSubReg covers add/adds/sub/subs, plus the cmp/cmn (Rd=ZR) and
// neg (sub with Rn=ZR) aliases. neg's OpNegReg/OpNegRegX base word is
// likewise just the "sub" base with Rn fixed to ZR, so — like mov above —
// it falls out of this generic path rather than needing its own case.
func decodeAddSubReg(w uint32, sf bool, nm string) (string, bool) {
	rd, rs, rm := w&0x1F, w>>5&0x1F, w>>16&0x1F
	if nm == "subs" && rd == 31 {
		return fmt.Sprintf("cmp %s, %s", reg(sf, rs, false), reg(sf, rm, false)), true
	}
	if nm == "adds" && rd == 31 {
		return fmt.Sprintf("cmn %s, %s", reg(sf, rs, false), reg(sf, rm, false)), true
	}
	if nm == "sub" && rs == 31 {
		return fmt.Sprintf("neg %s, %s", reg(sf, rd, false), reg(sf, rm, false)), true
	}
	return fmt.Sprintf("%s %s, %s, %s", nm, reg(sf, rd, false), reg(sf, rs, false), reg(sf, rm, false)), true
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