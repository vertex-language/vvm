package aarch64

import (
	"fmt"
	"math/bits"
)

// encodeFunc turns the resolved minst stream into A64 machine words.
// Serialization is unconditionally little-endian: A64 instruction fetch is
// always LE, in both aarch64 and aarch64_be (arch.go). Nothing in this
// file may consult Arch.
//
// Encoding reference points: fixed 4-byte words; sf (bit 31) selects the
// W/X form of data-processing ops; add/sub immediates are imm12 with an
// optional LSL #12; move-wide is a split hw:imm16; loads/stores use
// scaled unsigned imm12, falling back to the unscaled signed-imm9 LDUR/
// STUR family for small negative FP-relative offsets and to an IP0-based
// address materialization beyond that; branch offsets are counted in words
// from the instruction's own address (no A32-style PC+8 bias).
//
// IP0 (x16) is the encoder's scratch for wide immediates and far slot
// addressing; isel keeps x16 dead at every alu/cmp/mem site. IP1 (x17)
// holds the indirect-call callee and is never touched by the encoder.
func encodeFunc(insts []minst, localBytes int32, arch Arch) ([]byte, []Fixup, error) {
	_ = arch // deliberate: code bytes are arch-independent (arch.go)
	e := &enc{labels: map[string]int{}}
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
		rel := int32(t-p.pos) >> 2 // A64: PC-relative from the instruction itself
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

// u32 appends one instruction word, always little-endian (arch.go).
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

// sf selects the 64-bit form of a W/X-agnostic encoding.
func sf(sz int) uint32 {
	if sz == 8 {
		return 1 << 31
	}
	return 0
}

// movImm materializes v into rd flag-free via movz/movn + movk, choosing
// the seed that needs the fewest writes.
func (e *enc) movImm(rd reg, v uint64) {
	zeros, ones := 0, 0
	for i := uint(0); i < 64; i += 16 {
		switch (v >> i) & 0xFFFF {
		case 0:
			zeros++
		case 0xFFFF:
			ones++
		}
	}
	if ones > zeros { // movn seed: unwritten halfwords stay 0xFFFF
		seeded := false
		for i := uint(0); i < 64; i += 16 {
			h := uint32(v>>i) & 0xFFFF
			if h == 0xFFFF {
				continue
			}
			if !seeded {
				e.u32(0x92800000 | uint32(i/16)<<21 | (^h&0xFFFF)<<5 | uint32(rd)) // movn
				seeded = true
				continue
			}
			e.u32(0xF2800000 | uint32(i/16)<<21 | h<<5 | uint32(rd)) // movk
		}
		if !seeded {
			e.u32(0x92800000 | uint32(rd)) // v == ~0
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
			e.u32(0xD2800000 | uint32(i/16)<<21 | h<<5 | uint32(rd)) // movz
			seeded = true
			continue
		}
		e.u32(0xF2800000 | uint32(i/16)<<21 | h<<5 | uint32(rd)) // movk
	}
	if !seeded {
		e.u32(0xD2800000 | uint32(rd)) // v == 0
	}
}

// movSym materializes sym+addend into rd via a fixed movz+movk×3 quad, all
// four carrying fixups; the addend's group halfwords are pre-encoded into
// the imm16 fields for REL-style consumers. Non-PIC absolute addressing;
// adrp+lo12 is the small-code-model upgrade (program.go).
func (e *enc) movSym(rd reg, sym string, addend int64) {
	a := uint64(addend)
	quad := []struct {
		kind  FixupKind
		shift uint
		base  uint32
	}{
		{FixupMovzG3, 48, 0xD2800000},
		{FixupMovkG2, 32, 0xF2800000},
		{FixupMovkG1, 16, 0xF2800000},
		{FixupMovkG0, 0, 0xF2800000},
	}
	for _, q := range quad {
		e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: q.kind, Addend: addend})
		e.u32(q.base | uint32(q.shift/16)<<21 | (uint32(a>>q.shift)&0xFFFF)<<5 | uint32(rd))
	}
}

