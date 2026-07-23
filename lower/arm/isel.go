// isel.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	isaarm "github.com/vertex-language/vvm/isa/arm"
)

// fnLower is the per-function selection state.
type fnLower struct {
	x     *index
	f     *vir.Function
	frame *Frame
	types map[string]vir.Type
	out   []Inst
	nlbl  int
}

func (x *index) lowerFunc(f *vir.Function) (Func, error) {
	types, order, err := x.typeFunc(f)
	if err != nil {
		return Func{}, err
	}
	frame, err := BuildFrame(x.layout, f, order, types)
	if err != nil {
		return Func{}, err
	}
	c := &fnLower{x: x, f: f, frame: frame, types: types}

	for i, b := range f.AllBlocks() {
		if i > 0 {
			c.emit(Inst{Op: "label", Lbl: b.Label})
		}
		for _, in := range b.Lines {
			if err := c.instruction(in); err != nil {
				return Func{}, fmt.Errorf("%s: %w", in.Op, err)
			}
		}
		if err := c.terminator(b.Term); err != nil {
			return Func{}, err
		}
	}

	code, fixups, err := c.assemble()
	if err != nil {
		return Func{}, err
	}
	sym, _ := x.symbol(f.Name)
	return Func{Name: sym, Code: code, Align: 4, Export: f.Export, Fixups: fixups}, nil
}

// ---------------------------------------------------------------------------
// Emission helpers.
// ---------------------------------------------------------------------------

func (c *fnLower) emit(in Inst) { c.out = append(c.out, in) }

func (c *fnLower) dp(op string, d, n, m Opr) { c.emit(Inst{Op: op, D: d, N: n, M: m}) }

func (c *fnLower) mov(d Reg, m Opr) { c.emit(Inst{Op: "mov", D: R(d), M: m}) }

func (c *fnLower) movCC(cc Cond, d Reg, m Opr) {
	c.emit(Inst{Op: "mov", CC: cc, D: R(d), M: m})
}

func (c *fnLower) cmp(a Reg, m Opr) { c.emit(Inst{Op: "cmp", N: R(a), M: m}) }

func (c *fnLower) br(cc Cond, lbl string) { c.emit(Inst{Op: "b", CC: cc, Lbl: lbl}) }

func (c *fnLower) mark(lbl string) { c.emit(Inst{Op: "label", Lbl: lbl}) }

// newLabel mints an internal label. The '.' prefix cannot collide with an
// IR block label, which is a bare identifier (§3).
func (c *fnLower) newLabel() string {
	c.nlbl++
	return fmt.Sprintf(".L%d", c.nlbl)
}

// movImm materializes an arbitrary 32-bit constant. A modified immediate
// covers most of them in one instruction; its complement covers most of
// the rest via mvn; anything else needs the MOVW/MOVT pair, which is why
// this backend's baseline is ARMv6T2 and not ARMv4.
func (c *fnLower) movImm(d Reg, v int64) {
	u := uint32(v)
	switch {
	case isaarm.FitsModImm(u):
		c.mov(d, Imm(int64(u)))
	case isaarm.FitsModImm(^u):
		c.emit(Inst{Op: "mvn", D: R(d), M: Imm(int64(^u))})
	default:
		c.emit(Inst{Op: "movw", D: R(d), Imm: int64(u & 0xFFFF)})
		if u>>16 != 0 {
			c.emit(Inst{Op: "movt", D: R(d), Imm: int64(u >> 16)})
		}
	}
}

// symAddr materializes a symbol's absolute address. Both halves carry
// relocations; the object writer fills them in.
func (c *fnLower) symAddr(d Reg, sym string) {
	c.emit(Inst{Op: "movw", D: R(d), M: SymAddr(sym)})
	c.emit(Inst{Op: "movt", D: R(d), M: SymAddr(sym)})
}

