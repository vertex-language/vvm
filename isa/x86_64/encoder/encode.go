package encoder

import (
	"fmt"

	x86_64 "github.com/vertex-language/vvm/isa/x86_64"
)

// Encode turns a resolved Inst stream into x86-64 machine bytes. It does
// not know what a function is: no prologue, no epilogue, no frame size —
// callers that want those emit the corresponding mov/push/pop/sub
// instructions into insts themselves, as ordinary Insts, before calling
// Encode.
func Encode(insts []Inst) ([]byte, []Fixup, error) {
	e := &enc{labels: map[string]int{}}
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

// rex emits a REX prefix if any of W/R/B demands one. X (SIB.index
// extension) is always false — this encoder never uses an index register.
func (e *enc) rex(w bool, regField byte, m Opr) {
	r := regField>>3&1 != 0
	var b bool
	switch m.K {
	case KReg:
		b = x86_64.HiBit(m.Reg) == 1
	case KMem:
		if m.Base != x86_64.RNone {
			b = x86_64.HiBit(m.Base) == 1
		}
	}
	if w || r || b {
		e.u8(x86_64.PackREX(w, r, false, b))
	}
}

// memBody emits the ModRM (+SIB, +disp) bytes addressing operand m, with
// the low 3 bits of regField in ModRM.reg (the caller already folded bit
// 3 into REX.R via rex). Handles the x86-64 special cases: rm==RSP/R12
// always forces a SIB byte, mod=00 rm=101 means [RIP+disp32] (so RBP/R13
// bases always carry a displacement, and RIP-relative operands use
// exactly this form), and absolute [disp32] needs the SIB escape.
func (e *enc) memBody(regField byte, m Opr) error {
	rf := regField & 7
	switch m.K {
	case KReg:
		e.u8(x86_64.PackModRM(x86_64.ModReg, rf, x86_64.LoBits(m.Reg)))
		return nil
	case KRIP:
		e.u8(x86_64.PackModRM(x86_64.ModDisp0, rf, x86_64.RMRipOrDisp32))
		e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: m.Sym, Kind: FixupPCRel32, Addend: int64(m.Disp) - 4})
		e.u32(uint32(0xFFFFFFFC + uint32(m.Disp))) // A = disp - 4, REL-style
		return nil
	case KMem:
		if m.Base == x86_64.RNone { // absolute [disp32]: SIB escape, no base
			e.u8(x86_64.PackModRM(x86_64.ModDisp0, rf, x86_64.RMNeedsSIB))
			e.u8(x86_64.PackSIB(0, x86_64.SIBNoIndex, x86_64.SIBBaseEscape))
			e.u32(uint32(m.Disp))
			return nil
		}
		disp := m.Disp
		var mod byte
		switch {
		case disp == 0 && x86_64.LoBits(m.Base) != 5: // RBP/R13 base forces a disp
			mod = x86_64.ModDisp0
		case disp >= -128 && disp <= 127:
			mod = x86_64.ModDisp8
		default:
			mod = x86_64.ModDisp32
		}
		e.u8(x86_64.PackModRM(mod, rf, x86_64.LoBits(m.Base)))
		if x86_64.LoBits(m.Base) == x86_64.RMNeedsSIB { // RSP/R12 base needs SIB
			e.u8(x86_64.PackSIB(0, x86_64.SIBNoIndex, x86_64.LoBits(m.Base)))
		}
		switch mod {
		case x86_64.ModDisp8:
			e.u8(byte(int8(disp)))
		case x86_64.ModDisp32:
			e.u32(uint32(disp))
		}
		return nil
	}
	return fmt.Errorf("encode: operand is not a register or memory operand")
}

// op emits [66] [REX] opcode ModRM… for a reg-and-r/m instruction.
func (e *enc) op(sz int, regField byte, m Opr, opc ...byte) error {
	if sz == 2 {
		e.u8(x86_64.PrefixOperandSize)
	}
	e.rex(sz == 8, regField, m)
	e.u8(opc...)
	return e.memBody(regField, m)
}

func (e *enc) fixupJump(opcode byte, sym string) {
	e.u8(opcode) // E8 call rel32 / E9 jmp rel32
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupPCRel32Call, Addend: -4})
	e.u32(uint32(0xFFFFFFFC)) // -4 written into the field for REL-style consumers
}

