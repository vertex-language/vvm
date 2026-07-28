package amdtx

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Operand is anything that can occupy an instruction operand position. The
// interface is closed: only this package can implement it.
type Operand interface {
	Text() string
	operand()
}

// Addressable is an operand that can be the base of a memory operand.
type Addressable interface {
	Operand
	addressable()
}

// Imm is an integer immediate. It prints in decimal when it fits an inline
// constant slot and in eight-digit lowercase hex otherwise, which is
// canonical-printer rule 5 stated once, here.
type Imm int64

// IsInline reports whether the value occupies an inline constant slot:
// integers -16 through 64, free of instruction-stream space (§8.1).
func (i Imm) IsInline() bool { return i >= -16 && i <= 64 }

// Fits32 reports whether the value is representable as a single literal
// dword, either signed or unsigned.
func (i Imm) Fits32() bool {
	v := int64(i)
	return v >= math.MinInt32 && v <= math.MaxUint32
}

func (i Imm) Text() string {
	if i.IsInline() {
		return strconv.FormatInt(int64(i), 10)
	}
	if i.Fits32() {
		return fmt.Sprintf("0x%08x", uint32(int64(i)))
	}
	return fmt.Sprintf("0x%016x", uint64(i))
}
func (Imm) operand() {}

// FImm is a floating-point immediate. Inline floats print in canonical
// form; everything else prints in shortest round-trip form (rule 7).
type FImm float64

// Inv2Pi is the 1/(2π) inline constant available on GFX9 and later.
const Inv2Pi = FImm(0.15915494309189535)

var inlineFloats = map[FImm]string{
	0.5: "0.5", -0.5: "-0.5",
	1.0: "1.0", -1.0: "-1.0",
	2.0: "2.0", -2.0: "-2.0",
	4.0: "4.0", -4.0: "-4.0",
	Inv2Pi: "0.15915494",
}

// IsInline reports whether f is one of the standard inline floats.
func (f FImm) IsInline() bool { _, ok := inlineFloats[f]; return ok }

func (f FImm) Text() string {
	if s, ok := inlineFloats[f]; ok {
		return s
	}
	return strconv.FormatFloat(float64(f), 'g', -1, 64)
}
func (FImm) operand() {}

// Sym is a bare symbol reference. Prefer the typed handles returned by
// Module.Object, Body.Label and Module.Add; Sym exists for symbols this
// package does not model.
type Sym string

func (s Sym) Text() string { return string(s) }
func (Sym) operand()       {}
func (Sym) addressable()   {}

// Mem is a memory operand: [base], [base+off], or [base-off]. Displacements
// are signed and print in signed decimal; zero is omitted entirely (rule 6).
// Legal ranges are per-encoding and are checked at lowering, not here (V20).
type Mem struct {
	Base   Addressable
	Offset int64
}

func (m Mem) Text() string {
	if m.Base == nil {
		return "[<nil>]"
	}
	switch {
	case m.Offset > 0:
		return "[" + m.Base.Text() + "+" + strconv.FormatInt(m.Offset, 10) + "]"
	case m.Offset < 0:
		return "[" + m.Base.Text() + strconv.FormatInt(m.Offset, 10) + "]"
	}
	return "[" + m.Base.Text() + "]"
}
func (Mem) operand() {}

// At builds a memory operand with an optional byte displacement:
// At(r) renders as [%vd0], At(r, 16) as [%vd0+16].
func At(base Addressable, off ...int64) Mem {
	m := Mem{Base: base}
	if len(off) > 0 {
		m.Offset = off[0]
	}
	return m
}

// Mod is an instruction modifier: a dotted name with an optional integer
// argument, as in ".clamp" or ".mul(2)". Legality is per-instruction and
// per-target and is not modelled here.
type Mod struct {
	Name   string
	Arg    int
	HasArg bool
}

func (m Mod) Text() string {
	if m.HasArg {
		return "." + m.Name + "(" + strconv.Itoa(m.Arg) + ")"
	}
	return "." + m.Name
}

// Common modifiers. Cache-policy modifiers are target-specific; code that
// needs portable ordering should use Fence and let lowering pick the bits.
var (
	Clamp = Mod{Name: "clamp"}
	GLC   = Mod{Name: "glc"}
	SLC   = Mod{Name: "slc"}
	DLC   = Mod{Name: "dlc"}
	NT    = Mod{Name: "nt"}
	Offen = Mod{Name: "offen"}
	Idxen = Mod{Name: "idxen"}
)

// Mul and Div build the output-multiplier modifiers.
func Mul(n int) Mod { return Mod{"mul", n, true} }
func Div(n int) Mod { return Mod{"div", n, true} }

// Enc is a pinned physical encoding asserted with .enc(<name>). It is
// checked after instruction selection against the per-target encoding table
// (V25), which is a lowering obligation, not a Verify one.
type Enc uint8

const (
	EncAuto Enc = iota
	SOP1
	SOP2
	SOPK
	SOPP
	SOPC
	SMEM
	VOP1
	VOP2
	VOP3
	VOP3P
	VOPC
	VOPD
	DS
	FLAT
	GLOBAL
	SCRATCH
	MUBUF
)

func (e Enc) String() string {
	return [...]string{"auto", "SOP1", "SOP2", "SOPK", "SOPP", "SOPC", "SMEM",
		"VOP1", "VOP2", "VOP3", "VOP3P", "VOPC", "VOPD", "DS", "FLAT",
		"GLOBAL", "SCRATCH", "MUBUF"}[e]
}

// Text returns the .enc clause, or "" for the default (rule 8).
func (e Enc) Text() string {
	if e == EncAuto {
		return ""
	}
	return ".enc(" + e.String() + ")"
}

// operandRegs calls fn for every Reg reachable from o, including through a
// memory operand's base.
func operandRegs(o Operand, fn func(Reg)) {
	switch x := o.(type) {
	case Reg:
		fn(x)
	case Mem:
		if r, ok := x.Base.(Reg); ok {
			fn(r)
		}
	}
}

// literalOf reports whether o occupies a literal dword slot, and returns a
// key identifying the literal value (V18).
func literalOf(o Operand) (uint64, bool) {
	switch x := o.(type) {
	case Imm:
		if x.IsInline() {
			return 0, false
		}
		return uint64(int64(x)), true
	case FImm:
		if x.IsInline() {
			return 0, false
		}
		return math.Float64bits(float64(x)), true
	}
	return 0, false
}

func joinText(ops []Operand, sep string) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		parts[i] = o.Text()
	}
	return strings.Join(parts, sep)
}