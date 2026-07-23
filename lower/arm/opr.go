// opr.go
package arm

import (
	"fmt"

	isaarm "github.com/vertex-language/vvm/isa/arm"
	"github.com/vertex-language/vvm/isa/arm/encoder"
)

// Register identity and condition selectors come from the ISA/encoder
// packages; this package never redeclares them.
type (
	Reg  = isaarm.Reg
	Cond = encoder.Cond
)

const (
	R0 = isaarm.R0
	R1 = isaarm.R1
	R2 = isaarm.R2
	R3 = isaarm.R3

	// FP is the frame pointer. A32 has no architectural frame pointer;
	// r11 is the AAPCS convention and every local slot in this backend is
	// addressed off it, so an alloca that moves sp cannot invalidate a
	// slot reference.
	FP = isaarm.R11
	// IP (r12) is the AAPCS intra-procedure scratch register: caller-saved
	// and never an argument register, so it is the one register safe to
	// clobber while argument registers are already loaded.
	IP    = isaarm.R12
	SP    = isaarm.RSP
	LR    = isaarm.RLR
	RNone = isaarm.RNone
)

const (
	AL = encoder.AL
	EQ = encoder.EQ
	NE = encoder.NE
	HS = encoder.HS
	LO = encoder.LO
	MI = encoder.MI
	VS = encoder.VS
	HI = encoder.HI
	LS = encoder.LS
	GE = encoder.GE
	LT = encoder.LT
	GT = encoder.GT
	LE = encoder.LE
)

const (
	LSL = isaarm.ShiftLSL
	LSR = isaarm.ShiftLSR
	ASR = isaarm.ShiftASR
	ROR = isaarm.ShiftROR
)

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm
	OMem
	ORegList
	// OSlot is a named IR value's frame slot. It is the one operand kind
	// with no encoder equivalent — encode.go resolves it to [fp, #off]
	// once BuildFrame has assigned offsets.
	OSlot
)

// Opr mirrors encoder.Opr, plus OSlot. Always build one with a
// constructor: OReg and OMem carry RNone sentinels that a zero value gets
// wrong (RNone is 0xFF, but R0 is 0).
type Opr struct {
	Kind OprKind

	Reg Reg
	Imm int64
	Sym string

	Shift    byte
	ShiftAmt byte
	ShiftReg Reg

	Base  Reg
	Index Reg
	Disp  int32
	Add   bool
	Pre   bool
	Wback bool

	Slot string // OSlot: the IR value name
}

func R(r Reg) Opr     { return Opr{Kind: OReg, Reg: r, ShiftReg: RNone} }
func Imm(v int64) Opr { return Opr{Kind: OImm, Imm: v} }

func RShift(r Reg, shift, amt byte) Opr {
	return Opr{Kind: OReg, Reg: r, Shift: shift, ShiftAmt: amt, ShiftReg: RNone}
}

func RShiftReg(r Reg, shift byte, rs Reg) Opr {
	return Opr{Kind: OReg, Reg: r, Shift: shift, ShiftReg: rs}
}

func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

func Mem(base Reg, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Pre: true}
}

func MemPre(base Reg, disp int32, wback bool) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Pre: true, Wback: wback}
}

func MemPost(base Reg, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp}
}

func RegList(regs ...Reg) Opr {
	var mask uint16
	for _, r := range regs {
		if r.IsGPR() {
			mask |= 1 << r.Field()
		}
	}
	return Opr{Kind: ORegList, Imm: int64(mask)}
}

func Slot(name string) Opr { return Opr{Kind: OSlot, Slot: name} }

// Inst mirrors encoder.Inst, plus the three epilogue pseudo-ops expanded
// in encode.go: epi_ret, epi_jmp_sym (tailcall to a symbol) and epi_jmp_r
// (tailcall through a register).
type Inst struct {
	Op  string
	CC  Cond
	S   bool
	Wb  bool
	D   Opr
	N   Opr
	M   Opr
	A   Opr
	Lbl string
	Sym string
	Imm int64
}

// toEncoderOpr converts explicitly rather than by numeric cast, even
// though the two OprKind enums are currently declared in the same order.
// OSlot has no encoder counterpart; if either enum gains a case, this
// switch fails loudly instead of silently reinterpreting it.
func toEncoderOpr(o Opr) (encoder.Opr, error) {
	switch o.Kind {
	case ONone:
		return encoder.Opr{}, nil
	case OReg:
		return encoder.Opr{
			Kind: encoder.OReg, Reg: o.Reg,
			Shift: o.Shift, ShiftAmt: o.ShiftAmt, ShiftReg: o.ShiftReg,
		}, nil
	case OImm:
		return encoder.Opr{Kind: encoder.OImm, Imm: o.Imm, Sym: o.Sym}, nil
	case OMem:
		return encoder.Opr{
			Kind: encoder.OMem, Base: o.Base, Index: o.Index, Disp: o.Disp,
			Shift: o.Shift, ShiftAmt: o.ShiftAmt,
			Add: o.Add, Pre: o.Pre, Wback: o.Wback,
		}, nil
	case ORegList:
		return encoder.Opr{Kind: encoder.ORegList, Imm: o.Imm}, nil
	case OSlot:
		return encoder.Opr{}, fmt.Errorf("unresolved slot operand %q reached the encoder", o.Slot)
	}
	return encoder.Opr{}, fmt.Errorf("unknown operand kind %d", o.Kind)
}

func toEncoderInst(in Inst) (encoder.Inst, error) {
	var out encoder.Inst
	var err error
	if out.D, err = toEncoderOpr(in.D); err != nil {
		return out, err
	}
	if out.N, err = toEncoderOpr(in.N); err != nil {
		return out, err
	}
	if out.M, err = toEncoderOpr(in.M); err != nil {
		return out, err
	}
	if out.A, err = toEncoderOpr(in.A); err != nil {
		return out, err
	}
	out.Op, out.CC, out.S, out.Wb = in.Op, in.CC, in.S, in.Wb
	out.Lbl, out.Sym, out.Imm = in.Lbl, in.Sym, in.Imm
	return out, nil
}