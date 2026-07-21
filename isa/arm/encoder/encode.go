package encoder

import (
	"fmt"

	isaarm "github.com/vertex-language/vvm/isa/arm"
)

// Encode turns a fully-resolved Inst stream into A32 machine words,
// serialized in the byte order requested by big (true = big-endian words
// and data, false = little-endian). Nothing about this function is
// specific to any particular lowering pipeline or ABI — it doesn't know
// about stack frames or calling conventions; a caller that wants those
// builds them as ordinary push/mov/sub/pop/b Insts and prepends/appends
// them itself.
func Encode(insts []Inst, big bool) ([]byte, []Fixup, error) {
	e := &enc{labels: map[string]int{}, be: big}
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
		rel := int32(t-(p.pos+isaarm.PCBias)) >> 2
		w := e.word(p.pos) | uint32(rel)&0xFFFFFF
		e.put32(p.pos, w)
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
	be      bool
}

func (e *enc) u32(v uint32) {
	if e.be {
		e.b = append(e.b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		return
	}
	e.b = append(e.b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (e *enc) word(pos int) uint32 {
	if e.be {
		return uint32(e.b[pos])<<24 | uint32(e.b[pos+1])<<16 |
			uint32(e.b[pos+2])<<8 | uint32(e.b[pos+3])
	}
	return uint32(e.b[pos]) | uint32(e.b[pos+1])<<8 |
		uint32(e.b[pos+2])<<16 | uint32(e.b[pos+3])<<24
}

func (e *enc) put32(pos int, v uint32) {
	if e.be {
		e.b[pos], e.b[pos+1], e.b[pos+2], e.b[pos+3] =
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
		return
	}
	e.b[pos], e.b[pos+1], e.b[pos+2], e.b[pos+3] =
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// movImm materializes v into rd flag-free: movw, plus movt when needed.
func (e *enc) movImm(rd Reg, v uint32) {
	lo, hi := v&0xFFFF, v>>16
	imm4, imm12 := isaarm.SplitImm16(lo)
	e.u32(isaarm.BaseMOVW | imm4<<16 | uint32(rd)<<12 | imm12)
	if hi != 0 {
		imm4, imm12 = isaarm.SplitImm16(hi)
		e.u32(isaarm.BaseMOVT | imm4<<16 | uint32(rd)<<12 | imm12)
	}
}

// movSym materializes sym+addend into rd via a fixed movw/movt pair, both
// carrying fixups; the addend halves are pre-encoded into the imm fields
// for REL-style consumers.
func (e *enc) movSym(rd Reg, sym string, addend int64) {
	lo, hi := uint32(addend)&0xFFFF, (uint32(addend)>>16)&0xFFFF
	imm4, imm12 := isaarm.SplitImm16(lo)
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupMovwAbs, Addend: addend})
	e.u32(isaarm.BaseMOVW | imm4<<16 | uint32(rd)<<12 | imm12)
	imm4, imm12 = isaarm.SplitImm16(hi)
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupMovtAbs, Addend: addend})
	e.u32(isaarm.BaseMOVT | imm4<<16 | uint32(rd)<<12 | imm12)
}

// dp emits a data-processing op d := d OP s (or just flags, for the
// cmp/cmn/tst forms), falling back to RIP for immediates outside the
// rotated-immediate range.
func (e *enc) dp(op string, sBit uint32, rn, rd Reg, s Opr) error {
	d, ok := isaarm.DPByName(op)
	if !ok {
		return fmt.Errorf("encode: unknown data-processing op %q", op)
	}
	base := 0xE0000000 | d.Code<<21 | sBit<<20 | uint32(rn)<<16 | uint32(rd)<<12
	switch s.Kind {
	case OReg:
		e.u32(base | uint32(s.Reg))
		return nil
	case OImm:
		if imm, fits := isaarm.PackImm12(uint32(int32(s.Imm))); fits {
			e.u32(base | 1<<25 | imm)
			return nil
		}
		e.movImm(RIP, uint32(int32(s.Imm)))
		e.u32(base | uint32(RIP))
		return nil
	}
	return fmt.Errorf("encode: bad %s operand", op)
}

