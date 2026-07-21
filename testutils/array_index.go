// array_index.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Exercises index.ptr against a stack-allocated array[i32,4]: computes
// p + i*sizeof(T) (ir.md §4) for two different indices and confirms the
// one actually read back is the one that was stored there, not index 0.
func init() {
	register(testCase{
		name:       "array_index_store_load",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("array_index", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
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
}