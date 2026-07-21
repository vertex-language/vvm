// conversions.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Integer-domain conversions: trunc, sext vs. zext (same source bit
// pattern, deliberately different results), and the ptr<->usize-int
// bitcast round trip (ir.md §4 "Conversions", §6.2 "pointer↔integer
// bitcast round-trips exactly"). Float-crossing conversions (sfromint,
// stoint_sat, etc.) live in floats.go instead, since they only make sense
// paired with a float producer/consumer.

func init() {
	register(testCase{
		name:       "convert_trunc",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("convert_trunc", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Emit("v", "mov", vir.I32, vir.IntLiteral(300))
				t := fb.Emit("t", "trunc", vir.I8, v) // 300 mod 256 = 44
				return fb.Emit("z", "zext", vir.I32, t)
			})
		},
		wantValue: val(44),
	})

	// Same i8 bit pattern (0xFB), read two different ways.
	register(testCase{
		name:       "convert_sext_preserves_sign",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("convert_sext", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Emit("v", "mov", vir.I8, vir.IntLiteral(-5))
				return fb.Emit("s", "sext", vir.I32, v)
			})
		},
		wantValue: val(-5),
	})

	register(testCase{
		name:       "convert_zext_treats_as_unsigned",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("convert_zext", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				v := fb.Emit("v", "mov", vir.I8, vir.IntLiteral(-5)) // same bits as convert_sext
				return fb.Emit("z", "zext", vir.I32, v)
			})
		},
		wantValue: val(251),
	})

	register(testCase{
		name:       "convert_ptr_int_bitcast_roundtrip",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("convert_ptr_bitcast", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				p := fb.Alloca("p", vir.IntLiteral(4), 0)
				asInt := fb.Emit("as_int", "bitcast", vir.I64, p) // x86_64 usize = 64 bits
				asPtr := fb.Emit("as_ptr", "bitcast", vir.Ptr, asInt)
				fb.Store(vir.I32, asPtr, vir.IntLiteral(77))
				return fb.Load("v", vir.I32, p)
			})
		},
		wantValue: val(77),
	})
}