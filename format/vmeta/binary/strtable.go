// strtable.go
package binary

import "fmt"

// stringTable is the §F2.5 STRT builder: strings intern in first-reference
// order, index 0 is reserved for "absent", and no two entries may be
// byte-identical.
type stringTable struct {
	index map[string]uint32
	list  []string
}

func newStringTable() *stringTable { return &stringTable{index: map[string]uint32{}} }

// intern returns s's 1-based STRT index, assigning a fresh one on first
// reference. An empty string is never interned — callers use index 0
// ("absent") for it instead, since a `str` field can't otherwise
// distinguish an empty string from a missing one (§F2.1).
func (t *stringTable) intern(s string) uint32 {
	if s == "" {
		return 0
	}
	if i, ok := t.index[s]; ok {
		return i
	}
	t.list = append(t.list, s)
	i := uint32(len(t.list))
	t.index[s] = i
	return i
}

func (t *stringTable) encode() []byte {
	buf := putUleb(nil, uint64(len(t.list)))
	for _, s := range t.list {
		buf = putUleb(buf, uint64(len(s)))
		buf = append(buf, s...)
	}
	return buf
}

// decodeStringTable parses a full STRT payload into a 1-based lookup
// table: strs[0] is an unused placeholder for the "absent" index, real
// strings start at strs[1].
func decodeStringTable(payload []byte) ([]string, error) {
	count, n, err := readUleb(payload)
	if err != nil {
		return nil, err
	}
	out := make([]string, 1, count+1)
	seen := make(map[string]bool, count)
	for i := uint64(0); i < count; i++ {
		ln, m, err := readUleb(payload[n:])
		if err != nil {
			return nil, err
		}
		n += m
		if uint64(n)+ln > uint64(len(payload)) {
			return nil, fmt.Errorf("string entry runs past end of section")
		}
		s := string(payload[n : n+int(ln)])
		n += int(ln)
		if seen[s] {
			return nil, fmt.Errorf("duplicate string %q — STRT must be interned (§F2.5)", s)
		}
		seen[s] = true
		out = append(out, s)
	}
	if n != len(payload) {
		return nil, fmt.Errorf("trailing bytes after last string entry")
	}
	return out, nil
}

// strAt looks up a decoded 1-based STRT index; 0 means absent and returns "".
func strAt(strs []string, idx uint64) (string, error) {
	if idx == 0 {
		return "", nil
	}
	if idx >= uint64(len(strs)) {
		return "", fmt.Errorf("string index %d out of range", idx)
	}
	return strs[idx], nil
}