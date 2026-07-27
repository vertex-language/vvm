package msl

import "strconv"

// Attr models a [[...]] attribute on a parameter, struct field, or
// function. The zero value means "no attribute".
type Attr struct {
	Name string
	Args []string
}

// IsZero reports whether the attribute is absent.
func (a Attr) IsZero() bool { return a.Name == "" }

// String returns the [[...]] spelling of the attribute.
func (a Attr) String() string {
	if a.IsZero() {
		return ""
	}
	s := "[[" + a.Name
	if len(a.Args) > 0 {
		s += "("
		for i, arg := range a.Args {
			if i > 0 {
				s += ", "
			}
			s += arg
		}
		s += ")"
	}
	return s + "]]"
}

func indexAttr(name string, i int) Attr {
	return Attr{Name: name, Args: []string{strconv.Itoa(i)}}
}

// Binding attributes (parameters).
func Buffer(i int) Attr          { return indexAttr("buffer", i) }
func Texture(i int) Attr         { return indexAttr("texture", i) }
func SamplerAttr(i int) Attr     { return indexAttr("sampler", i) }
func ThreadgroupSlot(i int) Attr { return indexAttr("threadgroup", i) }

// Kernel built-ins (parameters).
var (
	ThreadPositionInGrid          = Attr{Name: "thread_position_in_grid"}
	ThreadPositionInThreadgroup   = Attr{Name: "thread_position_in_threadgroup"}
	ThreadgroupPositionInGrid     = Attr{Name: "threadgroup_position_in_grid"}
	ThreadsPerThreadgroup         = Attr{Name: "threads_per_threadgroup"}
	ThreadsPerGrid                = Attr{Name: "threads_per_grid"}
	ThreadIndexInThreadgroup      = Attr{Name: "thread_index_in_threadgroup"}
	SIMDGroupIndexInThreadgroup   = Attr{Name: "simdgroup_index_in_threadgroup"}
	ThreadIndexInSIMDGroup        = Attr{Name: "thread_index_in_simdgroup"}
	DispatchThreadsPerThreadgroup = Attr{Name: "dispatch_threads_per_threadgroup"}
)

// Vertex/fragment built-ins (params and struct fields).
var (
	StageIn    = Attr{Name: "stage_in"}
	VertexID   = Attr{Name: "vertex_id"}
	InstanceID = Attr{Name: "instance_id"}
	Position   = Attr{Name: "position"}
	PointSize  = Attr{Name: "point_size"}
)

// ColorAttr is [[color(i)]] on fragment outputs and imageblock fields.
func ColorAttr(i int) Attr { return indexAttr("color", i) }

// AttributeAttr is [[attribute(i)]] on stage-in struct fields.
func AttributeAttr(i int) Attr { return indexAttr("attribute", i) }

// Function-level attributes.
func MaxTotalThreadsPerThreadgroup(n int) Attr {
	return indexAttr("max_total_threads_per_threadgroup", n)
}

var (
	Visible    = Attr{Name: "visible"}
	Stitchable = Attr{Name: "stitchable"}
)

// RawAttr is the escape hatch for attributes without a typed constructor,
// e.g. RawAttr("early_fragment_tests").
func RawAttr(text string) Attr { return Attr{Name: text} }

// AttrList is an append-only collection of attributes.
type AttrList struct{ items []Attr }

// Add appends an attribute.
func (l *AttrList) Add(a Attr) { l.items = append(l.items, a) }

// Items returns the attributes in insertion order.
func (l *AttrList) Items() []Attr { return l.items }