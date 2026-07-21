package encoder

import (
	"fmt"

	isaaarch64 "github.com/vertex-language/vvm/isa/aarch64"
)

// Encode turns a resolved Inst stream (every not-yet-placed operand
// already rewritten to a concrete Opr by the caller's own register
// allocator) into A64 machine words. Serialization is unconditionally
// little-endian: A64 instruction fetch is always LE regardless of target
// byte order for data, so this function never needs an endianness
// parameter. It does no prologue/epilogue splicing — see the package doc
// comment.
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
			return nil, nil, fmt.Errorf("encoder: undefined label %q", p.lbl)
		}
		rel := int32(t-p.pos) >> 2
		w := e.word(p.pos)
		if p.imm19 {
			w |= (uint32(rel) & 0x7FFFF) << 5
		} else {
			w |= uint32(rel) & 0x3FFFFFF
		}
		e.put32(p.pos, w)
	}
	return e.b, e.fx, nil
}

type patch struct {
	pos   int
	lbl   string
	imm19 bool // b.cond/cbz/cbnz field; false = imm26 (b)
}

type enc struct {
	b       []byte
	fx      []Fixup
	labels  map[string]int
	patches []patch
}

func (e *enc) u32(v uint32) {
	e.b = append(e.b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (e *enc) word(pos int) uint32 {
	return uint32(e.b[pos]) | uint32(e.b[pos+1])<<8 |
		uint32(e.b[pos+2])<<16 | uint32(e.b[pos+3])<<24
}

func (e *enc) put32(pos int, v uint32) {
	e.b[pos], e.b[pos+1], e.b[pos+2], e.b[pos+3] =
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

func (e *enc) movImm(rd Reg, v uint64) {
	zeros, ones := 0, 0
	for i := uint(0); i < 64; i += 16 {
		switch (v >> i) & 0xFFFF {
		case 0:
			zeros++
		case 0xFFFF:
			ones++
		}
	}
	if ones > zeros {
		seeded := false
		for i := uint(0); i < 64; i += 16 {
			h := uint32(v>>i) & 0xFFFF
			if h == 0xFFFF {
				continue
			}
			if !seeded {
				e.u32(isaaarch64.OpMovnX | uint32(i/16)<<21 | (^h&0xFFFF)<<5 | uint32(rd))
				seeded = true
				continue
			}
			e.u32(isaaarch64.OpMovkX | uint32(i/16)<<21 | h<<5 | uint32(rd))
		}
		if !seeded {
			e.u32(isaaarch64.OpMovnX | uint32(rd))
		}
		return
	}
	seeded := false
	for i := uint(0); i < 64; i += 16 {
		h := uint32(v>>i) & 0xFFFF
		if h == 0 {
			continue
		}
		if !seeded {
			e.u32(isaaarch64.OpMovzX | uint32(i/16)<<21 | h<<5 | uint32(rd))
			seeded = true
			continue
		}
		e.u32(isaaarch64.OpMovkX | uint32(i/16)<<21 | h<<5 | uint32(rd))
	}
	if !seeded {
		e.u32(isaaarch64.OpMovzX | uint32(rd))
	}
}

func (e *enc) movSym(rd Reg, sym string, addend int64) {
	a := uint64(addend)
	quad := []struct {
		kind  FixupKind
		shift uint
		base  uint32
	}{
		{FixupMovzG3, 48, isaaarch64.OpMovzX},
		{FixupMovkG2, 32, isaaarch64.OpMovkX},
		{FixupMovkG1, 16, isaaarch64.OpMovkX},
		{FixupMovkG0, 0, isaaarch64.OpMovkX},
	}
	for _, q := range quad {
		e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: q.kind, Addend: addend})
		e.u32(q.base | uint32(q.shift/16)<<21 | (uint32(a>>q.shift)&0xFFFF)<<5 | uint32(rd))
	}
}

func (e *enc) dp(op string, sz int, rd, rn Reg, s Opr) error {
	switch s.Kind {
	case OReg:
		base, ok := isaaarch64.DPRegOpcodes[op]
		if !ok {
			return fmt.Errorf("encoder: unknown dp op %q", op)
		}
		e.u32(base[isaaarch64.Idx64(sz)] | uint32(s.Reg)<<16 | uint32(rn)<<5 | uint32(rd))
		return nil
	case OImm:
		if base, ok := isaaarch64.DPImmOpcodes[op]; ok {
			v := s.Imm
			if v >= 0 && v <= 0xFFF {
				e.u32(base[isaaarch64.Idx64(sz)] | uint32(v)<<10 | uint32(rn)<<5 | uint32(rd))
				return nil
			}
		}
		e.movImm(IP0, uint64(s.Imm))
		return e.dp(op, sz, rd, rn, R(IP0))
	}
	return fmt.Errorf("encoder: bad %s operand", op)
}

// addImmSP packs an SP-relative ADD/SUB #imm, choosing among the plain
// 12-bit immediate form, the shifted (LSL #12) two-instruction form, and
// (for anything wider) materializing the offset through IP0 and using the
// extended-register form.
func (e *enc) addImmSP(rd, rn Reg, v int64) {
	op := isaaarch64.DPImmOpcodes["add"][1]
	ext := isaaarch64.OpAddExtX
	if v < 0 {
		op, ext, v = isaaarch64.DPImmOpcodes["sub"][1], isaaarch64.OpSubExtX, -v
	}
	switch {
	case v <= 0xFFF:
		e.u32(op | uint32(v)<<10 | uint32(rn)<<5 | uint32(rd))
	case v < 1<<24:
		e.u32(op | 1<<22 | uint32(v>>12)<<10 | uint32(rn)<<5 | uint32(rd))
		if lo := v & 0xFFF; lo != 0 {
			e.u32(op | uint32(lo)<<10 | uint32(rd)<<5 | uint32(rd))
		}
	default:
		e.movImm(IP0, uint64(v))
		e.u32(ext | uint32(IP0)<<16 | uint32(rn)<<5 | uint32(rd))
	}
}

func (e *enc) memOff(c isaaarch64.LdStClass, rt, base Reg, disp int32) {
	switch {
	case disp >= 0 && uint32(disp)%c.Scale == 0 && uint32(disp)/c.Scale <= 0xFFF:
		e.u32(c.Scaled | (uint32(disp)/c.Scale)<<10 | uint32(base)<<5 | uint32(rt))
	case disp >= -256 && disp <= 255:
		e.u32(c.Unscaled | (uint32(disp)&0x1FF)<<12 | uint32(base)<<5 | uint32(rt))
	default:
		e.movImm(IP0, uint64(int64(disp)))
		e.u32(isaaarch64.OpAddExtX | uint32(IP0)<<16 | uint32(base)<<5 | uint32(IP0))
		e.u32(c.Scaled | uint32(IP0)<<5 | uint32(rt))
	}
}

func (e *enc) branchFix(word uint32, sym string, kind FixupKind) {
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: kind, Addend: 0})
	e.u32(word)
}

