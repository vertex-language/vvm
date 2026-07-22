package encoder

import (
	"fmt"

	isax86 "github.com/vertex-language/vvm/isa/x86"
)

// Encode turns a fully-resolved Inst stream into IA-32 machine bytes.
// Nothing about this function is specific to any particular lowering
// pipeline — it doesn't know about stack frames, calling conventions, or
// prologues/epilogues; a caller that wants those builds them as ordinary
// push/mov/sub/lea/pop Insts and prepends/appends them itself.
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
func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// ---------------------------------------------------------------------------
// Operand-level helpers.
// ---------------------------------------------------------------------------

// width normalizes an Inst.Sz into an operand width in bytes. Zero means
// unset, which in a 32-bit backend means 4.
func width(sz int) (int, error) {
	switch sz {
	case 0, 4:
		return 4, nil
	case 1:
		return 1, nil
	case 2:
		return 2, nil
	}
	return 0, fmt.Errorf("operand size %d is not 1, 2, or 4", sz)
}

// sizePrefix emits the operand-size override for 16-bit operands. The
// 8-bit forms use distinct opcode bytes rather than a prefix, so only
// width 2 produces anything.
func (e *enc) sizePrefix(w int) {
	if w == 2 {
		e.u8(isax86.Prefix66)
	}
}

// reg validates that an operand is an encodable register and returns its
// field value. Every ModRM.reg and short-form-opcode caller goes through
// here: silently encoding register 0 for an operand that was actually a
// memory reference produces working-looking code that touches the wrong
// storage, which is the worst possible failure mode for an assembler.
func reg(o Opr, role string) (byte, error) {
	if o.Kind != OReg {
		return 0, fmt.Errorf("%s operand must be a register", role)
	}
	if !o.Reg.IsGPR() {
		return 0, fmt.Errorf("%s operand names no encodable register", role)
	}
	return byte(o.Reg), nil
}

// imm emits an immediate of the given width, recording an absolute fixup
// first when the operand names a symbol. A symbolic immediate is an
// address, so it only exists at width 4 — anything narrower would truncate
// the relocated value, and silently dropping the fixup (the previous
// behaviour) wrote a zero where an address belonged.
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

// mem emits the ModRM (+SIB, +disp) bytes addressing operand m, with
// regField in ModRM.reg. Handles the IA-32 special cases named in
// isa/x86's encoding.go: an index or an ESP base forces a SIB byte;
// mod=00 rm=101 (no SIB) or mod=00 SIB-base=101 (with SIB) both mean "no
// base, disp32 follows"; an EBP base always carries a displacement.
func (e *enc) mem(regField byte, m Opr) error {
	if m.Kind == OReg {
		if !m.Reg.IsGPR() {
			return fmt.Errorf("r/m operand names no encodable register")
		}
		e.u8(isax86.PackModRM(isax86.ModReg, regField, byte(m.Reg)))
		return nil
	}
	if m.Kind != OMem {
		return fmt.Errorf("operand is not a memory operand")
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
		e.u8(isax86.PackModRM(isax86.ModIndir, regField, isax86.RMDisp32))
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
		return fmt.Errorf("symbolic memory operand %q cannot carry a base or index", m.MSym)
	}
	if m.Index == RESP {
		return fmt.Errorf("esp cannot be used as a SIB index register")
	}

	needSIB := hasIndex || m.Base == RESP
	var mod byte
	switch {
	case !hasBase:
		mod = isax86.ModIndir // [index*scale+disp32], no base
	case m.Disp == 0 && m.Base != REBP:
		mod = isax86.ModIndir
	case isax86.FitsDisp8(m.Disp):
		mod = isax86.ModDisp8
	default:
		mod = isax86.ModDisp32
	}

	rm := isax86.RMSIB
	if !needSIB {
		rm = byte(m.Base)
	}
	e.u8(isax86.PackModRM(mod, regField, rm))

	if needSIB {
		scaleBits, ok := isax86.ScaleBits(m.Scale)
		if !ok {
			return fmt.Errorf("scale %d is not 1, 2, 4, or 8", m.Scale)
		}
		indexField := isax86.SIBNoIndex
		if hasIndex {
			indexField = byte(m.Index)
		}
		baseField := isax86.SIBNoBase
		if hasBase {
			baseField = byte(m.Base)
		}
		e.u8(isax86.PackSIB(scaleBits, indexField, baseField))
		if !hasBase {
			e.u32(uint32(m.Disp))
			return nil
		}
	}
	switch mod {
	case isax86.ModDisp8:
		e.u8(byte(int8(m.Disp)))
	case isax86.ModDisp32:
		e.u32(uint32(m.Disp))
	}
	return nil
}

