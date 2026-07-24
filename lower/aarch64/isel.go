// isel.go
package aarch64

import (
	"fmt"
	"math/bits"

	"github.com/vertex-language/vvm/ir/vir"
	isaa64 "github.com/vertex-language/vvm/isa/aarch64"
	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// Scratch registers. Every named value lives in a slot, so selection needs
// only a working set — and every register in it is caller-saved or an
// intra-procedure-call scratch, which is why the prologue saves nothing but
// fp/lr.
const (
	RegA    = encoder.R9  // primary accumulator
	RegB    = encoder.R10 // second operand
	RegC    = encoder.R11 // spare
	RegD    = encoder.R12 // spare
	RegAddr = encoder.R16 // IP0: addresses, large-frame arithmetic, call targets
	RegAux  = encoder.R17 // IP1: second address
)

type sel struct {
	ix    *index
	fn    *vir.Function
	fr    *Frame
	types map[string]vir.Type
	out   []Inst
	nlbl  int
}

func (s *sel) emit(in Inst) { s.out = append(s.out, in) }

// label mints a function-local label that cannot collide with an IR block
// label, which the grammar restricts to plain identifiers.
func (s *sel) label() string {
	s.nlbl++
	return fmt.Sprintf(".La64.%d", s.nlbl)
}

func (s *sel) mark(l string) { s.emit(Inst{Op: "label", Lbl: l}) }

// ---------------------------------------------------------------------------
// Widths and the zero-extension invariant.
// ---------------------------------------------------------------------------

// bitsOf returns a value type's width in bits.
func (s *sel) bitsOf(t vir.Type) (int, error) {
	switch x := t.(type) {
	case vir.IntType:
		return x.Bits, nil
	case vir.PtrType, vir.ValistType:
		return 64, nil
	}
	return 0, todo("%s has no integer width", t)
}

// widthFor picks the sf bit. A 32-bit operation writes zeroes into bits
// 63:32 for free, which is what keeps the invariant cheap for every width at
// or below 32.
func widthFor(b int) encoder.Width {
	if b > 32 {
		return encoder.X
	}
	return encoder.W
}

// maskTo restores the zero-extension invariant: a value of type iN occupies
// its slot with the upper 64-N bits zero.
//
// At 32 bits a plain `mov w, w` is the whole job (the W-form write clears the
// top half). Below 32, `and` takes the mask directly: 0x1/0xFF/0xFFFF are all
// bitmask immediates, so unlike A32 — which needs a bic pair for 0xFFFF —
// there is nothing to synthesise.
func (s *sel) maskTo(r encoder.Reg, b int) {
	switch {
	case b >= 64:
	case b == 32:
		s.emit(Inst{Op: "mov", W: encoder.W, D: R(r), M: R(r)})
	default:
		s.emit(Inst{Op: "and", D: R(r), N: R(r), M: Imm(int64(lowMask(b)))})
	}
}

// sextTo sign-extends a scratch copy in place. Only signed consumers
// (sdiv/srem/asr/signed compares/sext) do this, and never back into a value
// slot.
func (s *sel) sextTo(r encoder.Reg, b int) {
	switch b {
	case 64:
	case 32:
		s.emit(Inst{Op: "sxtw", D: R(r), N: R(r)})
	case 16:
		s.emit(Inst{Op: "sxth", D: R(r), N: R(r)})
	case 8:
		s.emit(Inst{Op: "sxtb", D: R(r), N: R(r)})
	case 1:
		// No mnemonic for a one-bit extend; the underlying bitfield is
		// SBFM Xd, Xn, #0, #0.
		s.emit(Inst{Op: "sbfm", D: R(r), N: R(r), Imm: 0, Imm2: 0})
	}
}

func lowMask(b int) uint64 {
	if b >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << uint(b)) - 1
}

// ---------------------------------------------------------------------------
// Constants.
// ---------------------------------------------------------------------------

// movImm materializes a 64-bit constant. Three forms, cheapest first: a
// logical bitmask ORR, a single MOVZ/MOVN, then a MOVZ/MOVK chain. Which one
// to use is precisely the lowering decision isa/aarch64's README hands to
// this layer — the encoder's `mov` refuses an immediate for that reason.
//
// Materialization is always 64-bit, so the upper bits are zero by
// construction and the slot invariant needs no repair.
func (s *sel) movImm(dst encoder.Reg, v uint64) {
	if v == 0 {
		s.emit(Inst{Op: "movz", D: R(dst), Imm: 0, Imm2: 0})
		return
	}
	if isaa64.FitsBitmaskImm(v, 64) {
		s.emit(Inst{Op: "orr", D: R(dst), N: R(encoder.ZR), M: Imm(int64(v))})
		return
	}

	hw := func(x uint64, i int) uint64 { return (x >> uint(16*i)) & 0xFFFF }

	var zeros, ones int
	for i := 0; i < 4; i++ {
		switch hw(v, i) {
		case 0:
			zeros++
		case 0xFFFF:
			ones++
		}
	}

	if ones > zeros {
		// MOVN writes ~(imm16 << hw): every other halfword comes out all
		// ones, so patch each halfword that is not.
		first := -1
		for i := 0; i < 4; i++ {
			if hw(v, i) != 0xFFFF {
				first = i
				break
			}
		}
		s.emit(Inst{Op: "movn", D: R(dst), Imm: int64(^hw(v, first) & 0xFFFF), Imm2: int64(16 * first)})
		for i := 0; i < 4; i++ {
			if i != first && hw(v, i) != 0xFFFF {
				s.emit(Inst{Op: "movk", D: R(dst), Imm: int64(hw(v, i)), Imm2: int64(16 * i)})
			}
		}
		return
	}

	first := -1
	for i := 0; i < 4; i++ {
		if hw(v, i) != 0 {
			first = i
			break
		}
	}
	s.emit(Inst{Op: "movz", D: R(dst), Imm: int64(hw(v, first)), Imm2: int64(16 * first)})
	for i := first + 1; i < 4; i++ {
		if hw(v, i) != 0 {
			s.emit(Inst{Op: "movk", D: R(dst), Imm: int64(hw(v, i)), Imm2: int64(16 * i)})
		}
	}
}

