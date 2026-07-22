// typetable.go
package binary

import (
	"fmt"

	vir "github.com/vertex-language/vvm/ir/vir"
)

// Type table kinds present in .vmeta (§F2.6). STRUCT_N (0x08, nominal) is
// .vbyte-only; .vmeta only ever writes STRUCT_S (0x09, structural) — see
// the package doc (format.go), limitation 1.
const (
	kindVoid    = 0x01
	kindInt     = 0x02
	kindFloat   = 0x03
	kindPtr     = 0x04
	kindValist  = 0x05
	kindVec     = 0x06
	kindArray   = 0x07
	kindStructS = 0x09
)

// structResolver looks up the field shape of a struct by its in-module
// name, for expanding a nominal vir.StructType into a structural STRUCT_S
// entry (§F5.3). Only structs vir.ExtractShape tagged `export` have a
// shape available here.
type structResolver func(name string) (vir.StructShape, bool)

func localStructResolver(shape *vir.ModuleShape) structResolver {
	return func(name string) (vir.StructShape, bool) {
		for _, s := range shape.Structs {
			if s.Name == name {
				return s, true
			}
		}
		return vir.StructShape{}, false
	}
}

func isSpecIntWidth(bits int) bool {
	switch bits {
	case 1, 8, 16, 32, 64, 128:
		return true
	}
	return false
}

// typeTable is the §F2.6 TYPE builder: hash-consed, acyclic, each entry
// referencing only earlier indices.
type typeTable struct {
	key2idx map[string]uint32
	entries [][]byte // each: kind byte + payload, ready to concatenate
}

func newTypeTable() *typeTable { return &typeTable{key2idx: map[string]uint32{}} }

func (tt *typeTable) internRaw(kind byte, payload []byte) uint32 {
	key := string(append([]byte{kind}, payload...))
	if idx, ok := tt.key2idx[key]; ok {
		return idx
	}
	entry := append([]byte{kind}, payload...)
	tt.entries = append(tt.entries, entry)
	idx := uint32(len(tt.entries))
	tt.key2idx[key] = idx
	return idx
}

// intern returns t's TYPE index, recursively interning element/field
// types first so every reference points strictly backwards (§F2.6). nil
// (no suffix) returns index 0, "absent".
func (tt *typeTable) intern(t vir.Type, resolve structResolver) (uint32, error) {
	if t == nil {
		return 0, nil
	}
	switch x := t.(type) {
	case vir.VoidType:
		return tt.internRaw(kindVoid, nil), nil
	case vir.IntType:
		if !isSpecIntWidth(x.Bits) {
			return 0, fmt.Errorf("int width i%d is outside the §3 set {1,8,16,32,64,128}", x.Bits)
		}
		return tt.internRaw(kindInt, putUleb(nil, uint64(x.Bits))), nil
	case vir.FloatType:
		if x.Bits != 16 && x.Bits != 32 && x.Bits != 64 {
			return 0, fmt.Errorf("float width f%d is not one of 16/32/64", x.Bits)
		}
		return tt.internRaw(kindFloat, []byte{byte(x.Bits)}), nil
	case vir.PtrType:
		return tt.internRaw(kindPtr, nil), nil
	case vir.ValistType:
		return tt.internRaw(kindValist, nil), nil
	case vir.VecType:
		if x.Len <= 0 {
			return 0, fmt.Errorf("vec length must be positive")
		}
		elemIdx, err := tt.intern(x.Elem, resolve)
		if err != nil {
			return 0, err
		}
		payload := putUleb(nil, uint64(elemIdx))
		payload = putUleb(payload, uint64(x.Len))
		return tt.internRaw(kindVec, payload), nil
	case vir.ArrayType:
		if x.Len <= 0 {
			return 0, fmt.Errorf("array length must be positive")
		}
		elemIdx, err := tt.intern(x.Elem, resolve)
		if err != nil {
			return 0, err
		}
		payload := putUleb(nil, uint64(elemIdx))
		payload = putUleb(payload, uint64(x.Len))
		return tt.internRaw(kindArray, payload), nil
	case vir.StructType:
		ss, ok := resolve(x.Name)
		if !ok {
			return 0, fmt.Errorf("struct %q: no export-tagged shape available to expand structurally (§F5.3) — a field/param referencing a non-exported struct can't be represented in .vmeta", x.Name)
		}
		fieldIdxs := make([]uint32, len(ss.Fields))
		for i, f := range ss.Fields {
			idx, err := tt.intern(f.Type, resolve)
			if err != nil {
				return 0, err
			}
			fieldIdxs[i] = idx
		}
		payload := putUleb(nil, uint64(len(fieldIdxs)))
		for _, idx := range fieldIdxs {
			payload = putUleb(payload, uint64(idx))
		}
		return tt.internRaw(kindStructS, payload), nil
	default:
		return 0, fmt.Errorf("vmeta: cannot encode type %T", t)
	}
}

