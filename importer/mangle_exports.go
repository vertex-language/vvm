// mangle_exports.go
package vvm

import (
	"github.com/vertex-language/vvm/importer"
	"github.com/vertex-language/vvm/ir/vir"
)

// applyExportMangling renames m's own exported fn/global declarations to
// the real symbol §6.3 says their module exports them under, and rewrites
// every unqualified (same-module) reference to the old name so local
// calls/operands keep resolving correctly.
//
// Why this has to exist at all: lower/<arch> and object/<arch> are
// deliberately import-agnostic (they never import this package, and
// emit whatever Function.Name/Global.Name literally says as the real
// ABI symbol). importer.Rewrite already computes the correct mangled
// string and embeds it into every *caller's* rewritten call site — but
// nothing renames the *declaring* module's own symbol to match, so a
// namespaced module's real object-file export stayed bare while every
// caller now expected the mangled form. This pass is what makes both
// sides agree once they reach lower/<arch>.
//
// Ordering: for a multi-module graph build, this must run *after*
// importer.Set.Rewrite, not before — Rewrite's own mangledCallTarget/
// rewriteQualifiedOperand resolve a cross-module callee by matching the
// call site's short qualifier (e.g. "writeline") against the target
// module's *original* Function.Name; renaming that first would break the
// lookup. For a single-module build (Build/BuildModule, no `import` at
// all), there's no such ordering constraint — but the module still needs
// this pass, since a namespaced module built standalone must still emit
// the same mangled symbol any other program's future `import` of it will
// expect (§6.3 mangling isn't conditional on this particular build being
// multi-module).
func applyExportMangling(m *vir.Module) {
	if m.Namespace == "" {
		return // §6.3: no namespace -> exports stay bare, nothing to rename
	}

	renameFn := make(map[string]string)
	renameGlobal := make(map[string]string)

	for _, f := range m.Functions {
		if !f.Export {
			continue
		}
		newName := importer.SymbolForFunction(m, f)
		if newName == f.Name {
			continue // entry/extern_c carve-out (§2.2/§6.3): stays bare
		}
		renameFn[f.Name] = newName
		f.Name = newName
	}
	for _, g := range m.Globals {
		if !g.Export {
			continue
		}
		newName := importer.SymbolForGlobal(m, g)
		if newName == g.Name {
			continue
		}
		renameGlobal[g.Name] = newName
		g.Name = newName
	}
	if len(renameFn) == 0 && len(renameGlobal) == 0 {
		return
	}

	renameOperand := func(op *vir.Operand) {
		if op == nil || op.Kind != vir.OperandIdent || op.IsQualified() {
			return // qualified operands were already handled by importer.Rewrite
		}
		if n, ok := renameFn[op.Ident]; ok {
			op.Ident = n
			return
		}
		if n, ok := renameGlobal[op.Ident]; ok {
			op.Ident = n
		}
	}

	for _, f := range m.Functions {
		for _, b := range f.AllBlocks() {
			for _, line := range b.Lines {
				for i := range line.Args {
					renameOperand(&line.Args[i])
				}
			}
			switch t := b.Term.(type) {
			case vir.BranchIf:
				renameOperand(&t.Cond)
				b.Term = t
			case vir.Switch:
				renameOperand(&t.Value)
				b.Term = t
			case vir.Return:
				if t.Value != nil {
					renameOperand(t.Value)
				}
				b.Term = t
			case vir.TailCall:
				// t.Sig == "" is the direct form (grammar: "tailcall" ident
				// ...) — Callee is a plain local ident naming a fn in this
				// module. The indirect form (Sig != "") carries no Callee
				// name at all; its fn-pointer operand is Args[0], already
				// covered by the loop above.
				if t.Sig == "" {
					if n, ok := renameFn[t.Callee]; ok {
						t.Callee = n
					}
				}
				for i := range t.Args {
					renameOperand(&t.Args[i])
				}
				b.Term = t
			}
		}
	}

	// Global initializers can name an earlier fn/global via `addr ident`
	// (§6.2) — the only ConstInit form that references anything by name.
	for _, g := range m.Globals {
		g.Init = renameConstInit(g.Init, renameFn, renameGlobal)
	}
}

func renameConstInit(init vir.ConstInit, renameFn, renameGlobal map[string]string) vir.ConstInit {
	switch x := init.(type) {
	case vir.InitAddressOf:
		if n, ok := renameFn[x.Name]; ok {
			return vir.InitAddressOf{Name: n}
		}
		if n, ok := renameGlobal[x.Name]; ok {
			return vir.InitAddressOf{Name: n}
		}
		return x
	case vir.InitAggregate:
		for i, e := range x.Elems {
			x.Elems[i] = renameConstInit(e, renameFn, renameGlobal)
		}
		return x
	}
	return init
}