// check.go
package importer

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// CheckReferences walks every module in the Set and checks every
// cross-module reference directly against the real target module's real
// declarations: struct/fnsig references (StructType.Import wherever a
// type appears), fn call targets (arity/variadic-ness/export/noreturn),
// and const/global qualified-operand references (existence/export).
//
// Precondition (importer.md, verify.go): every module has already passed
// verify.Verify, and ResolveImports has already run on this Set.
func (s *Set) CheckReferences() error {
	for _, m := range s.modules {
		if err := s.checkModuleReferences(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Set) checkModuleReferences(m *vir.Module) error {
	for _, st := range m.Structs {
		for _, f := range st.Fields {
			if err := s.checkStructRef(m, f.Type); err != nil {
				return fmt.Errorf("module %q struct %q field %q: %w", m.Name, st.Name, f.Name, err)
			}
		}
	}
	for _, sig := range m.FunctionSignatures {
		for i, p := range sig.Params {
			if err := s.checkStructRef(m, p); err != nil {
				return fmt.Errorf("module %q fnsig %q param %d: %w", m.Name, sig.Name, i, err)
			}
		}
		if err := s.checkStructRef(m, sig.Ret); err != nil {
			return fmt.Errorf("module %q fnsig %q return type: %w", m.Name, sig.Name, err)
		}
	}
	for _, g := range m.Globals {
		if err := s.checkStructRef(m, g.Type); err != nil {
			return fmt.Errorf("module %q global %q: %w", m.Name, g.Name, err)
		}
	}
	for _, eg := range m.Externs {
		for _, ef := range eg.Functions {
			for _, p := range ef.Params {
				if err := s.checkStructRef(m, p.Type); err != nil {
					return fmt.Errorf("module %q extern fn %q param %q: %w", m.Name, ef.Name, p.Name, err)
				}
			}
			if err := s.checkStructRef(m, ef.Ret); err != nil {
				return fmt.Errorf("module %q extern fn %q return type: %w", m.Name, ef.Name, err)
			}
		}
	}
	for _, f := range m.Functions {
		if err := s.checkFunctionRefs(m, f); err != nil {
			return err
		}
	}
	return nil
}

// checkStructRef checks a StructType.Import claim (wherever it appears,
// including nested in vec/array element types) against the real target
// module. Note the Go data model (types.go) gives a StructType only
// {Name, Import} — no field list of its own to compare — so "field-for-
// field" here reduces to: the import resolves, the struct exists in the
// target, and it's exported. There is nothing further to rewrite (the
// type node is left as-is, per importer.md's struct example) and no
// deeper structural recursion is possible from this side, so — unlike a
// local self-referential struct — a cross-module struct reference can
// never itself be the cycle importer.md says is otherwise unhandled.
func (s *Set) checkStructRef(m *vir.Module, t vir.Type) error {
	if t == nil {
		return nil
	}
	switch x := t.(type) {
	case vir.StructType:
		if x.Import == "" {
			return nil // local struct — verify.Verify already confirmed it exists
		}
		target, err := s.resolvedTarget(m, x.Import)
		if err != nil {
			return err
		}
		for _, cs := range target.Structs {
			if cs.Name != x.Name {
				continue
			}
			if !cs.Export {
				return fmt.Errorf("struct %s.%s is not exported", x.Import, x.Name)
			}
			return nil
		}
		return fmt.Errorf("struct %s.%s does not exist in %q", x.Import, x.Name, target.Name)
	case vir.VecType:
		return s.checkStructRef(m, x.Elem)
	case vir.ArrayType:
		return s.checkStructRef(m, x.Elem)
	}
	return nil
}

// checkFunctionRefs checks one function's cross-module surface: its own
// param/return types, every qualified operand in its body, and (for
// qualified call targets only) the §4.2 noreturn call-site shape that
// ir/verify's checkNoreturnCallSites deliberately exempts for imported
// callees (body.go: "Qualified (imported) callees are exempt; importer
// checks those once it can see the real callee's attributes").
func (s *Set) checkFunctionRefs(m *vir.Module, f *vir.Function) error {
	for _, p := range f.Params {
		if err := s.checkStructRef(m, p.Type); err != nil {
			return fmt.Errorf("module %q fn %q param %q: %w", m.Name, f.Name, p.Name, err)
		}
	}
	if err := s.checkStructRef(m, f.Ret); err != nil {
		return fmt.Errorf("module %q fn %q return type: %w", m.Name, f.Name, err)
	}

	for _, b := range f.AllBlocks() {
		for i, line := range b.Lines {
			for idx, a := range line.Args {
				if !a.IsQualified() {
					continue
				}
				if line.Op == vir.OpCall && idx == 0 {
					argCount := len(line.Args) - 1
					noreturn, err := s.checkCallCallee(m, a, argCount)
					if err != nil {
						return fmt.Errorf("module %q fn %q: %w", m.Name, f.Name, err)
					}
					if noreturn {
						if err := checkImportedNoreturnCallSite(b, i, a); err != nil {
							return fmt.Errorf("module %q fn %q: %w", m.Name, f.Name, err)
						}
					}
					continue
				}
				if err := s.checkQualifiedOperand(m, a); err != nil {
					return fmt.Errorf("module %q fn %q: %w", m.Name, f.Name, err)
				}
			}
		}
		if err := s.checkTerminatorRefs(m, b.Term); err != nil {
			return fmt.Errorf("module %q fn %q: %w", m.Name, f.Name, err)
		}
	}
	return nil
}

// checkTerminatorRefs checks the handful of terminator operand positions
// that can carry a qualified const reference. tailcall's *callee* is
// never one of them — per the grammar (README §2.3), `tailcall` takes a
// plain ident, not a qualified-ident, so there's no cross-module tailcall
// target for this package to resolve.
func (s *Set) checkTerminatorRefs(m *vir.Module, t vir.Terminator) error {
	check := func(op vir.Operand) error {
		if !op.IsQualified() {
			return nil
		}
		return s.checkQualifiedOperand(m, op)
	}
	switch x := t.(type) {
	case vir.BranchIf:
		return check(x.Cond)
	case vir.Switch:
		return check(x.Value)
	case vir.Return:
		if x.Value != nil {
			return check(*x.Value)
		}
	case vir.TailCall:
		for _, a := range x.Args {
			if err := check(a); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkCallCallee checks a qualified call target's arity/variadic-ness/
// export status against the real fn or extern fn it names, per
// importer.md's http.get example. Returns whether the real callee is
// noreturn, so the caller can additionally enforce §4.2's call-site shape.
func (s *Set) checkCallCallee(m *vir.Module, callee vir.Operand, argCount int) (noreturn bool, err error) {
	target, err := s.resolvedTarget(m, callee.Qualifier)
	if err != nil {
		return false, err
	}
	for _, f := range target.Functions {
		if f.Name != callee.Ident {
			continue
		}
		if !f.Export {
			return false, fmt.Errorf("call to %s.%s, which is not exported", callee.Qualifier, callee.Ident)
		}
		if err := checkCallArity(callee, f.Params, f.Variadic, argCount); err != nil {
			return false, err
		}
		return f.HasAttribute(vir.AttributeNoReturn), nil
	}
	for _, g := range target.Externs {
		for _, ef := range g.Functions {
			if ef.Name != callee.Ident {
				continue
			}
			if err := checkCallArity(callee, ef.Params, ef.Variadic, argCount); err != nil {
				return false, err
			}
			for _, a := range ef.Attrs {
				if a == vir.AttributeNoReturn {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, fmt.Errorf("call to %s.%s, which does not exist in %q", callee.Qualifier, callee.Ident, target.Name)
}

func checkCallArity(callee vir.Operand, params []vir.Param, variadic bool, argCount int) error {
	if variadic {
		if argCount < len(params) {
			return fmt.Errorf("call to %s.%s: expects at least %d argument(s), got %d", callee.Qualifier, callee.Ident, len(params), argCount)
		}
		return nil
	}
	if argCount != len(params) {
		return fmt.Errorf("call to %s.%s: expects %d argument(s), got %d", callee.Qualifier, callee.Ident, len(params), argCount)
	}
	return nil
}

// checkImportedNoreturnCallSite mirrors ir/verify's checkNoreturnCallSites
// (body.go) for the one case that package deliberately exempts: a call
// whose noreturn-ness is only visible once the callee's real module is
// known.
func checkImportedNoreturnCallSite(b *vir.Block, lineIdx int, callee vir.Operand) error {
	for _, rest := range b.Lines[lineIdx+1:] {
		if rest.Op != vir.OpLoc {
			return fmt.Errorf("call to imported noreturn fn %s.%s must be immediately followed by unreachable, after loc/comments (§4.2)", callee.Qualifier, callee.Ident)
		}
	}
	switch b.Term.(type) {
	case vir.Trap, vir.Unreachable:
	default:
		return fmt.Errorf("call to imported noreturn fn %s.%s must precede a trap/unreachable terminator (§4.2)", callee.Qualifier, callee.Ident)
	}
	return nil
}

// checkQualifiedOperand checks a non-callee qualified operand — this is
// the "const disappears entirely" / global cases from importer.md's
// per-kind summary. A qualified ident that isn't a call callee must name
// either an exported const or an exported global in the target module.
func (s *Set) checkQualifiedOperand(m *vir.Module, op vir.Operand) error {
	target, err := s.resolvedTarget(m, op.Qualifier)
	if err != nil {
		return err
	}
	for _, c := range target.Constants {
		if c.Name == op.Ident {
			if !c.Export {
				return fmt.Errorf("reference to %s.%s, which is not exported", op.Qualifier, op.Ident)
			}
			return nil
		}
	}
	for _, g := range target.Globals {
		if g.Name == op.Ident {
			if !g.Export {
				return fmt.Errorf("reference to %s.%s, which is not exported", op.Qualifier, op.Ident)
			}
			return nil
		}
	}
	return fmt.Errorf("%s.%s does not name any exported const or global in %q", op.Qualifier, op.Ident, target.Name)
}