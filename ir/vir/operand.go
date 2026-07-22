// operand.go
package vir

import (
	"fmt"
	"strconv"
)

// OperandKind discriminates Operand payloads (§1.1 operand grammar).
type OperandKind int

const (
	OperandIdent    OperandKind = iota // value / global / const / fn / struct / field name
	OperandInt                         // integer literal
	OperandFloat                       // float literal (incl. NaN, Inf, -Inf)
	OperandString                      // byte-string literal
	OperandBool                        // true / false
	OperandNull                        // null (ptr)
	OperandType                        // a type used in operand position (index.ptr)
	OperandOrdering                    // relaxed | acquire | release | acqrel | seqcst
	OperandVector                      // (0, 4, 1, 5) — shuffle masks, vector consts
)

// Operand is one operand-grammar value. A qualified-ident (§7.3,
// "module.name") sets Qualifier to the import path/module name alongside
// Kind == OperandIdent and Ident holding the referenced name; an ordinary
// local ident leaves Qualifier "".
type Operand struct {
	Kind      OperandKind
	Ident     string
	Qualifier string
	Int       int64
	Float     float64
	Str       string
	Bool      bool
	Type      Type
	Ordering  string
	Vector    []int64
}

// Constructors — the builder-facing spelling of each operand form.
func Ident(name string) Operand        { return Operand{Kind: OperandIdent, Ident: name} }
func QualifiedIdent(module, name string) Operand {
	return Operand{Kind: OperandIdent, Ident: name, Qualifier: module}
}
func IntLiteral(v int64) Operand       { return Operand{Kind: OperandInt, Int: v} }
func FloatLiteral(v float64) Operand   { return Operand{Kind: OperandFloat, Float: v} }
func StringLiteral(s string) Operand   { return Operand{Kind: OperandString, Str: s} }
func BoolLiteral(v bool) Operand       { return Operand{Kind: OperandBool, Bool: v} }
func NullLiteral() Operand             { return Operand{Kind: OperandNull} }
func TypeOperand(t Type) Operand       { return Operand{Kind: OperandType, Type: t} }
func OrderingOperand(o string) Operand { return Operand{Kind: OperandOrdering, Ordering: o} }
func VectorLiteral(v ...int64) Operand { return Operand{Kind: OperandVector, Vector: v} }

func (o Operand) IsQualified() bool { return o.Kind == OperandIdent && o.Qualifier != "" }

func (o Operand) String() string {
	switch o.Kind {
	case OperandIdent:
		if o.Qualifier != "" {
			return o.Qualifier + "." + o.Ident
		}
		return o.Ident
	case OperandInt:
		return strconv.FormatInt(o.Int, 10)
	case OperandFloat:
		return formatFloat(o.Float)
	case OperandString:
		return strconv.Quote(o.Str)
	case OperandBool:
		if o.Bool {
			return "true"
		}
		return "false"
	case OperandNull:
		return "null"
	case OperandType:
		return o.Type.String()
	case OperandOrdering:
		return o.Ordering
	case OperandVector:
		s := "("
		for i, v := range o.Vector {
			if i > 0 {
				s += ", "
			}
			s += strconv.FormatInt(v, 10)
		}
		return s + ")"
	}
	return fmt.Sprintf("<bad operand kind %d>", o.Kind)
}