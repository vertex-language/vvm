// isel.go
package x86

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	isax86 "github.com/vertex-language/vvm/isa/x86"
)

// Scratch registers. Every named IR value lives in a stack slot and
// nothing survives in a register across an Inst boundary, so all six of
// these are free within one instruction's lowering — the three
// callee-saved ones included, since the prologue already preserved them
// for our own caller.
const (
	rA  = isax86.REAX
	rC  = isax86.RECX
	rD  = isax86.REDX
	rB  = isax86.REBX
	rSI = isax86.RESI
	rDI = isax86.REDI
	rSP = isax86.RESP
	rBP = isax86.REBP
)

// todo builds the error every unimplemented lowering returns. The suffix
// is load-bearing: it's how a caller tells "this backend can't do that
// yet" apart from "your module is wrong".
func todo(format string, args ...any) error {
	return fmt.Errorf(format+" (TODO)", args...)
}

// fnLower is the per-function selection state.
type fnLower struct {
	lw *lowerer
	fn *vir.Function

	// types maps every named value to its fixed type (§4.3's type
	// fixation rule), computed once by typeFunc before selection starts.
	// Selection needs it constantly: the IR annotates an instruction's
	// result type via Suffix, but says nothing about its *operands'*
	// types, and trunc/sext/zext/comparisons all need the source width.
	types map[string]vir.Type

	// paramEnd/argBytes describe the incoming argument area. They come
	// from LayoutArgs, the same routine BuildFrame will use afterwards —
	// selection needs them before a Frame exists (va_start and tailcall
	// both do), and recomputing from the same function is safe in a way
	// that reimplementing the formula is not.
	paramEnd map[string]int32
	argBytes int32

	out  []Inst
	nlbl int
}

func (fl *fnLower) emit(in ...Inst) { fl.out = append(fl.out, in...) }

// label mints an internal branch target. The '.' cannot appear in a vir
// identifier, so these can never collide with a block label from the IR.
func (fl *fnLower) label(tag string) string {
	fl.nlbl++
	return fmt.Sprintf(".%s%d", tag, fl.nlbl)
}

// lookupSig resolves a fnsig by name, for indirect calls and tailcalls.
// Linear because indirect calls are rare and the alternative is another
// index built for every module whether or not it has one.
func (lw *lowerer) lookupSig(name string) (*vir.FunctionSignature, bool) {
	for _, s := range lw.m.FunctionSignatures {
		if s.Name == name {
			return s, true
		}
	}
	return nil, false
}

