package msl

import (
	"fmt"
	"strconv"
)

const (
	frameBody = iota
	frameIf
	frameFor
	frameWhile
)

type frame struct {
	kind      int
	stmts     []Stmt
	cond      Expr
	initStmt  Stmt
	postStmt  Stmt
	thenStmts []Stmt
	inElse    bool
}

// CodeBuilder is a statement-oriented, append-only body builder. Every
// emit method returns the receiver so calls chain. Block statements
// (If, For, While) open a nested scope and are closed with End.
type CodeBuilder struct {
	frames []*frame
	names  map[string]int
	tmp    int
}

// NewCodeBuilder returns an empty body builder.
func NewCodeBuilder() *CodeBuilder {
	return &CodeBuilder{
		frames: []*frame{{kind: frameBody}},
		names:  map[string]int{},
	}
}

func (cb *CodeBuilder) top() *frame { return cb.frames[len(cb.frames)-1] }

func (cb *CodeBuilder) emit(s Stmt) *CodeBuilder {
	f := cb.top()
	f.stmts = append(f.stmts, s)
	return cb
}

// reserve claims name in the builder's scope, uniquifying duplicates
// (sum, sum_1, sum_2, ...) so generated code never shadows accidentally.
func (cb *CodeBuilder) reserve(name string) string {
	if _, taken := cb.names[name]; !taken {
		cb.names[name] = 0
		return name
	}
	for i := 1; ; i++ {
		cand := name + "_" + strconv.Itoa(i)
		if _, taken := cb.names[cand]; !taken {
			cb.names[cand] = 0
			return cand
		}
	}
}

// Depth returns the number of unclosed blocks. A structurally complete
// body has depth 0; the printer reports an error otherwise.
func (cb *CodeBuilder) Depth() int { return len(cb.frames) - 1 }

// Body returns the completed top-level statement list.
func (cb *CodeBuilder) Body() []Stmt { return cb.frames[0].stmts }

// Let declares a named local: float sum = 0.0;. The name is uniquified
// against previously declared names and parameters. The returned Expr
// is the (possibly uniquified) identifier.
func (cb *CodeBuilder) Let(t Type, name string, init any) Expr {
	n := cb.reserve(name)
	cb.emit(&DeclStmt{Type: t, Name: n, Init: asExpr(init)})
	return Ident(n)
}

// LetUninit declares a named local without an initializer.
func (cb *CodeBuilder) LetUninit(t Type, name string) Expr {
	n := cb.reserve(name)
	cb.emit(&DeclStmt{Type: t, Name: n})
	return Ident(n)
}

// LetThreadgroup declares a threadgroup local:
// threadgroup float tile[256];.
func (cb *CodeBuilder) LetThreadgroup(t Type, name string) Expr {
	n := cb.reserve(name)
	cb.emit(&DeclStmt{Space: Threadgroup, Type: t, Name: n})
	return Ident(n)
}

// Temp declares a fresh deterministic local (_t1, _t2, ...) initialized
// with init and returns its identifier. Temp is a naming convenience,
// not an allocator.
func (cb *CodeBuilder) Temp(t Type, init any) Expr {
	cb.tmp++
	n := cb.reserve("_t" + strconv.Itoa(cb.tmp))
	cb.emit(&DeclStmt{Type: t, Name: n, Init: asExpr(init)})
	return Ident(n)
}

// Assign emits dst = src;.
func (cb *CodeBuilder) Assign(dst, src any) *CodeBuilder {
	return cb.emit(&AssignStmt{Op: "=", Dst: asExpr(dst), Src: asExpr(src)})
}

// Compound assignments.
func (cb *CodeBuilder) AddAssign(dst, src any) *CodeBuilder {
	return cb.emit(&AssignStmt{Op: "+=", Dst: asExpr(dst), Src: asExpr(src)})
}
func (cb *CodeBuilder) SubAssign(dst, src any) *CodeBuilder {
	return cb.emit(&AssignStmt{Op: "-=", Dst: asExpr(dst), Src: asExpr(src)})
}
func (cb *CodeBuilder) MulAssign(dst, src any) *CodeBuilder {
	return cb.emit(&AssignStmt{Op: "*=", Dst: asExpr(dst), Src: asExpr(src)})
}
func (cb *CodeBuilder) DivAssign(dst, src any) *CodeBuilder {
	return cb.emit(&AssignStmt{Op: "/=", Dst: asExpr(dst), Src: asExpr(src)})
}

// Expr emits an expression statement.
func (cb *CodeBuilder) Expr(x any) *CodeBuilder {
	return cb.emit(&ExprStmt{X: asExpr(x)})
}

