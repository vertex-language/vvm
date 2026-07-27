package stablehlo_test

import (
	"strings"
	"testing"

	"github.com/vertex-language/ir/stablehlo"
	"github.com/vertex-language/ir/stablehlo/encoding/text"
)

func buildVadd() *stablehlo.Module {
	m := stablehlo.NewModule("vadd")
	tf := stablehlo.Tensor(stablehlo.F32, 1024)

	f := stablehlo.NewFunc("main")
	a := f.Params.Add(stablehlo.Param{Name: "arg0", Type: tf})
	b := f.Params.Add(stablehlo.Param{Name: "arg1", Type: tf})
	f.Results = []stablehlo.Type{tf}

	cb := f.Code
	sum := cb.Add(a, b)
	zero := cb.Constant(stablehlo.Splat(0.0, tf))
	out := cb.Maximum(sum, zero)
	cb.Return(out)

	m.Funcs.Add(f)
	return m
}

func TestQuickStart(t *testing.T) {
	src, err := text.NewPrinter(buildVadd()).WithPrettyFuncs(true).Print()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"module @vadd {",
		"func.func @main(%arg0: tensor<1024xf32>, %arg1: tensor<1024xf32>) -> tensor<1024xf32> {",
		`%0 = "stablehlo.add"(%arg0, %arg1) : (tensor<1024xf32>, tensor<1024xf32>) -> tensor<1024xf32>`,
		`%1 = "stablehlo.constant"() {value = dense<0.0> : tensor<1024xf32>} : () -> tensor<1024xf32>`,
		`%2 = "stablehlo.maximum"(%0, %1) : (tensor<1024xf32>, tensor<1024xf32>) -> tensor<1024xf32>`,
		`"func.return"(%2) : (tensor<1024xf32>) -> ()`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("output missing %q\n---\n%s", want, src)
		}
	}
}

func TestDeterminism(t *testing.T) {
	a, err := text.NewPrinter(buildVadd()).Print()
	if err != nil {
		t.Fatal(err)
	}
	b, err := text.NewPrinter(buildVadd()).Print()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("printing is not deterministic")
	}
}

func TestReduceRegion(t *testing.T) {
	m := stablehlo.NewModule("r")
	f := stablehlo.NewFunc("main")
	x := f.Params.Add(stablehlo.Param{Name: "arg0", Type: stablehlo.Tensor(stablehlo.F32, 4, 1024)})
	out := stablehlo.Tensor(stablehlo.F32, 4)
	f.Results = []stablehlo.Type{out}

	cb := f.Code
	zero := cb.Constant(stablehlo.Splat(0.0, stablehlo.Tensor(stablehlo.F32)))
	sum := cb.Reduce(out, []stablehlo.Value{x}, []stablehlo.Value{zero}, []int64{1},
		func(rb *stablehlo.RegionBuilder, args []stablehlo.Value) {
			rb.Return(rb.Add(args[0], args[1]))
		})
	cb.Return(sum)
	m.Funcs.Add(f)

	src, err := text.NewPrinter(m).Print()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, `"stablehlo.reduce"`) ||
		!strings.Contains(src, `"stablehlo.return"`) ||
		!strings.Contains(src, "dimensions = array<i64: 1>") {
		t.Errorf("unexpected reduce output:\n%s", src)
	}
}

func TestMissingRegionReturnIsError(t *testing.T) {
	m := stablehlo.NewModule("bad")
	f := stablehlo.NewFunc("main")
	x := f.Params.Add(stablehlo.Param{Name: "arg0", Type: stablehlo.Tensor(stablehlo.F32, 8)})
	f.Results = []stablehlo.Type{stablehlo.Tensor(stablehlo.F32)}
	cb := f.Code
	zero := cb.Constant(stablehlo.Splat(0.0, stablehlo.Tensor(stablehlo.F32)))
	s := cb.Reduce(stablehlo.Tensor(stablehlo.F32), []stablehlo.Value{x}, []stablehlo.Value{zero},
		[]int64{0}, func(rb *stablehlo.RegionBuilder, args []stablehlo.Value) {
			rb.Add(args[0], args[1]) // no Return
		})
	cb.Return(s)
	m.Funcs.Add(f)
	if _, err := text.NewPrinter(m).Print(); err == nil {
		t.Fatal("expected error for region without Return")
	}
}

func TestLint(t *testing.T) {
	m := stablehlo.NewModule("l")
	m.SetTargetVersion(stablehlo.V1_0)
	f := stablehlo.NewFunc("main")
	x := f.Params.Add(stablehlo.Param{Name: "arg0", Type: stablehlo.Tensor(stablehlo.F32, 8)})
	f.Results = []stablehlo.Type{stablehlo.Tensor(stablehlo.F32, 8)}
	cb := f.Code
	cb.Return(cb.Tan(x)) // tan requires >= 1.4
	m.Funcs.Add(f)

	p := text.NewPrinter(m).WithLint(true)
	if _, err := p.Print(); err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) == 0 {
		t.Fatal("expected a lint warning for tan at target 1.0.0")
	}
}