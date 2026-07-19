package mcode

import "fmt"

// Encode turns a resolved Inst stream (all OSlot operands already rewritten
// to OMem by package regalloc) into IA-32 machine bytes.
//
// Encoding reference points: one-byte opcodes + optional 0F map, ModRM
// (mod/reg/rm), SIB required whenever an index register is present or
// rm=100 (ESP base), mod=00 rm=101 meaning [disp32] absolute, mod=00 with
// an EBP base (or EBP as SIB base) being unavailable (forces disp8/disp32).
// No REX — this is 32-bit mode.
func Encode(insts []Inst, localBytes int32) ([]byte, []Fixup, error) {
	e := &enc{labels: map[string]int{}}
	e.prologue(localBytes)
	for i := range insts {
		if err := e.one(&insts[i], localBytes); err != nil {
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
func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
func modrm(mod, reg, rm byte) byte { return mod<<6 | reg<<3 | rm }

// mem emits the ModRM (+SIB, +disp) bytes addressing operand m, with
// regField in ModRM.reg. Handles the IA-32 special cases: an index or an
// ESP base forces a SIB byte; mod=00 rm=101 (no SIB) or mod=00 SIB-base=101
// (with SIB) both mean "no base, disp32 follows"; an EBP base always
// carries a displacement (mod can never be 0 when the base is EBP).
func (e *enc) mem(regField byte, m Opr) error {
	if m.Kind == OReg {
		e.u8(modrm(3, regField, byte(m.Reg)))
		return nil
	}
	if m.Kind != OMem {
		return fmt.Errorf("encode: operand is not a memory operand")
	}
	hasBase := m.Base != RNone
	hasIndex := m.Index != RNone
	if !hasBase && !hasIndex {
		e.u8(modrm(0, regField, 5)) // [disp32], absolute
		if m.MSym != "" {
			e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: m.MSym, Kind: FixupAbs32, Addend: int64(m.Disp)})
		}
		e.u32(uint32(m.Disp))
		return nil
	}
	if m.Index == RESP {
		return fmt.Errorf("encode: esp cannot be used as a SIB index register")
	}
	needSIB := hasIndex || m.Base == RESP
	var mod byte
	switch {
	case !hasBase:
		mod = 0 // [index*scale+disp32], no base
	case m.Disp == 0 && m.Base != REBP:
		mod = 0
	case m.Disp >= -128 && m.Disp <= 127:
		mod = 1
	default:
		mod = 2
	}
	rm := byte(4) // SIB marker
	if !needSIB {
		rm = byte(m.Base)
	}
	e.u8(modrm(mod, regField, rm))
	if needSIB {
		var scaleBits byte
		switch m.Scale {
		case 0, 1:
			scaleBits = 0
		case 2:
			scaleBits = 1
		case 4:
			scaleBits = 2
		case 8:
			scaleBits = 3
		default:
			return fmt.Errorf("encode: scale %d is not 1, 2, 4, or 8", m.Scale)
		}
		indexField := byte(4) // "no index" SIB encoding
		if hasIndex {
			indexField = byte(m.Index)
		}
		baseField := byte(5) // "no base" SIB encoding (forces trailing disp32)
		if hasBase {
			baseField = byte(m.Base)
		}
		e.u8(scaleBits<<6 | indexField<<3 | baseField)
		if !hasBase {
			e.u32(uint32(m.Disp))
			return nil
		}
	}
	switch mod {
	case 1:
		e.u8(byte(int8(m.Disp)))
	case 2:
		e.u32(uint32(m.Disp))
	}
	return nil
}

func (e *enc) prologue(localBytes int32) {
	e.u8(0x55)       // push ebp
	e.u8(0x89, 0xE5) // mov ebp, esp
	e.u8(0x53)       // push ebx
	e.u8(0x56)       // push esi
	e.u8(0x57)       // push edi
	if localBytes > 0 {
		e.u8(0x81, modrm(3, 5, byte(RESP))) // sub esp, imm32
		e.u32(uint32(localBytes))
	}
}

func (e *enc) epilogue() {
	e.u8(0x8D, modrm(1, byte(RESP), byte(REBP)), byte(int8(-SavedRegBytes))) // lea esp, [ebp-12]
	e.u8(0x5F)                                                               // pop edi
	e.u8(0x5E)                                                               // pop esi
	e.u8(0x5B)                                                               // pop ebx
	e.u8(0x5D)                                                               // pop ebp
}

func (e *enc) callFix(opcode byte, sym string) {
	e.u8(opcode)
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupPCRel32, Addend: -4})
	e.u32(uint32(0xFFFFFFFC))
}

