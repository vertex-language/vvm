// names.go
package verify

import "fmt"

// nameTable enforces §2.2's "strict flat namespace ... zero shadowing":
// every struct/fnsig/const/global/extern-fn/fn name lives in one map,
// checked at declaration time regardless of which section it came from.
// A qualified name (module.name) is never itself a flat-namespace entry,
// so this table only ever sees bare, unqualified names.
type nameTable struct {
	kind map[string]string // name -> kind of thing that first claimed it (for error messages)
}

func newNameTable() *nameTable {
	return &nameTable{kind: make(map[string]string)}
}

// declare registers name as belonging to kind, or errors if the flat
// namespace already holds an entry for it — from this or any other kind;
// collisions are illegal across kinds too, not just within one.
func (t *nameTable) declare(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s: name must not be empty", kind)
	}
	if prev, ok := t.kind[name]; ok {
		return fmt.Errorf("name %q declared twice (%s, then %s) — flat namespace forbids shadowing (§2.2)", name, prev, kind)
	}
	t.kind[name] = kind
	return nil
}