// addImm emits `dst = src + v` for a possibly-large signed v, falling back to
// a materialized register when the 12-bit (optionally shifted) field cannot
// carry it. sp is legal in either position only through the immediate and
// extended forms, so the fallback uses the extended one.
func (s *sel) addImm(dst, src encoder.Reg, v int64, dstSP, srcSP bool) {
	d, n := R(dst), R(src)
	if dstSP {
		d = Rsp()
	}
	if srcSP {
		n = Rsp()
	}
	op := "add"
	mag := v
	if v < 0 {
		op, mag = "sub", -v
	}
	if isaa64.FitsAddSubImm(uint64(mag)) {
		s.emit(Inst{Op: op, D: d, N: n, M: Imm(mag)})
		return
	}
	s.movImm(RegAddr, uint64(mag))
	s.emit(Inst{Op: op, D: d, N: n, M: RExt(RegAddr, encoder.UXTX, 0)})
}

// cmpImm compares reg against a constant, choosing between the immediate
// form, its cmn mirror for a negative value, and a materialized register.
func (s *sel) cmpImm(r encoder.Reg, v int64, w encoder.Width) {
	switch {
	case v >= 0 && isaa64.FitsAddSubImm(uint64(v)):
		s.emit(Inst{Op: "cmp", W: w, N: R(r), M: Imm(v)})
	case v < 0 && isaa64.FitsAddSubImm(uint64(-v)):
		s.emit(Inst{Op: "cmn", W: w, N: R(r), M: Imm(-v)})
	default:
		s.movImm(RegC, uint64(v))
		s.emit(Inst{Op: "cmp", W: w, N: R(r), M: R(RegC)})
	}
}

// ---------------------------------------------------------------------------
// Operand access.
// ---------------------------------------------------------------------------

// value loads an operand into dst. A named value comes out of its slot; a
// literal is materialized; a global or function name becomes its address via
// the adrp/add page pair — the position-independent long-mode idiom, and the
// only one the encoder names.
func (s *sel) value(dst encoder.Reg, o vir.Operand, hint vir.Type) error {
	switch o.Kind {
	case vir.OperandInt:
		return s.constInt(dst, o.Int, hint)
	case vir.OperandBool:
		v := uint64(0)
		if o.Bool {
			v = 1
		}
		s.movImm(dst, v)
		return nil
	case vir.OperandNull:
		s.movImm(dst, 0)
		return nil
	case vir.OperandFloat:
		return todo("float literal")
	case vir.OperandIdent:
		if o.IsQualified() {
			return fmt.Errorf("qualified operand %s: importer.Rewrite has not run", o)
		}
		if _, ok := s.types[o.Ident]; ok {
			s.emit(Inst{Op: "ldr", D: R(dst), M: Slot(o.Ident)})
			return nil
		}
		if c, ok := s.ix.consts[o.Ident]; ok {
			return s.value(dst, c.Value, c.Type)
		}
		if sym, ok := s.ix.symOf[o.Ident]; ok {
			if g, isGlobal := s.ix.globals[o.Ident]; isGlobal && g.TLS {
				return todo("tls global %s needs a thread-pointer sequence", o.Ident)
			}
			s.emit(Inst{Op: "adrp", D: R(dst), Sym: sym})
			s.emit(Inst{Op: "add", D: R(dst), N: R(dst), M: SymAddr(sym)})
			return nil
		}
		return fmt.Errorf("unresolved operand %s", o.Ident)
	}
	return fmt.Errorf("operand %s is not a value", o)
}

// constInt materializes an integer literal already truncated to its type's
// width, keeping the zero-extension invariant for a negative literal.
func (s *sel) constInt(dst encoder.Reg, v int64, hint vir.Type) error {
	b := 64
	if hint != nil {
		n, err := s.bitsOf(hint)
		if err != nil {
			return err
		}
		b = n
	}
	s.movImm(dst, uint64(v)&lowMask(b))
	return nil
}

// store writes a scratch register back to a named value's slot.
func (s *sel) store(name string, r encoder.Reg) {
	if name == "" {
		return
	}
	s.emit(Inst{Op: "str", D: R(r), M: Slot(name)})
}

// binary is the shape almost every two-operand instruction takes: load, run
// the op at the value's width, restore the invariant, store.
func (s *sel) binary(in *vir.Instruction, op string) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	w := widthFor(b)
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}
	s.emit(Inst{Op: op, W: w, D: R(RegA), N: R(RegA), M: R(RegB)})
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// ---------------------------------------------------------------------------
// Function lowering.
// ---------------------------------------------------------------------------

func lowerFunc(ix *index, f *vir.Function) (*Func, error) {
	types, order, err := typeFunc(ix, f)
	if err != nil {
		return nil, err
	}
	fr, err := BuildFrame(ix.layout, f, types, order, ix.stackVarargs)
	if err != nil {
		return nil, err
	}

	s := &sel{ix: ix, fn: f, fr: fr, types: types}
	for i, b := range f.AllBlocks() {
		if i > 0 {
			s.mark(b.Label)
		}
		for _, in := range b.Lines {
			if err := s.instruction(in); err != nil {
				return nil, fmt.Errorf("block %q: %s: %w", b.Label, in.Op, err)
			}
		}
		if b.Term == nil {
			return nil, fmt.Errorf("block %q has no terminator", b.Label)
		}
		if err := s.terminator(b.Term); err != nil {
			return nil, fmt.Errorf("block %q terminator: %w", b.Label, err)
		}
	}

	code, fx, err := assemble(s)
	if err != nil {
		return nil, err
	}
	return &Func{
		Name:   ix.symOf[f.Name],
		Code:   code,
		Align:  4, // every A64 instruction is word-aligned and word-sized
		Export: f.Export,
		Fixups: fx,
	}, nil
}

