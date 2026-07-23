// variadics.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// va_start / va_arg / va_end (ir.md §4.4). Previously untested despite
// builder.go having dedicated VaStart/VaArg/VaEnd methods and §4.4 devoting
// a whole subsection to the mechanism (self-referential fnsig token,
// linear-use constraint, the "no other way to name variadic arguments"
// rule). These cases exercise the ordinary path: a variadic function that
// sums its trailing i32 arguments via a loop over va_arg, called with both
// a non-empty and an empty (count=0) argument list.
//
// Deliberately not covered here: tailcall-into-variadic-with-live-valist
// rejection, va_arg over-read (UB), and re-va_start-without-va_end
// (verification error) — those are error-path / verifier-behavior cases,
// not observable-value cases this runner's PASS/FAIL/exit-code shape can
// check the way the rest of this suite does.

func init() {
	register(testCase{
		name:       "variadic_sum_i32_args",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(arch, osName string) *vir.Module {
			m := vir.NewModule("variadic_sum")
			m.SetTarget(arch, osName, abiFor(osName))
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			// fn sum_varargs(count i32, ...) -> i32
			sumFn := m.DeclareFunction("sum_varargs", []vir.Param{{Name: "count", Type: vir.I32}}, vir.I32, false)
			sumFn.SetVariadic()
			sumFn.AllocaValist("va")
			sumFn.VaStart("sum_varargs_sig", "va", "count")

			identity(sumFn, "i", vir.I32, vir.IntLiteral(0))
			identity(sumFn, "sum", vir.I32, vir.IntLiteral(0))
			sumFn.Branch("loop")

			sumFn.Label("loop")
			cond := sumFn.Emit("cond", vir.OpSlt, vir.I32, vir.Ident("i"), vir.Ident("count"))
			sumFn.BranchIf(cond, "body", "done")

			sumFn.Label("body")
			next := sumFn.VaArg("next", vir.I32, vir.Ident("va"))
			sumFn.Add("sum", vir.I32, vir.Ident("sum"), next)
			sumFn.Add("i", vir.I32, vir.Ident("i"), vir.IntLiteral(1))
			sumFn.Branch("loop")

			sumFn.Label("done")
			sumFn.VaEnd(vir.Ident("va"))
			sumFn.Return(vir.Ident("sum"))

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			r := mainFn.Call("r", "sum_varargs", vir.IntLiteral(3),
				vir.IntLiteral(10), vir.IntLiteral(20), vir.IntLiteral(30))
			mainFn.Call("_", "printf", vir.Ident(fmtG.Name), r)
			mainFn.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(60),
	})

	// count=0: the trivial path where the loop body (and every va_arg read)
	// never executes at all — the boundary case for the "reading past the
	// number of arguments supplied is UB" rule, approached from the safe
	// side (reading zero of zero, never touching va_arg).
	register(testCase{
		name:       "variadic_sum_zero_args",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(arch, osName string) *vir.Module {
			m := vir.NewModule("variadic_sum_zero")
			m.SetTarget(arch, osName, abiFor(osName))
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			sumFn := m.DeclareFunction("sum_varargs", []vir.Param{{Name: "count", Type: vir.I32}}, vir.I32, false)
			sumFn.SetVariadic()
			sumFn.AllocaValist("va")
			sumFn.VaStart("sum_varargs_sig", "va", "count")

			identity(sumFn, "i", vir.I32, vir.IntLiteral(0))
			identity(sumFn, "sum", vir.I32, vir.IntLiteral(0))
			sumFn.Branch("loop")

			sumFn.Label("loop")
			cond := sumFn.Emit("cond", vir.OpSlt, vir.I32, vir.Ident("i"), vir.Ident("count"))
			sumFn.BranchIf(cond, "body", "done")

			sumFn.Label("body")
			next := sumFn.VaArg("next", vir.I32, vir.Ident("va"))
			sumFn.Add("sum", vir.I32, vir.Ident("sum"), next)
			sumFn.Add("i", vir.I32, vir.Ident("i"), vir.IntLiteral(1))
			sumFn.Branch("loop")

			sumFn.Label("done")
			sumFn.VaEnd(vir.Ident("va"))
			sumFn.Return(vir.Ident("sum"))

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			r := mainFn.Call("r", "sum_varargs", vir.IntLiteral(0))
			mainFn.Call("_", "printf", vir.Ident(fmtG.Name), r)
			mainFn.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(0),
	})
}