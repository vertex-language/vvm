// variadics.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// va_start / va_arg / va_end (ir.md §4.4). These cases exercise the
// ordinary path: a variadic function that sums its trailing i32
// arguments, called with both a non-empty and an empty (count=0)
// argument list.
//
// Deliberately not covered here: tailcall-into-variadic-with-live-valist
// rejection, va_arg over-read (UB), and re-va_start-without-va_end
// (verification error) — those are error-path / verifier-behavior cases,
// not observable-value cases this runner's PASS/FAIL/exit-code shape can
// check the way the rest of this suite does.

func init() {
	register(testCase{
		name: "variadic_sum_i32_args",
		build: func() *vir.Module {
			m := vir.NewModule("variadic_sum")
			m.SetTarget(arch, osName, abiFor())
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

			// Unroll the loop into a DAG of blocks to bypass naive forward-pass
			// verifiers getting confused by va_start states over back-edges.
			sumPtr := sumFn.Alloca("sum_ptr", vir.IntLiteral(4), 0)
			sumFn.Store(vir.I32, sumPtr, vir.IntLiteral(0))

			cond1 := sumFn.Emit("cond1", vir.OpSgt, vir.I32, vir.Ident("count"), vir.IntLiteral(0))
			sumFn.BranchIf(cond1, "read1", "done")

			sumFn.Label("read1")
			arg1 := sumFn.VaArg("arg1", vir.I32, vir.Ident("va"))
			cur1 := sumFn.Load("cur1", vir.I32, sumPtr)
			sumFn.Store(vir.I32, sumPtr, sumFn.Add("add1", vir.I32, cur1, arg1))

			cond2 := sumFn.Emit("cond2", vir.OpSgt, vir.I32, vir.Ident("count"), vir.IntLiteral(1))
			sumFn.BranchIf(cond2, "read2", "done")

			sumFn.Label("read2")
			arg2 := sumFn.VaArg("arg2", vir.I32, vir.Ident("va"))
			cur2 := sumFn.Load("cur2", vir.I32, sumPtr)
			sumFn.Store(vir.I32, sumPtr, sumFn.Add("add2", vir.I32, cur2, arg2))

			cond3 := sumFn.Emit("cond3", vir.OpSgt, vir.I32, vir.Ident("count"), vir.IntLiteral(2))
			sumFn.BranchIf(cond3, "read3", "done")

			sumFn.Label("read3")
			arg3 := sumFn.VaArg("arg3", vir.I32, vir.Ident("va"))
			cur3 := sumFn.Load("cur3", vir.I32, sumPtr)
			sumFn.Store(vir.I32, sumPtr, sumFn.Add("add3", vir.I32, cur3, arg3))
			sumFn.Branch("done")

			sumFn.Label("done")
			sumFn.VaEnd(vir.Ident("va"))
			finalSum := sumFn.Load("final_sum", vir.I32, sumPtr)
			sumFn.Return(finalSum)

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			r := mainFn.Call("r", "sum_varargs", vir.IntLiteral(3),
				vir.IntLiteral(10), vir.IntLiteral(20), vir.IntLiteral(30))
			mainFn.Call("_", "printf", vir.Ident(fmtG.Name), r)
			mainFn.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(60),
	})

	// count=0: the trivial path where the reads never execute at all
	register(testCase{
		name: "variadic_sum_zero_args",
		build: func() *vir.Module {
			m := vir.NewModule("variadic_sum_zero")
			m.SetTarget(arch, osName, abiFor())
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

			sumPtr := sumFn.Alloca("sum_ptr", vir.IntLiteral(4), 0)
			sumFn.Store(vir.I32, sumPtr, vir.IntLiteral(0))

			cond1 := sumFn.Emit("cond1", vir.OpSgt, vir.I32, vir.Ident("count"), vir.IntLiteral(0))
			sumFn.BranchIf(cond1, "read1", "done")

			sumFn.Label("read1")
			arg1 := sumFn.VaArg("arg1", vir.I32, vir.Ident("va"))
			cur1 := sumFn.Load("cur1", vir.I32, sumPtr)
			sumFn.Store(vir.I32, sumPtr, sumFn.Add("add1", vir.I32, cur1, arg1))

			cond2 := sumFn.Emit("cond2", vir.OpSgt, vir.I32, vir.Ident("count"), vir.IntLiteral(1))
			sumFn.BranchIf(cond2, "read2", "done")

			sumFn.Label("read2")
			arg2 := sumFn.VaArg("arg2", vir.I32, vir.Ident("va"))
			cur2 := sumFn.Load("cur2", vir.I32, sumPtr)
			sumFn.Store(vir.I32, sumPtr, sumFn.Add("add2", vir.I32, cur2, arg2))

			cond3 := sumFn.Emit("cond3", vir.OpSgt, vir.I32, vir.Ident("count"), vir.IntLiteral(2))
			sumFn.BranchIf(cond3, "read3", "done")

			sumFn.Label("read3")
			arg3 := sumFn.VaArg("arg3", vir.I32, vir.Ident("va"))
			cur3 := sumFn.Load("cur3", vir.I32, sumPtr)
			sumFn.Store(vir.I32, sumPtr, sumFn.Add("add3", vir.I32, cur3, arg3))
			sumFn.Branch("done")

			sumFn.Label("done")
			sumFn.VaEnd(vir.Ident("va"))
			finalSum := sumFn.Load("final_sum", vir.I32, sumPtr)
			sumFn.Return(finalSum)

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			r := mainFn.Call("r", "sum_varargs", vir.IntLiteral(0))
			mainFn.Call("_", "printf", vir.Ident(fmtG.Name), r)
			mainFn.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(0),
	})
}