// ---------------------------------------------------------------------------
// The instruction switch.
// ---------------------------------------------------------------------------

func (s *sel) instruction(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpLoc:
		return nil // no debug info yet

	case vir.OpAdd:
		return s.binary(in, "add")
	case vir.OpSub:
		return s.binary(in, "sub")
	case vir.OpMul:
		return s.binary(in, "mul")
	case vir.OpAnd:
		return s.binary(in, "and")
	case vir.OpOr:
		return s.binary(in, "orr")
	case vir.OpXor:
		return s.binary(in, "eor")

	case vir.OpNeg:
		return s.unary(in, "neg")
	case vir.OpNot:
		return s.unary(in, "mvn")

	case vir.OpAbs:
		return s.selAbs(in)

	case vir.OpUDiv, vir.OpSDiv, vir.OpURem, vir.OpSRem:
		return s.selDivide(in)

	case vir.OpUMulH, vir.OpSMulH:
		return s.selMulHigh(in)

	case vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO, vir.OpUMulO, vir.OpSMulO:
		return s.selOverflow(in)

	case vir.OpUAddSat, vir.OpSAddSat, vir.OpUSubSat, vir.OpSSubSat:
		return s.selSat(in)

	case vir.OpShl, vir.OpLShr, vir.OpAShr:
		return s.selShift(in)
	case vir.OpRotl, vir.OpRotr:
		return s.selRotate(in)

	case vir.OpCtlz:
		return s.selCtlz(in)
	case vir.OpCttz:
		return s.selCttz(in)
	case vir.OpPopcnt:
		// A64's population count is CNT, a SIMD instruction on a V register.
		// There is no GP form and the encoder has no FP/SIMD at all.
		return todo("popcnt needs the SIMD cnt instruction")
	case vir.OpBitrev:
		return s.selBitrev(in)
	case vir.OpBSwap:
		return s.selBswap(in)

	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge:
		return s.selCompare(in)
	case vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe:
		return todo("float comparison")

	case vir.OpSMin, vir.OpSMax, vir.OpUMin, vir.OpUMax:
		return s.selMinMax(in)
	case vir.OpMin, vir.OpMax:
		return todo("float min/max")

	case vir.OpSelect:
		return s.selSelect(in)

	case vir.OpAlloca:
		return s.selAlloca(in)
	case vir.OpLoad, vir.OpLoadVol:
		return s.selLoad(in)
	case vir.OpStore, vir.OpStoreVol:
		return s.selStore(in)
	case vir.OpField:
		return s.selField(in)
	case vir.OpIndex:
		return s.selIndex(in)
	case vir.OpMemcopy, vir.OpMemmove, vir.OpMemset:
		return s.selBulk(in)

	case vir.OpAtomicLoad, vir.OpAtomicStore, vir.OpAtomicAdd, vir.OpAtomicSub,
		vir.OpAtomicAnd, vir.OpAtomicOr, vir.OpAtomicXor, vir.OpAtomicXchg,
		vir.OpCmpxchg, vir.OpFence:
		// See the encoder-dependency note in the README: isa/aarch64/encoder's
		// switch has no ldxr/stxr/ldar/stlr/dmb, so there is nothing correct
		// to emit. A non-atomic sequence would be silently wrong.
		return todo("%s needs ldxr/stxr/ldar/stlr/dmb in the encoder", in.Op)

	case vir.OpTrunc:
		return s.selTrunc(in)
	case vir.OpZext:
		return s.selZext(in)
	case vir.OpSext:
		return s.selSext(in)
	case vir.OpBitcast:
		return s.selBitcast(in)
	case vir.OpFdemote, vir.OpFpromote, vir.OpSfromint, vir.OpUfromint,
		vir.OpStoint, vir.OpUtoint, vir.OpStointSat, vir.OpUtointSat:
		return todo("float conversion %s", in.Op)

	case vir.OpSqrt, vir.OpFma, vir.OpCopysign, vir.OpFloor, vir.OpCeil,
		vir.OpTruncF, vir.OpNearest:
		return todo("float intrinsic %s", in.Op)

	case vir.OpSplat, vir.OpExtract, vir.OpInsert, vir.OpShuffle,
		vir.OpMaskedLoad, vir.OpMaskedStore, vir.OpGather, vir.OpScatter,
		vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax,
		vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		return todo("vector op %s", in.Op)

	case vir.OpPrefetch:
		// A hint with no architectural effect on correctness. PRFM is not in
		// the encoder; dropping it is semantically exact.
		return nil

	case vir.OpCall:
		return s.selCall(in)
	case vir.OpSyscall:
		return s.selSyscall(in)

	case vir.OpVaStart:
		return s.selVaStart(in)
	case vir.OpVaArg:
		return s.selVaArg(in)
	case vir.OpVaEnd:
		return s.selVaEnd(in)
	}
	return fmt.Errorf("unhandled opcode %s", in.Op)
}

