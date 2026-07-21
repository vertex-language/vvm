// memory_struct.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "alloca_store_load", build: func(a, o string) *vir.Module {
		return intPrintingModule("alloca_basic", func(fb *vir.FunctionBuilder) vir.Operand {
			slot := fb.Alloca("slot", vir.IntLiteral(4), 4)
			fb.Store(vir.I32, slot, vir.IntLiteral(99))
			return fb.Load("r", vir.I32, slot)
		})
	}, wantValue: val(99)})

	register(testCase{name: "struct_field_ptr", build: func(a, o string) *vir.Module {
		m := vir.NewModule("field_ptr")
		m.DeclareStruct("Point", vir.Field{Name: "x", Type: vir.I32}, vir.Field{Name: "y", Type: vir.I32})

		// "%d\x00" is 3 bytes: '%', 'd', NUL. No implicit NUL is added (§8),
		// so the array length must match exactly.
		fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 3},
			vir.InitByteString{Data: []byte("%d\x00")})
		ext := m.DeclareExternGroup("")
		ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

		fb := m.DeclareFunction("main", nil, vir.I32, true)
		p := fb.Alloca("p", vir.IntLiteral(8), 4) // 2×i32, no padding
		xPtr := fb.FieldPointer("xPtr", p, "Point", "x")
		yPtr := fb.FieldPointer("yPtr", p, "Point", "y")
		fb.Store(vir.I32, xPtr, vir.IntLiteral(3))
		fb.Store(vir.I32, yPtr, vir.IntLiteral(4))
		xv := fb.Load("xv", vir.I32, xPtr)
		yv := fb.Load("yv", vir.I32, yPtr)
		r := fb.Add("r", vir.I32, xv, yv)
		fb.Call("_", "printf", vir.Ident(fmtG.Name), r)
		fb.Return(vir.IntLiteral(0))
		return m
	}, wantValue: val(7)})

	register(testCase{name: "array_index_ptr", build: func(a, o string) *vir.Module {
		return intPrintingModule("index_ptr", func(fb *vir.FunctionBuilder) vir.Operand {
			arr := fb.Alloca("arr", vir.IntLiteral(16), 4)
			for i, v := range []int64{10, 20, 30, 40} {
				el := fb.IndexPointer("el", arr, vir.I32, vir.IntLiteral(int64(i)))
				fb.Store(vir.I32, el, vir.IntLiteral(v))
			}
			el2 := fb.IndexPointer("el2", arr, vir.I32, vir.IntLiteral(2))
			return fb.Load("r", vir.I32, el2)
		})
	}, wantValue: val(30)})
}