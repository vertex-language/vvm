// callable.go
package msl

import (
	"fmt"
	"strconv"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// lowerKernel emits one dispatchable entry point (§6.1).
func (l *lowerer) lowerKernel(k *gvir.Kernel) error {
	fn := msl.NewKernel(k.Name)
	f := newFn(l, fn, &k.Body, k)
	prologue := msl.NewBlock()

	if len(k.Params) > 0 {
		if err := l.argBuffer(f, k, prologue); err != nil {
			return err
		}
	}

	// §6.1: on msl, group_size lowers to a thread-count bound only; the exact
	// shape is the launcher's contract, enforced before dispatch.
	switch {
	case k.GroupSize != nil:
		fn.Attr(msl.MaxTotalThreadsPerThreadgroup(k.GroupSize.Threads()))
	case k.MaxGroupSize > 0:
		fn.Attr(msl.MaxTotalThreadsPerThreadgroup(k.MaxGroupSize))
	}

	if dg := k.DynamicGroup; dg != nil {
		p := fn.Param(prefix+"dyn", msl.Ptr(msl.Threadgroup, msl.UChar), msl.ThreadgroupSlot(0))
		b, err := f.defineParam(dg.Name, gvir.PtrGroup)
		if err != nil {
			return err
		}
		b.mname = prefix + "dyn" // the parameter is the binding; no copy needed
		_ = p
	}

	for _, g := range k.Groups {
		if err := f.groupVar(prologue, g); err != nil {
			return fmt.Errorf("group %s: %w", g.Name, err)
		}
	}

	body := msl.NewBlock()
	if err := f.lowerBody(body); err != nil {
		return err
	}
	fn.Body = f.assemble(prologue, body)
	l.out.Add(fn)
	return nil
}

// argBuffer realizes §6.3: a single argument buffer at [[buffer(0)]] whose
// offsets and padding are defined by the specification, not by Metal's struct
// rules. KernargLayout is the one derivation the launcher generator, ir/verify
// and the differential suite also use.
func (l *lowerer) argBuffer(f *fnLowerer, k *gvir.Kernel, prologue *msl.Block) error {
	layout, err := l.src.KernargLayout(k)
	if err != nil {
		return err
	}
	name := l.unique(k.Name + "_args")
	st := msl.NewStruct(name)

	fields := map[string]*msl.Field{}
	off := 0
	for _, p := range layout.Params {
		if p.Offset > off {
			st.Field(fmt.Sprintf("_pad_%d", off), msl.Array(msl.UChar, p.Offset-off))
		}
		t, err := l.typeOf(p.Type)
		if err != nil {
			return fmt.Errorf("param %d (%s): %w", p.Index, p.Name, err)
		}
		fields[p.Name] = st.Field(p.Name, t)
		off = p.Offset + p.Size
	}
	if layout.Size > off {
		st.Field(fmt.Sprintf("_pad_%d", off), msl.Array(msl.UChar, layout.Size-off))
	}
	l.out.Add(&msl.CommentDecl{Text: fmt.Sprintf(
		"§6.3 packed kernarg layout: %d bytes, %d-byte aligned", layout.Size, layout.Align)})
	l.out.Add(st)

	args := f.fn.Param(argsParam, msl.Ref(msl.Constant, msl.Named(name)), msl.Buffer(0))

	// §7.3 counts parameters as entry-block assignments, and a parameter name
	// may be reassigned, so each one becomes a local initialized in the
	// prologue rather than an alias for the argument-buffer field.
	for _, p := range layout.Params {
		field := args.Fld(fields[p.Name])
		if _, isStruct := p.Type.(gvir.StructType); isStruct {
			// §4.7: an aggregate is never held in a named value. Copy it into
			// thread space and bind the name to a pointer at it, so field.ptr
			// works and the pointee is known.
			storage := f.storageName(p.Name)
			mt, err := l.typeOf(p.Type)
			if err != nil {
				return err
			}
			f.decls = append(f.decls, &msl.VarDecl{Type: mt, Name: storage})
			prologue.Assign(msl.Name(storage), field)
			b, err := f.define(p.Name, gvir.PtrPrivate)
			if err != nil {
				return err
			}
			b.pointee = p.Type
			prologue.Assign(b.ref(), bytePtr(msl.Thread, msl.Name(storage).Addr()))
			continue
		}
		b, err := f.define(p.Name, p.Type)
		if err != nil {
			return err
		}
		prologue.Assign(b.ref(), field)
	}
	return nil
}

// groupVar declares one statically sized group allocation (§8.2). Zero
// initialization is not emitted: §8.2 does not guarantee it.
func (f *fnLowerer) groupVar(prologue *msl.Block, g *gvir.GroupVar) error {
	mt, err := f.l.typeOf(g.Type)
	if err != nil {
		return err
	}
	storage := f.storageName(g.Name)
	decl := &msl.VarDecl{Space: msl.Threadgroup, Type: mt, Name: storage}
	if g.Align > 0 {
		if !gvir.ValidAlign(g.Align) {
			return fmt.Errorf("align %d is not a power of two in [1,1024] (§2)", g.Align)
		}
		// MSL has no typed alignas node; RawAttr is the escape hatch.
		decl.Attrs = append(decl.Attrs, msl.RawAttr("gnu::aligned", strconv.Itoa(g.Align)))
	}
	f.decls = append(f.decls, decl)

	b, err := f.define(g.Name, gvir.PtrGroup)
	if err != nil {
		return err
	}
	b.pointee = g.Type
	prologue.Assign(b.ref(), bytePtr(msl.Threadgroup, addressOf(g.Type, msl.Name(storage))))
	return nil
}

// lowerFunc emits a device-side helper (§6.4). There is no inline/noinline
// attribute because inlining is neither observable nor controllable.
func (l *lowerer) lowerFunc(fd *gvir.Func) error {
	fn := msl.NewFunction(fd.Name)
	if !gvir.IsVoid(fd.Ret) {
		ret, err := l.typeOf(fd.Ret)
		if err != nil {
			return err
		}
		fn.Ret = ret
	}
	f := newFn(l, fn, &fd.Body, nil)

	for _, p := range fd.Params {
		if !gvir.IsFuncParamType(p.Type) {
			return fmt.Errorf("parameter %s: %s is not a value type (§2)", p.Name, p.Type)
		}
		b, err := f.defineParam(p.Name, p.Type)
		if err != nil {
			return fmt.Errorf("parameter %s: %w", p.Name, err)
		}
		mt, err := l.typeOf(p.Type)
		if err != nil {
			return err
		}
		fn.Param(b.mname, mt)
	}
	if fd.Readonly {
		// `readonly` is an assertion about arguments, not a qualifier: pointers
		// here are untyped bytes, so there is nothing to spell const. Violating
		// it stays UB (§12.8).
		fn.Attrs = append(fn.Attrs)
	}

	body := msl.NewBlock()
	if err := f.lowerBody(body); err != nil {
		return err
	}
	fn.Body = f.assemble(msl.NewBlock(), body)
	l.out.Add(fn)
	return nil
}

// assemble splices the hoisted §7.3 declarations in ahead of the prologue and
// the structurized body. Declarations are collected while the body is built, so
// this necessarily runs last.
func (f *fnLowerer) assemble(prologue, body *msl.Block) *msl.Block {
	out := msl.NewBlock()
	out.Append(f.decls...)
	if len(f.decls) > 0 && prologue.Len()+body.Len() > 0 {
		out.Blank()
	}
	out.Append(prologue.Stmts()...)
	if prologue.Len() > 0 && body.Len() > 0 {
		out.Blank()
	}
	out.Append(body.Stmts()...)
	return out
}

// bytePtr reinterprets an address as the untyped byte pointer every .gvir
// pointer value is.
func bytePtr(space msl.AddressSpace, e msl.Expr) msl.Expr {
	return msl.TCall("reinterpret_cast", []msl.TypeArg{msl.TArg(msl.Ptr(space, msl.UChar))}, e)
}

// addressOf takes the address of a storage object: an array already decays.
func addressOf(t gvir.Type, e msl.Expr) msl.Expr {
	if _, isArray := t.(gvir.ArrayType); isArray {
		return e
	}
	return e.Addr()
}