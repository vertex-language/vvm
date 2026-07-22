// File: format/vbyte/binary/types.go
package binary

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Type tags (§6).
const (
	typeTagInt    = 0x01
	typeTagF16    = 0x02
	typeTagF32    = 0x03
	typeTagF64    = 0x04
	typeTagPtr    = 0x05
	typeTagVoid   = 0x06
	typeTagValist = 0x07
	typeTagVec    = 0x08
	typeTagStruct = 0x09
	typeTagArray  = 0x0A
)

func encodeType(w *writer, t vir.Type, ec *encodeContext) error {
	switch v := t.(type) {
	case vir.IntType:
		if v.Bits < 1 {
			return fmt.Errorf("vbyte: i0 is rejected (§6)")
		}
		w.u8(typeTagInt)
		w.uleb(uint64(v.Bits))
	case vir.FloatType:
		switch v.Bits {
		case 16:
			w.u8(typeTagF16)
		case 32:
			w.u8(typeTagF32)
		case 64:
			w.u8(typeTagF64)
		default:
			return fmt.Errorf("vbyte: unsupported float width f%d", v.Bits)
		}
	case vir.PtrType:
		w.u8(typeTagPtr)
	case vir.VoidType:
		w.u8(typeTagVoid)
	case vir.ValistType:
		w.u8(typeTagValist)
	case vir.VecType:
		w.u8(typeTagVec)
		if err := encodeType(w, v.Elem, ec); err != nil {
			return err
		}
		w.uleb(uint64(v.Len))
	case vir.StructType:
		w.u8(typeTagStruct)
		if err := encodeStructTypeRef(w, v, ec); err != nil {
			return err
		}
	case vir.ArrayType:
		w.u8(typeTagArray)
		if err := encodeType(w, v.Elem, ec); err != nil {
			return err
		}
		w.uleb(uint64(v.Len))
	default:
		return fmt.Errorf("vbyte: unknown Type implementation %T", t)
	}
	return nil
}

func encodeStructTypeRef(w *writer, t vir.StructType, ec *encodeContext) error {
	if t.Import == "" {
		w.boolean(false)
		idx, ok := ec.structIndex[t.Name]
		if !ok {
			return fmt.Errorf("vbyte: reference to unknown local struct %q", t.Name)
		}
		w.idx(idx)
		return nil
	}
	w.boolean(true)
	pathID, err := ec.strings.id(t.Import)
	if err != nil {
		return err
	}
	nameID, err := ec.strings.id(t.Name)
	if err != nil {
		return err
	}
	w.idx(pathID)
	w.idx(nameID)
	return nil
}

func decodeType(r *reader, dc *decodeContext) (vir.Type, error) {
	tag, err := r.u8()
	if err != nil {
		return nil, err
	}
	switch tag {
	case typeTagInt:
		n, err := r.uleb()
		if err != nil {
			return nil, err
		}
		if n < 1 {
			return nil, fmt.Errorf("vbyte: i0 is rejected (§6)")
		}
		return vir.IntType{Bits: int(n)}, nil
	case typeTagF16:
		return vir.FloatType{Bits: 16}, nil
	case typeTagF32:
		return vir.FloatType{Bits: 32}, nil
	case typeTagF64:
		return vir.FloatType{Bits: 64}, nil
	case typeTagPtr:
		return vir.PtrType{}, nil
	case typeTagVoid:
		return vir.VoidType{}, nil
	case typeTagValist:
		return vir.ValistType{}, nil
	case typeTagVec:
		elem, err := decodeType(r, dc)
		if err != nil {
			return nil, err
		}
		n, err := r.uleb()
		if err != nil {
			return nil, err
		}
		return vir.VecType{Elem: elem, Len: int(n)}, nil
	case typeTagStruct:
		return decodeStructTypeRef(r, dc)
	case typeTagArray:
		elem, err := decodeType(r, dc)
		if err != nil {
			return nil, err
		}
		n, err := r.uleb()
		if err != nil {
			return nil, err
		}
		return vir.ArrayType{Elem: elem, Len: int(n)}, nil
	default:
		return nil, fmt.Errorf("vbyte: unknown type tag 0x%02x (§1 strict rejection)", tag)
	}
}

func decodeStructTypeRef(r *reader, dc *decodeContext) (vir.Type, error) {
	imported, err := r.boolean()
	if err != nil {
		return nil, err
	}
	if !imported {
		idx, err := r.idx()
		if err != nil {
			return nil, err
		}
		if idx < 0 || idx >= len(dc.structs) {
			return nil, fmt.Errorf("vbyte: struct index %d out of range (§1 strict rejection)", idx)
		}
		return vir.StructType{Name: dc.structs[idx].Name}, nil
	}
	pathID, err := r.idx()
	if err != nil {
		return nil, err
	}
	nameID, err := r.idx()
	if err != nil {
		return nil, err
	}
	path, err := stringAt(dc.strings, pathID)
	if err != nil {
		return nil, err
	}
	name, err := stringAt(dc.strings, nameID)
	if err != nil {
		return nil, err
	}
	return vir.StructType{Name: name, Import: path}, nil
}