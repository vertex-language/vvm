// alloca.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Round-trips a value through a stack slot: alloca.ptr, store, load. Per
// ir.md §4, an alloca's slot lives for the whole enclosing invocation, so a
// single slot allocated once in straight-line code is safe to store into
// and load back from — the "fresh slot per execution" rule only matters
// once alloca sits inside a loop, which isn't the case here.
func init() {
	register(testCase{
		name:       "alloca_store_load_roundtrip",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("alloca_roundtrip", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0) // 4 bytes for one i32
				fb.Store(vir.I32, slot, vir.IntLiteral(456))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(456),
	})
}