// Data-processing opcode bases, indexed [32-bit, 64-bit].
var dpImm = map[string][2]uint32{
	"add": {0x11000000, 0x91000000}, "adds": {0x31000000, 0xB1000000},
	"sub": {0x51000000, 0xD1000000}, "subs": {0x71000000, 0xF1000000},
}
var dpReg = map[string][2]uint32{
	"add": {0x0B000000, 0x8B000000}, "adds": {0x2B000000, 0xAB000000},
	"sub": {0x4B000000, 0xCB000000}, "subs": {0x6B000000, 0xEB000000},
	"and": {0x0A000000, 0x8A000000}, "orr": {0x2A000000, 0xAA000000},
	"eor": {0x4A000000, 0xCA000000}, "bic": {0x0A200000, 0x8A200000},
}

func idx64(sz int) int {
	if sz == 8 {
		return 1
	}
	return 0
}

// dp emits d := rn OP s (or flags for cmp/cmn via rd = zr), falling back
// to IP0 for immediates outside the imm12 range or on logical ops.
func (e *enc) dp(op string, sz int, rd, rn reg, s opr) error {
	switch s.k {
	case oReg:
		base, ok := dpReg[op]
		if !ok {
			return fmt.Errorf("encode: unknown dp op %q", op)
		}
		e.u32(base[idx64(sz)] | uint32(s.reg)<<16 | uint32(rn)<<5 | uint32(rd))
		return nil
	case oImm:
		if base, ok := dpImm[op]; ok {
			v := s.imm
			if v >= 0 && v <= 0xFFF {
				e.u32(base[idx64(sz)] | uint32(v)<<10 | uint32(rn)<<5 | uint32(rd))
				return nil
			}
		}
		// Wide or logical immediate: materialize into IP0 (dead by contract).
		e.movImm(rIP0, uint64(s.imm))
		return e.dp(op, sz, rd, rn, R(rIP0))
	}
	return fmt.Errorf("encode: bad %s operand", op)
}

// addImmSP adjusts rd := rn ± v where rd/rn may be SP (imm form treats 31
// as SP). Handles imm12, imm12<<12 composition, and an IP0 fallback via
// the extended-register form (which also treats 31 as SP).
func (e *enc) addImmSP(rd, rn reg, v int64) {
	op := uint32(0x91000000) // add X imm
	ext := uint32(0x8B206000) // add X extended, uxtx
	if v < 0 {
		op, ext, v = 0xD1000000, 0xCB206000, -v
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
		e.movImm(rIP0, uint64(v))
		e.u32(ext | uint32(rIP0)<<16 | uint32(rn)<<5 | uint32(rd))
	}
}

// Load/store base words: [scaled unsigned imm12, unscaled signed imm9].
type ldstClass struct {
	scaled, unscaled uint32
	scale            uint32
}

var ldstLd = map[int]ldstClass{
	1: {0x39400000, 0x38400000, 1},
	2: {0x79400000, 0x78400000, 2},
	4: {0xB9400000, 0xB8400000, 4},
	8: {0xF9400000, 0xF8400000, 8},
}
var ldstSt = map[int]ldstClass{
	1: {0x39000000, 0x38000000, 1},
	2: {0x79000000, 0x78000000, 2},
	4: {0xB9000000, 0xB8000000, 4},
	8: {0xF9000000, 0xF8000000, 8},
}

// memOff emits one load/store of size sz to/from rt at [base + disp].
func (e *enc) memOff(c ldstClass, rt, base reg, disp int32) {
	switch {
	case disp >= 0 && uint32(disp)%c.scale == 0 && uint32(disp)/c.scale <= 0xFFF:
		e.u32(c.scaled | (uint32(disp)/c.scale)<<10 | uint32(base)<<5 | uint32(rt))
	case disp >= -256 && disp <= 255:
		e.u32(c.unscaled | (uint32(disp)&0x1FF)<<12 | uint32(base)<<5 | uint32(rt))
	default: // far slot: address in IP0 (dead by contract), zero offset
		e.movImm(rIP0, uint64(int64(disp)))
		e.u32(0x8B206000 | uint32(rIP0)<<16 | uint32(base)<<5 | uint32(rIP0)) // add uxtx (base may be SP)
		e.u32(c.scaled | uint32(rIP0)<<5 | uint32(rt))
	}
}

