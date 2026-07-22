package x86_64

// Condition codes (Intel tttn encoding: 0F 8x jcc, 0F 9x setcc, 0F 4x
// cmovcc all share this 4-bit space). Left as untyped constants — matching
// how both the encoder (Inst.CC byte) and the printer (a decoded ModRM/
// opcode nibble) use them as plain byte values.
const (
	CondO  = 0
	CondNO = 1
	CondB  = 2 // unsigned <  (carry set)
	CondAE = 3 // unsigned >= (carry clear)
	CondE  = 4
	CondNE = 5
	CondBE = 6 // unsigned <=
	CondA  = 7 // unsigned >
	CondS  = 8
	CondNS = 9
	CondP  = 10
	CondNP = 11
	CondL  = 12 // signed 
	CondGE = 13 // signed >=
	CondLE = 14 // signed <=
	CondG  = 15 // signed >
)

// Alternate spellings Intel documents for the same encodings. These are
// synonyms, not distinct codes — CondC and CondB are both 2 — and exist
// because the flag-oriented spelling reads better at some call sites: an
// overflow check after an add wants CondC, an unsigned relational compare
// wants CondB, and they encode identically.
const (
	CondC   = CondB
	CondNC  = CondAE
	CondZ   = CondE
	CondNZ  = CondNE
	CondNA  = CondBE
	CondNBE = CondA
	CondPE  = CondP
	CondPO  = CondNP
	CondNGE = CondL
	CondNL  = CondGE
	CondNG  = CondLE
	CondNLE = CondG
)

var condName = [16]string{
	"o", "no", "b", "ae", "e", "ne", "be", "a",
	"s", "ns", "p", "np", "l", "ge", "le", "g",
}

// CondName returns the canonical mnemonic suffix for a condition code
// (e.g. 4 -> "e", so jcc prints "je", setcc prints "sete", cmovcc prints
// "cmove"). Out-of-range input returns "?" rather than panicking, since
// the only callers are diagnostics.
func CondName(cc byte) string {
	if int(cc) >= len(condName) {
		return "?"
	}
	return condName[cc]
}

// NegateCond returns the code testing the complementary condition.
//
// This is an ISA fact rather than a lowering convenience: the tttn
// encoding deliberately pairs every condition with its negation in
// adjacent even/odd slots, so complementing a condition is a single bit
// flip on the encoding itself. Instruction selection needs it any time it
// inverts a two-way branch — rewriting `jcc then; jmp else` into
// `j<not cc> else` with a fallthrough to then.
func NegateCond(cc byte) byte { return cc ^ 1 }

var condByName map[string]byte

func init() {
	condByName = make(map[string]byte, len(condName))
	for i, n := range condName {
		condByName[n] = byte(i)
	}
}

// ParseCond resolves a canonical mnemonic suffix to its code. Only the
// canonical spellings in condName are accepted; the Go-level synonyms
// above are not a second textual vocabulary, and a disassembler that
// round-trips through ParseCond/CondName should get back what it started
// with.
func ParseCond(s string) (byte, bool) {
	cc, ok := condByName[s]
	return cc, ok
}