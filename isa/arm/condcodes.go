package arm

// Condition codes (A32 cond field, bits 31:28 of every conditionally
// executed instruction word). Left as untyped constants — matching how
// both the encoder (Inst.CC byte) and a future decoder (a fetched word's
// top nibble) use them as plain byte values.
const (
	CondEQ = 0x0
	CondNE = 0x1
	CondHS = 0x2 // unsigned >= (carry set)
	CondLO = 0x3 // unsigned <  (carry clear)
	CondMI = 0x4
	CondPL = 0x5
	CondVS = 0x6
	CondVC = 0x7
	CondHI = 0x8 // unsigned >
	CondLS = 0x9 // unsigned <=
	CondGE = 0xA
	CondLT = 0xB
	CondGT = 0xC
	CondLE = 0xD
	CondAL = 0xE // always (unconditional)
)

var condName = [16]string{
	"eq", "ne", "hs", "lo", "mi", "pl", "vs", "vc",
	"hi", "ls", "ge", "lt", "gt", "le", "al", "",
}

// CondName returns the mnemonic suffix for a condition code (e.g. 0x0 ->
// "eq", so a conditional branch prints "beq", a conditional move "moveq").
func CondName(cc byte) string { return condName[cc] }