func (e *enc) prologue(localBytes int32) {
	e.u32(0xA9BF7BFD) // stp x29, x30, [sp, #-16]!
	e.u32(0x910003FD) // mov x29, sp
	if localBytes > 0 {
		e.addImmSP(rSP, rSP, -int64(localBytes))
	}
}

// epilogue restores SP from FP and pops the frame pair; LR is always
// restored (A64 has no pop-into-PC idiom — control leaves via ret/b/br).
func (e *enc) epilogue() {
	e.u32(0x910003BF) // mov sp, x29
	e.u32(0xA8C17BFD) // ldp x29, x30, [sp], #16
}

func (e *enc) branchFix(word uint32, sym string, kind FixupKind) {
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: kind, Addend: 0})
	e.u32(word) // imm26 pre-encoded 0: A = 0 on A64 (no PC bias)
}

// Bitfield aliases: UBFM/SBFM base words [32, 64] (N tracks sf).
const (
	ubfmW = 0x53000000
	ubfmX = 0xD3400000
	sbfmW = 0x13000000
	sbfmX = 0x93400000
)

func bfm(base uint32, immr, imms uint32, rn, rd reg) uint32 {
	return base | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd)
}

// Data-processing 2-source (shifts, div) opcode field values.
var dp2 = map[string]uint32{
	"udiv": 0x2, "sdiv": 0x3,
	"lslv": 0x8, "lsrv": 0x9, "asrv": 0xA, "rorv": 0xB,
}

// Data-processing 1-source opcode field values.
var dp1 = map[string][2]uint32{ // [32, 64]
	"rbit":  {0x5AC00000, 0xDAC00000},
	"rev16": {0x5AC00400, 0xDAC00400},
	"rev":   {0x5AC00800, 0xDAC00C00}, // rev W is opcode 10; rev X is 11
	"clz":   {0x5AC01000, 0xDAC01000},
}

// Acquire/release and exclusive base words; size class in bits 31:30.
func szBits(sz int) (uint32, error) {
	switch sz {
	case 1:
		return 0 << 30, nil
	case 2:
		return 1 << 30, nil
	case 4:
		return 2 << 30, nil
	case 8:
		return 3 << 30, nil
	}
	return 0, fmt.Errorf("encode: bad atomic size %d", sz)
}

