package msl

import "strconv"

// AttrName is the identifier inside a [[...]] attribute.
type AttrName string

// Binding and indexing attributes.
const (
	AttrBuffer           AttrName = "buffer"
	AttrTexture          AttrName = "texture"
	AttrSampler          AttrName = "sampler"
	AttrThreadgroup      AttrName = "threadgroup"
	AttrID               AttrName = "id"
	AttrFunctionConstant AttrName = "function_constant"
)

// Kernel and dispatch built-ins.
const (
	AttrThreadPositionInGrid          AttrName = "thread_position_in_grid"
	AttrThreadPositionInThreadgroup   AttrName = "thread_position_in_threadgroup"
	AttrThreadgroupPositionInGrid     AttrName = "threadgroup_position_in_grid"
	AttrThreadsPerThreadgroup         AttrName = "threads_per_threadgroup"
	AttrThreadsPerGrid                AttrName = "threads_per_grid"
	AttrThreadIndexInThreadgroup      AttrName = "thread_index_in_threadgroup"
	AttrSIMDGroupIndexInThreadgroup   AttrName = "simdgroup_index_in_threadgroup"
	AttrThreadIndexInSIMDGroup        AttrName = "thread_index_in_simdgroup"
	AttrThreadsPerSIMDGroup           AttrName = "threads_per_simdgroup"
	AttrDispatchThreadsPerThreadgroup AttrName = "dispatch_threads_per_threadgroup"
)

// Graphics-stage built-ins.
const (
	AttrStageIn     AttrName = "stage_in"
	AttrVertexID    AttrName = "vertex_id"
	AttrInstanceID  AttrName = "instance_id"
	AttrPosition    AttrName = "position"
	AttrPointSize   AttrName = "point_size"
	AttrColor       AttrName = "color"
	AttrAttribute   AttrName = "attribute"
	AttrPatch       AttrName = "patch"
	AttrPayload     AttrName = "payload"
	AttrFrontFacing AttrName = "front_facing"
)

// Function-level attributes.
const (
	AttrMaxTotalThreadsPerThreadgroup AttrName = "max_total_threads_per_threadgroup"
	AttrVisible                       AttrName = "visible"
	AttrStitchable                    AttrName = "stitchable"
	AttrEarlyFragmentTests            AttrName = "early_fragment_tests"
	AttrInvariant                     AttrName = "invariant"
)

// Attr is a [[...]] attribute. The zero value means "no attribute".
type Attr struct {
	Name AttrName
	Args []string
}

// IsZero reports whether the attribute is absent.
func (a Attr) IsZero() bool { return a.Name == "" }

// Place is a bit set of the syntactic positions an attribute may occupy.
type Place uint8

// Attribute placements.
const (
	OnParam Place = 1 << iota
	OnField
	OnFunc
	OnReturn
	OnVar
)

type attrSpec struct {
	place Place
	args  int // -1 means any arity
	since Version
}

// attrTable drives Verify. Attributes absent from the table (including those
// built with RawAttr) are accepted anywhere and never version-gated.
var attrTable = map[AttrName]attrSpec{
	AttrBuffer:           {OnParam, 1, Version{}},
	AttrTexture:          {OnParam, 1, Version{}},
	AttrSampler:          {OnParam, 1, Version{}},
	AttrThreadgroup:      {OnParam, 1, Version{}},
	AttrID:               {OnField, 1, Version{}},
	AttrFunctionConstant: {OnVar | OnParam, 1, Version{}},

	AttrThreadPositionInGrid:          {OnParam, 0, Version{}},
	AttrThreadPositionInThreadgroup:   {OnParam, 0, Version{}},
	AttrThreadgroupPositionInGrid:     {OnParam, 0, Version{}},
	AttrThreadsPerThreadgroup:         {OnParam, 0, Version{}},
	AttrThreadsPerGrid:                {OnParam, 0, Version{}},
	AttrThreadIndexInThreadgroup:      {OnParam, 0, Version{}},
	AttrSIMDGroupIndexInThreadgroup:   {OnParam, 0, Version{}},
	AttrThreadIndexInSIMDGroup:        {OnParam, 0, Version{}},
	AttrThreadsPerSIMDGroup:           {OnParam, 0, Version{}},
	AttrDispatchThreadsPerThreadgroup: {OnParam, 0, Version{}},

	AttrStageIn:     {OnParam, 0, Version{}},
	AttrVertexID:    {OnParam, 0, Version{}},
	AttrInstanceID:  {OnParam, 0, Version{}},
	AttrFrontFacing: {OnParam, 0, Version{}},
	AttrPatch:       {OnParam, -1, Version{}},
	AttrPosition:    {OnField | OnReturn, 0, Version{}},
	AttrPointSize:   {OnField | OnReturn, 0, Version{}},
	AttrColor:       {OnField | OnReturn | OnParam, 1, Version{}},
	AttrAttribute:   {OnField, 1, Version{}},
	AttrPayload:     {OnParam, -1, Metal30},

	AttrMaxTotalThreadsPerThreadgroup: {OnFunc, 1, Metal30},
	AttrVisible:                       {OnFunc, 0, Metal23},
	AttrStitchable:                    {OnFunc, 0, Metal24},
	AttrEarlyFragmentTests:            {OnFunc, 0, Version{}},
	AttrInvariant:                     {OnFunc | OnField, 0, Version{}},
}

