package x86_64

// The pre-encoding instruction form: a thin, x86-64-shaped pseudo instruction
// stream over physical registers, immediates, symbols, and value slots.
// Isel produces these; regalloc resolves slots to RBP-relative memory;
// encode turns them into bytes.

type reg byte

const (
	rRAX reg = 0
	rRCX reg = 1
	rRDX reg = 2
	rRBX reg = 3
	rRSP reg = 4
	rRBP reg = 5
	rRSI reg = 6
	rRDI reg = 7
	rR8  reg = 8
	rR9  reg = 9
	rR10 reg = 10
	rR11 reg = 11
	rR12 reg = 12
	rR13 reg = 13
	rR14 reg = 14
	rR15 reg = 15
	rNone reg = 0xFF
)

// SysV AMD64 integer argument registers, in order.
var argRegs = [6]reg{rRDI, rRSI, rRDX, rRCX, rR8, rR9}

type oprKind byte

const (
	oNone oprKind = iota
	oReg
	oImm  // immediate
	oSym  // a symbol address, materialized RIP-relatively (+imm addend)
	oMem  // [base+disp]
	oRIP  // [rip + sym + disp] — RIP-relative memory operand
	oSlot // a vir value's home slot; resolved by regalloc
)

type opr struct {
	k    oprKind
	reg  reg
	imm  int64
	sym  string
	base reg
	disp int32
	slot string
}

func R(r reg) opr            { return opr{k: oReg, reg: r} }
func Imm(v int64) opr        { return opr{k: oImm, imm: v} }
func SymAddr(s string) opr   { return opr{k: oSym, sym: s} }
func Mem(b reg, d int32) opr { return opr{k: oMem, base: b, disp: d} }
func RipMem(s string) opr    { return opr{k: oRIP, sym: s} }
func Slot(n string) opr      { return opr{k: oSlot, slot: n} }

// Condition codes (Intel tttn encoding: 0F 8x jcc, 0F 9x setcc, 0F 4x cmovcc).
const (
	ccO  = 0
	ccNO = 1
	ccB  = 2 // unsigned <  (carry)
	ccAE = 3
	ccE  = 4
	ccNE = 5
	ccBE = 6
	ccA  = 7
	ccS  = 8
	ccNS = 9
	ccL  = 12 // signed 
	ccGE = 13
	ccLE = 14
	ccG  = 15
)

// minst ops (op field). sz ∈ {1, 2, 4, 8}; 8 selects REX.W forms.
//
//	label                 lbl
//	mov                   d,s (reg<-imm/imm64, reg<-sym [lea rip], reg<-reg,
//	                           reg<-mem, mem<-reg[sz], mem<-imm32)
//	movzx / movsx         d reg, s reg/mem, sz 1|2|4 (4 = movsxd for movsx,
//	                           plain 32-bit mov for movzx)
//	lea                   d reg, s mem/rip
//	add or and sub xor cmp  d,s in reg/mem/imm combinations; sz 4|8
//	test                  d reg, s reg; sz 4|8
//	imul                  d reg, s reg/mem (0F AF); sz 4|8
//	imul3                 d reg, s reg/mem, imm (69 /r id); sz 4|8
//	not neg mul1 imul1 div idiv   s reg (F7 group); sz 4|8
//	cdq / cqo
//	shl shr sar rol ror   d reg, s = imm (Cx) or CL (Dx); sz 1|2|4|8
//	setcc                 cc, d reg (low byte; RAX..RBX only)
//	cmovcc                cc, d reg, s reg/mem; sz 4|8
//	jmp / jcc             lbl (intra-function, rel32, patched)
//	call_sym / call_r     sym / s reg
//	push / pop            s reg / d reg (64-bit)
//	ret ud2 mfence cld std rep_movsb rep_stosb
//	bsr bsf               d reg, s reg/mem; sz 4|8
//	bswap                 d reg; sz 4|8
//	xchg / lock_xadd / lock_cmpxchg   d mem, s reg; sz 4|8
//	popcnt                d reg, s reg/mem; sz 4|8 (tier-gated, TODO §10.4)
//	epi_ret / epi_jmp_sym / epi_jmp_r
type minst struct {
	op  string
	d   opr
	s   opr
	cc  byte
	sz  int
	lbl string
	sym string
	imm int64
}