func (e *enc) one(in *minst) error {
	switch in.op {
	case "label":
		e.labels[in.lbl] = len(e.b)

	case "movimm":
		e.movImm(in.d.reg, uint64(in.imm))
	case "movsym":
		e.movSym(in.d.reg, in.sym, in.imm)
	case "mov_r": // orr d, zr, s — the sz-4 form clears bits 63:32
		base := [2]uint32{0x2A0003E0, 0xAA0003E0}[idx64(in.sz)]
		e.u32(base | uint32(in.s.reg)<<16 | uint32(in.d.reg))
	case "mvn": // orn d, zr, s
		base := [2]uint32{0x2A2003E0, 0xAA2003E0}[idx64(in.sz)]
		e.u32(base | uint32(in.s.reg)<<16 | uint32(in.d.reg))
	case "neg": // sub d, zr, s
		base := [2]uint32{0x4B0003E0, 0xCB0003E0}[idx64(in.sz)]
		e.u32(base | uint32(in.s.reg)<<16 | uint32(in.d.reg))

	case "add", "sub", "and", "orr", "eor", "bic":
		return e.dp(in.op, in.sz, in.d.reg, in.d.reg, in.s)
	case "adds", "subs":
		return e.dp(in.op, in.sz, in.d.reg, in.d.reg, in.s)
	case "cmp":
		return e.dp("subs", in.sz, rZR, in.d.reg, in.s)
	case "cmn":
		return e.dp("adds", in.sz, rZR, in.d.reg, in.s)

	case "lslv", "lsrv", "asrv", "rorv", "udiv", "sdiv":
		base := uint32(0x1AC00000) | sf(in.sz)
		e.u32(base | uint32(in.t.reg)<<16 | dp2[in.op]<<10 | uint32(in.s.reg)<<5 | uint32(in.d.reg))
	case "lsr_i":
		if in.sz == 8 {
			e.u32(bfm(ubfmX, uint32(in.imm)&63, 63, in.s.reg, in.d.reg))
		} else {
			e.u32(bfm(ubfmW, uint32(in.imm)&31, 31, in.s.reg, in.d.reg))
		}
	case "asr_i":
		if in.sz == 8 {
			e.u32(bfm(sbfmX, uint32(in.imm)&63, 63, in.s.reg, in.d.reg))
		} else {
			e.u32(bfm(sbfmW, uint32(in.imm)&31, 31, in.s.reg, in.d.reg))
		}
	case "lsl_i":
		s := uint32(in.imm)
		if in.sz == 8 {
			e.u32(bfm(ubfmX, (64-s)&63, 63-s, in.s.reg, in.d.reg))
		} else {
			e.u32(bfm(ubfmW, (32-s)&31, 31-s, in.s.reg, in.d.reg))
		}

	case "mul": // madd d, s, t, zr
		e.u32(0x1B007C00 | sf(in.sz) | uint32(in.t.reg)<<16 | uint32(in.s.reg)<<5 | uint32(in.d.reg))
	case "msub": // d := x - s*t
		e.u32(0x1B008000 | sf(in.sz) | uint32(in.t.reg)<<16 | uint32(in.x.reg)<<10 | uint32(in.s.reg)<<5 | uint32(in.d.reg))
	case "smulh":
		e.u32(0x9B407C00 | uint32(in.t.reg)<<16 | uint32(in.s.reg)<<5 | uint32(in.d.reg))
	case "umulh":
		e.u32(0x9BC07C00 | uint32(in.t.reg)<<16 | uint32(in.s.reg)<<5 | uint32(in.d.reg))
	case "smull": // smaddl d, s, t, zr
		e.u32(0x9B207C00 | uint32(in.t.reg)<<16 | uint32(in.s.reg)<<5 | uint32(in.d.reg))
	case "umull":
		e.u32(0x9BA07C00 | uint32(in.t.reg)<<16 | uint32(in.s.reg)<<5 | uint32(in.d.reg))

	case "clz", "rbit", "rev", "rev16":
		e.u32(dp1[in.op][idx64(in.sz)] | uint32(in.s.reg)<<5 | uint32(in.d.reg))

	case "uxtb":
		e.u32(bfm(ubfmW, 0, 7, in.s.reg, in.d.reg))
	case "uxth":
		e.u32(bfm(ubfmW, 0, 15, in.s.reg, in.d.reg))
	case "and1": // ubfx bit 0 — i1 normalization
		e.u32(bfm(ubfmW, 0, 0, in.s.reg, in.d.reg))
	case "sxtb":
		e.u32(bfm(map[int]uint32{4: sbfmW, 8: sbfmX}[in.sz], 0, 7, in.s.reg, in.d.reg))
	case "sxth":
		e.u32(bfm(map[int]uint32{4: sbfmW, 8: sbfmX}[in.sz], 0, 15, in.s.reg, in.d.reg))
	case "sxtw":
		e.u32(bfm(sbfmX, 0, 31, in.s.reg, in.d.reg))
	case "sxt1": // sbfx bit 0 — signed i1 (0 -> 0, 1 -> -1)
		e.u32(bfm(map[int]uint32{4: sbfmW, 8: sbfmX}[in.sz], 0, 0, in.s.reg, in.d.reg))

	case "cset": // csinc d, zr, zr, invert(cc); W form zero-extends
		e.u32(0x1A9F07E0 | uint32(invert(in.cc))<<12 | uint32(in.d.reg))
	case "csel": // d := cc ? s : d  ==  csel d, s, d, cc
		base := uint32(0x1A800000) | sf(in.sz)
		e.u32(base | uint32(in.d.reg)<<16 | uint32(in.cc)<<12 | uint32(in.s.reg)<<5 | uint32(in.d.reg))

	case "ldr":
		c, ok := ldstLd[in.sz]
		if !ok {
			return fmt.Errorf("encode: ldr size %d", in.sz)
		}
		e.memOff(c, in.d.reg, in.s.base, in.s.disp)
	case "str":
		c, ok := ldstSt[in.sz]
		if !ok {
			return fmt.Errorf("encode: str size %d", in.sz)
		}
		e.memOff(c, in.s.reg, in.d.base, in.d.disp)

	case "ldrb_r": // ldrb d, [s, t] (lsl #0)
		e.u32(0x38606800 | uint32(in.t.reg)<<16 | uint32(in.s.reg)<<5 | uint32(in.d.reg))
	case "strb_r": // strb s, [d, t]
		e.u32(0x38206800 | uint32(in.t.reg)<<16 | uint32(in.d.reg)<<5 | uint32(in.s.reg))

	case "ldar":
		b, err := szBits(in.sz)
		if err != nil {
			return err
		}
		e.u32(b | 0x08DFFC00 | uint32(in.s.base)<<5 | uint32(in.d.reg))
	case "stlr":
		b, err := szBits(in.sz)
		if err != nil {
			return err
		}
		e.u32(b | 0x089FFC00 | uint32(in.d.base)<<5 | uint32(in.s.reg))
	case "ldaxr":
		b, err := szBits(in.sz)
		if err != nil {
			return err
		}
		e.u32(b | 0x085FFC00 | uint32(in.s.base)<<5 | uint32(in.d.reg))
	case "stlxr": // x := status
		b, err := szBits(in.sz)
		if err != nil {
			return err
		}
		e.u32(b | 0x0800FC00 | uint32(in.x.reg)<<16 | uint32(in.d.base)<<5 | uint32(in.s.reg))
	case "clrex":
		e.u32(0xD5033F5F)
	case "dmb":
		e.u32(0xD5033BBF) // dmb ish

	case "b":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.lbl})
		e.u32(0x14000000)
	case "bcc":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.lbl, imm19: true})
		e.u32(0x54000000 | uint32(in.cc))
	case "cbz", "cbnz":
		base := uint32(0x34000000) | sf(in.sz)
		if in.op == "cbnz" {
			base |= 1 << 24
		}
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.lbl, imm19: true})
		e.u32(base | uint32(in.s.reg))
	case "bl_sym":
		e.branchFix(0x94000000, in.sym, FixupCall26)
	case "blr_r":
		e.u32(0xD63F0000 | uint32(in.s.reg)<<5)

	case "sub_sp":
		e.addImmSP(rSP, rSP, -in.imm)
	case "add_sp":
		e.addImmSP(rSP, rSP, in.imm)
	case "sub_sp_r": // sub sp, sp, Xs (extended, uxtx — Rn/Rd 31 are SP)
		e.u32(0xCB206000 | uint32(in.s.reg)<<16 | uint32(rSP)<<5 | uint32(rSP))
	case "and_sp": // and sp, sp, #-N (bitmask immediate; N a power of two)
		n := uint64(-in.imm)
		if n == 0 || n&(n-1) != 0 {
			return fmt.Errorf("encode: and_sp mask %d not -2^k", in.imm)
		}
		k := uint32(bits.TrailingZeros64(n))
		// -2^k = (64-k) ones rotated into the top: N=1, immr=(64-k)&63, imms=63-k.
		e.u32(0x92400000 | ((64-k)&63)<<16 | (63-k)<<10 | uint32(rSP)<<5 | uint32(rSP))
	case "mov_r_sp": // add d, sp, #0
		e.u32(0x91000000 | uint32(rSP)<<5 | uint32(in.d.reg))

	case "brk":
		e.u32(0xD4200000) // brk #0 — canonical deterministic halt (§6.1)

	case "epi_ret":
		e.epilogue()
		e.u32(0xD65F03C0) // ret
	case "epi_jmp_sym":
		e.epilogue()
		e.branchFix(0x14000000, in.sym, FixupJump26)
	case "epi_jmp_r":
		e.epilogue()
		e.u32(0xD61F0000 | uint32(in.s.reg)<<5) // br

	default:
		return fmt.Errorf("encode: unknown minst op %q", in.op)
	}
	return nil
}