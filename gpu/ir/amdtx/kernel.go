package amdtx

// Kernel is an entry-point function invoked via an AQL dispatch packet. Its
// fields mirror the AMDHSA kernel descriptor + code-object metadata: the
// segment sizes carried in the dispatch packet, kernarg layout, and the
// occupancy/launch-bounds hints that ptx expresses with .reqntid / .maxntid.
type Kernel struct {
	Name    string
	Wave    Wave // WaveDefault inherits from module/target
	Visible bool // .visible vs .local linkage

	// ABI / segment sizing (mirrors the AMDHSA kernel descriptor + metadata).
	GroupSegmentFixedSize   uint32 // LDS bytes per work-group (group_segment_fixed_size)
	PrivateSegmentFixedSize uint32 // scratch bytes per work-item (private_segment_fixed_size)
	KernargSize             uint32 // total kernarg segment size
	KernargAlign            uint32 // kernarg segment alignment (default 8)

	// Launch bounds / occupancy hints. Zero means "unset" and the directive is
	// omitted. ReqdWorkGroupSize is ptx's .reqntid analogue (reqd_work_group_size
	// metadata); WavesPerEU maps to the amdgpu-waves-per-eu attribute.
	ReqdWorkGroupSize [3]uint32 // .reqd_workgroup_size x, y, z
	MaxFlatWorkGroup  uint32    // .max_flat_workgroup (amdgpu-flat-work-group-size upper)
	WavesPerEU        [2]uint32 // .waves_per_eu min, max

	Args argList
	Code *CodeBuilder
}

// NewKernel creates a visible kernel with an attached code builder.
func NewKernel(name string) *Kernel {
	k := &Kernel{Name: name, Visible: true, KernargAlign: 8}
	k.Code = newCodeBuilder()
	return k
}

// EffectiveWave resolves the kernel's wave size against a module target.
func (k *Kernel) EffectiveWave(t Target) int { return k.Wave.Lanes(t) }

// hasLaunchBounds reports whether any occupancy/launch directive is set.
func (k *Kernel) hasLaunchBounds() bool {
	return k.ReqdWorkGroupSize != [3]uint32{} ||
		k.MaxFlatWorkGroup != 0 ||
		k.WavesPerEU != [2]uint32{}
}

// Function is a (potentially callable) non-entry helper.
type Function struct {
	Name   string
	Params []*Param
	Ret    *Param
	Code   *CodeBuilder
}

// NewFunction creates a helper function with an attached code builder.
func NewFunction(name string) *Function {
	return &Function{Name: name, Code: newCodeBuilder()}
}

// Param is a typed formal parameter or return value.
type Param struct {
	Name string
	Type Type
}

// ArgKind classifies a kernel argument.
type ArgKind int

const (
	ArgByValue ArgKind = iota
	ArgGlobalBuffer
	ArgDynamicShared
	ArgSampler
	ArgImage

	// Hidden ABI args appended by the runtime/ABI.
	ArgHiddenGlobalOffsetX
	ArgHiddenGlobalOffsetY
	ArgHiddenGlobalOffsetZ
	ArgHiddenGridDims
	ArgHiddenPrintfBuffer
	ArgHiddenHeapV1
)

func (k ArgKind) String() string {
	switch k {
	case ArgByValue:
		return "by_value"
	case ArgGlobalBuffer:
		return "global_buffer"
	case ArgDynamicShared:
		return "dynamic_shared_pointer"
	case ArgSampler:
		return "sampler"
	case ArgImage:
		return "image"
	case ArgHiddenGlobalOffsetX:
		return "hidden_global_offset_x"
	case ArgHiddenGlobalOffsetY:
		return "hidden_global_offset_y"
	case ArgHiddenGlobalOffsetZ:
		return "hidden_global_offset_z"
	case ArgHiddenGridDims:
		return "hidden_grid_dims"
	case ArgHiddenPrintfBuffer:
		return "hidden_printf_buffer"
	case ArgHiddenHeapV1:
		return "hidden_heap_v1"
	}
	return "hidden_none"
}

func (k ArgKind) hidden() bool { return k >= ArgHiddenGlobalOffsetX }

// Access is the access qualifier for buffer/image args.
type Access int

const (
	AccessReadOnly Access = iota
	AccessWriteOnly
	AccessReadWrite
)

func (a Access) String() string {
	switch a {
	case AccessWriteOnly:
		return "write_only"
	case AccessReadWrite:
		return "read_write"
	}
	return "read_only"
}

// KernelArg is a single kernel argument descriptor.
type KernelArg struct {
	Name      string
	Kind      ArgKind
	Size      uint32
	Offset    uint32
	AddrSpace AddrSpace
	Access    Access
	// ValueType is the element type for by-value/pointer args (used by text +
	// metadata). Defaults to U64 for buffers, U32 for by-value.
	ValueType Type
	typeSet   bool
}

// WithType sets the value type explicitly and marks it as user-specified.
func (a KernelArg) WithType(t Type) KernelArg {
	a.ValueType = t
	a.typeSet = true
	return a
}

type argList struct{ items []KernelArg }

func (l *argList) Add(a KernelArg)     { l.items = append(l.items, a) }
func (l *argList) Items() []KernelArg  { return l.items }
func (l *argList) Len() int            { return len(l.items) }

// resolvedType returns the arg's value type, applying kind-based defaults.
func (a KernelArg) resolvedType() Type {
	if a.typeSet {
		return a.ValueType
	}
	if a.Kind == ArgGlobalBuffer || a.Kind == ArgDynamicShared {
		return U64
	}
	return U32
}