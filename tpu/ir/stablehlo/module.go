// Package stablehlo is an in-memory intermediate representation (IR) for
// StableHLO modules. It models the structure of a StableHLO translation
// unit — module, functions, and SSA op bodies — without any formatting
// logic; text printing lives in encoding/text.
//
// This package never type-checks: result types are computed by trivial
// shape propagation where possible and are caller-supplied everywhere
// else. stablehlo-opt is the verifier of record, and portable artifacts
// (the only form carrying compatibility guarantees) are produced by piping
// the printed text through stablehlo-translate --serialize.
package stablehlo

// Module is the root of the IR. It corresponds to a single MLIR module op.
type Module struct {
	Name  string
	Attrs AttrList
	Funcs FuncList

	targetVersion Version
}

// NewModule creates a module with the package's pinned opset version as
// its advisory TargetVersion.
func NewModule(name string) *Module {
	return &Module{Name: name, targetVersion: DefaultVersion}
}

// SetTargetVersion sets the advisory serialization floor. It is used only
// by the printer's linter and as the suggested --target for
// stablehlo-translate; it gates nothing in this package.
func (m *Module) SetTargetVersion(v Version) { m.targetVersion = v }

// TargetVersion returns the advisory target version.
func (m *Module) TargetVersion() Version { return m.targetVersion }

// AttrList is an ordered list of module-level attributes.
type AttrList struct{ attrs []NamedAttr }

func (l *AttrList) Add(a NamedAttr)  { l.attrs = append(l.attrs, a) }
func (l *AttrList) All() []NamedAttr { return l.attrs }

// FuncList is an ordered list of functions; printing preserves insertion
// order.
type FuncList struct{ funcs []*Func }

func (l *FuncList) Add(f *Func)  { l.funcs = append(l.funcs, f) }
func (l *FuncList) All() []*Func { return l.funcs }

// Param is a function parameter.
type Param struct {
	Name string
	Type Type
}

// ParamList holds a function's parameters. Add returns the parameter's
// SSA Value.
type ParamList struct {
	params []Param
	values []Value
}

func (pl *ParamList) Add(p Param) Value {
	v := newValue(p.Type)
	pl.params = append(pl.params, p)
	pl.values = append(pl.values, v)
	return v
}

func (pl *ParamList) All() []Param     { return pl.params }
func (pl *ParamList) Values() []Value  { return pl.values }
func (pl *ParamList) Len() int         { return len(pl.params) }

// Func corresponds to func.func. A nil Code body is a declaration only.
type Func struct {
	Name    string
	Params  ParamList
	Results []Type
	Private bool

	// ArgAttrs / ResAttrs attach one raw attribute per parameter / result
	// index (e.g. mhlo.sharding).
	ArgAttrs map[int]NamedAttr
	ResAttrs map[int]NamedAttr

	Code *CodeBuilder
}

// NewFunc creates a function with an empty body builder.
func NewFunc(name string) *Func {
	f := &Func{
		Name:     name,
		ArgAttrs: map[int]NamedAttr{},
		ResAttrs: map[int]NamedAttr{},
	}
	f.Code = &CodeBuilder{fn: f, terminator: "func.return"}
	return f
}