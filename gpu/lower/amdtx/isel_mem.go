// isel_mem.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

func (b *bodyLowerer) mem(o *amdtx.Body, in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpAlloca:
		return b.alloca(o, in)
	case gvir.OpLoad:
		return b.load(o, in)
	case gvir.OpStore:
		return b.store(o, in)
	case gvir.OpIndex:
		return b.index(o, in)
	case gvir.OpField:
		return b.field(o, in)
	case gvir.OpExtract:
		return b.extract(o, in)
	case gvir.OpInsert:
		return b.insert(o, in)
	case gvir.OpSplat:
		return b.splat(o, in)
	case gvir.OpSwizzle:
		return b.swizzle(o, in)
	case gvir.OpMemcopy, gvir.OpMemmove, gvir.OpMemset:
		return fmt.Errorf("%s is a per-thread byte loop (§8.3) and is not yet emitted", in.Op)
	}
	return fmt.Errorf("%s is not a memory opcode", in.Op)
}

// alloca assigns a byte offset in the private segment. §8.1 puts every alloca
// in the entry block before any other instruction, so the offsets are known
// before any access to them can appear.
func (b *bodyLowerer) alloca(o *amdtx.Body, in *gvir.Instruction) error {
	size, err := b.l.src.SizeOf(in.Suffix)
	if err != nil {
		return err
	}
	align, err := b.l.src.AlignOf(in.Suffix)
	if err != nil {
		return err
	}
	if in.Align > 0 {
		align = in.Align
	}
	b.privBytes = alignUp(b.privBytes, align)
	off := b.privBytes
	b.privBytes += size
	return b.bindAddress(in.Result, gvir.PtrPrivate, off, in.Suffix)
}

// address materializes the address operand for a space. Global and constant
// take the full 64-bit pair; group and private take the low dword, which is
// the 32-bit hardware address the AMDTX §6 pointer widths describe.
func (b *bodyLowerer) address(v *value, space gvir.AddrSpace) (amdtx.Mem, error) {
	switch space {
	case gvir.SpaceGlobal, gvir.SpaceConstant:
		return amdtx.At(v.regs[0]), nil
	case gvir.SpaceGroup, gvir.SpacePrivate:
		return amdtx.At(v.regs[0].Dword(0)), nil
	}
	return amdtx.Mem{}, fmt.Errorf("unknown address space %q", space)
}

func (b *bodyLowerer) load(o *amdtx.Body, in *gvir.Instruction) error {
	p, err := b.srcValue(o, in.Args[0], gvir.PtrGlobal)
	if err != nil {
		return err
	}
	if p.space == "" {
		return fmt.Errorf("load through a value that is not a space-qualified pointer")
	}
	prefix, err := memPrefix(p.space)
	if err != nil {
		return err
	}
	addr, err := b.address(p, p.space)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	elem := gvir.ElemOrSelf(in.Suffix)
	step, err := b.l.src.SizeOf(elem)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		at := amdtx.Mem{Base: addr.Base, Offset: addr.Offset + int64(lane*step)}
		if sfx := memSuffix(elem, false); sfx != "" {
			o.Inst(prefix+"_load_"+sfx, dst.regs[lane], at)
			continue
		}
		// The typed helpers derive the _bN suffix from the data register, so
		// V9 holds by construction.
		switch prefix {
		case "global":
			o.GlobalLoad(dst.regs[lane], at)
		case "ds":
			o.DSLoad(dst.regs[lane], at)
		case "scratch":
			o.ScratchLoad(dst.regs[lane], at)
		}
	}
	return b.waitFor(o, p.space)
}

func (b *bodyLowerer) store(o *amdtx.Body, in *gvir.Instruction) error {
	p, err := b.srcValue(o, in.Args[0], gvir.PtrGlobal)
	if err != nil {
		return err
	}
	if p.space == "" {
		return fmt.Errorf("store through a value that is not a space-qualified pointer")
	}
	if p.space == gvir.SpaceConstant {
		return fmt.Errorf("store through ptr[constant] is a verification error (§5)")
	}
	prefix, err := memPrefix(p.space)
	if err != nil {
		return err
	}
	addr, err := b.address(p, p.space)
	if err != nil {
		return err
	}
	src, err := b.srcValue(o, in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	elem := gvir.ElemOrSelf(in.Suffix)
	step, err := b.l.src.SizeOf(elem)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		at := amdtx.Mem{Base: addr.Base, Offset: addr.Offset + int64(lane*step)}
		if sfx := memSuffix(elem, true); sfx != "" {
			o.InstN(prefix+"_store_"+sfx, nil, []amdtx.Operand{at, src.reg(lane)})
			continue
		}
		switch prefix {
		case "global":
			o.GlobalStore(at, src.reg(lane))
		case "ds":
			o.DSStore(at, src.reg(lane))
		case "scratch":
			o.ScratchStore(at, src.reg(lane))
		}
	}
	return nil
}

// waitFor emits the counter wait a load makes pending. Adjacency conveys
// nothing (P6), and this is the conservative discipline the README's todo
// list wants replaced with a sinking pass.
func (b *bodyLowerer) waitFor(o *amdtx.Body, space gvir.AddrSpace) error {
	switch space {
	case gvir.SpaceGroup:
		o.Waitcnt(amdtx.LGKM(0))
	default:
		o.Waitcnt(amdtx.VM(0))
	}
	return nil
}

