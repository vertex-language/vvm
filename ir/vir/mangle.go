// mangle.go
package vir

import "strings"

// MangledSymbol computes the ABI-visible symbol for an exported fn/global
// (§6.3). entry/extern_c always force a bare symbol regardless of
// namespace (§2.2, §6.3 "Carve-outs"). Otherwise: bare if no namespace is
// declared, length-prefixed Itanium-style otherwise.
//
// Note on the README's two §6.3 examples: the `namespace "acme/net", module
// "http", export "get"` example mangles as documented below. The second
// example (`module "mathlib", export "add" -> _M7mathlib3add`) shows the
// scheme mangling with *no* namespace present, which conflicts with the
// explicit rule stated twice elsewhere ("No namespace: exports get a bare,
// unmangled C symbol", §2.2 and §6.3's own first bullet). This
// implementation follows the explicit rule text rather than that example;
// the example appears to illustrate the length-prefix formula in isolation
// rather than override the namespace gate.
func MangledSymbol(m *Module, exportName string, attrs []FunctionAttribute) string {
	for _, a := range attrs {
		if a == AttributeEntry || a == AttributeExternC {
			return exportName
		}
	}
	if m.Namespace == "" {
		return exportName
	}
	parts := append(strings.Split(m.Namespace, "/"), m.Name, exportName)
	return mangleParts(parts)
}

func mangleParts(parts []string) string {
	var sb strings.Builder
	sb.WriteString("_M")
	for _, p := range parts {
		sb.WriteString(itoa(len(p)))
		sb.WriteString(p)
	}
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}