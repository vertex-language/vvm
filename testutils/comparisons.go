// comparisons.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// Signed vs. unsigned integer comparisons, and pointer comparisons (ir.md
// §4 "Comparisons"). The signed/unsigned pair here is the one place a
// negative-looking literal's comparison result genuinely depends on which
// opcode you use — everything else in this suite that reads like a
// comparison (br_if.go, select.go, switch.go) only ever needed the signed
// family, so unsigned never got directly exercised until now.

func init() {
	register(testCase{
		name:       "cmp_slt_treats_as_signed",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("cmp_slt_signed", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				cond := fb.Emit("cond", vir.OpSlt, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(1)) // -1 < 1
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name:       "cmp_ult_treats_as_unsigned",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("cmp_ult_unsigned", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				// Same bit pattern (-1 == 0xFFFFFFFF), opposite comparison
				// result once read as unsigned: 0xFFFFFFFF is not < 1.
				cond := fb.Emit("cond", vir.OpUlt, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(1))
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(0),
	})

	register(testCase{
		name:       "cmp_uge_treats_as_unsigned",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("cmp_uge_unsigned", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				cond := fb.Emit("cond", vir.OpUge, vir.I32, vir.IntLiteral(-1), vir.IntLiteral(1))
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name:       "cmp_ptr_eq_same_object",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("cmp_ptr_eq", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				p := fb.Alloca("p", vir.IntLiteral(4), 0)
				cond := fb.Emit("cond", vir.OpEq, vir.Ptr, p, p)
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})

	register(testCase{
		name:       "cmp_ptr_ne_different_objects",
		hostArches: []string{"x86_64"},
		hostOSes:   []string{"linux"},
		build: func(a, o string) *vir.Module {
			return i32PrintingModule("cmp_ptr_ne", a, o, func(fb *vir.FunctionBuilder) vir.Operand {
				p1 := fb.Alloca("p1", vir.IntLiteral(4), 0)
				p2 := fb.Alloca("p2", vir.IntLiteral(4), 0)
				cond := fb.Emit("cond", vir.OpNe, vir.Ptr, p1, p2)
				return fb.Emit("v", vir.OpSelect, vir.I32, cond, vir.IntLiteral(1), vir.IntLiteral(0))
			})
		},
		wantValue: val(1),
	})
}