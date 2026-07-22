package arm

// Condition codes. Bits 31:28 of almost every A32 instruction hold one of
// these 4-bit values; the instruction executes only if the encoded
// condition holds against the CPSR N/Z/C/V flags. Left as untyped
// constants — matching how both an encoder (an Inst.CC byte placed into the
// top nibble) and a printer (that nibble decoded back out) use them as
// plain byte values.
const (
	CondEQ = 0  // Z set                    — equal
	CondNE = 1  // Z clear                  — not equal
	CondCS = 2  // C set                    — unsigned higher or same
	CondCC = 3  // C clear                  — unsigned lower
	CondMI = 4  // N set                    — negative
	CondPL = 5  // N clear                  — positive or zero
	CondVS = 6  // V set                    — overflow
	CondVC = 7  // V clear                  — no overflow
	CondHI = 8  // C set and Z clear        — unsigned higher
	CondLS = 9  // C clear or Z set         — unsigned lower or same
	CondGE = 10 // N == V                   — signed >=
	CondLT = 11 // N != V                   — signed 
	CondGT = 12 // Z clear and N == V       — signed >
	CondLE = 13 // Z set or N != V          — signed <=
	CondAL = 14 // always
	CondNV = 15 // "never" historically; see below
)

// CondNV (0b1111) is not a usable condition. On the earliest ARMs it meant
// "never execute"; from ARMv5 the architecture reclaimed the entire
// 0b1111 condition space to encode *unconditional* instructions (BLX
// immediate, PLD, and the Advanced SIMD / memory-hint groups), so a normal
// instruction carrying 0b1111 in its condition field is not "never" but a
// different instruction altogether — or UNPREDICTABLE. It is named here so
// a decoder can recognize the value, not so an encoder can emit it as a
// condition.
const (
	// Alternate spellings the architecture documents for two of the codes.
	// These are synonyms, not distinct codes — CondHS and CondCS are both
	// 2 — and read better where the carry flag is being used as an
	// unsigned-comparison result ("higher or same" / "lower") rather than
	// as a raw carry.
	CondHS = CondCS // unsigned higher or same (carry set)
	CondLO = CondCC // unsigned lower          (carry clear)
)

var condName = [16]string{
	"eq", "ne", "cs", "cc", "mi", "pl", "vs", "vc",
	"hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
}

// CondName returns the canonical two-letter mnemonic suffix for a condition
// code (e.g. 0 -> "eq", so a conditional add prints "addeq"). Out-of-range
// input returns "?" rather than panicking, since the only callers are
// diagnostics. Note "al" is returned for CondAL even though assemblers
// normally omit it, and "nv" for CondNV even though it is not an emittable
// condition — a disassembler still has to name whatever nibble it decoded.
func CondName(cc byte) string {
	if int(cc) >= len(condName) {
		return "?"
	}
	return condName[cc]
}

// NegateCond returns the code testing the complementary condition.
//
// Like x86's tttn encoding, the condition field deliberately pairs every
// condition with its negation in adjacent even/odd slots, so complementing
// is a single bit flip: eq(0)/ne(1), cs(2)/cc(3), ..., gt(12)/le(13). An
// instruction selector uses it whenever it inverts a two-way branch.
//
// The one wrinkle A32 has and x86 does not: the final pair is al(14)/nv(15),
// so NegateCond(CondAL) is CondNV, which is not a usable condition (see
// CondNV). This is harmless in practice — inverting a branch only arises
// for a *conditional* branch, and an unconditional (AL) branch has no
// negation to take — but callers that might feed AL in should know the
// result is the unconditional-instruction escape value, not a real "never".
func NegateCond(cc byte) byte { return cc ^ 1 }

var condByName map[string]byte

func init() {
	condByName = make(map[string]byte, len(condName))
	for i, n := range condName {
		condByName[n] = byte(i)
	}
	// The documented synonyms resolve too, but Name emits the canonical
	// cs/cc spellings, so a round-trip through ParseCond/CondName is
	// stable.
	condByName["hs"] = CondHS
	condByName["lo"] = CondLO
}

// ParseCond resolves a mnemonic suffix to its code. The canonical spellings
// in condName plus the hs/lo synonyms are accepted; a round-trip back
// through CondName yields the canonical cs/cc spelling.
func ParseCond(s string) (byte, bool) {
	cc, ok := condByName[s]
	return cc, ok
}