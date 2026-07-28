package msl

import "strconv"

// Block is an ordered, editable statement list. Block is the only container
// for statements, and the emit methods below are sugar over Append: every
// statement they build is an ordinary value you can also construct directly.
//
// Nested blocks are built with closures, so a block cannot be left unclosed
// and there is no depth to track.
type Block struct{ stmts []Stmt }

// NewBlock returns an empty block.
func NewBlock() *Block { return &Block{} }

func newBlock(fn func(*Block)) *Block {
	b := &Block{}
	if fn != nil {
		fn(b)
	}
	return b
}

// Stmts returns the statements in order. The slice aliases the block.
func (b *Block) Stmts() []Stmt { return b.stmts }

// Len returns the number of statements.
func (b *Block) Len() int { return len(b.stmts) }

// Append adds statements to the end of the block.
func (b *Block) Append(s ...Stmt) *Block {
	b.stmts = append(b.stmts, s...)
	return b
}

// InsertBefore inserts statements before index i.
func (b *Block) InsertBefore(i int, s ...Stmt) *Block {
	b.stmts = append(b.stmts[:i], append(append([]Stmt{}, s...), b.stmts[i:]...)...)
	return b
}

// Replace overwrites the statement at index i.
func (b *Block) Replace(i int, s Stmt) *Block {
	b.stmts[i] = s
	return b
}

// Remove deletes the statement at index i.
func (b *Block) Remove(i int) *Block {
	b.stmts = append(b.stmts[:i], b.stmts[i+1:]...)
	return b
}

func (b *Block) add(s Stmt) { b.stmts = append(b.stmts, s) }

// Let declares an initialized local and returns a reference to it.
func (b *Block) Let(t Type, name string, init Expr) Expr {
	b.add(&VarDecl{Type: t, Name: name, Init: init})
	return Name(name)
}

// Var declares an uninitialized local, optionally in an address space, and
// returns a reference to it: Var(Threadgroup, Array(Float, 256), "tile").
func (b *Block) Var(space AddressSpace, t Type, name string) Expr {
	b.add(&VarDecl{Space: space, Type: t, Name: name})
	return Name(name)
}

// Declare appends an explicitly built declaration and returns a reference.
func (b *Block) Declare(v *VarDecl) Expr {
	b.add(v)
	return Name(v.Name)
}

// Assign emits dst = src;.
func (b *Block) Assign(dst, src Expr) *Assign {
	s := &Assign{Op: Set, Dst: dst, Src: src}
	b.add(s)
	return s
}

// SetOp emits a compound assignment: SetOp(sum, SetAdd, x) is sum += x;.
func (b *Block) SetOp(dst Expr, op AssignOp, src Expr) *Assign {
	s := &Assign{Op: op, Dst: dst, Src: src}
	b.add(s)
	return s
}

// Do emits an expression statement.
func (b *Block) Do(x Expr) *ExprStmt {
	s := &ExprStmt{X: x}
	b.add(s)
	return s
}

// Return emits `return;`, or `return x;` when given a value.
func (b *Block) Return(x ...Expr) *Return {
	s := &Return{}
	if len(x) > 1 {
		panic("msl: Return takes at most one value")
	}
	if len(x) == 1 {
		s.X = x[0]
	}
	b.add(s)
	return s
}

// If emits a conditional; attach further branches with Else or ElseIf.
func (b *Block) If(cond Expr, then func(*Block)) *If {
	s := &If{Cond: cond, Then: newBlock(then)}
	b.add(s)
	return s
}

// For emits for (init; cond; post) { body }. Clauses may be nil or zero.
func (b *Block) For(init Stmt, cond Expr, post Stmt, body func(*Block)) *For {
	s := &For{Init: init, Cond: cond, Post: post, Body: newBlock(body)}
	b.add(s)
	return s
}

// Range emits a counted loop: for (uint name = lo; name < hi; ++name).
func (b *Block) Range(name string, lo, hi Expr, body func(*Block)) *For {
	v := Name(name)
	return b.For(
		&VarDecl{Type: UInt, Name: name, Init: lo},
		v.Lt(hi),
		&IncDec{Op: Inc, X: v},
		body,
	)
}

// While emits while (cond) { body }.
func (b *Block) While(cond Expr, body func(*Block)) *While {
	s := &While{Cond: cond, Body: newBlock(body)}
	b.add(s)
	return s
}

// DoWhile emits do { body } while (cond);.
func (b *Block) DoWhile(body func(*Block), cond Expr) *DoWhile {
	s := &DoWhile{Body: newBlock(body), Cond: cond}
	b.add(s)
	return s
}

// Switch emits a switch; attach arms with Case, Fallthrough, and Default.
func (b *Block) Switch(tag Expr) *Switch {
	s := &Switch{Tag: tag}
	b.add(s)
	return s
}

// Scope emits a bare nested block.
func (b *Block) Scope(body func(*Block)) *Scope {
	s := &Scope{Body: newBlock(body)}
	b.add(s)
	return s
}

// PPIf emits a preprocessor conditional around statements.
func (b *Block) PPIf(cond string, then func(*Block)) *PPIf {
	s := &PPIf{Cond: cond, Then: newBlock(then)}
	b.add(s)
	return s
}

// VersionAtLeast emits `#if __METAL_VERSION__ >= <v>` around statements.
func (b *Block) VersionAtLeast(v Version, then func(*Block)) *PPIf {
	return b.PPIf("__METAL_VERSION__ >= "+strconv.Itoa(v.Macro()), then)
}

// Break emits break;.
func (b *Block) Break() { b.add(&Break{}) }

// Continue emits continue;.
func (b *Block) Continue() { b.add(&Continue{}) }

// Comment emits a standalone // line.
func (b *Block) Comment(text string) { b.add(&Comment{Text: text}) }

// Blank emits an empty line.
func (b *Block) Blank() { b.add(&Blank{}) }

// Raw emits verbatim source text.
func (b *Block) Raw(text string) { b.add(&RawStmt{Text: text}) }