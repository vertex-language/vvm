package ptx

import (
	"fmt"

	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// The §7.3 Join Convention, realized as virtual registers.
//
// Values merge across blocks by same-name assignment. A name is therefore one
// register (or one register per lane), written on first assignment and rewritten
// on every later one. There are no phi nodes to insert and loop-carried values
// need no special form, exactly as §7.3 promises.

type value struct {
	typ  gvir.Type
	regs []ptx.Reg // one per lane; len == 1 for scalars

	// pointee is the type a pointer value points at, when it is recoverable
	// from provenance. .gvir pointers carry a space but no pointee, so this is
	// best-effort and nil means "unknown" — field.ptr through such a pointer is
	// a lowering error rather than a guess (README §9).
	pointee gvir.Type
}

func (v value) reg() ptx.Reg { return v.regs[0] }

func regOperands(rs []ptx.Reg) []ptx.Operand {
	out := make([]ptx.Operand, len(rs))
	for i, r := range rs {
		out[i] = r
	}
	return out
}

// fn is the per-body lowering context: one func or one kernel.
type fn struct {
	*lowerer

	b      *ptx.Body
	names  map[string]value
	labels map[string]*ptx.Label

	kernel  *gvir.Kernel // nil in a func
	fdecl   *gvir.Func   // nil in a kernel
	retPar  []*ptx.Param // callee-side return params, lane by lane
	retType gvir.Type
}

func (l *lowerer) newFn(b *ptx.Body) *fn {
	return &fn{lowerer: l, b: b, names: map[string]value{}, labels: map[string]*ptx.Label{}}
}

// newValue allocates the registers for one value of type t.
func (f *fn) newValue(t gvir.Type) (value, error) {
	rt, err := regType(t)
	if err != nil {
		return value{}, err
	}
	return value{typ: t, regs: f.b.Regs.NewN(rt, laneCount(t))}, nil
}

// define binds name to a value of type t, or returns the existing binding.
// The first assignment permanently fixes the type (§7.3 rule 2), including the
// address space of a pointer.
func (f *fn) define(name string, t gvir.Type) (value, error) {
	if name == "" {
		return f.newValue(t)
	}
	if v, ok := f.names[name]; ok {
		if !gvir.Equal(v.typ, t) {
			return value{}, todof("name %q is bound as %s and reassigned as %s (§7.3)", name, v.typ, t)
		}
		return v, nil
	}
	v, err := f.newValue(t)
	if err != nil {
		return value{}, err
	}
	f.names[name] = v
	return v, nil
}

// bind installs a fully formed value under name, used by the prologue for
// parameters, group declarations and allocas.
func (f *fn) bind(name string, v value) { f.names[name] = v }

// temp allocates an unnamed value.
func (f *fn) temp(t gvir.Type) (value, error) { return f.newValue(t) }

func (f *fn) tempReg(t ptx.Type) ptx.Reg { return f.b.Regs.New(t) }

// result allocates the destination of in from the opcode's registered result
// rule. Opcodes whose result depends on operands (call, index, field) are
// handled by their own selection routines and never reach this.
func (f *fn) result(in *gvir.Instruction) (value, error) {
	t, ok := in.Op.ResultType(in.Suffix, in.Dim)
	if !ok {
		return value{}, todof("%s has no mechanically derived result type", in.Op)
	}
	if gvir.IsVoid(t) {
		return value{}, todof("%s produces no value", in.Op)
	}
	return f.define(in.Result, t)
}

// lookup resolves an ident operand to its bound value.
func (f *fn) lookup(o gvir.Operand) (value, bool) {
	if o.Kind != gvir.OperandIdent {
		return value{}, false
	}
	v, ok := f.names[o.Ident]
	return v, ok
}

// typeOf reports the .gvir type of an operand, when it has one independent of
// context. Literals do not.
func (f *fn) typeOf(o gvir.Operand) (gvir.Type, bool) {
	if v, ok := f.lookup(o); ok {
		return v.typ, true
	}
	if t, ok := f.constType[o.Ident]; ok && o.Kind == gvir.OperandIdent {
		return t, true
	}
	return nil, false
}

// lanes materializes o as one PTX operand per lane of type t. A literal is
// replicated across every lane; a scalar bound value used where a vector is
// expected is likewise replicated, which is what splat-shaped operands want.
func (f *fn) lanes(o gvir.Operand, t gvir.Type) ([]ptx.Operand, error) {
	n := laneCount(t)
	if o.Kind == gvir.OperandIdent {
		if v, ok := f.names[o.Ident]; ok {
			switch {
			case len(v.regs) == n:
				return regOperands(v.regs), nil
			case len(v.regs) == 1:
				return repeat(v.reg(), n), nil
			}
			return nil, todof("operand %q has %d lanes, %s wants %d", o.Ident, len(v.regs), t, n)
		}
		if imm, ok := f.constImm[o.Ident]; ok {
			return repeat(imm, n), nil
		}
		if v, ok := f.constVar[o.Ident]; ok {
			// An aggregate const: naming one yields its address.
			return repeat(v, n), nil
		}
		return nil, todof("unbound name %q", o.Ident)
	}
	imm, err := immOperand(o, gvir.ElemOrSelf(t))
	if err != nil {
		return nil, err
	}
	return repeat(imm, n), nil
}

// scalar materializes o as a single operand of type t.
func (f *fn) scalar(o gvir.Operand, t gvir.Type) (ptx.Operand, error) {
	ops, err := f.lanes(o, gvir.ElemOrSelf(t))
	if err != nil {
		return nil, err
	}
	return ops[0], nil
}

// reg materializes o into a register of type t, moving an immediate if need be.
// PTX accepts immediates in most source positions, so this is only for the
// instructions that insist on a register.
func (f *fn) reg(o gvir.Operand, t gvir.Type) (ptx.Reg, error) {
	if v, ok := f.lookup(o); ok && len(v.regs) == 1 {
		return v.reg(), nil
	}
	src, err := f.scalar(o, t)
	if err != nil {
		return ptx.Reg{}, err
	}
	if r, ok := src.(ptx.Reg); ok {
		return r, nil
	}
	rt, err := regType(gvir.ElemOrSelf(t))
	if err != nil {
		return ptx.Reg{}, err
	}
	d := f.tempReg(rt)
	f.b.Mov(rt, d, src)
	return d, nil
}

// pred materializes an i1 operand as a predicate register.
func (f *fn) pred(o gvir.Operand) (ptx.Reg, error) {
	if v, ok := f.lookup(o); ok {
		if v.regs[0].Type() != ptx.Pred {
			return ptx.Reg{}, todof("operand %q is %s, not i1", o.Ident, v.typ)
		}
		return v.reg(), nil
	}
	if o.Kind != gvir.OperandBool {
		return ptx.Reg{}, todof("operand %s is not an i1 value", o)
	}
	p := f.tempReg(ptx.Pred)
	n := int64(0)
	if o.Bool {
		n = 1
	}
	f.b.Mov(ptx.Pred, p, ptx.Imm(n))
	return p, nil
}

func repeat(o ptx.Operand, n int) []ptx.Operand {
	out := make([]ptx.Operand, n)
	for i := range out {
		out[i] = o
	}
	return out
}

// immOperand renders a literal as a PTX immediate of element type t.
func immOperand(o gvir.Operand, t gvir.Type) (ptx.Operand, error) {
	switch o.Kind {
	case gvir.OperandInt:
		return ptx.Imm(o.Int), nil
	case gvir.OperandBool:
		if o.Bool {
			return ptx.Imm(1), nil
		}
		return ptx.Imm(0), nil
	case gvir.OperandNull:
		return ptx.Imm(0), nil
	case gvir.OperandFloat:
		ft, ok := t.(gvir.FloatType)
		if !ok {
			return nil, todof("float literal used where %s is expected", t)
		}
		switch {
		case ft.Brain:
			return ptx.Imm(int64(bf16bits(o.Float))), nil
		case ft.Bits == 16:
			return ptx.Imm(int64(f16bits(o.Float))), nil
		case ft.Bits == 32:
			return ptx.F32Imm(float32(o.Float)), nil
		case ft.Bits == 64:
			return ptx.F64Imm(o.Float), nil
		}
	}
	return nil, todof("operand %s has no PTX immediate form", o)
}

// each applies emit once per lane of the instruction's result, with every
// argument resolved lane by lane. This is where vector scalarization happens:
// PTX has no vector ALU, so a vec[T,N] opcode is N scalar instructions.
func (f *fn) each(in *gvir.Instruction, emit func(d ptx.Reg, src []ptx.Operand) error) error {
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	src := make([][]ptx.Operand, len(in.Args))
	for i, a := range in.Args {
		src[i], err = f.lanes(a, in.Suffix)
		if err != nil {
			return err
		}
	}
	for k, d := range dst.regs {
		ops := make([]ptx.Operand, len(src))
		for i := range src {
			ops[i] = src[i][k]
		}
		if err := emit(d, ops); err != nil {
			return err
		}
	}
	if needsMask(in.Suffix) {
		return f.maskLanes(dst)
	}
	return nil
}

// maskLanes restores the zero-extension invariant after a wrapping i8 result.
func (f *fn) maskLanes(v value) error {
	for _, r := range v.regs {
		f.b.And(ptx.B16, r, r, ptx.Imm(0xff))
	}
	return nil
}

// signed returns o sign-extended to the register width, for the signed
// consumers of an i8 value. Every other width already fills its register.
func (f *fn) signed(o ptx.Operand, t gvir.Type) ptx.Operand {
	if valueBits(t) != 8 {
		return o
	}
	d := f.tempReg(ptx.S16)
	f.b.Cvt(ptx.S16, ptx.S8, d, o)
	return d
}

func (f *fn) errAt(in *gvir.Instruction, err error) error {
	if err == nil {
		return nil
	}
	if in.Result != "" {
		return fmt.Errorf("%s = %s: %w", in.Result, in.Op, err)
	}
	return fmt.Errorf("%s: %w", in.Op, err)
}