func (s *sel) unary(in *vir.Instruction, op string) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	s.emit(Inst{Op: op, W: widthFor(b), D: R(RegA), M: R(RegA)})
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// selAbs uses CNEG, which negates when the *inverse* of the given condition
// holds — so "negate if LT" is exactly |x| for a sign-extended operand.
func (s *sel) selAbs(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	w := widthFor(b)
	s.sextTo(RegA, b)
	s.emit(Inst{Op: "cmp", W: w, N: R(RegA), M: Imm(0)})
	s.emit(Inst{Op: "cneg", W: w, D: R(RegA), N: R(RegA), CC: encoder.LT})
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// ---------------------------------------------------------------------------
// Saturating arithmetic.
// ---------------------------------------------------------------------------

func (s *sel) selSat(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSAddSat || in.Op == vir.OpSSubSat

	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}

	// For types narrower than 64 bits, extend them to 64 bits where the operation
	// cannot overflow, execute it there, and clamp it to the representable range.
	if b < 64 {
		if signed {
			s.sextTo(RegA, b)
			s.sextTo(RegB, b)
		} else {
			s.maskTo(RegA, b)
			s.maskTo(RegB, b)
		}
		if in.Op == vir.OpUAddSat || in.Op == vir.OpSAddSat {
			s.emit(Inst{Op: "add", W: encoder.X, D: R(RegA), N: R(RegA), M: R(RegB)})
		} else {
			s.emit(Inst{Op: "sub", W: encoder.X, D: R(RegA), N: R(RegA), M: R(RegB)})
		}

		var max, min uint64
		if signed {
			max = (uint64(1) << (b - 1)) - 1
			min = ^max
		} else {
			max = (uint64(1) << b) - 1
			min = 0
		}

		s.movImm(RegB, max)
		s.emit(Inst{Op: "cmp", W: encoder.X, N: R(RegA), M: R(RegB)})
		s.emit(Inst{Op: "csel", W: encoder.X, D: R(RegA), N: R(RegB), M: R(RegA), CC: encoder.GT})

		if signed || in.Op == vir.OpUSubSat {
			s.movImm(RegB, min)
			s.emit(Inst{Op: "cmp", W: encoder.X, N: R(RegA), M: R(RegB)})
			s.emit(Inst{Op: "csel", W: encoder.X, D: R(RegA), N: R(RegB), M: R(RegA), CC: encoder.LT})
		}
		s.maskTo(RegA, b)
		s.store(in.Result, RegA)
		return nil
	}

	w := widthFor(b)
	switch in.Op {
	case vir.OpUAddSat:
		// Unsigned add overflow = CS. csinv yields RegA if CC, or ~ZR (UINT64_MAX) if CS.
		s.emit(Inst{Op: "adds", W: w, D: R(RegA), N: R(RegA), M: R(RegB)})
		s.emit(Inst{Op: "csinv", W: w, D: R(RegA), N: R(RegA), M: R(ZR), CC: encoder.CC})
	case vir.OpUSubSat:
		// Unsigned sub underflow = CC. csel yields RegA if CS, or ZR (0) if CC.
		s.emit(Inst{Op: "subs", W: w, D: R(RegA), N: R(RegA), M: R(RegB)})
		s.emit(Inst{Op: "csel", W: w, D: R(RegA), N: R(RegA), M: R(ZR), CC: encoder.CS})
	case vir.OpSAddSat, vir.OpSSubSat:
		op := "adds"
		if in.Op == vir.OpSSubSat {
			op = "subs"
		}
		s.emit(Inst{Op: op, W: w, D: R(RegC), N: R(RegA), M: R(RegB)})
		// Extract the original sign of RegA: RegD = 0 if RegA >= 0, or -1 if RegA < 0.
		s.emit(Inst{Op: "asr", W: w, D: R(RegD), N: R(RegA), M: Imm(63)})
		// ~((1<<63)-1) == 1<<63 == INT64_MIN, which maps (RegD=0 => MaxInt, RegD=-1 => MinInt)
		s.movImm(RegA, (uint64(1)<<63)-1)
		s.emit(Inst{Op: "eor", W: w, D: R(RegA), N: R(RegA), M: R(RegD)})
		// If VS (overflow), choose clamped value. Otherwise choose computed result in RegC.
		s.emit(Inst{Op: "csel", W: w, D: R(RegA), N: R(RegA), M: R(RegC), CC: encoder.VS})
	}
	s.store(in.Result, RegA)
	return nil
}

// ---------------------------------------------------------------------------
// Division.
// ---------------------------------------------------------------------------

// selDivide is the sharpest divergence from both x86 backends. There, 32-bit
// idiv/div *trap* on a zero divisor and on INT_MIN / -1, and the ISA does the
// work §4.1 and §5.3 require for free.
//
// A64's SDIV and UDIV do not trap at all: division by zero yields zero and
// INT_MIN / -1 yields INT_MIN, both quietly. So every check §4.1 mandates is
// emitted explicitly here, branching to a UDF. That is not a defensive
// nicety; without it the IR's trap semantics would simply not hold.
func (s *sel) selDivide(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSDiv || in.Op == vir.OpSRem
	rem := in.Op == vir.OpURem || in.Op == vir.OpSRem
	w := widthFor(b)

	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}

	ok := s.label()
	trap := s.label()

	// Zero divisor.
	s.emit(Inst{Op: "cbz", W: w, D: R(RegB), Lbl: trap})

	if signed {
		// INT_MIN / -1. Tested at the *operand's* width: sign-extending
		// first would make a narrow INT_MIN/-1 spuriously representable.
		s.sextTo(RegA, b)
		s.sextTo(RegB, b)
		cont := s.label()
		s.cmpImm(RegB, -1, w)
		s.emit(Inst{Op: "b.cond", CC: encoder.NE, Lbl: cont})
		s.cmpImm(RegA, int64(-1)<<uint(b-1), w)
		s.emit(Inst{Op: "b.cond", CC: encoder.EQ, Lbl: trap})
		s.mark(cont)
	}
	s.emit(Inst{Op: "b", Lbl: ok})
	s.mark(trap)
	s.emit(Inst{Op: "udf"})
	s.mark(ok)

	div := "udiv"
	if signed {
		div = "sdiv"
	}
	s.emit(Inst{Op: div, W: w, D: R(RegC), N: R(RegA), M: R(RegB)})
	if rem {
		// No remainder instruction: a - (a/b)*b, which MSUB does in one.
		s.emit(Inst{Op: "msub", W: w, D: R(RegC), N: R(RegC), M: R(RegB), A: R(RegA)})
	}
	s.maskTo(RegC, b)
	s.store(in.Result, RegC)
	return nil
}

