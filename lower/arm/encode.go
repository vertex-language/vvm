package arm

import (
	"fmt"
	"math/bits"
)

// encodeFunc turns the resolved minst stream into A32 machine words,
// serialized in arch's byte order (arch.go).
//
// Encoding reference points: fixed 4-byte words, cond field in bits 31:28
// (always AL = 0xE here except conditional branches/movcc), data-processing
// immediates as 8 bits rotated right by an even amount, movw/movt split
// imm16 (imm4:imm12), LDR/STR imm12 with a U (add/sub) bit, halfword forms
// with a split imm8, and 24-bit branch offsets counted in words from PC+8.
// All of that is word-level arithmetic and identical for both archs; byte
// order appears only in u32/word/put32 below.
//
// IP (r12) doubles as encoder scratch for immediates that don't fit the
// rotated form; isel keeps IP dead across every alu-imm site (the only live
// use of IP — the indirect-call callee — is separated from wide-imm SP
// adjustments by construction in selCall).
func encodeFunc(insts []minst, localBytes int32, arch Arch) ([]byte, []Fixup, error) {
	e := &enc{labels: map[string]int{}, be: arch.Big()}
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
		rel := int32(t-(p.pos+8)) >> 2 // A32 PC bias: PC reads two words ahead
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
	be      bool // big-endian serialization (armeb)
}

// u32 appends one instruction word in the selected byte order.
func (e *enc) u32(v uint32) {
	if e.be {
		e.b = append(e.b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		return
	}
	e.b = append(e.b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// word reads back the instruction word at pos in the selected byte order.
func (e *enc) word(pos int) uint32 {
	if e.be {
		return uint32(e.b[pos])<<24 | uint32(e.b[pos+1])<<16 |
			uint32(e.b[pos+2])<<8 | uint32(e.b[pos+3])
	}
	return uint32(e.b[pos]) | uint32(e.b[pos+1])<<8 |
		uint32(e.b[pos+2])<<16 | uint32(e.b[pos+3])<<24
}

// put32 rewrites the word at pos in the selected byte order (branch patching).
func (e *enc) put32(pos int, v uint32) {
	if e.be {
		e.b[pos], e.b[pos+1], e.b[pos+2], e.b[pos+3] =
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
		return
	}
	e.b[pos], e.b[pos+1], e.b[pos+2], e.b[pos+3] =
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// imm12 encodes v as an A32 rotated immediate if possible.
func imm12(v uint32) (uint32, bool) {
	for r := uint(0); r < 32; r += 2 {
		if x := bits.RotateLeft32(v, int(r)); x <= 0xFF {
			return (uint32(r)/2)<<8 | x, true
		}
	}
	return 0, false
}

// movImm materializes v into rd flag-free: movw, plus movt when needed.
func (e *enc) movImm(rd reg, v uint32) {
	lo, hi := v&0xFFFF, v>>16
	e.u32(0xE3000000 | (lo>>12)<<16 | uint32(rd)<<12 | lo&0xFFF) // movw
	if hi != 0 {
		e.u32(0xE3400000 | (hi>>12)<<16 | uint32(rd)<<12 | hi&0xFFF) // movt
	}
}

// movSym materializes sym+addend into rd via a fixed movw/movt pair, both
// carrying fixups; the addend halves are pre-encoded into the imm fields
// for REL-style consumers. Field-level pre-encoding is byte-order-neutral;
// u32 handles serialization.
func (e *enc) movSym(rd reg, sym string, addend int64) {
	lo, hi := uint32(addend)&0xFFFF, (uint32(addend)>>16)&0xFFFF
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupMovwAbs, Addend: addend})
	e.u32(0xE3000000 | (lo>>12)<<16 | uint32(rd)<<12 | lo&0xFFF)
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: FixupMovtAbs, Addend: addend})
	e.u32(0xE3400000 | (hi>>12)<<16 | uint32(rd)<<12 | hi&0xFFF)
}

// A32 data-processing opcodes (bits 24:21).
var dpOp = map[string]uint32{
	"and": 0x0, "eor": 0x1, "sub": 0x2, "rsb": 0x3, "add": 0x4,
	"tst": 0x8, "cmp": 0xA, "cmn": 0xB, "orr": 0xC, "bic": 0xE,
}

