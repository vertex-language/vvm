package x86

// Condition codes (Intel tttn encoding: 0F 8x jcc, 0F 9x setcc, 0F 4x
// cmovcc all share this 4-bit space). Left as untyped constants — matching
// how both the encoder (Inst.CC byte) and the printer (a decoded ModRM/
// opcode nibble) use them as plain byte values.
const (
	CondO  = 0
	CondNO = 1
	CondB  = 2 // unsigned < (carry)
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

var condName = [16]string{
	"o", "no", "b", "ae", "e", "ne", "be", "a",
	"s", "ns", "p", "np", "l", "ge", "le", "g",
}

// CondName returns the mnemonic suffix for a condition code (e.g. 4 ->
// "e", so jcc prints "je", setcc prints "sete", cmovcc prints "cmove").
func CondName(cc byte) string { return condName[cc] }