func (tt *typeTable) encode() []byte {
	buf := putUleb(nil, uint64(len(tt.entries)))
	for _, e := range tt.entries {
		buf = append(buf, e...)
	}
	return buf
}

// rawTypeEntry is one parsed-but-not-yet-materialized TYPE table entry.
// STRUCT_S entries stay as field-index lists until a name is known
// (materializeTypes), since a bare TYPE table has none (§F5.3).
type rawTypeEntry struct {
	kind    byte
	bits    int
	elemIdx uint32
	length  int
	fields  []uint32
}

func decodeTypeTable(payload []byte) ([]rawTypeEntry, error) {
	count, n, err := readUleb(payload)
	if err != nil {
		return nil, err
	}
	out := make([]rawTypeEntry, 1, count+1) // out[0] unused ("absent")
	for i := uint64(0); i < count; i++ {
		if n >= len(payload) {
			return nil, fmt.Errorf("type entry runs past end of section")
		}
		kind := payload[n]
		n++
		e := rawTypeEntry{kind: kind}
		switch kind {
		case kindVoid, kindPtr, kindValist:
			// no payload
		case kindInt:
			bits, m, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m
			if !isSpecIntWidth(int(bits)) {
				return nil, fmt.Errorf("int width i%d outside §3 set", bits)
			}
			e.bits = int(bits)
		case kindFloat:
			if n >= len(payload) {
				return nil, fmt.Errorf("truncated FLOAT entry")
			}
			bits := payload[n]
			n++
			if bits != 16 && bits != 32 && bits != 64 {
				return nil, fmt.Errorf("float width f%d not one of 16/32/64", bits)
			}
			e.bits = int(bits)
		case kindVec, kindArray:
			elemIdx, m, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m
			if elemIdx >= uint64(i+1) {
				return nil, fmt.Errorf("type entry %d: forward reference", i+1)
			}
			ln, m2, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m2
			if ln == 0 || ln > 1<<24 {
				return nil, fmt.Errorf("implausible vec/array length %d", ln)
			}
			e.elemIdx = uint32(elemIdx)
			e.length = int(ln)
		case kindStructS:
			fcount, m, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m
			fields := make([]uint32, fcount)
			for f := uint64(0); f < fcount; f++ {
				idx, m2, err := readUleb(payload[n:])
				if err != nil {
					return nil, err
				}
				n += m2
				if idx >= uint64(i+1) {
					return nil, fmt.Errorf("type entry %d: forward reference", i+1)
				}
				fields[f] = uint32(idx)
			}
			e.fields = fields
		default:
			return nil, fmt.Errorf("unrecognized TYPE kind 0x%02x", kind)
		}
		out = append(out, e)
	}
	if n != len(payload) {
		return nil, fmt.Errorf("trailing bytes after last type entry")
	}
	return out, nil
}

// materializeTypes resolves raw TYPE entries into concrete vir.Type
// values, in index order (always dependency-first, since every TYPE
// reference points strictly backwards, §F2.6). structName supplies the
// name to use for the STRUCT_S entry at a given 1-based index (package
// doc, limitation 1).
func materializeTypes(entries []rawTypeEntry, structName func(idx uint32) string) ([]vir.Type, error) {
	out := make([]vir.Type, len(entries))
	for i := 1; i < len(entries); i++ {
		e := entries[i]
		switch e.kind {
		case kindVoid:
			out[i] = vir.Void
		case kindInt:
			out[i] = vir.IntType{Bits: e.bits}
		case kindFloat:
			out[i] = vir.FloatType{Bits: e.bits}
		case kindPtr:
			out[i] = vir.Ptr
		case kindValist:
			out[i] = vir.Valist
		case kindVec:
			out[i] = vir.VecType{Elem: out[e.elemIdx], Len: e.length}
		case kindArray:
			out[i] = vir.ArrayType{Elem: out[e.elemIdx], Len: e.length}
		case kindStructS:
			out[i] = vir.StructType{Name: structName(uint32(i))}
		default:
			return nil, fmt.Errorf("type %d: unrecognized kind 0x%02x", i, e.kind)
		}
	}
	return out, nil
}