func (e *enc) one(in *Inst) error {
	switch in.Op {
	case "label":
		e.labels[in.Lbl] = len(e.b)

	case "movimm":
		e.movImm(in.D.Reg, uint64(in.Imm))
	case "movsym":
		e.movSym(in.D.Reg, in.Sym, in.Imm)
	case "mov_r":
		base := isaaarch64.OpMovReg
		if in.Sz == 8 {
			base = isaaarch64.OpMovRegX
		}
		e.u32(base | uint32(in.S.Reg)<<16 | uint32(in.D.Reg))
	case "mvn":
		base := isaaarch64.OpMvnReg
		if in.Sz == 8 {
			base = isaaarch64.OpMvnRegX
		}
		e.u32(base | uint32(in.S.Reg)<<16 | uint32(in.D.Reg))
	case "neg":
		base := isaaarch64.OpNegReg
		if in.Sz == 8 {
			base = isaaarch64.OpNegRegX
		}
		e.u32(base | uint32(in.S.Reg)<<16 | uint32(in.D.Reg))

	case "add", "sub", "and", "orr", "eor", "bic", "adds", "subs":
		return e.dp(in.Op, in.Sz, in.D.Reg, in.D.Reg, in.S)
	case "cmp":
		return e.dp("subs", in.Sz, ZR, in.D.Reg, in.S)
	case "cmn":
		return e.dp("adds", in.Sz, ZR, in.D.Reg, in.S)

	case "lslv", "lsrv", "asrv", "rorv", "udiv", "sdiv":
		e.u32(isaaarch64.OpDP2Base | isaaarch64.Sf(in.Sz) | uint32(in.T.Reg)<<16 | isaaarch64.DP2Opcodes[in.Op]<<10 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))
	case "lsr_i":
		if in.Sz == 8 {
			e.u32(isaaarch64.PackBFM(isaaarch64.OpUBFMX, uint32(in.Imm)&63, 63, in.S.Reg, in.D.Reg))
		} else {
			e.u32(isaaarch64.PackBFM(isaaarch64.OpUBFMW, uint32(in.Imm)&31, 31, in.S.Reg, in.D.Reg))
		}
	case "asr_i":
		if in.Sz == 8 {
			e.u32(isaaarch64.PackBFM(isaaarch64.OpSBFMX, uint32(in.Imm)&63, 63, in.S.Reg, in.D.Reg))
		} else {
			e.u32(isaaarch64.PackBFM(isaaarch64.OpSBFMW, uint32(in.Imm)&31, 31, in.S.Reg, in.D.Reg))
		}
	case "lsl_i":
		s := uint32(in.Imm)
		if in.Sz == 8 {
			e.u32(isaaarch64.PackBFM(isaaarch64.OpUBFMX, (64-s)&63, 63-s, in.S.Reg, in.D.Reg))
		} else {
			e.u32(isaaarch64.PackBFM(isaaarch64.OpUBFMW, (32-s)&31, 31-s, in.S.Reg, in.D.Reg))
		}

	case "mul":
		e.u32(isaaarch64.OpMul | isaaarch64.Sf(in.Sz) | uint32(in.T.Reg)<<16 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))
	case "msub":
		e.u32(isaaarch64.OpMSub | isaaarch64.Sf(in.Sz) | uint32(in.T.Reg)<<16 | uint32(in.X.Reg)<<10 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))
	case "smulh":
		e.u32(isaaarch64.OpSMulH | uint32(in.T.Reg)<<16 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))
	case "umulh":
		e.u32(isaaarch64.OpUMulH | uint32(in.T.Reg)<<16 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))
	case "smull":
		e.u32(isaaarch64.OpSMull | uint32(in.T.Reg)<<16 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))
	case "umull":
		e.u32(isaaarch64.OpUMull | uint32(in.T.Reg)<<16 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))

	case "clz", "rbit", "rev", "rev16":
		e.u32(isaaarch64.DP1Opcodes[in.Op][isaaarch64.Idx64(in.Sz)] | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))

	case "uxtb":
		e.u32(isaaarch64.PackBFM(isaaarch64.OpUBFMW, 0, 7, in.S.Reg, in.D.Reg))
	case "uxth":
		e.u32(isaaarch64.PackBFM(isaaarch64.OpUBFMW, 0, 15, in.S.Reg, in.D.Reg))
	case "and1":
		e.u32(isaaarch64.PackBFM(isaaarch64.OpUBFMW, 0, 0, in.S.Reg, in.D.Reg))
	case "sxtb":
		base := map[int]uint32{4: isaaarch64.OpSBFMW, 8: isaaarch64.OpSBFMX}[in.Sz]
		e.u32(isaaarch64.PackBFM(base, 0, 7, in.S.Reg, in.D.Reg))
	case "sxth":
		base := map[int]uint32{4: isaaarch64.OpSBFMW, 8: isaaarch64.OpSBFMX}[in.Sz]
		e.u32(isaaarch64.PackBFM(base, 0, 15, in.S.Reg, in.D.Reg))
	case "sxtw":
		e.u32(isaaarch64.PackBFM(isaaarch64.OpSBFMX, 0, 31, in.S.Reg, in.D.Reg))
	case "sxt1":
		base := map[int]uint32{4: isaaarch64.OpSBFMW, 8: isaaarch64.OpSBFMX}[in.Sz]
		e.u32(isaaarch64.PackBFM(base, 0, 0, in.S.Reg, in.D.Reg))

	case "cset":
		e.u32(isaaarch64.OpCSet | uint32(Invert(in.Cc))<<12 | uint32(in.D.Reg))
	case "csel":
		e.u32(isaaarch64.OpCSel | isaaarch64.Sf(in.Sz) | uint32(in.D.Reg)<<16 | uint32(in.Cc)<<12 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))

	case "ldr":
		c, ok := isaaarch64.LdClasses[in.Sz]
		if !ok {
			return fmt.Errorf("encoder: ldr size %d", in.Sz)
		}
		e.memOff(c, in.D.Reg, in.S.Base, in.S.Disp)
	case "str":
		c, ok := isaaarch64.StClasses[in.Sz]
		if !ok {
			return fmt.Errorf("encoder: str size %d", in.Sz)
		}
		e.memOff(c, in.S.Reg, in.D.Base, in.D.Disp)

	case "ldrb_r":
		e.u32(isaaarch64.OpLdrbReg | uint32(in.T.Reg)<<16 | uint32(in.S.Reg)<<5 | uint32(in.D.Reg))
	case "strb_r":
		e.u32(isaaarch64.OpStrbReg | uint32(in.T.Reg)<<16 | uint32(in.D.Reg)<<5 | uint32(in.S.Reg))

	case "ldar":
		b, err := isaaarch64.SizeBits(in.Sz)
		if err != nil {
			return err
		}
		e.u32(b | isaaarch64.OpLdar | uint32(in.S.Base)<<5 | uint32(in.D.Reg))
	case "stlr":
		b, err := isaaarch64.SizeBits(in.Sz)
		if err != nil {
			return err
		}
		e.u32(b | isaaarch64.OpStlr | uint32(in.D.Base)<<5 | uint32(in.S.Reg))
	case "ldaxr":
		b, err := isaaarch64.SizeBits(in.Sz)
		if err != nil {
			return err
		}
		e.u32(b | isaaarch64.OpLdaxr | uint32(in.S.Base)<<5 | uint32(in.D.Reg))
	case "stlxr":
		b, err := isaaarch64.SizeBits(in.Sz)
		if err != nil {
			return err
		}
		e.u32(b | isaaarch64.OpStlxr | uint32(in.X.Reg)<<16 | uint32(in.D.Base)<<5 | uint32(in.S.Reg))
	case "clrex":
		e.u32(isaaarch64.OpClrex)
	case "dmb":
		e.u32(isaaarch64.OpDmb)

	case "b":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(isaaarch64.OpB)
	case "bcc":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl, imm19: true})
		e.u32(isaaarch64.OpBCond | uint32(in.Cc))
	case "cbz", "cbnz":
		base := isaaarch64.OpCBase | isaaarch64.Sf(in.Sz)
		if in.Op == "cbnz" {
			base |= 1 << 24
		}
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl, imm19: true})
		e.u32(base | uint32(in.S.Reg))
	case "bl_sym":
		e.branchFix(isaaarch64.OpBL, in.Sym, FixupCall26)
	case "b_sym":
		e.branchFix(isaaarch64.OpB, in.Sym, FixupJump26)
	case "blr_r":
		e.u32(isaaarch64.OpBLR | uint32(in.S.Reg)<<5)
	case "br_r":
		e.u32(isaaarch64.OpBR | uint32(in.S.Reg)<<5)

	case "sub_sp":
		e.addImmSP(SP, SP, -in.Imm)
	case "add_sp":
		e.addImmSP(SP, SP, in.Imm)
	case "sub_sp_r":
		e.u32(isaaarch64.OpSubExtX | uint32(in.S.Reg)<<16 | uint32(SP)<<5 | uint32(SP))
	case "and_sp":
		n := uint64(-in.Imm)
		if n == 0 || n&(n-1) != 0 {
			return fmt.Errorf("encoder: and_sp mask %d not -2^k", in.Imm)
		}
		k := uint32(0)
		for (n>>k)&1 == 0 {
			k++
		}
		e.u32(0x92400000 | ((64-k)&63)<<16 | (63-k)<<10 | uint32(SP)<<5 | uint32(SP))
	case "mov_r_sp":
		e.u32(isaaarch64.DPImmOpcodes["add"][1] | uint32(SP)<<5 | uint32(in.D.Reg))
	case "mov_to_sp":
		e.u32(isaaarch64.DPImmOpcodes["add"][1] | uint32(in.S.Reg)<<5 | uint32(SP))

	case "stp_pre":
		e.u32(isaaarch64.PackPair(isaaarch64.OpSTPPre64, in.S.Reg, in.T.Reg, in.D.Base, in.D.Disp/8))
	case "ldp_post":
		e.u32(isaaarch64.PackPair(isaaarch64.OpLDPPost64, in.S.Reg, in.T.Reg, in.D.Base, in.D.Disp/8))

	case "svc":
		e.u32(isaaarch64.OpSVC | (uint32(in.Imm)&0xFFFF)<<5)
	case "brk":
		e.u32(isaaarch64.OpBrk)
	case "ret":
		e.u32(isaaarch64.OpRet)

	default:
		return fmt.Errorf("encoder: unknown Inst op %q", in.Op)
	}
	return nil
}