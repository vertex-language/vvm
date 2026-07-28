package amdtx

// Module is the root of the IR: one .amdtx translation unit.
//
// Declarations live in a single ordered list rather than in per-kind lists,
// because AMDTX requires declaration before use and section order is
// normative: preamble, file table, module objects, bodies (§3.1).
type Module struct {
	Version Version
	Target  Target
	Wave    Wave

	decls       []Decl
	files       []*File
	filesByName map[string]*File
}

// NewModule returns a module with the given preamble. Wave width is
// resolved here because every width-dependent rule follows from it.
func NewModule(v Version, t Target, w Wave) *Module {
	return &Module{
		Version:     v,
		Target:      t,
		Wave:        w,
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

// Object declares a module-scope variable and returns a handle usable as an
// operand.
func (m *Module) Object(o Object) *Object {
	p := &o
	m.decls = append(m.decls, p)
	return p
}

// File adds a source file to the .file table, or returns the existing entry
// if the name is already registered.
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

// Kernels returns every kernel in source order.
func (m *Module) Kernels() []*Kernel {
	var out []*Kernel
	for _, d := range m.decls {
		if k, ok := d.(*Kernel); ok {
			out = append(out, k)
		}
	}
	return out
}

// Funcs returns every function in source order.
func (m *Module) Funcs() []*Func {
	var out []*Func
	for _, d := range m.decls {
		if f, ok := d.(*Func); ok {
			out = append(out, f)
		}
	}
	return out
}

// Lookup resolves a name in the flat module symbol namespace shared by
// kernels, functions and objects.
func (m *Module) Lookup(name string) Decl {
	for _, d := range m.decls {
		switch x := d.(type) {
		case *Kernel:
			if x.Name == name {
				return x
			}
		case *Func:
			if x.Name == name {
				return x
			}
		case *Object:
			if x.Name == name {
				return x
			}
		}
	}
	return nil
}