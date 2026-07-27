package ptx

// PtrInfo is .ptr metadata on a kernel parameter:
// .param .u64 .ptr .global .align 16 buf
type PtrInfo struct {
	Space Space
	Align int
}

// Param is a kernel or function parameter (or a function return param).
type Param struct {
	Name  string
	Type  Type
	Align int
	Len   int // array length for byte-array params; 0 = scalar
	Ptr   *PtrInfo
}

// ParamList is an ordered collection of parameters.
type ParamList struct{ Items []Param }

func (l *ParamList) Add(p Param) { l.Items = append(l.Items, p) }

// Callable is the shared core of Kernel (.entry) and Function (.func):
// parameter list, performance-tuning directives, cluster directives, and
// an instruction body. A nil Code with Extern linkage is a declaration.
type Callable struct {
	Name    string
	Linkage Linkage
	Params  ParamList
	Code    *CodeBuilder

	// Performance-tuning directives; zero values are omitted.
	MaxNTid      [3]int // .maxntid x, y, z
	ReqNTid      [3]int // .reqntid x, y, z
	MinNCTAPerSM int    // .minnctapersm n
	MaxNReg      int    // .maxnreg n

	// Cluster directives (sm_90+).
	ReqNCTAPerCluster [3]int // .reqnctapercluster x, y, z
	ExplicitCluster   bool   // .explicitcluster
}

// Kernel is a .entry kernel.
type Kernel struct{ Callable }

// NewKernel returns a kernel with an empty body ready for emission.
func NewKernel(name string) *Kernel {
	return &Kernel{Callable{Name: name, Code: NewCodeBuilder()}}
}

// Function is a .func device function.
type Function struct {
	Callable
	Ret      *Param // optional return parameter
	NoReturn bool   // .noreturn
}

// NewFunction returns a device function with an empty body.
func NewFunction(name string) *Function {
	return &Function{Callable: Callable{Name: name, Code: NewCodeBuilder()}}
}