// helpers.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// abiFor picks the canonical ABI (ir.md §10.3) that pairs with osName for
// this suite's link-libc pattern. Every test in this package is currently
// gated to hostArches:["x86_64"], hostOSes:["linux"] (the only combo with
// a registered entry thunk today — see entrythunk.go / asm_exit.go), so
// the "linux" branch is the only one actually exercised right now; the
// others exist so this stays correct the day a second entry thunk lands,
// instead of silently mismatching the ABI at that point.
func abiFor(osName string) string {
	switch osName {
	case "macos":
		return "macho"
	case "windows":
		return "msvc"
	default:
		return "gnu"
	}
}

// printerModule builds the smallest module capable of computing one value
// via body and printing it with format. It declares its libc dependency
// explicitly (link shared "c" + a matching extern "c" group) — there is
// no anonymous/default-namespace extern group (ir.md §1.2 rule 9, §9.9;
// verify.go rejects an empty Dependency outright) — and marks "main" with
// the entry attribute so resolveEntryPoint's libc-aware _start synthesis
// (entrythunk.go) actually fires and flushes stdio before the process
// exits.
func printerModule(name, arch, osName, format string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	m := vir.NewModule(name)
	m.SetTarget(arch, osName, abiFor(osName))
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
	fb.Call("_", "printf", vir.Ident(fmtG.Name), result)
	fb.Return(vir.IntLiteral(0))
	return m
}

// i32PrintingModule prints an i32-typed result with "%d".
func i32PrintingModule(name, arch, osName string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	return printerModule(name, arch, osName, "%d", body)
}

// i64PrintingModule prints an i64-typed result with "%lld". Plain "%d"
// only reads the low 32 bits of a variadic argument — it would silently
// truncate any value outside i32 range — so a genuine i64 test must use
// the wide conversion specifier instead.
func i64PrintingModule(name, arch, osName string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	return printerModule(name, arch, osName, "%lld", body)
}