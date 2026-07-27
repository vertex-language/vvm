package text

import (
	"strings"
	"testing"

	"github.com/vertex-language/ir/msl"
)

func buildVadd() *msl.Module {
	m := msl.NewModule()
	m.SetVersion(msl.Metal32)

	k := msl.NewKernel("vadd")
	a := k.Params.Add(msl.Param{Name: "A", Type: msl.Ptr(msl.Device, msl.Float), Attr: msl.Buffer(0)})
	b := k.Params.Add(msl.Param{Name: "B", Type: msl.Ptr(msl.Device, msl.Float), Attr: msl.Buffer(1)})
	c := k.Params.Add(msl.Param{Name: "C", Type: msl.Ptr(msl.Device, msl.Float), Attr: msl.Buffer(2)})
	n := k.Params.Add(msl.Param{Name: "n", Type: msl.Ref(msl.Constant, msl.UInt), Attr: msl.Buffer(3)})
	id := k.Params.Add(msl.Param{Name: "gid", Type: msl.UInt, Attr: msl.ThreadPositionInGrid})

	cb := k.Code
	cb.If(msl.Ge(id, n)).Return().End()
	cb.Assign(msl.Index(c, id), msl.Add(msl.Index(a, id), msl.Index(b, id)))

	m.Funcs.Add(k)
	return m
}

func TestQuickStart(t *testing.T) {
	src, err := NewPrinter(buildVadd()).Print()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#include <metal_stdlib>",
		"using namespace metal;",
		"kernel void vadd(",
		"[[buffer(0)]]",
		"[[thread_position_in_grid]]",
		"if (gid >= n) {",
		"        return;",
		"C[gid] = A[gid] + B[gid];",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("output missing %q:\n%s", want, src)
		}
	}
}

func TestDeterminism(t *testing.T) {
	a, err := NewPrinter(buildVadd()).Print()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPrinter(buildVadd()).Print()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("output is not byte-stable across identical IR")
	}
}

func TestValidation(t *testing.T) {
	m := msl.NewModule()
	k := msl.NewKernel("bad")
	k.Params.Add(msl.Param{Name: "p", Type: msl.PtrType{Elem: msl.Float}}) // no space
	k.Ret = msl.Float                                                      // kernel non-void
	m.Funcs.Add(k)
	if _, err := NewPrinter(m).Print(); err == nil {
		t.Fatal("expected structural error")
	}
}

func TestLint(t *testing.T) {
	m := msl.NewModule() // metal3.0 floor below tensor requirement
	m.SetVersion(msl.Metal30)
	k := msl.NewKernel("t")
	k.Params.Add(msl.Param{Name: "x", Type: msl.BFloat, Attr: msl.Buffer(0)})
	k.Params.Add(msl.Param{Name: "w", Type: msl.TensorHandle(msl.Half, 2), Attr: msl.Buffer(1)})
	m.Funcs.Add(k)

	p := NewPrinter(m).WithLint(true)
	if _, err := p.Print(); err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) != 2 {
		t.Fatalf("want 2 lint warnings, got %v", p.Warnings())
	}
}

func TestNameUniquify(t *testing.T) {
	k := msl.NewKernel("u")
	k.Params.Add(msl.Param{Name: "sum", Type: msl.Float, Attr: msl.Buffer(0)})
	v := k.Code.Let(msl.Float, "sum", msl.F(0))
	if v != msl.Ident("sum_1") {
		t.Fatalf("want sum_1, got %v", v)
	}
}