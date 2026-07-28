// operand.go
package gvir

import (
	"fmt"
	"strconv"
)

// ---------------------------------------------------------------------------
// Closed vocabularies (§2). Small enough to be string types: they appear in
// the text verbatim and there is no arithmetic to do on them.
// ---------------------------------------------------------------------------

// Ordering is a fence ordering (§10.2). Atomics are always relaxed and carry
// no ordering operand, so this appears on `fence` alone.
type Ordering string

const (
	OrderRelaxed Ordering = "relaxed"
	OrderAcquire Ordering = "acquire"
	OrderRelease Ordering = "release"
	OrderAcqRel  Ordering = "acqrel"
	OrderSeqCst  Ordering = "seqcst"
)

var CanonicalOrderings = map[Ordering]bool{
	OrderRelaxed: true, OrderAcquire: true, OrderRelease: true,
	OrderAcqRel: true, OrderSeqCst: true,
}

// Scope is a memory/atomic scope (§10.1, §10.2). ScopeNone is legal only as
// a barrier's memory scope.
type Scope string

const (
	ScopeSubgroup Scope = "subgroup"
	ScopeGroup    Scope = "group"
	ScopeGrid     Scope = "grid"
	ScopeNone     Scope = "none"
)

var CanonicalScopes = map[Scope]bool{
	ScopeSubgroup: true, ScopeGroup: true, ScopeGrid: true, ScopeNone: true,
}

// ExecScope is a barrier's execution scope (§10.1) — the suffix, not an
// operand, and never `grid` or `none`.
type ExecScope string

const (
	ExecNone     ExecScope = "" // unset
	ExecSubgroup ExecScope = "subgroup"
	ExecGroup    ExecScope = "group"
)

// Dim is a builtin's optional dimension suffix (§9).
type Dim string

const (
	DimNone Dim = ""
	DimX    Dim = "x"
	DimY    Dim = "y"
	DimZ    Dim = "z"
)

// ---------------------------------------------------------------------------
// Operands (§2 operand grammar)
// ---------------------------------------------------------------------------

// OperandKind discriminates Operand payloads. There are no qualified idents:
// the module namespace is flat and there is no cross-module reference form.
type OperandKind int

const (
	OperandIdent    OperandKind = iota // value / const / func / group / dynamic_group name
	OperandInt                         // integer literal
	OperandFloat                       // float literal (incl. NaN, Inf, -Inf)
	OperandBool                        // true / false
	OperandNull                        // null (ptr)
	OperandOrdering                    // fence ordering
	OperandScope                       // memory / atomic scope
	OperandString                      // string literal — `loc` only (§2)
)

// Operand is one operand-grammar value.
//
// Hex marks a float literal that must be emitted in the hex-float spelling.
// §2 makes hex-float exact by construction and the portable way to pin a bit
// pattern, so the choice is part of the source text's meaning and is
// preserved here rather than re-derived by a printer.
type Operand struct {
	Kind     OperandKind
	Ident    string
	Int      int64
	Float    float64
	Hex      bool
	Bool     bool
	Str      string
	Ordering Ordering
	Scope    Scope
}

// Constructors — the builder-facing spelling of each operand form.
func Ident(name string) Operand      { return Operand{Kind: OperandIdent, Ident: name} }
func IntLiteral(v int64) Operand     { return Operand{Kind: OperandInt, Int: v} }
func FloatLiteral(v float64) Operand { return Operand{Kind: OperandFloat, Float: v} }

// HexFloatLiteral is the exact spelling: the emitted text round-trips to v
// bit for bit on every conforming frontend (§2, §13 "Literals").
func HexFloatLiteral(v float64) Operand {
	return Operand{Kind: OperandFloat, Float: v, Hex: true}
}

func BoolLiteral(v bool) Operand         { return Operand{Kind: OperandBool, Bool: v} }
func NullLiteral() Operand               { return Operand{Kind: OperandNull} }
func StringLiteral(s string) Operand     { return Operand{Kind: OperandString, Str: s} }
func OrderingOperand(o Ordering) Operand { return Operand{Kind: OperandOrdering, Ordering: o} }
func ScopeOperand(s Scope) Operand       { return Operand{Kind: OperandScope, Scope: s} }

func (o Operand) String() string {
	switch o.Kind {
	case OperandIdent:
		return o.Ident
	case OperandInt:
		return strconv.FormatInt(o.Int, 10)
	case OperandFloat:
		if o.Hex {
			return formatHexFloat(o.Float)
		}
		return formatFloat(o.Float)
	case OperandBool:
		if o.Bool {
			return "true"
		}
		return "false"
	case OperandNull:
		return "null"
	case OperandOrdering:
		return string(o.Ordering)
	case OperandScope:
		return string(o.Scope)
	case OperandString:
		return strconv.Quote(o.Str)
	}
	return fmt.Sprintf("<bad operand kind %d>", o.Kind)
}