func fitsI32(v int64) bool { return v >= -(1 << 31) && v < 1<<31 }
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
			return e.op(8, byte(d.Reg), Opr{K: KRIP, Sym: s.Sym, Disp: int32(s.Imm)}, x86_64.OpLea)
		case d.K == KReg && s.K == KImm:
			switch {
			case fitsU32(s.Imm): // mov r32, imm32 zero-extends to 64
				if x86_64.HiBit(d.Reg) != 0 {
					e.u8(0x41)
				}
				e.u8(x86_64.OpMovImmR + x86_64.LoBits(d.Reg))
				e.u32(uint32(s.Imm))
			case fitsI32(s.Imm): // REX.W C7 /0 sign-extends imm32
				e.rex(true, 0, R(d.Reg))
				e.u8(x86_64.OpMovImm32, x86_64.PackModRM(x86_64.ModReg, 0, x86_64.LoBits(d.Reg)))
				e.u32(uint32(s.Imm))
			default: // movabs REX.W B8+r imm64
				e.u8(0x48 | x86_64.HiBit(d.Reg))
				e.u8(x86_64.OpMovImmR + x86_64.LoBits(d.Reg))
				e.u64(uint64(s.Imm))
			}
		case d.K == KReg && s.K == KReg:
			return e.op(sz, byte(s.Reg), R(d.Reg), x86_64.OpMovRM)
		case d.K == KReg && (s.K == KMem || s.K == KRIP):
			return e.op(sz, byte(d.Reg), s, x86_64.OpMovMR)
		case (d.K == KMem || d.K == KRIP) && s.K == KReg:
			if sz == 1 {
				return e.op(4, byte(s.Reg), d, x86_64.OpMovRM8) // no REX.W; AL..BL sources only
			}
			return e.op(sz, byte(s.Reg), d, x86_64.OpMovRM)
		case d.K == KMem && s.K == KImm:
			if !fitsI32(s.Imm) {
				return fmt.Errorf("encode: mov m, imm beyond int32")
			}
			if err := e.op(sz, 0, d, x86_64.OpMovImm32); err != nil {
				return err
			}
			e.u32(uint32(s.Imm))
		default:
			return fmt.Errorf("encode: bad mov operands")
		}

	case "movzx":
		if sz == 4 { // mov r32, r/m32 zero-extends to 64 by definition
			return e.op(4, byte(in.D.Reg), in.S, x86_64.OpMovMR)
		}
		op2 := byte(x86_64.OpMovzx8)
		if sz == 2 {
			op2 = x86_64.OpMovzx16
		}
		return e.op(4, byte(in.D.Reg), in.S, 0x0F, op2)

	case "movsx":
		if sz == 4 { // movsxd r64, r/m32
			return e.op(8, byte(in.D.Reg), in.S, x86_64.OpMovsxd)
		}
		op2 := byte(x86_64.OpMovsx8)
		if sz == 2 {
			op2 = x86_64.OpMovsx16
		}
		return e.op(8, byte(in.D.Reg), in.S, 0x0F, op2) // sign-extend to 64

	case "lea":
		return e.op(8, byte(in.D.Reg), in.S, x86_64.OpLea)

	case "add", "or", "and", "sub", "xor", "cmp":
		alu := x86_64.ALUOpcodes[in.Op]
		d, s := in.D, in.S
		switch {
		case d.K == KReg && s.K == KReg:
			return e.op(sz, byte(s.Reg), R(d.Reg), alu.MR)
		case d.K == KReg && s.K == KMem:
			return e.op(sz, byte(d.Reg), s, alu.RM)
		case d.K == KMem && s.K == KReg:
			return e.op(sz, byte(s.Reg), d, alu.MR)
		case s.K == KImm:
			if !fitsI32(s.Imm) {
				return fmt.Errorf("encode: %s imm beyond int32 (materialize via mov first)", in.Op)
			}
			if err := e.op(sz, alu.Ext, d, 0x81); err != nil {
				return err
			}
			e.u32(uint32(s.Imm))
		default:
			return fmt.Errorf("encode: bad %s operands", in.Op)
		}

	case "test":
		return e.op(sz, byte(in.S.Reg), R(in.D.Reg), x86_64.OpTest)

	case "imul": // imul r, r/m
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, x86_64.OpImulRM)

	case "imul3": // imul r, r/m, imm32
		if !fitsI32(in.Imm) {
			return fmt.Errorf("encode: imul3 imm beyond int32")
		}
		if err := e.op(sz, byte(in.D.Reg), in.S, x86_64.OpImul3); err != nil {
			return err
		}
		e.u32(uint32(in.Imm))

	case "not", "neg", "mul1", "imul1", "div", "idiv":
		return e.op(sz, x86_64.Grp3Ext[in.Op], in.S, 0xF7)

	case "inc", "dec":
		return e.op(sz, x86_64.Grp5Ext[in.Op], in.S, 0xFF)

	case "cdq":
		e.u8(x86_64.OpCdq)
	case "cqo":
		e.u8(x86_64.PackREX(true, false, false, false), x86_64.OpCdq)

	case "shl", "shr", "sar", "rol", "ror":
		ext := x86_64.ShiftExt[in.Op]
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

	case "setcc": // 0F 9x /0, low byte of d (AL..BL without REX, in this bring-up)
		if in.D.Reg > x86_64.RBX {
			return fmt.Errorf("encode: setcc needs AL..BL in this bring-up")
		}
		e.u8(0x0F, x86_64.OpSetccBase+in.CC, x86_64.PackModRM(x86_64.ModReg, 0, x86_64.LoBits(in.D.Reg)))

	case "cmovcc": // 0F 4x /r
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, x86_64.OpCmovccBase+in.CC)

	case "jmp":
		e.u8(x86_64.OpJmpRel32)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(0)

	case "jcc":
		e.u8(0x0F, x86_64.OpJccBase+in.CC)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(0)

	case "jmp_sym":
		e.fixupJump(x86_64.OpJmpRel32, in.Sym)

	case "call_sym":
		e.fixupJump(x86_64.OpCallRel32, in.Sym)

	case "call_r":
		if x86_64.HiBit(in.S.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0xFF, x86_64.PackModRM(x86_64.ModReg, x86_64.OpCallRegExt, x86_64.LoBits(in.S.Reg)))

	case "jmp_r":
		if x86_64.HiBit(in.S.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0xFF, x86_64.PackModRM(x86_64.ModReg, x86_64.OpJmpRegExt, x86_64.LoBits(in.S.Reg)))

	case "push": // 64-bit, no REX.W needed
		if x86_64.HiBit(in.S.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(x86_64.OpPushBase + x86_64.LoBits(in.S.Reg))

	case "pop":
		if x86_64.HiBit(in.D.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(x86_64.OpPopBase + x86_64.LoBits(in.D.Reg))

	case "ret":
		e.u8(x86_64.OpRet)

	case "ud2":
		e.u8(0x0F, x86_64.OpUD2Lo)

	case "nop":
		e.u8(x86_64.OpNop)

	case "syscall":
		e.u8(0x0F, x86_64.OpSyscallLo)

	case "bsr":
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, x86_64.OpBsr)

	case "bsf":
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, x86_64.OpBsf)

	case "bswap":
		if sz == 8 {
			e.u8(0x48 | x86_64.HiBit(in.D.Reg))
		} else if x86_64.HiBit(in.D.Reg) != 0 {
			e.u8(0x41)
		}
		e.u8(0x0F, x86_64.OpBswapBase+x86_64.LoBits(in.D.Reg))

	case "xchg": // xchg r/m, r — implicitly locked with a memory operand
		return e.op(sz, byte(in.S.Reg), in.D, x86_64.OpXchg)

	case "lock_xadd":
		e.u8(x86_64.PrefixLock) // LOCK precedes REX
		return e.op(sz, byte(in.S.Reg), in.D, 0x0F, x86_64.OpLockXadd)

	case "lock_cmpxchg":
		e.u8(x86_64.PrefixLock)
		return e.op(sz, byte(in.S.Reg), in.D, 0x0F, x86_64.OpLockCmpxchg)

	case "mfence":
		e.u8(0x0F, x86_64.OpMfence, 0xF0)

	case "cld":
		e.u8(x86_64.OpCld)
	case "std":
		e.u8(x86_64.OpStd)
	case "rep_movsb":
		e.u8(x86_64.PrefixRep, x86_64.OpRepMovsb)
	case "rep_stosb":
		e.u8(x86_64.PrefixRep, x86_64.OpRepStosb)

	case "popcnt": // F3 [REX] 0F B8 /r
		e.u8(x86_64.PrefixRep) // mandatory prefix precedes REX
		return e.op(sz, byte(in.D.Reg), in.S, 0x0F, x86_64.OpPopcntLo)

	default:
		return fmt.Errorf("encode: unknown Inst op %q", in.Op)
	}
	return nil
}