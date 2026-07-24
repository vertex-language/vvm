// globals_consts.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// const (compile-time scalar, yields its value as an operand) and global
// (mutable module storage, yields a pointer as an operand) — ir.md §1.3
// rule 7 and §8. Neither had a dedicated test before; every prior use of
// DeclareGlobal in this suite was just the printf format string.
//
// Beyond the plain-literal/byte-string cases below, §6.2's ConstInit
// grammar also allows `zero`, `addr ident`, and aggregate lists — each
// gets its own case here since none was covered before.

func init() {
	register(testCase{
		name: "const_used_as_operand",
		build: func() *vir.Module {
			m := vir.NewModule("const_operand")
			m.SetTarget(arch, osName, abiFor())
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
		name: "global_store_load_roundtrip",
		build: func() *vir.Module {
			m := vir.NewModule("global_roundtrip")
			m.SetTarget(arch, osName, abiFor())
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

	// InitZero (§6.2 "zero"): a fresh global with no explicit value must
	// read back as zero without anything ever storing into it first —
	// distinct from global_store_load_roundtrip, which only reads back a
	// value this test's own function wrote.
	register(testCase{
		name: "global_zero_init_reads_as_zero",
		build: func() *vir.Module {
			m := vir.NewModule("global_zero_init")
			m.SetTarget(arch, osName, abiFor())
			g := m.DeclareGlobal("zeroed", vir.I32, vir.InitZero{})
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			cur := fb.Load("cur", vir.I32, vir.Ident(g.Name))
			v := fb.Add("v", vir.I32, cur, vir.IntLiteral(7)) // 0 + 7, confirms cur really was 0
			fb.Call("_", "printf", vir.Ident(fmtG.Name), v)
			fb.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(7),
	})

	// InitAddressOf (§6.2 "addr ident"): a ptr-typed global relocated to
	// point at an earlier global, dereferenced through that relocation
	// rather than through the pointee's own name.
	register(testCase{
		name: "global_addr_of_another_global",
		build: func() *vir.Module {
			m := vir.NewModule("global_addr_of")
			m.SetTarget(arch, osName, abiFor())
			pointee := m.DeclareGlobal("pointee", vir.I32, vir.InitLiteral{Value: vir.IntLiteral(55)})
			ptrG := m.DeclareGlobal("ptr_to_pointee", vir.Ptr, vir.InitAddressOf{Name: pointee.Name})
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			relocated := fb.Load("relocated", vir.Ptr, vir.Ident(ptrG.Name)) // load the address itself
			v := fb.Load("v", vir.I32, relocated)                           // then dereference it
			fb.Call("_", "printf", vir.Ident(fmtG.Name), v)
			fb.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(55),
	})

	// InitAggregate (§6.2 "(" const-init ("," const-init)* ")"): an
	// array[i32,3] global initialized with a literal list, indexed to
	// confirm each element landed at its own offset rather than all
	// collapsing to the first.
	register(testCase{
		name: "global_aggregate_init_array",
		build: func() *vir.Module {
			m := vir.NewModule("global_aggregate_init")
			m.SetTarget(arch, osName, abiFor())
			arr := m.DeclareGlobal("arr", vir.ArrayType{Elem: vir.I32, Len: 3}, vir.InitAggregate{
				Elems: []vir.ConstInit{
					vir.InitLiteral{Value: vir.IntLiteral(10)},
					vir.InitLiteral{Value: vir.IntLiteral(20)},
					vir.InitLiteral{Value: vir.IntLiteral(30)},
				},
			})
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			p1 := fb.IndexPointer("p1", vir.Ident(arr.Name), vir.I32, vir.IntLiteral(1))
			v := fb.Load("v", vir.I32, p1) // want the middle element, 20
			fb.Call("_", "printf", vir.Ident(fmtG.Name), v)
			fb.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(20),
	})
}