// index is byte pointer arithmetic keeping the operand's space (§8.3).
func (b *bodyLowerer) index(o *amdtx.Body, in *gvir.Instruction) error {
	p, err := b.srcValue(o, in.Args[0], gvir.PtrGlobal)
	if err != nil {
		return err
	}
	off, err := b.srcValue(o, in.Args[1], gvir.I64)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, gvir.PtrType{Space: p.space})
	if err != nil {
		return err
	}
	dst.space = p.space
	dst.pointee = p.pointee
	switch p.space {
	case gvir.SpaceGroup, gvir.SpacePrivate:
		o.Inst("v_add_u32", dst.regs[0].Dword(0), p.regs[0].Dword(0), off.regs[0].Dword(0))
		o.Inst("v_mov_b32", dst.regs[0].Dword(1), amdtx.Imm(0))
	default:
		o.Inst("v_add_co_u32", dst.regs[0].Dword(0), p.regs[0].Dword(0), off.regs[0].Dword(0))
		o.Inst("v_addc_co_u32", dst.regs[0].Dword(1), p.regs[0].Dword(1), off.regs[0].Dword(1))
	}
	return nil
}

// field needs the pointee type, which .gvir pointers do not carry. Provenance
// recovers it for allocas, group declarations and chains from one of those;
// through a raw kernel argument there is nothing in the IR to name the struct.
func (b *bodyLowerer) field(o *amdtx.Body, in *gvir.Instruction) error {
	p, err := b.srcValue(o, in.Args[0], gvir.PtrGlobal)
	if err != nil {
		return err
	}
	if p.pointee == nil {
		return fmt.Errorf("field.ptr through %s: the pointee type is not recoverable from provenance, "+
			"and .gvir pointers carry an address space but no pointee (§8.3)", p.name)
	}
	st, ok := p.pointee.(gvir.StructType)
	if !ok {
		return fmt.Errorf("field.ptr through a pointer to %s, which is not a struct", p.pointee)
	}
	s := b.l.src.StructByName(st.Name)
	if s == nil {
		return fmt.Errorf("undeclared struct %s", st.Name)
	}
	layout, err := b.l.src.StructLayout(s)
	if err != nil {
		return err
	}
	k := int(in.Args[1].Int)
	if k < 0 || k >= len(layout.Fields) {
		return fmt.Errorf("field index %d is out of range for struct %s", k, st.Name)
	}
	f := layout.Fields[k]
	dst, err := b.define(in.Result, gvir.PtrType{Space: p.space})
	if err != nil {
		return err
	}
	dst.space = p.space
	dst.pointee = f.Type
	switch p.space {
	case gvir.SpaceGroup, gvir.SpacePrivate:
		o.Inst("v_add_u32", dst.regs[0].Dword(0), p.regs[0].Dword(0), amdtx.Imm(int64(f.Offset)))
		o.Inst("v_mov_b32", dst.regs[0].Dword(1), amdtx.Imm(0))
	default:
		o.Inst("v_add_co_u32", dst.regs[0].Dword(0), p.regs[0].Dword(0), amdtx.Imm(int64(f.Offset)))
		o.Inst("v_addc_co_u32", dst.regs[0].Dword(1), p.regs[0].Dword(1), amdtx.Imm(0))
	}
	return nil
}

// ---- Vectors --------------------------------------------------------------

func (b *bodyLowerer) extract(o *amdtx.Body, in *gvir.Instruction) error {
	v, err := b.srcValue(o, in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	k := int(in.Args[1].Int)
	elem := gvir.ElemOrSelf(in.Suffix)
	dst, err := b.define(in.Result, elem)
	if err != nil {
		return err
	}
	n := dwordsOf(elem)
	for d := 0; d < n; d++ {
		o.Inst("v_mov_b32", dword(dst.regs[0], d, n), dword(v.reg(k), d, n))
	}
	return nil
}

func (b *bodyLowerer) insert(o *amdtx.Body, in *gvir.Instruction) error {
	v, err := b.srcValue(o, in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	k := int(in.Args[1].Int)
	elem := gvir.ElemOrSelf(in.Suffix)
	x, err := b.srcValue(o, in.Args[2], elem)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	n := dwordsOf(elem)
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		src := v.reg(lane)
		if lane == k {
			src = x.reg(0)
		}
		for d := 0; d < n; d++ {
			o.Inst("v_mov_b32", dword(dst.regs[lane], d, n), dword(src, d, n))
		}
	}
	return nil
}

func (b *bodyLowerer) splat(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	x, err := b.srcValue(o, in.Args[0], elem)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	n := dwordsOf(elem)
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		for d := 0; d < n; d++ {
			o.Inst("v_mov_b32", dword(dst.regs[lane], d, n), dword(x.reg(0), d, n))
		}
	}
	return nil
}

func (b *bodyLowerer) swizzle(o *amdtx.Body, in *gvir.Instruction) error {
	if len(in.Args) < 2 {
		return fmt.Errorf("swizzle needs two vector operands and a mask")
	}
	a, err := b.srcValue(o, in.Args[0], in.Suffix)
	if err != nil {
		return err
	}
	c, err := b.srcValue(o, in.Args[1], in.Suffix)
	if err != nil {
		return err
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	elem := gvir.ElemOrSelf(in.Suffix)
	n := dwordsOf(elem)
	width := laneCount(in.Suffix)
	for lane := 0; lane < width && lane+2 < len(in.Args); lane++ {
		k := int(in.Args[lane+2].Int)
		src := a.reg(k)
		if k >= width {
			src = c.reg(k - width)
		}
		for d := 0; d < n; d++ {
			o.Inst("v_mov_b32", dword(dst.regs[lane], d, n), dword(src, d, n))
		}
	}
	return nil
}