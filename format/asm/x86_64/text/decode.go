package text

import (
	"fmt"
	"strings"

	isax64 "github.com/vertex-language/vvm/isa/x86_64"
	enc "github.com/vertex-language/vvm/isa/x86_64/encoder"
)

// decoder walks one function's machine code left to right. fixups is keyed
// by the byte offset of the field a relocation patches, exactly as written
// by the encoder — a RIP-relative or symbolic-absolute operand's disp/imm
// bytes are placeholders, so wherever a fixup exists we print the symbol
// instead of the literal bytes.
type decoder struct {
	code   []byte
	pos    int
	fixups map[uint32]enc.Fixup
}

func (d *decoder) u8() byte {
	v := d.code[d.pos]
	d.pos++
	return v
}
func (d *decoder) i8() int8 { return int8(d.u8()) }
func (d *decoder) u16() uint16 {
	v := uint16(d.code[d.pos]) | uint16(d.code[d.pos+1])<<8
	d.pos += 2
	return v
}
func (d *decoder) u32() uint32 {
	v := uint32(d.code[d.pos]) | uint32(d.code[d.pos+1])<<8 |
		uint32(d.code[d.pos+2])<<16 | uint32(d.code[d.pos+3])<<24
	d.pos += 4
	return v
}
func (d *decoder) i32() int32 { return int32(d.u32()) }
func (d *decoder) u64() uint64 {
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(d.code[d.pos+i]) << (8 * i)
	}
	d.pos += 8
	return v
}

func (d *decoder) modrm() (mod, reg, rm byte) {
	return isax64.UnpackModRM(d.u8())
}

// readImmText reads a width-byte immediate at the current position and
// renders it: as a symbol (+addend) if a fixup patches this exact field,
// otherwise as a signed/unsigned decimal literal.
func (d *decoder) readImmText(width int) string {
	off := uint32(d.pos)
	var text string
	switch width {
	case 1:
		text = fmt.Sprintf("%d", int64(d.i8()))
	case 2:
		text = fmt.Sprintf("%d", int64(int16(d.u16())))
	case 4:
		text = fmt.Sprintf("%d", int64(d.i32()))
	case 8:
		text = fmt.Sprintf("%d", d.u64())
	default:
		text = "?"
	}
	if fx, ok := d.fixups[off]; ok && (fx.Kind == enc.FixupAbs32 || fx.Kind == enc.FixupAbs64) {
		if fx.Addend == 0 {
			return fx.Symbol
		}
		return fmt.Sprintf("%s%+d", fx.Symbol, fx.Addend)
	}
	return text
}

