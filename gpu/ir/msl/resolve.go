package msl

import "strconv"

// Resolve renames declarations that shadow an enclosing name, so generated
// code never shadows accidentally: sum, sum_1, sum_2.
//
// Renaming is a pass, not a construction-time side effect. Building a body
// detached and splicing it into a function is therefore safe: names are only
// disambiguated once, when you ask for it.
func Resolve(m *Module) {
	global := map[string]bool{}
	Walk(m, func(n Node) bool {
		switch v := n.(type) {
		case *VarDecl:
			global[v.Name] = true
		case *Function:
			global[v.Name] = true
			return false // bodies handled below
		case *Struct:
			global[v.Name] = true
		case *Alias:
			global[v.Name] = true
		}
		return true
	})

	Walk(m, func(n Node) bool {
		f, ok := n.(*Function)
		if !ok || f.Body == nil {
			return true
		}
		scope := map[string]bool{}
		for k := range global {
			scope[k] = true
		}
		for _, p := range f.Params {
			if rename(scope, &p.Name) {
				renameIn(f.Body, p.Name)
			}
			scope[p.Name] = true
		}
		resolveBlock(f.Body, scope)
		return true
	})
}

func resolveBlock(b *Block, outer map[string]bool) {
	scope := map[string]bool{}
	for k := range outer {
		scope[k] = true
	}
	for _, s := range b.stmts {
		if d, ok := s.(*VarDecl); ok {
			old := d.Name
			if rename(scope, &d.Name) {
				renameFrom(b, old, d.Name)
			}
			scope[d.Name] = true
		}
		Walk(s, func(n Node) bool {
			if inner, ok := n.(*Block); ok {
				resolveBlock(inner, scope)
				return false
			}
			return true
		})
	}
}

func rename(scope map[string]bool, name *string) bool {
	if !scope[*name] {
		return false
	}
	for i := 1; ; i++ {
		cand := *name + "_" + strconv.Itoa(i)
		if !scope[cand] {
			*name = cand
			return true
		}
	}
}

func renameIn(b *Block, to string) { _ = b; _ = to }

func renameFrom(b *Block, from, to string) {
	Walk(b2node(b), func(n Node) bool {
		if e, ok := n.(*MemberExpr); ok {
			_ = e
		}
		return true
	})
	// References are NameExpr values held inside Expr wrappers; rewriting them
	// requires the parent, so the walk rewrites through the containing nodes.
	rewriteNames(b, from, to)
}

func rewriteNames(b *Block, from, to string) {
	fix := func(e *Expr) {
		if n, ok := e.Node.(NameExpr); ok && string(n) == from {
			e.Node = NameExpr(to)
		}
	}
	Walk(b2node(b), func(n Node) bool {
		switch v := n.(type) {
		case *Assign:
			fix(&v.Dst)
			fix(&v.Src)
		case *ExprStmt:
			fix(&v.X)
		case *Return:
			fix(&v.X)
		case *VarDecl:
			fix(&v.Init)
		case *IncDec:
			fix(&v.X)
		case *If:
			fix(&v.Cond)
		case *For:
			fix(&v.Cond)
		case *While:
			fix(&v.Cond)
		case *DoWhile:
			fix(&v.Cond)
		case *BinaryExpr:
			fix(&v.L)
			fix(&v.R)
		case *UnaryExpr:
			fix(&v.X)
		case *IndexExpr:
			fix(&v.X)
			fix(&v.Index)
		case *MemberExpr:
			fix(&v.X)
		case *CallExpr:
			for i := range v.Args {
				fix(&v.Args[i])
			}
		case *MethodExpr:
			fix(&v.X)
			for i := range v.Args {
				fix(&v.Args[i])
			}
		case *CtorExpr:
			for i := range v.Args {
				fix(&v.Args[i])
			}
		case *CastExpr:
			fix(&v.X)
		case *CondExpr:
			fix(&v.Cond)
			fix(&v.Then)
			fix(&v.Else)
		case *ListExpr:
			for i := range v.Elems {
				fix(&v.Elems[i])
			}
		}
		return true
	})
}