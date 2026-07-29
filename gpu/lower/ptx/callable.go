package ptx

import (
	"fmt"

	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Kernel and func lowering: the prologue that turns .param, .shared and .local
// declarations into bound .gvir names, plus the tuning directives.

// ---------------------------------------------------------------------------
// Kernels (§6.1)
// ---------------------------------------------------------------------------

func (l *lowerer) lowerKernel(k *gvir.Kernel) error {
	pk := ptx.NewKernel(k.Name)
	pk.Linkage = ptx.Visible

	if k.GroupSize != nil {
		pk.ReqNTid = ptx.Dim3{k.GroupSize.X, k.GroupSize.Y, k.GroupSize.Z}
	}
	if k.MaxGroupSize != 0 {
		// max_group_size bounds X*Y*Z; .maxntid takes three dimensions whose
		// product is the bound, so the total goes in the x slot.
		pk.MaxNTid = ptx.Dim3{k.MaxGroupSize, 1, 1}
	}

	f := l.newFn(pk.Body)
	f.kernel = k

	if err := f.kernelParams(pk, k); err != nil {
		return err
	}
	if err := f.groupDecls(k); err != nil {
		return err
	}
	if err := f.lowerBody(&k.Body); err != nil {
		return err
	}
	l.pm.Add(pk)
	return nil
}

// kernelParams declares the kernarg list and binds each parameter name.
//
// The declaration order and natural alignment §6.3 pins are exactly PTX's own
// .param layout rules, so the buffer is byte-identical without this package
// doing anything special — the layout is computed here only to size struct
// parameters and to fail loudly if it ever disagrees.
func (f *fn) kernelParams(pk *ptx.Kernel, k *gvir.Kernel) error {
	lay, err := f.gm.KernargLayout(k)
	if err != nil {
		return err
	}
	for i, p := range k.Params {
		info := lay.Params[i]
		switch t := p.Type.(type) {
		case gvir.PtrType:
			sp, err := space(t.Space)
			if err != nil {
				return err
			}
			par := pk.ParamPtr(p.Name, ptx.U64, sp, 0)
			r := f.tempReg(ptx.U64)
			f.b.Ld(ptx.U64, r, ptx.At(par), ptx.ParamSpace)
			// The argument arrives as a generic address; .gvir has no generic
			// pointer, so convert once here and never again (§5).
			f.b.Cvta(ptx.U64, r, r, ptx.To, sp)
			f.bind(p.Name, value{typ: p.Type, regs: []ptx.Reg{r}})

		case gvir.VecType:
			par := pk.ParamArray(p.Name, info.Align, info.Size)
			v, err := f.newValue(p.Type)
			if err != nil {
				return err
			}
			mt, err := memType(t.Elem)
			if err != nil {
				return err
			}
			esz, err := f.gm.SizeOf(t.Elem)
			if err != nil {
				return err
			}
			for lane, r := range v.regs {
				f.b.Ld(mt, r, ptx.At(par, int64(lane*esz)), ptx.ParamSpace)
			}
			f.bind(p.Name, v)

		case gvir.StructType:
			// §4.7 keeps aggregates out of named values, so a by-value struct
			// argument is copied into .local and the name binds to that
			// address. field.ptr then works and the pointee is known.
			par := pk.ParamArray(p.Name, info.Align, info.Size)
			local := f.b.Local(ptx.Var{
				Space: ptx.Local, Align: info.Align, Type: ptx.B8,
				Name: p.Name + "$buf", Len: info.Size,
			})
			base := f.tempReg(ptx.U64)
			f.b.Mov(ptx.U64, base, local)
			if err := f.copyParamToLocal(par, base, info.Size, info.Align); err != nil {
				return err
			}
			f.bind(p.Name, value{
				typ:     gvir.PtrPrivate,
				regs:    []ptx.Reg{base},
				pointee: p.Type,
			})

		default:
			rt, err := regType(p.Type)
			if err != nil {
				return err
			}
			mt, err := memType(p.Type)
			if err != nil {
				return err
			}
			par := pk.Param(p.Name, mt)
			r := f.tempReg(rt)
			f.b.Ld(mt, r, ptx.At(par), ptx.ParamSpace)
			f.bind(p.Name, value{typ: p.Type, regs: []ptx.Reg{r}})
		}
	}
	return nil
}

// copyParamToLocal moves a by-value struct argument out of .param space, in the
// widest chunks its alignment permits.
func (f *fn) copyParamToLocal(par *ptx.Param, base ptx.Reg, size, align int) error {
	chunk := 1
	switch {
	case align%8 == 0 && size%8 == 0:
		chunk = 8
	case align%4 == 0 && size%4 == 0:
		chunk = 4
	case align%2 == 0 && size%2 == 0:
		chunk = 2
	}
	var t ptx.Type
	switch chunk {
	case 8:
		t = ptx.B64
	case 4:
		t = ptx.B32
	case 2:
		t = ptx.B16
	default:
		t = ptx.B8
	}
	scratch := f.tempReg(mustRegForBits(chunk * 8))
	for off := 0; off < size; off += chunk {
		f.b.Ld(t, scratch, ptx.At(par, int64(off)), ptx.ParamSpace)
		f.b.St(t, ptx.At(base, int64(off)), scratch, ptx.Local)
	}
	return nil
}

func mustRegForBits(bits int) ptx.Type {
	switch bits {
	case 8, 16:
		return ptx.U16
	case 32:
		return ptx.U32
	}
	return ptx.U64
}

// groupDecls declares the kernel's static group memory and its one dynamic
// allocation, binding each name to a ptr[group] holding its address.
func (f *fn) groupDecls(k *gvir.Kernel) error {
	for _, g := range k.Groups {
		size, err := f.gm.SizeOf(g.Type)
		if err != nil {
			return todof("group %s: %w", g.Name, err)
		}
		align := g.Align
		if align == 0 {
			if align, err = f.gm.AlignOf(g.Type); err != nil {
				return todof("group %s: %w", g.Name, err)
			}
		}
		v := f.b.Local(ptx.Var{
			Space: ptx.Shared, Align: align, Type: ptx.B8, Name: g.Name, Len: size,
		})
		r := f.tempReg(ptx.U64)
		f.b.Mov(ptx.U64, r, v)
		// Zero-initialization is not guaranteed (§8.2); nothing is emitted.
		f.bind(g.Name, value{typ: gvir.PtrGroup, regs: []ptx.Reg{r}, pointee: g.Type})
	}

	if k.DynamicGroup != nil {
		if f.dynSmem == nil {
			return todof("kernel %s declares dynamic_group but no extern shared array was created", k.Name)
		}
		r := f.tempReg(ptx.U64)
		f.b.Mov(ptx.U64, r, f.dynSmem)
		f.bind(k.DynamicGroup.Name, value{typ: gvir.PtrGroup, regs: []ptx.Reg{r}})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Funcs (§6.4)
// ---------------------------------------------------------------------------

func (l *lowerer) lowerFunc(g *gvir.Func) error {
	pf := ptx.NewFunc(g.Name)

	f := l.newFn(pf.Body)
	f.fdecl = g
	f.retType = g.Ret

	if !gvir.IsVoid(g.Ret) {
		mt, err := memType(gvir.ElemOrSelf(g.Ret))
		if err != nil {
			return err
		}
		for lane := 0; lane < laneCount(g.Ret); lane++ {
			f.retPar = append(f.retPar, pf.Return(retName(lane), mt))
		}
	}

	// Parameters are declared before the body reads them; a vector parameter
	// becomes one .param per lane, since PTX has no vector value.
	type binding struct {
		name  string
		typ   gvir.Type
		pars  []*ptx.Param
	}
	var bindings []binding
	for _, p := range g.Params {
		mt, err := memType(gvir.ElemOrSelf(p.Type))
		if err != nil {
			return err
		}
		n := laneCount(p.Type)
		bd := binding{name: p.Name, typ: p.Type}
		for lane := 0; lane < n; lane++ {
			bd.pars = append(bd.pars, pf.Param(paramName(p.Name, lane, n), mt))
		}
		bindings = append(bindings, bd)
	}
	for _, bd := range bindings {
		v, err := f.newValue(bd.typ)
		if err != nil {
			return err
		}
		mt, err := memType(gvir.ElemOrSelf(bd.typ))
		if err != nil {
			return err
		}
		for lane, r := range v.regs {
			f.b.Ld(mt, r, ptx.At(bd.pars[lane]), ptx.ParamSpace)
		}
		f.bind(bd.name, v)
	}

	// `readonly` (§6.4) has no PTX expression: violating it is UB (§12.8) and
	// ptxas has no attribute to record the promise against.
	if g.Readonly && l.opts.Comments {
		pf.Pragmas = append(pf.Pragmas, "gvir readonly")
	}

	if err := f.lowerBody(&g.Body); err != nil {
		return err
	}
	l.pm.Add(pf)
	l.funcs[g.Name] = pf
	return nil
}

func retName(lane int) string { return fmt.Sprintf("retval%d", lane) }

func paramName(name string, lane, n int) string {
	if n == 1 {
		return name
	}
	return fmt.Sprintf("%s$%d", name, lane)
}