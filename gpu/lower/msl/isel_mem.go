// isel_mem.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

func (f *fnLower) memory(b *msl.Block, in *gvir.Instruction) error {
	switch in.Op {

	// --- alloca (§8.1) ------------------------------------------------------
	case gvir.OpAlloca:
		size, align, err := f.l.mslSizeAlign(in.Suffix)
		if err != nil {
			return err
		}
		if in.Align > 0 {
			if !gvir.ValidAlign(in.Align) {
				return fmt.Errorf("align %d is not a power of two in [1,1024] (§2)", in.Align)
			}
			align = in.Align
		}
		st, err := storageType(size, align)
		if err != nil {
			return err
		}
		obj := f.temp("scratch")
		// §8.1 puts allocas at the top of the entry block, so the object joins
		// the prologue and the name binds to its address for the whole body.
		f.prologue = append(f.prologue, &msl.VarDecl{
			Type: st, Name: obj,
			Comment: fmt.Sprintf("alloca %s: %d bytes, align %d", in.Suffix, size, align),
		})
		bp, err := bytePtr(gvir.SpacePrivate)
		if err != nil {
			return err
		}
		f.vals.bind(in.Result, gvir.PtrPrivate, bp, bytesOf(msl.Name(obj)), in.Suffix)
		return nil

	// --- load / store (§8.3) ------------------------------------------------
	case gvir.OpLoad:
		p, err := f.ptrOperand(in.Args[0])
		if err != nil {
			return err
		}
		t, err := f.l.mapType(in.Suffix)
		if err != nil {
			return err
		}
		bd, err := f.define(in)
		if err != nil {
			return err
		}
		bd.pointee = nil
		b.Assign(bd.ref, deref(t, p.ref))
		return nil

	case gvir.OpStore:
		// Destination first (§8.3).
		p, err := f.ptrOperand(in.Args[0])
		if err != nil {
			return err
		}
		t, err := f.l.mapType(in.Suffix)
		if err != nil {
			return err
		}
		v, err := f.operand(in.Args[1], t)
		if err != nil {
			return err
		}
		if p.gtyp.(gvir.PtrType).Space == gvir.SpaceConstant {
			return fmt.Errorf("store through ptr[constant] (§5)")
		}
		b.Assign(deref(t, p.ref), v)
		return nil

	// --- pointer arithmetic (§8.3) ------------------------------------------
	case gvir.OpIndex:
		p, err := f.ptrOperand(in.Args[0])
		if err != nil {
			return err
		}
		off, err := f.operand(in.Args[1], msl.Long)
		if err != nil {
			return err
		}
		bd, err := f.define(in)
		if err != nil {
			return err
		}
		// Byte arithmetic on a byte pointer is the identity, and the pointee
		// travels with it so a later field.ptr still knows the struct.
		bd.pointee = p.pointee
		b.Assign(bd.ref, p.ref.Add(off))
		return nil

	case gvir.OpField:
		p, err := f.ptrOperand(in.Args[0])
		if err != nil {
			return err
		}
		k, err := index(in.Args[1])
		if err != nil {
			return err
		}
		st, ok := p.pointee.(gvir.StructType)
		if !ok {
			return fmt.Errorf("field.ptr through %s: the pointee is not recoverable from provenance, and .gvir pointers carry no pointee type (§8.3)", in.Args[0])
		}
		s := f.l.src.StructByName(st.Name)
		if s == nil || k < 0 || k >= len(s.Fields) {
			return fmt.Errorf("struct %s has no field %d", st.Name, k)
		}
		lay, err := f.l.src.StructLayout(s)
		if err != nil {
			return err
		}
		bd, err := f.define(in)
		if err != nil {
			return err
		}
		bd.pointee = s.Fields[k].Type
		b.Assign(bd.ref, p.ref.Add(msl.Cast(msl.Long, msl.I(int64(lay.Fields[k].Offset)))))
		return nil

	// --- bulk memory (§8.3) -------------------------------------------------
	case gvir.OpMemcopy, gvir.OpMemmove, gvir.OpMemset:
		return f.bulk(b, in)

	// --- vectors (§4.4, §8.3) -----------------------------------------------
	case gvir.OpExtract:
		t, err := f.l.mapType(in.Suffix)
		if err != nil {
			return err
		}
		v, err := f.operand(in.Args[0], t)
		if err != nil {
			return err
		}
		k, err := index(in.Args[1])
		if err != nil {
			return err
		}
		return f.assign(b, in, v.At(msl.I(int64(k))))

	case gvir.OpInsert:
		t, err := f.l.mapType(in.Suffix)
		if err != nil {
			return err
		}
		v, err := f.operand(in.Args[0], t)
		if err != nil {
			return err
		}
		k, err := index(in.Args[1])
		if err != nil {
			return err
		}
		vt, ok := in.Suffix.(gvir.VecType)
		if !ok {
			return fmt.Errorf("insert's suffix names the vector type")
		}
		et, err := f.l.mapType(vt.Elem)
		if err != nil {
			return err
		}
		x, err := f.operand(in.Args[2], et)
		if err != nil {
			return err
		}
		bd, err := f.define(in)
		if err != nil {
			return err
		}
		b.Assign(bd.ref, v)
		b.Assign(bd.ref.At(msl.I(int64(k))), x)
		return nil

	case gvir.OpSplat:
		t, err := f.l.mapType(in.Suffix)
		if err != nil {
			return err
		}
		vt, ok := in.Suffix.(gvir.VecType)
		if !ok {
			return fmt.Errorf("splat's suffix names the vector type")
		}
		et, err := f.l.mapType(vt.Elem)
		if err != nil {
			return err
		}
		x, err := f.operand(in.Args[0], et)
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Ctor(t, x))

	case gvir.OpSwizzle:
		t, err := f.l.mapType(in.Suffix)
		if err != nil {
			return err
		}
		vt, ok := in.Suffix.(gvir.VecType)
		if !ok {
			return fmt.Errorf("swizzle's suffix names the vector type")
		}
		if len(in.Args) < 2 {
			return fmt.Errorf("swizzle takes two sources and a mask")
		}
		a, err := f.operand(in.Args[0], t)
		if err != nil {
			return err
		}
		c, err := f.operand(in.Args[1], t)
		if err != nil {
			return err
		}
		elems := make([]msl.Expr, 0, len(in.Args)-2)
		for _, o := range in.Args[2:] {
			k, err := index(o)
			if err != nil {
				return err
			}
			if k < vt.Len {
				elems = append(elems, a.At(msl.I(int64(k))))
			} else {
				elems = append(elems, c.At(msl.I(int64(k-vt.Len))))
			}
		}
		return f.assign(b, in, msl.Ctor(t, elems...))
	}
	return fmt.Errorf("%s is not a memory opcode", in.Op)
}