// addImm computes d = n + v, falling back to a scratch register when the
// constant is not a modified immediate. scratch must not be n or d.
func (c *fnLower) addImm(d, n Reg, v int32, scratch Reg) {
	switch {
	case v == 0 && d == n:
	case v >= 0 && isaarm.FitsModImm(uint32(v)):
		c.dp("add", R(d), R(n), Imm(int64(v)))
	case v < 0 && isaarm.FitsModImm(uint32(-v)):
		c.dp("sub", R(d), R(n), Imm(int64(-v)))
	default:
		c.movImm(scratch, int64(v))
		c.dp("add", R(d), R(n), R(scratch))
	}
}

func (c *fnLower) loadSlot(d Reg, name string) { c.emit(Inst{Op: "ldr", D: R(d), M: Slot(name)}) }
func (c *fnLower) storeSlot(s Reg, name string) {
	if name == "" {
		return
	}
	c.emit(Inst{Op: "str", D: R(s), M: Slot(name)})
}

// ---------------------------------------------------------------------------
// The zero-extension invariant.
// ---------------------------------------------------------------------------
//
// A value of type iN always occupies a full 4-byte slot with the upper
// 32-N bits zero. Producers restore that with maskTo after anything that
// could carry into the upper bits; only signed consumers (sdiv, srem,
// ashr, signed compares, sext) sign-extend, and always into a scratch copy
// that is never written back.

func (c *fnLower) maskTo(r Reg, bits int) {
	switch bits {
	case 0, 32:
	case 1:
		c.dp("and", R(r), R(r), Imm(1))
	case 8:
		c.dp("and", R(r), R(r), Imm(0xFF))
	case 16:
		// 0xFFFF is not a modified immediate, but its complement's two
		// halves each are, so two bics beat an lsl/lsr pair on flags.
		c.dp("bic", R(r), R(r), Imm(0xFF000000))
		c.dp("bic", R(r), R(r), Imm(0x00FF0000))
	default:
		panic("maskTo: width outside the checkValueType set")
	}
}

func (c *fnLower) sext32(r Reg, bits int) {
	if bits >= 32 || bits == 0 {
		return
	}
	n := byte(32 - bits)
	c.mov(r, RShift(r, LSL, n))
	c.mov(r, RShift(r, ASR, n))
}

// ---------------------------------------------------------------------------
// Operands.
// ---------------------------------------------------------------------------

func intWidth(t vir.Type) (int, bool) {
	switch x := t.(type) {
	case vir.IntType:
		return x.Bits, true
	case vir.PtrType, vir.ValistType:
		return 32, true
	}
	return 0, false
}

// width is the bit width an instruction's type suffix names.
func (c *fnLower) width(in *vir.Instruction) (int, error) {
	if in.Suffix == nil {
		return 0, fmt.Errorf("instruction has no type suffix")
	}
	if err := checkValueType(in.Suffix); err != nil {
		return 0, err
	}
	w, ok := intWidth(in.Suffix)
	if !ok {
		return 0, todo("%s operands", in.Suffix)
	}
	return w, nil
}

// operandWidth is the fixed width of a value operand, for conversions that
// need the *source* type rather than the suffix.
func (c *fnLower) operandWidth(o vir.Operand) int {
	if o.Kind == vir.OperandIdent {
		if t, ok := c.types[o.Ident]; ok {
			if w, ok := intWidth(t); ok {
				return w
			}
		}
	}
	return 32 // literals are already the value they denote
}