// selMulHigh computes the high half of the double-width product. At 64 bits
// that is a single UMULH/SMULH; below it, the full product fits one register
// and the high half is a shift away.
func (s *sel) selMulHigh(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSMulH
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}
	if b == 64 {
		op := "umulh"
		if signed {
			op = "smulh"
		}
		s.emit(Inst{Op: op, D: R(RegA), N: R(RegA), M: R(RegB)})
		s.store(in.Result, RegA)
		return nil
	}
	if signed {
		s.sextTo(RegA, b)
		s.sextTo(RegB, b)
	}
	s.emit(Inst{Op: "mul", D: R(RegA), N: R(RegA), M: R(RegB)})
	shift := "lsr"
	if signed {
		shift = "asr"
	}
	s.emit(Inst{Op: shift, D: R(RegA), N: R(RegA), M: Imm(int64(b))})
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// selOverflow lowers the six overflow predicates. At the native width the
// flags answer directly; below it the operands are extended into 64 bits,
// where the operation cannot overflow, and the result is compared against
// its own truncation.
func (s *sel) selOverflow(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSAddO || in.Op == vir.OpSSubO || in.Op == vir.OpSMulO
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}

	if b == 64 {
		var op string
		var cc encoder.Cond
		switch in.Op {
		case vir.OpUAddO:
			op, cc = "adds", encoder.CS
		case vir.OpSAddO:
			op, cc = "adds", encoder.VS
		case vir.OpUSubO:
			op, cc = "subs", encoder.CC
		case vir.OpSSubO:
			op, cc = "subs", encoder.VS
		case vir.OpUMulO:
			s.emit(Inst{Op: "umulh", D: R(RegC), N: R(RegA), M: R(RegB)})
			s.emit(Inst{Op: "cmp", N: R(RegC), M: Imm(0)})
			s.emit(Inst{Op: "cset", D: R(RegA), CC: encoder.NE})
			s.store(in.Result, RegA)
			return nil
		case vir.OpSMulO:
			// The product overflows exactly when its high half is not the
			// sign extension of its low half.
			s.emit(Inst{Op: "smulh", D: R(RegC), N: R(RegA), M: R(RegB)})
			s.emit(Inst{Op: "mul", D: R(RegD), N: R(RegA), M: R(RegB)})
			s.emit(Inst{Op: "cmp", N: R(RegC), M: RShift(RegD, encoder.ASR, 63)})
			s.emit(Inst{Op: "cset", D: R(RegA), CC: encoder.NE})
			s.store(in.Result, RegA)
			return nil
		}
		s.emit(Inst{Op: op, D: R(RegC), N: R(RegA), M: R(RegB)})
		s.emit(Inst{Op: "cset", D: R(RegA), CC: cc})
		s.store(in.Result, RegA)
		return nil
	}

	if signed {
		s.sextTo(RegA, b)
		s.sextTo(RegB, b)
	}
	var op string
	switch in.Op {
	case vir.OpUAddO, vir.OpSAddO:
		op = "add"
	case vir.OpUSubO, vir.OpSSubO:
		op = "sub"
	default:
		op = "mul"
	}
	s.emit(Inst{Op: op, D: R(RegC), N: R(RegA), M: R(RegB)})

	if signed {
		// Overflow iff the exact 64-bit result differs from its own
		// truncation-and-sign-extension.
		s.emit(Inst{Op: "mov", D: R(RegD), M: R(RegC)})
		s.sextTo(RegD, b)
		s.emit(Inst{Op: "cmp", N: R(RegC), M: R(RegD)})
		s.emit(Inst{Op: "cset", D: R(RegA), CC: encoder.NE})
		s.store(in.Result, RegA)
		return nil
	}
	if in.Op == vir.OpUSubO {
		// Unsigned subtraction overflows exactly when a < b, and the
		// zero-extended compare says so directly.
		s.emit(Inst{Op: "cmp", N: R(RegA), M: R(RegB)})
		s.emit(Inst{Op: "cset", D: R(RegA), CC: encoder.LO})
		s.store(in.Result, RegA)
		return nil
	}
	s.emit(Inst{Op: "lsr", D: R(RegC), N: R(RegC), M: Imm(int64(b))})
	s.emit(Inst{Op: "cmp", N: R(RegC), M: Imm(0)})
	s.emit(Inst{Op: "cset", D: R(RegA), CC: encoder.NE})
	s.store(in.Result, RegA)
	return nil
}

// ---------------------------------------------------------------------------
// Shifts and rotates.
// ---------------------------------------------------------------------------

