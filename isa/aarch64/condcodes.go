package aarch64

// Condition codes. A64 shares A32's 4-bit tttn condition encoding, but not
// its universality: only B.cond, the conditional-select family
// (csel/csinc/cset/...), and the conditional-compare family (ccmp/ccmn)
// read a condition. So these are a per-instruction nibble placed where each
// such encoding puts it (bits 3:0 of B.cond, bits 15:12 of the
// select/compare forms), not a field on the whole instruction set the way
// A32's are. Left as untyped constants — an encoder places the nibble and a
// printer decodes it back, both as plain byte values.
const (
	CondEQ = 0  // Z set                 — equal
	CondNE = 1  // Z clear               — not equal
	CondCS = 2  // C set                 — unsigned higher or same
	CondCC = 3  // C clear               — unsigned lower
	CondMI = 4  // N set                 — negative
	CondPL = 5  // N clear               — positive or zero
	CondVS = 6  // V set                 — overflow
	CondVC = 7  // V clear               — no overflow
	CondHI = 8  // C set and Z clear     — unsigned higher
	CondLS = 9  // C clear or Z set      — unsigned lower or same
	CondGE = 10 // N == V                — signed >=
	CondLT = 11 // N != V                — signed 
	CondGT = 12 // Z clear and N == V    — signed >
	CondLE = 13 // Z set or N != V       — signed <=
	CondAL = 14 // always
	CondNV = 15 // "never" spelling; see below
)

// CondNV (0b1111) is not a usable condition. Historically "never," in A64 it
// behaves identically to AL (always) rather than never executing — but it
// is not the canonical spelling of "always" and assemblers do not emit it.
// It is named here so a decoder can recognize the value it reads, not so an
// encoder can produce it. Canonical "always" is CondAL.
const (
	// Documented synonyms for the two carry-flag codes. Synonyms, not
	// distinct codes — CondHS and CondCS are both 2 — reading better where
	// the carry is an unsigned-comparison result ("higher or same"/"lower")
	// than a raw carry.
	CondHS = CondCS // unsigned higher or same (carry set)
	CondLO = CondCC // unsigned lower          (carry clear)
)

var condName = [16]string{
	"eq", "ne", "cs", "cc", "mi", "pl", "vs", "vc",
	"hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
}

// CondName returns the canonical two-letter mnemonic for a condition code
// (e.g. 0 -> "eq", so "b.eq"). Out-of-range input returns "?" rather than
// panicking, since the only callers are diagnostics. "al" is returned for
// CondAL and "nv" for CondNV even though assemblers normally omit the former
// and never emit the latter — a disassembler still has to name whatever
// nibble it decoded.
func CondName(cc byte) string {
	if int(cc) >= len(condName) {
		return "?"
	}
	return condName[cc]
}

// NegateCond returns the code testing the complementary condition. Like
// A32/x86 tttn, adjacent even/odd slots are negations, so complementing is a
// single bit flip: eq(0)/ne(1), cs(2)/cc(3), ..., gt(12)/le(13). An
// instruction selector uses it when inverting a two-way B.cond.
//
// The final pair is al(14)/nv(15), so NegateCond(CondAL) is CondNV. That is
// harmless in practice — inverting a branch only arises for a *conditional*
// branch, and an unconditional (AL) branch has no negation — but a caller
// that might feed AL in should know the result is the NV spelling, which
// behaves as "always," not a real "never."
func NegateCond(cc byte) byte { return cc ^ 1 }

var condByName map[string]byte

func init() {
	condByName = make(map[string]byte, len(condName)+2)
	for i, n := range condName {
		condByName[n] = byte(i)
	}
	// The synonyms resolve too, but CondName emits cs/cc, so a round-trip
	// through ParseCond/CondName is stable.
	condByName["hs"] = CondHS
	condByName["lo"] = CondLO
}

// ParseCond resolves a condition mnemonic to its code. The canonical
// spellings plus the hs/lo synonyms are accepted; a round-trip back through
// CondName yields the canonical cs/cc spelling.
func ParseCond(s string) (byte, bool) {
	cc, ok := condByName[s]
	return cc, ok
}