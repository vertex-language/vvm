package msl

// Decl is implemented by every module-scope declaration.
type Decl interface{ isDecl() }

// Include is a #include line. Local selects "quotes" over <angle brackets>.
type Include struct {
	Name  string
	Local bool
}

func (*Include) isDecl() {}

// Using is a `using namespace N;` line.
type Using struct{ Namespace string }

func (*Using) isDecl() {}

// Alias is a `using Name = Type;` declaration. Metal 4's operator descriptors
// are long template spellings; aliasing them keeps bodies readable.
type Alias struct {
	Name string
	Type Type
}

func (*Alias) isDecl() {}

// Type returns a reference to the alias.
func (a *Alias) Type_() NamedType { return NamedType(a.Name) }

// CommentDecl is a standalone // line at module scope.
type CommentDecl struct{ Text string }

func (*CommentDecl) isDecl() {}

// RawDecl is verbatim text at module scope.
type RawDecl struct{ Text string }

func (*RawDecl) isDecl() {}

// PPIfDecl is a preprocessor conditional around declarations.
type PPIfDecl struct {
	Cond string
	Then []Decl
	Els  []Decl
}

func (*PPIfDecl) isDecl() {}

// Field is a struct member with optional attributes.
type Field struct {
	Name  string
	Type  Type
	Attrs []Attr
}

// Struct is a struct definition. Vertex and fragment IO, argument buffers, and
// imageblock layouts are all structs with attributed fields. MSL is C++, so a
// struct may also carry member functions.
type Struct struct {
	Name    string
	Fields  []*Field
	Methods []*Function
}

func (*Struct) isDecl() {}

// NewStruct creates an empty struct definition.
func NewStruct(name string) *Struct { return &Struct{Name: name} }

// Field appends a field and returns its handle, for use with Expr.Fld.
func (s *Struct) Field(name string, t Type, attrs ...Attr) *Field {
	f := &Field{Name: name, Type: t, Attrs: attrs}
	s.Fields = append(s.Fields, f)
	return f
}

// Method appends a member function and returns it.
func (s *Struct) Method(name string, ret Type) *Function {
	f := &Function{Name: name, Ret: ret, Body: NewBlock()}
	s.Methods = append(s.Methods, f)
	return f
}

// Type returns a reference to the struct by name.
func (s *Struct) Type() NamedType { return NamedType(s.Name) }

// Param is a function parameter.
type Param struct {
	Name  string
	Type  Type
	Attrs []Attr
}

// Ref returns a reference to the parameter.
func (p *Param) Ref() Expr { return Name(p.Name) }

// Stage is a function's stage qualifier.
type Stage string

// Stage qualifiers. Plain functions carry none and are distinguished by their
// attributes ([[visible]], [[stitchable]]).
const (
	Plain             Stage = ""
	KernelStage       Stage = "kernel"
	VertexStage       Stage = "vertex"
	FragmentStage     Stage = "fragment"
	IntersectionStage Stage = "intersection"
	ObjectStage       Stage = "object"
	MeshStage         Stage = "mesh"
)

// Function is a callable. A nil Body means declaration only; a nil Ret means
// void. There are no per-stage types: the stage is a field, because that is
// what it is in the grammar.
type Function struct {
	Stage  Stage
	Name   string
	Ret    Type
	RetAttrs []Attr // attributes on the return type, e.g. [[position]]
	Params []*Param
	Attrs  []Attr
	Body   *Block
}

func (*Function) isDecl() {}

func newFn(stage Stage, name string) *Function {
	return &Function{Stage: stage, Name: name, Body: NewBlock()}
}

// Stage constructors.
func NewKernel(name string) *Function   { return newFn(KernelStage, name) }
func NewVertex(name string) *Function   { return newFn(VertexStage, name) }
func NewFragment(name string) *Function { return newFn(FragmentStage, name) }
func NewFunction(name string) *Function { return newFn(Plain, name) }

// NewStageFunction creates a function with any other stage qualifier.
func NewStageFunction(stage Stage, name string) *Function { return newFn(stage, name) }

// Param appends a parameter and returns a reference to it. Thread and dispatch
// indices are declaration-site concerns: declare an attributed parameter once,
// then use the returned Expr throughout the body.
func (f *Function) Param(name string, t Type, attrs ...Attr) Expr {
	f.Params = append(f.Params, &Param{Name: name, Type: t, Attrs: attrs})
	return Name(name)
}

// Attr appends function-level attributes and returns the receiver.
func (f *Function) Attr(a ...Attr) *Function {
	f.Attrs = append(f.Attrs, a...)
	return f
}