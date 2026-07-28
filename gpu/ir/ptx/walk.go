package ptx

// Walk calls fn for every instruction in every body in the module, in source
// order, descending into nested blocks. Returning false from fn stops the
// traversal.
func Walk(m *Module, fn func(*Instr) bool) {
	for _, d := range m.decls {
		var body *Body
		switch x := d.(type) {
		case *Kernel:
			body = x.Body
		case *Func:
			body = x.Body
		default:
			continue
		}
		if body == nil {
			continue
		}
		if !walkItems(body.items, fn) {
			return
		}
	}
}

func walkItems(items []Item, fn func(*Instr) bool) bool {
	for _, it := range items {
		switch x := it.(type) {
		case *Instr:
			if !fn(x) {
				return false
			}
		case *Block:
			if !walkItems(x.Items, fn) {
				return false
			}
		}
	}
	return true
}

// WalkBody calls fn for every instruction in b, descending into blocks.
func WalkBody(b *Body, fn func(*Instr) bool) { walkItems(b.items, fn) }

// Bodies returns every non-nil body in the module, in source order.
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