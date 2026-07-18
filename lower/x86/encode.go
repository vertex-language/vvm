package x86

import "fmt"

// encodeFunc turns the resolved minst stream into IA-32 machine bytes.
//
// Encoding reference points: one-byte opcodes + optional 0F map, ModRM
// (mod/reg/rm), SIB required when rm=100 in a memory form (ESP base),
// mod=00 rm=101 meaning [disp32] absolute, and mod=00 with an EBP base being
// unavailable (forces disp8/disp32). No REX — this is 32-bit mode.
func encodeFunc(insts []minst, localBytes int32) ([]byte, []Fixup, error) {
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

func (e *enc) u8(v ...byte)  { e.b = append(e.b, v...) }
func (e *enc) u32(v uint32)  { e.b = append(e.b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
func modrm(mod, reg, rm byte) byte { return mod<<6 | reg<<3 | rm }

// mem emits the ModRM (+SIB, +disp) bytes addressing operand m, with regField
// in ModRM.reg. Handles the two IA-32 special cases: rm=100 requires a SIB
// byte (ESP base -> SIB 0x24), and mod=00 rm=101 means [disp32] absolute, so
// an EBP base always carries a displacement.
func (e *enc) mem(regField byte, m opr) error {
	if m.k == oReg { // register-direct form
		e.u8(modrm(3, regField, byte(m.reg)))
		return nil
	}
	if m.k != oMem {
		return fmt.Errorf("encode: operand is not a memory operand")
	}
	if m.base == rNone { // absolute [sym+disp] / [disp32]
		e.u8(modrm(0, regField, 5))
		if m.msym != "" {
			e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: m.msym, Kind: FixupAbs32, Addend: int64(m.disp)})
		}
		e.u32(uint32(m.disp))
		return nil
	}
	disp := m.disp
	var mod byte
	switch {
	case disp == 0 && m.base != rEBP:
		mod = 0
	case disp >= -128 && disp <= 127:
		mod = 1
	default:
		mod = 2
	}
	e.u8(modrm(mod, regField, byte(m.base)))
	if m.base == rESP {
		e.u8(0x24) // SIB: scale=0, index=none(100), base=ESP
	}
	switch mod {
	case 1:
		e.u8(byte(int8(disp)))
	case 2:
		e.u32(uint32(disp))
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
		e.u8(0x81, modrm(3, 5, byte(rESP))) // sub esp, imm32
		e.u32(uint32(localBytes))
	}
}

func (e *enc) epilogue() {
	e.u8(0x8D, modrm(1, byte(rESP), byte(rEBP)), byte(int8(-savedRegBytes))) // lea esp, [ebp-12]
	e.u8(0x5F)       // pop edi
	e.u8(0x5E)       // pop esi
	e.u8(0x5B)       // pop ebx
	e.u8(0x5D)       // pop ebp
}

func (e *enc) callFix(opcode byte, sym string) {
	e.u8(opcode) // E8 call rel32 / E9 jmp rel32
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupPCRel32, Addend: -4})
	e.u32(uint32(0xFFFFFFFC)) // -4 written into the field for REL-style consumers
}

var aluEnc = map[string]struct{ mr, rm, ext byte }{
	"add": {0x01, 0x03, 0}, "or": {0x09, 0x0B, 1}, "and": {0x21, 0x23, 4},
	"sub": {0x29, 0x2B, 5}, "xor": {0x31, 0x33, 6}, "cmp": {0x39, 0x3B, 7},
}
var shiftExt = map[string]byte{"rol": 0, "ror": 1, "shl": 4, "shr": 5, "sar": 7}
var grp3Ext = map[string]byte{"not": 2, "neg": 3, "mul32": 4, "imul32": 5, "div": 6, "idiv": 7}

