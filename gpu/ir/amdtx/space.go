package amdtx

// Space is an AMDTX address space. Only 64-bit process address spaces are
// supported for amdgcn, which is why there is no .address_size directive
// (§6, PTX deviation).
type Space uint8

const (
	NoSpace Space = iota
	Generic
	Global
	Constant
	Shared
	Private
)

type spaceInfo struct {
	name    string
	segment string // HSA segment
	ptrBits int
}

var spaceTable = map[Space]spaceInfo{
	Generic:  {"generic", "flat", 64},
	Global:   {"global", "global", 64},
	Constant: {"constant", "constant", 64},
	Shared:   {"shared", "group", 32},
	Private:  {"private", "private", 32},
}

// String returns the dotted space specifier, e.g. ".global".
func (s Space) String() string {
	i, ok := spaceTable[s]
	if !ok {
		return ""
	}
	return "." + i.name
}

// Name returns the bare space name without the leading dot.
func (s Space) Name() string { return spaceTable[s].name }

// Segment returns the HSA segment the space maps onto.
func (s Space) Segment() string { return spaceTable[s].segment }

// PointerBits returns the width of a pointer into the space.
func (s Space) PointerBits() int { return spaceTable[s].ptrBits }

// ScalarLoadable reports whether s may be addressed by s_load/s_store (V8).
func (s Space) ScalarLoadable() bool { return s == Global || s == Constant }

// IsValid reports whether s names a real address space.
func (s Space) IsValid() bool { _, ok := spaceTable[s]; return ok }

// Access is a .param access qualifier.
type Access uint8

const (
	NoAccess Access = iota
	ReadOnly
	WriteOnly
	ReadWrite
)

func (a Access) String() string {
	return [...]string{"", ".read_only", ".write_only", ".read_write"}[a]
}

// ParamKind is a .param kind qualifier. Kernarg layout for these is
// deferred to v1.1 (§19.2); Verify says so rather than guessing.
type ParamKind uint8

const (
	NoParamKind ParamKind = iota
	BufferParam
	DynSharedParam
)

func (k ParamKind) String() string {
	return [...]string{"", ".buffer", ".dynshared"}[k]
}