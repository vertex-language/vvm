// callable.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// fnLower carries the per-callable state: the emitted function, the value
// bindings, the block table the structurizer walks, and the builtin
// parameters §9 forces into the signature.
type fnLower struct {
	l    *lowerer
	fn   *msl.Function
	vals *values

	kernel   *gvir.Kernel
	blocks   map[string]*gvir.Block
	visited  map[string]bool
	builtins map[string]msl.Expr // attribute name -> parameter reference
	prologue []msl.Stmt
	temps    int
}

func (l *lowerer) newFn(fn *msl.Function) *fnLower {
	return &fnLower{
		l: l, fn: fn,
		vals:     newValues(),
		blocks:   map[string]*gvir.Block{},
		visited:  map[string]bool{},
		builtins: map[string]msl.Expr{},
	}
}

func (f *fnLower) temp(prefix string) string {
	f.temps++
	return fmt.Sprintf("vv_%s%d", prefix, f.temps)
}

// ---------------------------------------------------------------------------
// Funcs (§6.4)
// ---------------------------------------------------------------------------

// lowerFunc emits a device-side helper. Direct calls only, so there is no
// declaration/definition split and no forward declaration: §2 requires
// declare-before-use, which is exactly MSL's own rule.
func (l *lowerer) lowerFunc(src *gvir.Func) error {
	fn := msl.NewFunction(l.names.ident(src.Name))
	ret, err := l.mapType(src.Ret)
	if err != nil {
		return fmt.Errorf("lower/msl: func %s return type: %w", src.Name, err)
	}
	fn.Ret = ret

	f := l.newFn(fn)
	for _, p := range src.Params {
		mt, err := l.mapType(p.Type)
		if err != nil {
			return fmt.Errorf("lower/msl: func %s param %s: %w", src.Name, p.Name, err)
		}
		ref := fn.Param(l.names.ident(p.Name), mt)
		f.vals.bind(p.Name, p.Type, mt, ref, nil)
	}

	if err := f.body(&src.Body); err != nil {
		return fmt.Errorf("lower/msl: func %s: %w", src.Name, err)
	}
	l.out.Add(fn)
	return nil
}

// ---------------------------------------------------------------------------
// Kernels (§6.1)
// ---------------------------------------------------------------------------

func (l *lowerer) lowerKernel(k *gvir.Kernel) error {
	fn := msl.NewKernel(l.names.ident(k.Name))
	f := l.newFn(fn)
	f.kernel = k

	if err := f.kernargs(k); err != nil {
		return fmt.Errorf("lower/msl: kernel %s: %w", k.Name, err)
	}
	if err := f.groupMemory(k); err != nil {
		return fmt.Errorf("lower/msl: kernel %s: %w", k.Name, err)
	}
	if err := f.threadgroupBound(k); err != nil {
		return fmt.Errorf("lower/msl: kernel %s: %w", k.Name, err)
	}
	if err := f.body(&k.Body); err != nil {
		return fmt.Errorf("lower/msl: kernel %s: %w", k.Name, err)
	}
	l.out.Add(fn)
	return nil
}

// kernargs emits the §6.3 packed argument buffer as a struct whose offsets are
// defined by that section rather than by Metal's own struct rules, and binds
// each parameter to its field. The buffer sits at [[buffer(0)]] and requires
// Argument Buffers Tier 2; [[id(n)]] carries the §6.2 parameter index, which
// is the portable identity of a parameter and therefore the right thing for
// the encoder to see.
func (f *fnLower) kernargs(k *gvir.Kernel) error {
	want, err := f.l.src.KernargLayout(k)
	if err != nil {
		return err
	}
	if len(k.Params) == 0 {
		return nil
	}

	name := f.l.names.fresh("args_t")
	s := msl.NewStruct(name)
	handles := make([]*msl.Field, len(k.Params))

	off, pads := 0, 0
	for i, p := range want.Params {
		ft, err := f.l.mapType(p.Type)
		if err != nil {
			return fmt.Errorf("param %d (%s): %w", i, p.Name, err)
		}
		size, align, err := f.l.mslSizeAlign(p.Type)
		if err != nil {
			return fmt.Errorf("param %d (%s): %w", i, p.Name, err)
		}
		off = alignUp(off, align)
		if off > p.Offset {
			return fmt.Errorf("param %d (%s): MSL places it at byte %d, §6.3 requires %d",
				i, p.Name, off, p.Offset)
		}
		if off < p.Offset {
			s.Field(fmt.Sprintf("vv_pad%d", pads), msl.Array(msl.UChar, p.Offset-off))
			pads++
			off = p.Offset
		}
		handles[i] = s.Field(f.l.names.ident(p.Name), ft, msl.ID(i))
		off += size
	}
	if off < want.Size {
		s.Field(fmt.Sprintf("vv_pad%d", pads), msl.Array(msl.UChar, want.Size-off))
	}
	f.l.out.Add(s)

	argsName := f.l.names.fresh("args")
	args := f.fn.Param(argsName, msl.Ref(msl.Constant, s.Type()), msl.Buffer(0))

	for i, p := range k.Params {
		mt, err := f.l.mapType(p.Type)
		if err != nil {
			return err
		}
		if st, ok := p.Type.(gvir.StructType); ok {
			// §6.2 permits a struct by value; §4.7 forbids holding an
			// aggregate in a named value. The name therefore binds to a
			// thread-space copy, whose pointee is known, exactly as field.ptr
			// on a struct parameter needs.
			local := f.temp("param")
			f.prologue = append(f.prologue,
				&msl.VarDecl{Type: mt, Name: local, Init: args.Fld(handles[i])})
			bp, err := bytePtr(gvir.SpacePrivate)
			if err != nil {
				return err
			}
			f.vals.bind(p.Name, gvir.PtrPrivate, bp,
				msl.Call("vv_bytes", msl.Name(local).Addr()), st)
			continue
		}
		f.vals.bind(p.Name, p.Type, mt, args.Fld(handles[i]), nil)
	}
	return nil
}

