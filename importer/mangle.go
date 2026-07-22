// mangle.go
package importer

import (
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
)

// MangledSymbol computes a module's length-prefixed Itanium-style export
// symbol (§6.3): each "/"-separated namespace component, then the module
// name, then the export name, each emitted as <length><text>, prefixed by
// "_M" — chosen specifically to avoid naive-concatenation collisions
// (module a_b export c vs. module a export b_c, both naively "a_b_c"):
//
//	MangledSymbol("acme/net", "http", "get") == "_M4acme3net4http3get"
//	MangledSymbol("", "mathlib", "add")       == "_M7mathlib3add"
//
// An empty namespace still produces an "_M..." mangled form here — it's
// SymbolForFunction/SymbolForGlobal, not this function, that implement
// §6.3's "no namespace -> bare symbol" rule by not calling MangledSymbol
// at all in that case.
func MangledSymbol(namespace, moduleName, exportName string) string {
	var b strings.Builder
	b.WriteString("_M")
	if namespace != "" {
		for _, part := range strings.Split(namespace, "/") {
			writeLengthPrefixed(&b, part)
		}
	}
	writeLengthPrefixed(&b, moduleName)
	writeLengthPrefixed(&b, exportName)
	return b.String()
}

func writeLengthPrefixed(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteString(s)
}

// SymbolForFunction returns the real symbol f's own module exports it
// under (§6.3, §2.2): entry/extern_c force a bare symbol regardless of
// namespace (carve-outs, never resolved by precedence since the two are
// mutually exclusive on one fn already, per ir/verify's checkFunctionAttrs);
// an unnamespaced module's exports are always bare; otherwise mangled.
// Mangling never depends on whether f is actually imported by anyone —
// callers only invoke this on exports, but nothing here checks Export.
func SymbolForFunction(m *vir.Module, f *vir.Function) string {
	if f.HasAttribute(vir.AttributeEntry) || f.HasAttribute(vir.AttributeExternC) {
		return f.Name
	}
	if m.Namespace == "" {
		return f.Name
	}
	return MangledSymbol(m.Namespace, m.Name, f.Name)
}

// SymbolForGlobal returns the real symbol g's own module exports it under
// (§6.3). Globals carry no entry/extern_c-style override — those are
// fn-only attributes — so only the namespace decides.
func SymbolForGlobal(m *vir.Module, g *vir.Global) string {
	if m.Namespace == "" {
		return g.Name
	}
	return MangledSymbol(m.Namespace, m.Name, g.Name)
}