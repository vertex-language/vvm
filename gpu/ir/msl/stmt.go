package msl

// Stmt is the interface implemented by all statement nodes.
type Stmt interface{ isStmt() }

// DeclStmt declares a local variable, optionally in an address space
// (threadgroup locals) and optionally initialized. Array types place
// their length at the declarator site.
type DeclStmt struct {
	Space   AddressSpace // "" for plain locals
	Type    Type
	Name    string
	Init    Expr // nil for no initializer
	Comment string
}

func (*DeclStmt) isStmt() {}

// AssignStmt is dst OP src; where Op is "=", "+=", "-=", etc.
type AssignStmt struct {
	Op      string
	Dst     Expr
	Src     Expr
	Comment string
}

func (*AssignStmt) isStmt() {}

// ExprStmt is an expression evaluated for effect.
type ExprStmt struct {
	X       Expr
	Comment string
}

func (*ExprStmt) isStmt() {}

// ReturnStmt is return; or return x;.
type ReturnStmt struct {
	X       Expr // nil for bare return
	Comment string
}

func (*ReturnStmt) isStmt() {}

// IfStmt is a structured conditional with optional else block.
type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt // nil for no else
}

func (*IfStmt) isStmt() {}

// ForStmt is for (init; cond; post) { body }. Init and Post print in
// inline (semicolon-free) form; either may be nil.
type ForStmt struct {
	Init Stmt
	Cond Expr
	Post Stmt
	Body []Stmt
}

func (*ForStmt) isStmt() {}

// WhileStmt is while (cond) { body }.
type WhileStmt struct {
	Cond Expr
	Body []Stmt
}

func (*WhileStmt) isStmt() {}

// BreakStmt is break;.
type BreakStmt struct{}

func (*BreakStmt) isStmt() {}

// ContinueStmt is continue;.
type ContinueStmt struct{}

func (*ContinueStmt) isStmt() {}

// IncStmt is ++x (used primarily as a for-loop post statement).
type IncStmt struct{ X Expr }

func (*IncStmt) isStmt() {}

// CommentStmt is a standalone // comment line.
type CommentStmt struct{ Text string }

func (*CommentStmt) isStmt() {}

// BlankStmt is an empty line.
type BlankStmt struct{}

func (*BlankStmt) isStmt() {}

// RawStmt is verbatim source text. It participates in scoping and
// indentation; multi-line text is indented line by line.
type RawStmt struct{ Text string }

func (*RawStmt) isStmt() {}

// Inc builds an ++x statement for use as a for-loop post statement.
func Inc(x any) Stmt { return &IncStmt{X: asExpr(x)} }

// AssignS builds a dst = src statement for use as a for-loop init or
// post statement.
func AssignS(dst, src any) Stmt {
	return &AssignStmt{Op: "=", Dst: asExpr(dst), Src: asExpr(src)}
}

// DeclS builds a typed declaration statement for use as a for-loop init.
func DeclS(t Type, name string, init any) Stmt {
	return &DeclStmt{Type: t, Name: name, Init: asExpr(init)}
}