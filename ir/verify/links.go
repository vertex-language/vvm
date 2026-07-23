// links.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// checkLinks validates §2.1 link-decls: a target is required if any link
// is present, kinds are one of the three closed values, and names are
// unique (they're what extern groups match against, byte-for-byte).
func checkLinks(m *vir.Module) error {
	if len(m.Links) > 0 && m.Target == nil {
		return fmt.Errorf("link section present but no target declared (§2.1: target is required if link is present)")
	}
	seen := make(map[string]bool, len(m.Links))
	for _, l := range m.Links {
		if l.Name == "" {
			return fmt.Errorf("link: name must not be empty")
		}
		switch l.Kind {
		case vir.LinkStatic, vir.LinkShared, vir.LinkFramework:
		default:
			return fmt.Errorf("link %q: unrecognized kind %q", l.Name, l.Kind)
		}
		if seen[l.Name] {
			return fmt.Errorf("link %q: declared twice", l.Name)
		}
		seen[l.Name] = true
	}
	return nil
}

// checkExterns enforces link<->extern correspondence (verify.md: every
// extern group's dependency string must match a previously declared link
// name, byte-for-byte) and registers each extern fn in the flat namespace.
func checkExterns(m *vir.Module, names *nameTable) error {
	linkNames := make(map[string]bool, len(m.Links))
	for _, l := range m.Links {
		linkNames[l.Name] = true
	}
	tc := structTypeCtx(m)
	for _, g := range m.Externs {
		if !linkNames[g.Dependency] {
			return fmt.Errorf("extern group %q: dependency does not match any declared link, byte-for-byte", g.Dependency)
		}
		for _, f := range g.Functions {
			if err := checkExternFunction(f, tc); err != nil {
				return fmt.Errorf("extern %q: %w", f.Name, err)
			}
			if err := names.declare("extern fn", f.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkExternFunction(f *vir.ExternFunction, tc *typeCtx) error {
	for i, p := range f.Params {
		if err := checkParam(p, tc); err != nil {
			return fmt.Errorf("param %d: %w", i, err)
		}
	}
	if f.Ret == nil {
		return fmt.Errorf("return type is required")
	}
	if err := tc.checkType(f.Ret); err != nil {
		return fmt.Errorf("return type: %w", err)
	}
	for _, a := range f.Attrs {
		switch a {
		case vir.AttributeNoReturn, vir.AttributeReadonly, vir.AttributeInline, vir.AttributeNoInline, vir.AttributeCold:
		case vir.AttributeEntry, vir.AttributeExternC:
			return fmt.Errorf("attribute %q illegal on an extern fn (§2.2: both apply only to fn defs)", a)
		default:
			return fmt.Errorf("unrecognized attribute %q", a)
		}
	}
	return nil
}

// checkParam validates one param against §4/§7.4's byval/sret rules and
// disallows valist as a bare parameter type (§4.5 — it's alloca-only).
//
// byval[S] and sret[S] are mirror-image ABI attributes: both cross the C
// boundary as a plain pointer (byval: the caller's copy the callee reads
// through; sret: the destination the callee writes its return value
// into) — this matches how the x86_64 backend's typefix.go treats both
// ("byval/sret params arrive as pointers"), how the sret check two lines
// below this one is written, and how the calls.go test suite's own
// comment describes byval ("crosses the C boundary as a pointer whose
// callee-side writes never affect the caller's object"). The byval check
// previously required p.Type to literally BE `struct S` — the one
// attribute value real byval/sret usage never has, since the whole point
// is that the parameter itself is a pointer to the struct, not the
// struct passed by raw value in a register/slot. Fixed to require
// IsPtr(p.Type), matching sret's own check exactly, plus a declare-
// before-use existence check on the named struct (§2.2) that neither
// check previously performed.
func checkParam(p vir.Param, tc *typeCtx) error {
	if vir.IsValist(p.Type) {
		return fmt.Errorf("param %q: valist is never a legal parameter type (§3, §4.5) — use alloca.valist in the body instead", p.Name)
	}
	if err := tc.checkType(p.Type); err != nil {
		return err
	}
	if p.ByVal != "" && p.SRet != "" {
		return fmt.Errorf("param %q: byval and sret are mutually exclusive on one param", p.Name)
	}
	if p.ByVal != "" {
		if !vir.IsPtr(p.Type) {
			return fmt.Errorf("param %q: byval[%s] requires a ptr-typed param", p.Name, p.ByVal)
		}
		if !tc.structs[p.ByVal] {
			return fmt.Errorf("param %q: byval[%s] names an undeclared struct (§2.2)", p.Name, p.ByVal)
		}
	}
	if p.SRet != "" {
		if !vir.IsPtr(p.Type) {
			return fmt.Errorf("param %q: sret[%s] requires a ptr-typed param", p.Name, p.SRet)
		}
		if !tc.structs[p.SRet] {
			return fmt.Errorf("param %q: sret[%s] names an undeclared struct (§2.2)", p.Name, p.SRet)
		}
	}
	return nil
}