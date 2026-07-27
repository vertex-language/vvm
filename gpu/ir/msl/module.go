package msl

// Const is a module-scoped constant address space global:
// constant float kPi = 3.14159265;
type Const struct {
	Type Type
	Name string
	Init Expr
}

// ConstList is an append-only constant collection.
type ConstList struct{ items []Const }

// Add appends a module constant.
func (l *ConstList) Add(c Const) { l.items = append(l.items, c) }

// Items returns the constants in declaration order.
func (l *ConstList) Items() []Const { return l.items }

// FnConst is a function constant — MSL's link-time specialization
// mechanism, filled in on the host via MTLFunctionConstantValues:
// constant bool useFastPath [[function_constant(0)]];
type FnConst struct {
	Type  Type
	Name  string
	Index int
}

// FnConstList is an append-only function-constant collection.
type FnConstList struct{ items []FnConst }

// Add appends a function constant.
func (l *FnConstList) Add(c FnConst) { l.items = append(l.items, c) }

// Items returns the function constants in declaration order.
func (l *FnConstList) Items() []FnConst { return l.items }

// IncludeList is an ordered, deduplicated system header list.
type IncludeList struct{ items []string }

// Add appends a system header (without angle brackets), skipping
// duplicates: Add("metal_tensor") prints #include <metal_tensor>.
func (l *IncludeList) Add(name string) {
	for _, it := range l.items {
		if it == name {
			return
		}
	}
	l.items = append(l.items, name)
}

// Items returns the headers in insertion order.
func (l *IncludeList) Items() []string { return l.items }

// Module is the root of the IR. It corresponds to a single .metal
// translation unit.
type Module struct {
	Version         Version
	Includes        IncludeList
	UsingNamespaces []string
	Consts          ConstList
	FnConsts        FnConstList
	Structs         StructList
	Funcs           FuncList
}

// NewModule creates a module with the defaults: Metal32,
// #include <metal_stdlib>, using namespace metal. The default favors
// deployment reach — set Metal40+ explicitly for tensor work.
func NewModule() *Module {
	m := &Module{
		Version:         Metal32,
		UsingNamespaces: []string{"metal"},
	}
	m.Includes.Add("metal_stdlib")
	return m
}

// SetVersion sets the advisory language version (lint floor and
// suggested -std= flag).
func (m *Module) SetVersion(v Version) { m.Version = v }