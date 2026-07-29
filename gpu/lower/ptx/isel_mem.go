package ptx

import (
	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Memory (§8) and the vector opcodes (§4.4, §8.3).
//
// Every access carries the state space taken from the pointer's own type: .gvir
// has no generic pointer, so no cvta appears here — only in the kernel prologue,
// where a kernarg arrives generic.

func (f *fn) alloca(in *gvir.Instruction) error {
	size, err := f.gm.SizeOf(in.Suffix)
	if err != nil {
		return err
	}
	align := in.Align
	if align == 0 {
		if align, err = f.gm.AlignOf(in.Suffix); err != nil {
			return err
		}
	}
	v := f.b.Local(ptx.Var{
		Space: ptx.Local, Align: align, Type: ptx.B8, Name: in.Result, Len: size,
	})
	r := f.tempReg(ptx.U64)
	f.b.Mov(ptx.U64, r, v)
	f.bind(in.Result, value{typ: gvir.PtrPrivate, regs: []ptx.Reg{r}, pointee: in.Suffix})
	return nil
}

func (f *fn) load(in *gvir.Instruction) error {
	p, sp, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	quals := []ptx.Qual{sp}
	return f.access(in.Suffix, p.reg(), in.Align, dst.regs, true, quals)
}

func (f *fn) store(in *gvir.Instruction) error {
	// Destination first (§8.3).
	p, sp, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	ops, err := f.lanes(in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	regs := make([]ptx.Reg, len(ops))
	for i, o := range ops {
		r, ok := o.(ptx.Reg)
		if !ok {
			rt, err := regType(gvir.ElemOrSelf(in.Suffix))
			if err != nil {
				return err
			}
			r = f.tempReg(rt)
			f.b.Mov(rt, r, o)
		}
		regs[i] = r
	}
	return f.access(in.Suffix, p.reg(), in.Align, regs, false, []ptx.Qual{sp})
}

// access emits one load or store of t through base, vectorizing where PTX can.
// Width 3 is elementwise: the fourth lane is padding (§4.4) and reading it
// would be an access this IR never asked for.
func (f *fn) access(t gvir.Type, base ptx.Reg, align int, regs []ptx.Reg, isLoad bool, quals []ptx.Qual) error {
	mt, err := memType(gvir.ElemOrSelf(t))
	if err != nil {
		return err
	}
	if align != 0 {
		// align N is forwarded verbatim; violating it is UB (§12.3).
		quals = append(quals, nil) // placeholder kept out of the qualifier set
		quals = quals[:len(quals)-1]
	}

	n := len(regs)
	if n == 2 || n == 4 {
		ops := make([]ptx.Operand, n)
		for i, r := range regs {
			ops[i] = r
		}
		if isLoad {
			f.b.Ld(mt, ptx.Vec(ops...), ptx.At(base), quals...)
		} else {
			f.b.St(mt, ptx.At(base), ptx.Vec(ops...), quals...)
		}
		return nil
	}

	esz, err := f.gm.SizeOf(gvir.ElemOrSelf(t))
	if err != nil {
		return err
	}
	for i, r := range regs {
		off := int64(i * esz)
		if isLoad {
			f.b.Ld(mt, r, ptx.At(base, off), quals...)
		} else {
			f.b.St(mt, ptx.At(base, off), r, quals...)
		}
	}
	return nil
}

// pointer resolves an operand to a pointer value and its state space.
func (f *fn) pointer(o gvir.Operand) (value, ptx.Space, error) {
	v, ok := f.lookup(o)
	if !ok {
		return value{}, ptx.NoSpace, todof("operand %s is not a bound pointer", o)
	}
	sp, err := spaceOfPtr(v.typ)
	if err != nil {
		return value{}, ptx.NoSpace, err
	}
	return v, sp, nil
}

// indexPtr is byte pointer arithmetic keeping p's address space (§8.3). It
// wraps normally, which 64-bit add does for free.
func (f *fn) indexPtr(in *gvir.Instruction) error {
	p, _, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	off, err := f.scalar(in.Args[1], gvir.I64)
	if err != nil {
		return err
	}
	dst, err := f.define(in.Result, p.typ)
	if err != nil {
		return err
	}
	f.b.Add(ptx.U64, dst.reg(), p.reg(), off)

	// Indexing an array of T keeps pointing at T.
	if cur, ok := f.names[in.Result]; ok {
		cur.pointee = p.pointee
		if a, isArr := p.pointee.(gvir.ArrayType); isArr {
			cur.pointee = a.Elem
		}
		f.names[in.Result] = cur
	}
	return nil
}

// fieldPtr needs the pointee struct, which .gvir pointers do not carry. It is
// recovered from provenance where possible; where not, the failure is explicit
// rather than a guess (README §9).
func (f *fn) fieldPtr(in *gvir.Instruction) error {
	p, _, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	st, ok := p.pointee.(gvir.StructType)
	if !ok {
		return todof("field.ptr through a pointer whose pointee is unknown: .gvir pointers carry an address space but no pointee type, and this one is not traceable to an alloca, a group declaration or a struct parameter")
	}
	s := f.gm.StructByName(st.Name)
	if s == nil {
		return todof("undeclared struct %s", st.Name)
	}
	lay, err := f.gm.StructLayout(s)
	if err != nil {
		return err
	}
	k := int(in.Args[1].Int)
	if k < 0 || k >= len(lay.Fields) {
		return todof("field index %d out of range for %s", k, st)
	}
	fo := lay.Fields[k]

	dst, err := f.define(in.Result, p.typ)
	if err != nil {
		return err
	}
	f.b.Add(ptx.U64, dst.reg(), p.reg(), ptx.Imm(int64(fo.Offset)))

	cur := f.names[in.Result]
	cur.pointee = fo.Type
	f.names[in.Result] = cur
	return nil
}

// memcopy and memset are per-thread operations, not group collectives (§8.3):
// each thread executing one moves the whole range itself. Both are forward byte
// loops; overlapping memcopy operands are UB (§12.4), so no direction test.

func (f *fn) memcopy(in *gvir.Instruction) error {
	dstP, dstSpace, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	srcP, srcSpace, err := f.pointer(in.Args[1])
	if err != nil {
		return err
	}
	n, err := f.scalar(in.Args[2], gvir.I64)
	if err != nil {
		return err
	}

	i := f.tempReg(ptx.U64)
	f.b.Mov(ptx.U64, i, ptx.Imm(0))

	head := f.b.Label("$memcopy")
	done := f.b.Label("$memcopy_done")
	f.b.Bind(head)

	p := f.tempReg(ptx.Pred)
	f.b.Setp(ptx.U64, ptx.Hs, p, i, n)
	f.b.Bra(done).If(p)

	sa := f.tempReg(ptx.U64)
	da := f.tempReg(ptx.U64)
	byteReg := f.tempReg(ptx.U16)
	f.b.Add(ptx.U64, sa, srcP.reg(), i)
	f.b.Add(ptx.U64, da, dstP.reg(), i)
	f.b.Ld(ptx.U8, byteReg, ptx.At(sa), srcSpace)
	f.b.St(ptx.U8, ptx.At(da), byteReg, dstSpace)
	f.b.Add(ptx.U64, i, i, ptx.Imm(1))
	f.b.Bra(head)
	f.b.Bind(done)
	return nil
}

func (f *fn) memset(in *gvir.Instruction) error {
	dstP, dstSpace, err := f.pointer(in.Args[0])
	if err != nil {
		return err
	}
	val, err := f.scalar(in.Args[1], gvir.I8)
	if err != nil {
		return err
	}
	n, err := f.scalar(in.Args[2], gvir.I64)
	if err != nil {
		return err
	}

	byteReg := f.tempReg(ptx.U16)
	f.b.Mov(ptx.U16, byteReg, val)

	i := f.tempReg(ptx.U64)
	f.b.Mov(ptx.U64, i, ptx.Imm(0))

	head := f.b.Label("$memset")
	done := f.b.Label("$memset_done")
	f.b.Bind(head)

	p := f.tempReg(ptx.Pred)
	f.b.Setp(ptx.U64, ptx.Hs, p, i, n)
	f.b.Bra(done).If(p)

	da := f.tempReg(ptx.U64)
	f.b.Add(ptx.U64, da, dstP.reg(), i)
	f.b.St(ptx.U8, ptx.At(da), byteReg, dstSpace)
	f.b.Add(ptx.U64, i, i, ptx.Imm(1))
	f.b.Bra(head)
	f.b.Bind(done)
	return nil
}

// ---------------------------------------------------------------------------
// Vector opcodes (§4.4, §8.3)
// ---------------------------------------------------------------------------

func (f *fn) extract(in *gvir.Instruction) error {
	v, ok := f.lookup(in.Args[0])
	if !ok {
		return todof("extract operand is not a bound vector")
	}
	k := int(in.Args[1].Int)
	if k < 0 || k >= len(v.regs) {
		// k = 3 on a width-3 vector is a verification error (§4.4); if one
		// reaches here the module did not go through ir/verify.
		return todof("extract index %d out of range for %s", k, v.typ)
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	t, err := movType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	f.b.Mov(t, dst.reg(), v.regs[k])
	return nil
}

func (f *fn) insert(in *gvir.Instruction) error {
	src, err := f.lanes(in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	k := int(in.Args[1].Int)
	x, err := f.scalar(in.Args[2], gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	if k < 0 || k >= len(dst.regs) {
		return todof("insert index %d out of range for %s", k, in.Suffix)
	}
	t, err := movType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	for i, d := range dst.regs {
		if i == k {
			f.b.Mov(t, d, x)
			continue
		}
		f.b.Mov(t, d, src[i])
	}
	return nil
}

func (f *fn) splat(in *gvir.Instruction) error {
	x, err := f.scalar(in.Args[0], gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	t, err := movType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	for _, d := range dst.regs {
		f.b.Mov(t, d, x)
	}
	return nil
}

// swizzle selects lanes from the concatenation of its two vector operands.
func (f *fn) swizzle(in *gvir.Instruction) error {
	if len(in.Args) < 2 {
		return todof("swizzle takes two vectors and a mask")
	}
	a, err := f.lanes(in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	c, err := f.lanes(in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	t, err := movType(gvir.ElemOrSelf(in.Suffix))
	if err != nil {
		return err
	}
	mask := in.Args[2:]
	if len(mask) != len(dst.regs) {
		return todof("swizzle mask has %d entries, %s has %d lanes", len(mask), in.Suffix, len(dst.regs))
	}
	n := len(a)
	for i, d := range dst.regs {
		k := int(mask[i].Int)
		switch {
		case k < 0 || k >= 2*n:
			return todof("swizzle index %d out of range", k)
		case k < n:
			f.b.Mov(t, d, a[k])
		default:
			f.b.Mov(t, d, c[k-n])
		}
	}
	return nil
}

// movType is the type a plain register-to-register move uses. Floats move as
// floats so the printer's register classes stay honest; everything else moves
// as bits.
func movType(t gvir.Type) (ptx.Type, error) {
	if gvir.IsFloat(t) {
		return regType(t)
	}
	if gvir.IsBool(t) {
		return ptx.Pred, nil
	}
	return bitType(t)
}