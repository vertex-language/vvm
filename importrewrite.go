// importrewrite.go
package vvm

import (
	"fmt"

	vir "github.com/vertex-language/vvm/ir/vir"
)

// crossModuleLinkPrefix marks a vir.Link/vir.ExternGroup synthesized by
// rewriteImports to satisfy vir.Verify's own link/extern-group pairing
// rule (verify.go: "extern group has no matching link declaration"), for
// a dependency that isn't a real system library at all — it's another
// vvm module in the same build graph, whose object file is attached to
// the same linker invocation via a sibling AddObject call
// (buildgraph.go). dispatch.go's addELFLinkDependencies skips any Link
// carrying this prefix rather than trying to resolve it as a real
// libX.a/libX.so.
const crossModuleLinkPrefix = "$vvm-import:"

// rewriteImports is vvm's implementation of the Stage B "rewrite" half
// described in ir.md §7.3's stage table: replacing every qualified
// (import-derived) reference in m with an ordinary construct
// lower/<arch> already knows how to lower — lower/<arch> is deliberately
// kept ignorant of imports entirely (vvm README, "Strict dependency
// boundaries": "lower/<arch> imports only ir/vir").
//
// shapes must be exactly m's own direct imports (graph.ShapesForImports
// gives this), and m must already have passed
// vir.VerifyWithImports(m, shapes) — rewriteImports trusts Stage A
// already confirmed every qualified reference resolves to a real export
// of the right kind; it only performs substitution, never re-derives
// that.
//
// Per kind, per §7.3's own description ("struct/const/fnsig references
// resolve/substitute in place; fn/global references become unresolved
// external references"):
//
//   - const: the qualified operand is replaced outright by the
//     exporter's literal value (inlined — a const never had runtime
//     storage to reference in the first place, §6.2).
//   - fn: the qualified operand becomes a plain ident naming a
//     synthesized `extern fn` declared under a per-import-path pseudo
//     dependency, with the exporter's real mangled symbol
//     (vir.MangledSymbol) as its extern name.
//   - struct/fnsig: never appear in operand position needing rewrite —
//     field.ptr's struct/field names are compile-time entities (skipped
//     below, matching verify.go's own entityArgPositions treatment) and
//     call.<fnsig> never carries a qualified Sig at all (§4.5's `<fnsig>`
//     is always self-referential or a local declare-before-use name).
//   - global: **not implemented** — see the returned error below.
func rewriteImports(m *vir.Module, shapes map[string]*vir.ModuleShape) error {
	rewriteOperand := func(o *vir.Operand) error {
		if o.Kind != vir.OperandIdent || o.Qualifier == "" {
			return nil
		}
		shape, ok := shapes[o.Qualifier]
		if !ok {
			// Stage A already verified this resolves; reaching here means
			// shapes wasn't actually built from the same import set m was
			// verified against — a vvm orchestration bug, not a
			// module-authoring error.
			return fmt.Errorf("%q: no shape supplied for import %q despite passing Stage A", o.Ident, o.Qualifier)
		}

		for _, c := range shape.Consts {
			if c.Name == o.Ident {
				*o = c.Value
				return nil
			}
		}

		for _, fn := range shape.Fns {
			if fn.Name == o.Ident {
				exporter := &vir.Module{Namespace: shape.Namespace, Name: shape.ModuleName}
				sym := vir.MangledSymbol(exporter, fn.Name, fn.Attrs)
				if err := ensureExternFn(m, o.Qualifier, sym, fn.Params, fn.Ret, fn.Variadic, fn.Attrs); err != nil {
					return err
				}
				o.Ident, o.Qualifier = sym, ""
				return nil
			}
		}

		for _, g := range shape.Globals {
			if g.Name == o.Ident {
				// KNOWN GAP: ir/vir's Global (module.go) always requires
				// a ConstInit (verify.go rejects a missing one) — there
				// is no "extern global, no local storage" grammar
				// production in the spec at all (§2.3's global-decl
				// always carries "= const-init"). A qualified reference
				// to an imported `global` therefore has no legal vir
				// construct to rewrite into today; fixing this needs a
				// new IR-level extern-global concept, not just plumbing
				// in vvm, so it's surfaced as an explicit, named error
				// rather than silently mis-lowered.
				return fmt.Errorf("%q: qualified reference to imported global %q.%s has no lowering target — ir/vir has no extern-global construct yet", o.Ident, o.Qualifier, g.Name)
			}
		}

		return fmt.Errorf("%q: not found as an exported const/fn/global of %q", o.Ident, o.Qualifier)
	}

	for _, f := range m.Functions {
		for _, b := range f.AllBlocks() {
			for i := range b.Lines {
				ln := &b.Lines[i]
				if ln.Instruction == nil {
					continue // asm blocks never carry cross-module operands (§4.4/§7.3 are disjoint)
				}
				skip := entitySkip(ln.Instruction)
				for ai := range ln.Instruction.Args {
					if skip[ai] {
						continue
					}
					if err := rewriteOperand(&ln.Instruction.Args[ai]); err != nil {
						return fmt.Errorf("fn %s: %w", f.Name, err)
					}
				}
			}
			switch t := b.Term.(type) {
			case vir.BranchIf:
				if err := rewriteOperand(&t.Cond); err != nil {
					return fmt.Errorf("fn %s: %w", f.Name, err)
				}
				b.Term = t
			case vir.Switch:
				if err := rewriteOperand(&t.Value); err != nil {
					return fmt.Errorf("fn %s: %w", f.Name, err)
				}
				b.Term = t
			case vir.Return:
				if t.Value != nil {
					if err := rewriteOperand(t.Value); err != nil {
						return fmt.Errorf("fn %s: %w", f.Name, err)
					}
				}
			case vir.TailCall:
				for ai := range t.Args {
					if err := rewriteOperand(&t.Args[ai]); err != nil {
						return fmt.Errorf("fn %s: %w", f.Name, err)
					}
				}
			}
		}
	}

	// Global initializers (`addr ident`) never legally name a qualified
	// import — the const-init grammar (§2.3) only ever takes a bare
	// ident for `addr`, no qualified-ident production — so there is
	// nothing to rewrite there.

	return nil
}

