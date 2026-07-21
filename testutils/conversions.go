// conversions.go
package main

import "github.com/vertex-language/vvm/ir/vir"

func init() {
	register(testCase{name: "sext_zext_trunc", build: func(a, o string) *vir.Module {
		return intPrintingModule("conversions", func(fb *vir.FunctionBuilder) vir.Operand {
			neg8 := fb.Emit("neg8", "mov", vir.I8, vir.IntLiteral(-1))
			sx := fb.Emit("sx", "sext", vir.I32, neg8) // -1
			z8 := fb.Emit("z8", "mov", vir.I8, vir.IntLiteral(-1))
			zx := fb.Emit("zx", "zext", vir.I32, z8) // 255
			trunced := fb.Emit("t", "trunc", vir.I8, vir.IntLiteral(257)) // 1
			trz := fb.Emit("trz", "zext", vir.I32, trunced)
			sum := fb.Add("s1", vir.I32, sx, zx)
			return fb.Add("r", vir.I32, sum, trz) // 254 + 1 = 255
		})
	}, wantValue: val(255)})

	register(testCase{name: "stoint_sat_clamps", build: func(a, o string) *vir.Module {
		return intPrintingModule("stoint_sat", func(fb *vir.FunctionBuilder) vir.Operand {
			return fb.Emit("r", "stoint_sat", vir.I32, vir.FloatLiteral(1e30))
		})
	}, wantValue: val(2147483647)})

	register(testCase{name: "bitcast_ptr_roundtrip", build: func(a, o string) *vir.Module {
		return intPrintingModule("bitcast_ptr", func(fb *vir.FunctionBuilder) vir.Operand {
			slot := fb.Alloca("slot", vir.IntLiteral(4), 4)
			asInt := fb.Emit("asInt", "bitcast", vir.I64, slot)
			backToPtr := fb.Emit("backToPtr", "bitcast", vir.Ptr, asInt)
			fb.Store(vir.I32, backToPtr, vir.IntLiteral(77))
			return fb.Load("r", vir.I32, slot)
		})
	}, wantValue: val(77)})
}