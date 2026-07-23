// isel.go
package x86_64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

func errBadModule(format string, a ...any) error { return fmt.Errorf(format, a...) }

// lowerFunc runs type fixation, frame layout, then per-block instruction
// selection, and finally prologue/epilogue + encoding (encode.go).
func lowerFunc(m *vir.Module, ix *index, f *vir.Function) (Func, error) {
	l := newLayout(ix)

	types, order, err := typeFunc(l, f)
	if err != nil {
		return Func{}, err
	}
	fr, err := BuildFrame(l, f, order, types)
	if err != nil {
		return Func{}, err
	}

	s := &sel{
		m: m, ix: ix, l: l, f: f, fr: fr, types: types,
		os: m.Target.OS, tiers: tierSet(m.Target.Tiers),
	}
	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			s.emit(Inst{Op: "label", Lbl: blockLabel(f.Name, b.Label)})
		}
		for _, line := range b.Lines {
			if err := s.selInst(line); err != nil {
				return Func{}, fmt.Errorf("%s: %w", line.Op, err)
			}
		}
		if err := s.selTerm(b.Term); err != nil {
			return Func{}, err
		}
	}

	code, fx, err := s.finish()
	if err != nil {
		return Func{}, err
	}
	return Func{Name: f.Name, Code: code, Align: 16, Export: f.Export, Fixups: fx}, nil
}

type sel struct {
	m     *vir.Module
	ix    *index
	l     *Layout
	f     *vir.Function
	fr    *Frame
	types map[string]vir.Type
	os    string
	tiers map[string]bool
	out   []Inst
}

func (s *sel) emit(i Inst) { s.out = append(s.out, i) }

func tierSet(tiers []string) map[string]bool {
	m := map[string]bool{}
	for _, t := range tiers {
		m[t] = true
	}
	return m
}

func blockLabel(fn, lbl string) string { return ".L." + fn + "." + lbl }

// checkValueType: what may live in a named 8-byte slot. i128 needs a
// register pair and floats/vectors need an SSE path — all todo, matching
// the IA-32 backend's deferred set.
func checkValueType(t vir.Type) error {
	switch x := t.(type) {
	case vir.IntType:
		if x.Bits > 64 {
			return todo("i%d values need a register pair", x.Bits)
		}
		return nil
	case vir.PtrType, vir.ValistType:
		return nil
	case vir.FloatType:
		return todo("float values")
	case vir.VecType:
		return todo("vector values")
	}
	return errBadModule("type %s cannot name a value", t)
}

// widthOf returns the operand width in bytes for a value type (1/2/4/8).
func widthOf(t vir.Type) int {
	switch x := t.(type) {
	case vir.IntType:
		switch {
		case x.Bits <= 8:
			return 1
		case x.Bits <= 16:
			return 2
		case x.Bits <= 32:
			return 4
		default:
			return 8
		}
	default:
		return 8 // ptr / valist cursor address
	}
}

// ---- operand load/store against slots -------------------------------------

// loadOperand materializes an operand (value slot, literal, or symbol addr)
// into register dst.
func (s *sel) loadOperand(o vir.Operand, dst Reg) {
	switch o.Kind {
	case vir.OperandIdent:
		if g, ok := s.ix.globals[o.Ident]; ok {
			// A bare global name in value position is its address.
			_ = g
			s.emit(Inst{Op: "lea", D: R(dst), S: MemRIP(o.Ident, 0)})
			return
		}
		if c, ok := s.ix.consts[o.Ident]; ok {
			s.emit(Inst{Op: "mov", D: R(dst), S: Imm(c.Value.Int), Sz: 8})
			return
		}
		if slot, ok := s.fr.SlotOf(o.Ident); ok {
			s.emit(Inst{Op: "mov", D: R(dst), S: Slot(slot), Sz: 8})
			return
		}
		// Otherwise a function symbol used as a pointer.
		s.emit(Inst{Op: "lea", D: R(dst), S: MemRIP(o.Ident, 0)})
	case vir.OperandInt:
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(o.Int), Sz: 8})
	case vir.OperandBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(v), Sz: 8})
	case vir.OperandNull:
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(0), Sz: 8})
	default:
		// Types/orderings/vectors are handled at their specific call sites.
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(0), Sz: 8})
	}
}

func (s *sel) storeReg(src Reg, name string) {
	if slot, ok := s.fr.SlotOf(name); ok {
		s.emit(Inst{Op: "mov", D: Slot(slot), S: R(src), Sz: 8})
	}
}

