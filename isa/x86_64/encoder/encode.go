// encoder/encode.go
package encoder

import (
	"fmt"

	isax64 "github.com/vertex-language/vvm/isa/x86_64"
)

func Encode(insts []Inst) ([]byte, []Fixup, error) {
	e := &enc{labels: map[string]int{}}
	for i := range insts {
		if err := e.one(&insts[i]); err != nil {
			return nil, nil, fmt.Errorf("encode: %s: %w", insts[i].Op, err)
		}
	}
	for _, p := range e.patches {
		t, ok := e.labels[p.lbl]
		if !ok {
			return nil, nil, fmt.Errorf("encode: undefined label %q", p.lbl)
		}
		putLE32(e.b[p.pos:], uint32(int32(t-(p.pos+4))))
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
func (e *enc) u64(v uint64) {
	e.b = append(e.b,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}
func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

func width(sz int) (int, error) {
	switch sz {
	case 0, 8:
		return 8, nil
	case 4:
		return 4, nil
	case 2:
		return 2, nil
	case 1:
		return 1, nil
	}
	return 0, fmt.Errorf("operand size %d is not 1, 2, 4, or 8", sz)
}

func (e *enc) sizePrefix(w int) {
	if w == 2 {
		e.u8(isax64.Prefix66)
	}
}

func reg(o Opr, role string) (Reg, error) {
	if o.Kind != OReg {
		return 0, fmt.Errorf("%s operand must be a register", role)
	}
	if !o.Reg.IsGPR() {
		return 0, fmt.Errorf("%s operand names no encodable register", role)
	}
	return o.Reg, nil
}

type rexNeed struct {
	w    bool 
	r    bool 
	x    bool 
	b    bool 
	must bool 
	no   bool 
}

func (rn rexNeed) emit(e *enc) error {
	if rn.no && (rn.w || rn.r || rn.x || rn.b || rn.must) {
		return fmt.Errorf("instruction needs a REX prefix but also uses ah/ch/dh/bh, which forbids one")
	}
	if rn.no {
		return nil
	}
	if rn.w || rn.r || rn.x || rn.b || rn.must {
		e.u8(isax64.PackREX(rn.w, rn.r, rn.x, rn.b))
	}
	return nil
}

func (rn *rexNeed) byteRegREX(r Reg) {
	switch r {
	case RRSP, RRBP, RRSI, RRDI: 
		rn.must = true
	case RRAX, RRCX, RRDX, RRBX:
	default:
	}
}

func (rn *rexNeed) memREX(m Opr) {
	if m.Kind == OReg {
		rn.b = m.Reg.NeedsREXBit()
		return
	}
	if m.Base != RNone {
		rn.b = m.Base.NeedsREXBit()
	}
	if m.Index != RNone {
		rn.x = m.Index.NeedsREXBit()
	}
}

func (e *enc) imm(w int, o Opr) error {
	if o.Sym != "" {
		if w != 4 {
			return fmt.Errorf("symbolic immediate %q needs a 4-byte field, got %d", o.Sym, w)
		}
		e.fx = append(e.fx, Fixup{
			Offset: uint32(len(e.b)), Symbol: o.Sym, Kind: FixupAbs32, Addend: o.Imm,
		})
	}
	switch w {
	case 1:
		e.u8(byte(o.Imm))
	case 2:
		e.u8(byte(o.Imm), byte(o.Imm>>8))
	default:
		e.u32(uint32(o.Imm))
	}
	return nil
}

func (e *enc) mem(regField byte, m Opr) error {
	if m.Kind == OReg {
		if !m.Reg.IsGPR() {
			return fmt.Errorf("r/m operand names no encodable register")
		}
		e.u8(isax64.PackModRM(isax64.ModReg, regField, m.Reg.Low3()))
		return nil
	}
	if m.Kind != OMem {
		return fmt.Errorf("operand is not a memory operand")
	}

	if m.RIPSym != "" {
		if m.Base != RNone || m.Index != RNone {
			return fmt.Errorf("RIP-relative operand %q cannot carry a base or index", m.RIPSym)
		}
		e.u8(isax64.PackModRM(isax64.ModIndir, regField, isax64.RMRIP))
		e.fx = append(e.fx, Fixup{
			Offset: uint32(len(e.b)), Symbol: m.RIPSym,
			Kind: FixupPCRel32, Addend: int64(m.Disp) - 4,
		})
		e.u32(uint32(0xFFFFFFFC))
		return nil
	}

	hasBase := m.Base != RNone
	hasIndex := m.Index != RNone
	if hasBase && !m.Base.IsGPR() {
		return fmt.Errorf("memory base names no encodable register")
	}
	if hasIndex && !m.Index.IsGPR() {
		return fmt.Errorf("memory index names no encodable register")
	}

	if !hasBase && !hasIndex {
		e.u8(isax64.PackModRM(isax64.ModIndir, regField, isax64.RMSIB))
		e.u8(isax64.PackSIB(0, isax64.SIBNoIndex, isax64.SIBNoBase))
		if m.MSym != "" {
			e.fx = append(e.fx, Fixup{
				Offset: uint32(len(e.b)), Symbol: m.MSym,
				Kind: FixupAbs32, Addend: int64(m.Disp),
			})
		}
		e.u32(uint32(m.Disp))
		return nil
	}
	if m.MSym != "" {
		return fmt.Errorf("symbolic absolute operand %q cannot carry a base or index", m.MSym)
	}
	if m.Index == RRSP {
		return fmt.Errorf("rsp cannot be used as a SIB index register")
	}

	needSIB := hasIndex || m.Base.Low3() == isax64.RMSIB
	baseIsBPLike := hasBase && m.Base.Low3() == isax64.RMRIP

	var mod byte
	switch {
	case !hasBase:
		mod = isax64.ModIndir
	case m.Disp == 0 && !baseIsBPLike:
		mod = isax64.ModIndir
	case isax64.FitsDisp8(m.Disp):
		mod = isax64.ModDisp8
	default:
		mod = isax64.ModDisp32
	}

	rm := isax64.RMSIB
	if !needSIB {
		rm = m.Base.Low3()
	}
	e.u8(isax64.PackModRM(mod, regField, rm))

	if needSIB {
		scaleBits, ok := isax64.ScaleBits(m.Scale)
		if !ok {
			return fmt.Errorf("scale %d is not 1, 2, 4, or 8", m.Scale)
		}
		indexField := isax64.SIBNoIndex
		if hasIndex {
			indexField = m.Index.Low3()
		}
		baseField := isax64.SIBNoBase
		if hasBase {
			baseField = m.Base.Low3()
		}
		e.u8(isax64.PackSIB(scaleBits, indexField, baseField))
		if !hasBase {
			e.u32(uint32(m.Disp))
			return nil
		}
	}
	switch mod {
	case isax64.ModDisp8:
		e.u8(byte(int8(m.Disp)))
	case isax64.ModDisp32:
		e.u32(uint32(m.Disp))
	}
	return nil
}

func (e *enc) relFix(opcode byte, sym string) error {
	if sym == "" {
		return fmt.Errorf("no target symbol")
	}
	e.u8(opcode)
	e.fx = append(e.fx, Fixup{
		Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupPCRel32, Addend: -4,
	})
	e.u32(uint32(0xFFFFFFFC))
	return nil
}

func (e *enc) one(in *Inst) error {
	w, err := width(in.Sz)
	if err != nil {
		return err
	}

	switch in.Op {
	case "label":
		if in.Lbl == "" {
			return fmt.Errorf("label has no name")
		}
		if _, dup := e.labels[in.Lbl]; dup {
			return fmt.Errorf("label %q defined twice", in.Lbl)
		}
		e.labels[in.Lbl] = len(e.b)

	case "mov":
		return e.mov(in, w)

	case "movabs":
		return e.movabs(in)

	case "movzx", "movsx":
		if in.Sz != 1 && in.Sz != 2 {
			return fmt.Errorf("source width must be 1 or 2, got %d", in.Sz)
		}
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = true 
		rn.r = dr.NeedsREXBit()
		rn.memREX(in.S)
		if in.Sz == 1 && in.S.Kind == OReg {
			if in.S.Reg == RRAX || in.S.Reg == RRCX || in.S.Reg == RRDX || in.S.Reg == RRBX {
			} else {
				rn.byteRegREX(in.S.Reg)
			}
		}
		if err := rn.emit(e); err != nil {
			return err
		}
		op2 := byte(0xB6) 
		switch {
		case in.Op == "movzx" && in.Sz == 2:
			op2 = 0xB7
		case in.Op == "movsx" && in.Sz == 1:
			op2 = 0xBE
		case in.Op == "movsx" && in.Sz == 2:
			op2 = 0xBF
		}
		e.u8(0x0F, op2)
		return e.mem(dr.Low3(), in.S)

	case "lea":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		if in.S.Kind != OMem {
			return fmt.Errorf("source must be a memory operand")
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(in.S)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x8D)
		return e.mem(dr.Low3(), in.S)

	case "add", "or", "and", "sub", "xor", "cmp":
		return e.alu(in, w)

	case "test":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit() 
		rn.b = dr.NeedsREXBit()
		if w == 1 {
			rn.byteRegREX(sr)
			rn.byteRegREX(dr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		op := byte(0x85)
		if w == 1 {
			op = 0x84
		}
		e.u8(op, isax64.PackModRM(isax64.ModReg, sr.Low3(), dr.Low3()))

	case "imul2":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(in.S)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(isax64.Imul2Esc, isax64.Imul2Op)
		return e.mem(dr.Low3(), in.S)

	case "imul3":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(in.S)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if isax64.FitsImm8(in.Imm) {
			e.u8(isax64.Imul3Imm8)
			if err := e.mem(dr.Low3(), in.S); err != nil {
				return err
			}
			e.u8(byte(in.Imm))
			break
		}
		e.u8(isax64.Imul3Imm32)
		if err := e.mem(dr.Low3(), in.S); err != nil {
			return err
		}
		return e.imm(4, Imm(in.Imm))

	case "not", "neg", "mul", "imul1", "div", "idiv":
		name := in.Op
		if name == "imul1" {
			name = "imul"
		}
		g3, ok := isax64.Group3ByName(name)
		if !ok || g3.HasImm {
			return fmt.Errorf("not a single-operand group-3 instruction")
		}
		var rn rexNeed
		rn.w = w == 8
		rn.memREX(in.S)
		if w == 1 && in.S.Kind == OReg {
			rn.byteRegREX(in.S.Reg)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(isax64.Group3Byte)
		} else {
			e.u8(isax64.Group3)
		}
		return e.mem(g3.Ext, in.S)

	case "inc", "dec":
		var rn rexNeed
		rn.w = w == 8
		rn.memREX(in.D)
		if w == 1 && in.D.Kind == OReg {
			rn.byteRegREX(in.D.Reg)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(0xFE)
		} else {
			e.u8(0xFF)
		}
		ext := byte(0)
		if in.Op == "dec" {
			ext = 1
		}
		return e.mem(ext, in.D)

	case "cqo":
		if w == 8 {
			e.u8(isax64.PackREX(true, false, false, false))
		} else {
			e.sizePrefix(w)
		}
		e.u8(0x99)

	case "shl", "shr", "sar", "rol", "ror":
		sh, ok := isax64.ShiftByName(in.Op)
		if !ok {
			return fmt.Errorf("unknown shift op")
		}
		var rn rexNeed
		rn.w = w == 8
		rn.memREX(in.D)
		if w == 1 && in.D.Kind == OReg {
			rn.byteRegREX(in.D.Reg)
		}
		e.sizePrefix(w)
		if in.S.Kind == OImm {
			if in.S.Sym != "" {
				return fmt.Errorf("shift count cannot be a symbol")
			}
			if in.S.Imm == 1 {
				if err := rn.emit(e); err != nil {
					return err
				}
				if w == 1 {
					e.u8(isax64.ShiftOneB)
				} else {
					e.u8(isax64.ShiftOne)
				}
				return e.mem(sh.Ext, in.D)
			}
			if err := rn.emit(e); err != nil {
				return err
			}
			if w == 1 {
				e.u8(isax64.ShiftImm8B)
			} else {
				e.u8(isax64.ShiftImm8)
			}
			if err := e.mem(sh.Ext, in.D); err != nil {
				return err
			}
			e.u8(byte(in.S.Imm))
			break
		}
		if _, err := reg(in.S, "count"); err != nil {
			return err
		}
		if in.S.Reg != RRCX {
			return fmt.Errorf("variable shift count must be in cl")
		}
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(isax64.ShiftCLB)
		} else {
			e.u8(isax64.ShiftCL)
		}
		return e.mem(sh.Ext, in.D)

	case "setcc":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.b = dr.NeedsREXBit()
		rn.byteRegREX(dr)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x90+in.CC, isax64.PackModRM(isax64.ModReg, 0, dr.Low3()))

	case "cmovcc":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(in.S)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x40+in.CC)
		return e.mem(dr.Low3(), in.S)

	case "jmp":
		if in.Lbl == "" {
			return fmt.Errorf("no target label")
		}
		e.u8(0xE9)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(0)

	case "jcc":
		if in.Lbl == "" {
			return fmt.Errorf("no target label")
		}
		e.u8(0x0F, 0x80+in.CC)
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(0)

	case "call_sym":
		return e.relFix(0xE8, in.Sym)

	case "jmp_sym":
		return e.relFix(0xE9, in.Sym)

	case "call_r":
		sr, err := reg(in.S, "target")
		if err != nil {
			return err
		}
		rn := rexNeed{b: sr.NeedsREXBit()}
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0xFF, isax64.PackModRM(isax64.ModReg, 2, sr.Low3()))

	case "jmp_r":
		sr, err := reg(in.S, "target")
		if err != nil {
			return err
		}
		rn := rexNeed{b: sr.NeedsREXBit()}
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0xFF, isax64.PackModRM(isax64.ModReg, 4, sr.Low3()))

	case "push":
		switch in.S.Kind {
		case OImm:
			e.u8(0x68)
			return e.imm(4, in.S)
		case OReg:
			sr, err := reg(in.S, "source")
			if err != nil {
				return err
			}
			if sr.NeedsREXBit() {
				e.u8(isax64.PackREX(false, false, false, true))
			}
			e.u8(0x50 + sr.Low3())
		case OMem:
			var rn rexNeed
			rn.memREX(in.S)
			if err := rn.emit(e); err != nil {
				return err
			}
			e.u8(0xFF)
			return e.mem(6, in.S)
		default:
			return fmt.Errorf("operand must be a register, immediate, or memory reference")
		}

	case "pop":
		switch in.D.Kind {
		case OReg:
			dr, err := reg(in.D, "destination")
			if err != nil {
				return err
			}
			if dr.NeedsREXBit() {
				e.u8(isax64.PackREX(false, false, false, true))
			}
			e.u8(0x58 + dr.Low3())
		case OMem:
			var rn rexNeed
			rn.memREX(in.D)
			if err := rn.emit(e); err != nil {
				return err
			}
			e.u8(0x8F)
			return e.mem(0, in.D)
		default:
			return fmt.Errorf("operand must be a register or memory reference")
		}

	case "ret":
		e.u8(0xC3)
		
	case "syscall":
		e.u8(0x0F, 0x05)

	case "ud2":
		e.u8(0x0F, 0x0B)

	case "int":
		if in.Imm == 3 {
			e.u8(0xCC)
			break
		}
		if in.Imm < 0 || in.Imm > 0xFF {
			return fmt.Errorf("vector %d is out of range", in.Imm)
		}
		e.u8(0xCD, byte(in.Imm))

	case "nop":
		e.u8(0x90)

	case "bsr", "bsf":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(in.S)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		op2 := byte(0xBD)
		if in.Op == "bsf" {
			op2 = 0xBC
		}
		e.u8(0x0F, op2)
		return e.mem(dr.Low3(), in.S)

	case "bswap":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		if w != 4 && w != 8 {
			return fmt.Errorf("only the 32- and 64-bit forms are defined")
		}
		rn := rexNeed{w: w == 8, b: dr.NeedsREXBit()}
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0xC8+dr.Low3())

	case "xchg":
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit()
		rn.memREX(in.D)
		if w == 1 && in.S.Kind == OReg {
			rn.byteRegREX(sr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(0x86)
		} else {
			e.u8(0x87)
		}
		return e.mem(sr.Low3(), in.D)

	case "lock_xadd":
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		e.u8(isax64.PrefixF0)
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit()
		rn.memREX(in.D)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0xC1)
		return e.mem(sr.Low3(), in.D)

	case "lock_cmpxchg":
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		e.u8(isax64.PrefixF0)
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit()
		rn.memREX(in.D)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0xB1)
		return e.mem(sr.Low3(), in.D)

	case "mfence":
		e.u8(0x0F, 0xAE, 0xF0)

	case "cld":
		e.u8(0xFC)
	case "std":
		e.u8(0xFD)
	case "rep_movsb":
		e.u8(isax64.PrefixF3, 0xA4)
	case "rep_stosb":
		e.u8(isax64.PrefixF3, 0xAA)

	case "popcnt":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		e.u8(isax64.PrefixF3)
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(in.S)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0xB8)
		return e.mem(dr.Low3(), in.S)

	case "movq_to_xmm", "movd_to_xmm":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		rn := rexNeed{w: in.Op == "movq_to_xmm", r: dr.NeedsREXBit(), b: sr.NeedsREXBit()}
		e.u8(isax64.Prefix66)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x6E, isax64.PackModRM(isax64.ModReg, dr.Low3(), sr.Low3()))

	case "movq_from_xmm", "movd_from_xmm":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		rn := rexNeed{w: in.Op == "movq_from_xmm", r: sr.NeedsREXBit(), b: dr.NeedsREXBit()}
		e.u8(isax64.Prefix66)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x7E, isax64.PackModRM(isax64.ModReg, sr.Low3(), dr.Low3()))

	case "addsd", "subsd", "mulsd", "divsd", "minsd", "maxsd", "sqrtsd",
		"addss", "subss", "mulss", "divss", "minss", "maxss", "sqrtss",
		"ucomisd", "ucomiss", "andpd", "orpd", "xorpd", "andps", "orps", "xorps":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		rn := rexNeed{r: dr.NeedsREXBit(), b: sr.NeedsREXBit()}

		var prefix byte
		var op2 byte

		switch in.Op {
		case "addsd": prefix = isax64.PrefixF2; op2 = 0x58
		case "addss": prefix = isax64.PrefixF3; op2 = 0x58
		case "subsd": prefix = isax64.PrefixF2; op2 = 0x5C
		case "subss": prefix = isax64.PrefixF3; op2 = 0x5C
		case "mulsd": prefix = isax64.PrefixF2; op2 = 0x59
		case "mulss": prefix = isax64.PrefixF3; op2 = 0x59
		case "divsd": prefix = isax64.PrefixF2; op2 = 0x5E
		case "divss": prefix = isax64.PrefixF3; op2 = 0x5E
		case "minsd": prefix = isax64.PrefixF2; op2 = 0x5D
		case "minss": prefix = isax64.PrefixF3; op2 = 0x5D
		case "maxsd": prefix = isax64.PrefixF2; op2 = 0x5F
		case "maxss": prefix = isax64.PrefixF3; op2 = 0x5F
		case "sqrtsd": prefix = isax64.PrefixF2; op2 = 0x51
		case "sqrtss": prefix = isax64.PrefixF3; op2 = 0x51
		case "ucomisd": prefix = isax64.Prefix66; op2 = 0x2E
		case "ucomiss": prefix = 0x00; op2 = 0x2E
		case "andpd": prefix = isax64.Prefix66; op2 = 0x54
		case "andps": prefix = 0x00; op2 = 0x54
		case "orpd": prefix = isax64.Prefix66; op2 = 0x56
		case "orps": prefix = 0x00; op2 = 0x56
		case "xorpd": prefix = isax64.Prefix66; op2 = 0x57
		case "xorps": prefix = 0x00; op2 = 0x57
		}

		if prefix != 0 {
			e.u8(prefix)
		}
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, op2, isax64.PackModRM(isax64.ModReg, dr.Low3(), sr.Low3()))

	case "cvtsi2sd", "cvtsi2ss":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		rn := rexNeed{w: in.Sz == 8, r: dr.NeedsREXBit(), b: sr.NeedsREXBit()}
		if in.Op == "cvtsi2sd" {
			e.u8(isax64.PrefixF2)
		} else {
			e.u8(isax64.PrefixF3)
		}
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x2A, isax64.PackModRM(isax64.ModReg, dr.Low3(), sr.Low3()))

	case "cvttsd2si", "cvttss2si":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		rn := rexNeed{w: in.Sz == 8, r: dr.NeedsREXBit(), b: sr.NeedsREXBit()}
		if in.Op == "cvttsd2si" {
			e.u8(isax64.PrefixF2)
		} else {
			e.u8(isax64.PrefixF3)
		}
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x2C, isax64.PackModRM(isax64.ModReg, dr.Low3(), sr.Low3()))

	case "cvtsd2ss", "cvtss2sd":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		rn := rexNeed{r: dr.NeedsREXBit(), b: sr.NeedsREXBit()}
		if in.Op == "cvtsd2ss" {
			e.u8(isax64.PrefixF2)
		} else {
			e.u8(isax64.PrefixF3)
		}
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x5A, isax64.PackModRM(isax64.ModReg, dr.Low3(), sr.Low3()))

	case "roundsd", "roundss":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		rn := rexNeed{r: dr.NeedsREXBit(), b: sr.NeedsREXBit()}
		e.u8(isax64.Prefix66)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(0x0F, 0x3A)
		if in.Op == "roundsd" {
			e.u8(0x0B)
		} else {
			e.u8(0x0A)
		}
		e.u8(isax64.PackModRM(isax64.ModReg, dr.Low3(), sr.Low3()))
		e.u8(byte(in.Imm))

	default:
		return fmt.Errorf("unknown inst op")
	}
	return nil
}

