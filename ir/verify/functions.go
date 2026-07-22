// functions.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// fnCtx bundles the module-wide lookups a function body needs to resolve
// local calls/tailcalls (§4.2) and noreturn call sites (§4.2). fns is
// built incrementally as functions are processed, in file order, so a
// function only ever "sees" itself (self-recursion, §2.2's sole exemption
// from declare-before-use) and functions declared strictly earlier.
type fnCtx struct {
	fnsigs         map[string]*vir.FunctionSignature
	fns            map[string]*vir.Function
	externs        map[string]*vir.ExternFunction
	localNoreturn  map[string]bool
	externNoreturn map[string]bool
}

func checkFunctions(m *vir.Module, names *nameTable) error {
	tc := structTypeCtx(m)

	fnsigs := make(map[string]*vir.FunctionSignature, len(m.FunctionSignatures))
	for _, s := range m.FunctionSignatures {
		fnsigs[s.Name] = s
	}
	externs := make(map[string]*vir.ExternFunction)
	externNoreturn := make(map[string]bool)
	for _, g := range m.Externs {
		for _, ef := range g.Functions {
			externs[ef.Name] = ef
			for _, a := range ef.Attrs {
				if a == vir.AttributeNoReturn {
					externNoreturn[ef.Name] = true
				}
			}
		}
	}

	ctx := &fnCtx{
		fnsigs:         fnsigs,
		fns:            make(map[string]*vir.Function),
		externs:        externs,
		localNoreturn:  make(map[string]bool),
		externNoreturn: externNoreturn,
	}

	sawEntry := false
	for _, f := range m.Functions {
		if err := checkFunctionAttrs(f, &sawEntry); err != nil {
			return fmt.Errorf("fn %q: %w", f.Name, err)
		}
		for i, p := range f.Params {
			if err := checkParam(p, tc); err != nil {
				return fmt.Errorf("fn %q param %d: %w", f.Name, i, err)
			}
		}
		if f.Ret == nil {
			return fmt.Errorf("fn %q: return type is required", f.Name)
		}
		if err := tc.checkType(f.Ret); err != nil {
			return fmt.Errorf("fn %q return type: %w", f.Name, err)
		}
		if err := names.declare("fn", f.Name); err != nil {
			return err
		}
		if f.HasAttribute(vir.AttributeNoReturn) {
			ctx.localNoreturn[f.Name] = true
		}
		// Self-recursion is exempt from declare-before-use (§2.2): make f
		// visible to its own body before checking it.
		ctx.fns[f.Name] = f

		if err := checkFunctionBody(f, ctx); err != nil {
			return fmt.Errorf("fn %q: %w", f.Name, err)
		}
	}
	return nil
}

func checkFunctionAttrs(f *vir.Function, sawEntry *bool) error {
	isEntry := f.HasAttribute(vir.AttributeEntry)
	isExternC := f.HasAttribute(vir.AttributeExternC)
	if isEntry && isExternC {
		return fmt.Errorf("entry and extern_c are mutually exclusive on the same fn (§2.2)")
	}
	if (isEntry || isExternC) && !f.Export {
		return fmt.Errorf("entry/extern_c both require export (§2.2)")
	}
	if isEntry {
		if *sawEntry {
			return fmt.Errorf("at most one entry fn is allowed per module (§2.2, §9.4a)")
		}
		*sawEntry = true
		if f.HasAttribute(vir.AttributeNoReturn) {
			return fmt.Errorf("entry fn must not be noreturn (§2.2)")
		}
		for _, p := range f.Params {
			if p.ByVal != "" || p.SRet != "" {
				return fmt.Errorf("entry fn must not have byval/sret params (§2.2)")
			}
		}
	}
	return nil
}