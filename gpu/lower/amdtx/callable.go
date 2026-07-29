// callable.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// bodyLowerer holds the per-body state: one kernel or one func.
type bodyLowerer struct {
	l *lowerer

	kernel *gvir.Kernel // exactly one of kernel and fn is non-nil
	fn     *gvir.Func
	src    *gvir.Body
	out    *amdtx.Body
	regs   *amdtx.RegFile

	vals map[string]*value
	uni  *uniformity
	ntmp int

	groupBytes  int // running group-segment offset
	privBytes   int // running private-segment offset
	retVal      *value
	divergent   int // depth of divergent regions, for the early-return check
	wgConst     [3]int
	wgConstOK   bool
}

func (l *lowerer) newBody(out *amdtx.Body) *bodyLowerer {
	return &bodyLowerer{l: l, out: out, regs: out.Regs, vals: map[string]*value{}}
}

// ---- Kernels --------------------------------------------------------------

func (l *lowerer) kernel(k *gvir.Kernel) error {
	out := amdtx.NewKernel(k.Name)
	out.Linkage = amdtx.Visible

	layout, err := l.src.KernargLayout(k)
	if err != nil {
		return err
	}
	if err := l.declareParams(out, layout); err != nil {
		return err
	}
	out.Launch.KernargSize = layout.Size
	out.Launch.KernargAlign = layout.Align
	if err := checkKernargAgreement(out, layout); err != nil {
		return err
	}

	if k.GroupSize != nil {
		out.Launch.ReqdWorkgroupSize = amdtx.Dim3{k.GroupSize.X, k.GroupSize.Y, k.GroupSize.Z}
		out.Launch.MaxFlatWorkgroupSize = k.GroupSize.Threads()
	}
	if k.MaxGroupSize != 0 {
		out.Launch.MaxFlatWorkgroupSize = k.MaxGroupSize
	}
	if k.DynamicGroup != nil {
		out.Launch.DynamicGroupSegment = true
	}

	b := l.newBody(out.Body)
	b.kernel = k
	b.src = &k.Body
	if k.GroupSize != nil {
		b.wgConst = [3]int{k.GroupSize.X, k.GroupSize.Y, k.GroupSize.Z}
		b.wgConstOK = true
	}

	if err := b.prologue(out, layout); err != nil {
		return err
	}
	if err := b.run(); err != nil {
		return err
	}

	out.Launch.GroupSegmentSize = b.groupBytes
	out.Launch.PrivateSegmentSize = b.privBytes
	l.out.Add(out)
	return nil
}

// declareParams turns the packed kernarg layout into .param declarations.
//
// AMDTX .param widths are multiples of 32 (§5) and §19.2 defers aggregate
// kernarg layout to v1.1, so the parameter forms .gvir permits are wider than
// the ones AMDTX 1.0 can spell. Refusing them is better than declaring a .b32
// for an i8 and quietly shifting every offset behind it: §6.3 requires the
// buffer to be byte-identical across backends, and that is the promise at
// stake.
func (l *lowerer) declareParams(out *amdtx.Kernel, layout gvir.KernargLayout) error {
	for _, p := range layout.Params {
		if gvir.IsAggregate(p.Type) {
			return fmt.Errorf("parameter %s is a struct; AMDTX 1.0 leaves aggregate kernarg layout "+
				"unspecified (§19.2) and this backend will not guess it", p.Name)
		}
		if p.Size%4 != 0 {
			return fmt.Errorf("parameter %s (%s) occupies %d bytes; AMDTX .param widths are multiples "+
				"of 32 bits (§5), and widening it would change the §6.3 packed offsets", p.Name, p.Type, p.Size)
		}
		w := amdtx.Width(p.Size * 8)
		if !w.IsValid() {
			return fmt.Errorf("parameter %s occupies %d bytes, which is not a legal AMDTX tuple size (V13)", p.Name, p.Size)
		}
		if sp, ok := spaceOf(p.Type); ok {
			as, err := amdSpace(sp)
			if err != nil {
				return err
			}
			out.ParamPtr(p.Name, as, amdtx.ReadWrite)
			continue
		}
		out.Param(p.Name, w)
	}
	return nil
}

// checkKernargAgreement compares the layout .gvir computed with the one AMDTX
// derives from the declared widths. They are two independent derivations of
// the same §6.3 buffer; a divergence is a lowering error rather than a
// silently different artifact, because "byte-identical across all three
// backends" is checked by comparing buffers (§13 "Layout").
func checkKernargAgreement(out *amdtx.Kernel, layout gvir.KernargLayout) error {
	slots := out.KernargLayout()
	if len(slots) != len(layout.Params) {
		return fmt.Errorf("kernarg layouts disagree: %d AMDTX slots against %d .gvir parameters",
			len(slots), len(layout.Params))
	}
	for i, s := range slots {
		p := layout.Params[i]
		if s.Offset != p.Offset || s.Size != p.Size {
			return fmt.Errorf("kernarg layouts disagree at parameter %s: .gvir places it at +%d (%d bytes), "+
				"AMDTX at +%d (%d bytes)", p.Name, p.Offset, p.Size, s.Offset, s.Size)
		}
	}
	return nil
}

