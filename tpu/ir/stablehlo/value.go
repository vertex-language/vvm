package stablehlo

// Value is the central SSA handle: function parameters are Values, and
// every emit method returns one (or several). Values are immutable and
// single-assignment. A Value knows its Type, but this is bookkeeping, not
// verification.
type Value struct {
	n *valueNode
}

type valueNode struct {
	typ Type
}

// Type returns the type recorded for this value.
func (v Value) Type() Type { return v.n.typ }

// IsValid reports whether v was produced by a builder (the zero Value is
// invalid).
func (v Value) IsValid() bool { return v.n != nil }

func newValue(t Type) Value { return Value{&valueNode{typ: t}} }

// Op is a single emitted operation. Fields are exported for the printer
// sub-package; user code normally never constructs Ops directly.
type Op struct {
	Name        string // e.g. "stablehlo.add", "func.return"
	Operands    []Value
	Attrs       []NamedAttr
	Regions     []*Region
	ResultTypes []Type

	// Raw escape hatch: when RawFormat != "", the op prints as the format
	// string with RawOperands' printed names interpolated for %s verbs.
	RawFormat   string
	RawOperands []Value

	Comment string // trailing // comment (printed with WithComments)

	// MinVersion is the opset version that introduced this op (zero = no
	// constraint). Advisory; used only by the printer's linter.
	MinVersion Version

	results []Value
}

// Results returns the op's result values, in order.
func (o *Op) Results() []Value { return o.results }

// Region is a single-block region carried by ops like reduce and while.
// StableHLO has no jump ops, so a region is exactly one block.
type Region struct {
	ArgTypes []Type
	Body     *CodeBuilder

	args []Value
}

// Args returns the region's block-argument values, in order.
func (r *Region) Args() []Value { return r.args }