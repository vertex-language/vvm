// encoder/encode.go
package encoder

import (
	"fmt"

	isaa64 "github.com/vertex-language/vvm/isa/aarch64"
)

// Encode turns a fully-resolved Inst stream into A64 machine words. Like the
// A32 and x86 encoders it knows nothing about stack frames or calling
// conventions — a caller that wants a prologue builds it as ordinary
// stp/sub/mov/ldp Insts and prepends it.
//
// Words are emitted little-endian: A64 instruction fetch is little-endian
// regardless of the data endianness of the target.
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
		// The offset is relative to the branch's own address, in words.
		// No A32-style +8: the A64 PC is the instruction's address.
		word := int64(t-p.pos) >> 2
		w := getLE32(e.b[p.pos:])
		switch p.fld {
		case fld26:
			if !isaa64.FitsBranchImm26(word) {
				return nil, nil, fmt.Errorf("encode: %q is out of imm26 range", p.lbl)
			}
			w = w&^0x03FFFFFF | isaa64.EncodeBranchImm26(word)
		case fld19:
			if !isaa64.FitsBranchImm19(word) {
				return nil, nil, fmt.Errorf("encode: %q is out of imm19 range", p.lbl)
			}
			w = w&^(0x0007FFFF<<5) | isaa64.EncodeBranchImm19(word)
		case fld14:
			if !isaa64.FitsBranchImm14(word) {
				return nil, nil, fmt.Errorf("encode: %q is out of imm14 range", p.lbl)
			}
			w = w&^(0x00003FFF<<5) | isaa64.EncodeBranchImm14(word)
		}
		putLE32(e.b[p.pos:], w)
	}
	return e.b, e.fx, nil
}

// branchField names which of the three branch immediate widths a pending
// local patch has to fill. The width is fixed by the instruction that left
// the hole, so it is recorded when the hole is made.
type branchField byte

const (
	fld26 branchField = iota
	fld19
	fld14
)

