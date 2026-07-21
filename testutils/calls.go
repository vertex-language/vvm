// calls.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Direct recursion, tailcalls (direct + indirect), and the byval/sret C
// ABI attributes (ir.md §7.2). None of these can go through helpers.go's
// printerModule, since that helper only ever declares one function
// ("main") — here a callee has to be declared before main, or main isn't
// the function whose return code matters.

func init() {
	// factorial recurses via an ordinary "call", not "tailcall" — direct
	// self-recursion inside one fn body is legal (§1.2 rule 3) unlike
	// mutual recursion between two fns, which needs the global-slot
	// workaround described there.
	register(testCase{
		name:       "call_recursion_factorial",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(arch, osName string) *vir.Module {
			m := vir.NewModule("recursion_factorial")
			m.SetTarget(arch, osName, abiFor(osName))
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			fact := m.DeclareFunction("factorial", []vir.Param{{Name: "n", Type: vir.I32}}, vir.I32, false)
			cond := fact.Emit("cond", "sle", vir.I32, vir.Ident("n"), vir.IntLiteral(1))
			fact.BranchIf(cond, "base", "rec")

			fact.Label("base")
			fact.Return(vir.IntLiteral(1))

			fact.Label("rec")
			nm1 := fact.Sub("nm1", vir.I32, vir.Ident("n"), vir.IntLiteral(1))
			subResult := fact.Call("sub_result", "factorial", nm1)
			result := fact.Mul("result", vir.I32, vir.Ident("n"), subResult)
			fact.Return(result)

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			r := mainFn.Call("r", "factorial", vir.IntLiteral(5))
			mainFn.Call("_", "printf", vir.Ident(fmtG.Name), r)
			mainFn.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(120), // 5!
	})

	// No libc — same raw-syscall exit path as process_exit.go — because
	// the fact under test is the tailcall terminator itself. "main" never
	// returns its own value; control transfers to "answer", whose return
	// becomes the process's exit code (§5 tailcall, §9.29).
	register(testCase{
		name:       "call_tailcall_direct",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			m := vir.NewModule("tailcall_direct")

			callee := m.DeclareFunction("answer", nil, vir.I32, false)
			callee.Return(vir.IntLiteral(55))

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			mainFn.TailCall("answer")

			return m
		},
		wantExit: 55,
	})

	// Companion to the direct case: same shape, but the callee is reached
	// through a fnsig-typed function pointer rather than by name (§9.29).
	// A bare fn name in operand position already yields its address as
	// ptr, so no separate global slot is needed to get the pointer.
	register(testCase{
		name:       "call_tailcall_indirect",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			m := vir.NewModule("tailcall_indirect")

			sig := m.DeclareFunctionSignature("answer_sig", nil, false, vir.I32)

			callee := m.DeclareFunction("answer", nil, vir.I32, false)
			callee.Return(vir.IntLiteral(77))

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			mainFn.TailCallIndirect(sig.Name, vir.Ident("answer"))
			return m
		},
		wantExit: 77,
	})

	// byval/sret (§7.2, §9.28): a byval[Point] param crosses the C boundary
	// as a pointer whose callee-side writes never affect the caller's
	// object; an sret[Point] param names the destination for a by-value
	// struct return (which is why the fn itself must return void).
	register(testCase{
		name:       "call_byval_and_sret",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(arch, osName string) *vir.Module {
			m := vir.NewModule("byval_sret")
			m.SetTarget(arch, osName, abiFor(osName))
			m.DeclareStruct("Point", vir.Field{Name: "x", Type: vir.I32}, vir.Field{Name: "y", Type: vir.I32})
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			sumPoint := m.DeclareFunction("sum_point",
				[]vir.Param{{Name: "p", Type: vir.Ptr, ByVal: "Point"}}, vir.I32, false)
			xp := sumPoint.FieldPointer("xp", vir.Ident("p"), "Point", "x")
			yp := sumPoint.FieldPointer("yp", vir.Ident("p"), "Point", "y")
			x := sumPoint.Load("x", vir.I32, xp)
			y := sumPoint.Load("y", vir.I32, yp)
			sumPoint.Return(sumPoint.Add("sum", vir.I32, x, y))

			makePoint := m.DeclareFunction("make_point", []vir.Param{
				{Name: "out", Type: vir.Ptr, SRet: "Point"},
				{Name: "x", Type: vir.I32},
				{Name: "y", Type: vir.I32},
			}, vir.Void, false)
			oxp := makePoint.FieldPointer("oxp", vir.Ident("out"), "Point", "x")
			oyp := makePoint.FieldPointer("oyp", vir.Ident("out"), "Point", "y")
			makePoint.Store(vir.I32, oxp, vir.Ident("x"))
			makePoint.Store(vir.I32, oyp, vir.Ident("y"))
			makePoint.Return()

			mainFn := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			pt := mainFn.Alloca("pt", vir.IntLiteral(8), 0)
			ptXp := mainFn.FieldPointer("ptxp", pt, "Point", "x")
			ptYp := mainFn.FieldPointer("ptyp", pt, "Point", "y")
			mainFn.Store(vir.I32, ptXp, vir.IntLiteral(3))
			mainFn.Store(vir.I32, ptYp, vir.IntLiteral(4))
			sum := mainFn.Call("sum", "sum_point", pt) // 3 + 4 = 7

			dest := mainFn.Alloca("dest", vir.IntLiteral(8), 0)
			mainFn.Call("", "make_point", dest, vir.IntLiteral(9), vir.IntLiteral(33)) // void call: no result name
			destY := mainFn.FieldPointer("desty", dest, "Point", "y")
			y2 := mainFn.Load("y2", vir.I32, destY) // 33

			total := mainFn.Add("total", vir.I32, sum, y2) // 7 + 33 = 40
			mainFn.Call("_", "printf", vir.Ident(fmtG.Name), total)
			mainFn.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(40),
	})
}