var aluEnc = map[string]struct{ mr, rm, ext byte }{
	"add": {0x01, 0x03, 0}, "or": {0x09, 0x0B, 1}, "and": {0x21, 0x23, 4},
	"sub": {0x29, 0x2B, 5}, "xor": {0x31, 0x33, 6}, "cmp": {0x39, 0x3B, 7},
}
var shiftExt = map[string]byte{"rol": 0, "ror": 1, "shl": 4, "shr": 5, "sar": 7}
var grp3Ext = map[string]byte{"not": 2, "neg": 3, "mul32": 4, "imul32": 5, "div": 6, "idiv": 7}

func (e *enc) one(in *Inst, localBytes int32) error {
	switch in.Op {
	case "label":
		e.labels[in.Lbl] = len(e.b)

	case "mov":
		d, s := in.D, in.S
		switch {
		case d.Kind == OReg && s.Kind == OImm && s.Sym != "":
			e.u8(0xB8 + byte(d.Reg))
			e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: s.Sym, Kind: FixupAbs32, Addend: s.Imm})
			e.u32(uint32(s.Imm))
		case d.Kind == OReg && s.Kind == OImm:
			e.u8(0xB8 + byte(d.Reg))
			e.u32(uint32(s.Imm))
		case d.Kind == OReg && s.Kind == OReg:
			e.u8(0x89, modrm(3, byte(s.Reg), byte(d.Reg)))
		case d.Kind == OReg && s.Kind == OMem:
			e.u8(0x8B)
			return e.mem(byte(d.Reg), s)
		case d.Kind == OMem && s.Kind == OReg:
			switch in.Sz {
			case 1:
				e.u8(0x88)
			case 2:
				e.u8(0x66, 0x89)
			default:
				e.u8(0x89)
			}
			return e.mem(byte(s.Reg), d)
		case d.Kind == OMem && s.Kind == OImm:
			e.u8(0xC7)
			if err := e.mem(0, d); err != nil {
				return err
			}
			e.u32(uint32(s.Imm))
		default:
			return fmt.Errorf("encode: bad mov operands")
		}

	case "movzx", "movsx":
		op2 := byte(0xB6)
		if in.Sz == 2 {
			op2 = 0xB7
		}
		if in.Op == "movsx" {
			op2 = 0xBE
			if in.Sz == 2 {
				op2 = 0xBF
			}
		}
		e.u8(0x0F, op2)
		return e.mem(byte(in.D.Reg), in.S)

	case "lea":
		e.u8(0x8D)
		return e.mem(byte(in.D.Reg), in.S)

	case "add", "or", "and", "sub", "xor", "cmp":
		enc := aluEnc[in.Op]
		d, s := in.D, in.S
		switch {
		case d.Kind == OReg && s.Kind == OReg:
			e.u8(enc.mr, modrm(3, byte(s.Reg), byte(d.Reg)))
		case d.Kind == OReg && s.Kind == OMem:
			e.u8(enc.rm)
			return e.mem(byte(d.Reg), s)
		case d.Kind == OMem && s.Kind == OReg:
			e.u8(enc.mr)
			return e.mem(byte(s.Reg), d)
		case s.Kind == OImm:
			e.u8(0x81)
			if err := e.mem(enc.ext, d); err != nil {
				return err
			}
			e.u32(uint32(s.Imm))
		default:
			return fmt.Errorf("encode: bad %s operands", in.Op)
		}

	case "test":
		if in.D.Kind != OReg || in.S.Kind != OReg {
			return fmt.Errorf("encode: test requires two registers")
		}
		e.u8(0x85, modrm(3, byte(in.S.Reg), byte(in.D.Reg)))

	case "imul":
		e.u8(0x0F, 0xAF)
		return e.mem(byte(in.D.Reg), in.S)

	case "imul3":
		e.u8(0x69)
		if err := e.mem(byte(in.D.Reg), in.S); err != nil {
			return err
		}
		e.u32(uint32(in.Imm))

	case "not", "neg", "mul32", "imul32", "div", "idiv":
		e.u8(0xF7)
		return e.mem(grp3Ext[in.Op], in.S)

	case "inc":
		e.u8(0x40 + byte(in.D.Reg))
	case "dec":
		e.u8(0x48 + byte(in.D.Reg))

	case "cdq":
		e.u8(0x99)

	case "shl", "shr", "sar", "rol", "ror":
		ext := shiftExt[in.Op]
		if in.S.Kind == OImm {
			switch in.Sz {
			case 1:
				e.u8(0xC0)
			case 2:
				e.u8(0x66, 0xC1)
			default:
				e.u8(0xC1)
			}
			if err := e.mem(ext, in.D); err != nil {
				return err
			}
			e.u8(byte(in.S.Imm))
		} else { // by CL
			switch in.Sz {
			case 1:
				e.u8(0xD2)
			case 2:
				e.u8(0x66, 0xD3)
			default:
				e.u8(0xD3)
			}
			return e.mem(ext, in.D)
		}

	case "setcc":
		if in.D.Reg > REBX {
			return fmt.Errorf("encode: setcc needs a byte-addressable register")
		}
		e.u8(0x0F, 0x90+in.CC, modrm(3, 0, byte(in.D.Reg)))

	case "cmovcc":
		e.u8(0x0F, 0x40+in.CC)
		return e.mem(byte(in.D.Reg), in.S)

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
		e.u8(0xFF, modrm(3, 2, byte(in.S.Reg)))

	case "push":
		if in.S.Kind == OImm {
			e.u8(0x68)
			e.u32(uint32(in.S.Imm))
			break
		}
		if in.S.Kind != OReg {
			return fmt.Errorf("encode: push only supports a register or immediate operand")
		}
		e.u8(0x50 + byte(in.S.Reg))

	case "pop":
		if in.D.Kind != OReg {
			return fmt.Errorf("encode: pop only supports a register operand")
		}
		e.u8(0x58 + byte(in.D.Reg))

	case "ret":
		e.u8(0xC3)

	case "ud2":
		e.u8(0x0F, 0x0B)

	case "int":
		e.u8(0xCD, byte(in.Imm))

	case "nop":
		e.u8(0x90)

	case "bsr":
		e.u8(0x0F, 0xBD)
		return e.mem(byte(in.D.Reg), in.S)

	case "bsf":
		e.u8(0x0F, 0xBC)
		return e.mem(byte(in.D.Reg), in.S)

	case "bswap":
		e.u8(0x0F, 0xC8+byte(in.D.Reg))

	case "xchg":
		e.u8(0x87)
		return e.mem(byte(in.S.Reg), in.D)

	case "lock_xadd":
		e.u8(0xF0, 0x0F, 0xC1)
		return e.mem(byte(in.S.Reg), in.D)

	case "lock_cmpxchg":
		e.u8(0xF0, 0x0F, 0xB1)
		return e.mem(byte(in.S.Reg), in.D)

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

	case "popcnt": // F3 0F B8 /r — tier-gated (§10.4), TODO
		e.u8(0xF3, 0x0F, 0xB8)
		return e.mem(byte(in.D.Reg), in.S)

	case "epi_ret":
		e.epilogue()
		e.u8(0xC3)

	case "epi_jmp_sym":
		e.epilogue()
		e.callFix(0xE9, in.Sym)

	case "epi_jmp_r":
		e.epilogue()
		e.u8(0xFF, modrm(3, 4, byte(in.S.Reg)))

	default:
		return fmt.Errorf("encode: unknown inst op %q", in.Op)
	}
	return nil
}