// hasTier reports whether the module's target declares a feature tier.
func (lw *lowerer) hasTier(names ...string) bool {
	if lw.m.Target == nil {
		return false
	}
	for _, t := range lw.m.Target.Tiers {
		for _, n := range names {
			if t == n {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Entry point.
// ---------------------------------------------------------------------------

// lowerFunc lowers one vir function to machine code.
//
// The symbol name is taken verbatim from the IR. Mangling (§6.3) is
// applied before a module reaches this package, in the same pass that
// erases cross-module references — a backend that re-derived symbol names
// would need to know about namespaces, and knowing about namespaces is
// exactly what importer.Rewrite exists to spare it.
func (lw *lowerer) lowerFunc(f *vir.Function) (Func, error) {
	fl := &fnLower{lw: lw, fn: f, paramEnd: map[string]int32{}}

	if err := fl.typeFunc(); err != nil {
		return Func{}, err
	}

	slots, argBytes, err := LayoutArgs(f.Params, len(f.Params), lw.lay.ByValSize)
	if err != nil {
		return Func{}, err
	}
	fl.argBytes = int32(argBytes)
	for i, p := range f.Params {
		fl.paramEnd[p.Name] = ParamBase + int32(slots[i].Offset+slots[i].Size)
	}

	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(Inst{Op: "label", Lbl: b.Label})
		}
		for _, in := range b.Lines {
			if err := fl.selInst(in); err != nil {
				return Func{}, fmt.Errorf("%s: %w", in.Op, err)
			}
		}
		if b.Term == nil {
			return Func{}, fmt.Errorf("block %q has no terminator", b.Label)
		}
		if err := fl.selTerm(b.Term); err != nil {
			return Func{}, err
		}
	}

	fr, err := BuildFrame(f, fl.out, lw.lay.ByValSize)
	if err != nil {
		return Func{}, err
	}
	code, fx, err := assemble(fl.out, fr)
	if err != nil {
		return Func{}, err
	}
	return Func{Name: f.Name, Code: code, Align: 16, Export: f.Export, Fixups: fx}, nil
}

// ---------------------------------------------------------------------------
// Types.
// ---------------------------------------------------------------------------

// checkValueType reports whether this backend can hold t in a named value.
//
// The width list is not "iN for N <= 32" but exactly {1, 8, 16, 32}:
// layout.go only assigns a memory size to those widths (plus i64/i128,
// which need register pairs), so an i24 has nowhere to live even in
// memory. Pinning the set to four widths is also what makes rotates, byte
// swaps and widening multiplies expressible with real instructions
// instead of open-coded emulation.
func checkValueType(t vir.Type) error {
	switch x := t.(type) {
	case vir.IntType:
		switch x.Bits {
		case 1, 8, 16, 32:
			return nil
		case 64, 128:
			return todo("i%d values need register pairs on x86", x.Bits)
		}
		return todo("i%d is not a width this backend can size", x.Bits)
	case vir.PtrType, vir.ValistType:
		return nil
	case vir.FloatType:
		return todo("f%d values need an x87/SSE codegen path", x.Bits)
	case vir.VecType:
		return todo("vector values need a vector tier")
	}
	return fmt.Errorf("%s cannot be a named value on x86", t)
}

// intBits is the significant width of a value held in a slot. ptr and
// valist are both 32-bit on this target.
func intBits(t vir.Type) (int, error) {
	switch x := t.(type) {
	case vir.IntType:
		if err := checkValueType(t); err != nil {
			return 0, err
		}
		return x.Bits, nil
	case vir.PtrType, vir.ValistType:
		return 32, nil
	}
	return 0, fmt.Errorf("%s is not an integer-class type", t)
}

// typeFunc computes the fixed type of every named value in the function.
//
// §4.3's type fixation says the first assignment fixes a name's type
// permanently and parameters count as entry assignments, so one pass in
// declaration order is enough — there is no need to iterate to a fixed
// point, and a later assignment disagreeing with the first is a verifier
// error this package is entitled to assume didn't happen.
func (fl *fnLower) typeFunc() error {
	fl.types = make(map[string]vir.Type, len(fl.fn.Params)+8)
	for _, p := range fl.fn.Params {
		if err := checkValueType(p.Type); err != nil {
			return fmt.Errorf("parameter %s: %w", p.Name, err)
		}
		fl.types[p.Name] = p.Type
	}
	for _, b := range fl.fn.AllBlocks() {
		for _, in := range b.Lines {
			if in.Result == "" {
				continue
			}
			if _, fixed := fl.types[in.Result]; fixed {
				continue
			}
			t, err := fl.resultType(in)
			if err != nil {
				return fmt.Errorf("%s = %s: %w", in.Result, in.Op, err)
			}
			if err := checkValueType(t); err != nil {
				return fmt.Errorf("%s = %s: %w", in.Result, in.Op, err)
			}
			fl.types[in.Result] = t
		}
	}
	return nil
}

// resultType derives an instruction's result type. Most opcodes take it
// straight from Suffix; the exceptions are the ones whose result type
// isn't spelled anywhere in the instruction.
func (fl *fnLower) resultType(in *vir.Instruction) (vir.Type, error) {
	switch in.Op {
	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge,
		vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe,
		vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO,
		vir.OpUMulO, vir.OpSMulO:
		// Predicates yield i1 regardless of the operand width their
		// suffix names.
		if v, ok := in.Suffix.(vir.VecType); ok {
			return vir.VecType{Elem: vir.I1, Len: v.Len}, nil
		}
		return vir.I1, nil

	case vir.OpCall:
		if in.Sig != "" {
			sig, ok := fl.lw.lookupSig(in.Sig)
			if !ok {
				return nil, fmt.Errorf("fnsig %q is not declared", in.Sig)
			}
			return sig.Ret, nil
		}
		if len(in.Args) == 0 || in.Args[0].Kind != vir.OperandIdent {
			return nil, fmt.Errorf("call has no callee operand")
		}
		c, ok := fl.lw.lookupCallee(in.Args[0].Ident)
		if !ok {
			return nil, fmt.Errorf("%q is not a declared function", in.Args[0].Ident)
		}
		return c.Ret, nil
	}
	if in.Suffix == nil {
		return nil, fmt.Errorf("instruction has no type suffix")
	}
	return in.Suffix, nil
}

func (fl *fnLower) valueType(name string) (vir.Type, error) {
	t, ok := fl.types[name]
	if !ok {
		return nil, fmt.Errorf("value %q is never assigned", name)
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Operands and the zero-extended slot invariant.
// ---------------------------------------------------------------------------

// The representation invariant, stated once:
//
//	A value of type iN occupies a full 4-byte slot holding the value's N
//	low bits, with the upper 32-N bits ZERO.
//
// The alternative — leaving the upper bits unspecified — would force
// every consumer to normalize its inputs. This way only the signed
// consumers do (sdiv, srem, ashr, signed compares, sext), and every
// operation on an i32 or a ptr, which is the overwhelming majority,
// elides normalization entirely.
//
// Producers restore the invariant with maskTo after any operation that
// can carry into the upper bits. Signed consumers call sext32 first,
// which is destructive — so it always runs on a scratch copy that is
// about to be consumed, never written back to a slot.

// src materializes an operand as something a mov/alu source can name.
func (fl *fnLower) src(o vir.Operand) (Opr, error) {
	switch o.Kind {
	case vir.OperandInt:
		return Imm(o.Int), nil
	case vir.OperandBool:
		if o.Bool {
			return Imm(1), nil
		}
		return Imm(0), nil
	case vir.OperandNull:
		return Imm(0), nil
	case vir.OperandIdent:
		if o.Qualifier != "" {
			return Opr{}, fmt.Errorf("qualified reference %s survived importer.Rewrite", o)
		}
		// A local value reads from its slot. Everything else in the flat
		// namespace that can appear in operand position is either an
		// inlined constant or a symbol whose *address* is the value.
		if _, local := fl.types[o.Ident]; local {
			return Slot(o.Ident), nil
		}
		switch fl.lw.kinds[o.Ident] {
		case "const":
			c := fl.lw.consts[o.Ident]
			return fl.src(c.Value)
		case "global", "fn", "extern":
			return SymAddr(o.Ident), nil
		}
		return Opr{}, fmt.Errorf("%q names no value, constant, global or function", o.Ident)
	}
	return Opr{}, fmt.Errorf("operand %s is not a value", o)
}

// isNormalized reports whether an operand already satisfies the
// zero-extension invariant. Locals do by construction; literals and
// inlined constants do not (a -1 written for an i8 has to become 0xFF),
// so loading one costs an extra mask that loading a local doesn't.
func (fl *fnLower) isNormalized(o vir.Operand) bool {
	return o.Kind == vir.OperandIdent && fl.types[o.Ident] != nil
}

// load moves an operand into a register with no width fixup. For
// pointers, valists, and any i32 this is the whole job.
func (fl *fnLower) load(r isax86.Reg, o vir.Operand) error {
	s, err := fl.src(o)
	if err != nil {
		return err
	}
	fl.emit(Inst{Op: "mov", D: R(r), S: s, Sz: 4})
	return nil
}

// loadZ loads an operand and guarantees the zero-extension invariant.
func (fl *fnLower) loadZ(r isax86.Reg, o vir.Operand, bits int) error {
	if err := fl.load(r, o); err != nil {
		return err
	}
	if !fl.isNormalized(o) {
		fl.maskTo(r, bits)
	}
	return nil
}

// loadS loads an operand sign-extended to a full 32 bits, for the
// consumers that interpret it as signed.
func (fl *fnLower) loadS(r isax86.Reg, o vir.Operand, bits int) error {
	if err := fl.loadZ(r, o, bits); err != nil {
		return err
	}
	fl.sext32(r, bits)
	return nil
}

// maskTo clears the bits above a value's significant width, restoring the
// invariant after an operation that may have carried into them.
func (fl *fnLower) maskTo(r isax86.Reg, bits int) {
	var m int64
	switch bits {
	case 32:
		return
	case 1:
		m = 0x1
	case 8:
		m = 0xFF
	case 16:
		m = 0xFFFF
	default:
		return
	}
	fl.emit(Inst{Op: "and", D: R(r), S: Imm(m), Sz: 4})
}

// sext32 replaces a zero-extended N-bit value with its 32-bit signed
// equivalent. i8 and i16 have dedicated instructions; i1 does not, and
// the shl/sar pair is the general form (for i1 it broadcasts bit 0 across
// all 32, turning 1 into -1, which is what a one-bit signed value means).
func (fl *fnLower) sext32(r isax86.Reg, bits int) {
	switch bits {
	case 32:
	case 8:
		fl.emit(Inst{Op: "movsx", D: R(r), S: R(r), Sz: 1})
	case 16:
		fl.emit(Inst{Op: "movsx", D: R(r), S: R(r), Sz: 2})
	case 1:
		fl.emit(
			Inst{Op: "shl", D: R(r), S: Imm(31), Sz: 4},
			Inst{Op: "sar", D: R(r), S: Imm(31), Sz: 4},
		)
	}
}

// store writes a register to a value's slot.
func (fl *fnLower) store(name string, r isax86.Reg) {
	if name == "" {
		return
	}
	fl.emit(Inst{Op: "mov", D: Slot(name), S: R(r), Sz: 4})
}

// setccInto materializes a condition flag as a full 0-or-1 word.
func (fl *fnLower) setccInto(r isax86.Reg, cc byte) {
	fl.emit(
		Inst{Op: "setcc", D: R(r), CC: cc},
		Inst{Op: "movzx", D: R(r), S: R(r), Sz: 1},
	)
}

// fitsCheck leaves ZF set iff the 32-bit value in rA is representable in
// `bits` under the given signedness — the overflow predicate for every
// narrow width, since narrow arithmetic is done at 32 bits where the
// hardware flags describe the wrong type.
func (fl *fnLower) fitsCheck(bits int, signed bool) {
	fl.emit(Inst{Op: "mov", D: R(rD), S: R(rA), Sz: 4})
	if signed {
		fl.sext32(rD, bits)
	} else {
		fl.maskTo(rD, bits)
	}
	fl.emit(Inst{Op: "cmp", D: R(rA), S: R(rD), Sz: 4})
}

func (fl *fnLower) argN(in *vir.Instruction, n int) error {
	if len(in.Args) != n {
		return fmt.Errorf("expected %d operands, got %d", n, len(in.Args))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Instruction selection.
// ---------------------------------------------------------------------------

func (fl *fnLower) selInst(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpLoc:
		// Debug line info; this package emits no DWARF.
		return nil
	case vir.OpPrefetch:
		// A hint with no semantic content. Dropping it is a legal
		// implementation of every prefetch.
		return nil

	case vir.OpAdd, vir.OpSub, vir.OpMul,
		vir.OpAnd, vir.OpOr, vir.OpXor:
		return fl.selBinaryWrapping(in)

	case vir.OpNeg, vir.OpNot, vir.OpAbs:
		return fl.selUnary(in)

	case vir.OpUDiv, vir.OpSDiv, vir.OpURem, vir.OpSRem:
		return fl.selDivide(in)

	case vir.OpShl, vir.OpLShr, vir.OpAShr:
		return fl.selShift(in)

	case vir.OpRotl, vir.OpRotr:
		return fl.selRotate(in)

	case vir.OpCtlz, vir.OpCttz, vir.OpPopcnt, vir.OpBSwap:
		return fl.selBitCount(in)

	case vir.OpUMulH, vir.OpSMulH:
		return fl.selMulHigh(in)

	case vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO,
		vir.OpUMulO, vir.OpSMulO:
		return fl.selOverflow(in)

	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge:
		return fl.selCompare(in)

	case vir.OpSMin, vir.OpSMax, vir.OpUMin, vir.OpUMax:
		return fl.selMinMax(in)

	case vir.OpSelect:
		return fl.selSelect(in)

	case vir.OpTrunc, vir.OpSext, vir.OpZext, vir.OpBitcast:
		return fl.selConvert(in)

	case vir.OpAlloca:
		return fl.selAlloca(in)

	case vir.OpLoad, vir.OpLoadVol:
		return fl.selLoad(in)

	case vir.OpStore, vir.OpStoreVol:
		return fl.selStore(in)

	case vir.OpMemcopy, vir.OpMemmove, vir.OpMemset:
		return fl.selBulk(in)

	case vir.OpField:
		return fl.selField(in)

	case vir.OpIndex:
		return fl.selIndex(in)

	case vir.OpAtomicLoad, vir.OpAtomicStore, vir.OpAtomicXchg,
		vir.OpAtomicAdd, vir.OpAtomicSub,
		vir.OpAtomicAnd, vir.OpAtomicOr, vir.OpAtomicXor,
		vir.OpCmpxchg, vir.OpFence:
		return fl.selAtomic(in)

	case vir.OpCall:
		return fl.selCall(in)

	case vir.OpSyscall:
		return fl.selSyscall(in)

	case vir.OpVaStart:
		return fl.selVaStart(in)
	case vir.OpVaArg:
		return fl.selVaArg(in)
	case vir.OpVaEnd:
		// No register save area and no ABI-mandated cleanup on this
		// convention, so this genuinely compiles to nothing. §4.4 still
		// requires it at the source level, because on some other target
		// it would not.
		return nil

	case vir.OpMin, vir.OpMax, vir.OpSqrt, vir.OpFma, vir.OpCopysign,
		vir.OpFloor, vir.OpCeil, vir.OpTruncF, vir.OpNearest,
		vir.OpFdemote, vir.OpFpromote, vir.OpSfromint, vir.OpUfromint,
		vir.OpStoint, vir.OpUtoint, vir.OpStointSat, vir.OpUtointSat,
		vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe:
		return todo("%s: no float codegen path on x86", in.Op)

	case vir.OpSplat, vir.OpExtract, vir.OpInsert, vir.OpShuffle,
		vir.OpMaskedLoad, vir.OpMaskedStore, vir.OpGather, vir.OpScatter,
		vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax,
		vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		return todo("%s: no vector tier implemented on x86", in.Op)

	case vir.OpUAddSat, vir.OpSAddSat, vir.OpUSubSat, vir.OpSSubSat:
		return todo("%s: saturating arithmetic not lowered on x86", in.Op)

	case vir.OpBitrev:
		return todo("bitrev has no x86 instruction and no expansion yet")
	}
	return fmt.Errorf("opcode %s is not lowered on x86 (TODO)", in.Op)
}

// selBinaryWrapping covers the operations whose result is the low N bits
// of the mathematical result, which is every one of them where signedness
// makes no difference to those bits: add, sub, mul, and, or, xor.
func (fl *fnLower) selBinaryWrapping(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	if err := fl.loadZ(rA, in.Args[0], bits); err != nil {
		return err
	}
	if err := fl.loadZ(rC, in.Args[1], bits); err != nil {
		return err
	}
	op := ""
	switch in.Op {
	case vir.OpAdd:
		op = "add"
	case vir.OpSub:
		op = "sub"
	case vir.OpAnd:
		op = "and"
	case vir.OpOr:
		op = "or"
	case vir.OpXor:
		op = "xor"
	case vir.OpMul:
		// The low 32 bits of a product don't depend on how the operands
		// are interpreted, so the two-operand same-width form serves both
		// signednesses.
		op = "imul2"
	}
	fl.emit(Inst{Op: op, D: R(rA), S: R(rC), Sz: 4})
	// and/or/xor can't carry above N bits when both inputs are already
	// masked, but the mask is free to emit and costs nothing at i32.
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

func (fl *fnLower) selUnary(in *vir.Instruction) error {
	if err := fl.argN(in, 1); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	if err := fl.loadZ(rA, in.Args[0], bits); err != nil {
		return err
	}
	switch in.Op {
	case vir.OpNeg:
		fl.emit(Inst{Op: "neg", S: R(rA), Sz: 4})
	case vir.OpNot:
		fl.emit(Inst{Op: "not", S: R(rA), Sz: 4})
	case vir.OpAbs:
		// Branchless: broadcast the sign into a mask, then xor-and-sub.
		// abs(INT_MIN) wraps back to INT_MIN, which is what §4.1's
		// "integers wrap modulo 2^N, no UB on overflow" asks for.
		fl.sext32(rA, bits)
		fl.emit(
			Inst{Op: "mov", D: R(rC), S: R(rA), Sz: 4},
			Inst{Op: "sar", D: R(rC), S: Imm(31), Sz: 4},
			Inst{Op: "xor", D: R(rA), S: R(rC), Sz: 4},
			Inst{Op: "sub", D: R(rA), S: R(rC), Sz: 4},
		)
	}
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

// selDivide lowers udiv/sdiv/urem/srem.
//
// §5.3 requires a trap on a zero divisor, and on INT_MIN/-1 for the
// signed forms. At 32 bits the hardware does exactly that: both cases
// raise #DE, which is a deterministic, uncatchable halt. At narrower
// widths it does not — after sign extension, (-128)/(-1) is a perfectly
// representable 128 in 32-bit arithmetic — so the signed narrow forms get
// an explicit check. The zero divisor still traps in hardware at every
// width, since the extended divisor is zero exactly when the narrow one is.
func (fl *fnLower) selDivide(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSDiv || in.Op == vir.OpSRem
	rem := in.Op == vir.OpURem || in.Op == vir.OpSRem

	if signed {
		if err := fl.loadS(rA, in.Args[0], bits); err != nil {
			return err
		}
		if err := fl.loadS(rC, in.Args[1], bits); err != nil {
			return err
		}
		if bits < 32 {
			ok := fl.label("divok")
			fl.emit(
				Inst{Op: "cmp", D: R(rC), S: Imm(-1), Sz: 4},
				Inst{Op: "jcc", CC: isax86.CondNE, Lbl: ok},
				Inst{Op: "cmp", D: R(rA), S: Imm(-(1 << (bits - 1))), Sz: 4},
				Inst{Op: "jcc", CC: isax86.CondNE, Lbl: ok},
				Inst{Op: "ud2"},
				Inst{Op: "label", Lbl: ok},
			)
		}
		fl.emit(
			Inst{Op: "cdq"},
			Inst{Op: "idiv", S: R(rC), Sz: 4},
		)
	} else {
		if err := fl.loadZ(rA, in.Args[0], bits); err != nil {
			return err
		}
		if err := fl.loadZ(rC, in.Args[1], bits); err != nil {
			return err
		}
		fl.emit(
			Inst{Op: "mov", D: R(rD), S: Imm(0), Sz: 4},
			Inst{Op: "div", S: R(rC), Sz: 4},
		)
	}

	res := rA // quotient
	if rem {
		res = rD
	}
	fl.maskTo(res, bits)
	fl.store(in.Result, res)
	return nil
}

// selShift lowers shl/lshr/ashr. §4.1 masks the shift count to the
// operand width — no UB, no trap — which for i32 is exactly what the
// hardware already does, and for narrower widths has to be done by hand
// since x86 always masks to 31.
func (fl *fnLower) selShift(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	if in.Op == vir.OpAShr {
		if err := fl.loadS(rA, in.Args[0], bits); err != nil {
			return err
		}
	} else {
		if err := fl.loadZ(rA, in.Args[0], bits); err != nil {
			return err
		}
	}
	if err := fl.loadZ(rC, in.Args[1], bits); err != nil {
		return err
	}
	fl.emit(Inst{Op: "and", D: R(rC), S: Imm(int64(bits - 1)), Sz: 4})

	op := "shl"
	switch in.Op {
	case vir.OpLShr:
		op = "shr"
	case vir.OpAShr:
		op = "sar"
	}
	fl.emit(Inst{Op: op, D: R(rA), S: R(rC), Sz: 4})
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

// selRotate lowers rotl/rotr using the hardware rotate at the value's own
// width. This is the payoff for restricting values to {1, 8, 16, 32}: a
// rotate is only a single instruction when the register width matches the
// type width, and every one of those four has a form that does.
func (fl *fnLower) selRotate(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	if err := fl.loadZ(rA, in.Args[0], bits); err != nil {
		return err
	}
	if bits == 1 {
		// Rotating a one-bit value is the identity for any count.
		fl.store(in.Result, rA)
		return nil
	}
	if err := fl.loadZ(rC, in.Args[1], bits); err != nil {
		return err
	}
	fl.emit(Inst{Op: "and", D: R(rC), S: Imm(int64(bits - 1)), Sz: 4})

	op := "rol"
	if in.Op == vir.OpRotr {
		op = "ror"
	}
	fl.emit(Inst{Op: op, D: R(rA), S: R(rC), Sz: bits / 8})
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

func (fl *fnLower) selBitCount(in *vir.Instruction) error {
	if err := fl.argN(in, 1); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	if err := fl.loadZ(rA, in.Args[0], bits); err != nil {
		return err
	}

	switch in.Op {
	case vir.OpCtlz:
		// bsr yields the index of the highest set bit and leaves the
		// destination architecturally undefined when the source is zero,
		// so the zero case is recovered from ZF rather than from the
		// destination. -1 is chosen as the sentinel because it makes the
		// final subtract produce exactly `bits` with no second branch.
		fl.emit(
			Inst{Op: "bsr", D: R(rC), S: R(rA), Sz: 4},
			Inst{Op: "mov", D: R(rD), S: Imm(-1), Sz: 4},
			Inst{Op: "cmovcc", D: R(rC), S: R(rD), CC: isax86.CondE, Sz: 4},
			Inst{Op: "mov", D: R(rA), S: Imm(int64(bits - 1)), Sz: 4},
			Inst{Op: "sub", D: R(rA), S: R(rC), Sz: 4},
		)
	case vir.OpCttz:
		fl.emit(
			Inst{Op: "bsf", D: R(rC), S: R(rA), Sz: 4},
			Inst{Op: "mov", D: R(rD), S: Imm(int64(bits)), Sz: 4},
			Inst{Op: "cmovcc", D: R(rC), S: R(rD), CC: isax86.CondE, Sz: 4},
			Inst{Op: "mov", D: R(rA), S: R(rC), Sz: 4},
		)
	case vir.OpPopcnt:
		// popcnt is not baseline IA-32. Emitting it unconditionally would
		// produce a binary that faults with #UD on the machines this
		// backend otherwise targets, so it stays behind a declared tier.
		if !fl.lw.hasTier("popcnt", "sse4.2") {
			return todo("popcnt needs a declared popcnt or sse4.2 feature tier")
		}
		fl.emit(Inst{Op: "popcnt", D: R(rA), S: R(rA), Sz: 4})
	case vir.OpBSwap:
		switch bits {
		case 32:
			fl.emit(Inst{Op: "bswap", D: R(rA), Sz: 4})
		case 16:
			// bswap is 32-bit only; a 16-bit byte swap is a rotate.
			fl.emit(Inst{Op: "rol", D: R(rA), S: Imm(8), Sz: 2})
		default:
			// §9.20 rejects bswap on i8; i1 is nonsensical for the same
			// reason. ir/verify should have caught it.
			return fmt.Errorf("bswap is not defined on i%d", bits)
		}
	}
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

// selMulHigh lowers umulh/smulh — the high half of the double-width
// product.
//
// At 32 bits the widening group-3 multiply produces exactly that in EDX.
// At 8 and 16 bits the doubled width still fits one register, so a
// same-width multiply of correctly extended operands gives the whole
// product and the high half is a shift away. There is no width in between
// to worry about, because value widths are {1, 8, 16, 32}.
func (fl *fnLower) selMulHigh(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSMulH

	if bits == 1 {
		// The high half of a 1x1-bit product is always zero.
		fl.emit(Inst{Op: "mov", D: R(rA), S: Imm(0), Sz: 4})
		fl.store(in.Result, rA)
		return nil
	}

	loadOne := fl.loadZ
	if signed {
		loadOne = fl.loadS
	}
	if err := loadOne(rA, in.Args[0], bits); err != nil {
		return err
	}
	if err := loadOne(rC, in.Args[1], bits); err != nil {
		return err
	}

	if bits == 32 {
		if signed {
			fl.emit(Inst{Op: "imul1", S: R(rC), Sz: 4})
		} else {
			fl.emit(Inst{Op: "mul", S: R(rC), Sz: 4})
		}
		fl.store(in.Result, rD)
		return nil
	}

	fl.emit(Inst{Op: "imul2", D: R(rA), S: R(rC), Sz: 4})
	if signed {
		fl.emit(Inst{Op: "sar", D: R(rA), S: Imm(int64(bits)), Sz: 4})
	} else {
		fl.emit(Inst{Op: "shr", D: R(rA), S: Imm(int64(bits)), Sz: 4})
	}
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

// selOverflow lowers the six predicates that report whether an operation
// overflowed its declared width.
//
// At 32 bits the hardware flags describe the right type and are read
// directly. Below 32 they describe a 32-bit operation on extended
// operands and are therefore always clear, so the predicate becomes "does
// the 32-bit result still fit in N bits" — one comparison against the
// re-narrowed value, uniform across all six opcodes.
func (fl *fnLower) selOverflow(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}

	var signed bool
	var op string
	var cc32 byte
	switch in.Op {
	case vir.OpUAddO:
		signed, op, cc32 = false, "add", isax86.CondC
	case vir.OpSAddO:
		signed, op, cc32 = true, "add", isax86.CondO
	case vir.OpUSubO:
		signed, op, cc32 = false, "sub", isax86.CondB
	case vir.OpSSubO:
		signed, op, cc32 = true, "sub", isax86.CondB
	case vir.OpUMulO:
		signed, op, cc32 = false, "mul", isax86.CondC
	case vir.OpSMulO:
		signed, op, cc32 = true, "imul1", isax86.CondO
	}
	if in.Op == vir.OpSSubO {
		cc32 = isax86.CondO
	}

	loadOne := fl.loadZ
	if signed {
		loadOne = fl.loadS
	}
	if err := loadOne(rA, in.Args[0], bits); err != nil {
		return err
	}
	if err := loadOne(rC, in.Args[1], bits); err != nil {
		return err
	}

	if bits == 32 {
		switch op {
		case "mul", "imul1":
			// Both widening forms set CF and OF together when the result
			// does not fit the source width, so either flag reads it.
			fl.emit(Inst{Op: op, S: R(rC), Sz: 4})
		default:
			fl.emit(Inst{Op: op, D: R(rA), S: R(rC), Sz: 4})
		}
		fl.setccInto(rC, cc32)
		fl.store(in.Result, rC)
		return nil
	}

	if op == "mul" || op == "imul1" {
		op = "imul2"
	}
	fl.emit(Inst{Op: op, D: R(rA), S: R(rC), Sz: 4})
	fl.fitsCheck(bits, signed)
	fl.setccInto(rC, isax86.CondNE)
	fl.store(in.Result, rC)
	return nil
}

func (fl *fnLower) selCompare(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}

	var cc byte
	signed := false
	switch in.Op {
	case vir.OpEq:
		cc = isax86.CondE
	case vir.OpNe:
		cc = isax86.CondNE
	case vir.OpSlt:
		cc, signed = isax86.CondL, true
	case vir.OpSgt:
		cc, signed = isax86.CondG, true
	case vir.OpSle:
		cc, signed = isax86.CondLE, true
	case vir.OpSge:
		cc, signed = isax86.CondGE, true
	case vir.OpUlt:
		cc = isax86.CondB
	case vir.OpUgt:
		cc = isax86.CondA
	case vir.OpUle:
		cc = isax86.CondBE
	case vir.OpUge:
		cc = isax86.CondAE
	}

	// eq/ne need no extension at all: two values normalized to the same
	// width are bit-equal exactly when they are value-equal, whichever
	// way the bits are read.
	loadOne := fl.loadZ
	if signed {
		loadOne = fl.loadS
	}
	if err := loadOne(rA, in.Args[0], bits); err != nil {
		return err
	}
	if err := loadOne(rC, in.Args[1], bits); err != nil {
		return err
	}
	fl.emit(Inst{Op: "cmp", D: R(rA), S: R(rC), Sz: 4})
	fl.setccInto(rD, cc)
	fl.store(in.Result, rD)
	return nil
}

func (fl *fnLower) selMinMax(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	signed := in.Op == vir.OpSMin || in.Op == vir.OpSMax
	var cc byte
	switch in.Op {
	case vir.OpSMin:
		cc = isax86.CondL
	case vir.OpSMax:
		cc = isax86.CondG
	case vir.OpUMin:
		cc = isax86.CondB
	case vir.OpUMax:
		cc = isax86.CondA
	}

	loadOne := fl.loadZ
	if signed {
		loadOne = fl.loadS
	}
	if err := loadOne(rA, in.Args[0], bits); err != nil {
		return err
	}
	if err := loadOne(rC, in.Args[1], bits); err != nil {
		return err
	}
	fl.emit(
		Inst{Op: "cmp", D: R(rC), S: R(rA), Sz: 4},
		Inst{Op: "cmovcc", D: R(rA), S: R(rC), CC: cc, Sz: 4},
	)
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

func (fl *fnLower) selSelect(in *vir.Instruction) error {
	if err := fl.argN(in, 3); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	// The two arms are loaded before the condition so that the test and
	// the cmov are adjacent — mov does not disturb flags, but keeping the
	// flag-setting and flag-reading instructions together is what makes
	// that fact something a reader doesn't have to verify.
	if err := fl.loadZ(rC, in.Args[1], bits); err != nil {
		return err
	}
	if err := fl.loadZ(rD, in.Args[2], bits); err != nil {
		return err
	}
	if err := fl.load(rA, in.Args[0]); err != nil {
		return err
	}
	fl.emit(
		Inst{Op: "test", D: R(rA), S: R(rA), Sz: 4},
		Inst{Op: "cmovcc", D: R(rD), S: R(rC), CC: isax86.CondNE, Sz: 4},
	)
	fl.store(in.Result, rD)
	return nil
}

// selConvert lowers trunc/sext/zext/bitcast.
//
// Under the zero-extension invariant two of the four are free. zext is a
// plain copy: an N-bit value already sits zero-extended, and widening it
// changes no bits. bitcast between ptr and i32 is a copy for the same
// reason — §4.1 requires an exact usize match, so there is nothing to
// convert. Only trunc and sext do real work.
func (fl *fnLower) selConvert(in *vir.Instruction) error {
	if err := fl.argN(in, 1); err != nil {
		return err
	}
	dstBits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	srcBits := 32
	if in.Args[0].Kind == vir.OperandIdent {
		if t, err := fl.valueType(in.Args[0].Ident); err == nil {
			if b, err := intBits(t); err == nil {
				srcBits = b
			}
		}
	}

	if err := fl.loadZ(rA, in.Args[0], srcBits); err != nil {
		return err
	}
	if in.Op == vir.OpSext {
		fl.sext32(rA, srcBits)
	}
	fl.maskTo(rA, dstBits)
	fl.store(in.Result, rA)
	return nil
}

// selAlloca reserves stack space.
//
// The reservation is rounded up to StackAlign, not merely to a word. A
// dynamically-sized bump that left esp on an arbitrary boundary would
// silently break the 16-byte alignment every subsequent call in the
// function depends on — a bug that surfaces as a fault inside libc, far
// from the alloca that caused it.
func (fl *fnLower) selAlloca(in *vir.Instruction) error {
	if vir.IsValist(in.Suffix) {
		// A valist is a plain 4-byte cursor here, so declaring one emits
		// nothing: the slot appears the first time va_start writes to it,
		// exactly like any other value's slot.
		return nil
	}
	if !vir.IsPtr(in.Suffix) {
		return fmt.Errorf("alloca suffix must be ptr or valist, got %s", in.Suffix)
	}
	if err := fl.argN(in, 1); err != nil {
		return err
	}

	if sz := in.Args[0]; sz.Kind == vir.OperandInt {
		if sz.Int < 0 {
			return fmt.Errorf("alloca size %d is negative", sz.Int)
		}
		n := int64(roundUp(int(sz.Int), StackAlign))
		if n > 0 {
			fl.emit(Inst{Op: "sub", D: R(rSP), S: Imm(n), Sz: 4})
		}
	} else {
		if err := fl.load(rA, in.Args[0]); err != nil {
			return err
		}
		fl.emit(
			Inst{Op: "add", D: R(rA), S: Imm(StackAlign - 1), Sz: 4},
			Inst{Op: "and", D: R(rA), S: Imm(-StackAlign), Sz: 4},
			Inst{Op: "sub", D: R(rSP), S: R(rA), Sz: 4},
		)
	}

	// An over-aligned request needs the pointer itself aligned, not just
	// the size; StackAlign already covers anything at or below it.
	if in.Align > StackAlign {
		if in.Align&(in.Align-1) != 0 {
			return fmt.Errorf("alloca alignment %d is not a power of two", in.Align)
		}
		fl.emit(Inst{Op: "and", D: R(rSP), S: Imm(int64(-in.Align)), Sz: 4})
	}
	fl.store(in.Result, rSP)
	return nil
}

// memWidth is the number of bytes a type occupies in memory, which is not
// the same as the four bytes its value occupies in a slot.
func (fl *fnLower) memWidth(t vir.Type) (int, error) {
	if err := checkValueType(t); err != nil {
		return 0, err
	}
	return fl.lw.lay.Size(t)
}

// selLoad lowers load/load_vol. Volatility needs no different encoding:
// the guarantee is that the access is not elided, duplicated, reordered
// against another volatile access, or changed in width, and this backend
// performs none of those transformations on any access.
func (fl *fnLower) selLoad(in *vir.Instruction) error {
	if err := fl.argN(in, 1); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	w, err := fl.memWidth(in.Suffix)
	if err != nil {
		return err
	}
	if err := fl.load(rA, in.Args[0]); err != nil {
		return err
	}
	switch w {
	case 1, 2:
		fl.emit(Inst{Op: "movzx", D: R(rA), S: Mem(rA, 0), Sz: w})
	default:
		fl.emit(Inst{Op: "mov", D: R(rA), S: Mem(rA, 0), Sz: 4})
	}
	// An i1 occupies a whole byte in memory but only one significant bit
	// in a slot, so a loaded byte still has to be narrowed.
	fl.maskTo(rA, bits)
	fl.store(in.Result, rA)
	return nil
}

func (fl *fnLower) selStore(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	w, err := fl.memWidth(in.Suffix)
	if err != nil {
		return err
	}
	if err := fl.load(rA, in.Args[0]); err != nil {
		return err
	}
	if err := fl.loadZ(rC, in.Args[1], bits); err != nil {
		return err
	}
	fl.emit(Inst{Op: "mov", D: Mem(rA, 0), S: R(rC), Sz: w})
	return nil
}

// selBulk lowers memcopy/memmove/memset with the string instructions.
// ESI/EDI/ECX are all fair game: the prologue saved the two callee-saved
// ones, and no IR value lives in a register across an instruction.
func (fl *fnLower) selBulk(in *vir.Instruction) error {
	if err := fl.argN(in, 3); err != nil {
		return err
	}

	switch in.Op {
	case vir.OpMemset:
		if err := fl.load(rDI, in.Args[0]); err != nil {
			return err
		}
		if err := fl.load(rA, in.Args[1]); err != nil {
			return err
		}
		if err := fl.load(rC, in.Args[2]); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cld"}, Inst{Op: "rep_stosb"})
		return nil

	case vir.OpMemcopy:
		// §5.4 makes overlap UB here, so the forward copy is
		// unconditionally correct and no direction test is emitted.
		if err := fl.load(rDI, in.Args[0]); err != nil {
			return err
		}
		if err := fl.load(rSI, in.Args[1]); err != nil {
			return err
		}
		if err := fl.load(rC, in.Args[2]); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cld"}, Inst{Op: "rep_movsb"})
		return nil
	}

	// memmove: forward when the destination starts at or below the
	// source, backward otherwise. DF is left clear on both paths, since
	// the ABI requires it clear at every call and return.
	if err := fl.load(rDI, in.Args[0]); err != nil {
		return err
	}
	if err := fl.load(rSI, in.Args[1]); err != nil {
		return err
	}
	if err := fl.load(rC, in.Args[2]); err != nil {
		return err
	}
	back := fl.label("mmback")
	done := fl.label("mmdone")
	fl.emit(
		Inst{Op: "cmp", D: R(rDI), S: R(rSI), Sz: 4},
		Inst{Op: "jcc", CC: isax86.CondA, Lbl: back},
		Inst{Op: "cld"},
		Inst{Op: "rep_movsb"},
		Inst{Op: "jmp", Lbl: done},
		Inst{Op: "label", Lbl: back},
		Inst{Op: "lea", D: R(rDI), S: MemIndexed(rDI, rC, 1, -1)},
		Inst{Op: "lea", D: R(rSI), S: MemIndexed(rSI, rC, 1, -1)},
		Inst{Op: "std"},
		Inst{Op: "rep_movsb"},
		Inst{Op: "cld"},
		Inst{Op: "label", Lbl: done},
	)
	return nil
}

func (fl *fnLower) selField(in *vir.Instruction) error {
	if err := fl.argN(in, 3); err != nil {
		return err
	}
	if in.Args[1].Kind != vir.OperandIdent || in.Args[2].Kind != vir.OperandIdent {
		return fmt.Errorf("field.ptr needs a struct name and a field name")
	}
	off, err := fl.lw.lay.FieldOffset(in.Args[1].Ident, in.Args[2].Ident)
	if err != nil {
		return err
	}
	if err := fl.load(rA, in.Args[0]); err != nil {
		return err
	}
	if off != 0 {
		fl.emit(Inst{Op: "add", D: R(rA), S: Imm(int64(off)), Sz: 4})
	}
	fl.store(in.Result, rA)
	return nil
}

func (fl *fnLower) selIndex(in *vir.Instruction) error {
	if err := fl.argN(in, 3); err != nil {
		return err
	}
	if in.Args[1].Kind != vir.OperandType {
		return fmt.Errorf("index.ptr needs an element type operand")
	}
	es, err := fl.lw.lay.Size(in.Args[1].Type)
	if err != nil {
		return err
	}
	if err := fl.load(rA, in.Args[0]); err != nil {
		return err
	}
	if es == 0 {
		fl.store(in.Result, rA)
		return nil
	}
	if err := fl.load(rC, in.Args[2]); err != nil {
		return err
	}
	// The SIB scale factors cover the common element sizes in a single
	// address computation; anything else needs a real multiply.
	switch es {
	case 1, 2, 4, 8:
		fl.emit(Inst{Op: "lea", D: R(rA), S: MemIndexed(rA, rC, byte(es), 0)})
	default:
		fl.emit(
			Inst{Op: "imul3", D: R(rC), S: R(rC), Imm: int64(es), Sz: 4},
			Inst{Op: "add", D: R(rA), S: R(rC), Sz: 4},
		)
	}
	fl.store(in.Result, rA)
	return nil
}

// selAtomic lowers the atomic family, restricted to 32-bit operands.
//
// x86's memory model does most of the work: every access below is already
// at least acquire-release, so the ordering operand only changes codegen
// where sequential consistency needs a store-load barrier. A seqcst store
// is therefore an xchg (implicitly locked, and a full barrier) rather
// than a mov plus a separate fence.
func (fl *fnLower) selAtomic(in *vir.Instruction) error {
	if in.Op == vir.OpFence {
		if err := fl.argN(in, 1); err != nil {
			return err
		}
		if ordAtLeast(in.Args[0], "acqrel") {
			fl.emit(Inst{Op: "mfence"})
		}
		// Weaker fences constrain only the compiler, and this backend
		// reorders nothing.
		return nil
	}

	if bits, err := intBits(in.Suffix); err != nil {
		return err
	} else if bits != 32 {
		return todo("atomic %s on i%d: only 32-bit atomics are lowered on x86", in.Op, bits)
	}

	switch in.Op {
	case vir.OpAtomicLoad:
		if err := fl.argN(in, 2); err != nil {
			return err
		}
		if err := fl.load(rA, in.Args[0]); err != nil {
			return err
		}
		// An aligned 32-bit load is already acquire on x86, and remains
		// correct for seqcst because the seqcst *stores* are barriers.
		fl.emit(Inst{Op: "mov", D: R(rA), S: Mem(rA, 0), Sz: 4})
		fl.store(in.Result, rA)
		return nil

	case vir.OpAtomicStore:
		if err := fl.argN(in, 3); err != nil {
			return err
		}
		if err := fl.load(rDI, in.Args[0]); err != nil {
			return err
		}
		if err := fl.load(rA, in.Args[1]); err != nil {
			return err
		}
		if ordAtLeast(in.Args[2], "seqcst") {
			fl.emit(Inst{Op: "xchg", D: Mem(rDI, 0), S: R(rA), Sz: 4})
		} else {
			fl.emit(Inst{Op: "mov", D: Mem(rDI, 0), S: R(rA), Sz: 4})
		}
		return nil

	case vir.OpAtomicXchg:
		if err := fl.argN(in, 3); err != nil {
			return err
		}
		if err := fl.load(rDI, in.Args[0]); err != nil {
			return err
		}
		if err := fl.load(rA, in.Args[1]); err != nil {
			return err
		}
		// xchg with a memory operand asserts LOCK implicitly.
		fl.emit(Inst{Op: "xchg", D: Mem(rDI, 0), S: R(rA), Sz: 4})
		fl.store(in.Result, rA)
		return nil

	case vir.OpAtomicAdd, vir.OpAtomicSub:
		if err := fl.argN(in, 3); err != nil {
			return err
		}
		if err := fl.load(rDI, in.Args[0]); err != nil {
			return err
		}
		if err := fl.load(rA, in.Args[1]); err != nil {
			return err
		}
		if in.Op == vir.OpAtomicSub {
			fl.emit(Inst{Op: "neg", S: R(rA), Sz: 4})
		}
		// xadd leaves the previous value in the source register, which is
		// what an RMW is defined to return.
		fl.emit(Inst{Op: "lock_xadd", D: Mem(rDI, 0), S: R(rA), Sz: 4})
		fl.store(in.Result, rA)
		return nil

	case vir.OpAtomicAnd, vir.OpAtomicOr, vir.OpAtomicXor:
		if err := fl.argN(in, 3); err != nil {
			return err
		}
		// There is no locked and/or/xor that returns the previous value,
		// so these become a compare-and-swap retry loop.
		if err := fl.load(rDI, in.Args[0]); err != nil {
			return err
		}
		val, err := fl.src(in.Args[1])
		if err != nil {
			return err
		}
		op := "and"
		switch in.Op {
		case vir.OpAtomicOr:
			op = "or"
		case vir.OpAtomicXor:
			op = "xor"
		}
		retry := fl.label("rmw")
		fl.emit(
			Inst{Op: "mov", D: R(rA), S: Mem(rDI, 0), Sz: 4},
			Inst{Op: "label", Lbl: retry},
			Inst{Op: "mov", D: R(rC), S: R(rA), Sz: 4},
			Inst{Op: op, D: R(rC), S: val, Sz: 4},
			// cmpxchg compares EAX with the destination; on failure it
			// reloads EAX with the current value, so the retry needs no
			// separate load.
			Inst{Op: "lock_cmpxchg", D: Mem(rDI, 0), S: R(rC), Sz: 4},
			Inst{Op: "jcc", CC: isax86.CondNE, Lbl: retry},
		)
		fl.store(in.Result, rA)
		return nil

	case vir.OpCmpxchg:
		if err := fl.argN(in, 5); err != nil {
			return err
		}
		if err := fl.load(rDI, in.Args[0]); err != nil {
			return err
		}
		if err := fl.load(rA, in.Args[1]); err != nil {
			return err
		}
		if err := fl.load(rC, in.Args[2]); err != nil {
			return err
		}
		fl.emit(Inst{Op: "lock_cmpxchg", D: Mem(rDI, 0), S: R(rC), Sz: 4})
		// EAX holds the previous value either way; the caller compares it
		// against its expected value to learn whether the swap happened.
		fl.store(in.Result, rA)
		return nil
	}
	return fmt.Errorf("unhandled atomic %s", in.Op)
}

// ordAtLeast reports whether an ordering operand names `want` or
// something stronger. The vocabulary is small and totally ordered, so a
// rank table is clearer than a set of comparisons.
func ordAtLeast(o vir.Operand, want string) bool {
	rank := map[string]int{
		"relaxed": 0, "acquire": 1, "release": 1, "acqrel": 2, "seqcst": 3,
	}
	if o.Kind != vir.OperandOrdering {
		return true // unrecognized: assume the strongest and stay correct
	}
	return rank[o.Ordering] >= rank[want]
}

// ---------------------------------------------------------------------------
// Calls.
// ---------------------------------------------------------------------------

func (fl *fnLower) selCall(in *vir.Instruction) error {
	var (
		params   []vir.Param
		ret      vir.Type
		callee   string
		indirect bool
	)
	if in.Sig != "" {
		sig, ok := fl.lw.lookupSig(in.Sig)
		if !ok {
			return fmt.Errorf("fnsig %q is not declared", in.Sig)
		}
		// A fnsig records parameter *types* but no byval attribution, so
		// an indirect call cannot pass a struct by value — which is
		// exactly what the grammar allows.
		ret, indirect = sig.Ret, true
	} else {
		if len(in.Args) == 0 || in.Args[0].Kind != vir.OperandIdent {
			return fmt.Errorf("call has no callee operand")
		}
		callee = in.Args[0].Ident
		c, ok := fl.lw.lookupCallee(callee)
		if !ok {
			return fmt.Errorf("%q is not a declared function", callee)
		}
		params, ret = c.Params, c.Ret
	}
	if len(in.Args) == 0 {
		return fmt.Errorf("call has no target operand")
	}
	args := in.Args[1:]

	slots, total, err := PlanCall(params, len(args), fl.lw.lay.ByValSize)
	if err != nil {
		return err
	}
	if total > 0 {
		fl.emit(Inst{Op: "sub", D: R(rSP), S: Imm(int64(total)), Sz: 4})
	}
	if err := fl.writeArgs(args, slots, rSP, 0); err != nil {
		return err
	}

	if indirect {
		// Loaded last, after the argument stores have finished with EAX.
		// The pointer lives in a frame slot, which the argument area
		// below esp cannot have disturbed.
		if err := fl.load(rA, in.Args[0]); err != nil {
			return err
		}
		fl.emit(Inst{Op: "call_r", S: R(rA)})
	} else {
		fl.emit(Inst{Op: "call_sym", Sym: callee})
	}
	if total > 0 {
		fl.emit(Inst{Op: "add", D: R(rSP), S: Imm(int64(total)), Sz: 4})
	}

	if in.Result != "" {
		if ret == nil || vir.IsVoid(ret) {
			return fmt.Errorf("call binds a result but the callee returns void")
		}
		bits, err := intBits(ret)
		if err != nil {
			return err
		}
		// A callee returning a narrow type is only obliged to make the
		// low bits meaningful, so the invariant is re-established here
		// rather than trusted.
		fl.maskTo(rA, bits)
		fl.store(in.Result, rA)
	}
	return nil
}

// writeArgs stores each outgoing argument into the argument area based at
// [base+baseOff]. byval arguments name a pointer to the struct and are
// copied by value; everything else is a single word.
func (fl *fnLower) writeArgs(args []vir.Operand, slots []ArgSlot, base isax86.Reg, baseOff int32) error {
	for i, a := range args {
		dst := int32(slots[i].Offset) + baseOff
		if bv := slots[i].ByVal; bv != "" {
			sz, err := fl.lw.lay.ByValSize(bv)
			if err != nil {
				return err
			}
			if err := fl.load(rSI, a); err != nil {
				return err
			}
			fl.emit(
				Inst{Op: "lea", D: R(rDI), S: Mem(base, dst)},
				Inst{Op: "mov", D: R(rC), S: Imm(int64(sz)), Sz: 4},
				Inst{Op: "cld"},
				Inst{Op: "rep_movsb"},
			)
			continue
		}
		if err := fl.load(rA, a); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mov", D: Mem(base, dst), S: R(rA), Sz: 4})
	}
	return nil
}

// selSyscall lowers a hardware trap using the per-OS register convention.
func (fl *fnLower) selSyscall(in *vir.Instruction) error {
	if fl.lw.m.Target == nil {
		return fmt.Errorf("syscall needs a declared target OS")
	}
	conv, ok := syscallConventionFor(fl.lw.m.Target.OS)
	if !ok {
		return todo("no syscall convention for os %q", fl.lw.m.Target.OS)
	}
	if len(in.Args) == 0 {
		return fmt.Errorf("syscall has no system-call number")
	}
	if len(in.Args) > 7 {
		return fmt.Errorf("syscall takes at most 7 operands, got %d", len(in.Args))
	}

	if conv.StackArgsPushRetAddrPlaceholder {
		// FreeBSD's convention: only the number is in a register, the
		// arguments sit above a placeholder where a return address would
		// be for a normal call.
		for i := len(in.Args) - 1; i >= 1; i-- {
			s, err := fl.src(in.Args[i])
			if err != nil {
				return err
			}
			fl.emit(Inst{Op: "push", S: s})
		}
		fl.emit(Inst{Op: "push", S: Imm(0)})
		if err := fl.load(conv.Result, in.Args[0]); err != nil {
			return err
		}
		fl.emit(conv.Trap)
		fl.emit(Inst{Op: "add", D: R(rSP), S: Imm(int64(4 * len(in.Args))), Sz: 4})
		if in.Result != "" {
			fl.store(in.Result, conv.Result)
		}
		return nil
	}

	// Register convention. EBP is the last argument register on Linux and
	// is also this frame's base pointer, so it is loaded last — the load
	// itself reads through EBP, which is still valid at that instant —
	// and restored before anything else needs a frame reference again.
	var ebpArg *vir.Operand
	for i := 1; i < len(in.Args); i++ {
		r, ok := conv.RegisterFor(i)
		if !ok {
			return fmt.Errorf("syscall convention has no register for argument %d", i)
		}
		if r == rBP {
			a := in.Args[i]
			ebpArg = &a
			continue
		}
		if err := fl.load(r, in.Args[i]); err != nil {
			return err
		}
	}
	numReg, ok := conv.RegisterFor(0)
	if !ok {
		return fmt.Errorf("syscall convention has no register for the call number")
	}
	if err := fl.load(numReg, in.Args[0]); err != nil {
		return err
	}
	if ebpArg != nil {
		fl.emit(Inst{Op: "push", S: R(rBP)})
		if err := fl.load(rBP, *ebpArg); err != nil {
			return err
		}
	}
	fl.emit(conv.Trap)
	if ebpArg != nil {
		fl.emit(Inst{Op: "pop", D: R(rBP)})
	}
	if in.Result != "" {
		fl.store(in.Result, conv.Result)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Variadic access (§4.4).
// ---------------------------------------------------------------------------

// selVaStart points the cursor just past the last named parameter.
//
// The offset comes from the parameter layout, not from the parameter's
// index: ParamBase+4*(i+1) is only correct when no earlier parameter is
// byval, and silently wrong when one is.
func (fl *fnLower) selVaStart(in *vir.Instruction) error {
	if err := fl.argN(in, 2); err != nil {
		return err
	}
	if !fl.fn.Variadic {
		return fmt.Errorf("va_start in a non-variadic function")
	}
	dst, last := in.Args[0], in.Args[1]
	if dst.Kind != vir.OperandIdent || last.Kind != vir.OperandIdent {
		return fmt.Errorf("va_start needs two identifiers")
	}
	off, ok := fl.paramEnd[last.Ident]
	if !ok {
		return fmt.Errorf("%q is not a parameter of this function", last.Ident)
	}
	if off != ParamBase+fl.argBytes {
		return fmt.Errorf("va_start's last_named %q is not the final parameter", last.Ident)
	}
	fl.emit(
		Inst{Op: "lea", D: R(rA), S: Mem(rBP, off)},
		Inst{Op: "mov", D: Slot(dst.Ident), S: R(rA), Sz: 4},
	)
	return nil
}

// selVaArg reads one variadic argument and advances the cursor.
//
// Every argument occupies a whole 4-byte word regardless of its declared
// width, so the read is always a full word and the advance is always 4 —
// the narrowing happens afterwards, on the value.
func (fl *fnLower) selVaArg(in *vir.Instruction) error {
	if err := fl.argN(in, 1); err != nil {
		return err
	}
	if !vir.IsVaArgType(in.Suffix) {
		return fmt.Errorf("%s is not a legal va_arg destination type", in.Suffix)
	}
	bits, err := intBits(in.Suffix)
	if err != nil {
		return err
	}
	src := in.Args[0]
	if src.Kind != vir.OperandIdent {
		return fmt.Errorf("va_arg source must be a valist identifier")
	}
	fl.emit(
		Inst{Op: "mov", D: R(rA), S: Slot(src.Ident), Sz: 4},
		Inst{Op: "mov", D: R(rC), S: Mem(rA, 0), Sz: 4},
		Inst{Op: "add", D: R(rA), S: Imm(ArgWordBytes), Sz: 4},
		Inst{Op: "mov", D: Slot(src.Ident), S: R(rA), Sz: 4},
	)
	fl.maskTo(rC, bits)
	fl.store(in.Result, rC)
	return nil
}

// ---------------------------------------------------------------------------
// Terminators (§4.3).
// ---------------------------------------------------------------------------

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		fl.emit(Inst{Op: "jmp", Lbl: x.Label})
		return nil

	case vir.BranchIf:
		if err := fl.load(rA, x.Cond); err != nil {
			return err
		}
		fl.emit(
			Inst{Op: "test", D: R(rA), S: R(rA), Sz: 4},
			Inst{Op: "jcc", CC: isax86.CondNE, Lbl: x.Then},
			Inst{Op: "jmp", Lbl: x.Else},
		)
		return nil

	case vir.Switch:
		// A compare chain rather than a jump table. Correct for any case
		// distribution; a table would be faster for dense ones and is the
		// obvious thing to add once anything here cares about speed.
		if err := fl.load(rA, x.Value); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.emit(
				Inst{Op: "cmp", D: R(rA), S: Imm(c.Value), Sz: 4},
				Inst{Op: "jcc", CC: isax86.CondE, Lbl: c.Label},
			)
		}
		fl.emit(Inst{Op: "jmp", Lbl: x.Default})
		return nil

	case vir.Return:
		if x.Value != nil {
			if err := fl.load(rA, *x.Value); err != nil {
				return err
			}
		}
		fl.emit(Inst{Op: "epi_ret"})
		return nil

	case vir.TailCall:
		return fl.selTailCall(x)

	case vir.Trap:
		fl.emit(Inst{Op: "ud2"})
		return nil

	case vir.Unreachable:
		// §5.4 makes reaching this UB, so anything at all would be legal.
		// Halting is the choice that turns a miscompile into a crash at
		// the point of the mistake instead of an arbitrary fallthrough.
		fl.emit(Inst{Op: "ud2"})
		return nil
	}
	return fmt.Errorf("unknown terminator %T", t)
}

// selTailCall reuses this frame for the callee.
//
// Arguments are staged below the frame and then block-copied up into the
// incoming argument area, rather than written there directly. Writing
// directly would be a hazard: the incoming area holds this function's own
// parameters, so storing argument i could destroy a parameter that
// argument j > i still needs to read. The staging copy costs a rep movsb
// on a path that is rare, and removes the need to reason about the
// overlap at all.
func (fl *fnLower) selTailCall(t vir.TailCall) error {
	var params []vir.Param
	var args []vir.Operand
	indirect := t.Sig != ""

	if indirect {
		if _, ok := fl.lw.lookupSig(t.Sig); !ok {
			return fmt.Errorf("fnsig %q is not declared", t.Sig)
		}
		if len(t.Args) == 0 {
			return fmt.Errorf("indirect tailcall has no function pointer")
		}
		args = t.Args[1:]
	} else {
		c, ok := fl.lw.lookupCallee(t.Callee)
		if !ok {
			return fmt.Errorf("%q is not a declared function", t.Callee)
		}
		params, args = c.Params, t.Args
	}

	slots, total, err := LayoutArgs(params, len(args), fl.lw.lay.ByValSize)
	if err != nil {
		return err
	}
	for _, s := range slots {
		if s.ByVal != "" {
			// §4.2 rejects byval on a tailcall; the copy would have to
			// live in a frame that is about to be reused.
			return fmt.Errorf("tailcall cannot pass a struct by value")
		}
	}
	if int32(total) > fl.argBytes {
		return todo("tailcall needs %d argument bytes but this frame only has %d", total, fl.argBytes)
	}

	if total > 0 {
		staged := roundUp(total, StackAlign)
		fl.emit(Inst{Op: "sub", D: R(rSP), S: Imm(int64(staged)), Sz: 4})
		if err := fl.writeArgs(args, slots, rSP, 0); err != nil {
			return err
		}
		fl.emit(
			Inst{Op: "mov", D: R(rSI), S: R(rSP), Sz: 4},
			Inst{Op: "lea", D: R(rDI), S: Mem(rBP, ParamBase)},
			Inst{Op: "mov", D: R(rC), S: Imm(int64(total)), Sz: 4},
			Inst{Op: "cld"},
			Inst{Op: "rep_movsb"},
		)
	}

	if indirect {
		// EAX is one of the three registers the epilogue does not pop, so
		// the target survives the frame teardown.
		if err := fl.load(rA, t.Args[0]); err != nil {
			return err
		}
		fl.emit(Inst{Op: "epi_jmp_r", S: R(rA)})
		return nil
	}
	fl.emit(Inst{Op: "epi_jmp_sym", Sym: t.Callee})
	return nil
}