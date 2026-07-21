// recursion.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// factorial recurses via an ordinary "call", not "tailcall" — direct
// self-recursion inside one fn body is legal per ir.md §1.2 rule 3 (unlike
// mutual recursion between two fns, which needs the global-slot workaround
// described there). This can't be built with helpers.go's printerModule,
// since that helper only ever declares one function ("main"); here a
// callee has to be declared before main.
func init() {
	register(testCase{
		name:       "recursion_factorial",
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

			// fn factorial(n i32) i32:
			//   cond = sle.i32 n, 1
			//   br_if cond, base, rec
			// base:  return 1
			// rec:   nm1 = sub n, 1
			//        sub_result = call factorial, nm1
			//        return mul n, sub_result
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
}