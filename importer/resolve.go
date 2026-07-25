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
//
// byPath is keyed by each imported module's own short name (target.Name),
// not by the raw import-path string. This matters because the operand
// grammar's qualified-ident (§2.3: `qualified-ident := ident "." ident`)
// only ever carries a single bare identifier before the dot — a call site
// writes `http.get`, never `acme/net/http.get`, even when the import
// declaration that brought "http" into scope was the fully namespaced
// `import "acme/net/http"` (see the package README's own worked example).
// `import.Path` is still what resolves against s.byID (the full,
// namespace-qualified module identity) — that part is unchanged; only the
// *local* key a call site's qualifier is looked up under changes, from
// the import string to the resolved target's own short name.
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
			alias := target.Name
			if existing, dup := byPath[alias]; dup && existing != target {
				return fmt.Errorf(
					"module %q: two imports (%q) both resolve to the local name %q — "+
						"a call/reference qualifier of %q would be ambiguous between them",
					m.Name, imp.Path, alias, alias)
			}
			byPath[alias] = target
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
//
// path here is a call-site qualifier — a bare identifier, per the operand
// grammar — so it's looked up against byPath's short-name keys, exactly
// as ResolveImports populated them above.
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