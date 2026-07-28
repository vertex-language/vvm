package msl

// ExprNode is implemented by every expression node.
type ExprNode interface{ isExpr() }

// Expr wraps an ExprNode so operators can be written as methods. MSL
// expression trees nest deeply; postfix method chains read in evaluation
// order where nested constructors would read inside-out.
//
// The zero Expr means "absent" (a bare return, a missing for-condition).
type Expr struct{ Node ExprNode }

// IsZero reports whether the expression is absent.
func (e Expr) IsZero() bool { return e.Node == nil }

// BinOp is a binary operator, spelled as it prints.
type BinOp string

// Binary operators.
const (
	OpAdd    BinOp = "+"
	OpSub    BinOp = "-"
	OpMul    BinOp = "*"
	OpDiv    BinOp = "/"
	OpRem    BinOp = "%"
	OpEq     BinOp = "=="
	OpNe     BinOp = "!="
	OpLt     BinOp = "<"
	OpLe     BinOp = "<="
	OpGt     BinOp = ">"
	OpGe     BinOp = ">="
	OpAnd    BinOp = "&&"
	OpOr     BinOp = "||"
	OpBitAnd BinOp = "&"
	OpBitOr  BinOp = "|"
	OpBitXor BinOp = "^"
	OpShl    BinOp = "<<"
	OpShr    BinOp = ">>"
)

// UnOp is a prefix unary operator, spelled as it prints.
type UnOp string

// Unary operators.
const (
	OpNeg    UnOp = "-"
	OpNot    UnOp = "!"
	OpBitNot UnOp = "~"
	OpAddr   UnOp = "&"
	OpDeref  UnOp = "*"
)

// Expression nodes.
type (
	// NameExpr is an identifier.
	NameExpr string
	// IntExpr is a signed integer literal.
	IntExpr int64
	// UintExpr is an unsigned integer literal (prints with a u suffix).
	UintExpr uint64
	// FloatExpr is a floating-point literal.
	FloatExpr float64
	// BoolExpr is a boolean literal.
	BoolExpr bool
	// RawExpr is verbatim expression text.
	RawExpr string

	// BinaryExpr is L op R.
	BinaryExpr struct {
		Op   BinOp
		L, R Expr
	}
	// UnaryExpr is op X.
	UnaryExpr struct {
		Op UnOp
		X  Expr
	}
	// IndexExpr is X[Index].
	IndexExpr struct{ X, Index Expr }
	// MemberExpr is X.Name; also used for swizzles.
	MemberExpr struct {
		X    Expr
		Name string
	}
	// CallExpr is a free call, optionally templated: Fn<TArgs...>(Args...).
	CallExpr struct {
		Fn    string
		TArgs []TypeArg
		Args  []Expr
	}
	// MethodExpr is X.Name<TArgs...>(Args...).
	MethodExpr struct {
		X     Expr
		Name  string
		TArgs []TypeArg
		Args  []Expr
	}
	// CtorExpr is a functional-style constructor: float4(x, y, z, 1.0).
	CtorExpr struct {
		Type Type
		Args []Expr
	}
	// CastExpr is a functional-style cast: int(x).
	CastExpr struct {
		Type Type
		X    Expr
	}
	// CondExpr is the ternary Cond ? Then : Else.
	CondExpr struct{ Cond, Then, Else Expr }
	// ListExpr is a braced initializer list: {a, b, c}.
	ListExpr struct{ Elems []Expr }
)

func (NameExpr) isExpr()   {}
func (IntExpr) isExpr()    {}
func (UintExpr) isExpr()   {}
func (FloatExpr) isExpr()  {}
func (BoolExpr) isExpr()   {}
func (RawExpr) isExpr()    {}
func (*BinaryExpr) isExpr() {}
func (*UnaryExpr) isExpr()  {}
func (*IndexExpr) isExpr()  {}
func (*MemberExpr) isExpr() {}
func (*CallExpr) isExpr()   {}
func (*MethodExpr) isExpr() {}
func (*CtorExpr) isExpr()   {}
func (*CastExpr) isExpr()   {}
func (*CondExpr) isExpr()   {}
func (*ListExpr) isExpr()   {}

// Leaf constructors.
func Name(s string) Expr  { return Expr{NameExpr(s)} }
func I(v int64) Expr      { return Expr{IntExpr(v)} }
func U(v uint64) Expr     { return Expr{UintExpr(v)} }
func F(v float64) Expr    { return Expr{FloatExpr(v)} }
func B(v bool) Expr       { return Expr{BoolExpr(v)} }
func Raw(text string) Expr { return Expr{RawExpr(text)} }

// DynamicExtent is the Metal 4 tensor sentinel for a dimension the operator
// loops internally.
var DynamicExtent = Name("dynamic_extent")

// Call builds a free-function call. Anything in <metal_stdlib> is reachable.
func Call(fn string, args ...Expr) Expr {
	return Expr{&CallExpr{Fn: fn, Args: args}}
}

// TCall builds a templated free call: TCall("matmul2d", []TypeArg{TArg(desc)}).
func TCall(fn string, targs []TypeArg, args ...Expr) Expr {
	return Expr{&CallExpr{Fn: fn, TArgs: targs, Args: args}}
}

// Ctor builds a functional-style constructor.
func Ctor(t Type, args ...Expr) Expr { return Expr{&CtorExpr{Type: t, Args: args}} }