// decodeRM reads a ModRM's (already-unpacked) mod/rm fields plus any SIB
// and displacement bytes that follow, mirroring encoder.mem in reverse:
// register-direct, [rip+sym] (via a FixupPCRel32), the SIB no-base
// absolute form (via a FixupAbs32), and the ordinary
// [base(+index*scale)+disp] shapes.
func (d *decoder) decodeRM(mod, rm byte, rexX, rexB bool) (isReg bool, regNum byte, memText string, err error) {
	if mod == isax64.ModReg {
		n := rm
		if rexB {
			n |= 8
		}
		return true, n, "", nil
	}

	if mod == isax64.ModIndir && rm == isax64.RMRIP {
		off := uint32(d.pos)
		_ = d.i32() // placeholder bytes when a fixup patches this field
		if fx, ok := d.fixups[off]; ok && fx.Kind == enc.FixupPCRel32 {
			real := fx.Addend + 4 // memREX's addend is disp-4; invert it
			if real == 0 {
				return false, 0, fmt.Sprintf("[rip+%s]", fx.Symbol), nil
			}
			return false, 0, fmt.Sprintf("[rip+%s%+d]", fx.Symbol, real), nil
		}
		return false, 0, "[rip+?]", nil
	}

	hasSIB := rm == isax64.RMSIB
	var indexField, baseField byte
	hasIndex, hasBase := false, true
	var scale byte = 1

	if hasSIB {
		sib := d.u8()
		sc, idx, base := isax64.UnpackSIB(sib)
		scale = isax64.ScaleFactor(sc)
		hasIndex = !(idx == isax64.SIBNoIndex && !rexX)
		if hasIndex {
			indexField = idx
			if rexX {
				indexField |= 8
			}
		}
		if mod == isax64.ModIndir && base == isax64.SIBNoBase {
			hasBase = false
		} else {
			baseField = base
			if rexB {
				baseField |= 8
			}
		}
	} else {
		baseField = rm
		if rexB {
			baseField |= 8
		}
	}

	var disp int32
	haveDisp := false
	if !hasBase {
		off := uint32(d.pos)
		disp = d.i32()
		haveDisp = true
		if fx, ok := d.fixups[off]; ok && fx.Kind == enc.FixupAbs32 {
			sym := fx.Symbol
			if fx.Addend != 0 {
				sym = fmt.Sprintf("%s%+d", fx.Symbol, fx.Addend)
			}
			if hasIndex {
				return false, 0, fmt.Sprintf("[%s+%s*%d]", sym, isax64.Reg(indexField).String(), scale), nil
			}
			return false, 0, fmt.Sprintf("[%s]", sym), nil
		}
	} else {
		switch mod {
		case isax64.ModDisp8:
			disp = int32(d.i8())
			haveDisp = true
		case isax64.ModDisp32:
			disp = d.i32()
			haveDisp = true
		}
	}

	var sb strings.Builder
	sb.WriteByte('[')
	wrote := false
	if hasBase {
		sb.WriteString(isax64.Reg(baseField).String())
		wrote = true
	}
	if hasIndex {
		if wrote {
			sb.WriteByte('+')
		}
		fmt.Fprintf(&sb, "%s*%d", isax64.Reg(indexField).String(), scale)
		wrote = true
	}
	if haveDisp && (disp != 0 || !wrote) {
		if wrote {
			fmt.Fprintf(&sb, "%+d", disp)
		} else {
			fmt.Fprintf(&sb, "%d", disp)
		}
	}
	sb.WriteByte(']')
	return false, 0, sb.String(), nil
}

// operandText renders a decodeRM result: a bare register name, or a sized
// memory operand ("dword ptr [...]") so width is never ambiguous.
func operandText(isReg bool, regNum byte, mem string, width int, hasREX bool) string {
	if isReg {
		return regName(regNum, width, hasREX)
	}
	if kw := sizeKeyword(width); kw != "" {
		return kw + " " + mem
	}
	return mem
}

func sizeKeyword(width int) string {
	switch width {
	case 1:
		return "byte ptr"
	case 2:
		return "word ptr"
	case 4:
		return "dword ptr"
	case 8:
		return "qword ptr"
	}
	return ""
}

func regName(n byte, width int, hasREX bool) string {
	r := isax64.Reg(n)
	if width == 1 {
		return r.NameByte(hasREX)
	}
	return r.Name(width * 8)
}

func regWithREX(reg byte, rexBit bool) byte {
	if rexBit {
		return reg | 8
	}
	return reg
}

// opWidth is the operand width in bytes selected by REX.W / 0x66, absent
// any byte-opcode override (which callers handle themselves).
func opWidth(hasREX, wBit, pf66 bool) int {
	if hasREX && wBit {
		return 8
	}
	if pf66 {
		return 2
	}
	return 4
}

// immWidthFor mirrors encode.go's iw rule: a 16-bit operand takes a 16-bit
// immediate, everything wider (32 or 64) takes a 32-bit one.
func immWidthFor(width int) int {
	if width == 2 {
		return 2
	}
	return 4
}

// matchAlu checks op against every AluOp's three register/memory forms
// (MR, RM, and the accumulator-immediate short form) and their byte
// counterparts (opcode-1 in each case, per opByWidth). form: 0=MR
// (dst=r/m,src=reg), 1=RM (dst=reg,src=r/m), 2=Acc (dst=AL/eAX/rAX, imm
// follows directly, no ModRM).
func matchAlu(op byte) (a isax64.AluOp, form int, isByte bool, ok bool) {
	for _, x := range isax64.AluOps {
		switch op {
		case x.MR:
			return x, 0, false, true
		case x.MR - 1:
			return x, 0, true, true
		case x.RM:
			return x, 1, false, true
		case x.RM - 1:
			return x, 1, true, true
		case x.Acc:
			return x, 2, false, true
		case x.Acc - 1:
			return x, 2, true, true
		}
	}
	return isax64.AluOp{}, 0, false, false
}

