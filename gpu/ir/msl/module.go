package msl

import "strconv"

// Module is the root of the IR: one .metal translation unit.
//
// Declarations live in a single ordered list rather than in per-kind buckets,
// because MSL requires declare-before-use and the order is therefore load
// bearing: a `constant Params kDefaults = {...};` must follow `struct Params`.
type Module struct {
	Version Version
	Decls   []Decl
}

// NewModule creates a module at the given language revision, seeded with
// <metal_stdlib> and `using namespace metal;`.
//
// The revision is required rather than defaulted: it is the -std= floor, it
// gates which OS releases can load the resulting library, and Verify reads it.
func NewModule(v Version) *Module {
	m := &Module{Version: v}
	m.Include("metal_stdlib")
	m.Using("metal")
	return m
}

// Add appends declarations in order.
func (m *Module) Add(d ...Decl) *Module {
	m.Decls = append(m.Decls, d...)
	return m
}

// Include appends a system header, skipping duplicates.
func (m *Module) Include(name string) *Module {
	for _, d := range m.Decls {
		if inc, ok := d.(*Include); ok && inc.Name == name && !inc.Local {
			return m
		}
	}
	return m.Add(&Include{Name: name})
}

// Using appends a namespace using-directive, skipping duplicates.
func (m *Module) Using(ns string) *Module {
	for _, d := range m.Decls {
		if u, ok := d.(*Using); ok && u.Namespace == ns {
			return m
		}
	}
	return m.Add(&Using{Namespace: ns})
}

// Alias appends a type alias and returns it.
func (m *Module) Alias(name string, t Type) *Alias {
	a := &Alias{Name: name, Type: t}
	m.Add(a)
	return a
}

// Constant appends a module-scope constant: constant float kPi = 3.14;.
func (m *Module) Constant(t Type, name string, init Expr) *VarDecl {
	v := &VarDecl{Space: Constant, Type: t, Name: name, Init: init}
	m.Add(v)
	return v
}

// FnConst appends a function constant, MSL's link-time specialization hook:
// constant bool useFastPath [[function_constant(0)]];
func (m *Module) FnConst(t Type, name string, index int) *VarDecl {
	v := &VarDecl{
		Space: Constant, Type: t, Name: name,
		Attrs: []Attr{FunctionConstant(index)},
	}
	m.Add(v)
	return v
}

// VersionGate appends a preprocessor conditional on __METAL_VERSION__. A 4.1
// binary will not load on an older OS, so shipping libraries carry gated
// source rather than a single revision floor.
func (m *Module) VersionGate(v Version, then, els []Decl) *PPIfDecl {
	d := &PPIfDecl{
		Cond: "__METAL_VERSION__ >= " + strconv.Itoa(v.Macro()),
		Then: then,
		Els:  els,
	}
	m.Add(d)
	return d
}

// Func returns the first function with the given name, or nil.
func (m *Module) Func(name string) *Function {
	var found *Function
	Walk(m, func(n Node) bool {
		if f, ok := n.(*Function); ok && f.Name == name && found == nil {
			found = f
		}
		return found == nil
	})
	return found
}