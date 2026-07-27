package text

import (
	"strings"
	"testing"

	"github.com/vertex-language/ir/ptx"
)

// TestQuickStart replicates the README vadd kernel end to end.
func TestQuickStart(t *testing.T) {
	m := ptx.NewModule()
	m.SetTarget(ptx.SM90)

	k := ptx.NewKernel("vadd")
	k.Linkage = ptx.Visible
	k.Params.Add(ptx.Param{Name: "A", Type: ptx.U64})
	k.Params.Add(ptx.Param{Name: "B", Type: ptx.U64})
	k.Params.Add(ptx.Param{Name: "C", Type: ptx.U64})
	k.Params.Add(ptx.Param{Name: "n", Type: ptx.U32})

	cb := k.Code
	rd := cb.Regs

	i := rd.U32()
	n := rd.U32()
	p := rd.Pred()
	a := rd.U64()
	b := rd.U64()
	c := rd.U64()
	off := rd.U64()
	va := rd.F32()
	vb := rd.F32()

	done := cb.NewLabel("done")

	cb.MovU32(i, ptx.CtaIdX)
	cb.MadLoU32(i, i, ptx.NtidX, ptx.TidX)
	cb.LdParamU32(n, "n")
	cb.SetpGeU32(p, i, n)
	cb.BraIf(p, done)

	cb.LdParamU64(a, "A")
	cb.LdParamU64(b, "B")
	cb.LdParamU64(c, "C")
	cb.MulWideU32(off, i, 4)
	cb.AddU64(a, a, off)
	cb.AddU64(b, b, off)
	cb.AddU64(c, c, off)
	cb.LdGlobalF32(va, ptx.Addr(a))
	cb.LdGlobalF32(vb, ptx.Addr(b))
	cb.AddF32(va, va, vb)
	cb.StGlobalF32(ptx.Addr(c), va)

	cb.Bind(done)
	cb.Ret()

	m.Kernels.Add(k)

	src, err := NewPrinter(m).Print()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".version 9.3",
		".target sm_90",
		".address_size 64",
		".visible .entry vadd(",
		".param .u64 A,",
		".param .u32 n",
		".reg .pred %p<2>;",
		".reg .u32  %u<3>;",
		".reg .u64  %d<5>;",
		".reg .f32  %f<3>;",
		"mov.u32         %u1, %ctaid.x;",
		"mad.lo.u32      %u1, %u1, %ntid.x, %tid.x;",
		"ld.param.u32    %u2, [n];",
		"setp.ge.u32     %p1, %u1, %u2;",
		"@%p1 bra        done;",
		"mul.wide.u32    %d4, %u1, 4;",
		"ld.global.f32   %f1, [%d1];",
		"st.global.f32   [%d3], %f1;",
		"done:",
		"ret;",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("output missing %q\n---\n%s", want, src)
		}
	}
}

// TestDeterminism: same IR must print byte-identically.
func TestDeterminism(t *testing.T) {
	build := func() string {
		m := ptx.NewModule()
		k := ptx.NewKernel("k")
		x := k.Code.Regs.F32()
		k.Code.AddF32(x, x, x, ptx.Rn, ptx.Ftz)
		k.Code.Ret()
		m.Kernels.Add(k)
		s, _ := NewPrinter(m).Print()
		return s
	}
	if build() != build() {
		t.Fatal("printing is not deterministic")
	}
}