package ptx

import "strconv"

// Decl is a module-scope declaration.
type Decl interface{ decl() }

// Dim3 is a three-element dimension directive operand. The zero value means
// the directive is omitted.
type Dim3 [3]int

// IsZero reports whether the directive should be omitted.
func (d Dim3) IsZero() bool { return d[0] == 0 && d[1] == 0 && d[2] == 0 }

func (d Dim3) String() string {
	return strconv.Itoa(d[0]) + ", " + strconv.Itoa(d[1]) + ", " + strconv.Itoa(d[2])
}

// ---- Variables ------------------------------------------------------------

// Init is a variable initializer expression.
type Init interface{ InitText() string }

type initList struct {
	elems []string
}

func (v initList) InitText() string {
	s := "{"
	for i, e := range v.elems {
		if i > 0 {
			s += ", "
		}
		s += e
	}
	return s + "}"
}

// InitS builds a signed integer initializer.
func InitS(vs ...int64) Init {
	e := make([]string, len(vs))
	for i, v := range vs {
		e[i] = Imm(v).Text()
	}
	return initList{e}
}

// InitU builds an unsigned integer initializer.
func InitU(vs ...uint64) Init {
	e := make([]string, len(vs))
	for i, v := range vs {
		e[i] = UImm(v).Text()
	}
	return initList{e}
}

// InitF32 builds a single-precision initializer in exact-bits form.
func InitF32(vs ...float32) Init {
	e := make([]string, len(vs))
	for i, v := range vs {
		e[i] = F32Imm(v).Text()
	}
	return initList{e}
}

// InitF64 builds a double-precision initializer in exact-bits form.
func InitF64(vs ...float64) Init {
	e := make([]string, len(vs))
	for i, v := range vs {
		e[i] = F64Imm(v).Text()
	}
	return initList{e}
}

type initSym struct{ o Operand }

func (v initSym) InitText() string { return v.o.Text() }

// InitAddr initializes a variable with the address of a symbol.
func InitAddr(o Operand) Init { return initSym{o} }

type initNested struct{ parts []Init }

func (v initNested) InitText() string {
	s := "{"
	for i, p := range v.parts {
		if i > 0 {
			s += ", "
		}
		s += p.InitText()
	}
	return s + "}"
}

// InitNest builds a nested aggregate initializer.
func InitNest(parts ...Init) Init { return initNested{parts} }

// Var is a module-scoped or function-scoped variable declaration. It is also
// an operand: a *Var may be used directly wherever its symbol is expected.
//
// Len: 0 means scalar, -1 means an incomplete array "[]", n > 0 means "[n]".
// Vec: 0 means scalar, 2 or 4 select ".v2" or ".v4".
type Var struct {
	Linkage Linkage
	Space   Space
	Align   int
	Type    Type
	Vec     int
	Name    string
	Len     int
	Init    Init
	Attrs   []Attr
}

func (v *Var) Text() string { return v.Name }
func (*Var) operand()       {}
func (*Var) addressable()   {}
func (*Var) decl()          {}

// ---- Parameters -----------------------------------------------------------

// PtrInfo is .ptr metadata on a kernel parameter.
type PtrInfo struct {
	Space Space
	Align int
}

// Param is a kernel, function, or return parameter. It is also an operand.
type Param struct {
	Name  string
	Type  Type
	Align int
	Len   int // byte-array parameters; 0 means scalar
	Ptr   *PtrInfo
}

func (p *Param) Text() string { return p.Name }
func (*Param) operand()       {}
func (*Param) addressable()   {}

// ---- Callables ------------------------------------------------------------

// Callable is the shared core of Kernel and Func: a parameter list,
// performance-tuning and cluster directives, and a body. A nil Body with
// Extern linkage is a declaration.
type Callable struct {
	Name    string
	Linkage Linkage
	Params  []*Param
	Body    *Body
	Attrs   []Attr
	Pragmas []string

	// Performance-tuning directives; zero values are omitted.
	MaxNTid      Dim3
	ReqNTid      Dim3
	MinNCTAPerSM int
	MaxNReg      int

	// Cluster dimension directives; require sm_90 or higher.
	ReqNCTAPerCluster Dim3
	MaxClusterRank    int
	ExplicitCluster   bool
}

// Param appends a parameter and returns a handle to it.
func (c *Callable) Param(name string, t Type) *Param {
	p := &Param{Name: name, Type: t}
	c.Params = append(c.Params, p)
	return p
}

// ParamPtr appends a parameter carrying .ptr metadata.
func (c *Callable) ParamPtr(name string, t Type, space Space, align int) *Param {
	p := &Param{Name: name, Type: t, Ptr: &PtrInfo{Space: space, Align: align}}
	c.Params = append(c.Params, p)
	return p
}

// ParamArray appends a byte-array parameter.
func (c *Callable) ParamArray(name string, align, n int) *Param {
	p := &Param{Name: name, Type: B8, Align: align, Len: n}
	c.Params = append(c.Params, p)
	return p
}

// Kernel is a .entry kernel.
type Kernel struct{ Callable }

// NewKernel returns a kernel with an empty body ready for emission.
func NewKernel(name string) *Kernel {
	return &Kernel{Callable{Name: name, Body: NewBody()}}
}

func (*Kernel) decl() {}

// Func is a .func device function.
type Func struct {
	Callable
	Ret      []*Param
	NoReturn bool
}

// NewFunc returns a device function with an empty body.
func NewFunc(name string) *Func {
	return &Func{Callable: Callable{Name: name, Body: NewBody()}}
}

// Return appends a return parameter and returns a handle to it.
func (f *Func) Return(name string, t Type) *Param {
	p := &Param{Name: name, Type: t}
	f.Ret = append(f.Ret, p)
	return p
}

func (f *Func) Text() string { return f.Name }
func (*Func) operand()       {}
func (*Func) decl()          {}

// ---- Prototypes, aliases, sections ----------------------------------------

// Proto is a .callprototype describing the signature of an indirect call.
type Proto struct {
	Name   string
	Ret    []Type
	Params []Type
	NoReturn bool
}

func (p *Proto) Text() string { return p.Name }
func (*Proto) operand()       {}
func (*Proto) decl()          {}

// Alias is a .alias directive binding a name to an existing function.
type Alias struct {
	Name   string
	Target *Func
}

func (*Alias) decl() {}

// Section is a .section directive carrying raw bytes, used for binary DWARF.
type Section struct {
	Name string
	Data []byte
}

func (*Section) decl() {}

// File is an entry in the module's .file table. Indices are 1-based.
type File struct {
	Index     int
	Name      string
	Timestamp uint64
	Size      uint64
}