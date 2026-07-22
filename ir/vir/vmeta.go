// vmeta.go
package vir

import (
	"fmt"
	"strings"
)

// Stage 0 (Extraction, §7.3): a ModuleShape is what a module's own
// `.vmeta` would carry — export-tagged struct/const/fnsig shapes plus
// fn/global *signatures* (no bodies, no initializers). Stage A (Provisional,
// §7.3) then checks a module's qualified references against a direct
// import's ModuleShape as if locally declared. Stage B (Structural, §7.4)
// is explicitly `vvm`'s job at build-orchestration time, against the real
// compiled exporter — out of scope for this package.

type StructShape struct {
	Name   string
	Fields []Field
}

type ConstShape struct {
	Name  string
	Type  Type
	Value Operand
}

type FnSigShape struct {
	Name     string
	Params   []Type
	Variadic bool
	Ret      Type
}

type FnShape struct {
	Name     string
	Params   []Param
	Variadic bool
	Ret      Type
	Attrs    []FunctionAttribute
}

type GlobalShape struct {
	Name string
	Type Type
	TLS  bool
}

// ModuleShape is one module's exported-shape summary (§7.3).
type ModuleShape struct {
	Namespace  string
	ModuleName string
	Structs    []StructShape
	Consts     []ConstShape
	FnSigs     []FnSigShape
	Fns        []FnShape
	Globals    []GlobalShape
}

// QualifiedID returns "namespace/module" or bare "module" (§7.3).
func (s *ModuleShape) QualifiedID() string {
	if s.Namespace == "" {
		return s.ModuleName
	}
	return s.Namespace + "/" + s.ModuleName
}

// ExtractShape performs Stage 0 (§7.3): pulls every export-tagged
// struct/const/fnsig/fn-and-global *signature* out of m, deliberately
// omitting fn bodies and global initializers.
func ExtractShape(m *Module) *ModuleShape {
	s := &ModuleShape{Namespace: m.Namespace, ModuleName: m.Name}
	for _, st := range m.Structs {
		if st.Export {
			s.Structs = append(s.Structs, StructShape{Name: st.Name, Fields: st.Fields})
		}
	}
	for _, c := range m.Constants {
		if c.Export {
			s.Consts = append(s.Consts, ConstShape{Name: c.Name, Type: c.Type, Value: c.Value})
		}
	}
	for _, sig := range m.FunctionSignatures {
		if sig.Export {
			s.FnSigs = append(s.FnSigs, FnSigShape{Name: sig.Name, Params: sig.Params, Variadic: sig.Variadic, Ret: sig.Ret})
		}
	}
	for _, f := range m.Functions {
		if f.Export {
			s.Fns = append(s.Fns, FnShape{Name: f.Name, Params: f.Params, Variadic: f.Variadic, Ret: f.Ret, Attrs: f.Attrs})
		}
	}
	for _, g := range m.Globals {
		if g.Export {
			s.Globals = append(s.Globals, GlobalShape{Name: g.Name, Type: g.Type, TLS: g.TLS})
		}
	}
	return s
}

func (s *ModuleShape) findStruct(name string) (StructShape, bool) {
	for _, x := range s.Structs {
		if x.Name == name {
			return x, true
		}
	}
	return StructShape{}, false
}
func (s *ModuleShape) findConst(name string) (ConstShape, bool) {
	for _, x := range s.Consts {
		if x.Name == name {
			return x, true
		}
	}
	return ConstShape{}, false
}
func (s *ModuleShape) findFnSig(name string) (FnSigShape, bool) {
	for _, x := range s.FnSigs {
		if x.Name == name {
			return x, true
		}
	}
	return FnSigShape{}, false
}
func (s *ModuleShape) findFn(name string) (FnShape, bool) {
	for _, x := range s.Fns {
		if x.Name == name {
			return x, true
		}
	}
	return FnShape{}, false
}
func (s *ModuleShape) findGlobal(name string) (GlobalShape, bool) {
	for _, x := range s.Globals {
		if x.Name == name {
			return x, true
		}
	}
	return GlobalShape{}, false
}

// resolveImportKind looks up name's kind ("struct"/"const"/"fnsig"/"fn"/
// "global") in shape, for Stage A's "as if locally declared" trust model.
func resolveImportKind(shape *ModuleShape, name string) string {
	if _, ok := shape.findStruct(name); ok {
		return "struct"
	}
	if _, ok := shape.findConst(name); ok {
		return "const"
	}
	if _, ok := shape.findFnSig(name); ok {
		return "fnsig"
	}
	if _, ok := shape.findFn(name); ok {
		return "fn"
	}
	if _, ok := shape.findGlobal(name); ok {
		return "global"
	}
	return ""
}

func fmtImportRef(path, name string) string { return fmt.Sprintf("%s.%s", path, name) }

var _ = strings.TrimSpace // keep strings imported for future path-normalization needs