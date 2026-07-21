// struct_field.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Exercises field.ptr against a genuine two-field struct (ir.md §7.1
// layout, §4 field.ptr): allocate a Point-shaped slot, store into each
// field independently through its own field.ptr, then load one field back
// to confirm the offset computation landed on the right member and not,
// say, always on field zero.
func init() {
	register(testCase{
		name:       "struct_field_store_load",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(arch, osName string) *vir.Module {
			m := vir.NewModule("struct_field")
			m.SetTarget(arch, osName, abiFor(osName))
			m.DeclareStruct("Point", vir.Field{Name: "x", Type: vir.I32}, vir.Field{Name: "y", Type: vir.I32})
			m.DeclareLink(vir.LinkShared, "c")

			data := append([]byte("%d"), 0)
			fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
				vir.InitByteString{Data: data})

			ext := m.DeclareExternGroup("c")
			ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

			fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
			p := fb.Alloca("p", vir.IntLiteral(8), 0) // sizeof(Point): two i32 fields
			xPtr := fb.FieldPointer("xptr", p, "Point", "x")
			yPtr := fb.FieldPointer("yptr", p, "Point", "y")
			fb.Store(vir.I32, xPtr, vir.IntLiteral(11))
			fb.Store(vir.I32, yPtr, vir.IntLiteral(22))
			y := fb.Load("y", vir.I32, yPtr)
			fb.Call("_", "printf", vir.Ident(fmtG.Name), y)
			fb.Return(vir.IntLiteral(0))
			return m
		},
		wantValue: val(22),
	})
}