// builtin.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// §9 execution builtins. MSL delivers every one of them as an attributed
// kernel parameter, so a builtin's first use adds a parameter and every later
// use reads the same one.

// builtinParam returns the parameter carrying an attribute, declaring it on
// first use.
func (f *fnLower) builtinParam(attr msl.Attr, hint string, t msl.Type) msl.Expr {
	key := string(attr.Name)
	if e, ok := f.builtins[key]; ok {
		return e
	}
	e := f.fn.Param(f.l.names.fresh(hint), t, attr)
	f.builtins[key] = e
	return e
}

// positional and extent builtins arrive as uint3; the subgroup ones as ushort.
var builtinAttr = map[gvir.Opcode]struct {
	attr msl.Attr
	hint string
	vec  bool
}{
	gvir.OpThreadInGrid:      {msl.ThreadPositionInGrid, "tid", true},
	gvir.OpThreadInGroup:     {msl.ThreadPositionInThreadgroup, "ltid", true},
	gvir.OpGroupInGrid:       {msl.ThreadgroupPositionInGrid, "gid", true},
	gvir.OpThreadsPerGroup:   {msl.ThreadsPerThreadgroup, "ntid", true},
	gvir.OpThreadsPerGrid:    {msl.ThreadsPerGrid, "nthreads", true},
	gvir.OpGroupsPerGrid:     {msl.RawAttr("threadgroups_per_grid"), "ngroups", true},
	gvir.OpThreadInSubgroup:  {msl.ThreadIndexInSIMDGroup, "lane", false},
	gvir.OpSubgroupInGroup:   {msl.SIMDGroupIndexInThreadgroup, "simd", false},
	gvir.OpThreadsPerSubgroup: {msl.ThreadsPerSIMDGroup, "simdwidth", false},
	gvir.OpSubgroupsPerGroup: {msl.RawAttr("simdgroups_per_threadgroup"), "nsimd", false},
}

// extentOf pairs a positional builtin with the extent §9's linearization
// multiplies it by.
var extentOf = map[gvir.Opcode]gvir.Opcode{
	gvir.OpThreadInGrid:  gvir.OpThreadsPerGrid,
	gvir.OpThreadInGroup: gvir.OpThreadsPerGroup,
	gvir.OpGroupInGrid:   gvir.OpGroupsPerGrid,
}

func (f *fnLower) builtin(b *msl.Block, in *gvir.Instruction) error {
	if f.kernel == nil {
		// A func has no attributed parameters of its own; §9 builtins are a
		// kernel-signature concern, and threading them through would change
		// the calling convention.
		return fmt.Errorf("%s is only reachable from a kernel on this backend (see todos)", in.Op)
	}

	if in.Op == gvir.OpDynamicGroupSize {
		if f.kernel.DynamicGroup == nil {
			return f.assign(b, in, msl.Cast(msl.Int, msl.I(0))) // §9.1: 0 when none is declared
		}
		return f.assign(b, in, msl.Cast(msl.Int, f.dynamicGroupSize()))
	}

	spec, ok := builtinAttr[in.Op]
	if !ok {
		return fmt.Errorf("%s has no MSL builtin", in.Op)
	}
	t := msl.Type(msl.UShort)
	if spec.vec {
		t = msl.Vec(msl.UInt, 3)
	}
	p := f.builtinParam(spec.attr, spec.hint, t)

	if !spec.vec {
		// These five reject every dimension suffix (§9.1). Two are i64 and
		// three are i32.
		out, _ := in.Op.ResultType(nil, gvir.DimNone)
		mt, err := f.l.mapType(out)
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Cast(mt, p))
	}

	if in.Dim != gvir.DimNone {
		return f.assign(b, in, msl.Cast(msl.Int, p.Sel(string(in.Dim))))
	}

	// Unsuffixed: the normative i64 linearization, px + py*ex + pz*ex*ey.
	ext, ok := extentOf[in.Op]
	if !ok {
		// An extent builtin's unsuffixed form is ex*ey*ez.
		return f.assign(b, in, lin3(p, nil))
	}
	espec := builtinAttr[ext]
	e := f.builtinParam(espec.attr, espec.hint, msl.Vec(msl.UInt, 3))
	return f.assign(b, in, lin3(p, &e))
}

// lin3 emits §9's linearization in i64. With no extent it is the product form
// used by the extent builtins themselves.
func lin3(p msl.Expr, ext *msl.Expr) msl.Expr {
	l := func(e msl.Expr, c string) msl.Expr { return msl.Cast(msl.Long, e.Sel(c)) }
	if ext == nil {
		return l(p, "x").Mul(l(p, "y")).Mul(l(p, "z"))
	}
	ex, ey := l(*ext, "x"), l(*ext, "y")
	return l(p, "x").
		Add(l(p, "y").Mul(ex)).
		Add(l(p, "z").Mul(ex).Mul(ey))
}

// dynamicGroupSize reads the launch-provisioned byte count. §6.3 says every
// backend carries it natively; on Metal the threadgroup allocation's length is
// set with setThreadgroupMemoryLength and is not readable from the shader, so
// it arrives in a backend-private word at [[buffer(1)]] — outside the §6.3
// buffer, which stays byte-identical.
func (f *fnLower) dynamicGroupSize() msl.Expr {
	const key = "vv_dynamic_group_size"
	if e, ok := f.builtins[key]; ok {
		return e
	}
	e := f.fn.Param(f.l.names.fresh("dyngroup"), msl.Ref(msl.Constant, msl.UInt), msl.Buffer(1))
	f.builtins[key] = e
	return e
}