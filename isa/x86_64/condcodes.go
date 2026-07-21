package x86_64

// Condition-code constants (Intel tttn encoding): the same 4-bit field
// numbers Jcc (0F 8x), SETcc (0F 9x), and CMOVcc (0F 4x) all share.
const (
	CondO  = 0
	CondNO = 1
	CondB  = 2 // unsigned <  (carry)
	CondAE = 3
	CondE  = 4
	CondNE = 5
	CondBE = 6
	CondA  = 7
	CondS  = 8
	CondNS = 9
	CondP  = 10
	CondNP = 11
	CondL  = 12 // signed 
	CondGE = 13
	CondLE = 14
	CondG  = 15
)

// CondMnemonics maps every mnemonic-suffix spelling assemblers accept —
// including the redundant aliases (jc/jnae both mean CondB, jz/je are
// both CondE) — to its condition-code number.
//
// Before this package existed, lower/x86_64/inlineasm/common.go kept this
// exact table (jccTable) as its own unexported copy, purely to parse
// jCC/setCC/cmovCC mnemonics — duplicating a fact the encoder already
// depended on, with nothing to catch the two drifting apart.
var CondMnemonics = map[string]byte{
	"o": CondO, "no": CondNO,
	"b": CondB, "c": CondB, "nae": CondB,
	"ae": CondAE, "nb": CondAE, "nc": CondAE,
	"e": CondE, "z": CondE, "ne": CondNE, "nz": CondNE,
	"be": CondBE, "na": CondBE, "a": CondA, "nbe": CondA,
	"s": CondS, "ns": CondNS,
	"p": CondP, "pe": CondP, "np": CondNP, "po": CondNP,
	"l": CondL, "nge": CondL, "ge": CondGE, "nl": CondGE,
	"le": CondLE, "ng": CondLE, "g": CondG, "nle": CondG,
}

// CondName is the canonical (non-aliased) mnemonic suffix for each
// condition code — the inverse direction of CondMnemonics, for anything
// that prints a jCC/setCC/cmovCC mnemonic rather than parses one.
var CondName = map[byte]string{
	CondO: "o", CondNO: "no", CondB: "b", CondAE: "ae",
	CondE: "e", CondNE: "ne", CondBE: "be", CondA: "a",
	CondS: "s", CondNS: "ns", CondP: "p", CondNP: "np",
	CondL: "l", CondGE: "ge", CondLE: "le", CondG: "g",
}