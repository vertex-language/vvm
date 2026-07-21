// vectors.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "vector_add_reduce", build: func(a, o string) *vir.Module {
		return intPrintingModule("vec_add_reduce", func(fb *vir.FunctionBuilder) vir.Operand {
			vt := vir.VecType{Elem: vir.I32, Len: 4}
			va := fb.Emit("va", "mov", vt, vir.VectorLiteral(1, 2, 3, 4))
			vb := fb.Emit("vb", "mov", vt, vir.VectorLiteral(10, 20, 30, 40))
			vsum := fb.Emit("vsum", "add", vt, va, vb)
			return fb.Emit("total", "reduce_add", vt, vsum) // 110
		})
	}, wantValue: val(110)})

	register(testCase{name: "vector_extract_shuffle", build: func(a, o string) *vir.Module {
		return intPrintingModule("vec_shuffle", func(fb *vir.FunctionBuilder) vir.Operand {
			vt := vir.VecType{Elem: vir.I32, Len: 4}
			va := fb.Emit("va", "mov", vt, vir.VectorLiteral(1, 2, 3, 4))
			vb := fb.Emit("vb", "mov", vt, vir.VectorLiteral(10, 20, 30, 40))
			shuf := fb.Emit("shuf", "shuffle", vir.VecType{Elem: vir.I32, Len: 2}, va, vb,
				vir.VectorLiteral(1, 4)) // a[1], b[0]
			return fb.Emit("r", "reduce_add", vir.VecType{Elem: vir.I32, Len: 2}, shuf)
		})
	}, wantValue: val(12)})
}