// operand.go
package gvir

import (
	"fmt"
	"strconv"
)

// OperandKind discriminates Operand payloads (§2 operand grammar):
//
//	operand := ident | literal | ordering | scope
//
// Narrower than vir's: no type operands (index.ptr is byte arithmetic and
// takes no element type), no vector literals (a shuffle mask is a run of
// int literals), and no qualified idents (the namespace is flat, and `__`
// is reserved for the host symbol ABI). Strings appear only in loc.
type OperandKind int

const (
	OperandIdent    OperandKind = iota // value / const / func / group / dynamic_group name
	OperandInt                         // integer literal
	OperandFloat                       // float literal (incl. NaN, Inf, -Inf)
	OperandBool                        // true / false
	OperandNull                        // null (ptr)
	OperandOrdering                    // relaxed | acquire | release | acqrel | seqcst
	OperandScope                       // subgroup | group | grid | none
	OperandString                      // loc only (§2)
)

// Ordering is a fence's memory ordering (§10.2). Atomics never carry one:
// they are relaxed in v1 and ordering is expressed separately.
type Ordering string

const (
	Relaxed Ordering = "relaxed"
	Acquire Ordering = "acquire"
	Release Ordering = "release"
	AcqRel  Ordering = "acqrel"
	SeqCst  Ordering = "seqcst"
)

// Scope is a memory or atomic scope (§10).
type Scope string

const (
	ScopeSubgroup Scope = "subgroup"
	ScopeGroup    Scope = "group"
	ScopeGrid     Scope = "grid"
	ScopeNone     Scope = "none" // barrier memory scope only
)

// ExecScope is a barrier's execution scope suffix (§10.1). Deliberately a
// separate type from Scope: grid and none are memory scopes and are never
// execution scopes.
type ExecScope string

const (
	ExecSubgroup ExecScope = "subgroup"
	ExecGroup    ExecScope = "group"
	ExecNone     ExecScope = "" // absent
)

// DefaultMemScope is the memory scope a barrier gets when the comma form
// is omitted: the execution scope itself (§10.1).
func DefaultMemScope(e ExecScope) Scope { return Scope(e) }

// Operand is one operand-grammar value.
type Operand struct {
	Kind     OperandKind
	Ident    string
	Int      int64
	Float    float64
	Bool     bool
	Str      string
	Ordering Ordering
	Scope    Scope
}

func Ident(name string) Operand      { return Operand{Kind: OperandIdent, Ident: name} }
func IntLiteral(v int64) Operand     { return Operand{Kind: OperandInt, Int: v} }
func FloatLiteral(v float64) Operand { return Operand{Kind: OperandFloat, Float: v} }
func BoolLiteral(v bool) Operand     { return Operand{Kind: OperandBool, Bool: v} }
func NullLiteral() Operand           { return Operand{Kind: OperandNull} }
func StringLiteral(s string) Operand { return Operand{Kind: OperandString, Str: s} }

func OrderingOperand(o Ordering) Operand {
	return Operand{Kind: OperandOrdering, Ordering: o}
}
func ScopeOperand(s Scope) Operand { return Operand{Kind: OperandScope, Scope: s} }

func (o Operand) String() string {
	switch o.Kind {
	case OperandIdent:
		return o.Ident
	case OperandInt:
		return strconv.FormatInt(o.Int, 10)
	case OperandFloat:
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