// values.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// The §7.3 Join Convention: values merge across blocks by same-name
// assignment. In C++ that is one whole-body local written more than once, so
// every binding is declared once in a prologue and every instruction is an
// assignment to it. No phi insertion, no loop-carried special case, and no
// question about which C++ scope a name belongs to.

type binding struct {
	name    string
	ref     msl.Expr  // usually Name(name); parameters and group objects differ
	typ     msl.Type  // nil for void
	gtyp    gvir.Type
	pointee gvir.Type // best-effort pointee for field.ptr; nil = unknown
	declare bool      // false for parameters and pre-bound objects
}

type values struct {
	byName map[string]*binding
	order  []*binding
}

func newValues() *values { return &values{byName: map[string]*binding{}} }

// define binds a name on first assignment and returns the same binding
// thereafter. Rebinding at a different type is rejected rather than silently
// reallocated: §7.3 fixes the type permanently at the first assignment, and
// address space is part of a pointer's type.
func (v *values) define(name string, gt gvir.Type, mt msl.Type, mangled string) (*binding, error) {
	if b, ok := v.byName[name]; ok {
		if !gvir.Equal(b.gtyp, gt) {
			return nil, fmt.Errorf("%s is bound as %s and reassigned as %s (§7.3)", name, b.gtyp, gt)
		}
		return b, nil
	}
	b := &binding{name: mangled, ref: msl.Name(mangled), typ: mt, gtyp: gt, declare: true}
	v.byName[name] = b
	v.order = append(v.order, b)
	return b, nil
}

// bind installs a name whose storage this backend supplies: a kernel or func
// parameter, a `group` object's address, a `dynamic_group` name.
func (v *values) bind(name string, gt gvir.Type, mt msl.Type, ref msl.Expr, pointee gvir.Type) *binding {
	b := &binding{name: name, ref: ref, typ: mt, gtyp: gt, pointee: pointee}
	v.byName[name] = b
	v.order = append(v.order, b)
	return b
}

func (v *values) lookup(name string) (*binding, bool) {
	b, ok := v.byName[name]
	return b, ok
}

// declarations returns the prologue: one declaration per bound value, in
// binding order.
func (v *values) declarations() []msl.Stmt {
	var out []msl.Stmt
	for _, b := range v.order {
		if !b.declare || b.typ == nil {
			continue
		}
		out = append(out, &msl.VarDecl{Type: b.typ, Name: b.name})
	}
	return out
}

// ---------------------------------------------------------------------------
// Operands
// ---------------------------------------------------------------------------

// operand materializes one .gvir operand at a known MSL type. Literals are
// spelled with an explicit conversion — long(1), float(0.5) — because the
// unsigned readings go through as_type, which requires the operand's width to
// match exactly and would otherwise see a plain int.
func (f *fnLower) operand(o gvir.Operand, t msl.Type) (msl.Expr, error) {
	if o.Kind == gvir.OperandIdent {
		if b, ok := f.vals.lookup(o.Ident); ok {
			return b.ref, nil
		}
		if c := f.l.src.ConstByName(o.Ident); c != nil {
			return msl.Name(f.l.names.ident(c.Name)), nil
		}
		return msl.Expr{}, fmt.Errorf("%s is not bound on every path to this use (§7.3)", o.Ident)
	}
	return literal(o, t)
}

func literal(o gvir.Operand, t msl.Type) (msl.Expr, error) {
	switch o.Kind {
	case gvir.OperandInt:
		if t == nil {
			return msl.I(o.Int), nil
		}
		return msl.Cast(t, msl.I(o.Int)), nil
	case gvir.OperandFloat:
		if t == nil {
			return msl.F(o.Float), nil
		}
		return msl.Cast(t, msl.F(o.Float)), nil
	case gvir.OperandBool:
		return msl.B(o.Bool), nil
	case gvir.OperandNull:
		return msl.Raw("nullptr"), nil
	case gvir.OperandString:
		return msl.Raw(o.String()), nil
	}
	return msl.Expr{}, fmt.Errorf("operand %s is not a value", o)
}

// index materializes a literal index operand — a vector lane, a struct field
// number, a swizzle selector — which is always a plain integer.
func index(o gvir.Operand) (int, error) {
	if o.Kind != gvir.OperandInt {
		return 0, fmt.Errorf("expected a literal index, got %s", o)
	}
	return int(o.Int), nil
}

// ptrOperand resolves an operand that must be a pointer value and returns its
// binding, so the address space and the tracked pointee travel with it.
func (f *fnLower) ptrOperand(o gvir.Operand) (*binding, error) {
	if o.Kind != gvir.OperandIdent {
		return nil, fmt.Errorf("%s is not a pointer value", o)
	}
	b, ok := f.vals.lookup(o.Ident)
	if !ok {
		return nil, fmt.Errorf("%s is not bound (§7.3)", o.Ident)
	}
	if !gvir.IsSpacedPtr(b.gtyp) {
		return nil, fmt.Errorf("%s is %s, not a pointer", o.Ident, b.gtyp)
	}
	return b, nil
}

// at spells *vv_at<T>(p) — the reinterpretation of a byte address at the
// accessed type, in the address space the pointer's own type names (§5).
func at(t msl.Type, p msl.Expr) msl.Expr {
	return msl.TCall("vv_at", []msl.TypeArg{msl.TArg(t)}, p)
}

func deref(t msl.Type, p msl.Expr) msl.Expr { return at(t, p).Deref() }

// bytes spells vv_bytes(&obj[0]) — the address of a threadgroup or thread
// object as the byte pointer the IR expects (§8.1, §8.2).
func bytesOf(obj msl.Expr) msl.Expr {
	return msl.Call("vv_bytes", obj.At(msl.I(0)).Addr())
}