// into materializes an operand into register r.
func (c *fnLower) into(o vir.Operand, r Reg) error {
	switch o.Kind {
	case vir.OperandInt:
		c.movImm(r, o.Int)
		return nil
	case vir.OperandBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		c.movImm(r, v)
		return nil
	case vir.OperandNull:
		c.movImm(r, 0)
		return nil
	case vir.OperandFloat:
		return todo("float literals")
	case vir.OperandIdent:
		if o.Qualifier != "" {
			return fmt.Errorf("unrewritten cross-module reference %s.%s", o.Qualifier, o.Ident)
		}
		if _, ok := c.types[o.Ident]; ok {
			c.loadSlot(r, o.Ident)
			return nil
		}
		if k, ok := c.x.consts[o.Ident]; ok {
			return c.into(k.Value, r) // a const is a compile-time scalar (§6.2)
		}
		if g, ok := c.x.globals[o.Ident]; ok {
			if g.TLS {
				return todo("thread-local global %s (needs __aeabi_read_tp)", g.Name)
			}
			sym, _ := c.x.symbol(o.Ident)
			c.symAddr(r, sym)
			return nil
		}
		if sym, ok := c.x.symbol(o.Ident); ok {
			c.symAddr(r, sym) // a function's address
			return nil
		}
		return fmt.Errorf("unknown identifier %q", o.Ident)
	}
	return fmt.Errorf("operand %s is not a value", o)
}

// ---------------------------------------------------------------------------
// Instruction selection.
// ---------------------------------------------------------------------------

func (c *fnLower) instruction(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpLoc:
		return nil // no debug-info emission yet; a loc line lowers to nothing
	case vir.OpPrefetch:
		// A hint (§4). isa/arm's encoder has no PLD, and dropping a
		// prefetch changes no observable behaviour, so it lowers to
		// nothing rather than to a todo.
		return nil

	case vir.OpAdd, vir.OpSub, vir.OpMul, vir.OpAnd, vir.OpOr, vir.OpXor:
		return c.selBinary(in)
	case vir.OpNeg, vir.OpNot, vir.OpAbs:
		return c.selUnary(in)
	case vir.OpShl, vir.OpLShr, vir.OpAShr:
		return c.selShift(in)
	case vir.OpRotl, vir.OpRotr:
		return c.selRotate(in)
	case vir.OpUDiv, vir.OpSDiv, vir.OpURem, vir.OpSRem:
		return c.selDivide(in)
	case vir.OpUMulH, vir.OpSMulH:
		return c.selMulHigh(in)
	case vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO, vir.OpUMulO, vir.OpSMulO:
		return c.selOverflow(in)
	case vir.OpCtlz, vir.OpCttz:
		return c.selCountBits(in)
	case vir.OpBSwap:
		return c.selBSwap(in)
	case vir.OpSMin, vir.OpSMax, vir.OpUMin, vir.OpUMax:
		return c.selMinMax(in)
	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge:
		return c.selCompare(in)
	case vir.OpSelect:
		return c.selSelect(in)

	case vir.OpAlloca:
		return c.selAlloca(in)
	case vir.OpLoad, vir.OpLoadVol:
		return c.selLoad(in)
	case vir.OpStore, vir.OpStoreVol:
		return c.selStore(in)
	case vir.OpField:
		return c.selField(in)
	case vir.OpIndex:
		return c.selIndex(in)
	case vir.OpMemcopy, vir.OpMemmove, vir.OpMemset:
		return c.selBulk(in)

	case vir.OpTrunc, vir.OpZext, vir.OpSext, vir.OpBitcast:
		return c.selConvert(in)

	case vir.OpCall:
		return c.selCall(in)
	case vir.OpSyscall:
		return c.selSyscall(in)
	case vir.OpVaStart, vir.OpVaArg, vir.OpVaEnd:
		return c.selVararg(in)

	case vir.OpAtomicLoad, vir.OpAtomicStore, vir.OpAtomicAdd, vir.OpAtomicSub,
		vir.OpAtomicAnd, vir.OpAtomicOr, vir.OpAtomicXor, vir.OpAtomicXchg,
		vir.OpCmpxchg, vir.OpFence:
		// Every one of these needs LDREX/STREX/DMB, none of which is in
		// isa/arm/encoder's instruction switch. See the README.
		return todo("%s (encoder has no ldrex/strex/dmb)", in.Op)
	}
	return todo("%s", in.Op)
}