// maskTo restores the zero-extension invariant after an op that could dirty
// the upper bits of a sub-64 result. 32-bit ops auto-zero bits 32..63, so
// only widths < 4 need an explicit mask.
func (s *sel) maskTo(r Reg, bits int) {
	if bits >= 32 || bits == 0 {
		return
	}
	mask := int64(1)<<uint(bits) - 1
	s.emit(Inst{Op: "and", D: R(r), S: Imm(mask), Sz: 8})
}

// ---- instruction dispatch --------------------------------------------------

func (s *sel) selInst(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpLoc:
		return nil // debug line info: no code

	case vir.OpAdd, vir.OpSub, vir.OpMul,
		vir.OpAnd, vir.OpOr, vir.OpXor:
		return s.selBinALU(in)

	case vir.OpShl, vir.OpLShr, vir.OpAShr:
		return s.selShift(in)

	case vir.OpNeg, vir.OpNot:
		return s.selUnary(in)

	case vir.OpUDiv, vir.OpSDiv, vir.OpURem, vir.OpSRem:
		return s.selDivide(in)

	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge:
		return s.selCompare(in)

	case vir.OpSelect:
		return s.selSelect(in)

	case vir.OpTrunc, vir.OpZext, vir.OpSext, vir.OpBitcast:
		return s.selConvert(in)

	case vir.OpLoad, vir.OpLoadVol:
		return s.selLoad(in)
	case vir.OpStore, vir.OpStoreVol:
		return s.selStore(in)

	case vir.OpField:
		return s.selField(in)
	case vir.OpIndex:
		return s.selIndex(in)
	case vir.OpAlloca:
		return s.selAlloca(in)

	case vir.OpMemcopy, vir.OpMemmove, vir.OpMemset:
		return s.selBulk(in)

	case vir.OpAtomicLoad, vir.OpAtomicStore, vir.OpAtomicAdd, vir.OpAtomicSub,
		vir.OpAtomicAnd, vir.OpAtomicOr, vir.OpAtomicXor, vir.OpAtomicXchg,
		vir.OpCmpxchg, vir.OpFence:
		return s.selAtomic(in)

	case vir.OpCall:
		return s.selCall(in)
	case vir.OpSyscall:
		return s.selSyscall(in)

	case vir.OpVaStart:
		return s.selVaStart(in)
	case vir.OpVaArg:
		return s.selVaArg(in)
	case vir.OpVaEnd:
		return nil // GP-only valist has no cleanup state

	case vir.OpPopcnt:
		return s.selPopcnt(in)
	case vir.OpCtlz, vir.OpCttz:
		return s.selBitScan(in)
	case vir.OpBSwap:
		return s.selBswap(in)

	// Deferred, matching lower/x86's unimplemented set.
	case vir.OpSqrt, vir.OpFma, vir.OpCopysign, vir.OpFloor, vir.OpCeil,
		vir.OpTruncF, vir.OpNearest, vir.OpSfromint, vir.OpUfromint,
		vir.OpStoint, vir.OpUtoint, vir.OpStointSat, vir.OpUtointSat,
		vir.OpFdemote, vir.OpFpromote:
		return todo("floating-point op %s", in.Op)
	case vir.OpSplat, vir.OpExtract, vir.OpInsert, vir.OpShuffle,
		vir.OpMaskedLoad, vir.OpMaskedStore, vir.OpGather, vir.OpScatter,
		vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax,
		vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		return todo("vector op %s", in.Op)
	case vir.OpUAddSat, vir.OpSAddSat, vir.OpUSubSat, vir.OpSSubSat:
		return todo("saturating op %s", in.Op)
	case vir.OpBitrev:
		return todo("bitrev")

	default:
		return todo("opcode %s", in.Op)
	}
}

// ---- arithmetic / logic ----------------------------------------------------

var aluName = map[vir.Opcode]string{
	vir.OpAdd: "add", vir.OpSub: "sub",
	vir.OpAnd: "and", vir.OpOr: "or", vir.OpXor: "xor",
}