// decodeInsn decodes one instruction starting at d.pos: legacy prefixes,
// an optional REX byte, the opcode, and whatever operands it needs. A
// panic from running off the end of d.code (a truncated instruction) is
// recovered into an error so the caller can degrade gracefully.
func decodeInsn(d *decoder) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("truncated or malformed instruction: %v", r)
		}
	}()

	var pf66, pfF0, pfF3 bool
prefixLoop:
	for {
		switch d.code[d.pos] {
		case isax64.Prefix66:
			pf66 = true
		case isax64.PrefixF0:
			pfF0 = true
		case isax64.PrefixF2:
			// recognized but unused by anything this backend emits
		case isax64.PrefixF3:
			pfF3 = true
		default:
			break prefixLoop
		}
		d.pos++
	}

	var rex byte
	var hasREX bool
	if isax64.IsREX(d.code[d.pos]) {
		rex = d.code[d.pos]
		hasREX = true
		d.pos++
	}
	wBit := rex&isax64.REXW != 0
	rBit := rex&isax64.REXR != 0
	xBit := rex&isax64.REXX != 0
	bBit := rex&isax64.REXB != 0

	op := d.u8()

	if op == 0x0F {
		return d.decodeTwoByte(hasREX, wBit, rBit, xBit, bBit, pf66, pfF0, pfF3)
	}

	if a, form, isByte, ok := matchAlu(op); ok {
		width := 1
		if !isByte {
			width = opWidth(hasREX, wBit, pf66)
		}
		switch form {
		case 2: // accumulator, imm follows directly, no ModRM
			dst := regName(0, width, hasREX)
			immW := 1
			if !isByte {
				immW = immWidthFor(width)
			}
			return fmt.Sprintf("%s %s, %s", a.Name, dst, d.readImmText(immW)), nil
		default: // 0 (MR) or 1 (RM)
			mod, regf, rmf := d.modrm()
			reg := regWithREX(regf, rBit)
			isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
			if err != nil {
				return "", err
			}
			rmText := operandText(isReg, rn, mem, width, hasREX)
			regText := regName(reg, width, hasREX)
			if form == 0 {
				return fmt.Sprintf("%s %s, %s", a.Name, rmText, regText), nil
			}
			return fmt.Sprintf("%s %s, %s", a.Name, regText, rmText), nil
		}
	}

	switch {
	case op == isax64.AluImm8B || op == isax64.AluImm32 || op == isax64.AluImm8:
		return d.decodeAluImmForm(op, hasREX, wBit, pf66, xBit, bBit)

	case op == 0x69 || op == 0x6B: // imul3
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		rmText := operandText(isReg, rn, mem, width, hasREX)
		immW := 1
		if op == 0x69 {
			immW = 4
		}
		return fmt.Sprintf("imul %s, %s, %s", regName(reg, width, hasREX), rmText, d.readImmText(immW)), nil

	case op == 0x88 || op == 0x89: // mov MR
		width := 1
		if op == 0x89 {
			width = opWidth(hasREX, wBit, pf66)
		}
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("mov %s, %s", operandText(isReg, rn, mem, width, hasREX), regName(reg, width, hasREX)), nil

	case op == 0x8A || op == 0x8B: // mov RM
		width := 1
		if op == 0x8B {
			width = opWidth(hasREX, wBit, pf66)
		}
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("mov %s, %s", regName(reg, width, hasREX), operandText(isReg, rn, mem, width, hasREX)), nil

	case op == 0x8D: // lea
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, _, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		if isReg {
			return "", fmt.Errorf("lea requires a memory operand")
		}
		return fmt.Sprintf("lea %s, %s", regName(reg, width, hasREX), mem), nil

	case op == 0x84 || op == 0x85: // test
		width := 1
		if op == 0x85 {
			width = opWidth(hasREX, wBit, pf66)
		}
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("test %s, %s", operandText(isReg, rn, mem, width, hasREX), regName(reg, width, hasREX)), nil

	case op == 0x86 || op == 0x87: // xchg
		width := 1
		if op == 0x87 {
			width = opWidth(hasREX, wBit, pf66)
		}
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("xchg %s, %s", operandText(isReg, rn, mem, width, hasREX), regName(reg, width, hasREX)), nil

	case op >= 0xB0 && op <= 0xB7: // mov r8, imm8
		reg := regWithREX(op-0xB0, bBit)
		return fmt.Sprintf("mov %s, %s", regName(reg, 1, hasREX), d.readImmText(1)), nil

	case op >= 0xB8 && op <= 0xBF: // mov r,imm(w) or movabs r64,imm64
		reg := regWithREX(op-0xB8, bBit)
		if hasREX && wBit {
			off := uint32(d.pos)
			val := d.u64()
			immText := fmt.Sprintf("%d", val)
			if fx, ok := d.fixups[off]; ok && fx.Kind == enc.FixupAbs64 {
				if fx.Addend == 0 {
					immText = fx.Symbol
				} else {
					immText = fmt.Sprintf("%s%+d", fx.Symbol, fx.Addend)
				}
			}
			return fmt.Sprintf("movabs %s, %s", regName(reg, 8, hasREX), immText), nil
		}
		width := 4
		if pf66 {
			width = 2
		}
		return fmt.Sprintf("mov %s, %s", regName(reg, width, hasREX), d.readImmText(width)), nil

	case op == 0xC6 || op == 0xC7: // mov r/m, imm
		width := 1
		if op == 0xC7 {
			width = opWidth(hasREX, wBit, pf66)
		}
		mod, regf, rmf := d.modrm()
		if regf != 0 {
			return "", fmt.Errorf("unsupported /%d for opcode 0x%02x", regf, op)
		}
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		immW := 1
		if op == 0xC7 {
			immW = immWidthFor(width)
		}
		return fmt.Sprintf("mov %s, %s", operandText(isReg, rn, mem, width, hasREX), d.readImmText(immW)), nil

	case op == 0xC0 || op == 0xC1 || op == 0xD0 || op == 0xD1 || op == 0xD2 || op == 0xD3: // shift/rotate
		width := 1
		if op == 0xC1 || op == 0xD1 || op == 0xD3 {
			width = opWidth(hasREX, wBit, pf66)
		}
		mod, regf, rmf := d.modrm()
		sh, ok := isax64.ShiftByExt(regf)
		if !ok {
			return "", fmt.Errorf("unmapped shift /%d", regf)
		}
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		dst := operandText(isReg, rn, mem, width, hasREX)
		switch op {
		case 0xC0, 0xC1:
			return fmt.Sprintf("%s %s, %s", sh.Name, dst, d.readImmText(1)), nil
		case 0xD0, 0xD1:
			return fmt.Sprintf("%s %s, 1", sh.Name, dst), nil
		default:
			return fmt.Sprintf("%s %s, cl", sh.Name, dst), nil
		}

	case op == 0xF6 || op == 0xF7: // group 3
		width := 1
		if op == 0xF7 {
			width = opWidth(hasREX, wBit, pf66)
		}
		mod, regf, rmf := d.modrm()
		g3, ok := isax64.Group3ByExt(regf)
		if !ok {
			return "", fmt.Errorf("unmapped group3 /%d", regf)
		}
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		text := operandText(isReg, rn, mem, width, hasREX)
		if g3.HasImm {
			immW := 1
			if op == 0xF7 {
				immW = immWidthFor(width)
			}
			return fmt.Sprintf("%s %s, %s", g3.Name, text, d.readImmText(immW)), nil
		}
		return fmt.Sprintf("%s %s", g3.Name, text), nil

	case op == 0xFE: // byte inc/dec
		mod, regf, rmf := d.modrm()
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		text := operandText(isReg, rn, mem, 1, hasREX)
		switch regf {
		case 0:
			return fmt.Sprintf("inc %s", text), nil
		case 1:
			return fmt.Sprintf("dec %s", text), nil
		}
		return "", fmt.Errorf("unsupported /%d for opcode 0xFE", regf)

	case op == 0xFF: // group 5
		mod, regf, rmf := d.modrm()
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		switch regf {
		case 0, 1:
			width := opWidth(hasREX, wBit, pf66)
			text := operandText(isReg, rn, mem, width, hasREX)
			if regf == 0 {
				return fmt.Sprintf("inc %s", text), nil
			}
			return fmt.Sprintf("dec %s", text), nil
		case 2:
			return fmt.Sprintf("call %s", operandText(isReg, rn, mem, 8, hasREX)), nil
		case 4:
			return fmt.Sprintf("jmp %s", operandText(isReg, rn, mem, 8, hasREX)), nil
		case 6:
			return fmt.Sprintf("push %s", operandText(isReg, rn, mem, 8, hasREX)), nil
		}
		return "", fmt.Errorf("unsupported group5 /%d", regf)

	case op == 0x8F: // pop r/m
		mod, regf, rmf := d.modrm()
		if regf != 0 {
			return "", fmt.Errorf("unsupported /%d for opcode 0x8F", regf)
		}
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("pop %s", operandText(isReg, rn, mem, 8, hasREX)), nil

	case op == 0x68: // push imm32
		return fmt.Sprintf("push %s", d.readImmText(4)), nil

	case op >= 0x50 && op <= 0x57: // push reg
		reg := regWithREX(op-0x50, bBit)
		return fmt.Sprintf("push %s", regName(reg, 8, hasREX)), nil

	case op >= 0x58 && op <= 0x5F: // pop reg
		reg := regWithREX(op-0x58, bBit)
		return fmt.Sprintf("pop %s", regName(reg, 8, hasREX)), nil

	case op == 0xE8 || op == 0xE9: // call/jmp rel32
		off := uint32(d.pos)
		rel := d.i32()
		mnem := "jmp"
		if op == 0xE8 {
			mnem = "call"
		}
		if fx, ok := d.fixups[off]; ok && fx.Kind == enc.FixupPCRel32 {
			target := fx.Symbol
			if real := fx.Addend + 4; real != 0 {
				target = fmt.Sprintf("%s%+d", fx.Symbol, real)
			}
			return fmt.Sprintf("%s %s", mnem, target), nil
		}
		return fmt.Sprintf("%s 0x%04x", mnem, int64(d.pos)+int64(rel)), nil

	case op == 0xC3:
		return "ret", nil
	case op == 0x90:
		return "nop", nil
	case op == 0xCC:
		return "int3", nil
	case op == 0xCD:
		return fmt.Sprintf("int %d", d.u8()), nil
	case op == 0xFC:
		return "cld", nil
	case op == 0xFD:
		return "std", nil
	case op == 0x99:
		switch {
		case hasREX && wBit:
			return "cqo", nil
		case pf66:
			return "cwd", nil
		default:
			return "cdq", nil
		}
	case op == 0xA4:
		if pfF3 {
			return "rep movsb", nil
		}
		return "movsb", nil
	case op == 0xAA:
		if pfF3 {
			return "rep stosb", nil
		}
		return "stosb", nil
	}

	return "", fmt.Errorf("unknown opcode 0x%02x", op)
}

