// rewrite.go
package importer

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Rewrite erases every cross-module reference CheckReferences has already
// validated, replacing each with what lower/<arch> actually consumes
// (importer.md's per-kind summary):
//
//	const        -> inline literal operand (no symbol ever existed)
//	fn / global  -> the real mangled symbol, extern-style
//	struct/fnsig -> untouched — CheckReferences was the entire job
//
// Rewrite re-resolves the same references Check References did and
// assumes they're valid rather than re-validating from scratch, so it
// must only run after CheckReferences has succeeded on every module in
// the Set.
func (s *Set) Rewrite() error {
	for _, m := range s.modules {
		for _, f := range m.Functions {
			for _, b := range f.AllBlocks() {
				for _, line := range b.Lines {
					if err := s.rewriteInstructionArgs(m, line); err != nil {
						return fmt.Errorf("module %q fn %q: %w", m.Name, f.Name, err)
					}
				}
				if err := s.rewriteTerminator(m, b.Term); err != nil {
					return fmt.Errorf("module %q fn %q: %w", m.Name, f.Name, err)
				}
			}
		}
	}
	return nil
}

func (s *Set) rewriteInstructionArgs(m *vir.Module, line *vir.Instruction) error {
	for idx := range line.Args {
		a := line.Args[idx]
		if !a.IsQualified() {
			continue
		}
		if line.Op == vir.OpCall && idx == 0 {
			sym, err := s.mangledCallTarget(m, a)
			if err != nil {
				return err
			}
			line.Args[idx] = vir.Ident(sym)
			continue
		}
		rewritten, err := s.rewriteQualifiedOperand(m, a)
		if err != nil {
			return err
		}
		line.Args[idx] = rewritten
	}
	return nil
}

// rewriteTerminator handles TailCall.Args — the only terminator operand
// positions that can carry a qualified const reference (see
// checkTerminatorRefs's note on why tailcall's callee itself never can).
func (s *Set) rewriteTerminator(m *vir.Module, t vir.Terminator) error {
	tc, ok := t.(vir.TailCall)
	if !ok {
		return nil
	}
	for i, a := range tc.Args {
		if !a.IsQualified() {
			continue
		}
		rewritten, err := s.rewriteQualifiedOperand(m, a)
		if err != nil {
			return err
		}
		tc.Args[i] = rewritten
	}
	return nil
}

// mangledCallTarget resolves a qualified call callee to the real mangled
// symbol its own module exports it under (importer.md's http.get example:
// "_M4acme3net4http3get is exactly ... for http.get — the same mangled
// name http's own object file exports").
func (s *Set) mangledCallTarget(m *vir.Module, callee vir.Operand) (string, error) {
	target, err := s.resolvedTarget(m, callee.Qualifier)
	if err != nil {
		return "", err
	}
	for _, f := range target.Functions {
		if f.Name == callee.Ident {
			return SymbolForFunction(target, f), nil
		}
	}
	for _, g := range target.Externs {
		for _, ef := range g.Functions {
			if ef.Name == callee.Ident {
				// A re-exported extern fn has no mangling of its own to
				// compute — it's already an ordinary extern symbol in the
				// real linker's sense, so its declared Name is the symbol.
				return ef.Name, nil
			}
		}
	}
	return "", fmt.Errorf("module %q: %s.%s vanished between CheckReferences and Rewrite", m.Name, callee.Qualifier, callee.Ident)
}

// rewriteQualifiedOperand resolves a non-callee qualified operand: a
// const reference disappears into its literal value; a global reference
// becomes its real mangled symbol as an ordinary ident, extern-style.
func (s *Set) rewriteQualifiedOperand(m *vir.Module, op vir.Operand) (vir.Operand, error) {
	target, err := s.resolvedTarget(m, op.Qualifier)
	if err != nil {
		return vir.Operand{}, err
	}
	for _, c := range target.Constants {
		if c.Name == op.Ident {
			return c.Value, nil // the qualified-ident operand ceases to exist
		}
	}
	for _, g := range target.Globals {
		if g.Name == op.Ident {
			return vir.Ident(SymbolForGlobal(target, g)), nil
		}
	}
	return vir.Operand{}, fmt.Errorf("module %q: %s.%s vanished between CheckReferences and Rewrite", m.Name, op.Qualifier, op.Ident)
}