// selShift masks the shift count to the operand width explicitly for narrow
// types. A64's variable shifts mask the amount modulo the *datasize* (32 or
// 64), which is §4.1's rule only when the value's width is the datasize.
func (s *sel) selShift(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	w := widthFor(b)
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}
	if b < 32 {
		s.emit(Inst{Op: "and", D: R(RegB), N: R(RegB), M: Imm(int64(b - 1))})
	}
	switch in.Op {
	case vir.OpShl:
		s.emit(Inst{Op: "lsl", W: w, D: R(RegA), N: R(RegA), M: R(RegB)})
	case vir.OpLShr:
		s.emit(Inst{Op: "lsr", W: w, D: R(RegA), N: R(RegA), M: R(RegB)})
	case vir.OpAShr:
		s.sextTo(RegA, b)
		s.emit(Inst{Op: "asr", W: w, D: R(RegA), N: R(RegA), M: R(RegB)})
	}
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// selRotate uses the native ROR at 32 and 64 bits and synthesises the
// narrower widths, which have no machine rotate to borrow.
func (s *sel) selRotate(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}
	w := widthFor(b)

	if b >= 32 {
		if in.Op == vir.OpRotl {
			// ROL k == ROR (width - k); the negation is free modulo width.
			s.emit(Inst{Op: "neg", W: w, D: R(RegB), M: R(RegB)})
		}
		s.emit(Inst{Op: "ror", W: w, D: R(RegA), N: R(RegA), M: R(RegB)})
		s.maskTo(RegA, b)
		s.store(in.Result, RegA)
		return nil
	}

	s.emit(Inst{Op: "and", D: R(RegB), N: R(RegB), M: Imm(int64(b - 1))})
	s.movImm(RegC, uint64(b))
	s.emit(Inst{Op: "sub", D: R(RegC), N: R(RegC), M: R(RegB)}) // width - k
	right, left := RegB, RegC
	if in.Op == vir.OpRotl {
		right, left = RegC, RegB
	}
	s.emit(Inst{Op: "lsr", D: R(RegD), N: R(RegA), M: R(right)})
	s.emit(Inst{Op: "lsl", D: R(RegA), N: R(RegA), M: R(left)})
	s.emit(Inst{Op: "orr", D: R(RegA), N: R(RegA), M: R(RegD)})
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// selCtlz counts leading zeros within the value's own width. For a narrow
// type the value is shifted to the top of a 32-bit register so CLZ counts
// from the right place, and a sentinel bit is planted just below it so the
// all-zero input yields N rather than 32 — cheaper than branching on zero,
// and it cannot perturb a nonzero input because every real bit is above it.
func (s *sel) selCtlz(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	switch {
	case b == 64:
		s.emit(Inst{Op: "clz", D: R(RegA), N: R(RegA)})
	case b == 32:
		s.emit(Inst{Op: "clz", W: encoder.W, D: R(RegA), N: R(RegA)})
	default:
		s.emit(Inst{Op: "lsl", W: encoder.W, D: R(RegA), N: R(RegA), M: Imm(int64(32 - b))})
		s.emit(Inst{Op: "orr", W: encoder.W, D: R(RegA), N: R(RegA), M: Imm(1 << uint(31-b))})
		s.emit(Inst{Op: "clz", W: encoder.W, D: R(RegA), N: R(RegA)})
	}
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// selCttz is RBIT followed by CLZ. The same sentinel trick applies, planted
// at bit N so a zero input reports N.
func (s *sel) selCttz(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	w := widthFor(b)
	if b < 32 {
		s.emit(Inst{Op: "orr", W: w, D: R(RegA), N: R(RegA), M: Imm(1 << uint(b))})
	}
	s.emit(Inst{Op: "rbit", W: w, D: R(RegA), N: R(RegA)})
	s.emit(Inst{Op: "clz", W: w, D: R(RegA), N: R(RegA)})
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// selBitrev is one RBIT plus a shift back down. Unlike both 32-bit backends,
// which have no bit-reverse instruction at all and mark bitrev a todo, A64
// has RBIT and the operation is nearly free.
//
// RBIT reverses the *whole* operation width, so an iN narrower than the
// datasize lands in the top N bits of the result and has to be shifted back
// down. The shift is by hostBits-N, not 64-N: a value at or below 32 bits is
// reversed by the W-form, whose datasize is 32.
func (s *sel) selBitrev(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	w := widthFor(b)
	hostBits := 32
	if w == encoder.X {
		hostBits = 64
	}
	s.emit(Inst{Op: "rbit", W: w, D: R(RegA), N: R(RegA)})
	if hostBits != b {
		s.emit(Inst{Op: "lsr", W: w, D: R(RegA), N: R(RegA), M: Imm(int64(hostBits - b))})
	}
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

func (s *sel) selBswap(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	switch b {
	case 8:
		return fmt.Errorf("bswap.i8 is illegal (§9.20)")
	case 16:
		// REV16 reverses within each halfword; the zero-extension invariant
		// makes the upper halves a no-op.
		s.emit(Inst{Op: "rev16", W: encoder.W, D: R(RegA), N: R(RegA)})
	case 32:
		s.emit(Inst{Op: "rev", W: encoder.W, D: R(RegA), N: R(RegA)})
	case 64:
		s.emit(Inst{Op: "rev", D: R(RegA), N: R(RegA)})
	default:
		return fmt.Errorf("bswap.i%d has no byte-reversal form", b)
	}
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// ---------------------------------------------------------------------------
// Comparisons and selection.
// ---------------------------------------------------------------------------

func (s *sel) selCompare(in *vir.Instruction) error {
	t := in.Suffix
	b := 64
	if !vir.IsPtr(t) {
		n, err := s.bitsOf(t)
		if err != nil {
			return err
		}
		b = n
	}
	var cc encoder.Cond
	signed := false
	switch in.Op {
	case vir.OpEq:
		cc = encoder.EQ
	case vir.OpNe:
		cc = encoder.NE
	case vir.OpSlt:
		cc, signed = encoder.LT, true
	case vir.OpSgt:
		cc, signed = encoder.GT, true
	case vir.OpSle:
		cc, signed = encoder.LE, true
	case vir.OpSge:
		cc, signed = encoder.GE, true
	case vir.OpUlt:
		cc = encoder.LO
	case vir.OpUgt:
		cc = encoder.HI
	case vir.OpUle:
		cc = encoder.LS
	case vir.OpUge:
		cc = encoder.HS
	}

	if err := s.value(RegA, in.Args[0], t); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], t); err != nil {
		return err
	}
	if signed {
		s.sextTo(RegA, b)
		s.sextTo(RegB, b)
	}
	w := widthFor(b)
	s.emit(Inst{Op: "cmp", W: w, N: R(RegA), M: R(RegB)})
	s.emit(Inst{Op: "cset", D: R(RegA), CC: cc})
	s.store(in.Result, RegA)
	return nil
}

func (s *sel) selMinMax(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSMin || in.Op == vir.OpSMax
	var cc encoder.Cond
	switch in.Op {
	case vir.OpSMin:
		cc = encoder.LT
	case vir.OpSMax:
		cc = encoder.GT
	case vir.OpUMin:
		cc = encoder.LO
	case vir.OpUMax:
		cc = encoder.HI
	}
	if err := s.value(RegA, in.Args[0], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[1], in.Suffix); err != nil {
		return err
	}
	if signed {
		s.sextTo(RegA, b)
		s.sextTo(RegB, b)
	}
	w := widthFor(b)
	s.emit(Inst{Op: "cmp", W: w, N: R(RegA), M: R(RegB)})
	s.emit(Inst{Op: "csel", W: w, D: R(RegA), N: R(RegA), M: R(RegB), CC: cc})
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

func (s *sel) selSelect(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegC, in.Args[0], vir.I1); err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[1], in.Suffix); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[2], in.Suffix); err != nil {
		return err
	}
	s.emit(Inst{Op: "cmp", W: encoder.W, N: R(RegC), M: Imm(0)})
	s.emit(Inst{Op: "csel", W: widthFor(b), D: R(RegA), N: R(RegA), M: R(RegB), CC: encoder.NE})
	s.store(in.Result, RegA)
	return nil
}

