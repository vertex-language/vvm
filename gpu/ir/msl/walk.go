package msl

// Node is any IR node: a *Module, Decl, Stmt, Expr node, or Type.
type Node any

// Walk visits n and every node beneath it in source order. Returning false
// from fn skips the subtree. Nodes are visited as pointers where they are
// pointers, so passes can rewrite in place.
func Walk(n Node, fn func(Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	switch v := n.(type) {
	case *Module:
		walkDecls(v.Decls, fn)

	// Declarations.
	case *Include, *Using, *CommentDecl, *RawDecl:
	case *Alias:
		walkType(v.Type, fn)
	case *PPIfDecl:
		walkDecls(v.Then, fn)
		walkDecls(v.Els, fn)
	case *Struct:
		for _, f := range v.Fields {
			walkType(f.Type, fn)
		}
		for _, mth := range v.Methods {
			Walk(mth, fn)
		}
	case *Function:
		walkType(v.Ret, fn)
		for _, p := range v.Params {
			walkType(p.Type, fn)
		}
		walkBlock(v.Body, fn)

	// Statements.
	case *VarDecl:
		walkType(v.Type, fn)
		walkExpr(v.Init, fn)
	case *Assign:
		walkExpr(v.Dst, fn)
		walkExpr(v.Src, fn)
	case *ExprStmt:
		walkExpr(v.X, fn)
	case *Return:
		walkExpr(v.X, fn)
	case *IncDec:
		walkExpr(v.X, fn)
	case *If:
		walkExpr(v.Cond, fn)
		walkBlock(v.Then, fn)
		walkBlock(v.Els, fn)
	case *For:
		if v.Init != nil {
			Walk(v.Init, fn)
		}
		walkExpr(v.Cond, fn)
		if v.Post != nil {
			Walk(v.Post, fn)
		}
		walkBlock(v.Body, fn)
	case *While:
		walkExpr(v.Cond, fn)
		walkBlock(v.Body, fn)
	case *DoWhile:
		walkBlock(v.Body, fn)
		walkExpr(v.Cond, fn)
	case *Switch:
		walkExpr(v.Tag, fn)
		for _, c := range v.Cases {
			for _, e := range c.Vals {
				walkExpr(e, fn)
			}
			walkBlock(c.Body, fn)
		}
		walkBlock(v.Def, fn)
	case *Scope:
		walkBlock(v.Body, fn)
	case *PPIf:
		walkBlock(v.Then, fn)
		walkBlock(v.Els, fn)
	case *Break, *Continue, *Comment, *Blank, *RawStmt:

	// Expressions.
	case *BinaryExpr:
		walkExpr(v.L, fn)
		walkExpr(v.R, fn)
	case *UnaryExpr:
		walkExpr(v.X, fn)
	case *IndexExpr:
		walkExpr(v.X, fn)
		walkExpr(v.Index, fn)
	case *MemberExpr:
		walkExpr(v.X, fn)
	case *CallExpr:
		walkTArgs(v.TArgs, fn)
		walkExprs(v.Args, fn)
	case *MethodExpr:
		walkExpr(v.X, fn)
		walkTArgs(v.TArgs, fn)
		walkExprs(v.Args, fn)
	case *CtorExpr:
		walkType(v.Type, fn)
		walkExprs(v.Args, fn)
	case *CastExpr:
		walkType(v.Type, fn)
		walkExpr(v.X, fn)
	case *CondExpr:
		walkExpr(v.Cond, fn)
		walkExpr(v.Then, fn)
		walkExpr(v.Else, fn)
	case *ListExpr:
		walkExprs(v.Elems, fn)

	// Types.
	case VecType:
		walkType(v.Elem, fn)
	case MatType:
		walkType(v.Elem, fn)
	case PackedType:
		walkType(v.Elem, fn)
	case AtomicType:
		walkType(v.Elem, fn)
	case ArrayType:
		walkType(v.Elem, fn)
	case PtrType:
		walkType(v.Elem, fn)
	case RefType:
		walkType(v.Elem, fn)
	case ConstType:
		walkType(v.Elem, fn)
	case TextureType:
		walkType(v.Elem, fn)
	case TensorType:
		walkType(v.Elem, fn)
	case CoopTensorType:
		walkType(v.Elem, fn)
	case TemplateType:
		walkTArgs(v.Args, fn)
	}
}

func walkDecls(ds []Decl, fn func(Node) bool) {
	for _, d := range ds {
		Walk(d, fn)
	}
}

func walkBlock(b *Block, fn func(Node) bool) {
	if b == nil {
		return
	}
	for _, s := range b.stmts {
		Walk(s, fn)
	}
}

func walkExpr(e Expr, fn func(Node) bool) {
	if e.Node != nil {
		Walk(e.Node, fn)
	}
}

func walkExprs(es []Expr, fn func(Node) bool) {
	for _, e := range es {
		walkExpr(e, fn)
	}
}

func walkType(t Type, fn func(Node) bool) {
	if t != nil {
		Walk(t, fn)
	}
}

func walkTArgs(as []TypeArg, fn func(Node) bool) {
	for _, a := range as {
		walkType(a.Type, fn)
		walkExpr(a.Val, fn)
	}
}