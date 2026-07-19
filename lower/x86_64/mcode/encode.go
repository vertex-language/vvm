package mcode

import "fmt"

// Encode turns a resolved Inst stream into x86-64 machine bytes.
//
// REX prefix (0100WRXB): W selects 64-bit operand size, R/X/B extend
// ModRM.reg, SIB.index, and ModRM.rm/SIB.base to reach r8–r15; legacy
// prefixes (66, F0, F2/F3) precede REX, which immediately precedes the
// opcode. ModRM mod=00 rm=101 means [RIP+disp32] (plain [disp32] absolute
// requires the SIB form base=101), rm=100 still forces a SIB byte (RSP/R12
// base), and an RBP/R13 base always carries a displacement. RSP is 16-byte
// aligned after the prologue and every call-site adjustment keeps it that
// way.
func Encode(insts []Inst, localBytes int32) ([]byte, []Fixup, error) {
	e := &enc{labels: map[string]int{}}
	e.prologue(localBytes)
	for i := range insts {
		if err := e.one(&insts[i]); err != nil {
			return nil, nil, err
		}
	}
	for _, p := range e.patches {
		t, ok := e.labels[p.lbl]
		if !ok {
			return nil, nil, fmt.Errorf("encode: undefined label %q", p.lbl)
		}
		rel := int32(t - (p.pos + 4))
		putLE32(e.b[p.pos:], uint32(rel))
	}
	return e.b, e.fx, nil
}

type patch struct {
	pos int
	lbl string
}

type enc struct {
	b       []byte
	fx      []Fixup
	labels  map[string]int
	patches []patch
}

func (e *enc) u8(v ...byte) { e.b = append(e.b, v...) }
func (e *enc) u32(v uint32) { e.b = append(e.b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
func (e *enc) u64(v uint64) { e.u32(uint32(v)); e.u32(uint32(v >> 32)) }
func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
func modrm(mod, reg, rm byte) byte { return mod<<6 | reg<<3 | rm }

func hi(r Reg) byte { return byte(r) >> 3 & 1 } // REX extension bit
func lo(r Reg) byte { return byte(r) & 7 }      // 3-bit ModRM field

// rex emits a REX prefix if any of W/R/B demands one. X (SIB.index
// extension) is always 0 — this encoder never uses an index reg.
func (e *enc) rex(w bool, regField byte, m Opr) {
	var wb, r, b byte
	if w {
		wb = 1
	}
	r = regField >> 3 & 1
	switch m.K {
	case KReg:
		b = hi(m.Reg)
	case KMem:
		if m.Base != RNone {
			b = hi(m.Base)
		}
	}
	if wb|r|b != 0 {
		e.u8(0x40 | wb<<3 | r<<2 | b)
	}
}

// memBody emits the ModRM (+SIB, +disp) bytes addressing operand m, with the
// low 3 bits of regField in ModRM.reg (the caller has already folded bit 3
// into REX.R). Handles the x86-64 special cases: rm=100 requires a SIB byte
// (RSP/R12 base -> SIB 0x24), mod=00 rm=101 means [RIP+disp32] (so RBP/R13
// bases always carry a displacement, and RIP-relative operands use exactly
// this form), and absolute [disp32] needs the SIB escape (0x25).
func (e *enc) memBody(regField byte, m Opr) error {
	rf := regField & 7
	switch m.K {
	case KReg:
		e.u8(modrm(3, rf, lo(m.Reg)))
		return nil
	case KRIP:
		e.u8(modrm(0, rf, 5)) // [RIP+disp32]
		e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: m.Sym, Kind: FixupPCRel32, Addend: int64(m.Disp) - 4})
		e.u32(uint32(0xFFFFFFFC + uint32(m.Disp))) // A = disp - 4, REL-style
		return nil
	case KMem:
		if m.Base == RNone { // absolute [disp32]: SIB escape, no base
			e.u8(modrm(0, rf, 4), 0x25)
			e.u32(uint32(m.Disp))
			return nil
		}
		disp := m.Disp
		var mod byte
		switch {
		case disp == 0 && lo(m.Base) != 5: // RBP/R13 base forces a disp
			mod = 0
		case disp >= -128 && disp <= 127:
			mod = 1
		default:
			mod = 2
		}
		e.u8(modrm(mod, rf, lo(m.Base)))
		if lo(m.Base) == 4 { // RSP/R12 base needs SIB
			e.u8(0x24)
		}
		switch mod {
		case 1:
			e.u8(byte(int8(disp)))
		case 2:
			e.u32(uint32(disp))
		}
		return nil
	}
	return fmt.Errorf("encode: operand is not a register or memory operand")
}

// op emits [66] [REX] opcode ModRM… for a reg-and-r/m instruction.
func (e *enc) op(sz int, regField byte, m Opr, opc ...byte) error {
	if sz == 2 {
		e.u8(0x66)
	}
	e.rex(sz == 8, regField, m)
	e.u8(opc...)
	return e.memBody(regField, m)
}