// relFix emits a one-byte opcode followed by a PC-relative rel32 fixup —
// shared by call_sym and jmp_sym, whose fixup math is identical (the
// field's own end is the reference point in both cases).
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

// ---------------------------------------------------------------------------
// The instruction switch.
// ---------------------------------------------------------------------------

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

	case "movzx", "movsx":
		// Sz is the *source* width here, not the destination's: these
		// instructions exist precisely to widen a narrower operand into a
		// full 32-bit register, so a 4-byte source is a plain mov and
		// almost certainly a caller bug rather than something to silently
		// re-encode as a byte load.
		if in.Sz != 1 && in.Sz != 2 {
			return fmt.Errorf("source width must be 1 or 2, got %d", in.Sz)
		}
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		if in.Sz == 1 && in.S.Kind == OReg && !in.S.Reg.ByteAddressable() {
			return fmt.Errorf("byte source needs a byte-addressable register")
		}
		op2 := byte(0xB6) // movzx r32, r/m8
		switch {
		case in.Op == "movzx" && in.Sz == 2:
			op2 = 0xB7
		case in.Op == "movsx" && in.Sz == 1:
			op2 = 0xBE
		case in.Op == "movsx" && in.Sz == 2:
			op2 = 0xBF
		}
		e.u8(0x0F, op2)
		return e.mem(d, in.S)

	case "lea":
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		// mod=11 lea is architecturally undefined — the instruction
		// computes an address, and a register operand has none.
		if in.S.Kind != OMem {
			return fmt.Errorf("source must be a memory operand")
		}
		e.u8(0x8D)
		return e.mem(d, in.S)

	case "add", "or", "and", "sub", "xor", "cmp":
		return e.alu(in, w)

	case "test":
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		s, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		if w == 1 {
			e.u8(0x84, isax86.PackModRM(isax86.ModReg, s, d))
			break
		}
		e.u8(0x85, isax86.PackModRM(isax86.ModReg, s, d))

	case "imul2": // 0F AF /r — r32 := r32 * r/m32
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		e.u8(isax86.Imul2Esc, isax86.Imul2Op)
		return e.mem(d, in.S)

	case "imul3": // 0x69 / 0x6B /r — r32 := r/m32 * imm
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		if isax86.FitsImm8(in.Imm) {
			e.u8(isax86.Imul3Imm8)
			if err := e.mem(d, in.S); err != nil {
				return err
			}
			e.u8(byte(in.Imm))
			break
		}
		e.u8(isax86.Imul3Imm32)
		if err := e.mem(d, in.S); err != nil {
			return err
		}
		return e.imm(w, Imm(in.Imm))

	case "not", "neg", "mul", "imul1", "div", "idiv":
		// Group 3's single r/m operand lives in S for every member. That
		// is arbitrary for not/neg — where the operand is really a
		// destination — but it has to be uniform, because all six share
		// one opcode byte and one code path here. isa/x86's Group3Op
		// comment explains why the group is encoding-shaped rather than
		// semantics-shaped.
		name := in.Op
		if name == "imul1" {
			name = "imul"
		}
		g3, ok := isax86.Group3ByName(name)
		if !ok || g3.HasImm {
			return fmt.Errorf("not a single-operand group-3 instruction")
		}
		e.sizePrefix(w)
		if w == 1 {
			e.u8(isax86.Group3Byte)
		} else {
			e.u8(isax86.Group3)
		}
		return e.mem(g3.Ext, in.S)

	case "inc", "dec":
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		// The one-byte 0x40+r / 0x48+r forms are 32-bit-mode only; in
		// 64-bit mode those bytes are REX prefixes. This backend never
		// emits 64-bit code, so they're always available.
		if w != 4 {
			return fmt.Errorf("only the 32-bit form is encoded")
		}
		if in.Op == "inc" {
			e.u8(0x40 + d)
		} else {
			e.u8(0x48 + d)
		}

	case "cdq":
		e.u8(0x99)

	case "shl", "shr", "sar", "rol", "ror":
		sh, ok := isax86.ShiftByName(in.Op)
		if !ok {
			return fmt.Errorf("unknown shift op")
		}
		e.sizePrefix(w)
		if in.S.Kind == OImm {
			if in.S.Sym != "" {
				return fmt.Errorf("shift count cannot be a symbol")
			}
			// A count of 1 has a dedicated one-byte-shorter encoding.
			if in.S.Imm == 1 {
				if w == 1 {
					e.u8(isax86.ShiftOneB)
				} else {
					e.u8(isax86.ShiftOne)
				}
				return e.mem(sh.Ext, in.D)
			}
			if w == 1 {
				e.u8(isax86.ShiftImm8B)
			} else {
				e.u8(isax86.ShiftImm8)
			}
			if err := e.mem(sh.Ext, in.D); err != nil {
				return err
			}
			e.u8(byte(in.S.Imm))
			break
		}
		// Count in CL. The operand is implicit in the encoding, so it
		// isn't checked here beyond rejecting a non-register.
		if _, err := reg(in.S, "count"); err != nil {
			return err
		}
		if in.S.Reg != RECX {
			return fmt.Errorf("variable shift count must be in cl")
		}
		if w == 1 {
			e.u8(isax86.ShiftCLB)
		} else {
			e.u8(isax86.ShiftCL)
		}
		return e.mem(sh.Ext, in.D)

	case "setcc":
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		if !in.D.Reg.ByteAddressable() {
			return fmt.Errorf("needs a byte-addressable register (al/cl/dl/bl)")
		}
		e.u8(0x0F, 0x90+in.CC, isax86.PackModRM(isax86.ModReg, 0, d))

	case "cmovcc":
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		e.u8(0x0F, 0x40+in.CC)
		return e.mem(d, in.S)

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
		s, err := reg(in.S, "target")
		if err != nil {
			return err
		}
		e.u8(0xFF, isax86.PackModRM(isax86.ModReg, 2, s))

	case "jmp_r":
		s, err := reg(in.S, "target")
		if err != nil {
			return err
		}
		e.u8(0xFF, isax86.PackModRM(isax86.ModReg, 4, s))

	case "push":
		switch in.S.Kind {
		case OImm:
			e.u8(0x68)
			return e.imm(4, in.S)
		case OReg:
			s, err := reg(in.S, "source")
			if err != nil {
				return err
			}
			e.u8(0x50 + s)
		case OMem:
			e.u8(0xFF)
			return e.mem(6, in.S)
		default:
			return fmt.Errorf("operand must be a register, immediate, or memory reference")
		}

	case "pop":
		switch in.D.Kind {
		case OReg:
			d, err := reg(in.D, "destination")
			if err != nil {
				return err
			}
			e.u8(0x58 + d)
		case OMem:
			e.u8(0x8F)
			return e.mem(0, in.D)
		default:
			return fmt.Errorf("operand must be a register or memory reference")
		}

	case "ret":
		e.u8(0xC3)

	case "ud2":
		e.u8(0x0F, 0x0B)

	case "int":
		// int3 has a dedicated one-byte encoding that debuggers depend
		// on: the two-byte CD 03 form does not raise the same
		// breakpoint exception on every implementation.
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
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		op2 := byte(0xBD)
		if in.Op == "bsf" {
			op2 = 0xBC
		}
		e.sizePrefix(w)
		e.u8(0x0F, op2)
		return e.mem(d, in.S)

	case "bswap":
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		// The 16-bit form is architecturally undefined, so it isn't
		// reachable through this encoder even though 66 0F C8 assembles.
		if w != 4 {
			return fmt.Errorf("only the 32-bit form is defined")
		}
		e.u8(0x0F, 0xC8+d)

	case "xchg":
		s, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		e.u8(0x87)
		return e.mem(s, in.D)

	case "lock_xadd":
		s, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		e.u8(isax86.PrefixF0)
		e.sizePrefix(w)
		e.u8(0x0F, 0xC1)
		return e.mem(s, in.D)

	case "lock_cmpxchg":
		s, err := reg(in.S, "source")
		if err != nil {
			return err
		}
		e.u8(isax86.PrefixF0)
		e.sizePrefix(w)
		e.u8(0x0F, 0xB1)
		return e.mem(s, in.D)

	case "mfence":
		e.u8(0x0F, 0xAE, 0xF0)

	case "cld":
		e.u8(0xFC)
	case "std":
		e.u8(0xFD)
	case "rep_movsb":
		e.u8(isax86.PrefixF3, 0xA4)
	case "rep_stosb":
		e.u8(isax86.PrefixF3, 0xAA)

	case "popcnt": // F3 0F B8 /r — tier-gated (§10.4)
		d, err := reg(in.D, "destination")
		if err != nil {
			return err
		}
		e.u8(isax86.PrefixF3)
		e.sizePrefix(w)
		e.u8(0x0F, 0xB8)
		return e.mem(d, in.S)

	default:
		return fmt.Errorf("unknown inst op")
	}
	return nil
}

