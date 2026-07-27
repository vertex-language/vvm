package msl

// Param is a function parameter: name, type, and optional [[...]] attribute.
// Thread and dispatch indices are declaration-site concerns — declare an
// attributed parameter once, then use the returned Expr everywhere.
type Param struct {
	Name string
	Type Type
	Attr Attr // zero value = no attribute
}

// ParamList is an append-only, ordered parameter collection whose names
// share the function body's scope for uniquification.
type ParamList struct {
	items []Param
	scope *CodeBuilder
}

// Add appends a parameter and returns its identifier as an Expr for use
// in the body. If the parameter name collides with an earlier name, it
// is uniquified.
func (l *ParamList) Add(p Param) Expr {
	if l.scope != nil {
		p.Name = l.scope.reserve(p.Name)
	}
	l.items = append(l.items, p)
	return Ident(p.Name)
}

// Items returns the parameters in declaration order.
func (l *ParamList) Items() []Param { return l.items }

// Function is the shared callable core of every MSL function kind. The
// Qualifier field selects the stage: "kernel", "vertex", "fragment", ""
// (plain / [[visible]] / [[stitchable]]), or a later stage qualifier
// such as "intersection", "object", or "mesh" via NewStageFunction.
//
// A nil Code means declaration only. A nil Ret means void.
type Function struct {
	Qualifier string
	Name      string
	Ret       Type
	Params    ParamList
	Attrs     AttrList
	Code      *CodeBuilder
}

// Kernel, Vertex, and Fragment are documentation aliases: all function
// kinds share the same callable core.
type (
	Kernel   = Function
	Vertex   = Function
	Fragment = Function
)

func newFn(qualifier, name string) *Function {
	f := &Function{Qualifier: qualifier, Name: name, Code: NewCodeBuilder()}
	f.Params.scope = f.Code
	return f
}

// NewKernel creates a kernel (compute) function with an empty body.
func NewKernel(name string) *Kernel { return newFn("kernel", name) }

// NewVertex creates a vertex function with an empty body.
func NewVertex(name string) *Vertex { return newFn("vertex", name) }

// NewFragment creates a fragment function with an empty body.
func NewFragment(name string) *Fragment { return newFn("fragment", name) }

// NewFunction creates a plain function (add Visible or Stitchable via
// Attrs for [[visible]] / [[stitchable]]).
func NewFunction(name string) *Function { return newFn("", name) }

// NewStageFunction creates a function with a later stage qualifier such
// as "intersection", "object", or "mesh".
func NewStageFunction(qualifier, name string) *Function { return newFn(qualifier, name) }

// FuncList is an append-only collection of functions.
type FuncList struct{ items []*Function }

// Add appends a function.
func (l *FuncList) Add(f *Function) { l.items = append(l.items, f) }

// Items returns the functions in insertion order.
func (l *FuncList) Items() []*Function { return l.items }