// ---------------------------------------------------------------------------
// Memory.
// ---------------------------------------------------------------------------

// selAlloca moves sp down and hands back the new sp. Because locals are
// addressed at *positive* offsets from a frame pointer that never moves, a
// dynamic alloca disturbs nothing already assigned — and the epilogue
// restores sp from fp rather than undoing arithmetic it cannot see.
func (s *sel) selAlloca(in *vir.Instruction) error {
	if vir.IsValist(in.Suffix) {
		// A valist slot is the value's own frame slot: the cursor is one
		// word, so there is nothing to allocate.
		return nil
	}
	if len(in.Args) == 0 {
		return fmt.Errorf("alloca.ptr has no size operand")
	}
	if in.Args[0].Kind == vir.OperandInt {
		n := roundUp(uint32(in.Args[0].Int), StackAlign)
		if in.Align > StackAlign {
			return todo("alloca with alignment %d beyond the 16-byte stack alignment", in.Align)
		}
		s.addImm(encoder.SPr, encoder.SPr, -int64(n), true, true)
	} else {
		if err := s.value(RegA, in.Args[0], vir.I64); err != nil {
			return err
		}
		// Round the dynamic size up to 16: sp is unusable while misaligned.
		// -StackAlign is the AND mask that clears the low alignment bits
		// (^(StackAlign-1) in two's complement), computed this way because
		// StackAlign is a power of two and the bitwise-complement form
		// overflows int64 as a compile-time constant.
		s.emit(Inst{Op: "add", D: R(RegA), N: R(RegA), M: Imm(StackAlign - 1)})
		s.emit(Inst{Op: "and", D: R(RegA), N: R(RegA), M: Imm(-int64(StackAlign))})
		// The shifted-register form cannot name sp at all; the extended form
		// takes Xn|SP in both Rd and Rn.
		s.emit(Inst{Op: "sub", D: Rsp(), N: Rsp(), M: RExt(RegA, encoder.UXTX, 0)})
	}
	s.emit(Inst{Op: "mov", D: R(RegA), M: Rsp()})
	s.store(in.Result, RegA)
	return nil
}