func (e *enc) movabs(in *Inst) error {
	dr, err := reg(in.D, "destination")
	if err != nil {
		return err
	}
	if in.S.Kind != OImm {
		return fmt.Errorf("movabs source must be an immediate")
	}
	e.u8(isax64.PackREX(true, false, false, dr.NeedsREXBit()))
	e.u8(0xB8 + dr.Low3())
	if in.S.Sym != "" {
		e.fx = append(e.fx, Fixup{
			Offset: uint32(len(e.b)), Symbol: in.S.Sym, Kind: FixupAbs64, Addend: in.S.Imm,
		})
	}
	e.u64(uint64(in.S.Imm))
	return nil
}

func (e *enc) mov(in *Inst, w int) error {
	d, s := in.D, in.S
	switch {
	case d.Kind == OReg && s.Kind == OImm:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		if w == 8 && (s.Sym != "" || !isax64.FitsImm32(s.Imm)) {
			return e.movabs(&Inst{D: d, S: s})
		}
		if w == 8 {
			rn := rexNeed{w: true, b: dr.NeedsREXBit()}
			if err := rn.emit(e); err != nil {
				return err
			}
			e.u8(0xC7, isax64.PackModRM(isax64.ModReg, 0, dr.Low3()))
			return e.imm(4, s)
		}
		var rn rexNeed
		rn.r = false
		rn.b = dr.NeedsREXBit()
		if w == 1 {
			rn.byteRegREX(dr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(0xB0 + dr.Low3())
			return e.imm(1, s)
		}
		e.u8(0xB8 + dr.Low3())
		return e.imm(w, s)

	case d.Kind == OReg && s.Kind == OReg:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(s, "source")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit()
		rn.b = dr.NeedsREXBit()
		if w == 1 {
			rn.byteRegREX(sr)
			rn.byteRegREX(dr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(0x88, isax64.PackModRM(isax64.ModReg, sr.Low3(), dr.Low3()))
			return nil
		}
		e.u8(0x89, isax64.PackModRM(isax64.ModReg, sr.Low3(), dr.Low3()))
		return nil

	case d.Kind == OReg && s.Kind == OMem:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(s)
		if w == 1 {
			rn.byteRegREX(dr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(0x8A)
		} else {
			e.u8(0x8B)
		}
		return e.mem(dr.Low3(), s)

	case d.Kind == OMem && s.Kind == OReg:
		sr, err := reg(s, "source")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit()
		rn.memREX(d)
		if w == 1 {
			rn.byteRegREX(sr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(0x88)
		} else {
			e.u8(0x89)
		}
		return e.mem(sr.Low3(), d)

	case d.Kind == OMem && s.Kind == OImm:
		if w == 8 && (s.Sym != "" || !isax64.FitsImm32(s.Imm)) {
			return fmt.Errorf("64-bit memory immediate must be materialized via a register")
		}
		var rn rexNeed
		rn.w = w == 8
		rn.memREX(d)
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		if w == 1 {
			e.u8(0xC6)
		} else {
			e.u8(0xC7)
		}
		if err := e.mem(0, d); err != nil {
			return err
		}
		iw := w
		if w == 8 {
			iw = 4 
		}
		return e.imm(iw, s)
	}
	return fmt.Errorf("unsupported operand combination")
}

func (e *enc) alu(in *Inst, w int) error {
	op, ok := isax64.AluByName(in.Op)
	if !ok {
		return fmt.Errorf("unknown alu op")
	}
	d, s := in.D, in.S

	switch {
	case d.Kind == OReg && s.Kind == OReg:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(s, "source")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit()
		rn.b = dr.NeedsREXBit()
		if w == 1 {
			rn.byteRegREX(sr)
			rn.byteRegREX(dr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(opByWidth(op.MR, w), isax64.PackModRM(isax64.ModReg, sr.Low3(), dr.Low3()))
		return nil

	case d.Kind == OReg && s.Kind == OMem:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = dr.NeedsREXBit()
		rn.memREX(s)
		if w == 1 {
			rn.byteRegREX(dr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(opByWidth(op.RM, w))
		return e.mem(dr.Low3(), s)

	case d.Kind == OMem && s.Kind == OReg:
		sr, err := reg(s, "source")
		if err != nil {
			return err
		}
		var rn rexNeed
		rn.w = w == 8
		rn.r = sr.NeedsREXBit()
		rn.memREX(d)
		if w == 1 {
			rn.byteRegREX(sr)
		}
		e.sizePrefix(w)
		if err := rn.emit(e); err != nil {
			return err
		}
		e.u8(opByWidth(op.MR, w))
		return e.mem(sr.Low3(), d)

	case s.Kind == OImm:
		if d.Kind != OReg && d.Kind != OMem {
			return fmt.Errorf("destination must be a register or memory reference")
		}
		if w == 8 && (s.Sym != "" || !isax64.FitsImm32(s.Imm)) {
			return fmt.Errorf("64-bit ALU immediate must be materialized via a register")
		}
		var rn rexNeed
		rn.w = w == 8
		rn.memREX(d)
		if w == 1 && d.Kind == OReg {
			rn.byteRegREX(d.Reg)
		}
		e.sizePrefix(w)
		switch {
		case s.Sym == "" && w != 1 && isax64.FitsImm8(s.Imm):
			if err := rn.emit(e); err != nil {
				return err
			}
			e.u8(isax64.AluImm8)
			if err := e.mem(op.Ext, d); err != nil {
				return err
			}
			e.u8(byte(s.Imm))
			return nil
		case s.Sym == "" && d.Kind == OReg && d.Reg == RRAX && (w == 4 || w == 8):
			if err := rn.emit(e); err != nil {
				return err
			}
			e.u8(op.Acc)
			return e.imm(4, s)
		default:
			if err := rn.emit(e); err != nil {
				return err
			}
			if w == 1 {
				e.u8(isax64.AluImm8B)
			} else {
				e.u8(isax64.AluImm32)
			}
			if err := e.mem(op.Ext, d); err != nil {
				return err
			}
			iw := w
			if w == 8 {
				iw = 4
			}
			return e.imm(iw, s)
		}
	}
	return fmt.Errorf("unsupported operand combination")
}

func opByWidth(op byte, w int) byte {
	if w == 1 {
		return op - 1
	}
	return op
}