// Return emits return; or return x;. Passing more than one value panics.
func (cb *CodeBuilder) Return(vals ...any) *CodeBuilder {
	switch len(vals) {
	case 0:
		return cb.emit(&ReturnStmt{})
	case 1:
		return cb.emit(&ReturnStmt{X: asExpr(vals[0])})
	default:
		panic("msl: Return takes at most one value")
	}
}

// If opens an if block; close with End (optionally via Else).
func (cb *CodeBuilder) If(cond any) *CodeBuilder {
	cb.frames = append(cb.frames, &frame{kind: frameIf, cond: asExpr(cond)})
	return cb
}

// Else switches the innermost open if block to its else branch.
func (cb *CodeBuilder) Else() *CodeBuilder {
	f := cb.top()
	if f.kind != frameIf || f.inElse {
		panic("msl: Else without matching If")
	}
	f.thenStmts = f.stmts
	f.stmts = nil
	f.inElse = true
	return cb
}

// For opens a for block: for (init; cond; post) { ... }. Init and post
// may be nil; build them with DeclS, AssignS, or Inc.
func (cb *CodeBuilder) For(init Stmt, cond any, post Stmt) *CodeBuilder {
	cb.frames = append(cb.frames, &frame{
		kind: frameFor, cond: asExpr(cond), initStmt: init, postStmt: post,
	})
	return cb
}

// ForRange is sugar for a counted loop: for (uint i = lo; i < hi; ++i).
// v may be a string or Ident naming the loop variable; the name is
// reserved in the builder's scope.
func (cb *CodeBuilder) ForRange(v, lo, hi any) *CodeBuilder {
	id := asExpr(v)
	name, ok := id.(Ident)
	if !ok {
		panic("msl: ForRange loop variable must be a name")
	}
	n := cb.reserve(string(name))
	return cb.For(
		&DeclStmt{Type: UInt, Name: n, Init: asExpr(lo)},
		Lt(Ident(n), hi),
		&IncStmt{X: Ident(n)},
	)
}

// While opens a while block; close with End.
func (cb *CodeBuilder) While(cond any) *CodeBuilder {
	cb.frames = append(cb.frames, &frame{kind: frameWhile, cond: asExpr(cond)})
	return cb
}

// End closes the innermost open block.
func (cb *CodeBuilder) End() *CodeBuilder {
	if len(cb.frames) == 1 {
		panic("msl: End without open block")
	}
	f := cb.top()
	cb.frames = cb.frames[:len(cb.frames)-1]
	var node Stmt
	switch f.kind {
	case frameIf:
		n := &IfStmt{Cond: f.cond}
		if f.inElse {
			n.Then, n.Else = f.thenStmts, f.stmts
		} else {
			n.Then = f.stmts
		}
		node = n
	case frameFor:
		node = &ForStmt{Init: f.initStmt, Cond: f.cond, Post: f.postStmt, Body: f.stmts}
	case frameWhile:
		node = &WhileStmt{Cond: f.cond, Body: f.stmts}
	default:
		panic("msl: internal: bad frame kind")
	}
	return cb.emit(node)
}

// Break emits break;.
func (cb *CodeBuilder) Break() *CodeBuilder { return cb.emit(&BreakStmt{}) }

// Continue emits continue;.
func (cb *CodeBuilder) Continue() *CodeBuilder { return cb.emit(&ContinueStmt{}) }

// Comment emits a standalone // comment line.
func (cb *CodeBuilder) Comment(text string) *CodeBuilder {
	return cb.emit(&CommentStmt{Text: text})
}

// Blank emits an empty line.
func (cb *CodeBuilder) Blank() *CodeBuilder { return cb.emit(&BlankStmt{}) }

// Trailing attaches a trailing // comment to the most recently emitted
// simple statement (declaration, assignment, expression, or return).
// Printed only when the printer has comments enabled.
func (cb *CodeBuilder) Trailing(text string) *CodeBuilder {
	f := cb.top()
	if len(f.stmts) == 0 {
		return cb
	}
	switch s := f.stmts[len(f.stmts)-1].(type) {
	case *DeclStmt:
		s.Comment = text
	case *AssignStmt:
		s.Comment = text
	case *ExprStmt:
		s.Comment = text
	case *ReturnStmt:
		s.Comment = text
	}
	return cb
}

// Raw emits verbatim source text as a statement. It participates in
// scoping and indentation. This is the escape hatch for stdlib surface
// without typed constructors (tensor_ops, ray tracing, ...).
func (cb *CodeBuilder) Raw(text string) *CodeBuilder {
	return cb.emit(&RawStmt{Text: text})
}

// Rawf is Raw with fmt.Sprintf formatting.
func (cb *CodeBuilder) Rawf(format string, args ...any) *CodeBuilder {
	return cb.Raw(fmt.Sprintf(format, args...))
}