// imports.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// checkImports validates §2.1 import-decls: non-empty, declared at most
// once. Whether the named module actually exists, or its bound name
// collides with anything, is importer's job entirely (verify.md).
func checkImports(m *vir.Module) error {
	seen := make(map[string]bool, len(m.Imports))
	for _, imp := range m.Imports {
		if imp.Path == "" {
			return fmt.Errorf("import: path must not be empty")
		}
		if seen[imp.Path] {
			return fmt.Errorf("import %q: declared more than once", imp.Path)
		}
		seen[imp.Path] = true
	}
	return nil
}