// decodeAluImmForm decodes the three ALU immediate-group opcodes: 0x80
// (r/m8,imm8), 0x81 (r/m,imm32/imm16), 0x83 (r/m,imm8 sign-extended). The
// operation comes from ModRM.reg via AluByExt, exactly mirroring
// encode.go's alu() immediate path in reverse.
func (d *decoder) decodeAluImmForm(op byte, hasREX, wBit, pf66, xBit, bBit bool) (string, error) {
	width := 1
	if op != isax64.AluImm8B {
		width = opWidth(hasREX, wBit, pf66)
	}
	mod, regf, rmf := d.modrm()
	a, ok := isax64.AluByExt(regf)
	if !ok {
		return "", fmt.Errorf("unmapped alu /%d", regf)
	}
	isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
	if err != nil {
		return "", err
	}
	dst := operandText(isReg, rn, mem, width, hasREX)
	immWidth := 1
	if op == isax64.AluImm32 {
		immWidth = immWidthFor(width)
	}
	return fmt.Sprintf("%s %s, %s", a.Name, dst, d.readImmText(immWidth)), nil
}

// decodeTwoByte handles every 0x0F-prefixed opcode this backend emits:
// movzx/movsx, imul2, jcc/setcc/cmovcc, bsr/bsf, bswap, mfence, the locked
// xadd/cmpxchg forms, and popcnt.
func (d *decoder) decodeTwoByte(hasREX, wBit, rBit, xBit, bBit, pf66, pfF0, pfF3 bool) (string, error) {
	op2 := d.u8()

	switch {
	case op2 == 0xB6 || op2 == 0xB7 || op2 == 0xBE || op2 == 0xBF: // movzx/movsx
		srcWidth := 1
		if op2 == 0xB7 || op2 == 0xBF {
			srcWidth = 2
		}
		mnem := "movzx"
		if op2 == 0xBE || op2 == 0xBF {
			mnem = "movsx"
		}
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		src := operandText(isReg, rn, mem, srcWidth, hasREX)
		return fmt.Sprintf("%s %s, %s", mnem, regName(reg, 8, hasREX), src), nil

	case op2 == 0xAF: // imul2
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		src := operandText(isReg, rn, mem, width, hasREX)
		return fmt.Sprintf("imul %s, %s", regName(reg, width, hasREX), src), nil

	case op2 >= 0x80 && op2 <= 0x8F: // jcc
		cc := op2 - 0x80
		off := uint32(d.pos)
		rel := d.i32()
		if fx, ok := d.fixups[off]; ok && fx.Kind == enc.FixupPCRel32 {
			target := fx.Symbol
			if real := fx.Addend + 4; real != 0 {
				target = fmt.Sprintf("%s%+d", fx.Symbol, real)
			}
			return fmt.Sprintf("j%s %s", isax64.CondName(cc), target), nil
		}
		return fmt.Sprintf("j%s 0x%04x", isax64.CondName(cc), int64(d.pos)+int64(rel)), nil

	case op2 >= 0x90 && op2 <= 0x9F: // setcc
		cc := op2 - 0x90
		mod, _, rmf := d.modrm()
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("set%s %s", isax64.CondName(cc), operandText(isReg, rn, mem, 1, hasREX)), nil

	case op2 >= 0x40 && op2 <= 0x4F: // cmovcc
		cc := op2 - 0x40
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		src := operandText(isReg, rn, mem, width, hasREX)
		return fmt.Sprintf("cmov%s %s, %s", isax64.CondName(cc), regName(reg, width, hasREX), src), nil

	case op2 == 0xBD || op2 == 0xBC: // bsr/bsf
		mnem := "bsr"
		if op2 == 0xBC {
			mnem = "bsf"
		}
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		src := operandText(isReg, rn, mem, width, hasREX)
		return fmt.Sprintf("%s %s, %s", mnem, regName(reg, width, hasREX), src), nil

	case op2 >= 0xC8 && op2 <= 0xCF: // bswap
		width := 4
		if hasREX && wBit {
			width = 8
		}
		reg := regWithREX(op2-0xC8, bBit)
		return fmt.Sprintf("bswap %s", regName(reg, width, hasREX)), nil

	case op2 == 0xAE: // mfence (0F AE F0 only)
		if next := d.u8(); next == 0xF0 {
			return "mfence", nil
		}
		return "", fmt.Errorf("unsupported 0F AE variant")

	case op2 == 0xC1: // xadd
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		dst := operandText(isReg, rn, mem, width, hasREX)
		mnem := "xadd"
		if pfF0 {
			mnem = "lock xadd"
		}
		return fmt.Sprintf("%s %s, %s", mnem, dst, regName(reg, width, hasREX)), nil

	case op2 == 0xB1: // cmpxchg
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		dst := operandText(isReg, rn, mem, width, hasREX)
		mnem := "cmpxchg"
		if pfF0 {
			mnem = "lock cmpxchg"
		}
		return fmt.Sprintf("%s %s, %s", mnem, dst, regName(reg, width, hasREX)), nil

	case op2 == 0xB8: // popcnt (requires F3)
		if !pfF3 {
			return "", fmt.Errorf("0F B8 without an F3 prefix is not supported")
		}
		width := opWidth(hasREX, wBit, pf66)
		mod, regf, rmf := d.modrm()
		reg := regWithREX(regf, rBit)
		isReg, rn, mem, err := d.decodeRM(mod, rmf, xBit, bBit)
		if err != nil {
			return "", err
		}
		src := operandText(isReg, rn, mem, width, hasREX)
		return fmt.Sprintf("popcnt %s, %s", regName(reg, width, hasREX), src), nil
	}

	return "", fmt.Errorf("unknown two-byte opcode 0x0F 0x%02x", op2)
}