// Cast builds a functional-style cast.
func Cast(t Type, x Expr) Expr { return Expr{&CastExpr{Type: t, X: x}} }

// Cond builds the ternary c ? a : b.
func Cond(c, a, b Expr) Expr { return Expr{&CondExpr{Cond: c, Then: a, Else: b}} }

// Init builds a braced initializer list.
func Init(elems ...Expr) Expr { return Expr{&ListExpr{Elems: elems}} }

func (e Expr) bin(op BinOp, o Expr) Expr {
	return Expr{&BinaryExpr{Op: op, L: e, R: o}}
}

func (e Expr) un(op UnOp) Expr { return Expr{&UnaryExpr{Op: op, X: e}} }

// Arithmetic.
func (e Expr) Add(o Expr) Expr { return e.bin(OpAdd, o) }
func (e Expr) Sub(o Expr) Expr { return e.bin(OpSub, o) }
func (e Expr) Mul(o Expr) Expr { return e.bin(OpMul, o) }
func (e Expr) Div(o Expr) Expr { return e.bin(OpDiv, o) }
func (e Expr) Rem(o Expr) Expr { return e.bin(OpRem, o) }

// Comparison.
func (e Expr) Eq(o Expr) Expr { return e.bin(OpEq, o) }
func (e Expr) Ne(o Expr) Expr { return e.bin(OpNe, o) }
func (e Expr) Lt(o Expr) Expr { return e.bin(OpLt, o) }
func (e Expr) Le(o Expr) Expr { return e.bin(OpLe, o) }
func (e Expr) Gt(o Expr) Expr { return e.bin(OpGt, o) }
func (e Expr) Ge(o Expr) Expr { return e.bin(OpGe, o) }

// Logic and bits.
func (e Expr) And(o Expr) Expr    { return e.bin(OpAnd, o) }
func (e Expr) Or(o Expr) Expr     { return e.bin(OpOr, o) }
func (e Expr) BitAnd(o Expr) Expr { return e.bin(OpBitAnd, o) }
func (e Expr) BitOr(o Expr) Expr  { return e.bin(OpBitOr, o) }
func (e Expr) BitXor(o Expr) Expr { return e.bin(OpBitXor, o) }
func (e Expr) Shl(o Expr) Expr    { return e.bin(OpShl, o) }
func (e Expr) Shr(o Expr) Expr    { return e.bin(OpShr, o) }

func (e Expr) Neg() Expr    { return e.un(OpNeg) }
func (e Expr) Not() Expr    { return e.un(OpNot) }
func (e Expr) BitNot() Expr { return e.un(OpBitNot) }
func (e Expr) Addr() Expr   { return e.un(OpAddr) }
func (e Expr) Deref() Expr  { return e.un(OpDeref) }

// At builds e[i].
func (e Expr) At(i Expr) Expr { return Expr{&IndexExpr{X: e, Index: i}} }

// Sel builds e.name, for swizzles and stdlib members.
func (e Expr) Sel(name string) Expr { return Expr{&MemberExpr{X: e, Name: name}} }

// Fld builds e.name from a declared field, so member access cannot drift out
// of sync with the struct definition.
func (e Expr) Fld(f *Field) Expr { return Expr{&MemberExpr{X: e, Name: f.Name}} }

// Method builds e.name(args...).
func (e Expr) Method(name string, args ...Expr) Expr {
	return Expr{&MethodExpr{X: e, Name: name, Args: args}}
}

// TMethod builds a templated member call e.name<targs...>(args...).
func (e Expr) TMethod(name string, targs []TypeArg, args ...Expr) Expr {
	return Expr{&MethodExpr{X: e, Name: name, TArgs: targs, Args: args}}
}

// Precedence levels, ordered as in C++. Higher binds tighter. The printer
// parenthesizes only where the tree demands it.
const (
	PrecTernary = 3
	PrecOr      = 4
	PrecAnd     = 5
	PrecBitOr   = 6
	PrecBitXor  = 7
	PrecBitAnd  = 8
	PrecEquality = 9
	PrecRelational = 10
	PrecShift   = 11
	PrecAdd     = 12
	PrecMul     = 13
	PrecUnary   = 14
	PrecPostfix = 15
	PrecPrimary = 16
)

var binPrec = map[BinOp]int{
	OpMul: PrecMul, OpDiv: PrecMul, OpRem: PrecMul,
	OpAdd: PrecAdd, OpSub: PrecAdd,
	OpShl: PrecShift, OpShr: PrecShift,
	OpLt: PrecRelational, OpLe: PrecRelational,
	OpGt: PrecRelational, OpGe: PrecRelational,
	OpEq: PrecEquality, OpNe: PrecEquality,
	OpBitAnd: PrecBitAnd, OpBitXor: PrecBitXor, OpBitOr: PrecBitOr,
	OpAnd: PrecAnd, OpOr: PrecOr,
}

// Prec returns the precedence of an expression node. Encoders use it to
// decide parenthesization; RawExpr is treated as primary and never wrapped.
func Prec(n ExprNode) int {
	switch v := n.(type) {
	case *BinaryExpr:
		if p, ok := binPrec[v.Op]; ok {
			return p
		}
		return PrecAdd
	case *UnaryExpr:
		return PrecUnary
	case *CondExpr:
		return PrecTernary
	case *IndexExpr, *MemberExpr, *MethodExpr:
		return PrecPostfix
	default:
		return PrecPrimary
	}
}