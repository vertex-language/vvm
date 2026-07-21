// linkfile.go
package vir

import (
	"fmt"
	"strings"
)

// DeriveLinkFile computes the on-disk filename l implies for output format
// f, using exactly the same short/exact-name rules Verify enforces (§7.4):
// a name containing '.' or a path separator is exact and returned
// byte-for-byte; otherwise it's a short name expanded via the table in
// §7.4 (e.g. shared "SDL2" -> "libSDL2.so" on ELF).
//
// Exported so build-layer code (vvm's dispatch.go) can resolve the same
// filename this package's own Verify already validated, rather than
// re-implementing §7.4's derivation table a second time and risking the
// two definitions drifting apart.
func DeriveLinkFile(l *Link, f BinFormat) (string, error) {
	if isExactName(l.Name) {
		if err := checkExactExtension(l, f); err != nil {
			return "", err
		}
		return l.Name, nil
	}
	switch l.Kind {
	case LinkShared:
		switch f {
		case FormatELF:
			return "lib" + l.Name + ".so", nil
		case FormatMachO:
			return "lib" + l.Name + ".dylib", nil
		case FormatPE:
			return l.Name + ".dll", nil
		}
	case LinkStatic:
		if f == FormatPE {
			return l.Name + ".lib", nil
		}
		return "lib" + l.Name + ".a", nil
	case LinkFramework:
		return l.Name + ".framework/" + l.Name, nil
	}
	return "", fmt.Errorf("link %q: cannot derive filename", l.Name)
}

func isExactName(s string) bool {
	return strings.ContainsAny(s, "./\\")
}

func checkExactExtension(l *Link, f BinFormat) error {
	n := l.Name
	ok := false
	switch l.Kind {
	case LinkShared:
		switch f {
		case FormatELF:
			ok = strings.Contains(n, ".so") // .so plus optional version components
		case FormatMachO:
			ok = strings.HasSuffix(n, ".dylib")
		case FormatPE:
			ok = strings.HasSuffix(n, ".dll")
		}
	case LinkStatic:
		ok = strings.HasSuffix(n, ".a") || strings.HasSuffix(n, ".lib")
	}
	if !ok {
		return fmt.Errorf("link %s %q: extension does not agree with kind for target format (§7.4)", l.Kind, n)
	}
	return nil
}