// bulk emits the per-thread byte loops §8.3 requires. These are not group
// collectives: each thread executing one moves the whole range itself.
func (f *fnLower) bulk(b *msl.Block, in *gvir.Instruction) error {
	dst, err := f.ptrOperand(in.Args[0])
	if err != nil {
		return err
	}
	length, err := f.operand(in.Args[2], msl.Long)
	if err != nil {
		return err
	}
	i := f.temp("i")

	if in.Op == gvir.OpMemset {
		val, err := f.operand(in.Args[1], msl.UChar)
		if err != nil {
			return err
		}
		b.Range(i, msl.Cast(msl.UInt, msl.I(0)), msl.Cast(msl.UInt, length), func(lb *msl.Block) {
			lb.Assign(dst.ref.At(msl.Name(i)), val)
		})
		return nil
	}

	src, err := f.ptrOperand(in.Args[1])
	if err != nil {
		return err
	}
	forward := func(lb *msl.Block) {
		lb.Range(i, msl.Cast(msl.UInt, msl.I(0)), msl.Cast(msl.UInt, length), func(ib *msl.Block) {
			ib.Assign(dst.ref.At(msl.Name(i)), src.ref.At(msl.Name(i)))
		})
	}
	if in.Op == gvir.OpMemcopy {
		// §12.4 makes overlapping memcopy operands UB, so one direction is
		// enough and no test is emitted.
		forward(b)
		return nil
	}

	sameSpace := dst.gtyp.(gvir.PtrType).Space == src.gtyp.(gvir.PtrType).Space
	if !sameSpace {
		// Objects in different address spaces cannot overlap, and MSL forbids
		// comparing pointers across spaces anyway.
		forward(b)
		return nil
	}
	j := f.temp("j")
	s := b.If(dst.ref.Lt(src.ref), forward)
	s.Else(func(eb *msl.Block) {
		eb.For(
			&msl.VarDecl{Type: msl.UInt, Name: j, Init: msl.Cast(msl.UInt, length)},
			msl.Name(j).Gt(msl.Cast(msl.UInt, msl.I(0))),
			&msl.IncDec{Op: msl.Dec, X: msl.Name(j)},
			func(ib *msl.Block) {
				idx := msl.Name(j).Sub(msl.Cast(msl.UInt, msl.I(1)))
				ib.Assign(dst.ref.At(idx), src.ref.At(idx))
			})
	})
	return nil
}