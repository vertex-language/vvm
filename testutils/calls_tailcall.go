// calls_tailcall.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "tailcall_accumulator", build: func(a, o string) *vir.Module {
		m := vir.NewModule("tailcall_fact")

		facts := m.DeclareFunction("facts",
			[]vir.Param{{Name: "n", Type: vir.I32}, {Name: "acc", Type: vir.I32}},
			vir.I32, false)
		cond := facts.Emit("cond", "sle", vir.I32, vir.Ident("n"), vir.IntLiteral(1))
		facts.BranchIf(cond, "base", "rec")
		facts.Label("base")
		facts.Return(vir.Ident("acc"))
		facts.Label("rec")
		n1 := facts.Sub("n1", vir.I32, vir.Ident("n"), vir.IntLiteral(1))
		acc1 := facts.Mul("acc1", vir.I32, vir.Ident("acc"), vir.Ident("n"))
		facts.TailCall("facts", n1, acc1)

		// "%d\x00" is 3 bytes: '%', 'd', NUL. No implicit NUL is added (§8),
		// so the array length must match exactly.
		fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 3},
			vir.InitByteString{Data: []byte("%d\x00")})
		ext := m.DeclareExternGroup("")
		ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

		mainFB := m.DeclareFunction("main", nil, vir.I32, true)
		r := mainFB.Call("r", "facts", vir.IntLiteral(5), vir.IntLiteral(1))
		mainFB.Call("_", "printf", vir.Ident(fmtG.Name), r)
		mainFB.Return(vir.IntLiteral(0))
		return m
	}, wantValue: val(120)})
}