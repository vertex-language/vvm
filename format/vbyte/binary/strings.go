// File: format/vbyte/binary/strings.go
package binary

import "fmt"

// stringTable is the encode-side StringTable builder (§3.2, §4). Every
// name referenced anywhere in the module must be interned here before any
// section bytes are emitted, since the StringTable must be fully written
// before the Header that references it.
type stringTable struct {
	list []string
	ids  map[string]int
}

func newStringTable() *stringTable {
	return &stringTable{ids: map[string]int{}}
}

// intern registers s if new, returning its (possibly pre-existing) index.
// Used only during the collection pass.
func (t *stringTable) intern(s string) int {
	if id, ok := t.ids[s]; ok {
		return id
	}
	id := len(t.list)
	t.list = append(t.list, s)
	t.ids[s] = id
	return id
}

// id looks up an already-interned string. Used during actual section
// emission; a miss indicates a bug in the collection pass rather than a
// legitimate new string (StringTable bytes are already fixed by then).
func (t *stringTable) id(s string) (int, error) {
	id, ok := t.ids[s]
	if !ok {
		return 0, fmt.Errorf("vbyte: internal error: string %q was not collected into the string table", s)
	}
	return id, nil
}

// decodeStringTable reads the StringTable section body: vec<bytes>, no
// duplicate entries permitted (§3.2).
func decodeStringTable(r *reader) ([]string, error) {
	n, err := r.uleb()
	if err != nil {
		return nil, err
	}
	list := make([]string, 0, n)
	seen := make(map[string]bool, n)
	for i := uint64(0); i < n; i++ {
		b, err := r.bytesVec()
		if err != nil {
			return nil, err
		}
		s := string(b)
		if seen[s] {
			return nil, fmt.Errorf("vbyte: duplicate StringTable entry %q (§3.2)", s)
		}
		seen[s] = true
		list = append(list, s)
	}
	return list, nil
}

func stringAt(strs []string, id int) (string, error) {
	if id < 0 || id >= len(strs) {
		return "", fmt.Errorf("vbyte: string index %d out of range (§1 strict rejection)", id)
	}
	return strs[id], nil
}