func idxAttr(n AttrName, i int) Attr {
	return Attr{Name: n, Args: []string{strconv.Itoa(i)}}
}

// Binding attributes.
func Buffer(i int) Attr          { return idxAttr(AttrBuffer, i) }
func TextureAt(i int) Attr       { return idxAttr(AttrTexture, i) }
func SamplerAt(i int) Attr       { return idxAttr(AttrSampler, i) }
func ThreadgroupSlot(i int) Attr { return idxAttr(AttrThreadgroup, i) }
func ID(i int) Attr              { return idxAttr(AttrID, i) }
func FunctionConstant(i int) Attr { return idxAttr(AttrFunctionConstant, i) }

// Indexed stage attributes.
func Color(i int) Attr     { return idxAttr(AttrColor, i) }
func Attribute(i int) Attr { return idxAttr(AttrAttribute, i) }

// MaxTotalThreadsPerThreadgroup is a function-level threadgroup size hint.
func MaxTotalThreadsPerThreadgroup(n int) Attr {
	return idxAttr(AttrMaxTotalThreadsPerThreadgroup, n)
}

// Nullary built-ins.
var (
	ThreadPositionInGrid          = Attr{Name: AttrThreadPositionInGrid}
	ThreadPositionInThreadgroup   = Attr{Name: AttrThreadPositionInThreadgroup}
	ThreadgroupPositionInGrid     = Attr{Name: AttrThreadgroupPositionInGrid}
	ThreadsPerThreadgroup         = Attr{Name: AttrThreadsPerThreadgroup}
	ThreadsPerGrid                = Attr{Name: AttrThreadsPerGrid}
	ThreadIndexInThreadgroup      = Attr{Name: AttrThreadIndexInThreadgroup}
	SIMDGroupIndexInThreadgroup   = Attr{Name: AttrSIMDGroupIndexInThreadgroup}
	ThreadIndexInSIMDGroup        = Attr{Name: AttrThreadIndexInSIMDGroup}
	ThreadsPerSIMDGroup           = Attr{Name: AttrThreadsPerSIMDGroup}
	DispatchThreadsPerThreadgroup = Attr{Name: AttrDispatchThreadsPerThreadgroup}

	StageIn     = Attr{Name: AttrStageIn}
	VertexID    = Attr{Name: AttrVertexID}
	InstanceID  = Attr{Name: AttrInstanceID}
	FrontFacing = Attr{Name: AttrFrontFacing}
	Position    = Attr{Name: AttrPosition}
	PointSize   = Attr{Name: AttrPointSize}

	Visible            = Attr{Name: AttrVisible}
	Stitchable         = Attr{Name: AttrStitchable}
	EarlyFragmentTests = Attr{Name: AttrEarlyFragmentTests}
	Invariant          = Attr{Name: AttrInvariant}
)

// RawAttr is the escape hatch for attributes without a typed constructor.
func RawAttr(name string, args ...string) Attr {
	return Attr{Name: AttrName(name), Args: args}
}