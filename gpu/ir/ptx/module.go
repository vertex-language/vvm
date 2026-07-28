package ptx

// Module is the root of the IR: one .ptx translation unit.
//
// Declarations are held in a single ordered list rather than in separate
// per-kind lists, because PTX requires declaration before use and the order
// in which globals, function declarations and definitions appear is
// semantically meaningful.
type Module struct {
	Version     ISAVersion
	Target      Target
	TargetOpts  []TargetOpt
	AddressSize AddrSize

	BlocksAreClusters bool // .blocksareclusters; requires ISA >= 9.0

	Pragmas []string

	decls      []Decl
	files      []*File
	filesByName map[string]*File
	protoSeq   int
}

// NewModule returns a module with the given header directives.
func NewModule(v ISAVersion, t Target, size AddrSize) *Module {
	return &Module{
		Version:     v,
		Target:      t,
		AddressSize: size,
		filesByName: map[string]*File{},
	}
}

// Add appends a declaration and returns it.
func (m *Module) Add(d Decl) Decl {
	m.decls = append(m.decls, d)
	return d
}

// Decls returns the declarations in source order.
func (m *Module) Decls() []Decl { return m.decls }

// Var declares a module-scoped variable and returns a handle usable as an
// operand.
func (m *Module) Var(v Var) *Var {
	p := &v
	m.decls = append(m.decls, p)
	return p
}

// Alias declares a .alias binding name to target.
func (m *Module) Alias(name string, target *Func) *Alias {
	a := &Alias{Name: name, Target: target}
	m.decls = append(m.decls, a)
	return a
}

// CallProto declares a .callprototype and returns a handle for use with
// Body.CallIndirect.
func (m *Module) CallProto(ret []Type, params []Type) *Proto {
	p := &Proto{Name: "$L__proto" + itoa(m.protoSeq), Ret: ret, Params: params}
	m.protoSeq++
	m.decls = append(m.decls, p)
	return p
}

// Section declares a .section carrying raw bytes.
func (m *Module) Section(name string, data []byte) *Section {
	s := &Section{Name: name, Data: data}
	m.decls = append(m.decls, s)
	return s
}

// File adds a source file to the .file table, or returns the existing entry
// if the name has already been registered.
func (m *Module) File(name string) *File {
	if f, ok := m.filesByName[name]; ok {
		return f
	}
	f := &File{Index: len(m.files) + 1, Name: name}
	m.files = append(m.files, f)
	m.filesByName[name] = f
	return f
}

// Files returns the .file table in index order.
func (m *Module) Files() []*File { return m.files }

// Pragma appends a module-scope .pragma directive.
func (m *Module) Pragma(s ...string) { m.Pragmas = append(m.Pragmas, s...) }