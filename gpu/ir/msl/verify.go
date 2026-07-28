package msl

import "fmt"

// Severity distinguishes structural errors from advisory findings.
type Severity int

// Severities.
const (
	SevError Severity = iota
	SevWarning
)

func (s Severity) String() string {
	if s == SevError {
		return "error"
	}
	return "warning"
}

// Diag is one finding from Verify.
type Diag struct {
	Sev   Severity
	Where string
	Msg   string
}

func (d Diag) String() string {
	return fmt.Sprintf("msl: %s: %s: %s", d.Sev, d.Where, d.Msg)
}

// Verify checks a module for structural problems and version gating. It does
// not type-check expressions, verify address-space compatibility, or gate per
// GPU family — the metal frontend is the verifier of record.
//
// Errors describe modules that cannot be valid MSL under any toolchain.
// Warnings describe features that postdate the module's revision.
func Verify(m *Module) []Diag {
	v := &verifier{mod: m, seen: map[string]bool{}}
	v.decls(m.Decls)
	return v.diags
}

type verifier struct {
	mod   *Module
	diags []Diag
	seen  map[string]bool
}

func (v *verifier) errf(where, format string, args ...any) {
	v.diags = append(v.diags, Diag{SevError, where, fmt.Sprintf(format, args...)})
}

func (v *verifier) warnf(where, format string, args ...any) {
	d := Diag{SevWarning, where, fmt.Sprintf(format, args...)}
	key := d.Where + "\x00" + d.Msg
	if v.seen[key] {
		return
	}
	v.seen[key] = true
	v.diags = append(v.diags, d)
}

func (v *verifier) decls(ds []Decl) {
	for _, d := range ds {
		switch n := d.(type) {
		case *Struct:
			for _, f := range n.Fields {
				where := "struct " + n.Name + "." + f.Name
				v.typ(f.Type, where)
				v.attrs(f.Attrs, OnField, where)
			}
			for _, mth := range n.Methods {
				v.fn(mth, "method "+n.Name+"::"+mth.Name)
			}
		case *Function:
			v.fn(n, "function "+n.Name)
		case *VarDecl:
			v.varDecl(n, "variable "+n.Name, OnVar)
		case *Alias:
			v.typ(n.Type, "alias "+n.Name)
		case *PPIfDecl:
			v.decls(n.Then)
			v.decls(n.Els)
		}
	}
}

func (v *verifier) fn(f *Function, where string) {
	if f.Name == "" {
		v.errf(where, "function has no name")
	}
	if f.Stage == KernelStage && f.Ret != nil && f.Ret != Type(Void) {
		v.errf(where, "kernel functions must return void")
	}
	if f.Body == nil && f.Stage != Plain {
		v.errf(where, "stage function has no body")
	}
	v.typ(f.Ret, where+" return type")
	v.attrs(f.Attrs, OnFunc, where)
	v.attrs(f.RetAttrs, OnReturn, where+" return type")

	names := map[string]bool{}
	for _, p := range f.Params {
		pw := where + " parameter " + p.Name
		if names[p.Name] {
			v.errf(pw, "duplicate parameter name")
		}
		names[p.Name] = true
		v.typ(p.Type, pw)
		v.attrs(p.Attrs, OnParam, pw)
	}
	if f.Body != nil {
		v.block(f.Body, where+" body")
	}
}

func (v *verifier) block(b *Block, where string) {
	for _, s := range b.stmts {
		if d, ok := s.(*VarDecl); ok {
			v.varDecl(d, where+" local "+d.Name, OnVar)
		}
	}
	Walk(b2node(b), func(n Node) bool {
		if t, ok := n.(Type); ok {
			v.typ(t, where)
			return false
		}
		return true
	})
}

// b2node lets Walk descend a bare block.
func b2node(b *Block) Node { return &Scope{Body: b} }

func (v *verifier) varDecl(d *VarDecl, where string, place Place) {
	if d.Name == "" {
		v.errf(where, "variable has no name")
	}
	if d.Type == nil {
		v.errf(where, "variable has no type")
	}
	if d.Space == Constant && d.Init.IsZero() && !hasAttr(d.Attrs, AttrFunctionConstant) {
		v.errf(where, "constant-space variable needs an initializer")
	}
	v.typ(d.Type, where)
	v.attrs(d.Attrs, place, where)
}

func hasAttr(as []Attr, n AttrName) bool {
	for _, a := range as {
		if a.Name == n {
			return true
		}
	}
	return false
}

func (v *verifier) attrs(as []Attr, place Place, where string) {
	for _, a := range as {
		if a.IsZero() {
			v.errf(where, "empty attribute")
			continue
		}
		spec, known := attrTable[a.Name]
		if !known {
			continue // RawAttr and unlisted spellings pass through
		}
		if spec.place&place == 0 {
			v.errf(where, "[[%s]] is not valid in this position", a.Name)
		}
		if spec.args >= 0 && len(a.Args) != spec.args {
			v.errf(where, "[[%s]] takes %d argument(s), got %d",
				a.Name, spec.args, len(a.Args))
		}
		if !spec.since.IsZero() && !v.mod.Version.GTE(spec.since) {
			v.warnf(where, "[[%s]] requires %s (module is %s)",
				a.Name, spec.since.Std(), v.mod.Version.Std())
		}
	}
}

func (v *verifier) typ(t Type, where string) {
	if t == nil {
		return
	}
	Walk(t, func(n Node) bool {
		switch x := n.(type) {
		case PtrType:
			if x.Space == NoSpace {
				v.errf(where, "pointer type has no address space")
			}
		case RefType:
			if x.Space == NoSpace {
				v.errf(where, "reference type has no address space")
			}
		case ScalarType:
			switch x {
			case BFloat:
				v.floor(Metal31, "bfloat", where)
			case Auto:
				v.floor(Metal32, "auto", where)
			}
		case TensorType:
			v.floor(Metal40, "tensor types", where)
			if x.Kind == TensorHandleKind && x.Space == NoSpace {
				v.errf(where, "tensor_handle has no address space")
			}
		case CoopTensorType:
			v.floor(Metal40, "cooperative tensors", where)
		}
		return true
	})
}

func (v *verifier) floor(min Version, what, where string) {
	if !v.mod.Version.GTE(min) {
		v.warnf(where, "%s require %s (module is %s)",
			what, min.Std(), v.mod.Version.Std())
	}
}

// Errors reports whether any diagnostic is an error.
func Errors(ds []Diag) bool {
	for _, d := range ds {
		if d.Sev == SevError {
			return true
		}
	}
	return false
}