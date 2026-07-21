// Package encoder is the generic A64 assembler: it turns an instruction
// stream built from isa/aarch64's static facts into machine words. It has
// no vir knowledge, no OS knowledge, no frame/ABI knowledge, and no
// automatic prologue/epilogue splicing — a function's frame setup and
// teardown are ordinary instructions (stp_pre/mov_r_sp/sub_sp/mov_to_sp/
// ldp_post/ret) that lower/aarch64 emits into the stream like any other
// instruction. See the package README for the full rationale.
package encoder

import isaaarch64 "github.com/vertex-language/vvm/isa/aarch64"

// Reg is re-exported from isa/aarch64 so a caller building an Inst stream
// can write encoder.X0, encoder.FP, etc. without a second, parallel
// register-constant declaration.
type Reg = isaaarch64.Reg

const (
	X0  = isaaarch64.X0
	X1  = isaaarch64.X1
	X2  = isaaarch64.X2
	X3  = isaaarch64.X3
	X4  = isaaarch64.X4
	X5  = isaaarch64.X5
	X6  = isaaarch64.X6
	X7  = isaaarch64.X7
	X8  = isaaarch64.X8
	X9  = isaaarch64.X9
	X10 = isaaarch64.X10
	X11 = isaaarch64.X11
	X12 = isaaarch64.X12
	X13 = isaaarch64.X13
	X14 = isaaarch64.X14
	X15 = isaaarch64.X15
	X16 = isaaarch64.X16
	X17 = isaaarch64.X17
	X18 = isaaarch64.X18
	X19 = isaaarch64.X19
	X20 = isaaarch64.X20
	X21 = isaaarch64.X21
	X22 = isaaarch64.X22
	X23 = isaaarch64.X23
	X24 = isaaarch64.X24
	X25 = isaaarch64.X25
	X26 = isaaarch64.X26
	X27 = isaaarch64.X27
	X28 = isaaarch64.X28
	X29 = isaaarch64.X29
	X30 = isaaarch64.X30
	SP  = isaaarch64.SP
	ZR  = isaaarch64.ZR
	FP  = isaaarch64.FP
	LR  = isaaarch64.LR
	IP0 = isaaarch64.IP0
	IP1 = isaaarch64.IP1
	PR  = isaaarch64.PR
)

// Condition codes, likewise re-exported.
const (
	CondEQ = isaaarch64.CondEQ
	CondNE = isaaarch64.CondNE
	CondHS = isaaarch64.CondHS
	CondLO = isaaarch64.CondLO
	CondMI = isaaarch64.CondMI
	CondPL = isaaarch64.CondPL
	CondVS = isaaarch64.CondVS
	CondVC = isaaarch64.CondVC
	CondHI = isaaarch64.CondHI
	CondLS = isaaarch64.CondLS
	CondGE = isaaarch64.CondGE
	CondLT = isaaarch64.CondLT
	CondGT = isaaarch64.CondGT
	CondLE = isaaarch64.CondLE
	CondAL = isaaarch64.CondAL
	CondNV = isaaarch64.CondNV
)

// Invert re-exports isa/aarch64.Invert for convenience at Inst-stream call
// sites.
func Invert(cc byte) byte { return isaaarch64.Invert(cc) }

// OprKind discriminates an Opr's payload. Strictly post-register-
// allocation: register, immediate/symbol, or a concrete memory operand.
// There is no "not yet placed" kind here — that's lower/aarch64's own Opr
// type's job to add, the same boundary isa/x86's, isa/x86_64's, and
// isa/arm's encoders draw against their own lowering packages' OSlot/KSlot.
type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm // immediate; Sym != "" makes it a symbol address (+Imm addend)
	OMem // [Base + Disp]
)

type Opr struct {
	Kind OprKind
	Reg  Reg
	Imm  int64
	Sym  string
	Base Reg
	Disp int32
}

func R(r Reg) Opr            { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr        { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr   { return Opr{Kind: OImm, Sym: s} }
func Mem(b Reg, d int32) Opr { return Opr{Kind: OMem, Base: b, Disp: d} }

// Inst is one pre-encoding pseudo-instruction. Sz selects the W (4) or X
// (8) form of an operation where both exist; for loads/stores/exclusive
// ops Sz is the access size in bytes (1/2/4/8). D/S/T/X follow each op's
// own convention (documented alongside its case in encode.go); for
// stp_pre/ldp_post, D is the base+displacement memory operand and S/T are
// the register pair (Rt/Rt2).
type Inst struct {
	Op         string
	D, S, T, X Opr
	Cc         byte
	Sz         int
	Lbl        string
	Sym        string
	Imm        int64
}

// FixupKind is this package's vocabulary for what kind of hole a Fixup
// patches — shared by anything downstream that resolves symbols.
type FixupKind int

const (
	// FixupCall26: BL — imm26 := ((S + A - P) >> 2) & 0x3FFFFFF, A = 0.
	FixupCall26 FixupKind = iota
	// FixupJump26: B — same field arithmetic as FixupCall26 (tailcalls).
	FixupJump26
	// FixupMovzG3: MOVZ — imm16 := ((S + A) >> 48) & 0xFFFF (checking form).
	FixupMovzG3
	// FixupMovkG2: MOVK — imm16 := ((S + A) >> 32) & 0xFFFF (no check).
	FixupMovkG2
	// FixupMovkG1: MOVK — imm16 := ((S + A) >> 16) & 0xFFFF (no check).
	FixupMovkG1
	// FixupMovkG0: MOVK — imm16 := (S + A) & 0xFFFF (no check).
	FixupMovkG0
	// FixupAbs64: plain 64-bit data word := S + A (global initializers).
	FixupAbs64
)

func (k FixupKind) String() string {
	switch k {
	case FixupCall26:
		return "call26"
	case FixupJump26:
		return "jump26"
	case FixupMovzG3:
		return "movz_uabs_g3"
	case FixupMovkG2:
		return "movk_uabs_g2"
	case FixupMovkG1:
		return "movk_uabs_g1"
	case FixupMovkG0:
		return "movk_uabs_g0"
	case FixupAbs64:
		return "abs64"
	}
	return "fixup?"
}

// Fixup is a hole in a byte stream that a downstream consumer must
// resolve.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}