// Package arm lowers verified vir modules to 32-bit ARM (A32) machine code,
// in either byte order: arch "arm" (little-endian) or "armeb" (big-endian),
// selected by the Arch argument to Lower (see arch.go).
//
// Instruction-set facts (register identity, condition codes,
// rotated-immediate/shift encodings, opcode<->mnemonic tables) live in
// isa/arm; the generic Inst-stream-to-bytes assembler, isa/arm/encoder,
// already re-exports isa/arm's Reg type and R0..RPC/CondEQ..CondAL
// constants for exactly this situation — a caller building an Inst stream
// that needs to name a register or condition. This package writes
// encoder.R0, encoder.RIP, encoder.CondEQ, etc. at the point of use and
// declares no register or condition constant of its own; a third copy of
// those eight-ish names would be the same drift risk isa/x86_64's README
// describes fixing in lower/x86_64.
package arm

import "github.com/vertex-language/vvm/isa/arm/encoder"

// OprKind is encoder.Opr's four kinds (register, immediate/symbol, memory,
// indexed-memory) plus OSlot: an unresolved reference to a vir value's
// not-yet-placed stack home. isel.go is the only thing that produces
// OSlot operands; encode.go's assemble is the only thing that consumes
// them, rewriting every OSlot to a concrete Mem(encoder.RFP, offset)
// before converting to encoder.Opr.
type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm
	OMem
	OSlot
)

// Opr's Reg/Base/Index fields are encoder.Reg (itself an alias of
// isa/arm.Reg) — not a locally redeclared type — so a value built here
// needs no conversion to become an encoder.Opr field.
type Opr struct {
	Kind  OprKind
	Reg   encoder.Reg
	Imm   int64
	Sym   string // OImm: symbol whose address (+Imm addend) this immediate is
	Base  encoder.Reg
	Index encoder.Reg // encoder.RNone for [Base+Disp]; else [Base+Index]
	Disp  int32
	Slot  string // OSlot: the vir value name
}

func R(r encoder.Reg) Opr             { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr                 { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr            { return Opr{Kind: OImm, Sym: s} }
func Mem(b encoder.Reg, d int32) Opr  { return Opr{Kind: OMem, Base: b, Index: encoder.RNone, Disp: d} }
func MemIndexed(b, i encoder.Reg) Opr { return Opr{Kind: OMem, Base: b, Index: i} }
func Slot(n string) Opr               { return Opr{Kind: OSlot, Slot: n} }

// Inst is encoder.Inst's vocabulary over Oprs that may still carry an
// OSlot, plus three function-exit markers — epi_ret, epi_jmp_sym,
// epi_jmp_r — isel.go emits at every return/tailcall site instead of a
// bare pop/branch. encode.go's assemble is the only place that expands
// one of these into the real epilogue (mov sp,fp; pop {fp,pc|lr}) followed
// by the plain exit encoder.Encode actually knows about — mirroring
// isa/arm/encoder's own design point that frame setup/teardown are
// ordinary instructions a caller emits into the stream, never spliced in
// by the assembler itself.
type Inst struct {
	Op         string
	D, S, T, X Opr
	CC         byte
	RegList    uint16
	Lbl        string
	Sym        string
	Imm        int64
}