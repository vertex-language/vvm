// helpers.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// abiFor picks the canonical ABI (ir.md §10.3) that pairs with the
// package-level osName. There is no per-call osName parameter any more —
// osName is resolved once in run() before any test case builds a module.
func abiFor() string {
	switch osName {
	case "macos":
		return "macho"
	case "windows":
		return "msvc"
	default:
		return "gnu"
	}
}

// identity materializes a literal (or any operand) as a named value of
// type t. §4's opcode vocabulary is closed and has no bare "mov"/identity
// op, so this uses the same idiom real .vir code would: add the value to
// a same-typed zero, which is the identity element for both integer and
// float add (OpAdd is ConstraintIntOrFloat, so it accepts either).
func identity(fb *vir.FunctionBuilder, name string, t vir.Type, v vir.Operand) vir.Operand {
	zero := vir.Operand(vir.IntLiteral(0))
	if vir.IsFloat(t) {
		zero = vir.FloatLiteral(0)
	}
	return fb.Emit(name, vir.OpAdd, t, v, zero)
}

// printerModuleWith builds the smallest module capable of computing one
// value via body, optionally converting it via conv (e.g. an f32->f64
// fpromote for variadic promotion), and printing it with format. It
// declares its libc dependency explicitly (link shared "c" + a matching
// extern "c" group) — there is no anonymous/default-namespace extern group
// (ir.md §1.2 rule 9, §9.9) — and marks "main" with the entry attribute so
// resolveEntryPoint's libc-aware crt-stub synthesis actually fires and
// flushes stdio before the process exits. Targets the package-level
// arch/osName — the one target this whole run builds against.
func printerModuleWith(name, format string, body func(fb *vir.FunctionBuilder) vir.Operand, conv func(fb *vir.FunctionBuilder, v vir.Operand) vir.Operand) *vir.Module {
	m := vir.NewModule(name)
	m.SetTarget(arch, osName, abiFor())
	m.DeclareLink(vir.LinkShared, "c")

	// No implicit NUL is added to a byte-string initializer (§8); the
	// array length must match the literal byte count exactly.
	data := append([]byte(format), 0)
	fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: len(data)},
		vir.InitByteString{Data: data})

	ext := m.DeclareExternGroup("c")
	ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

	fb := m.DeclareFunction("main", nil, vir.I32, true, vir.AttributeEntry)
	result := body(fb)
	if conv != nil {
		result = conv(fb, result)
	}
	fb.Call("_", "printf", vir.Ident(fmtG.Name), result)
	fb.Return(vir.IntLiteral(0))
	return m
}

func printerModule(name, format string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	return printerModuleWith(name, format, body, nil)
}

// i32PrintingModule prints an i32-typed result with "%d".
func i32PrintingModule(name string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	return printerModule(name, "%d", body)
}

// i64PrintingModule prints an i64-typed result with "%lld". Plain "%d"
// only reads the low 32 bits of a variadic argument — it would silently
// truncate any value outside i32 range — so a genuine i64 test must use
// the wide conversion specifier instead.
func i64PrintingModule(name string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	return printerModule(name, "%lld", body)
}

// f64PrintingModule prints an f64-typed result with "%f". No promotion
// needed: a variadic float argument must already be double-width (ir.md §4
// "Variadic Calls" — manual promotion, zero implicit conversions), and f64
// already is.
func f64PrintingModule(name string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	return printerModuleWith(name, "%f", body, nil)
}

// f32PrintingModule prints an f32-typed result with "%f". Unlike the f64
// case, this crosses the C variadic boundary as an f32, which is illegal
// without the manual fpromote §4 requires — so this helper inserts that
// promotion itself, once, instead of every f32 test case having to
// remember it.
func f32PrintingModule(name string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	return printerModuleWith(name, "%f", body, func(fb *vir.FunctionBuilder, v vir.Operand) vir.Operand {
		return fb.Emit("vpromoted", vir.OpFpromote, vir.F64, v)
	})
}