func (e *enc) prologue(localBytes int32) {
	e.u8(0x55)             // push rbp        (rsp: 8 mod 16 -> 0 mod 16)
	e.u8(0x48, 0x89, 0xE5) // mov rbp, rsp
	if localBytes > 0 {    // localBytes is 16-aligned (abi.BuildFrame)
		e.u8(0x48, 0x81, modrm(3, 5, byte(RSP))) // sub rsp, imm32
		e.u32(uint32(localBytes))
	}
}

func (e *enc) epilogue() {
	e.u8(0x48, 0x89, 0xEC) // mov rsp, rbp
	e.u8(0x5D)             // pop rbp
}

func (e *enc) callFix(opcode byte, sym string) {
	e.u8(opcode) // E8 call rel32 / E9 jmp rel32
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupPCRel32Call, Addend: -4})
	e.u32(uint32(0xFFFFFFFC)) // -4 written into the field for REL-style consumers
}

var aluEnc = map[string]struct{ mr, rm, ext byte }{
	"add": {0x01, 0x03, 0}, "or": {0x09, 0x0B, 1}, "and": {0x21, 0x23, 4},
	"sub": {0x29, 0x2B, 5}, "xor": {0x31, 0x33, 6}, "cmp": {0x39, 0x3B, 7},
}
var shiftExt = map[string]byte{"rol": 0, "ror": 1, "shl": 4, "shr": 5, "sar": 7}
var grp3Ext = map[string]byte{"not": 2, "neg": 3, "mul1": 4, "imul1": 5, "div": 6, "idiv": 7}
var grp5Ext = map[string]byte{"inc": 0, "dec": 1}

func fitsI32(v int64) bool { return v >= -(1<<31) && v < 1<<31 }
func fitsU32(v int64) bool { return uint64(v)>>32 == 0 }

