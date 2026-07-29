package ptx

import (
	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Execution builtins (§9).
//
// Dimension-suffixed forms are i32 and map to one special register (or one mad
// for the grid-relative positions). The unsuffixed forms are i64 and carry the
// normative linearization: px + py*ex + pz*ex*ey for positions, ex*ey*ez for
// extents, computed in i64.

const warpWidth = 32 // PTX's subgroup width is fixed (§9.2)

func (f *fn) builtin(in *gvir.Instruction) error {
	dst, err := f.result(in)
	if err != nil {
		return err
	}
	d := dst.reg()

	switch in.Op {
	case gvir.OpThreadsPerSubgroup:
		// A runtime value, not a constant, everywhere else — but on ptx the
		// width is fixed and WARP_SZ is the immediate that says so.
		f.b.MovSReg(d, ptx.WarpSz)
		return nil

	case gvir.OpDynamicGroupSize:
		// Read natively rather than from an appended kernarg field (§6.3). A
		// kernel with no dynamic_group reads 0, which is what the register
		// holds when nothing was provisioned.
		f.b.MovSReg(d, ptx.DynamicSmemSize)
		return nil

	case gvir.OpSubgroupsPerGroup:
		// ceil(threads_per_group / threads_per_subgroup)
		total := f.linearExtent32([3]ptx.SReg{ptx.NTidX, ptx.NTidY, ptx.NTidZ})
		f.b.Add(ptx.U32, total, total, ptx.Imm(warpWidth-1))
		f.b.Shr(ptx.U32, d, total, ptx.Imm(5))
		return nil

	case gvir.OpThreadInSubgroup:
		lane := f.tempReg(ptx.U32)
		f.b.MovSReg(lane, ptx.LaneID)
		f.b.Cvt(ptx.U64, ptx.U32, d, lane)
		return nil

	case gvir.OpSubgroupInGroup:
		// The linear thread index divided by the warp width. %warpid is a
		// volatile hint, not an identity, so it is deliberately not used.
		lin := f.linearPos32([3]ptx.SReg{ptx.TidX, ptx.TidY, ptx.TidZ},
			[3]ptx.SReg{ptx.NTidX, ptx.NTidY, ptx.NTidZ})
		sub := f.tempReg(ptx.U32)
		f.b.Shr(ptx.U32, sub, lin, ptx.Imm(5))
		f.b.Cvt(ptx.U64, ptx.U32, d, sub)
		return nil
	}

	// The dimension-suffixed family.
	pos, ext, isPos := builtinRegs(in.Op)
	if !isPos {
		return todof("%s is not a modelled builtin", in.Op)
	}

	if in.Dim != gvir.DimNone {
		i := dimIndex(in.Dim)
		switch in.Op {
		case gvir.OpThreadInGrid:
			// ctaid*ntid + tid
			f.b.MovSReg(d, ptx.CtaIdX+ptx.SReg(i))
			f.b.Mad(ptx.U32, d, d, ext[i], pos[i], ptx.MulLo)
		case gvir.OpThreadsPerGrid:
			n := f.tempReg(ptx.U32)
			f.b.MovSReg(n, pos[i])
			f.b.MovSReg(d, ext[i])
			f.b.Mul(ptx.U32, d, d, n, ptx.MulLo)
		default:
			f.b.MovSReg(d, pos[i])
		}
		return nil
	}

	// Unsuffixed: the i64 linearized form.
	var lin ptx.Reg
	switch in.Op {
	case gvir.OpThreadInGrid:
		lin = f.threadInGridLinear()
	case gvir.OpThreadsPerGrid:
		lin = f.gridExtentLinear()
	case gvir.OpThreadsPerGroup, gvir.OpGroupsPerGrid:
		lin = f.linearExtent32(pos)
	default:
		lin = f.linearPos32(pos, ext)
	}
	f.b.Cvt(ptx.U64, ptx.U32, d, lin)
	return nil
}

// builtinRegs returns the (position, extent) special-register triples a builtin
// reads, and whether the opcode is one of the dimension-suffixed family.
func builtinRegs(op gvir.Opcode) (pos, ext [3]ptx.SReg, ok bool) {
	tid := [3]ptx.SReg{ptx.TidX, ptx.TidY, ptx.TidZ}
	ntid := [3]ptx.SReg{ptx.NTidX, ptx.NTidY, ptx.NTidZ}
	ctaid := [3]ptx.SReg{ptx.CtaIdX, ptx.CtaIdY, ptx.CtaIdZ}
	nctaid := [3]ptx.SReg{ptx.NCtaIdX, ptx.NCtaIdY, ptx.NCtaIdZ}

	switch op {
	case gvir.OpThreadInGroup:
		return tid, ntid, true
	case gvir.OpGroupInGrid:
		return ctaid, nctaid, true
	case gvir.OpThreadsPerGroup:
		return ntid, ntid, true
	case gvir.OpGroupsPerGrid:
		return nctaid, nctaid, true
	case gvir.OpThreadInGrid:
		return tid, ntid, true
	case gvir.OpThreadsPerGrid:
		return ntid, nctaid, true
	}
	return pos, ext, false
}

func dimIndex(d gvir.Dim) int {
	switch d {
	case gvir.DimY:
		return 1
	case gvir.DimZ:
		return 2
	}
	return 0
}

// linearPos32 computes px + py*ex + pz*ex*ey in 32 bits. The i64 result width
// is a property of the binding, not of the arithmetic: no launch has more than
// 2^32 threads per group or blocks per grid.
func (f *fn) linearPos32(pos, ext [3]ptx.SReg) ptx.Reg {
	px, py, pz := f.sreg(pos[0]), f.sreg(pos[1]), f.sreg(pos[2])
	ex, ey := f.sreg(ext[0]), f.sreg(ext[1])

	lin := f.tempReg(ptx.U32)
	f.b.Mad(ptx.U32, lin, py, ex, px, ptx.MulLo)

	plane := f.tempReg(ptx.U32)
	f.b.Mul(ptx.U32, plane, ex, ey, ptx.MulLo)
	f.b.Mad(ptx.U32, lin, pz, plane, lin, ptx.MulLo)
	return lin
}

// linearExtent32 computes ex*ey*ez.
func (f *fn) linearExtent32(ext [3]ptx.SReg) ptx.Reg {
	ex, ey, ez := f.sreg(ext[0]), f.sreg(ext[1]), f.sreg(ext[2])
	d := f.tempReg(ptx.U32)
	f.b.Mul(ptx.U32, d, ex, ey, ptx.MulLo)
	f.b.Mul(ptx.U32, d, d, ez, ptx.MulLo)
	return d
}

// threadInGridLinear builds the per-component grid position first, then
// linearizes it against the grid extent — the components are ctaid*ntid + tid
// and the extent is nctaid*ntid (§9.1).
func (f *fn) threadInGridLinear() ptx.Reg {
	tid := [3]ptx.SReg{ptx.TidX, ptx.TidY, ptx.TidZ}
	ntid := [3]ptx.SReg{ptx.NTidX, ptx.NTidY, ptx.NTidZ}
	ctaid := [3]ptx.SReg{ptx.CtaIdX, ptx.CtaIdY, ptx.CtaIdZ}
	nctaid := [3]ptx.SReg{ptx.NCtaIdX, ptx.NCtaIdY, ptx.NCtaIdZ}

	var p, e [3]ptx.Reg
	for i := 0; i < 3; i++ {
		p[i] = f.tempReg(ptx.U32)
		f.b.Mad(ptx.U32, p[i], f.sreg(ctaid[i]), f.sreg(ntid[i]), f.sreg(tid[i]), ptx.MulLo)
		e[i] = f.tempReg(ptx.U32)
		f.b.Mul(ptx.U32, e[i], f.sreg(nctaid[i]), f.sreg(ntid[i]), ptx.MulLo)
	}

	lin := f.tempReg(ptx.U32)
	f.b.Mad(ptx.U32, lin, p[1], e[0], p[2-1+0], ptx.MulLo) // py*ex + px
	f.b.Mov(ptx.U32, lin, lin)
	plane := f.tempReg(ptx.U32)
	f.b.Mul(ptx.U32, plane, e[0], e[1], ptx.MulLo)
	f.b.Mad(ptx.U32, lin, p[2], plane, lin, ptx.MulLo)
	return lin
}

// gridExtentLinear is the componentwise product nctaid*ntid, linearized.
func (f *fn) gridExtentLinear() ptx.Reg {
	ntid := [3]ptx.SReg{ptx.NTidX, ptx.NTidY, ptx.NTidZ}
	nctaid := [3]ptx.SReg{ptx.NCtaIdX, ptx.NCtaIdY, ptx.NCtaIdZ}

	d := f.tempReg(ptx.U32)
	f.b.Mov(ptx.U32, d, ptx.Imm(1))
	for i := 0; i < 3; i++ {
		c := f.tempReg(ptx.U32)
		f.b.Mul(ptx.U32, c, f.sreg(nctaid[i]), f.sreg(ntid[i]), ptx.MulLo)
		f.b.Mul(ptx.U32, d, d, c, ptx.MulLo)
	}
	return d
}

// sreg materializes a special register into a virtual register; PTX reads them
// only through mov.
func (f *fn) sreg(s ptx.SReg) ptx.Reg {
	r := f.tempReg(s.Type())
	f.b.MovSReg(r, s)
	return r
}