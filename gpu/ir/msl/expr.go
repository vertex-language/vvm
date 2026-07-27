package msl

import "fmt"

// Expr is the interface implemented by all expression nodes.
type Expr interface{ isExpr() }

// Ident is a named variable, parameter, or other identifier.
type Ident string

func (Ident) isExpr() {}

// IntLit is a signed integer literal.
type IntLit int64

func (IntLit) isExpr() {}

// UintLit is an unsigned integer literal (prints with a u suffix).
type UintLit uint64

func (UintLit) isExpr() {}

// FloatLit is a floating-point literal.
type FloatLit float64

func (FloatLit) isExpr() {}

// BoolLit is a boolean literal.
type BoolLit bool

func (BoolLit) isExpr() {}

// I builds a signed integer literal.
func I(v int64) IntLit { return IntLit(v) }

// U builds an unsigned integer literal.
func U(v uint64) UintLit { return UintLit(v) }

// F builds a floating-point literal.
func F(v float64) FloatLit { return FloatLit(v) }

// B builds a boolean literal.
func B(v bool) BoolLit { return BoolLit(v) }

// BinaryExpr is a binary operation.
type BinaryExpr struct {
	Op   string
	L, R Expr
}

func (*BinaryExpr) isExpr() {}

// UnaryExpr is a prefix unary operation.
type UnaryExpr struct {
	Op string
	X  Expr
}

func (*UnaryExpr) isExpr() {}

// IndexExpr is base[index].
type IndexExpr struct{ Base, Index Expr }

func (*IndexExpr) isExpr() {}

// MemberExpr is base.name (also used for swizzles).
type MemberExpr struct {
	Base Expr
	Name string
}

func (*MemberExpr) isExpr() {}

// Call is a free-function call such as dot(a, b).
type Call struct {
	Fn   string
	Args []Expr
}

func (*Call) isExpr() {}

// MethodCall is a member-function call such as tex.sample(smp, uv).
type MethodCall struct {
	Base Expr
	Name string
	Args []Expr
}

func (*MethodCall) isExpr() {}

// CtorExpr is a functional-style constructor: float4(x, y, z, 1.0).
type CtorExpr struct {
	Type Type
	Args []Expr
}

func (*CtorExpr) isExpr() {}

// CastExpr is a functional-style cast: int(x).
type CastExpr struct {
	Type Type
	X    Expr
}

func (*CastExpr) isExpr() {}

// asExpr coerces plain Go values into Expr nodes: int -> IntLit,
// float64 -> FloatLit, string -> Ident, bool -> BoolLit. This is the one
// place ergonomics won over strict typing.
func asExpr(v any) Expr {
	switch x := v.(type) {
	case nil:
		return nil
	case Expr:
		return x
	case int:
		return IntLit(x)
	case int32:
		return IntLit(x)
	case int64:
		return IntLit(x)
	case uint:
		return UintLit(x)
	case uint32:
		return UintLit(x)
	case uint64:
		return UintLit(x)
	case float32:
		return FloatLit(x)
	case float64:
		return FloatLit(x)
	case string:
		return Ident(x)
	case bool:
		return BoolLit(x)
	default:
		panic(fmt.Sprintf("msl: cannot coerce %T to Expr", v))
	}
}

func asExprs(vs []any) []Expr {
	out := make([]Expr, len(vs))
	for i, v := range vs {
		out[i] = asExpr(v)
	}
	return out
}

func binary(op string, l, r any) Expr {
	return &BinaryExpr{Op: op, L: asExpr(l), R: asExpr(r)}
}

// Arithmetic.
func Add(l, r any) Expr { return binary("+", l, r) }
func Sub(l, r any) Expr { return binary("-", l, r) }
func Mul(l, r any) Expr { return binary("*", l, r) }
func Div(l, r any) Expr { return binary("/", l, r) }
func Rem(l, r any) Expr { return binary("%", l, r) }

// Comparison.
func Eq(l, r any) Expr { return binary("==", l, r) }
func Ne(l, r any) Expr { return binary("!=", l, r) }
func Lt(l, r any) Expr { return binary("<", l, r) }
func Le(l, r any) Expr { return binary("<=", l, r) }
func Gt(l, r any) Expr { return binary(">", l, r) }
func Ge(l, r any) Expr { return binary(">=", l, r) }

// Logic and bit operations.
func And(l, r any) Expr    { return binary("&&", l, r) }
func Or(l, r any) Expr     { return binary("||", l, r) }
func Not(x any) Expr       { return &UnaryExpr{Op: "!", X: asExpr(x)} }
func BitAnd(l, r any) Expr { return binary("&", l, r) }
func BitOr(l, r any) Expr  { return binary("|", l, r) }
func BitXor(l, r any) Expr { return binary("^", l, r) }
func BitNot(x any) Expr    { return &UnaryExpr{Op: "~", X: asExpr(x)} }
func Shl(l, r any) Expr    { return binary("<<", l, r) }
func Shr(l, r any) Expr    { return binary(">>", l, r) }
func Neg(x any) Expr       { return &UnaryExpr{Op: "-", X: asExpr(x)} }

// Index builds ptr[i].
func Index(base, i any) Expr { return &IndexExpr{Base: asExpr(base), Index: asExpr(i)} }

// Member builds v.name.
func Member(base any, name string) Expr {
	return &MemberExpr{Base: asExpr(base), Name: name}
}

// Swizzle builds v.xyz (an alias for Member).
func Swizzle(base any, components string) Expr { return Member(base, components) }

// CallExpr builds a free-function call: CallExpr("dot", a, b) is dot(a, b).
// Anything in <metal_stdlib> is reachable this way.
func CallExpr(fn string, args ...any) Expr { return &Call{Fn: fn, Args: asExprs(args)} }

// Method builds a member-function call: Method(tex, "sample", smp, uv).
func Method(base any, name string, args ...any) Expr {
	return &MethodCall{Base: asExpr(base), Name: name, Args: asExprs(args)}
}

// Ctor builds a functional-style constructor: Ctor(Vec(Float,4), x, y, z, 1).
func Ctor(t Type, args ...any) Expr { return &CtorExpr{Type: t, Args: asExprs(args)} }

// Cast builds a functional-style cast: Cast(Int, x) is int(x).
func Cast(t Type, x any) Expr { return &CastExpr{Type: t, X: asExpr(x)} }

// AddrOf builds &x.
func AddrOf(x any) Expr { return &UnaryExpr{Op: "&", X: asExpr(x)} }

// Deref builds *p.
func Deref(p any) Expr { return &UnaryExpr{Op: "*", X: asExpr(p)} }