// dp emits a data-processing op d := d OP s (or flags for cmp/cmn/tst),
// falling back to IP for immediates outside the rotated range.
func (e *enc) dp(op string, sBit uint32, rn, rd reg, s opr) error {
	code, ok := dpOp[op]
	if !ok {
		return fmt.Errorf("encode: unknown dp op %q", op)
	}
	base := 0xE0000000 | code<<21 | sBit<<20 | uint32(rn)<<16 | uint32(rd)<<12
	switch s.k {
	case oReg:
		e.u32(base | uint32(s.reg))
		return nil
	case oImm:
		if imm, fits := imm12(uint32(int32(s.imm))); fits {
			e.u32(base | 1<<25 | imm)
			return nil
		}
		e.movImm(rIP, uint32(int32(s.imm)))
		e.u32(base | uint32(rIP))
		return nil
	}
	return fmt.Errorf("encode: bad %s operand", op)
}

func (e *enc) prologue(localBytes int32) {
	e.u32(0xE92D4800)  // push {fp, lr}
	e.u32(0xE1A0B00D)  // mov fp, sp
	if localBytes > 0 { // sub sp, sp, #local
		_ = e.dp("sub", 0, rSP, rSP, Imm(int64(localBytes)))
	}
}

// epilogue restores SP from FP and pops the frame pair. keepLR pops into LR
// (tailcalls); otherwise straight into PC, which is the return.
func (e *enc) epilogue(keepLR bool) {
	e.u32(0xE1A0D00B) // mov sp, fp
	if keepLR {
		e.u32(0xE8BD4800) // pop {fp, lr}
	} else {
		e.u32(0xE8BD8800) // pop {fp, pc}
	}
}

func (e *enc) branchFix(word uint32, sym string, kind FixupKind) {
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: kind, Addend: -8})
	e.u32(word | 0x00FFFFFE) // (-8 >> 2) & 0xFFFFFF pre-encoded for REL
}

// memOff emits an LDR/STR-class access. base+disp with the U bit chosen by
// the displacement's sign; word/byte forms take imm12, halfword forms imm8.
func (e *enc) memOff(base uint32, rn reg, rt reg, disp int32, halfword bool) error {
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
		return fmt.Errorf("encode: displacement %d out of range (large frames TODO)", disp)
	}
	e.u32(base | u | uint32(rn)<<16 | uint32(rt)<<12 | uint32(d))
	return nil
}

var shiftType = map[string]uint32{"lsl": 0, "lsr": 1, "asr": 2, "ror": 3}

