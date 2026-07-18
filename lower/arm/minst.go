package arm

// The pre-encoding instruction form: a thin, A32-shaped pseudo instruction
// stream over physical registers, immediates, symbols, and value slots.
// Isel produces these; regalloc resolves slots to FP-relative memory;
// encode turns them into 4-byte words.

type reg byte

const (
	r0  reg = 0
	r1  reg = 1
	r2  reg = 2
	r3  reg = 3
	r4  reg = 4
	rIP reg = 12 // intra-procedure scratch; also encoder scratch for wide imms
	rSP reg = 13
	rLR reg = 14
	rPC reg = 15
	rFP reg = 11
	rNone reg = 0xFF
)

type oprKind byte

const (
	oNone oprKind = iota
	oReg
	oImm  // immediate; sym != "" makes it a symbol address (+imm addend)
	oMem  // [base + disp]
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
func SymAddr(s string) opr   { return opr{k: oImm, sym: s} }
func Mem(b reg, d int32) opr { return opr{k: oMem, base: b, disp: d} }
func Slot(n string) opr      { return opr{k: oSlot, slot: n} }

// A32 condition codes (bits 31:28).
const (
	ccEQ = 0x0
	ccNE = 0x1
	ccHS = 0x2 // unsigned >=
	ccLO = 0x3 // unsigned 
	ccMI = 0x4
	ccPL = 0x5
	ccVS = 0x6
	ccVC = 0x7
	ccHI = 0x8 // unsigned >
	ccLS = 0x9 // unsigned <=
	ccGE = 0xA
	ccLT = 0xB
	ccGT = 0xC
	ccLE = 0xD
	ccAL = 0xE
)

// minst ops (op field). Register-operand conventions are noted per op:
//
//	label                  lbl
//	movimm                 d reg, imm (movw, + movt when needed; flag-free)
//	movsym                 d reg, sym+imm (movw/movt pair with fixups)
//	mov_r                  d reg <- s reg
//	mvn                    d reg <- ~s reg
//	add sub rsb and orr eor bic   d reg, s reg|imm (d := d OP s)
//	adds subs              flag-setting variants (overflow predicates)
//	cmp cmn tst            d reg vs s reg|imm
//	cmp_asr31              cmp d, s ASR #31 (smulo check)
//	lsl lsr asr ror        d reg := s reg SHIFT t (t reg or imm)
//	mul                    d := s * t
//	mls                    d := x - s*t (x in the 'x' field)
//	umull smull            dlo=d, dhi=x, operands s, t
//	udiv sdiv              d := s / t  (idiv-capable core; tier TODO §10.4)
//	clz rbit rev           d reg <- s reg
//	uxtb uxth sxtb sxth    d reg <- s reg
//	movcc                  cc, d reg <- s reg|imm(0..255), condition-gated
//	ldr ldrb ldrh ldrsb ldrsh   d reg <- s mem/slot
//	str strb strh          s reg -> d mem/slot
//	ldrb_r strb_r          byte at [s.base? no: base in s, index in t] (loops)
//	ldrex strex            exclusive pair; strex status in x
//	clrex dmb udf
//	b / bcc                lbl (intra-function rel24, patched)
//	bl_sym                 sym (FixupCall24)
//	blx_r                  s reg
//	sub_sp / add_sp        imm (SP adjust; encoder may synthesize via IP)
//	and_sp                 imm mask (dynamic alloca over-alignment)
//	mov_r_sp / mov_sp_r    d/s = SP interchange helpers
//	epi_ret                mov sp, fp; pop {fp, pc}
//	epi_jmp_sym            epilogue keeping lr popped, then B sym (FixupJump24)
//	epi_jmp_r              epilogue, then BX s reg
type minst struct {
	op  string
	d   opr
	s   opr
	t   opr
	x   opr
	cc  byte
	sz  int
	lbl string
	sym string
	imm int64
}