// prologue loads the kernarg buffer and materializes the addresses of the
// group segment. Kernargs are not operands: a kernel reads them through
// %kernarg_ptr at the offset the layout assigns, so every parameter costs an
// s_load and a move into its VGPR.
func (b *bodyLowerer) prologue(out *amdtx.Kernel, layout gvir.KernargLayout) error {
	o := b.out
	type staged struct {
		v   *value
		sgp amdtx.Reg
		p   gvir.KernargParam
	}
	var stage []staged

	for _, p := range layout.Params {
		v, err := b.define(p.Name, p.Type)
		if err != nil {
			return err
		}
		if sp, ok := spaceOf(p.Type); ok {
			v.space = sp
		}
		w := amdtx.Width(p.Size * 8)
		s := b.regs.New(amdtx.Sgpr(w), "karg."+p.Name)
		o.SLoad(s, amdtx.At(amdtx.KernargPtr, int64(p.Offset)))
		stage = append(stage, staged{v: v, sgp: s, p: p})
	}
	if len(stage) > 0 {
		o.Waitcnt(amdtx.LGKM(0))
	}
	for _, st := range stage {
		n := st.p.Size / 4
		lanes := laneCount(st.p.Type)
		per := n / lanes
		for lane := 0; lane < lanes; lane++ {
			for d := 0; d < per; d++ {
				src := st.sgp
				if n > 1 {
					src = st.sgp.Dword(lane*per + d)
				}
				o.Inst("v_mov_b32", dword(st.v.regs[lane], d, per), src)
			}
		}
	}

	// group declarations are byte offsets into the group segment; naming one
	// in an operand position yields its address (§8.2).
	for _, g := range b.kernel.Groups {
		size, err := b.l.src.SizeOf(g.Type)
		if err != nil {
			return fmt.Errorf("group %s: %w", g.Name, err)
		}
		align, err := b.l.src.AlignOf(g.Type)
		if err != nil {
			return err
		}
		if g.Align > 0 {
			align = g.Align
		}
		b.groupBytes = alignUp(b.groupBytes, align)
		if err := b.bindAddress(g.Name, gvir.PtrGroup, b.groupBytes, g.Type); err != nil {
			return err
		}
		b.groupBytes += size
	}
	if d := b.kernel.DynamicGroup; d != nil {
		align := d.Align
		if align <= 0 {
			align = 16
		}
		b.groupBytes = alignUp(b.groupBytes, align)
		// The dynamic allocation follows the static ones; its size is a
		// launch-time quantity and is deliberately not counted here.
		if err := b.bindAddress(d.Name, gvir.PtrGroup, b.groupBytes, nil); err != nil {
			return err
		}
	}
	return nil
}

// bindAddress materializes a constant segment offset as a pointer value.
func (b *bodyLowerer) bindAddress(name string, t gvir.Type, off int, pointee gvir.Type) error {
	v, err := b.define(name, t)
	if err != nil {
		return err
	}
	v.pointee = pointee
	b.out.Inst("v_mov_b32", v.regs[0].Dword(0), amdtx.Imm(int64(off)))
	b.out.Inst("v_mov_b32", v.regs[0].Dword(1), amdtx.Imm(0))
	return nil
}

// ---- Functions ------------------------------------------------------------

// function lowers a device function.
//
// AMDTX 1.0 defines no call ABI and `ret` takes no operand (§3.2), so a
// value-returning func gains a synthetic trailing formal parameter carrying
// the result. Every call site allocates the register and reads it after the
// call; since lowering inlines every call, the convention is exact rather than
// an ABI in disguise.
func (l *lowerer) function(f *gvir.Func) error {
	out := l.funcs[f.Name]
	b := l.newBody(out.Body)
	b.fn = f
	b.src = &f.Body

	for _, p := range f.Params {
		c, err := laneClass(p.Type)
		if err != nil {
			return fmt.Errorf("param %s: %w", p.Name, err)
		}
		n := laneCount(p.Type)
		regs := make([]amdtx.Reg, n)
		for i := 0; i < n; i++ {
			name := p.Name
			if n > 1 {
				name = fmt.Sprintf("%s.%d", p.Name, i)
			}
			regs[i] = out.Param(c, name)
		}
		v := &value{name: p.Name, typ: p.Type, regs: regs}
		if sp, ok := spaceOf(p.Type); ok {
			v.space = sp
		}
		b.vals[p.Name] = v
	}
	if !gvir.IsVoid(f.Ret) {
		c, err := laneClass(f.Ret)
		if err != nil {
			return err
		}
		n := laneCount(f.Ret)
		regs := make([]amdtx.Reg, n)
		for i := 0; i < n; i++ {
			name := "$ret"
			if n > 1 {
				name = fmt.Sprintf("$ret.%d", i)
			}
			regs[i] = out.Param(c, name)
		}
		b.retVal = &value{name: "$ret", typ: f.Ret, regs: regs}
	}
	return b.run()
}

// run structurizes, analyzes and emits one body.
func (b *bodyLowerer) run() error {
	root, err := structurize(b.src)
	if err != nil {
		return err
	}
	b.uni = b.analyze(root)
	if err := b.emitSeq(b.out, root); err != nil {
		return err
	}
	b.terminate(b.out)
	return nil
}

// terminate appends the required terminator if the body does not already end
// in one. V23 and V24 are conservative — a body whose last statement is not a
// terminator is rejected even when a branch makes it unreachable — so the
// cheapest way to satisfy them is to always end in one.
func (b *bodyLowerer) terminate(o *amdtx.Body) {
	if n := o.Len(); n > 0 {
		if in := o.InstrAt(n - 1); in != nil {
			if b.kernel != nil && in.Op == amdtx.OpEndPgm {
				return
			}
			if b.kernel == nil && in.Op == amdtx.OpRet {
				return
			}
		}
	}
	if b.kernel != nil {
		o.EndPgm()
		return
	}
	o.Ret()
}

func alignUp(v, a int) int {
	if a <= 1 {
		return v
	}
	if r := v % a; r != 0 {
		return v + a - r
	}
	return v
}