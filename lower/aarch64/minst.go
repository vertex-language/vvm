package aarch64

// The pre-encoding instruction form: a thin, A64-shaped pseudo instruction
// stream over physical registers, immediates, symbols, and value slots.
// Isel produces these; regalloc resolves slots to FP-relative memory;
// encode turns them into 4-byte little-endian words.

type reg byte

const (
	x0 reg = 0
	x1 reg = 1
	x2 reg = 2
	x3 reg = 3
	x4 reg = 4
	x5 reg = 5
	x6 reg = 6
	x7 reg = 7
	x8 reg = 8 // AAPCS64 indirect-result register; reserved until sret lowering

	rIDX reg = 15 // isel loop-index scratch (bulk memory)
	rIP0 reg = 16 // encoder scratch: wide immediates, far slot addressing
	rIP1 reg = 17 // indirect-call/tailcall callee pointer; live only load->br
	rPR  reg = 18 // platform register (AAPCS64) — never touched
	rFP  reg = 29
	rLR  reg = 30
	rSP  reg = 31 // encoding 31 as base/sp contexts
	rZR  reg = 31 // encoding 31 as zero-register contexts
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

// A64 condition codes (B.cond / csel cond field).
const (
	ccEQ = 0x0
	ccNE = 0x1
	ccHS = 0x2 // unsigned >=
	ccLO = 0x3 // unsigned 
	ccMI = 0x4
	ccPL = 0x5
	ccVS = 0x6 // signed overflow
	ccVC = 0x7
	ccHI = 0x8 // unsigned >
	ccLS = 0x9 // unsigned <=
	ccGE = 0xA
	ccLT = 0xB
	ccGT = 0xC
	ccLE = 0xD
	ccAL = 0xE
)

// invert flips a condition code to its complement (cc ^ 1 pairs them).
func invert(cc byte) byte { return cc ^ 1 }

// minst ops (op field). sz selects the W (4) or X (8) form of an operation
// where both exist; for loads/stores sz is the access size (1/2/4/8).
// Register-operand conventions per op:
//
//	label                  lbl
//	movimm                 d reg, imm (movz/movn + movk synthesis; always X)
//	movsym                 d reg, sym+imm (movz/movk quad with 4 fixups)
//	mov_r                  d reg <- s reg (orr with zr; sz 4 clears bits 63:32)
//	mvn                    d reg <- ~s reg (orn with zr)
//	neg                    d reg <- 0 - s reg (sub from zr)
//	add sub and orr eor bic    d := d OP s (s reg; imm for add/sub only)
//	adds subs              flag-setting variants (overflow predicates)
//	cmp cmn                d reg vs s reg|imm
//	lslv lsrv asrv rorv    d := s SHIFT t reg (count mod 32/64 in hardware)
//	lsl_i lsr_i asr_i      d := s SHIFT imm (UBFM/SBFM aliases)
//	mul                    d := s * t (madd with zr)
//	msub                   d := x - s*t
//	smulh umulh            d := high64(s * t) (X only)
//	smull umull            d(X) := s(W) * t(W), full 64-bit product
//	sdiv udiv              d := s / t (never traps in hw; §6.1 traps explicit)
//	clz rbit rev rev16     d reg <- s reg
//	uxtb uxth and1         d reg <- s reg (W forms; zero-extend / i1 mask)
//	sxtb sxth sxtw sxt1    d reg <- s reg (sz selects W/X destination)
//	cset                   cc, d reg <- 0/1 from flags (csinc zr, zr, !cc)
//	csel                   cc, d := cc ? s : d (csel d, s, d, cc)
//	ldr str                d/s reg <-> s/d mem/slot (sz 1/2/4/8)
//	ldrb_r strb_r          byte at [base s/d + index t] (bulk-memory loops)
//	ldar stlr              acquire load / release store (sz 1/2/4/8)
//	ldaxr stlxr            exclusive pair (sz 4/8); stlxr status in x
//	clrex dmb brk
//	b / bcc                lbl (intra-function rel, patched; no PC bias)
//	cbz cbnz               s reg, lbl (sz)
//	bl_sym                 sym (FixupCall26)
//	blr_r                  s reg
//	sub_sp / add_sp        imm (SP adjust; encoder may synthesize via IP0)
//	sub_sp_r               sp -= s reg (extended-register form, uxtx)
//	and_sp                 imm = -N mask, N a power of two (alloca over-align)
//	mov_r_sp               d reg := sp
//	epi_ret                mov sp, fp; ldp fp, lr, [sp], 16; ret
//	epi_jmp_sym            epilogue, then B sym (FixupJump26)
//	epi_jmp_r              epilogue, then BR s reg (IP1 survives the epilogue)
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