type patch struct {
	pos int
	lbl string
	fld branchField
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

func (e *enc) fixup(sym string, kind FixupKind) {
	e.fx = append(e.fx, Fixup{Offset: uint32(len(e.b)), Symbol: sym, Kind: kind})
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

// ---------------------------------------------------------------------------
// Operand validation.
// ---------------------------------------------------------------------------

// regField validates a register operand and returns its 5-bit encoding.
// spRole says what slot 31 means in *this* field: with it true the field is
// Xn|SP, with it false Xn|ZR. Mismatching the role is an error rather than a
// silent reinterpretation, because both spell to 31 and a caller that writes
// the wrong one has written a different instruction than it meant.
func regField(o Opr, role string, spRole bool) (uint32, error) {
	if o.Kind != OReg && o.Kind != OExt {
		return 0, fmt.Errorf("%s operand must be a register", role)
	}
	if !o.Reg.Encodable() {
		return 0, fmt.Errorf("%s operand names no encodable register", role)
	}
	if o.Reg.IsSlot31() && o.SP != spRole {
		if spRole {
			return 0, fmt.Errorf("%s operand is Xn|SP: slot 31 here is the stack pointer, not the zero register", role)
		}
		return 0, fmt.Errorf("%s operand cannot be the stack pointer", role)
	}
	return uint32(o.Reg.Field()), nil
}

func rZR(o Opr, role string) (uint32, error) { return regField(o, role, false) }
func rSP(o Opr, role string) (uint32, error) { return regField(o, role, true) }

// baseField validates a memory operand's base, which every load/store format
// reads as Xn|SP. No role flag is consulted: the format settles it.
func baseField(m Opr) (uint32, error) {
	if !m.Base.Encodable() {
		return 0, fmt.Errorf("memory base names no encodable register")
	}
	return uint32(m.Base.Field()), nil
}

// shiftAmt checks an imm6 shift against the operand width: 0-63 for a 64-bit
// operation, 0-31 for 32-bit.
func shiftAmt(amt byte, w Width) (uint32, error) {
	limit := byte(63)
	if w.is32() {
		limit = 31
	}
	if amt > limit {
		return 0, fmt.Errorf("shift amount %d does not fit a %d-bit operation", amt, w.bits())
	}
	return uint32(amt), nil
}

// ---------------------------------------------------------------------------
// The instruction switch.
// ---------------------------------------------------------------------------

func (e *enc) one(in *Inst) error {
	// The three table-driven families come straight off isa/aarch64: the
	// opcode bits and the per-form irregularities are facts there, so the
	// only work left here is placing operands.
	if a, ok := isaa64.AddSubByName(in.Op); ok {
		return e.addSub(in, a)
	}
	if l, ok := isaa64.LogicalByName(in.Op); ok {
		return e.logical(in, l)
	}
	if m, ok := isaa64.MoveWideByName(in.Op); ok {
		return e.moveWide(in, m)
	}
	if d, ok := isaa64.DataProc2ByName(in.Op); ok {
		return e.dataProc2(in, d)
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
		return nil

	// Add/sub aliases. Each is the flag-setting or zero-register form of an
	// operation the table already names, so it re-enters through addSub with
	// the implied operand filled in rather than growing its own encoder.
	case "cmp", "cmn":
		base := "subs"
		if in.Op == "cmn" {
			base = "adds"
		}
		a, _ := isaa64.AddSubByName(base)
		alias := *in
		alias.D = R(ZR)
		return e.addSub(&alias, a)

	case "neg", "negs":
		base := "sub"
		if in.Op == "negs" {
			base = "subs"
		}
		a, _ := isaa64.AddSubByName(base)
		alias := *in
		alias.N = R(ZR)
		return e.addSub(&alias, a)

	case "tst":
		l, _ := isaa64.LogicalByName("ands")
		alias := *in
		alias.D = R(ZR)
		return e.logical(&alias, l)

	case "mvn":
		l, _ := isaa64.LogicalByName("orn")
		alias := *in
		alias.N = R(ZR)
		return e.logical(&alias, l)

	case "mov":
		return e.mov(in)

	case "lsl", "lsr", "asr", "ror":
		return e.shiftAlias(in)

	case "sbfm", "ubfm", "bfm":
		return e.bitfield(in)

	case "sxtb", "sxth", "sxtw", "uxtb", "uxth":
		return e.extendAlias(in)

	case "extr":
		return e.extr(in)

	case "clz", "cls", "rbit", "rev", "rev16", "rev32":
		return e.dataProc1(in)

	case "madd", "msub", "mul", "mneg",
		"smaddl", "umaddl", "smsubl", "umsubl",
		"smull", "umull", "smnegl", "umnegl",
		"smulh", "umulh":
		return e.dataProc3(in)

	case "ldr", "str", "ldrb", "strb", "ldrh", "strh",
		"ldrsb", "ldrsh", "ldrsw":
		return e.loadStore(in)

	case "ldp", "stp":
		return e.loadStorePair(in)

	case "adr", "adrp":
		return e.adr(in)

	case "b", "bl":
		return e.branch(in)

	case "b.cond":
		return e.branchCond(in)

	case "cbz", "cbnz":
		return e.compareBranch(in)

	case "tbz", "tbnz":
		return e.testBranch(in)

	case "br", "blr", "ret":
		return e.branchReg(in)

	case "csel", "csinc", "csinv", "csneg":
		return e.condSelect(in)

	case "cset", "csetm", "cinc", "cinv", "cneg":
		return e.condSelectAlias(in)

	case "ccmp", "ccmn":
		return e.condCompare(in)

	case "svc", "brk":
		if in.Imm < 0 || in.Imm > 0xFFFF {
			return fmt.Errorf("comment %d does not fit 16 bits", in.Imm)
		}
		base := uint32(0xD4000000) // svc: exception, imm16 at 20:5, LL = 01
		if in.Op == "brk" {
			base = 0xD4200000
		}
		e.word(base | uint32(in.Imm)<<5|bit(in.Op == "svc"))
		return nil

	case "nop":
		e.word(0xD503201F) // HINT #0
		return nil

	case "udf":
		// The permanently-undefined encoding: an all-zero word.
		e.word(0x00000000)
		return nil
	}
	return fmt.Errorf("unknown inst op")
}

// ---------------------------------------------------------------------------
// Add / subtract.
// ---------------------------------------------------------------------------

// addSub encodes the three operand forms off the shared (op,S) bits. The
// form is chosen by M's kind, and the slot-31 rules differ across them in a
// way worth spelling out, since it is the single most error-prone corner of
// A64 operand encoding:
//
//   - Immediate and extended-register: Rn is Xn|SP. Rd is Xd|SP for the
//     plain forms and Xd|ZR for the flag-setting ones (adds/subs), which is
//     what makes CMP-with-SP expressible and ADD-into-ZR not.
//   - Shifted-register: every field is Xd|ZR. The stack pointer cannot
//     appear at all.
func (e *enc) addSub(in *Inst, a isaa64.AddSubOp) error {
	sf := in.W.sf()
	sets := a.S == 1
	shifted := in.M.Kind == OReg

	var rd, rn uint32
	var err error
	if shifted || sets {
		rd, err = rZR(in.D, "destination")
	} else {
		rd, err = rSP(in.D, "destination")
	}
	if err != nil {
		return err
	}
	if shifted {
		rn, err = rZR(in.N, "first source")
	} else {
		rn, err = rSP(in.N, "first source")
	}
	if err != nil {
		return err
	}

	head := sf<<31 | uint32(a.Op)<<30 | uint32(a.S)<<29

	switch in.M.Kind {
	case OImm:
		// A symbolic immediate is the low-12 half of an adrp/add pair.
		if in.M.Sym != "" {
			e.fixup(in.M.Sym, FixupAddAbsLo12Nc)
			e.word(isaa64.AddSubImmBase | head | rn<<5 | rd)
			return nil
		}
		if in.M.Imm < 0 {
			return fmt.Errorf("negative immediate %d: emit the opposite operation instead", in.M.Imm)
		}
		sh, imm12, ok := isaa64.EncodeAddSubImm(uint64(in.M.Imm))
		if !ok {
			return fmt.Errorf("%d is not an add/sub immediate", in.M.Imm)
		}
		e.word(isaa64.AddSubImmBase | head |
			uint32(sh)<<22 | uint32(imm12&0xFFF)<<10 | rn<<5 | rd)
		return nil

	case OReg:
		if in.M.Shift == isaa64.ShiftROR && !isaa64.ShiftAllowsROR(false) {
			return fmt.Errorf("ror is UNDEFINED for an add/sub shifted register")
		}
		rm, err := rZR(in.M, "second source")
		if err != nil {
			return err
		}
		amt, err := shiftAmt(in.M.ShiftAmt, in.W)
		if err != nil {
			return err
		}
		e.word(isaa64.AddSubShiftedBase | head |
			uint32(in.M.Shift&3)<<22 | rm<<16 | amt<<10 | rn<<5 | rd)
		return nil

	case OExt:
		rm, err := rZR(in.M, "second source")
		if err != nil {
			return err
		}
		if in.M.ExtAmt > 4 {
			return fmt.Errorf("extend shift %d is UNDEFINED (0-4)", in.M.ExtAmt)
		}
		e.word(isaa64.AddSubExtendedBase | head |
			rm<<16 | uint32(in.M.Ext&7)<<13 | uint32(in.M.ExtAmt&7)<<10 | rn<<5 | rd)
		return nil
	}
	return fmt.Errorf("second operand must be an immediate, shifted register, or extended register")
}

// ---------------------------------------------------------------------------
// Logical.
// ---------------------------------------------------------------------------

// logical encodes the immediate and shifted-register forms. HasImmForm is
// the table's record of the irregularity that matters: the negated variants
// (bic/orn/eon/bics) have no immediate encoding, because the immediate
// form's bit 21 is not an invert but part of the bitmask element size.
//
// Slot-31 roles: Rd is Xd|SP for the non-flag-setting *immediate* form only
// (that is what makes the MOV-bitmask alias reach SP); everywhere else Rd
// and Rn are Xn|ZR.
func (e *enc) logical(in *Inst, l isaa64.LogicalOp) error {
	sf := in.W.sf()
	head := sf<<31 | uint32(l.Opc)<<29

	switch in.M.Kind {
	case OImm:
		if !l.HasImmForm {
			return fmt.Errorf("%s has no immediate form; it exists only as a shifted register", in.Op)
		}
		if in.M.Sym != "" {
			return fmt.Errorf("a symbol address is not a bitmask immediate")
		}
		n, immr, imms, ok := isaa64.EncodeBitmaskImm(uint64(in.M.Imm), in.W.bits())
		if !ok {
			return fmt.Errorf("%#x is not a bitmask immediate at %d bits", uint64(in.M.Imm), in.W.bits())
		}
		var rd uint32
		var err error
		if l.SetsFlags {
			rd, err = rZR(in.D, "destination")
		} else {
			rd, err = rSP(in.D, "destination")
		}
		if err != nil {
			return err
		}
		rn, err := rZR(in.N, "first source")
		if err != nil {
			return err
		}
		e.word(isaa64.LogicalImmBase | head |
			uint32(n&1)<<22 | uint32(immr&0x3F)<<16 | uint32(imms&0x3F)<<10 | rn<<5 | rd)
		return nil

	case OReg:
		rd, err := rZR(in.D, "destination")
		if err != nil {
			return err
		}
		rn, err := rZR(in.N, "first source")
		if err != nil {
			return err
		}
		rm, err := rZR(in.M, "second source")
		if err != nil {
			return err
		}
		amt, err := shiftAmt(in.M.ShiftAmt, in.W)
		if err != nil {
			return err
		}
		e.word(isaa64.LogicalShiftedBase | head |
			uint32(in.M.Shift&3)<<22 | bit(l.Negate)<<21 |
			rm<<16 | amt<<10 | rn<<5 | rd)
		return nil
	}
	return fmt.Errorf("second operand must be an immediate or shifted register")
}

// ---------------------------------------------------------------------------
// Move wide.
// ---------------------------------------------------------------------------

func (e *enc) moveWide(in *Inst, m isaa64.MoveWideOp) error {
	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	v := in.Imm
	if in.M.Kind == OImm {
		v = in.M.Imm
	}
	if v < 0 || v > 0xFFFF {
		return fmt.Errorf("%s immediate %d does not fit 16 bits", in.Op, v)
	}
	hw, ok := isaa64.MoveWideHW(int(in.Imm2), in.W.isa())
	if !ok {
		return fmt.Errorf("shift %d is not a legal halfword position for a %d-bit operation", in.Imm2, in.W.bits())
	}
	e.word(isaa64.MoveWideBase | in.W.sf()<<31 | uint32(m.Opc)<<29 |
		uint32(hw)<<21 | uint32(v)<<5 | rd)
	return nil
}

// mov materializes a register-to-register move. It is ORR Rd, ZR, Rm in
// general, but the zero register cannot spell the stack pointer, so a move
// touching SP on either side has to go through ADD Rd, Rn, #0 instead —
// a machine constraint, not a preference.
//
// An immediate operand is rejected: building an arbitrary constant is a
// movz/movk chain or a bitmask ORR or a literal load, and choosing among
// those is a lowering decision (see isa/aarch64's README).
func (e *enc) mov(in *Inst) error {
	if in.M.Kind == OImm {
		return fmt.Errorf("mov of an immediate is a lowering decision: emit movz/movk, orr with a bitmask, or a literal load")
	}
	touchesSP := (in.D.Reg.IsSlot31() && in.D.SP) || (in.M.Reg.IsSlot31() && in.M.SP)
	if touchesSP {
		a, _ := isaa64.AddSubByName("add")
		alias := *in
		alias.N = in.M
		alias.M = Imm(0)
		return e.addSub(&alias, a)
	}
	l, _ := isaa64.LogicalByName("orr")
	alias := *in
	alias.N = R(ZR)
	return e.logical(&alias, l)
}

// ---------------------------------------------------------------------------
// Data-processing, two source (variable shifts and divides).
// ---------------------------------------------------------------------------

func (e *enc) dataProc2(in *Inst, d isaa64.DataProc2Op) error {
	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	rn, err := rZR(in.N, "first source")
	if err != nil {
		return err
	}
	rm, err := rZR(in.M, "second source")
	if err != nil {
		return err
	}
	e.word(isaa64.DataProc2Base | in.W.sf()<<31 |
		rm<<16 | uint32(d.Opcode&0x3F)<<10 | rn<<5 | rd)
	return nil
}

// shiftAlias routes lsl/lsr/asr/ror to whichever real instruction the second
// operand implies: a register amount is the *v form from the ISA table, an
// immediate amount is a bitfield alias (or EXTR, for ROR). This is the one
// place the encoder rewrites a mnemonic into a structurally different
// instruction, and it does so because the architecture defines these
// spellings *as* those aliases — not because it is picking a strategy.
func (e *enc) shiftAlias(in *Inst) error {
	switch in.M.Kind {
	case OReg:
		d, ok := isaa64.DataProc2ByName(in.Op + "v")
		if !ok {
			return fmt.Errorf("no register-amount form for %s", in.Op)
		}
		return e.dataProc2(in, d)

	case OImm:
		n := int64(in.W.bits())
		sh := in.M.Imm
		if sh < 0 || sh >= n {
			return fmt.Errorf("shift %d is out of range for a %d-bit operation", sh, n)
		}
		alias := *in
		switch in.Op {
		case "lsl":
			alias.Op = "ubfm"
			alias.Imm = (n - sh) % n // immr
			alias.Imm2 = n - 1 - sh  // imms
		case "lsr":
			alias.Op = "ubfm"
			alias.Imm, alias.Imm2 = sh, n-1
		case "asr":
			alias.Op = "sbfm"
			alias.Imm, alias.Imm2 = sh, n-1
		case "ror":
			// ROR #sh is EXTR Rd, Rn, Rn, #sh.
			alias.Op = "extr"
			alias.M = in.N
			alias.Imm = sh
			return e.extr(&alias)
		}
		return e.bitfield(&alias)
	}
	return fmt.Errorf("shift amount must be a register or an immediate")
}

// ---------------------------------------------------------------------------
// Bitfield.
// ---------------------------------------------------------------------------

// bitfield encodes SBFM/UBFM/BFM: sf opc 100110 N immr imms Rn Rd. The N bit
// must equal sf — a 32-bit bitfield with N set is UNDEFINED — so it is
// derived here rather than taken from the caller.
func (e *enc) bitfield(in *Inst) error {
	var opc uint32
	switch in.Op {
	case "sbfm":
		opc = 0
	case "bfm":
		opc = 1
	case "ubfm":
		opc = 2
	}
	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	rn, err := rZR(in.N, "source")
	if err != nil {
		return err
	}
	limit := int64(in.W.bits() - 1)
	if in.Imm < 0 || in.Imm > limit || in.Imm2 < 0 || in.Imm2 > limit {
		return fmt.Errorf("immr/imms (%d, %d) out of range for a %d-bit operation", in.Imm, in.Imm2, in.W.bits())
	}
	sf := in.W.sf()
	e.word(0x13000000 | sf<<31 | opc<<29 | sf<<22 |
		uint32(in.Imm)<<16 | uint32(in.Imm2)<<10 | rn<<5 | rd)
	return nil
}

// extendAlias encodes the sign/zero-extension mnemonics, which are bitfield
// aliases with fixed immr/imms. SXTW is 64-bit only: there is no 32-bit
// "extend a word to a word".
func (e *enc) extendAlias(in *Inst) error {
	alias := *in
	alias.Imm = 0
	switch in.Op {
	case "sxtb":
		alias.Op, alias.Imm2 = "sbfm", 7
	case "sxth":
		alias.Op, alias.Imm2 = "sbfm", 15
	case "sxtw":
		if in.W.is32() {
			return fmt.Errorf("sxtw has no 32-bit form")
		}
		alias.Op, alias.Imm2 = "sbfm", 31
	case "uxtb":
		alias.Op, alias.Imm2, alias.W = "ubfm", 7, W
	case "uxth":
		alias.Op, alias.Imm2, alias.W = "ubfm", 15, W
	}
	return e.bitfield(&alias)
}

// extr encodes EXTR Rd, Rn, Rm, #lsb — the double-word extract that ROR
// aliases when Rn == Rm.
func (e *enc) extr(in *Inst) error {
	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	rn, err := rZR(in.N, "high source")
	if err != nil {
		return err
	}
	rm, err := rZR(in.M, "low source")
	if err != nil {
		return err
	}
	if in.Imm < 0 || in.Imm >= int64(in.W.bits()) {
		return fmt.Errorf("lsb %d is out of range for a %d-bit operation", in.Imm, in.W.bits())
	}
	sf := in.W.sf()
	e.word(0x13800000 | sf<<31 | sf<<22 | rm<<16 | uint32(in.Imm)<<10 | rn<<5 | rd)
	return nil
}

// ---------------------------------------------------------------------------
// Data-processing, one source (bit counts and byte reversals).
// ---------------------------------------------------------------------------

func (e *enc) dataProc1(in *Inst) error {
	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	rn, err := rZR(in.N, "source")
	if err != nil {
		return err
	}
	var opcode uint32
	switch in.Op {
	case "rbit":
		opcode = 0x00
	case "rev16":
		opcode = 0x01
	case "rev32":
		if in.W.is32() {
			return fmt.Errorf("rev32 has no 32-bit form; use rev")
		}
		opcode = 0x02
	case "rev":
		// The opcode for a full reversal is width-dependent: 10 names a
		// word reversal, 11 a doubleword one.
		opcode = 0x02
		if !in.W.is32() {
			opcode = 0x03
		}
	case "clz":
		opcode = 0x04
	case "cls":
		opcode = 0x05
	}
	e.word(0x5AC00000 | in.W.sf()<<31 | opcode<<10 | rn<<5 | rd)
	return nil
}

// ---------------------------------------------------------------------------
// Data-processing, three source (multiply-accumulate).
// ---------------------------------------------------------------------------

// dataProc3 covers MADD/MSUB and the widening and high-half multiplies. The
// bare multiplies (mul, mneg, smull, ...) are the accumulate forms with Ra
// tied to the zero register, so they route through the same encoder.
func (e *enc) dataProc3(in *Inst) error {
	var op31, o0 uint32
	wide := false  // 32-bit sources, 64-bit result
	long := false  // 64-bit only regardless of in.W
	ra := in.A

	switch in.Op {
	case "mul":
		ra = R(ZR)
		fallthrough
	case "madd":
	case "mneg":
		ra = R(ZR)
		fallthrough
	case "msub":
		o0 = 1
	case "smull", "smnegl":
		ra = R(ZR)
		fallthrough
	case "smaddl", "smsubl":
		op31, wide = 1, true
		if in.Op == "smsubl" || in.Op == "smnegl" {
			o0 = 1
		}
	case "umull", "umnegl":
		ra = R(ZR)
		fallthrough
	case "umaddl", "umsubl":
		op31, wide = 5, true
		if in.Op == "umsubl" || in.Op == "umnegl" {
			o0 = 1
		}
	case "smulh":
		op31, ra, long = 2, R(ZR), true
	case "umulh":
		op31, ra, long = 6, R(ZR), true
	}

	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	rn, err := rZR(in.N, "first source")
	if err != nil {
		return err
	}
	rm, err := rZR(in.M, "second source")
	if err != nil {
		return err
	}
	rax, err := rZR(ra, "accumulate")
	if err != nil {
		return err
	}

	// The widening and high-half forms have a single width; sf is fixed at 1
	// there and in.W says nothing.
	sf := in.W.sf()
	if wide || long {
		sf = 1
	}
	e.word(0x1B000000 | sf<<31 | op31<<21 | rm<<16 | o0<<15 | rax<<10 | rn<<5 | rd)
	return nil
}

// ---------------------------------------------------------------------------
// Load / store.
// ---------------------------------------------------------------------------

// ldstShape is the (size, opc) pair a mnemonic-plus-width names, together
// with the scale that the unsigned-offset form applies to its imm12.
type ldstShape struct {
	size uint32 // bits 31:30 — log2 of the access size in bytes
	opc  uint32 // bits 23:22 — store / zero-extending load / sign-extending load
}

// shapeOf resolves the access. Note that in.W does double duty across this
// family, and the duty differs: for ldr/str it selects the *access* size,
// while for the sign-extending loads it selects the *destination register*
// width, the access size being fixed by the mnemonic. That is the machine's
// arrangement, not a convenience.
func shapeOf(op string, w Width) (ldstShape, error) {
	sf := w.sf()
	switch op {
	case "str":
		return ldstShape{size: 2 + sf, opc: 0}, nil
	case "ldr":
		return ldstShape{size: 2 + sf, opc: 1}, nil
	case "strb":
		return ldstShape{size: 0, opc: 0}, nil
	case "ldrb":
		return ldstShape{size: 0, opc: 1}, nil
	case "strh":
		return ldstShape{size: 1, opc: 0}, nil
	case "ldrh":
		return ldstShape{size: 1, opc: 1}, nil
	case "ldrsb":
		// opc 10 sign-extends to 64 bits, opc 11 to 32.
		return ldstShape{size: 0, opc: 3 - sf}, nil
	case "ldrsh":
		return ldstShape{size: 1, opc: 3 - sf}, nil
	case "ldrsw":
		return ldstShape{size: 2, opc: 2}, nil
	}
	return ldstShape{}, fmt.Errorf("not a load/store mnemonic")
}

func lo12FixupFor(size uint32) FixupKind {
	switch size {
	case 0:
		return FixupLdSt8AbsLo12Nc
	case 1:
		return FixupLdSt16AbsLo12Nc
	case 2:
		return FixupLdSt32AbsLo12Nc
	}
	return FixupLdSt64AbsLo12Nc
}

func (e *enc) loadStore(in *Inst) error {
	sh, err := shapeOf(in.Op, in.W)
	if err != nil {
		return err
	}
	rt, err := rZR(in.D, "transfer")
	if err != nil {
		return err
	}
	m := in.M
	if m.Kind != OMem {
		return fmt.Errorf("operand must be a memory reference")
	}
	rn, err := baseField(m)
	if err != nil {
		return err
	}
	head := sh.size<<30 | sh.opc<<22 | rn<<5 | rt

	// Register offset: size 111000 opc 1 Rm option S 10 Rn Rt.
	if m.Index != RNone {
		if m.Mode != MemOffset {
			return fmt.Errorf("a register index cannot be pre- or post-indexed")
		}
		if !m.Index.Encodable() {
			return fmt.Errorf("memory index names no encodable register")
		}
		// option<1> == 0 is UNDEFINED here: a sub-word index makes no sense
		// as an address, so only the word/doubleword extends are legal.
		if m.Ext&0b010 == 0 {
			return fmt.Errorf("index extend must be uxtw, uxtx/lsl, sxtw or sxtx")
		}
		e.word(0x38000000 | head | 1<<21 |
			uint32(m.Index.Field())<<16 | uint32(m.Ext&7)<<13 | bit(m.Scaled)<<12 | 0b10<<10)
		return nil
	}

	// [base, #:lo12:sym]: the low half of an adrp pair. The field is left
	// zero and the linker scales the symbol's low 12 bits to the access.
	if m.Sym != "" {
		if m.Mode != MemOffset {
			return fmt.Errorf("a symbolic offset cannot be pre- or post-indexed")
		}
		e.fixup(m.Sym, lo12FixupFor(sh.size))
		e.word(0x39000000 | head)
		return nil
	}

	scale := int64(1) << sh.size

	// The scaled unsigned-offset form is preferred where it reaches: it is
	// the only one with real range. Falling back to the unscaled imm9
	// (LDUR/STUR) for a small negative or unaligned offset is this
	// encoder's choice, not a machine requirement — both spell the same
	// access, and the alternative would be to reject offsets the hardware
	// can plainly carry.
	if m.Mode == MemOffset && m.Disp >= 0 && m.Disp%scale == 0 && m.Disp/scale <= 0xFFF {
		e.word(0x39000000 | head | uint32(m.Disp/scale)<<10)
		return nil
	}

	if m.Disp < -256 || m.Disp > 255 {
		return fmt.Errorf("offset %d does not fit the signed 9-bit field", m.Disp)
	}
	var tail uint32
	switch m.Mode {
	case MemOffset:
		tail = 0b00 << 10 // LDUR/STUR: unscaled, no write-back
	case MemPost:
		tail = 0b01 << 10
	case MemPre:
		tail = 0b11 << 10
	}
	e.word(0x38000000 | head | uint32(m.Disp&0x1FF)<<12 | tail)
	return nil
}

// loadStorePair encodes LDP/STP: opc 101000 mode L imm7 Rt2 Rn Rt. The imm7
// is signed and scaled by the access size, giving multiples of 4 in
// -256..252 for a W pair and multiples of 8 in -512..504 for an X pair.
func (e *enc) loadStorePair(in *Inst) error {
	load := in.Op == "ldp"
	rt, err := rZR(in.D, "first transfer")
	if err != nil {
		return err
	}
	rt2, err := rZR(in.A, "second transfer")
	if err != nil {
		return err
	}
	m := in.M
	if m.Kind != OMem {
		return fmt.Errorf("operand must be a memory reference")
	}
	if m.Index != RNone {
		return fmt.Errorf("a pair transfer has no register-index form")
	}
	rn, err := baseField(m)
	if err != nil {
		return err
	}

	// opc 00 is a W pair (scale 2), opc 10 an X pair (scale 3). opc 11 is
	// UNDEFINED and opc 01 is LDPSW, which is not spelled ldp.
	var opc, scale uint32
	if in.W.is32() {
		opc, scale = 0, 2
	} else {
		opc, scale = 2, 3
	}
	step := int64(1) << scale
	if m.Disp%step != 0 {
		return fmt.Errorf("offset %d is not a multiple of %d", m.Disp, step)
	}
	imm7 := m.Disp / step
	if imm7 < -64 || imm7 > 63 {
		return fmt.Errorf("offset %d does not fit the scaled 7-bit field", m.Disp)
	}

	var mode uint32
	switch m.Mode {
	case MemPost:
		mode = 0b01
	case MemOffset:
		mode = 0b10
	case MemPre:
		mode = 0b11
	}
	e.word(0x28000000 | opc<<30 | mode<<23 | bit(load)<<22 |
		uint32(imm7&0x7F)<<15 | rt2<<10 | rn<<5 | rt)
	return nil
}

// ---------------------------------------------------------------------------
// PC-relative address formation.
// ---------------------------------------------------------------------------

// adr encodes ADR and ADRP, whose 21-bit immediate is split across two
// non-adjacent fields: immlo in bits 30:29 and immhi in bits 23:5. ADRP's
// value is a *page* offset — the immediate is implicitly shifted left 12 —
// which is why it pairs with a lo12 add or load.
//
// Only the symbolic form is emitted here. A local ADR to a label would need
// its own patch shape, and reaching a local datum is a lowering question
// (adrp+add versus a literal load) rather than an encoding one.
func (e *enc) adr(in *Inst) error {
	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	sym := in.Sym
	if sym == "" && in.M.Kind == OImm {
		sym = in.M.Sym
	}
	if sym == "" {
		return fmt.Errorf("%s needs a symbol", in.Op)
	}
	kind := FixupAdrPrelLo21
	base := uint32(0x10000000)
	if in.Op == "adrp" {
		kind = FixupAdrPrelPgHi21
		base = 0x90000000
	}
	e.fixup(sym, kind)
	e.word(base | rd)
	return nil
}

// ---------------------------------------------------------------------------
// Branches.
// ---------------------------------------------------------------------------

// target records a local label patch or a symbol relocation for a branch
// word that has already been placed at pos.
func (e *enc) target(in *Inst, fld branchField, kind FixupKind) error {
	switch {
	case in.Lbl != "":
		e.patches = append(e.patches, patch{pos: len(e.b), lbl: in.Lbl, fld: fld})
		return nil
	case in.Sym != "":
		e.fixup(in.Sym, kind)
		return nil
	}
	return fmt.Errorf("branch has neither label nor symbol")
}

func (e *enc) branch(in *Inst) error {
	link := in.Op == "bl"
	kind := FixupJump26
	if link {
		kind = FixupCall26
	}
	if err := e.target(in, fld26, kind); err != nil {
		return err
	}
	e.word(0x14000000 | bit(link)<<31)
	return nil
}

func (e *enc) branchCond(in *Inst) error {
	if in.CC > 15 {
		return fmt.Errorf("condition %d is not a 4-bit code", in.CC)
	}
	if err := e.target(in, fld19, FixupCondBr19); err != nil {
		return err
	}
	e.word(0x54000000 | uint32(in.CC&0xF))
	return nil
}

func (e *enc) compareBranch(in *Inst) error {
	rt, err := rZR(in.D, "test")
	if err != nil {
		return err
	}
	if err := e.target(in, fld19, FixupCondBr19); err != nil {
		return err
	}
	e.word(0x34000000 | in.W.sf()<<31 | bit(in.Op == "cbnz")<<24 | rt)
	return nil
}

// testBranch encodes TBZ/TBNZ. The bit number is itself split: b5 lives in
// bit 31, where every other format keeps sf, and b40 in bits 23:19. So the
// operand width is not encoded at all here — it is implied by which half of
// the register the bit falls in.
func (e *enc) testBranch(in *Inst) error {
	rt, err := rZR(in.D, "test")
	if err != nil {
		return err
	}
	if in.Imm < 0 || in.Imm > 63 {
		return fmt.Errorf("bit number %d is out of range", in.Imm)
	}
	if in.W.is32() && in.Imm > 31 {
		return fmt.Errorf("bit %d is not in a 32-bit register", in.Imm)
	}
	b := uint32(in.Imm)
	if err := e.target(in, fld14, FixupTestBr14); err != nil {
		return err
	}
	e.word(0x36000000 | (b>>5)<<31 | bit(in.Op == "tbnz")<<24 | (b&0x1F)<<19 | rt)
	return nil
}

// branchReg encodes the register-target branches. RET's operand defaults to
// x30: the link register is architectural, so an omitted target is not a
// guess but the encoding's own default.
func (e *enc) branchReg(in *Inst) error {
	n := in.N
	if in.Op == "ret" && n.Kind == ONone {
		n = R(LR)
	}
	rn, err := rZR(n, "target")
	if err != nil {
		return err
	}
	var base uint32
	switch in.Op {
	case "br":
		base = 0xD61F0000
	case "blr":
		base = 0xD63F0000
	case "ret":
		base = 0xD65F0000
	}
	e.word(base | rn<<5)
	return nil
}

// ---------------------------------------------------------------------------
// Conditional select and conditional compare.
// ---------------------------------------------------------------------------

func (e *enc) condSelect(in *Inst) error {
	var op, o2 uint32
	switch in.Op {
	case "csel":
	case "csinc":
		o2 = 1
	case "csinv":
		op = 1
	case "csneg":
		op, o2 = 1, 1
	}
	rd, err := rZR(in.D, "destination")
	if err != nil {
		return err
	}
	rn, err := rZR(in.N, "true source")
	if err != nil {
		return err
	}
	rm, err := rZR(in.M, "false source")
	if err != nil {
		return err
	}
	if in.CC > 15 {
		return fmt.Errorf("condition %d is not a 4-bit code", in.CC)
	}
	e.word(0x1A800000 | in.W.sf()<<31 | op<<30 |
		rm<<16 | uint32(in.CC&0xF)<<12 | o2<<10 | rn<<5 | rd)
	return nil
}

// condSelectAlias encodes cset/csetm/cinc/cinv/cneg. Every one of them
// *inverts* the condition, because the underlying instruction returns Rn
// when the condition holds and the transformed Rm when it does not — so
// "set on EQ" is "select the increment of zero on NE". Negate(AL) would
// yield the NV spelling, so an unconditional cset is rejected rather than
// silently encoded.
func (e *enc) condSelectAlias(in *Inst) error {
	if in.CC == AL || in.CC == isaa64.CondNV {
		return fmt.Errorf("%s needs a real condition; al/nv have no inverse", in.Op)
	}
	alias := *in
	alias.CC = Negate(in.CC)
	switch in.Op {
	case "cset":
		alias.Op, alias.N, alias.M = "csinc", R(ZR), R(ZR)
	case "csetm":
		alias.Op, alias.N, alias.M = "csinv", R(ZR), R(ZR)
	case "cinc":
		alias.Op, alias.M = "csinc", in.N
	case "cinv":
		alias.Op, alias.M = "csinv", in.N
	case "cneg":
		alias.Op, alias.M = "csneg", in.N
	}
	return e.condSelect(&alias)
}

// condCompare encodes CCMP/CCMN, which compare only if the condition holds
// and otherwise write the caller's nzcv literal into the flags. The second
// operand is a register or a 5-bit unsigned immediate — a much narrower
// field than the arithmetic immediate, and a separate encoding rather than
// a special case of it.
func (e *enc) condCompare(in *Inst) error {
	rn, err := rZR(in.N, "first source")
	if err != nil {
		return err
	}
	if in.CC > 15 {
		return fmt.Errorf("condition %d is not a 4-bit code", in.CC)
	}
	if in.Imm < 0 || in.Imm > 15 {
		return fmt.Errorf("nzcv %d is not a 4-bit value", in.Imm)
	}
	op := bit(in.Op == "ccmp")
	head := 0x1A400000 | in.W.sf()<<31 | op<<30 | 1<<29 |
		uint32(in.CC&0xF)<<12 | rn<<5 | uint32(in.Imm&0xF)

	switch in.M.Kind {
	case OImm:
		if in.M.Imm < 0 || in.M.Imm > 31 {
			return fmt.Errorf("%d does not fit the 5-bit compare immediate", in.M.Imm)
		}
		e.word(head | uint32(in.M.Imm)<<16 | 1<<11)
		return nil
	case OReg:
		rm, err := rZR(in.M, "second source")
		if err != nil {
			return err
		}
		e.word(head | rm<<16)
		return nil
	}
	return fmt.Errorf("second operand must be a register or a 5-bit immediate")
}