func (e *enc) one(in *minst) error {
	switch in.op {
	case "label":
		e.labels[in.lbl] = len(e.b)

	case "movimm":
		e.movImm(in.d.reg, uint32(int32(in.imm)))
	case "movsym":
		e.movSym(in.d.reg, in.sym, in.imm)
	case "mov_r":
		e.u32(0xE1A00000 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
	case "mvn":
		e.u32(0xE1E00000 | uint32(in.d.reg)<<12 | uint32(in.s.reg))

	case "add", "sub", "and", "orr", "eor", "bic":
		return e.dp(in.op, 0, in.d.reg, in.d.reg, in.s)
	case "adds":
		return e.dp("add", 1, in.d.reg, in.d.reg, in.s)
	case "subs":
		return e.dp("sub", 1, in.d.reg, in.d.reg, in.s)
	case "rsb": // d := s.imm - d (only used as negate: rsb d, d, #imm)
		return e.dp("rsb", 0, in.d.reg, in.d.reg, in.s)
	case "cmp":
		return e.dp("cmp", 1, in.d.reg, 0, in.s)
	case "cmn":
		return e.dp("cmn", 1, in.d.reg, 0, in.s)
	case "tst":
		return e.dp("tst", 1, in.d.reg, 0, in.s)
	case "cmp_asr31": // cmp d, s ASR #31
		e.u32(0xE1500000 | uint32(in.d.reg)<<16 | 31<<7 | 2<<5 | uint32(in.s.reg))

	case "lsl", "lsr", "asr", "ror":
		ty := shiftType[in.op]
		base := 0xE1A00000 | uint32(in.d.reg)<<12 | ty<<5
		if in.t.k == oImm {
			e.u32(base | uint32(in.t.imm&31)<<7 | uint32(in.s.reg))
		} else {
			e.u32(base | 1<<4 | uint32(in.t.reg)<<8 | uint32(in.s.reg))
		}

	case "mul": // d := s * t
		e.u32(0xE0000090 | uint32(in.d.reg)<<16 | uint32(in.t.reg)<<8 | uint32(in.s.reg))
	case "mls": // d := x - s*t
		e.u32(0xE0600090 | uint32(in.d.reg)<<16 | uint32(in.x.reg)<<12 | uint32(in.t.reg)<<8 | uint32(in.s.reg))
	case "umull", "smull": // dlo=d, dhi=x
		base := uint32(0xE0800090)
		if in.op == "smull" {
			base = 0xE0C00090
		}
		e.u32(base | uint32(in.x.reg)<<16 | uint32(in.d.reg)<<12 | uint32(in.t.reg)<<8 | uint32(in.s.reg))
	case "udiv": // d := s / t (idiv-capable core; tier gating TODO §10.4)
		e.u32(0xE730F010 | uint32(in.d.reg)<<16 | uint32(in.t.reg)<<8 | uint32(in.s.reg))
	case "sdiv":
		e.u32(0xE710F010 | uint32(in.d.reg)<<16 | uint32(in.t.reg)<<8 | uint32(in.s.reg))

	case "clz":
		e.u32(0xE16F0F10 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
	case "rbit":
		e.u32(0xE6FF0F30 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
	case "rev":
		e.u32(0xE6BF0F30 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
	case "uxtb":
		e.u32(0xE6EF0070 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
	case "uxth":
		e.u32(0xE6FF0070 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
	case "sxtb":
		e.u32(0xE6AF0070 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
	case "sxth":
		e.u32(0xE6BF0070 | uint32(in.d.reg)<<12 | uint32(in.s.reg))

	case "movcc":
		if in.s.k == oImm {
			if in.s.imm < 0 || in.s.imm > 0xFF {
				return fmt.Errorf("encode: movcc immediate %d out of range", in.s.imm)
			}
			e.u32(uint32(in.cc)<<28 | 0x03A00000 | uint32(in.d.reg)<<12 | uint32(in.s.imm))
		} else {
			e.u32(uint32(in.cc)<<28 | 0x01A00000 | uint32(in.d.reg)<<12 | uint32(in.s.reg))
		}

	case "ldr":
		return e.memOff(0xE5100000, in.s.base, in.d.reg, in.s.disp, false)
	case "str":
		return e.memOff(0xE5000000, in.d.base, in.s.reg, in.d.disp, false)
	case "ldrb":
		return e.memOff(0xE5500000, in.s.base, in.d.reg, in.s.disp, false)
	case "strb":
		return e.memOff(0xE5400000, in.d.base, in.s.reg, in.d.disp, false)
	case "ldrh":
		return e.memOff(0xE15000B0, in.s.base, in.d.reg, in.s.disp, true)
	case "strh":
		return e.memOff(0xE14000B0, in.d.base, in.s.reg, in.d.disp, true)
	case "ldrsb":
		return e.memOff(0xE15000D0, in.s.base, in.d.reg, in.s.disp, true)
	case "ldrsh":
		return e.memOff(0xE15000F0, in.s.base, in.d.reg, in.s.disp, true)

	case "ldrb_r": // d := byte [s + t]
		e.u32(0xE7D00000 | uint32(in.s.reg)<<16 | uint32(in.d.reg)<<12 | uint32(in.t.reg))
	case "strb_r": // byte [d + t] := s
		e.u32(0xE7C00000 | uint32(in.d.reg)<<16 | uint32(in.s.reg)<<12 | uint32(in.t.reg))

	case "ldrex": // d := [s.base]
		e.u32(0xE1900F9F | uint32(in.s.base)<<16 | uint32(in.d.reg)<<12)
	case "strex": // x := status, [d.base] := s
		e.u32(0xE1800F90 | uint32(in.d.base)<<16 | uint32(in.x.reg)<<12 | uint32(in.s.reg))
	case "clrex":
		e.u32(0xF57FF01F)
	case "dmb":
		e.u32(0xF57FF05B) // dmb ish

	case "b":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.lbl})
		e.u32(0xEA000000)
	case "bcc":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.lbl})
		e.u32(uint32(in.cc)<<28 | 0x0A000000)
	case "bl_sym":
		e.branchFix(0xEB000000, in.sym, FixupCall24)
	case "blx_r":
		e.u32(0xE12FFF30 | uint32(in.s.reg))

	case "sub_sp":
		return e.dp("sub", 0, rSP, rSP, Imm(in.imm))
	case "add_sp":
		return e.dp("add", 0, rSP, rSP, Imm(in.imm))
	case "sub_sp_r":
		return e.dp("sub", 0, rSP, rSP, R(in.s.reg))
	case "and_sp":
		return e.dp("and", 0, rSP, rSP, Imm(in.imm))
	case "mov_r_sp":
		e.u32(0xE1A00000 | uint32(in.d.reg)<<12 | uint32(rSP))

	case "udf":
		e.u32(0xE7F000F0) // udf #0 — canonical deterministic halt (§6.1)

	case "epi_ret":
		e.epilogue(false)
	case "epi_jmp_sym":
		e.epilogue(true)
		e.branchFix(0xEA000000, in.sym, FixupJump24)
	case "epi_jmp_r":
		e.epilogue(true)
		e.u32(0xE12FFF10 | uint32(in.s.reg)) // bx

	default:
		return fmt.Errorf("encode: unknown minst op %q", in.op)
	}
	return nil
}