func (c *fnLower) selBinary(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	switch in.Op {
	case vir.OpAdd:
		c.dp("add", R(R0), R(R0), R(R1))
	case vir.OpSub:
		c.dp("sub", R(R0), R(R0), R(R1))
	case vir.OpAnd:
		c.dp("and", R(R0), R(R0), R(R1))
	case vir.OpOr:
		c.dp("orr", R(R0), R(R0), R(R1))
	case vir.OpXor:
		c.dp("eor", R(R0), R(R0), R(R1))
	case vir.OpMul:
		// Rd must differ from Rm on pre-ARMv6 MUL, so the product lands
		// in a third register rather than in place.
		c.emit(Inst{Op: "mul", D: R(R2), M: R(R0), A: R(R1)})
		c.mov(R0, R(R2))
	}
	c.maskTo(R0, w)
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selUnary(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	switch in.Op {
	case vir.OpNeg:
		c.dp("rsb", R(R0), R(R0), Imm(0))
	case vir.OpNot:
		c.emit(Inst{Op: "mvn", D: R(R0), M: R(R0)})
	case vir.OpAbs:
		// abs is signed; abs(INT_MIN) wraps to INT_MIN, which is what
		// rsb gives for free (§4.1: integers wrap, no UB).
		c.sext32(R0, w)
		c.cmp(R0, Imm(0))
		c.emit(Inst{Op: "rsb", CC: LT, D: R(R0), N: R(R0), M: Imm(0)})
	}
	c.maskTo(R0, w)
	c.storeSlot(R0, in.Result)
	return nil
}

// selShift implements §4.1's "shift counts mask to operand bit width".
// ARM's register-specified shift uses the low byte of Rs and produces zero
// for counts of 32 or more, which is *not* the same rule, so the count is
// masked explicitly first.
func (c *fnLower) selShift(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	c.dp("and", R(R1), R(R1), Imm(int64(w-1)))
	switch in.Op {
	case vir.OpShl:
		c.mov(R0, RShiftReg(R0, LSL, R1))
	case vir.OpLShr:
		c.mov(R0, RShiftReg(R0, LSR, R1)) // slot is zero-extended already
	case vir.OpAShr:
		c.sext32(R0, w)
		c.mov(R0, RShiftReg(R0, ASR, R1))
	}
	c.maskTo(R0, w)
	c.storeSlot(R0, in.Result)
	return nil
}

// selRotate synthesizes rotl/rotr. A32 has only ROR, and only at the full
// 32-bit width, so a narrower rotate is built from a shift pair.
func (c *fnLower) selRotate(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	c.dp("and", R(R1), R(R1), Imm(int64(w-1)))
	if w == 32 {
		if in.Op == vir.OpRotl {
			// ror by (32-k). k == 0 gives ror #32, which the machine
			// treats as ror #0 — exactly the identity rotl by 0 wants.
			c.dp("rsb", R(R1), R(R1), Imm(32))
		}
		c.mov(R0, RShiftReg(R0, ROR, R1))
		c.storeSlot(R0, in.Result)
		return nil
	}
	// (x << k | x >> (w-k)) & mask, with the operand roles swapped for
	// rotr. k == 0 makes the second term x >> w, which is zero because the
	// slot is zero-extended.
	left, right := LSL, LSR
	if in.Op == vir.OpRotr {
		left, right = LSR, LSL
	}
	c.dp("rsb", R(R2), R(R1), Imm(int64(w)))
	c.mov(R3, RShiftReg(R0, left, R1))
	c.mov(R0, RShiftReg(R0, right, R2))
	c.dp("orr", R(R0), R(R0), R(R3))
	c.maskTo(R0, w)
	c.storeSlot(R0, in.Result)
	return nil
}

// selMulHigh computes the high half of a double-width product.
func (c *fnLower) selMulHigh(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSMulH
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	if w == 32 {
		op := "umull"
		if signed {
			op = "smull"
		}
		// RdLo = R2, RdHi = R3; the high half is the result.
		c.emit(Inst{Op: op, D: R(R2), N: R(R3), M: R(R0), A: R(R1)})
		c.storeSlot(R3, in.Result)
		return nil
	}
	// Below 32 bits the whole 2N-bit product fits in one register.
	if signed {
		c.sext32(R0, w)
		c.sext32(R1, w)
	}
	c.emit(Inst{Op: "mul", D: R(R2), M: R(R0), A: R(R1)})
	sh := LSR
	if signed {
		sh = ASR
	}
	c.mov(R0, RShift(R2, sh, byte(w)))
	c.maskTo(R0, w)
	c.storeSlot(R0, in.Result)
	return nil
}

// selOverflow lowers the uaddo/saddo/... predicates. At 32 bits the flags
// answer directly; below it, the uniform rule is that the operation
// overflowed iff the 32-bit result differs from its own truncation back to
// N bits and re-extension.
func (c *fnLower) selOverflow(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSAddO || in.Op == vir.OpSSubO || in.Op == vir.OpSMulO
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	if w == 32 {
		var cc Cond
		switch in.Op {
		case vir.OpUAddO:
			c.emit(Inst{Op: "add", S: true, D: R(R0), N: R(R0), M: R(R1)})
			cc = HS // carry out
		case vir.OpUSubO:
			c.emit(Inst{Op: "sub", S: true, D: R(R0), N: R(R0), M: R(R1)})
			cc = LO // borrow
		case vir.OpSAddO:
			c.emit(Inst{Op: "add", S: true, D: R(R0), N: R(R0), M: R(R1)})
			cc = VS
		case vir.OpSSubO:
			c.emit(Inst{Op: "sub", S: true, D: R(R0), N: R(R0), M: R(R1)})
			cc = VS
		case vir.OpUMulO:
			c.emit(Inst{Op: "umull", D: R(R2), N: R(R3), M: R(R0), A: R(R1)})
			c.cmp(R3, Imm(0))
			cc = NE
		case vir.OpSMulO:
			c.emit(Inst{Op: "smull", D: R(R2), N: R(R3), M: R(R0), A: R(R1)})
			c.cmp(R3, RShift(R2, ASR, 31)) // high half must be the sign
			cc = NE
		}
		c.mov(R0, Imm(0))
		c.movCC(cc, R0, Imm(1))
		c.storeSlot(R0, in.Result)
		return nil
	}

	if signed {
		c.sext32(R0, w)
		c.sext32(R1, w)
	}
	switch in.Op {
	case vir.OpUAddO, vir.OpSAddO:
		c.dp("add", R(R0), R(R0), R(R1))
	case vir.OpUSubO, vir.OpSSubO:
		c.dp("sub", R(R0), R(R0), R(R1))
	case vir.OpUMulO, vir.OpSMulO:
		c.emit(Inst{Op: "mul", D: R(R2), M: R(R0), A: R(R1)})
		c.mov(R0, R(R2))
	}
	c.mov(R3, R(R0))
	c.maskTo(R3, w)
	if signed {
		c.sext32(R3, w)
	}
	c.cmp(R0, R(R3))
	c.mov(R0, Imm(0))
	c.movCC(NE, R0, Imm(1))
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selCountBits(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if in.Op == vir.OpCtlz {
		// The slot is zero-extended, so clz32 counts 32-w extra leading
		// zeros — including for the zero input, where the subtraction
		// lands exactly on w without a special case.
		c.emit(Inst{Op: "clz", D: R(R0), M: R(R0)})
		if w < 32 {
			c.dp("sub", R(R0), R(R0), Imm(int64(32-w)))
		}
		c.storeSlot(R0, in.Result)
		return nil
	}
	// cttz: isolate the lowest set bit, then 31 - clz. rbit is not in the
	// encoder, and the zero input needs its own answer.
	c.dp("rsb", R(R2), R(R0), Imm(0))
	c.dp("and", R(R2), R(R2), R(R0))
	c.emit(Inst{Op: "clz", D: R(R2), M: R(R2)})
	c.dp("rsb", R(R2), R(R2), Imm(31))
	c.cmp(R0, Imm(0)) // the intervening ops set no flags
	c.mov(R0, R(R2))
	c.movCC(EQ, R0, Imm(int64(w)))
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selBSwap(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	switch w {
	case 8:
		return fmt.Errorf("bswap.i8 is not legal (§9.20)")
	case 16:
		c.mov(R1, RShift(R0, LSR, 8))
		c.mov(R0, RShift(R0, LSL, 8))
		c.dp("orr", R(R0), R(R0), R(R1))
		c.maskTo(R0, 16)
	case 32:
		// The standard eor/bic/ror sequence; rev is not in the encoder.
		c.dp("eor", R(R1), R(R0), RShift(R0, ROR, 16))
		c.dp("bic", R(R1), R(R1), Imm(0x00FF0000))
		c.mov(R0, RShift(R0, ROR, 8))
		c.dp("eor", R(R0), R(R0), RShift(R1, LSR, 8))
	default:
		return todo("bswap.i%d", w)
	}
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selMinMax(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	signed := in.Op == vir.OpSMin || in.Op == vir.OpSMax
	if signed {
		c.mov(R2, R(R0))
		c.mov(R3, R(R1))
		c.sext32(R2, w)
		c.sext32(R3, w)
		c.cmp(R2, R(R3))
	} else {
		c.cmp(R0, R(R1))
	}
	var take Cond
	switch in.Op {
	case vir.OpSMin:
		take = GT
	case vir.OpSMax:
		take = LT
	case vir.OpUMin:
		take = HI
	case vir.OpUMax:
		take = LO
	}
	c.movCC(take, R0, R(R1))
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selCompare(in *vir.Instruction) error {
	if in.Suffix != nil && vir.IsFloat(vir.ElemOrSelf(in.Suffix)) {
		return todo("float comparisons")
	}
	w, err := c.width(in)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	var cc Cond
	signed := false
	switch in.Op {
	case vir.OpEq:
		cc = EQ
	case vir.OpNe:
		cc = NE
	case vir.OpUlt:
		cc = LO
	case vir.OpUgt:
		cc = HI
	case vir.OpUle:
		cc = LS
	case vir.OpUge:
		cc = HS
	case vir.OpSlt:
		cc, signed = LT, true
	case vir.OpSgt:
		cc, signed = GT, true
	case vir.OpSle:
		cc, signed = LE, true
	case vir.OpSge:
		cc, signed = GE, true
	}
	if signed {
		c.sext32(R0, w)
		c.sext32(R1, w)
	}
	c.cmp(R0, R(R1))
	c.mov(R0, Imm(0))
	c.movCC(cc, R0, Imm(1))
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selSelect(in *vir.Instruction) error {
	if _, err := c.width(in); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R2); err != nil {
		return err
	}
	if err := c.into(in.Args[2], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[0], R1); err != nil {
		return err
	}
	c.cmp(R1, Imm(0))
	c.movCC(NE, R0, R(R2))
	c.storeSlot(R0, in.Result)
	return nil
}

// ---------------------------------------------------------------------------
// Memory.
// ---------------------------------------------------------------------------

func (c *fnLower) selAlloca(in *vir.Instruction) error {
	if vir.IsValist(in.Suffix) {
		// alloca.valist takes no size operand: the cursor's layout is the
		// backend's business (§5.1). Ours is one pointer, rounded to the
		// stack's 8-byte granule.
		c.dp("sub", R(SP), R(SP), Imm(StackAlign))
		c.mov(R0, R(SP))
		c.storeSlot(R0, in.Result)
		return nil
	}
	align := uint32(StackAlign)
	if in.Align > 0 && uint32(in.Align) > align {
		align = uint32(in.Align)
	}
	if lit := in.Args[0]; lit.Kind == vir.OperandInt {
		n := roundUp(uint32(lit.Int), StackAlign)
		if isaarm.FitsModImm(n) {
			c.dp("sub", R(SP), R(SP), Imm(int64(n)))
		} else {
			c.movImm(IP, int64(n))
			c.dp("sub", R(SP), R(SP), R(IP))
		}
	} else {
		if err := c.into(in.Args[0], R0); err != nil {
			return err
		}
		c.dp("add", R(R0), R(R0), Imm(StackAlign-1))
		c.dp("bic", R(R0), R(R0), Imm(StackAlign-1))
		c.dp("sub", R(SP), R(SP), R(R0))
	}
	if align > StackAlign {
		c.dp("bic", R(SP), R(SP), Imm(int64(align-1)))
	}
	c.mov(R0, R(SP))
	c.storeSlot(R0, in.Result)
	return nil
}

// memOp picks the transfer mnemonic for a width. A narrow load
// zero-extends in hardware, which is exactly the slot invariant.
func memOp(load bool, bits int) (string, error) {
	switch bits {
	case 1, 8:
		if load {
			return "ldrb", nil
		}
		return "strb", nil
	case 16:
		if load {
			return "ldrh", nil
		}
		return "strh", nil
	case 32:
		if load {
			return "ldr", nil
		}
		return "str", nil
	}
	return "", todo("%d-bit memory access", bits)
}

func (c *fnLower) selLoad(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	op, err := memOp(true, w)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R1); err != nil {
		return err
	}
	// A volatile access lowers to the same instruction; this backend never
	// elides, duplicates or reorders memory operations, so the §5.1
	// guarantees hold without a distinct encoding.
	c.emit(Inst{Op: op, D: R(R0), M: Mem(R1, 0)})
	if w == 1 {
		c.maskTo(R0, 1)
	}
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selStore(in *vir.Instruction) error {
	w, err := c.width(in)
	if err != nil {
		return err
	}
	op, err := memOp(false, w)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R1); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R0); err != nil {
		return err
	}
	c.emit(Inst{Op: op, D: R(R0), M: Mem(R1, 0)})
	return nil
}

func (c *fnLower) selField(in *vir.Instruction) error {
	if len(in.Args) != 3 || in.Args[1].Kind != vir.OperandIdent || in.Args[2].Kind != vir.OperandIdent {
		return fmt.Errorf("field.ptr needs a pointer, a struct name and a field name")
	}
	off, _, err := c.x.layout.FieldOffset(in.Args[1].Ident, in.Args[2].Ident)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	c.addImm(R0, R0, int32(off), IP)
	c.storeSlot(R0, in.Result)
	return nil
}

func (c *fnLower) selIndex(in *vir.Instruction) error {
	if len(in.Args) != 3 || in.Args[1].Kind != vir.OperandType {
		return fmt.Errorf("index.ptr needs a pointer, an element type and an index")
	}
	size, err := c.x.layout.Size(in.Args[1].Type)
	if err != nil {
		return err
	}
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[2], R1); err != nil {
		return err
	}
	if sh, ok := log2(size); ok {
		// Address arithmetic wraps normally (§5.2), so the shifted-operand
		// form needs no overflow handling.
		c.dp("add", R(R0), R(R0), RShift(R1, LSL, sh))
	} else {
		c.movImm(R2, int64(size))
		c.emit(Inst{Op: "mul", D: R(R3), M: R(R1), A: R(R2)})
		c.dp("add", R(R0), R(R0), R(R3))
	}
	c.storeSlot(R0, in.Result)
	return nil
}

func log2(v uint32) (byte, bool) {
	if v == 0 || v&(v-1) != 0 || v > 1<<31 {
		return 0, false
	}
	var n byte
	for v > 1 {
		v >>= 1
		n++
	}
	return n, true
}

// selBulk lowers memcopy/memmove/memset as byte loops. A32 has no rep
// prefix, and a word-at-a-time loop would need an alignment and residue
// path this backend has no reason to carry yet.
func (c *fnLower) selBulk(in *vir.Instruction) error {
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	if err := c.into(in.Args[1], R1); err != nil {
		return err
	}
	if err := c.into(in.Args[2], R2); err != nil {
		return err
	}
	done := c.newLabel()
	c.cmp(R2, Imm(0))
	c.br(EQ, done)

	switch in.Op {
	case vir.OpMemset:
		loop := c.newLabel()
		c.mark(loop)
		c.emit(Inst{Op: "strb", D: R(R1), M: MemPost(R0, 1)})
		c.emit(Inst{Op: "sub", S: true, D: R(R2), N: R(R2), M: Imm(1)})
		c.br(NE, loop)

	case vir.OpMemcopy:
		// Overlap is UB (§5.4 #4), so a forward loop is unconditional.
		loop := c.newLabel()
		c.mark(loop)
		c.emit(Inst{Op: "ldrb", D: R(R3), M: MemPost(R1, 1)})
		c.emit(Inst{Op: "strb", D: R(R3), M: MemPost(R0, 1)})
		c.emit(Inst{Op: "sub", S: true, D: R(R2), N: R(R2), M: Imm(1)})
		c.br(NE, loop)

	case vir.OpMemmove:
		// Direction is picked at runtime: copying up when the destination
		// is below the source, down otherwise.
		back := c.newLabel()
		fwd := c.newLabel()
		c.cmp(R0, R(R1))
		c.br(HI, back)
		c.mark(fwd)
		c.emit(Inst{Op: "ldrb", D: R(R3), M: MemPost(R1, 1)})
		c.emit(Inst{Op: "strb", D: R(R3), M: MemPost(R0, 1)})
		c.emit(Inst{Op: "sub", S: true, D: R(R2), N: R(R2), M: Imm(1)})
		c.br(NE, fwd)
		c.br(AL, done)
		c.mark(back)
		c.dp("add", R(R0), R(R0), R(R2))
		c.dp("add", R(R1), R(R1), R(R2))
		loop := c.newLabel()
		c.mark(loop)
		c.emit(Inst{Op: "ldrb", D: R(R3), M: MemPre(R1, -1, true)})
		c.emit(Inst{Op: "strb", D: R(R3), M: MemPre(R0, -1, true)})
		c.emit(Inst{Op: "sub", S: true, D: R(R2), N: R(R2), M: Imm(1)})
		c.br(NE, loop)
	}
	c.mark(done)
	return nil
}

// ---------------------------------------------------------------------------
// Conversions.
// ---------------------------------------------------------------------------

func (c *fnLower) selConvert(in *vir.Instruction) error {
	dst, err := c.width(in)
	if err != nil {
		return err
	}
	src := c.operandWidth(in.Args[0])
	if err := c.into(in.Args[0], R0); err != nil {
		return err
	}
	switch in.Op {
	case vir.OpTrunc:
		c.maskTo(R0, dst)
	case vir.OpZext:
		c.maskTo(R0, dst) // the source slot is already zero-extended
	case vir.OpSext:
		c.sext32(R0, src)
		c.maskTo(R0, dst)
	case vir.OpBitcast:
		// ptr <-> i32 only; usize must match exactly (§4.1), which on a
		// 32-bit target means the value is already in the right register.
		if src != 32 || dst != 32 {
			return fmt.Errorf("bitcast requires an exact usize match on a 32-bit target")
		}
	}
	c.storeSlot(R0, in.Result)
	return nil
}