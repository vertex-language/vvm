package ptx

import "strconv"

// Attr is a variable .attribute qualifier.
type Attr string

const (
	Managed Attr = "managed"
	Unified Attr = "unified"
)

// Initializer is a variable initializer expression.
type Initializer interface{ InitText() string }

type initInts []int64

func (v initInts) InitText() string {
	s := "{"
	for i, x := range v {
		if i > 0 {
			s += ", "
		}
		s += strconv.FormatInt(x, 10)
	}
	return s + "}"
}

// InitInts builds an integer aggregate initializer: = {1, 2, 3}.
func InitInts(vs ...int64) Initializer { return initInts(vs) }

type initFloats []float64

func (v initFloats) InitText() string {
	s := "{"
	for i, x := range v {
		if i > 0 {
			s += ", "
		}
		s += formatFloat(x)
	}
	return s + "}"
}

// InitFloats builds a floating-point aggregate initializer: = {0.0, 0.5}.
func InitFloats(vs ...float64) Initializer { return initFloats(vs) }

type initAddr string

func (v initAddr) InitText() string { return string(v) }

// InitAddr initializes a variable with the address of a symbol.
func InitAddr(symbol string) Initializer { return initAddr(symbol) }

type rawInit string

func (v rawInit) InitText() string { return string(v) }

// RawInit escapes the typed initializers for anything exotic.
func RawInit(s string) Initializer { return rawInit(s) }

// Variable is a module-scoped or function-local variable declaration.
//
// Len: 0 = scalar, -1 = incomplete array ("[]"), n > 0 = "[n]".
type Variable struct {
	Linkage Linkage
	Space   Space
	Align   int
	Type    Type
	Name    string
	Len     int
	Init    Initializer
	Attrs   []Attr
}

// VariableList is an ordered collection of variables.
type VariableList struct{ Items []Variable }

func (l *VariableList) Add(v Variable) { l.Items = append(l.Items, v) }