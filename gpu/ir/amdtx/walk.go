package amdtx

// Walk calls fn for every instruction in every body in the module, in
// source order, descending into structured regions. Returning false from fn
// stops the traversal.
func Walk(m *Module, fn func(*Instr) bool) {
	for _, b := range Bodies(m) {
		if !walkItems(b.items, fn) {
			return
		}
	}
}

// WalkBody calls fn for every instruction in b, descending into regions.
func WalkBody(b *Body, fn func(*Instr) bool) { walkItems(b.items, fn) }

func walkItems(items []Item, fn func(*Instr) bool) bool {
	for _, it := range items {
		switch x := it.(type) {
		case *Instr:
			if !fn(x) {
				return false
			}
		case *IfStmt:
			if !walkItems(x.Then.items, fn) {
				return false
			}
			if x.Else != nil && !walkItems(x.Else.items, fn) {
				return false
			}
		case *LoopStmt:
			if !walkItems(x.Body.items, fn) {
				return false
			}
		}
	}
	return true
}

// WalkBlocks calls fn for every body and nested block, outermost first.
func WalkBlocks(b *Body, fn func(*Body)) {
	fn(b)
	for _, it := range b.items {
		switch x := it.(type) {
		case *IfStmt:
			WalkBlocks(x.Then, fn)
			if x.Else != nil {
				WalkBlocks(x.Else, fn)
			}
		case *LoopStmt:
			WalkBlocks(x.Body, fn)
		}
	}
}

// Bodies returns every non-nil top-level body in the module, in source
// order.
func Bodies(m *Module) []*Body {
	var out []*Body
	for _, d := range m.decls {
		switch x := d.(type) {
		case *Kernel:
			if x.Body != nil {
				out = append(out, x.Body)
			}
		case *Func:
			if x.Body != nil {
				out = append(out, x.Body)
			}
		}
	}
	return out
}