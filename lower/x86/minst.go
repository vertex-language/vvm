package x86

// The pre-encoding instruction form: a thin, x86-shaped pseudo instruction
// stream over physical registers, immediates, symbols, and value slots.
// Isel produces these; regalloc resolves slots to EBP-relative memory;
// encode turns them into bytes.

type reg byte

const (
	rEAX reg = 0
	rECX reg = 1
	rEDX reg = 2
	rEBX reg = 3
	rESP reg = 4
	rEBP reg = 5
	rESI reg = 6
	rEDI reg = 7
	rNone reg = 0xFF
)

type oprKind byte

const (
	oNone oprKind = iota
	oReg
	oImm  // immediate; sym != "" makes it a symbol address (+imm addend)
	oMem  // [base+disp], or absolute [msym+disp] when base == rNone
	oSlot // a vir value's home slot; resolved by regalloc
)

type opr struct {
	k    oprKind
	reg  reg
	imm  int64
	sym  string
	base reg
	disp int32
	msym string
	slot string
}

func R(r reg) opr           { return opr{k: oReg, reg: r} }
func Imm(v int64) opr       { return opr{k: oImm, imm: v} }
func SymAddr(s string) opr  { return opr{k: oImm, sym: s} }
func Mem(b reg, d int32) opr { return opr{k: oMem, base: b, disp: d} }
func Slot(n string) opr     { return opr{k: oSlot, slot: n} }

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

// minst ops (op field):
//
//	label                 lbl
//	mov                   d,s (reg<-imm/sym, reg<-reg, reg<-mem32, mem<-reg[sz], mem<-imm32)
//	movzx / movsx         d reg, s reg/mem, sz 1|2
//	lea                   d reg, s mem
//	add or and sub xor cmp  d,s in reg/mem/imm combinations
//	test                  d reg, s reg
//	imul                  d reg, s reg/mem (0F AF)
//	imul3                 d reg, s reg/mem, imm (69 /r id)
//	not neg mul32 imul32 div idiv   s reg (F7 group)
//	cdq
//	shl shr sar rol ror   d reg, s = imm (Cx forms) or CL (Dx forms); sz 1|2|4
//	setcc                 cc, d reg (low byte; EAX..EBX only)
//	cmovcc                cc, d reg, s reg/mem
//	jmp / jcc             lbl (intra-function, rel32, patched)
//	call_sym / call_r     sym / s reg
//	push / pop            s reg / d reg
//	ret ud2 cdq mfence cld std rep_movsb rep_stosb
//	bsr bsf               d reg, s reg/mem
//	bswap                 d reg
//	xchg / lock_xadd / lock_cmpxchg   d mem, s reg
//	popcnt                d reg, s reg/mem (F3 0F B8; tier-gated, TODO §10.4)
//	prologue              imm = local byte count
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