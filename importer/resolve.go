// resolve.go
package importer

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// ResolveImports maps every `import "X"` in every module to the real
// *vir.Module it names. A pure name lookup — it only ever reads
// already-in-memory declarations, never bodies, so there's no scheduling
// step for it to choke on an import cycle (import cycles are legal and
// unhandled specially, per importer.md).
func (s *Set) ResolveImports() error {
	for _, m := range s.modules {
		byPath := make(map[string]*vir.Module, len(m.Imports))
		for _, imp := range m.Imports {
			target, ok := s.byID[imp.Path]
			if !ok {
				return fmt.Errorf("module %q: import %q does not resolve to any known module (§7.3)", m.Name, imp.Path)
			}
			if target == m {
				return fmt.Errorf("module %q: import %q names itself", m.Name, imp.Path)
			}
			byPath[imp.Path] = target
		}
		s.resolved[m] = byPath
	}
	return nil
}

// resolvedTarget looks up the real module a qualified-ident/StructType's
// import path names, within m's own already-resolved import table.
// Erroring here (rather than falling back to s.byID directly) is what
// enforces §2.2 declare-before-use for cross-module references: a path
// that resolves fine in s.byID but was never actually `import`-ed by m is
// still a violation.
func (s *Set) resolvedTarget(m *vir.Module, path string) (*vir.Module, error) {
	byPath, ok := s.resolved[m]
	if !ok {
		return nil, fmt.Errorf("module %q: ResolveImports has not run yet", m.Name)
	}
	target, ok := byPath[path]
	if !ok {
		return nil, fmt.Errorf("module %q: reference to %q, which was never declared with an import statement (§2.2)", m.Name, path)
	}
	return target, nil
}