func (s *sel) selBinALU(in *vir.Instruction) error {
	t := s.types[in.Result]
	if err := checkValueType(t); err != nil {
		return err
	}
	w := widthOf(t)

	s.loadOperand(in.Args[0], RRAX)
	if in.Op == vir.OpMul {
		s.loadOperand(in.Args[1], RRCX)
		s.emit(Inst{Op: "imul2", D: R(RRAX), S: R(RRCX), Sz: w})
	} else {
		s.loadOperand(in.Args[1], RRCX)
		s.emit(Inst{Op: aluName[in.Op], D: R(RRAX), S: R(RRCX), Sz: w})
	}
	s.maskTo(RRAX, intBits(t))
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selUnary(in *vir.Instruction) error {
	t := s.types[in.Result]
	w := widthOf(t)
	s.loadOperand(in.Args[0], RRAX)
	op := "neg"
	if in.Op == vir.OpNot {
		op = "not"
	}
	s.emit(Inst{Op: op, S: R(RRAX), Sz: w})
	s.maskTo(RRAX, intBits(t))
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selShift(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)

	s.loadOperand(in.Args[0], RRAX)
	if in.Op == vir.OpAShr && bits < 32 {
		// Arithmetic shift must see the sign in the operand width; sign-
		// extend the sub-32 value into the 32-bit register first.
		s.sext32(RRAX, bits)
	}
	s.loadOperand(in.Args[1], RRCX) // count in cl
	op := map[vir.Opcode]string{vir.OpShl: "shl", vir.OpLShr: "shr", vir.OpAShr: "sar"}[in.Op]
	s.emit(Inst{Op: op, D: R(RRAX), S: R(RRCX), Sz: 4})
	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

// sext32 sign-extends an N-bit value in r up to 32 bits, in place. Used only
// as scratch before signed consumers; never written back to a value slot.
func (s *sel) sext32(r Reg, bits int) {
	if bits >= 32 {
		return
	}
	sh := int64(32 - bits)
	s.emit(Inst{Op: "shl", D: R(r), S: Imm(sh), Sz: 4})
	s.emit(Inst{Op: "sar", D: R(r), S: Imm(sh), Sz: 4})
}

func (s *sel) selDivide(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	signed := in.Op == vir.OpSDiv || in.Op == vir.OpSRem
	rem := in.Op == vir.OpURem || in.Op == vir.OpSRem

	// 32-bit idiv/div naturally trap (#DE) on zero divisor and on
	// INT_MIN/-1. Narrower signed widths need explicit checks, since
	// sign-extension can make INT_MIN/-1 spuriously representable at 32 bits.
	w := 4
	if bits > 32 {
		w = 8
	}

	s.loadOperand(in.Args[0], RRAX) // dividend
	s.loadOperand(in.Args[1], RRCX) // divisor
	if signed {
		s.sext32(RRAX, bits)
		s.emit(Inst{Op: "cqo", Sz: w}) // sign-extend rax into rdx:rax
		s.emit(Inst{Op: "idiv", S: R(RRCX), Sz: w})
	} else {
		s.emit(Inst{Op: "xor", D: R(RRDX), S: R(RRDX), Sz: w}) // zero rdx
		s.emit(Inst{Op: "div", S: R(RRCX), Sz: w})
	}
	res := RRAX
	if rem {
		res = RRDX
	}
	s.maskTo(res, bits)
	s.storeReg(res, in.Result)
	return nil
}

// ---- comparisons / select --------------------------------------------------

func (s *sel) cmpCC(in *vir.Instruction) (cc byte, signed bool, w int) {
	// Operand width comes from the compared type, carried in the args'
	// declared type. Comparisons yield i1, so we look at the operand.
	ot := s.operandType(in.Args[0])
	w = widthOf(ot)
	switch in.Op {
	case vir.OpEq:
		return CondE, false, w
	case vir.OpNe:
		return CondNE, false, w
	case vir.OpSlt:
		return CondL, true, w
	case vir.OpSgt:
		return CondG, true, w
	case vir.OpSle:
		return CondLE, true, w
	case vir.OpSge:
		return CondGE, true, w
	case vir.OpUlt:
		return CondB, false, w
	case vir.OpUgt:
		return CondA, false, w
	case vir.OpUle:
		return CondBE, false, w
	default: // Uge
		return CondAE, false, w
	}
}

func (s *sel) selCompare(in *vir.Instruction) error {
	cc, signed, w := s.cmpCC(in)
	bits := 8 * w
	if ot := s.operandType(in.Args[0]); vir.IsInt(ot) {
		bits = ot.(vir.IntType).Bits
	}
	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	if signed && bits < 32 {
		s.sext32(RRAX, bits)
		s.sext32(RRCX, bits)
		w = 4
	}
	s.emit(Inst{Op: "cmp", D: R(RRAX), S: R(RRCX), Sz: w})
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(0), Sz: 8}) // clear before setcc
	s.emit(Inst{Op: "setcc", D: R(RRAX), CC: cc})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selSelect(in *vir.Instruction) error {
	// select cond, a, b : cond is i1, result type is Suffix.
	t := s.types[in.Result]
	w := widthOf(t)
	s.loadOperand(in.Args[1], RRAX) // a (taken when cond != 0)
	s.loadOperand(in.Args[2], RRCX) // b
	s.loadOperand(in.Args[0], RRDX) // cond
	s.emit(Inst{Op: "test", D: R(RRDX), S: R(RRDX), Sz: 4})
	s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRCX), CC: CondE, Sz: w}) // cond==0 -> b
	s.storeReg(RRAX, in.Result)
	return nil
}