// mov encodes every mov form this backend needs. Every path is width-aware:
// the previous implementation ignored Sz on the memory-destination
// immediate form and always wrote four bytes, so storing a byte-sized
// immediate scribbled over the three bytes after it.
func (e *enc) mov(in *Inst, w int) error {
	d, s := in.D, in.S
	switch {
	case d.Kind == OReg && s.Kind == OImm:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		if w != 4 {
			return fmt.Errorf("narrow register immediates go through a full-width mov")
		}
		e.u8(0xB8 + dr)
		return e.imm(4, s)

	case d.Kind == OReg && s.Kind == OReg:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		sr, err := reg(s, "source")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		if w == 1 {
			if !d.Reg.ByteAddressable() || !s.Reg.ByteAddressable() {
				return fmt.Errorf("byte move needs byte-addressable registers")
			}
			e.u8(0x88, isax86.PackModRM(isax86.ModReg, sr, dr))
			return nil
		}
		e.u8(0x89, isax86.PackModRM(isax86.ModReg, sr, dr))
		return nil

	case d.Kind == OReg && s.Kind == OMem:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		// A narrow load into a 32-bit register leaves the upper bits
		// undefined; the caller has to say whether it wants zero or sign
		// extension, which is what movzx/movsx are for.
		if w != 4 {
			return fmt.Errorf("narrow loads must use movzx or movsx")
		}
		e.u8(0x8B)
		return e.mem(dr, s)

	case d.Kind == OMem && s.Kind == OReg:
		sr, err := reg(s, "source")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		if w == 1 {
			if !s.Reg.ByteAddressable() {
				return fmt.Errorf("byte store needs a byte-addressable source register")
			}
			e.u8(0x88)
		} else {
			e.u8(0x89)
		}
		return e.mem(sr, d)

	case d.Kind == OMem && s.Kind == OImm:
		e.sizePrefix(w)
		if w == 1 {
			e.u8(0xC6)
		} else {
			e.u8(0xC7)
		}
		if err := e.mem(0, d); err != nil {
			return err
		}
		return e.imm(w, s)
	}
	return fmt.Errorf("unsupported operand combination")
}