// entitySkip mirrors verify.go's own entityArgPositions for the one
// opcode whose operands name compile-time entities rather than runtime
// values in a position rewriteOperand must never touch: field.ptr's
// struct/field-name args. (OpCall's direct-callee arg is deliberately
// *not* skipped here, unlike verify.go's version — that's exactly the
// position rewriteImports needs to rewrite for a qualified call.)
func entitySkip(i *vir.Instruction) map[int]bool {
	if i.Op == vir.OpField {
		return map[int]bool{1: true, 2: true}
	}
	return nil
}

// ensureExternFn makes sure m has an ExternGroup for the synthesized
// cross-module dependency backing importPath (declaring a matching Link
// on first use, since verify.go requires one), plus a declared
// ExternFunction named sym with the given signature — reusing both on
// every subsequent reference to the same importPath/symbol rather than
// re-declaring (which would collide in the flat namespace, §2.2).
func ensureExternFn(m *vir.Module, importPath, sym string, params []vir.Param, ret vir.Type, variadic bool, attrs []vir.FunctionAttribute) error {
	depName := crossModuleLinkPrefix + importPath

	var g *vir.ExternGroup
	for _, existing := range m.Externs {
		if existing.Dependency == depName {
			g = existing
			break
		}
	}
	if g == nil {
		hasLink := false
		for _, l := range m.Links {
			if l.Name == depName {
				hasLink = true
				break
			}
		}
		if !hasLink {
			m.DeclareLink(vir.LinkStatic, depName)
		}
		g = m.DeclareExternGroup(depName)
	}

	for _, existing := range g.Functions {
		if existing.Name == sym {
			return nil // already declared via an earlier call site
		}
	}
	ef := g.DeclareFunction(sym, params, ret, attrs...)
	if variadic {
		ef.SetVariadic()
	}
	return nil
}