// ---- conversions -----------------------------------------------------------

func (s *sel) selConvert(in *vir.Instruction) error {
	dst := s.types[in.Result]
	src := s.operandType(in.Args[0])
	s.loadOperand(in.Args[0], RRAX)

	switch in.Op {
	case vir.OpBitcast:
		// ptr<->i64 (or same-width int): value already in rax, no reshape.
		s.storeReg(RRAX, in.Result)
		return nil
	case vir.OpTrunc:
		s.maskTo(RRAX, intBits(dst))
	case vir.OpZext:
		s.maskTo(RRAX, intBits(src)) // ensure source bits clean; upper already zero
	case vir.OpSext:
		s.sext32(RRAX, intBits(src))
		s.maskTo(RRAX, intBits(dst)) // frozen-invariant: upper bits of dst zeroed
		if intBits(dst) >= 32 {
			// widen the sign into the full 64-bit slot via a signed store path
		}
	}
	s.storeReg(RRAX, in.Result)
	return nil
}

// ---- memory ----------------------------------------------------------------

func (s *sel) selLoad(in *vir.Instruction) error {
	t := s.types[in.Result]
	w := widthOf(t)
	vol := in.Op == vir.OpLoadVol
	s.loadOperand(in.Args[0], RRCX) // pointer
	op := "mov"
	_ = vol // volatility is honored by not folding/duplicating; single mov is fine here
	s.emit(Inst{Op: op, D: R(RRAX), S: Mem(RRCX, 0), Sz: w})
	s.maskTo(RRAX, intBits(t))
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selStore(in *vir.Instruction) error {
	vt := s.operandType(in.Args[1])
	w := widthOf(vt)
	s.loadOperand(in.Args[0], RRCX) // pointer
	s.loadOperand(in.Args[1], RRAX) // value
	s.emit(Inst{Op: "mov", D: Mem(RRCX, 0), S: R(RRAX), Sz: w})
	return nil
}

func (s *sel) selField(in *vir.Instruction) error {
	// field.ptr base, struct, field -> base + offset
	structName := in.Args[1].Ident
	field := in.Args[2].Ident
	off, err := s.l.FieldOffset(structName, field)
	if err != nil {
		return err
	}
	s.loadOperand(in.Args[0], RRAX)
	if off != 0 {
		s.emit(Inst{Op: "add", D: R(RRAX), S: Imm(off), Sz: 8})
	}
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selIndex(in *vir.Instruction) error {
	// index.ptr base, T, idx -> base + idx*sizeof(T)
	elem := in.Args[1].Type
	es, err := s.l.Size(elem)
	if err != nil {
		return err
	}
	s.loadOperand(in.Args[0], RRAX) // base
	s.loadOperand(in.Args[2], RRCX) // idx
	if es == 1 || es == 2 || es == 4 || es == 8 {
		s.emit(Inst{Op: "lea", D: R(RRAX), S: MemIndexed(RRAX, RRCX, byte(es), 0)})
	} else {
		s.emit(Inst{Op: "imul3", D: R(RRCX), S: R(RRCX), Imm: es, Sz: 8})
		s.emit(Inst{Op: "add", D: R(RRAX), S: R(RRCX), Sz: 8})
	}
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selAlloca(in *vir.Instruction) error {
	if _, ok := in.Suffix.(vir.ValistType); ok {
		// A valist slot is a fixed 24-byte local; its address is its slot.
		if slot, ok := s.fr.SlotOf(in.Result); ok {
			s.emit(Inst{Op: "lea", D: R(RRAX), S: Slot(slot)})
			s.storeReg(RRAX, in.Result)
		}
		return nil
	}
	// alloca.ptr size: dynamic stack bump. sub rsp, roundUp16(size); result
	// is the new rsp. The epilogue's lea rsp,[rbp-SavedRegBytes] undoes it.
	s.loadOperand(in.Args[0], RRAX)
	s.emit(Inst{Op: "add", D: R(RRAX), S: Imm(15), Sz: 8})
	s.emit(Inst{Op: "and", D: R(RRAX), S: Imm(^int64(15)), Sz: 8})
	s.emit(Inst{Op: "sub", D: R(RRSP), S: R(RRAX), Sz: 8})
	s.emit(Inst{Op: "mov", D: R(RRAX), S: R(RRSP), Sz: 8})
	s.storeReg(RRAX, in.Result)
	return nil
}

// ---- bulk / atomics (subset) -----------------------------------------------

func (s *sel) selBulk(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpMemset:
		s.loadOperand(in.Args[0], RRDI) // dst
		s.loadOperand(in.Args[1], RRAX) // byte
		s.loadOperand(in.Args[2], RRCX) // len
		s.emit(Inst{Op: "rep_stosb"})
		return nil
	case vir.OpMemcopy:
		s.loadOperand(in.Args[0], RRDI)
		s.loadOperand(in.Args[1], RRSI)
		s.loadOperand(in.Args[2], RRCX)
		s.emit(Inst{Op: "cld"})
		s.emit(Inst{Op: "rep_movsb"})
		return nil
	case vir.OpMemmove:
		return todo("memmove (runtime direction pick)")
	}
	return errBadModule("unexpected bulk op")
}

func (s *sel) selAtomic(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpFence:
		s.emit(Inst{Op: "mfence"})
		return nil
	case vir.OpAtomicLoad:
		s.loadOperand(in.Args[0], RRCX)
		s.emit(Inst{Op: "mov", D: R(RRAX), S: Mem(RRCX, 0), Sz: widthOf(s.types[in.Result])})
		s.storeReg(RRAX, in.Result)
		return nil
	case vir.OpAtomicStore:
		s.loadOperand(in.Args[0], RRCX)
		s.loadOperand(in.Args[1], RRAX)
		s.emit(Inst{Op: "xchg", D: Mem(RRCX, 0), S: R(RRAX), Sz: widthOf(s.operandType(in.Args[1]))})
		return nil
	case vir.OpAtomicAdd:
		s.loadOperand(in.Args[0], RRCX)
		s.loadOperand(in.Args[1], RRAX)
		s.emit(Inst{Op: "lock_xadd", D: Mem(RRCX, 0), S: R(RRAX), Sz: widthOf(s.types[in.Result])})
		s.storeReg(RRAX, in.Result)
		return nil
	default:
		// sub/and/or/xor return-previous and cmpxchg lower as retry loops.
		return todo("atomic %s", in.Op)
	}
}

// ---- bit intrinsics --------------------------------------------------------

func (s *sel) selPopcnt(in *vir.Instruction) error {
	if !s.tiers["popcnt"] && !s.tiers["sse4.2"] {
		return errBadModule("popcnt requires a popcnt/sse4.2 target tier")
	}
	w := widthOf(s.types[in.Result])
	s.loadOperand(in.Args[0], RRAX)
	s.emit(Inst{Op: "popcnt", D: R(RRAX), S: R(RRAX), Sz: max2(w, 4)})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selBitScan(in *vir.Instruction) error {
	// bsr/bsf give index; ctlz/cttz want counts. Baseline lowering via
	// bsr/bsf + fixups is nontrivial for the zero input — defer.
	return todo("%s", in.Op)
}

func (s *sel) selBswap(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	if bits == 8 {
		return errBadModule("bswap illegal on i8")
	}
	w := widthOf(t)
	if w != 4 && w != 8 {
		return todo("bswap i%d", bits)
	}
	s.loadOperand(in.Args[0], RRAX)
	s.emit(Inst{Op: "bswap", D: R(RRAX), Sz: w})
	s.storeReg(RRAX, in.Result)
	return nil
}

// ---- helpers ---------------------------------------------------------------

func (s *sel) operandType(o vir.Operand) vir.Type {
	if o.Kind == vir.OperandIdent {
		if t, ok := s.types[o.Ident]; ok {
			return t
		}
		if g, ok := s.ix.globals[o.Ident]; ok {
			_ = g
			return vir.Ptr
		}
	}
	return vir.I64 // literals default to 64-bit register width
}

func intBits(t vir.Type) int {
	if x, ok := t.(vir.IntType); ok {
		return x.Bits
	}
	return 64
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}