func (e *enc) one(in *Inst) error {
	sz := in.Sz
	if sz == 0 {
		sz = 8
	}
	switch in.Op {
	case "label":
		e.labels[in.Lbl] = len(e.b)

	case "mov":
		d, s := in.D, in.S
		switch {
		case d.K == KReg && s.K == KSym:
			// lea d, [rip+sym] — the PIC-clean address materialization.
			return e.op(8, byte(d.Reg), Opr{K: KRIP, Sym: s.Sym, Disp: int32(s.Imm)}, 0x8D)
		case d.K == KReg && s.K == KImm:
			switch {
			case fitsU32(s.Imm): // mov r32, imm32 zero-extends to 64
				if hi(d.Reg) != 0 {
					e.u8(0x41)
				}
				e.u8(0xB8 + lo(d.Reg))
				e.u32(uint32(s.Imm))
			case fitsI32(s.Imm): // REX.W C7 /0 sign-extends imm32
				e.rex(true, 0, R(d.Reg))
				e.u8(0xC7, modrm(3, 0, lo(d.Reg)))
				e.u32(uint32(s.Imm))
			default: // movabs REX.W B8+r imm64
				e.u8(0x48 | hi(d.Reg))
				e.u8(0xB8 + lo(d.Reg))
				e.u64(uint64(s.Imm))
			}
		case d.K == KReg && s.K == KReg:
			return e.op(sz, byte(s.Reg), R(d.Reg), 0x89)
		case d.K == KReg && (s.K == KMem || s.K == KRIP):
			return e.op(sz, byte(d.Reg), s, 0x8B)
		case (d.K == KMem || d.K == KRIP) && s.K == KReg:
			if sz == 1 {
				return e.op(4, byte(s.Reg), d, 0x88) // no REX.W; AL..BL sources only
			}
			return e.op(sz, byte(s.Reg), d, 0x89)
		case d.K == KMem && s.K == KImm:
			if !fitsI32(s.Imm) {
				return fmt.Errorf("encode: mov m, imm beyond int32")
			}
			if err := e.op(sz, 0, d, 0xC7); err != nil {
				return err
			}
			e.u32(uint32(s.Imm))
		default:
			return fmt.Errorf("encode: bad mov operands")
		}

	case "movzx":
		if sz == 4 { // mov r32, r/m32 zero-extends to 64 by definition
			return e.op(4, byte(in.D.Reg), in.S, 0x8B)
		}
		op2 := byte(0xB6) // movzx r32, r/m8
		if sz == 2 {
			op2 = 0xB7
		}
		return e.op(4, byte(in.D.Reg), in.S, 0x0F, op2)

	case "movsx":
		if sz == 4 { // movsxd r64, r/m32
			return e.op(8, byte(in.D.Reg), in.S, 0x63)
		}
		op2 := byte(0xBE)
		if sz == 2 {
			op2 = 0xBF
		}
		return e.op(8, byte(in.D.Reg), in.S, 0x0F, op2) // sign-extend to 64

	case "lea":
		return e.op(8, byte(in.D.Reg), in.S, 0x8D)

	case "add", "or", "and", "sub", "xor", "cmp":
		enc := aluEnc[in.Op]
		d, s := in.D, in.S
		switch {
		case d.K == KReg && s.K == KReg:
			return e.op(sz, byte(s.Reg), R(d.Reg), enc.mr)
		case d.K == KReg && s.K == KMem:
			return e.op(sz, byte(d.Reg), s, enc.rm)
		case d.K == KMem && s.K == KReg:
			return e.op(sz, byte(s.Reg), d, enc.mr)
		case s.K == KImm:
			if !fitsI32(s.Imm) {
				return fmt.Errorf("encode: %s imm beyond int32 (materialize via mov first)", in.Op)
			}
			if err := e.op(sz, enc.ext, d, 0x81); err != nil {
				return err
			}
			e.u32(uint32(s.Imm))
		default:
			return fmt.Errorf("encode: bad %s operands", in.Op)
		}

	case "test":
		return e.op(sz, byte(in.S.Reg), R(in.D.Reg), 0x85)

	case "imul": // imul r, r/m
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, 0xAF)

	case "imul3": // imul r, r/m, imm32
		if !fitsI32(in.Imm) {
			return fmt.Errorf("encode: imul3 imm beyond int32")
		}
		if err := e.op(sz, byte(in.D.Reg), in.S, 0x69); err != nil {
			return err
		}
		e.u32(uint32(in.Imm))

	case "not", "neg", "mul1", "imul1", "div", "idiv":
		return e.op(sz, grp3Ext[in.Op], in.S, 0xF7)

	case "inc", "dec":
		return e.op(sz, grp5Ext[in.Op], in.S, 0xFF)

	case "cdq":
		e.u8(0x99)
	case "cqo":
		e.u8(0x48, 0x99)

	case "shl", "shr", "sar", "rol", "ror":
		ext := shiftExt[in.Op]
		if in.S.K == KImm { // Cx /ext ib
			opc := byte(0xC1)
			if sz == 1 {
				opc = 0xC0
			}
			if err := e.op(sz, ext, in.D, opc); err != nil {
				return err
			}
			e.u8(byte(in.S.Imm))
		} else { // by CL: Dx /ext
			opc := byte(0xD3)
			if sz == 1 {
				opc = 0xD2
			}
			return e.op(sz, ext, in.D, opc)
		}

	case "setcc": // 0F 9x /0, low byte of d (AL..BL without REX)
		if in.D.Reg > RBX {
			return fmt.Errorf("encode: setcc needs AL..BL in this bring-up")
		}
		e.u8(0x0F, 0x90+in.CC, modrm(3, 0, lo(in.D.Reg)))

	case "cmovcc": // 0F 4x /r
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, 0x40+in.CC)

	case "jmp":
		e.u8(0xE9)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(0)

	case "jcc":
		e.u8(0x0F, 0x80+in.CC)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(0)

	case "call_sym":
		e.callFix(0xE8, in.Sym)

	case "call_r":
		if hi(in.S.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0xFF, modrm(3, 2, lo(in.S.Reg)))

	case "push": // 64-bit, no REX.W needed
		if hi(in.S.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0x50 + lo(in.S.Reg))

	case "pop":
		if hi(in.D.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0x58 + lo(in.D.Reg))

	case "ret":
		e.u8(0xC3)

	case "ud2":
		e.u8(0x0F, 0x0B)

	case "nop":
		e.u8(0x90)

	case "syscall":
		e.u8(0x0F, 0x05)

	case "bsr":
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, 0xBD)

	case "bsf":
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, 0xBC)

	case "bswap":
		if sz == 8 {
			e.u8(0x48 | hi(in.D.Reg))
		} else if hi(in.D.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0x0F, 0xC8+lo(in.D.Reg))

	case "xchg": // xchg r/m, r — implicitly locked with a memory operand
		return e.op(sz, byte(in.S.Reg), in.D, 0x87)

	case "lock_xadd":
		e.u8(0xF0) // LOCK precedes REX
		return e.op(sz, byte(in.S.Reg), in.D, 0x0F, 0xC1)

	case "lock_cmpxchg":
		e.u8(0xF0)
		return e.op(sz, byte(in.S.Reg), in.D, 0x0F, 0xB1)

	case "mfence":
		e.u8(0x0F, 0xAE, 0xF0)

	case "cld":
		e.u8(0xFC)
	case "std":
		e.u8(0xFD)
	case "rep_movsb":
		e.u8(0xF3, 0xA4)
	case "rep_stosb":
		e.u8(0xF3, 0xAA)

	case "popcnt": // F3 [REX] 0F B8 /r — tier-gated (§10.4), TODO
		e.u8(0xF3) // mandatory prefix precedes REX
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, 0xB8)

	case "epi_ret":
		e.epilogue()
		e.u8(0xC3)

	case "epi_jmp_sym":
		e.epilogue()
		e.callFix(0xE9, in.Sym)

	case "epi_jmp_r":
		e.epilogue()
		if hi(in.S.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0xFF, modrm(3, 4, lo(in.S.Reg)))

	default:
		return fmt.Errorf("encode: unknown Inst op %q", in.Op)
	}
	return nil
}