// alu encodes the six two-operand ALU instructions. Beyond the three
// register/memory forms it picks the shortest legal immediate encoding:
// the accumulator short form when the destination is eax, the
// sign-extended imm8 group when the value fits, the imm32 group
// otherwise. A symbolic immediate always takes the imm32 path, because
// the relocation needs a four-byte field.
func (e *enc) alu(in *Inst, w int) error {
	op, ok := isax86.AluByName(in.Op)
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
		e.sizePrefix(w)
		e.u8(opByWidth(op.MR, w), isax86.PackModRM(isax86.ModReg, sr, dr))
		return nil

	case d.Kind == OReg && s.Kind == OMem:
		dr, err := reg(d, "destination")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		e.u8(opByWidth(op.RM, w))
		return e.mem(dr, s)

	case d.Kind == OMem && s.Kind == OReg:
		sr, err := reg(s, "source")
		if err != nil {
			return err
		}
		e.sizePrefix(w)
		e.u8(opByWidth(op.MR, w))
		return e.mem(sr, d)

	case s.Kind == OImm:
		if d.Kind != OReg && d.Kind != OMem {
			return fmt.Errorf("destination must be a register or memory reference")
		}
		e.sizePrefix(w)
		switch {
		case s.Sym == "" && w != 1 && isax86.FitsImm8(s.Imm):
			e.u8(isax86.AluImm8)
			if err := e.mem(op.Ext, d); err != nil {
				return err
			}
			e.u8(byte(s.Imm))
			return nil
		case s.Sym == "" && d.Kind == OReg && d.Reg == REAX && w == 4:
			e.u8(op.Acc)
			return e.imm(4, s)
		default:
			if w == 1 {
				e.u8(isax86.AluImm8B)
			} else {
				e.u8(isax86.AluImm32)
			}
			if err := e.mem(op.Ext, d); err != nil {
				return err
			}
			return e.imm(w, s)
		}
	}
	return fmt.Errorf("unsupported operand combination")
}

// opByWidth turns a word/dword ALU opcode into its byte counterpart. The
// two always differ by exactly one, with the byte form the even member of
// the pair — an encoding regularity, not a coincidence.
func opByWidth(op byte, w int) byte {
	if w == 1 {
		return op - 1
	}
	return op
}