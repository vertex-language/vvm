// encoder/encode.go
package encoder

import (
	"fmt"

	isaarm "github.com/vertex-language/vvm/isa/arm"
)

// Encode turns a fully-resolved Inst stream into A32 machine words. Like
// the x86 encoders it knows nothing about stack frames or calling
// conventions — a caller that wants those builds them as ordinary
// push/mov/sub/ldr/pop Insts and prepends/appends them itself. Words are
// emitted little-endian (the instruction-stream order for the `arm` and
// modern BE-8 `armeb` targets).
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
		// Branch offset is relative to PC = instruction address + 8, in
		// words. Merge the 24-bit field into the existing word, preserving
		// the condition and the 101L opcode bits.
		off := int32(t-(p.pos+8)) >> 2
		w := getLE32(e.b[p.pos:])
		w = w&0xFF000000 | isaarm.EncodeBranchImm24(off)
		putLE32(e.b[p.pos:], w)
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

func (e *enc) word(w uint32) {
	e.b = append(e.b, byte(w), byte(w>>8), byte(w>>16), byte(w>>24))
}

func getLE32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

func bit(x bool) uint32 {
	if x {
		return 1
	}
	return 0
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// reg validates that an operand is an encodable register and returns it.
func reg(o Opr, role string) (Reg, error) {
	if o.Kind != OReg {
		return 0, fmt.Errorf("%s operand must be a register", role)
	}
	if !o.Reg.IsGPR() {
		return 0, fmt.Errorf("%s operand names no encodable register", role)
	}
	return o.Reg, nil
}

// ---------------------------------------------------------------------------
// Shifter operand (Operand2).
// ---------------------------------------------------------------------------

// encodeOp2 encodes an OReg or OImm operand into the 12-bit Operand2 field,
// reporting the I bit alongside it. An OImm that isn't a valid modified
// immediate is an error here — turning it into a MOVW/MOVT pair or a
// literal load is a lowering decision, not the encoder's.
func (e *enc) encodeOp2(m Opr) (i bool, op2 uint32, err error) {
	switch m.Kind {
	case OImm:
		if m.Sym != "" {
			return false, 0, fmt.Errorf("symbolic immediate must be built with movw/movt")
		}
		rot, imm8, ok := isaarm.EncodeModImm(uint32(m.Imm))
		if !ok {
			return false, 0, fmt.Errorf("%#x is not a valid modified immediate", uint32(m.Imm))
		}
		return true, uint32(rot)<<8 | uint32(imm8), nil
	case OReg:
		if !m.Reg.IsGPR() {
			return false, 0, fmt.Errorf("operand2 names no encodable register")
		}
		rm := uint32(m.Reg.Field())
		if m.ShiftReg != RNone {
			if !m.ShiftReg.IsGPR() {
				return false, 0, fmt.Errorf("shift register is not encodable")
			}
			if m.ShiftReg == R15 {
				return false, 0, fmt.Errorf("a register-specified shift cannot use the pc")
			}
			return false, uint32(m.ShiftReg.Field())<<8 | uint32(m.Shift&3)<<5 | 1<<4 | rm, nil
		}
		return false, uint32(m.ShiftAmt&0x1F)<<7 | uint32(m.Shift&3)<<5 | rm, nil
	}
	return false, 0, fmt.Errorf("operand2 must be a register or immediate")
}

func dpWord(cc, opcode byte, s bool, rn, rd byte, i bool, op2 uint32) uint32 {
	return uint32(cc)<<28 | bit(i)<<25 | uint32(opcode)<<21 | bit(s)<<20 |
		uint32(rn)<<16 | uint32(rd)<<12 | op2&0xFFF
}

// ---------------------------------------------------------------------------
// The instruction switch.
// ---------------------------------------------------------------------------

func (e *enc) one(in *Inst) error {
	cc := in.CC.code()

	// Data-processing is driven entirely off the ISA table: opcode plus the
	// three operand-shape facts (writes Rd? uses Rn? forces S?).
	if d, ok := isaarm.DataProcByName(in.Op); ok {
		i, op2, err := e.encodeOp2(in.M)
		if err != nil {
			return err
		}
		var rd, rn byte
		if d.WritesRd {
			r, err := reg(in.D, "destination")
			if err != nil {
				return err
			}
			rd = r.Field()
		}
		if d.UsesRn {
			r, err := reg(in.N, "first")
			if err != nil {
				return err
			}
			rn = r.Field()
		}
		e.word(dpWord(cc, d.Opcode, in.S || d.ForcesS, rn, rd, i, op2))
		return nil
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

	case "movw", "movt":
		return e.movwt(in, cc)

	case "mul", "mla":
		return e.mul(in, cc)

	case "umull", "umlal", "smull", "smlal":
		return e.mulLong(in, cc)

	case "ldr", "str", "ldrb", "strb":
		return e.ldrStr(in, cc)

	case "ldrh", "strh", "ldrsb", "ldrsh":
		return e.ldrhStrh(in, cc)

	case "push", "pop":
		return e.pushPop(in, cc)

	case "ldmia", "ldmib", "ldmda", "ldmdb",
		"stmia", "stmib", "stmda", "stmdb":
		return e.blockTransfer(in, cc)

	case "b", "bl":
		return e.branch(in, cc)

	case "bx", "blx":
		rm, err := reg(in.M, "target")
		if err != nil {
			return err
		}
		mid := uint32(0x12FFF1) // BX
		if in.Op == "blx" {
			mid = 0x12FFF3
		}
		e.word(uint32(cc)<<28 | mid<<4 | uint32(rm.Field()))

	case "clz":
		dr, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		mr, err := reg(in.M, "source")
		if err != nil {
			return err
		}
		e.word(uint32(cc)<<28 | 0x016<<20 | 0xF<<16 |
			uint32(dr.Field())<<12 | 0xF<<8 | 0x1<<4 | uint32(mr.Field()))

	case "svc", "swi":
		if in.Imm < 0 || in.Imm > 0xFFFFFF {
			return fmt.Errorf("svc comment %d out of range", in.Imm)
		}
		e.word(uint32(cc)<<28 | 0xF<<24 | uint32(in.Imm)&0xFFFFFF)

	case "nop":
		// The pre-ARMv6K portable NOP: MOV r0, r0.
		e.word(dpWord(cc, isaMovOpcode(), false, 0, 0, false, uint32(R0.Field())))

	case "ud":
		// A permanently-undefined encoding (the UDF space).
		e.word(uint32(cc)<<28 | 0x7F000F0)

	default:
		return fmt.Errorf("unknown inst op")
	}
	return nil
}

func isaMovOpcode() byte {
	d, _ := isaarm.DataProcByName("mov")
	return d.Opcode
}

// movwt encodes MOVW / MOVT with a 16-bit immediate or a relocation half.
func (e *enc) movwt(in *Inst, cc byte) error {
	dr, err := reg(in.D, "destination")
	if err != nil {
		return err
	}
	base := uint32(0x30) // MOVW: cc 0011 0000 ...
	kind := FixupMovwAbs
	if in.Op == "movt" {
		base = 0x34 // MOVT: cc 0011 0100 ...
		kind = FixupMovtAbs
	}

	var imm16 uint32
	if in.M.Kind == OImm && in.M.Sym != "" {
		e.fx = append(e.fx, Fixup{
			Offset: uint32(len(e.b)), Symbol: in.M.Sym, Kind: kind, Addend: in.M.Imm,
		})
	} else {
		v := in.Imm
		if in.M.Kind == OImm && in.M.Sym == "" {
			v = in.M.Imm
		}
		if v < 0 || v > 0xFFFF {
			return fmt.Errorf("%s immediate %d does not fit 16 bits", in.Op, v)
		}
		imm16 = uint32(v)
	}
	imm4 := imm16 >> 12 & 0xF
	imm12 := imm16 & 0xFFF
	e.word(uint32(cc)<<28 | base<<20 | imm4<<16 | uint32(dr.Field())<<12 | imm12)
	return nil
}

// mul encodes MUL (Rd := Rm*Rs) and MLA (Rd := Rm*Rs + Rn). Field layout:
// Rd is the upper register nibble here, not the usual Rn position.
func (e *enc) mul(in *Inst, cc byte) error {
	rd, err := reg(in.D, "destination")
	if err != nil {
		return err
	}
	rm, err := reg(in.M, "Rm")
	if err != nil {
		return err
	}
	rs, err := reg(in.A, "Rs")
	if err != nil {
		return err
	}
	var a uint32
	var rn byte
	if in.Op == "mla" {
		a = 1
		r, err := reg(in.N, "accumulate")
		if err != nil {
			return err
		}
		rn = r.Field()
	}
	e.word(uint32(cc)<<28 | a<<21 | bit(in.S)<<20 |
		uint32(rd.Field())<<16 | uint32(rn)<<12 | uint32(rs.Field())<<8 |
		0x9<<4 | uint32(rm.Field()))
	return nil
}

// mulLong encodes the 64-bit multiplies: RdHi:RdLo := Rm*Rs (+ RdHi:RdLo).
func (e *enc) mulLong(in *Inst, cc byte) error {
	rdlo, err := reg(in.D, "RdLo")
	if err != nil {
		return err
	}
	rdhi, err := reg(in.N, "RdHi")
	if err != nil {
		return err
	}
	rm, err := reg(in.M, "Rm")
	if err != nil {
		return err
	}
	rs, err := reg(in.A, "Rs")
	if err != nil {
		return err
	}
	u := bit(in.Op == "smull" || in.Op == "smlal") // signed
	a := bit(in.Op == "umlal" || in.Op == "smlal") // accumulate
	e.word(uint32(cc)<<28 | 1<<23 | u<<22 | a<<21 | bit(in.S)<<20 |
		uint32(rdhi.Field())<<16 | uint32(rdlo.Field())<<12 |
		uint32(rs.Field())<<8 | 0x9<<4 | uint32(rm.Field()))
	return nil
}

// ldrStr encodes single word/byte loads and stores (the 12-bit-offset form).
func (e *enc) ldrStr(in *Inst, cc byte) error {
	rd, err := reg(in.D, "data")
	if err != nil {
		return err
	}
	m := in.M
	if m.Kind != OMem {
		return fmt.Errorf("operand must be a memory reference")
	}
	if !m.Base.IsGPR() {
		return fmt.Errorf("memory base names no encodable register")
	}

	load := in.Op == "ldr" || in.Op == "ldrb"
	byteOp := in.Op == "ldrb" || in.Op == "strb"

	var i, u bool
	var off uint32
	if m.Index == RNone {
		// Immediate offset: magnitude in the field, sign in U.
		u = m.Disp >= 0
		mag := abs32(m.Disp)
		if mag > 0xFFF {
			return fmt.Errorf("immediate offset %d does not fit 12 bits", m.Disp)
		}
		off = uint32(mag)
	} else {
		if !m.Index.IsGPR() {
			return fmt.Errorf("memory index names no encodable register")
		}
		i = true
		u = m.Add
		off = uint32(m.ShiftAmt&0x1F)<<7 | uint32(m.Shift&3)<<5 | uint32(m.Index.Field())
	}

	w := m.Wback && m.Pre // post-indexed encodes W=0 (write-back is implicit)
	e.word(uint32(cc)<<28 | 0b01<<26 | bit(i)<<25 | bit(m.Pre)<<24 | bit(u)<<23 |
		bit(byteOp)<<22 | bit(w)<<21 | bit(load)<<20 |
		uint32(m.Base.Field())<<16 | uint32(rd.Field())<<12 | off)
	return nil
}

// ldrhStrh encodes the halfword / signed-byte / signed-halfword transfers,
// whose offset is an 8-bit value split across two nibbles and whose type is
// carried in bits 6:5 (the S and H bits).
func (e *enc) ldrhStrh(in *Inst, cc byte) error {
	rd, err := reg(in.D, "data")
	if err != nil {
		return err
	}
	m := in.M
	if m.Kind != OMem || !m.Base.IsGPR() {
		return fmt.Errorf("operand must be a memory reference with an encodable base")
	}
	load := in.Op == "ldrh" || in.Op == "ldrsb" || in.Op == "ldrsh"
	if !load && (in.Op == "ldrsb" || in.Op == "ldrsh") {
		return fmt.Errorf("%s is load-only", in.Op)
	}

	var sbit, hbit uint32 // bits 6:5 => 01 H, 10 SB, 11 SH
	switch in.Op {
	case "ldrh", "strh":
		hbit = 1
	case "ldrsb":
		sbit = 1
	case "ldrsh":
		sbit, hbit = 1, 1
	}

	var imm, u bool // imm: bit 22 (1 = immediate offset)
	var lo, hi, u1 uint32
	if m.Index == RNone {
		imm = true
		u = m.Disp >= 0
		mag := abs32(m.Disp)
		if mag > 0xFF {
			return fmt.Errorf("halfword offset %d does not fit 8 bits", m.Disp)
		}
		lo = uint32(mag) & 0xF
		hi = uint32(mag) >> 4 & 0xF
	} else {
		if !m.Index.IsGPR() {
			return fmt.Errorf("memory index names no encodable register")
		}
		u = m.Add
		lo = uint32(m.Index.Field())
	}
	_ = u1

	w := m.Wback && m.Pre
	e.word(uint32(cc)<<28 | bit(m.Pre)<<24 | bit(u)<<23 | bit(imm)<<22 |
		bit(w)<<21 | bit(load)<<20 |
		uint32(m.Base.Field())<<16 | uint32(rd.Field())<<12 | hi<<8 |
		1<<7 | sbit<<6 | hbit<<5 | 1<<4 | lo)
	return nil
}

// pushPop encodes the two common stack forms: push == STMDB sp!, and
// pop == LDMIA sp!. Both imply the stack pointer and write-back.
func (e *enc) pushPop(in *Inst, cc byte) error {
	if in.M.Kind != ORegList {
		return fmt.Errorf("operand must be a register list")
	}
	list := uint32(uint16(in.M.Imm))
	if list == 0 {
		return fmt.Errorf("register list is empty")
	}
	var p, u, l uint32
	if in.Op == "push" { // STMDB sp!, {list}
		p, u, l = 1, 0, 0
	} else { // pop: LDMIA sp!, {list}
		p, u, l = 0, 1, 1
	}
	e.word(uint32(cc)<<28 | 0b100<<25 | p<<24 | u<<23 | 1<<21 | l<<20 |
		uint32(SP.Field())<<16 | list)
	return nil
}

// blockTransfer encodes the explicit LDM/STM forms, taking the addressing
// mode from the mnemonic suffix and the base/list from N/M.
func (e *enc) blockTransfer(in *Inst, cc byte) error {
	base, err := reg(in.N, "base")
	if err != nil {
		return err
	}
	if in.M.Kind != ORegList {
		return fmt.Errorf("operand must be a register list")
	}
	list := uint32(uint16(in.M.Imm))
	if list == 0 {
		return fmt.Errorf("register list is empty")
	}
	mode, ok := isaarm.BlockModeByGeneric(in.Op[3:]) // "ia","ib","da","db"
	if !ok {
		return fmt.Errorf("unknown block-transfer mode")
	}
	l := in.Op[:3] == "ldm"
	e.word(uint32(cc)<<28 | 0b100<<25 | bit(mode.P)<<24 | bit(mode.U)<<23 |
		bit(in.Wb)<<21 | bit(l)<<20 | uint32(base.Field())<<16 | list)
	return nil
}

// branch encodes B/BL to a local label (patched at the end) or an external
// symbol (a PC-relative relocation).
func (e *enc) branch(in *Inst, cc byte) error {
	l := in.Op == "bl"
	word := uint32(cc)<<28 | 0b101<<25 | bit(l)<<24
	switch {
	case in.Lbl != "":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.word(word) // low 24 bits filled in during resolution
	case in.Sym != "":
		e.fx = append(e.fx, Fixup{
			Offset: uint32(len(e.b)), Symbol: in.Sym, Kind: FixupPCRel24, Addend: -8,
		})
		e.word(word)
	default:
		return fmt.Errorf("branch has neither label nor symbol")
	}
	return nil
}