func (s *sel) selLoad(in *vir.Instruction) error {
	if err := s.value(RegAddr, in.Args[0], vir.Ptr); err != nil {
		return err
	}
	t := in.Suffix
	if vir.IsAggregate(t) {
		return fmt.Errorf("load.%s of an aggregate is illegal", t)
	}
	b, err := s.bitsOf(t)
	if err != nil {
		return err
	}
	// Always the zero-extending form, which is the invariant the rest of
	// selection reads slots under.
	switch {
	case b <= 8:
		s.emit(Inst{Op: "ldrb", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	case b <= 16:
		s.emit(Inst{Op: "ldrh", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	case b <= 32:
		s.emit(Inst{Op: "ldr", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	default:
		s.emit(Inst{Op: "ldr", D: R(RegA), M: Mem(RegAddr, 0)})
	}
	if b == 1 {
		s.maskTo(RegA, 1)
	}
	s.store(in.Result, RegA)
	return nil
}

func (s *sel) selStore(in *vir.Instruction) error {
	if err := s.value(RegAddr, in.Args[0], vir.Ptr); err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[1], in.Suffix); err != nil {
		return err
	}
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	switch {
	case b <= 8:
		s.emit(Inst{Op: "strb", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	case b <= 16:
		s.emit(Inst{Op: "strh", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	case b <= 32:
		s.emit(Inst{Op: "str", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	default:
		s.emit(Inst{Op: "str", D: R(RegA), M: Mem(RegAddr, 0)})
	}
	return nil
}

func (s *sel) selField(in *vir.Instruction) error {
	if len(in.Args) != 3 {
		return fmt.Errorf("field.ptr needs base, struct, field")
	}
	off, _, err := s.ix.layout.FieldOffset(in.Args[1].Ident, in.Args[2].Ident)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], vir.Ptr); err != nil {
		return err
	}
	if off != 0 {
		s.addImm(RegA, RegA, int64(off), false, false)
	}
	s.store(in.Result, RegA)
	return nil
}

func (s *sel) selIndex(in *vir.Instruction) error {
	if len(in.Args) != 3 {
		return fmt.Errorf("index.ptr needs base, elem type, index")
	}
	if in.Args[1].Kind != vir.OperandType {
		return fmt.Errorf("index.ptr's second operand must be a type")
	}
	esz, err := s.ix.layout.Size(in.Args[1].Type)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], vir.Ptr); err != nil {
		return err
	}
	if err := s.value(RegB, in.Args[2], vir.I64); err != nil {
		return err
	}
	switch {
	case esz == 0:
	case esz&(esz-1) == 0:
		// A power-of-two stride folds into the shifted-register add.
		s.emit(Inst{Op: "add", D: R(RegA), N: R(RegA), M: RShift(RegB, encoder.LSL, byte(bits.TrailingZeros32(esz)))})
	default:
		s.movImm(RegC, uint64(esz))
		s.emit(Inst{Op: "madd", D: R(RegA), N: R(RegB), M: R(RegC), A: R(RegA)})
	}
	s.store(in.Result, RegA)
	return nil
}

// selBulk lowers the three bulk operations as byte loops. There is no A64
// string-move instruction to borrow the way x86 borrows rep movsb, and
// calling into libc would put a runtime under a no-runtime IR.
func (s *sel) selBulk(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpMemset:
		if err := s.value(RegAddr, in.Args[0], vir.Ptr); err != nil {
			return err
		}
		if err := s.value(RegB, in.Args[1], vir.I8); err != nil {
			return err
		}
		if err := s.value(RegC, in.Args[2], vir.I64); err != nil {
			return err
		}
		top, done := s.label(), s.label()
		s.mark(top)
		s.emit(Inst{Op: "cbz", D: R(RegC), Lbl: done})
		s.emit(Inst{Op: "strb", W: encoder.W, D: R(RegB), M: MemPost(RegAddr, 1)})
		s.emit(Inst{Op: "sub", D: R(RegC), N: R(RegC), M: Imm(1)})
		s.emit(Inst{Op: "b", Lbl: top})
		s.mark(done)
		return nil

	case vir.OpMemcopy, vir.OpMemmove:
		if err := s.value(RegAddr, in.Args[0], vir.Ptr); err != nil {
			return err
		}
		if err := s.value(RegAux, in.Args[1], vir.Ptr); err != nil {
			return err
		}
		if err := s.value(RegC, in.Args[2], vir.I64); err != nil {
			return err
		}
		if in.Op == vir.OpMemcopy {
			// Overlap is UB, so a single forward pass is the whole contract.
			s.copyLoop(false)
			return nil
		}
		// memmove picks direction at runtime: descending when the
		// destination sits above the source inside the copied range.
		back, done := s.label(), s.label()
		s.emit(Inst{Op: "cmp", N: R(RegAddr), M: R(RegAux)})
		s.emit(Inst{Op: "b.cond", CC: encoder.HI, Lbl: back})
		s.copyLoop(false)
		s.emit(Inst{Op: "b", Lbl: done})
		s.mark(back)
		s.emit(Inst{Op: "add", D: R(RegAddr), N: R(RegAddr), M: R(RegC)})
		s.emit(Inst{Op: "add", D: R(RegAux), N: R(RegAux), M: R(RegC)})
		s.copyLoop(true)
		s.mark(done)
		return nil
	}
	return fmt.Errorf("unhandled bulk op %s", in.Op)
}

// copyLoop moves RegC bytes between RegAux (source) and RegAddr
// (destination), ascending or descending.
func (s *sel) copyLoop(down bool) {
	top, done := s.label(), s.label()
	s.mark(top)
	s.emit(Inst{Op: "cbz", D: R(RegC), Lbl: done})
	if down {
		s.emit(Inst{Op: "sub", D: R(RegAddr), N: R(RegAddr), M: Imm(1)})
		s.emit(Inst{Op: "sub", D: R(RegAux), N: R(RegAux), M: Imm(1)})
		s.emit(Inst{Op: "ldrb", W: encoder.W, D: R(RegA), M: Mem(RegAux, 0)})
		s.emit(Inst{Op: "strb", W: encoder.W, D: R(RegA), M: Mem(RegAddr, 0)})
	} else {
		s.emit(Inst{Op: "ldrb", W: encoder.W, D: R(RegA), M: MemPost(RegAux, 1)})
		s.emit(Inst{Op: "strb", W: encoder.W, D: R(RegA), M: MemPost(RegAddr, 1)})
	}
	s.emit(Inst{Op: "sub", D: R(RegC), N: R(RegC), M: Imm(1)})
	s.emit(Inst{Op: "b", Lbl: top})
	s.mark(done)
}

// ---------------------------------------------------------------------------
// Conversions.
// ---------------------------------------------------------------------------

func (s *sel) selTrunc(in *vir.Instruction) error {
	b, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], nil); err != nil {
		return err
	}
	s.maskTo(RegA, b)
	s.store(in.Result, RegA)
	return nil
}

// selZext is a move: the source already occupies its slot zero-extended, so
// widening is exactly the invariant already in force.
func (s *sel) selZext(in *vir.Instruction) error {
	if err := s.value(RegA, in.Args[0], nil); err != nil {
		return err
	}
	s.store(in.Result, RegA)
	return nil
}

func (s *sel) selSext(in *vir.Instruction) error {
	dst, err := s.bitsOf(in.Suffix)
	if err != nil {
		return err
	}
	src, err := s.bitsOf(s.typeOfOperand(in.Args[0], in.Suffix))
	if err != nil {
		return err
	}
	if err := s.value(RegA, in.Args[0], nil); err != nil {
		return err
	}
	s.sextTo(RegA, src)
	s.maskTo(RegA, dst)
	s.store(in.Result, RegA)
	return nil
}

// selBitcast between ptr and i64 is a move; the widths must already agree
// (§4.1), which vir.Verify has checked.
func (s *sel) selBitcast(in *vir.Instruction) error {
	if vir.IsFloat(vir.ElemOrSelf(in.Suffix)) {
		return todo("bitcast to %s", in.Suffix)
	}
	if err := s.value(RegA, in.Args[0], nil); err != nil {
		return err
	}
	s.store(in.Result, RegA)
	return nil
}