func (e *enc) one(in *minst, localBytes int32) error {
	switch in.op {
	case "label":
		e.labels[in.lbl] = len(e.b)

	case "mov":
		d, s := in.d, in.s
		switch {
		case d.k == oReg && s.k == oImm && s.sym != "":
			e.u8(0xB8 + byte(d.reg))
			e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: s.sym, Kind: FixupAbs32, Addend: s.imm})
			e.u32(uint32(s.imm))
		case d.k == oReg && s.k == oImm:
			e.u8(0xB8 + byte(d.reg))
			e.u32(uint32(s.imm))
		case d.k == oReg && s.k == oReg:
			e.u8(0x89, modrm(3, byte(s.reg), byte(d.reg)))
		case d.k == oReg && s.k == oMem:
			e.u8(0x8B)
			return e.mem(byte(d.reg), s)
		case d.k == oMem && s.k == oReg:
			switch in.sz {
			case 1:
				e.u8(0x88)
			case 2:
				e.u8(0x66, 0x89)
			default:
				e.u8(0x89)
			}
			return e.mem(byte(s.reg), d)
		case d.k == oMem && s.k == oImm:
			e.u8(0xC7)
			if err := e.mem(0, d); err != nil {
				return err
			}
			e.u32(uint32(s.imm))
		default:
			return fmt.Errorf("encode: bad mov operands")
		}

	case "movzx", "movsx":
		op2 := byte(0xB6) // movzx r32, r/m8
		if in.sz == 2 {
			op2 = 0xB7
		}
		if in.op == "movsx" {
			op2 = 0xBE
			if in.sz == 2 {
				op2 = 0xBF
			}
		}
		e.u8(0x0F, op2)
		return e.mem(byte(in.d.reg), in.s)

	case "lea":
		e.u8(0x8D)
		return e.mem(byte(in.d.reg), in.s)

	case "add", "or", "and", "sub", "xor", "cmp":
		enc := aluEnc[in.op]
		d, s := in.d, in.s
		switch {
		case d.k == oReg && s.k == oReg:
			e.u8(enc.mr, modrm(3, byte(s.reg), byte(d.reg)))
		case d.k == oReg && s.k == oMem:
			e.u8(enc.rm)
			return e.mem(byte(d.reg), s)
		case d.k == oMem && s.k == oReg:
			e.u8(enc.mr)
			return e.mem(byte(s.reg), d)
		case s.k == oImm:
			e.u8(0x81)
			if err := e.mem(enc.ext, d); err != nil {
				return err
			}
			e.u32(uint32(s.imm))
		default:
			return fmt.Errorf("encode: bad %s operands", in.op)
		}

	case "test":
		e.u8(0x85, modrm(3, byte(in.s.reg), byte(in.d.reg)))

	case "imul": // imul r32, r/m32
		e.u8(0x0F, 0xAF)
		return e.mem(byte(in.d.reg), in.s)

	case "imul3": // imul r32, r/m32, imm32
		e.u8(0x69)
		if err := e.mem(byte(in.d.reg), in.s); err != nil {
			return err
		}
		e.u32(uint32(in.imm))

	case "not", "neg", "mul32", "imul32", "div", "idiv":
		e.u8(0xF7)
		return e.mem(grp3Ext[in.op], in.s)

	case "cdq":
		e.u8(0x99)

	case "shl", "shr", "sar", "rol", "ror":
		ext := shiftExt[in.op]
		if in.s.k == oImm { // Cx /ext ib
			switch in.sz {
			case 1:
				e.u8(0xC0)
			case 2:
				e.u8(0x66, 0xC1)
			default:
				e.u8(0xC1)
			}
			if err := e.mem(ext, in.d); err != nil {
				return err
			}
			e.u8(byte(in.s.imm))
		} else { // by CL: Dx /ext
			switch in.sz {
			case 1:
				e.u8(0xD2)
			case 2:
				e.u8(0x66, 0xD3)
			default:
				e.u8(0xD3)
			}
			return e.mem(ext, in.d)
		}

	case "setcc": // 0F 9x /0, low byte of d (AL..BL only)
		if in.d.reg > rEBX {
			return fmt.Errorf("encode: setcc needs a byte-addressable register")
		}
		e.u8(0x0F, 0x90+in.cc, modrm(3, 0, byte(in.d.reg)))

	case "cmovcc": // 0F 4x /r (P6+; part of x86 baseline here — tier note in README)
		e.u8(0x0F, 0x40+in.cc)
		return e.mem(byte(in.d.reg), in.s)

	case "jmp":
		e.u8(0xE9)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.lbl})
		e.u32(0)

	case "jcc":
		e.u8(0x0F, 0x80+in.cc)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.lbl})
		e.u32(0)

	case "call_sym":
		e.callFix(0xE8, in.sym)

	case "call_r":
		e.u8(0xFF, modrm(3, 2, byte(in.s.reg)))

	case "push":
		e.u8(0x50 + byte(in.s.reg))

	case "pop":
		e.u8(0x58 + byte(in.d.reg))

	case "ret":
		e.u8(0xC3)

	case "ud2":
		e.u8(0x0F, 0x0B)

	case "bsr":
		e.u8(0x0F, 0xBD)
		return e.mem(byte(in.d.reg), in.s)

	case "bsf":
		e.u8(0x0F, 0xBC)
		return e.mem(byte(in.d.reg), in.s)

	case "bswap":
		e.u8(0x0F, 0xC8+byte(in.d.reg))

	case "xchg": // xchg r/m32, r32 — implicitly locked with a memory operand
		e.u8(0x87)
		return e.mem(byte(in.s.reg), in.d)

	case "lock_xadd":
		e.u8(0xF0, 0x0F, 0xC1)
		return e.mem(byte(in.s.reg), in.d)

	case "lock_cmpxchg":
		e.u8(0xF0, 0x0F, 0xB1)
		return e.mem(byte(in.s.reg), in.d)

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
		return e.mem(byte(in.d.reg), in.s)

	case "epi_ret":
		e.epilogue()
		e.u8(0xC3)

	case "epi_jmp_sym":
		e.epilogue()
		e.callFix(0xE9, in.sym)

	case "epi_jmp_r":
		e.epilogue()
		e.u8(0xFF, modrm(3, 4, byte(in.s.reg)))

	default:
		return fmt.Errorf("encode: unknown minst op %q", in.op)
	}
	return nil
}