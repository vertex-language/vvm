// Package x86_64 is the static, data-only description of the x86-64
// (AMD64 / Intel 64, "long mode") instruction set: register identity,
// condition codes, ModRM/SIB layout, and the opcode<->mnemonic
// correspondence, with no control flow of consequence.
//
// x86-64 is not a relabeling of 32-bit x86. It keeps the legacy encoding as
// its substrate (the ModRM/SIB byte layout, the ALU opcode bytes, and the
// tttn condition encoding are unchanged) and extends it: sixteen GPRs
// instead of eight, a REX prefix carrying the extra register bits and the
// 64-bit operand-size selector, RIP-relative addressing, and a 64-bit
// immediate mov. Those extensions are what this package exists to pin
// down. See rex.go for the prefix and registers.go for the REX-dependent
// byte-register irregularity described below.
package x86_64

// Reg is a physical x86-64 general-purpose register. Values 0-15 are the
// sixteen GPRs in ModRM/SIB encoding order (low 3 bits in the ModRM/SIB
// field, high bit in a REX R/X/B slot); RNone is the "absent" sentinel for
// optional base/index operands and is never encodable.
type Reg byte

const (
	RRAX Reg = 0
	RRCX Reg = 1
	RRDX Reg = 2
	RRBX Reg = 3
	RRSP Reg = 4
	RRBP Reg = 5
	RRSI Reg = 6
	RRDI Reg = 7
	RR8  Reg = 8
	RR9  Reg = 9
	RR10 Reg = 10
	RR11 Reg = 11
	RR12 Reg = 12
	RR13 Reg = 13
	RR14 Reg = 14
	RR15 Reg = 15
	RNone Reg = 0xFF
)

// NumGPR is the number of encodable general-purpose registers in long
// mode. Reaching r8-r15 (indices 8-15) requires a REX prefix bit; the low
// three bits go in the ModRM/SIB field and the fourth in REX.R/X/B.
const NumGPR = 16

// IsGPR reports whether r names one of the sixteen encodable GPRs — i.e.
// whether r may legally appear in a ModRM reg/rm or SIB base/index field.
// RNone, and any other out-of-range value, reports false.
func (r Reg) IsGPR() bool { return int(r) < NumGPR }

// NeedsREXBit reports whether encoding r requires setting a REX R/X/B bit,
// i.e. whether it is one of the extended registers r8-r15. An operand
// using any such register forces a REX prefix onto the instruction even
// when REX.W is not otherwise needed.
func (r Reg) NeedsREXBit() bool { return r.IsGPR() && r >= RR8 }

// Low3 returns the low three bits of r's encoding — the value that goes in
// a ModRM reg/rm or SIB base/index field. The fourth bit (r >= 8) is
// carried separately in a REX slot; see rex.go.
func (r Reg) Low3() byte { return byte(r) & 7 }

// reg64/reg32/reg16 are the width-indexed name tables for the sixteen GPR
// encodings. Byte naming is handled separately by NameByte, because it is
// REX-dependent in a way a flat table cannot express (see ByteAddressable).
var reg64 = [NumGPR]string{
	"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi",
	"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
}
var reg32 = [NumGPR]string{
	"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi",
	"r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d",
}
var reg16 = [NumGPR]string{
	"ax", "cx", "dx", "bx", "sp", "bp", "si", "di",
	"r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w",
}

// The two byte-register spellings for indices 0-7. Which one applies is
// not a property of the register alone but of whether the instruction
// carries a REX prefix — see ByteAddressable and NameByte.
var reg8NoREX = [8]string{"al", "cl", "dl", "bl", "ah", "ch", "dh", "bh"}
var reg8REX = [16]string{
	"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil",
	"r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b",
}

// Name returns r's assembly spelling at the given operand width in bits
// (16, 32, or 64; anything else defaults to 64). For an 8-bit operand use
// NameByte instead, because the correct byte spelling depends on whether a
// REX prefix is present, which this signature can't carry.
//
// Non-GPR values — RNone above all — return "?" rather than panicking.
// This is a diagnostic path, and a caller asking to name an absent
// register should get "there isn't one," not a crash.
func (r Reg) Name(widthBits int) string {
	if !r.IsGPR() {
		return "?"
	}
	switch widthBits {
	case 16:
		return reg16[r]
	case 32:
		return reg32[r]
	}
	return reg64[r]
}

// NameByte returns r's 8-bit spelling. rex reports whether the instruction
// carries a REX prefix, because that flips the naming of indices 4-7: with
// a REX prefix they are the uniform low bytes spl/bpl/sil/dil, and without
// one they are the high bytes ah/ch/dh/bh. r8b-r15b (indices 8-15) only
// exist with REX and return "?" when rex is false.
func (r Reg) NameByte(rex bool) string {
	if !r.IsGPR() {
		return "?"
	}
	if rex {
		return reg8REX[r]
	}
	if r >= RR8 {
		return "?"
	}
	return reg8NoREX[r]
}

// ByteAddressable reports whether r has a byte spelling reachable under the
// given REX condition. This is the fact that inverts relative to 32-bit
// mode: there, indices 4-7 are always the high bytes ah/ch/dh/bh and a
// byte operand on esi/edi/etc. is simply unrepresentable. In long mode a
// REX prefix reclassifies 4-7 as spl/bpl/sil/dil, so the two cases trade
// places —
//
//   - without REX: al/cl/dl/bl (0-3) and ah/ch/dh/bh (4-7) are byte
//     spellings; r8b-r15b are not reachable.
//   - with REX: al/cl/dl/bl, spl/bpl/sil/dil, and r8b-r15b are all byte
//     spellings, but ah/ch/dh/bh become unencodable.
//
// The consequence an emitter must honor: a byte operand on spl/bpl/sil/dil
// *requires* a REX prefix, and a byte operand on ah/ch/dh/bh *forbids*
// one. The two can never appear in the same instruction.
func (r Reg) ByteAddressable(rex bool) bool {
	if !r.IsGPR() {
		return false
	}
	if rex {
		return true // every GPR has a byte spelling once REX is present
	}
	return r < RR8 // al..bl, ah..bh
}

// String is the width-free 64-bit spelling, for diagnostics that don't
// care about operand width.
func (r Reg) String() string { return r.Name(64) }