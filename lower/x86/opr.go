// opr.go is the pre-encoding instruction vocabulary instruction selection
// builds: an Opr is a register, an immediate/symbol, a memory reference,
// or — the one thing a lowering pass needs that the generic encoder
// deliberately doesn't have — an unresolved slot naming a vir value's
// not-yet-placed stack home. Register fields are isax86.Reg, used
// directly; nothing here re-declares REAX/RECX/etc. under a local name.
package x86

import isax86 "github.com/vertex-language/vvm/isa/x86"

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm  // immediate; Sym != "" makes it a symbol address (+ Imm addend)
	OMem  // [Base(+Index*Scale)+Disp], or absolute when Base/Index == isax86.RNone
	OSlot // a vir value's home slot; resolved during final assembly (encode.go)
)

// Opr is one operand as instruction selection produces it. OSlot is the
// one addition over isa/x86/encoder.Opr, and it's deliberately absent
// there — see isa/x86's README on which package owns which concept.
type Opr struct {
	Kind  OprKind
	Reg   isax86.Reg
	Imm   int64
	Sym   string     // OImm: symbol whose address (+ Imm addend) this immediate is
	Base  isax86.Reg // OMem: isax86.RNone if absent (pure index, or absolute)
	Index isax86.Reg // OMem: isax86.RNone if no index register
	Scale byte       // OMem: 1, 2, 4, or 8 (only meaningful when Index != isax86.RNone)
	Disp  int32      // OMem: displacement, or the absolute address's low bits
	MSym  string     // OMem: symbol for an absolute [msym+disp] reference
	Slot  string     // OSlot: the vir value name
}

func R(r isax86.Reg) Opr    { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr       { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr  { return Opr{Kind: OImm, Sym: s} }
func Slot(name string) Opr  { return Opr{Kind: OSlot, Slot: name} }

// Mem is a simple [base+disp] memory operand.
func Mem(b isax86.Reg, d int32) Opr {
	return Opr{Kind: OMem, Base: b, Index: isax86.RNone, Disp: d}
}

// MemIndexed is a full [base+index*scale+disp] memory operand. base may be
// isax86.RNone (pure index*scale+disp32); index may not be RESP (ESP
// cannot be a SIB index register).
func MemIndexed(base, index isax86.Reg, scale byte, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: index, Scale: scale, Disp: disp}
}

// MemAbs is an absolute [msym+disp] reference (mod=00,rm=101 form).
func MemAbs(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: isax86.RNone, Index: isax86.RNone, MSym: sym, Disp: disp}
}

// Inst is one pre-encoding pseudo instruction. Two Op spellings are pure
// pseudo-ops, expanded only by encode.go's assemble and never seen by
// isa/x86/encoder: "epi_ret", "epi_jmp_sym", "epi_jmp_r" — each means "run
// the epilogue, then do the real thing." Keeping them as pseudo-ops means
// the epilogue's shape (which callee-saved regs, how big the frame is)
// lives in exactly one place instead of being duplicated at every
// return/tailcall site in isel.go.
type Inst struct {
	Op  string
	D   Opr
	S   Opr
	CC  byte
	Sz  int
	Lbl string
	Sym string
	Imm int64
}