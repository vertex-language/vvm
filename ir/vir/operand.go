package vir

import (
	"fmt"
	"strconv"
)

// OperandKind discriminates Operand payloads (§1.1 operand grammar).
type OperandKind int

const (
	OIdent    OperandKind = iota // value / global / const / fn / struct / field name
	OInt                         // integer literal
	OFloat                       // float literal (incl. NaN, Inf, -Inf)
	OString                      // byte-string literal
	OBool                        // true / false
	ONull                        // null (ptr)
	OType                        // a type used in operand position (index.ptr)
	OOrdering                    // relaxed | acquire | release | acqrel | seqcst
	OVecLit                      // (0, 4, 1, 5) — shuffle masks, vector consts
)

type Operand struct {
	Kind  OperandKind
	Ident string
	Int   int64
	Float float64
	Str   string
	Bool  bool
	Type  Type
	Ord   string
	Vec   []int64
}

// Constructors — the builder-facing spelling of each operand form.
func V(name string) Operand      { return Operand{Kind: OIdent, Ident: name} }
func Int(v int64) Operand        { return Operand{Kind: OInt, Int: v} }
func Flt(v float64) Operand      { return Operand{Kind: OFloat, Float: v} }
func Str(s string) Operand       { return Operand{Kind: OString, Str: s} }
func Bl(v bool) Operand          { return Operand{Kind: OBool, Bool: v} }
func Null() Operand              { return Operand{Kind: ONull} }
func Ty(t Type) Operand          { return Operand{Kind: OType, Type: t} }
func Ord(o string) Operand       { return Operand{Kind: OOrdering, Ord: o} }
func VecLit(v ...int64) Operand  { return Operand{Kind: OVecLit, Vec: v} }

func (o Operand) String() string {
	switch o.Kind {
	case OIdent:
		return o.Ident
	case OInt:
		return strconv.FormatInt(o.Int, 10)
	case OFloat:
		return formatFloat(o.Float)
	case OString:
		return strconv.Quote(o.Str)
	case OBool:
		if o.Bool {
			return "true"
		}
		return "false"
	case ONull:
		return "null"
	case OType:
		return o.Type.String()
	case OOrdering:
		return o.Ord
	case OVecLit:
		s := "("
		for i, v := range o.Vec {
			if i > 0 {
				s += ", "
			}
			s += strconv.FormatInt(v, 10)
		}
		return s + ")"
	}
	return fmt.Sprintf("<bad operand kind %d>", o.Kind)
}