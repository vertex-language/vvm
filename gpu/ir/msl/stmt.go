package msl

// Stmt is implemented by every statement node.
type Stmt interface{ isStmt() }

// AssignOp is an assignment operator, spelled as it prints.
type AssignOp string

// Assignment operators.
const (
	Set       AssignOp = "="
	SetAdd    AssignOp = "+="
	SetSub    AssignOp = "-="
	SetMul    AssignOp = "*="
	SetDiv    AssignOp = "/="
	SetRem    AssignOp = "%="
	SetBitAnd AssignOp = "&="
	SetBitOr  AssignOp = "|="
	SetBitXor AssignOp = "^="
	SetShl    AssignOp = "<<="
	SetShr    AssignOp = ">>="
)

// IncOp is ++ or --.
type IncOp string

// Increment operators.
const (
	Inc IncOp = "++"
	Dec IncOp = "--"
)

// VarDecl declares a variable. It is both a Decl and a Stmt because MSL makes
// no distinction: a module-scope `constant float kPi = 3.14;` and a local
// `threadgroup float tile[256];` are the same production at different scopes.
// Function constants are VarDecls in the constant space carrying a
// [[function_constant(n)]] attribute.
type VarDecl struct {
	Space   AddressSpace // NoSpace for plain locals
	Type    Type
	Name    string
	Init    Expr // zero for no initializer
	Attrs   []Attr
	Comment string
}

func (*VarDecl) isStmt() {}
func (*VarDecl) isDecl() {}

// Assign is Dst Op Src.
type Assign struct {
	Op      AssignOp
	Dst     Expr
	Src     Expr
	Comment string
}

func (*Assign) isStmt() {}

// ExprStmt evaluates an expression for effect.
type ExprStmt struct {
	X       Expr
	Comment string
}

func (*ExprStmt) isStmt() {}

// Return is `return;` or `return x;`.
type Return struct {
	X       Expr // zero for a bare return
	Comment string
}

func (*Return) isStmt() {}

// IncDec is ++x or --x, used mostly as a for-loop post clause.
type IncDec struct {
	Op IncOp
	X  Expr
}

func (*IncDec) isStmt() {}

// If is a conditional. Els is nil when there is no else branch; an else-if
// chain is an Els block holding a single *If, which is exactly how it prints.
type If struct {
	Cond Expr
	Then *Block
	Els  *Block
}

func (*If) isStmt() {}

// Else attaches an else branch and returns the receiver for chaining.
func (s *If) Else(fn func(*Block)) *If {
	s.Els = newBlock(fn)
	return s
}

// ElseIf attaches an `else if` branch and returns the nested *If so further
// branches can be chained onto it.
func (s *If) ElseIf(cond Expr, fn func(*Block)) *If {
	inner := &If{Cond: cond, Then: newBlock(fn)}
	s.Els = &Block{stmts: []Stmt{inner}}
	return inner
}

// For is for (Init; Cond; Post) { Body }. Any clause may be nil or zero.
type For struct {
	Init Stmt
	Cond Expr
	Post Stmt
	Body *Block
}

func (*For) isStmt() {}

// While is while (Cond) { Body }.
type While struct {
	Cond Expr
	Body *Block
}

func (*While) isStmt() {}

// DoWhile is do { Body } while (Cond);.
type DoWhile struct {
	Body *Block
	Cond Expr
}

func (*DoWhile) isStmt() {}

// Case is one arm of a Switch. Vals holds the case labels; Fall omits the
// implicit trailing break.
type Case struct {
	Vals []Expr
	Body *Block
	Fall bool
}

// Switch is switch (Tag) { ... }. Def is nil when there is no default arm.
type Switch struct {
	Tag   Expr
	Cases []*Case
	Def   *Block
}

func (*Switch) isStmt() {}

// Case appends an arm and returns the Switch for chaining.
func (s *Switch) Case(vals []Expr, fn func(*Block)) *Switch {
	s.Cases = append(s.Cases, &Case{Vals: vals, Body: newBlock(fn)})
	return s
}

// Fallthrough appends an arm that omits the implicit break.
func (s *Switch) Fallthrough(vals []Expr, fn func(*Block)) *Switch {
	s.Cases = append(s.Cases, &Case{Vals: vals, Body: newBlock(fn), Fall: true})
	return s
}

// Default attaches the default arm and returns the Switch.
func (s *Switch) Default(fn func(*Block)) *Switch {
	s.Def = newBlock(fn)
	return s
}

// Break is break;.
type Break struct{}

func (*Break) isStmt() {}

// Continue is continue;.
type Continue struct{}

func (*Continue) isStmt() {}

// Scope is a bare nested block, used to bound the lifetime of locals.
type Scope struct{ Body *Block }

func (*Scope) isStmt() {}

// Comment is a standalone // line.
type Comment struct{ Text string }

func (*Comment) isStmt() {}

// Blank is an empty line.
type Blank struct{}

func (*Blank) isStmt() {}

// RawStmt is verbatim source text. It participates in scoping and indentation;
// multi-line text is indented line by line. This is the escape hatch for
// stdlib surface with no typed constructor.
type RawStmt struct{ Text string }

func (*RawStmt) isStmt() {}

// PPIf is a statement-level preprocessor conditional. Cond is the raw
// condition text, e.g. "__METAL_VERSION__ >= 400". Preprocessor lines print at
// column zero regardless of nesting depth.
type PPIf struct {
	Cond string
	Then *Block
	Els  *Block
}

func (*PPIf) isStmt() {}

// Else attaches an #else branch.
func (s *PPIf) Else(fn func(*Block)) *PPIf {
	s.Els = newBlock(fn)
	return s
}