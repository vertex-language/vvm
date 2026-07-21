package x86_64

import isax86_64 "github.com/vertex-language/vvm/isa/x86_64"

type OprKind byte

const (
	KNone OprKind = iota
	KReg
	KImm  // immediate
	KSym  // a symbol address, materialized RIP-relatively (+imm addend)
	KMem  // [base+disp]
	KRIP  // [rip + sym + disp] — RIP-relative memory operand
	KSlot // a vir value's home slot; resolved by resolveSlots before encoding
)

// Opr is this package's pre-encoding operand vocabulary: identical to
// encoder.Opr except for the one variant the generic encoder doesn't and
// shouldn't know about — KSlot, an unresolved reference to a vir value's
// not-yet-placed stack home. isel.go is the only thing that produces
// KSlot operands; encode.go's resolveSlots is the only thing that
// consumes them, rewriting every KSlot to a concrete KMem before the
// stream is converted to encoder.Opr. Because encoder.Opr has no KSlot
// kind at all, "did I forget to resolve a slot" is a conversion error
// (toEncoderOpr), not a silent miscompile.
//
// Reg fields hold isa/x86_64.Reg values directly — RAX, RCX, and so on are
// referenced as isax86_64.RAX at every call site, never re-declared here.
type Opr struct {
	K    OprKind
	Reg  isax86_64.Reg
	Imm  int64
	Sym  string
	Base isax86_64.Reg
	Disp int32
	Slot string
}

func R(r isax86_64.Reg) Opr   { return Opr{K: KReg, Reg: r} }
func Imm(v int64) Opr         { return Opr{K: KImm, Imm: v} }
func SymAddr(s string) Opr    { return Opr{K: KSym, Sym: s} }
func Mem(b isax86_64.Reg, d int32) Opr { return Opr{K: KMem, Base: b, Disp: d} }
func RipMem(s string) Opr     { return Opr{K: KRIP, Sym: s} }
func SlotOpr(n string) Opr    { return Opr{K: KSlot, Slot: n} }

// Inst is this package's pre-encoding instruction vocabulary: the same
// Op/D/S/CC/Sz/Lbl/Sym/Imm shape isel and the inline-asm lowerer both
// target, built from this package's own Opr so it can carry KSlot
// operands. CC holds an isa/x86_64 condition-code value (isax86_64.CondE,
// etc.) directly — no local re-declaration.
//
// It also carries three function-exit markers isel emits at every
// return/tail-call site — epi_ret, epi_jmp_sym, epi_jmp_r — which don't
// exist in encoder.Inst. encode.go's toEncoderInsts is the only place
// that expands one of these into the real epilogue instructions followed
// by the plain ret/jmp_sym/jmp_r the encoder knows about (see encode.go's
// doc comment for why frame teardown is spliced here, not inside the
// generic encoder).
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