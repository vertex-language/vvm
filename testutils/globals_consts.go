// globals_consts.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// const (compile-time scalar, yields its value as an operand) and global
// (mutable module storage, yields a pointer as an operand) — ir.md §1.3
// rule 7 and §8. Neither had a dedicated test before; every prior use of
// DeclareGlobal in this suite was just the printf format string.

func init() {
	register(testCase{
		name:       "const_used_as_operand",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(arch, osName string) *vir.Module {
			m := vir.NewModule("const_operand")
			m.SetTarget(arch, osName, abiFor(osName))
			m.DeclareConstant("Five", vir.I32, vir.IntLiteral(5))
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			v := fb.Add("v", vir.I32, vir.Ident("Five"), vir.IntLiteral(3))
			fb.Call("_", "printf", vir.Ident(fmtG.Name), v)
			fb.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(8),
	})

	register(testCase{
		name:       "global_store_load_roundtrip",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(arch, osName string) *vir.Module {
			m := vir.NewModule("global_roundtrip")
			m.SetTarget(arch, osName, abiFor(osName))
			counter := m.DeclareGlobal("counter", vir.I32, vir.InitLiteral{Value: vir.IntLiteral(0)})
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			// A `global` name in operand position yields its address (ptr).
			fb.Store(vir.I32, vir.Ident(counter.Name), vir.IntLiteral(1))
			cur := fb.Load("cur", vir.I32, vir.Ident(counter.Name))
			next := fb.Add("next", vir.I32, cur, vir.IntLiteral(41))
			fb.Store(vir.I32, vir.Ident(counter.Name), next)
			v := fb.Load("v", vir.I32, vir.Ident(counter.Name))
			fb.Call("_", "printf", vir.Ident(fmtG.Name), v)
			fb.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(42),
	})
}