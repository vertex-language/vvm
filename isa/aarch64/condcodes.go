package aarch64

// Condition-code field values, as they appear in B.cond, the
// CSET/CSEL/CSINC family, and CCMP/CCMN.
const (
	CondEQ byte = 0x0
	CondNE byte = 0x1
	CondHS byte = 0x2 // unsigned >=  (carry set)
	CondLO byte = 0x3 // unsigned <   (carry clear)
	CondMI byte = 0x4
	CondPL byte = 0x5
	CondVS byte = 0x6 // signed overflow
	CondVC byte = 0x7
	CondHI byte = 0x8 // unsigned >
	CondLS byte = 0x9 // unsigned <=
	CondGE byte = 0xA
	CondLT byte = 0xB
	CondGT byte = 0xC
	CondLE byte = 0xD
	CondAL byte = 0xE
	CondNV byte = 0xF // second "always" encoding; A64 asm never emits it
)

// CondNames gives the mnemonic suffix for each condition-code value — a
// disassembler, a printer, or an inline-asm parser would all want this
// same correspondence.
var CondNames = [16]string{
	CondEQ: "eq", CondNE: "ne", CondHS: "hs", CondLO: "lo",
	CondMI: "mi", CondPL: "pl", CondVS: "vs", CondVC: "vc",
	CondHI: "hi", CondLS: "ls", CondGE: "ge", CondLT: "lt",
	CondGT: "gt", CondLE: "le", CondAL: "al", CondNV: "nv",
}

// CondMnemonics is the reverse index (mnemonic suffix -> condition-code
// value), built once at init so an inline-asm dialect parser reads it
// instead of hand-typing a second copy of the same table (the exact
// duplication isa/x86_64's README calls out fixing for its own jccTable).
var CondMnemonics = map[string]byte{}

func init() {
	for cc, name := range CondNames {
		CondMnemonics[name] = byte(cc)
	}
}

// Invert returns the complementary condition. This is a fixed
// architectural pairing (bit 0 of the 4-bit field toggles sense for every
// pair below AL/NV), not a policy choice — AL and NV have no complement
// and are returned unchanged.
func Invert(cc byte) byte {
	if cc >= CondAL {
		return cc
	}
	return cc ^ 1
}