// groupMemory declares the kernel's static `group` objects as threadgroup
// locals and its one `dynamic_group` as a threadgroup parameter, which is what
// §8.2 says the msl realization is. Each name binds to the object's address as
// a byte pointer; nothing in the IR ever names the object itself.
func (f *fnLower) groupMemory(k *gvir.Kernel) error {
	total := 0
	for _, g := range k.Groups {
		size, align, err := f.l.mslSizeAlign(g.Type)
		if err != nil {
			return fmt.Errorf("group %s: %w", g.Name, err)
		}
		if g.Align > 0 {
			if !gvir.ValidAlign(g.Align) {
				return fmt.Errorf("group %s: align %d is not a power of two in [1,1024] (§2)", g.Name, g.Align)
			}
			align = g.Align
		}
		st, err := storageType(size, align)
		if err != nil {
			return fmt.Errorf("group %s: %w", g.Name, err)
		}
		name := f.l.names.ident(g.Name)
		obj := f.temp("tg")
		f.prologue = append(f.prologue, &msl.VarDecl{
			Space: msl.Threadgroup, Type: st, Name: obj,
			Comment: fmt.Sprintf("group %s: %d bytes, align %d", g.Name, alignUp(size, align), align),
		})
		bp, err := bytePtr(gvir.SpaceGroup)
		if err != nil {
			return err
		}
		f.vals.bind(g.Name, gvir.PtrGroup, bp, bytesOf(msl.Name(obj)), g.Type)
		_ = name
		total = alignUp(total, align) + alignUp(size, align)
	}

	if limit := gvir.StaticGroupLimit(gvir.BackendMSL); total > limit {
		return fmt.Errorf("static group footprint is %d bytes, over the msl budget of %d (§6.5)", total, limit)
	}

	if dg := k.DynamicGroup; dg != nil {
		bp, err := bytePtr(gvir.SpaceGroup)
		if err != nil {
			return err
		}
		ref := f.fn.Param(f.l.names.ident(dg.Name), bp, msl.ThreadgroupSlot(0))
		f.vals.bind(dg.Name, gvir.PtrGroup, bp, ref, nil)
	}
	return nil
}

// threadgroupBound realizes §6.1's note that on msl `group_size` lowers to a
// thread-count bound only: the exact shape stays a host-checked contract the
// generated launcher enforces before dispatch.
func (f *fnLower) threadgroupBound(k *gvir.Kernel) error {
	n := 0
	if k.GroupSize != nil {
		n = k.GroupSize.Threads()
	}
	if k.MaxGroupSize > 0 && (n == 0 || k.MaxGroupSize < n) {
		n = k.MaxGroupSize
	}
	if n > 0 {
		f.fn.Attr(msl.MaxTotalThreadsPerThreadgroup(n))
	}
	if k.GroupSize != nil && f.l.opt.Comments {
		f.fn.Attr(msl.RawAttr("//")) // placeholder never emitted; see note below
		f.fn.Attrs = f.fn.Attrs[:len(f.fn.Attrs)-1]
		f.prologue = append(f.prologue, &msl.Comment{Text: fmt.Sprintf(
			"group_size %d,%d,%d is enforced by the launcher (§6.1)",
			k.GroupSize.X, k.GroupSize.Y, k.GroupSize.Z)})
	}
	// §9.2: subgroup_size is not expressible in MSL and is gated (§4.3), so a
	// kernel carrying it never reaches this point.
	if k.SubgroupSize != 0 {
		return fmt.Errorf("subgroup_size is unavailable on msl (§9.2) — gating should have excluded this kernel")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Bodies
// ---------------------------------------------------------------------------

// body structurizes the annotated CFG into the function's block and then
// prepends the prologue: threadgroup objects and parameter copies first, then
// one declaration per §7.3 value binding.
func (f *fnLower) body(src *gvir.Body) error {
	if err := f.emitBody(f.fn.Body, src); err != nil {
		return err
	}
	head := append([]msl.Stmt{}, f.prologue...)
	head = append(head, f.vals.declarations()...)
	if len(head) > 0 {
		f.fn.Body.InsertBefore(0, head...)
	}
	return nil
}