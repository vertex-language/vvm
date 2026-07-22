// shapes.go
package graph

import (
	"fmt"

	vmetabinary "github.com/vertex-language/vvm/format/vmeta/binary"
	vir "github.com/vertex-language/vvm/ir/vir"
)

// ShapesForImports returns the subset of modules that key directly
// imports, keyed exactly the way vir.VerifyWithImports wants
// (map[string]*vir.ModuleShape). This exists so a Stage A caller (vvm's
// own build orchestration) doesn't have to re-walk modules[key].Imports
// by hand at every VerifyWithImports call site — ResolveImportGraph
// already validated every import resolves and is acyclic, so this never
// needs to re-check either.
//
// key must be a module ResolveImportGraph has already validated (or
// would validate) against the same modules map; calling this on an
// unvalidated map re-derives the same "no .vmeta supplied" error
// ResolveImportGraph itself would, but does not re-check cycles or
// self-imports.
func ShapesForImports(modules map[string]*vmetabinary.Result, key string) (map[string]*vir.ModuleShape, error) {
	res, ok := modules[key]
	if !ok {
		return nil, fmt.Errorf("graph: no .vmeta supplied for %q", key)
	}
	if len(res.Imports) == 0 {
		return nil, nil
	}
	out := make(map[string]*vir.ModuleShape, len(res.Imports))
	for _, imp := range res.Imports {
		dep, ok := modules[imp]
		if !ok {
			return nil, fmt.Errorf("graph: %q imports %q, but no .vmeta was supplied for it", key, imp)
		}
		if dep.Shape == nil {
			return nil, fmt.Errorf("graph: %q imports %q, but its supplied .vmeta has no shape data", key, imp)
		}
		out[imp] = dep.Shape
	}
	return out, nil
}