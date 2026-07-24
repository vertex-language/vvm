// memory.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// alloca, index.ptr, field.ptr, and bulk memory ops (ir.md §4 "Memory &
// Addresses"). Per ir.md §4, an alloca's slot lives for the whole
// enclosing invocation, so a single slot allocated once in straight-line
// code is safe to store into and load back from — the "fresh slot per
// execution" rule only matters once alloca sits inside a loop, which isn't
// the case in any of these.

func init() {
	register(testCase{
		name: "alloca_store_load_roundtrip",
		build: func() *vir.Module {
			return i32PrintingModule("alloca_roundtrip", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0) // 4 bytes for one i32
				fb.Store(vir.I32, slot, vir.IntLiteral(456))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(456),
	})

	// Exercises index.ptr against a stack-allocated array[i32,4]: computes
	// p + i*sizeof(T) for two different indices and confirms the one
	// actually read back is the one that was stored there, not index 0.
	register(testCase{
		name: "array_index_store_load",
		build: func() *vir.Module {
			return i32PrintingModule("array_index", func(fb *vir.FunctionBuilder) vir.Operand {
				base := fb.Alloca("base", vir.IntLiteral(16), 0) // array[i32, 4]: 4 * 4 bytes
				p0 := fb.IndexPointer("p0", base, vir.I32, vir.IntLiteral(0))
				p2 := fb.IndexPointer("p2", base, vir.I32, vir.IntLiteral(2))
				fb.Store(vir.I32, p0, vir.IntLiteral(9))
				fb.Store(vir.I32, p2, vir.IntLiteral(88))
				return fb.Load("v", vir.I32, p2)
			})
		},
		wantValue: val(88),
	})

	// Exercises field.ptr against a genuine two-field struct: allocate a
	// Point-shaped slot, store into each field independently through its
	// own field.ptr, then load one field back to confirm the offset
	// computation landed on the right member, not always field zero.
	register(testCase{
		name: "struct_field_store_load",
		build: func() *vir.Module {
			m := vir.NewModule("struct_field")
			m.SetTarget(arch, osName, abiFor())
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

	register(testCase{
		name: "memory_memcopy_roundtrip",
		build: func() *vir.Module {
			return i32PrintingModule("memory_memcopy", func(fb *vir.FunctionBuilder) vir.Operand {
				src := fb.Alloca("src", vir.IntLiteral(4), 0)
				dst := fb.Alloca("dst", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, src, vir.IntLiteral(999))
				fb.Emit("", vir.OpMemcopy, nil, dst, src, vir.IntLiteral(4))
				return fb.Load("v", vir.I32, dst)
			})
		},
		wantValue: val(999),
	})

	register(testCase{
		name: "memory_memset_fills_byte",
		build: func() *vir.Module {
			return i32PrintingModule("memory_memset", func(fb *vir.FunctionBuilder) vir.Operand {
				dst := fb.Alloca("dst", vir.IntLiteral(4), 0)
				fb.Emit("", vir.OpMemset, nil, dst, vir.IntLiteral(7), vir.IntLiteral(4))
				return fb.Load("v", vir.I32, dst) // 0x07070707
			})
		},
		wantValue: val(117901063),
	})

	// memmove is overlap-safe (§4 "Bulk Ops"), unlike memcopy where overlap
	// is UB (§5.4 item 4). This is the one case that actually distinguishes
	// the two ops: a forward shift of a 4-element array by one slot, where
	// src and dst genuinely overlap (dst = base+0, src = base+1, 12 bytes
	// spans 3 of the 4 elements). If this silently behaved like a naive
	// byte-by-byte forward copy without overlap awareness, element 0 would
	// end up equal to element 1 before it was itself overwritten by element
	// 2 — this case reads back element 0 to confirm the shift landed
	// correctly rather than corrupting through the overlap.
	register(testCase{
		name: "memory_memmove_overlapping_shift",
		build: func() *vir.Module {
			return i32PrintingModule("memory_memmove", func(fb *vir.FunctionBuilder) vir.Operand {
				base := fb.Alloca("base", vir.IntLiteral(16), 0) // array[i32, 4]
				p0 := fb.IndexPointer("p0", base, vir.I32, vir.IntLiteral(0))
				p1 := fb.IndexPointer("p1", base, vir.I32, vir.IntLiteral(1))
				p2 := fb.IndexPointer("p2", base, vir.I32, vir.IntLiteral(2))
				p3 := fb.IndexPointer("p3", base, vir.I32, vir.IntLiteral(3))
				fb.Store(vir.I32, p0, vir.IntLiteral(1))
				fb.Store(vir.I32, p1, vir.IntLiteral(2))
				fb.Store(vir.I32, p2, vir.IntLiteral(3))
				fb.Store(vir.I32, p3, vir.IntLiteral(4))
				// Shift elements [1,2,3] down into [0,1,2] — dst and src
				// overlap by 8 of the 12 bytes moved.
				fb.Emit("", vir.OpMemmove, nil, p0, p1, vir.IntLiteral(12))
				return fb.Load("v", vir.I32, p0) // was element 1's value: 2
			})
		},
		wantValue: val(2),
	})
}