func (e *enc) branchFix(base uint32, sym string, kind FixupKind) {
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: kind, Addend: -int64(isaarm.PCBias)})
	e.u32(base | 0x00FFFFFE) // (-PCBias >> 2) & 0xFFFFFF pre-encoded for REL
}

// memOff emits an LDR/STR-class access: base+disp with the U (add/sub)
// bit chosen by the displacement's sign; word/byte forms take a 12-bit
// displacement, halfword forms an 8-bit one split across two fields.
func (e *enc) memOff(base uint32, rn, rt Reg, disp int32, halfword bool) error {
	u := uint32(1 << 23)
	d := disp
	if d < 0 {
		u, d = 0, -d
	}
	if halfword {
		if d > 0xFF {
			return fmt.Errorf("encode: halfword displacement %d out of range", disp)
		}
		e.u32(base | u | uint32(rn)<<16 | uint32(rt)<<12 | uint32(d>>4)<<8 | uint32(d&0xF))
		return nil
	}
	if d > 0xFFF {
		return fmt.Errorf("encode: displacement %d out of range", disp)
	}
	e.u32(base | u | uint32(rn)<<16 | uint32(rt)<<12 | uint32(d))
	return nil
}

func (e *enc) one(in *Inst) error {
	switch in.Op {
	case "label":
		e.labels[in.Lbl] = len(e.b)

	case "movimm":
		e.movImm(in.D.Reg, uint32(int32(in.Imm)))
	case "movsym":
		e.movSym(in.D.Reg, in.Sym, in.Imm)
	case "mov_r":
		e.u32(isaarm.BaseMOVR | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
	case "mvn":
		e.u32(isaarm.BaseMVN | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))

	case "add", "sub", "and", "orr", "eor", "bic":
		return e.dp(in.Op, 0, in.D.Reg, in.D.Reg, in.S)
	case "adds":
		return e.dp("add", 1, in.D.Reg, in.D.Reg, in.S)
	case "subs":
		return e.dp("sub", 1, in.D.Reg, in.D.Reg, in.S)
	case "rsb":
		return e.dp("rsb", 0, in.D.Reg, in.D.Reg, in.S)
	case "cmp":
		return e.dp("cmp", 1, in.D.Reg, 0, in.S)
	case "cmn":
		return e.dp("cmn", 1, in.D.Reg, 0, in.S)
	case "tst":
		return e.dp("tst", 1, in.D.Reg, 0, in.S)
	case "cmp_asr31": // cmp D, S ASR #31 (smulo check)
		e.u32(isaarm.BaseCMPASR31 | uint32(in.D.Reg)<<16 | 31<<7 | 2<<5 | uint32(in.S.Reg))

	case "lsl", "lsr", "asr", "ror":
		sh, ok := isaarm.ShiftByName(in.Op)
		if !ok {
			return fmt.Errorf("encode: unknown shift op %q", in.Op)
		}
		base := isaarm.BaseMOVR | uint32(in.D.Reg)<<12 | sh.Code<<5
		if in.T.Kind == OImm {
			e.u32(base | uint32(in.T.Imm&31)<<7 | uint32(in.S.Reg))
		} else {
			e.u32(base | 1<<4 | uint32(in.T.Reg)<<8 | uint32(in.S.Reg))
		}

	case "mul": // D := S * T
		e.u32(isaarm.BaseMUL | uint32(in.D.Reg)<<16 | uint32(in.T.Reg)<<8 | uint32(in.S.Reg))
	case "mls": // D := X - S*T
		e.u32(isaarm.BaseMLS | uint32(in.D.Reg)<<16 | uint32(in.X.Reg)<<12 | uint32(in.T.Reg)<<8 | uint32(in.S.Reg))
	case "umull", "smull": // Dlo=D, Dhi=X
		base := isaarm.BaseUMULL
		if in.Op == "smull" {
			base = isaarm.BaseSMULL
		}
		e.u32(base | uint32(in.X.Reg)<<16 | uint32(in.D.Reg)<<12 | uint32(in.T.Reg)<<8 | uint32(in.S.Reg))
	case "udiv": // D := S / T
		e.u32(isaarm.BaseUDIV | uint32(in.D.Reg)<<16 | uint32(in.T.Reg)<<8 | uint32(in.S.Reg))
	case "sdiv":
		e.u32(isaarm.BaseSDIV | uint32(in.D.Reg)<<16 | uint32(in.T.Reg)<<8 | uint32(in.S.Reg))

	case "clz":
		e.u32(isaarm.BaseCLZ | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
	case "rbit":
		e.u32(isaarm.BaseRBIT | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
	case "rev":
		e.u32(isaarm.BaseREV | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
	case "uxtb":
		e.u32(isaarm.BaseUXTB | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
	case "uxth":
		e.u32(isaarm.BaseUXTH | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
	case "sxtb":
		e.u32(isaarm.BaseSXTB | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
	case "sxth":
		e.u32(isaarm.BaseSXTH | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))

	case "movcc":
		if in.S.Kind == OImm {
			if in.S.Imm < 0 || in.S.Imm > 0xFF {
				return fmt.Errorf("encode: movcc immediate %d out of range", in.S.Imm)
			}
			e.u32(uint32(in.CC)<<28 | isaarm.BaseMOVCCI | uint32(in.D.Reg)<<12 | uint32(in.S.Imm))
		} else {
			e.u32(uint32(in.CC)<<28 | isaarm.BaseMOVCCR | uint32(in.D.Reg)<<12 | uint32(in.S.Reg))
		}

	case "ldr":
		return e.memOff(isaarm.BaseLDR, in.S.Base, in.D.Reg, in.S.Disp, false)
	case "str":
		return e.memOff(isaarm.BaseSTR, in.D.Base, in.S.Reg, in.D.Disp, false)
	case "ldrb":
		return e.memOff(isaarm.BaseLDRB, in.S.Base, in.D.Reg, in.S.Disp, false)
	case "strb":
		return e.memOff(isaarm.BaseSTRB, in.D.Base, in.S.Reg, in.D.Disp, false)
	case "ldrh":
		return e.memOff(isaarm.BaseLDRH, in.S.Base, in.D.Reg, in.S.Disp, true)
	case "strh":
		return e.memOff(isaarm.BaseSTRH, in.D.Base, in.S.Reg, in.D.Disp, true)
	case "ldrsb":
		return e.memOff(isaarm.BaseLDRSB, in.S.Base, in.D.Reg, in.S.Disp, true)
	case "ldrsh":
		return e.memOff(isaarm.BaseLDRSH, in.S.Base, in.D.Reg, in.S.Disp, true)

	case "ldrb_r": // D := byte [S.Base + S.Index]
		e.u32(isaarm.BaseLDRBR | uint32(in.S.Base)<<16 | uint32(in.D.Reg)<<12 | uint32(in.S.Index))
	case "strb_r": // byte [D.Base + D.Index] := S
		e.u32(isaarm.BaseSTRBR | uint32(in.D.Base)<<16 | uint32(in.S.Reg)<<12 | uint32(in.D.Index))

	case "ldrex": // D := [S.Base]
		e.u32(isaarm.BaseLDREX | uint32(in.S.Base)<<16 | uint32(in.D.Reg)<<12)
	case "strex": // X := status, [D.Base] := S
		e.u32(isaarm.BaseSTREX | uint32(in.D.Base)<<16 | uint32(in.X.Reg)<<12 | uint32(in.S.Reg))
	case "clrex":
		e.u32(isaarm.BaseCLREX)
	case "dmb":
		e.u32(isaarm.BaseDMB)

	case "b":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(isaarm.BaseB)
	case "bcc":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl})
		e.u32(uint32(in.CC)<<28 | isaarm.BaseBcc)
	case "b_sym": // plain (non-linking) branch to an external symbol
		e.branchFix(isaarm.BaseB, in.Sym, FixupJump24)
	case "bl_sym":
		e.branchFix(isaarm.BaseBL, in.Sym, FixupCall24)
	case "blx_r":
		e.u32(isaarm.BaseBLXR | uint32(in.S.Reg))
	case "bx_r":
		e.u32(isaarm.BaseBXR | uint32(in.S.Reg))

	case "push": // STMDB sp!, {reglist}
		e.u32(isaarm.BasePUSH | uint32(in.RegList))
	case "pop": // LDMIA sp!, {reglist}
		e.u32(isaarm.BasePOP | uint32(in.RegList))

	case "udf":
		e.u32(isaarm.BaseUDF)

	default:
		return fmt.Errorf("encode: unknown inst op %q", in.Op)
	}
	return nil
}