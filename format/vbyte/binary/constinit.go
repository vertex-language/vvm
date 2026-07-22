// File: format/vbyte/binary/constinit.go
package binary

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

const (
	initTagZero        = 0x0F
	initTagAddressOf   = 0x10
	initTagAggregate   = 0x11
	initTagByteString  = 0x12
)

func encodeConstInit(w *writer, ci vir.ConstInit, ec *encodeContext) error {
	switch v := ci.(type) {
	case vir.InitLiteral:
		return encodeLiteralOperand(w, v.Value, ec)
	case vir.InitZero:
		w.u8(initTagZero)
		return nil
	case vir.InitAddressOf:
		return encodeAddressOf(w, v.Name, ec)
	case *vir.InitAddressOf:
		return encodeAddressOf(w, v.Name, ec)
	case vir.InitAggregate:
		w.u8(initTagAggregate)
		w.uleb(uint64(len(v.Elems)))
		for _, e := range v.Elems {
			if err := encodeConstInit(w, e, ec); err != nil {
				return err
			}
		}
		return nil
	case vir.InitByteString:
		w.u8(initTagByteString)
		w.bytesVec(v.Data)
		return nil
	default:
		return fmt.Errorf("vbyte: unknown ConstInit implementation %T", ci)
	}
}

func encodeAddressOf(w *writer, name string, ec *encodeContext) error {
	w.u8(initTagAddressOf)
	if idx, ok := ec.globalIndex[name]; ok {
		w.u8(0)
		w.idx(idx)
		return nil
	}
	if idx, ok := ec.fnIndex[name]; ok {
		w.u8(1)
		w.idx(idx)
		return nil
	}
	return fmt.Errorf("vbyte: addr target %q is neither a known global nor function", name)
}

// decodeConstInit decodes one const_init. InitAddressOf targets may be
// forward references to functions (the Functions section is decoded after
// Globals), so resolution is deferred via dc.pending and completed once
// the whole module has been read (see Decode in binary.go).
func decodeConstInit(r *reader, dc *decodeContext) (vir.ConstInit, error) {
	tag, err := r.u8()
	if err != nil {
		return nil, err
	}
	switch {
	case tag >= litTagInt && tag <= litTagNegInf:
		op, err := decodeLiteralPayload(r, dc, tag)
		if err != nil {
			return nil, err
		}
		return vir.InitLiteral{Value: op}, nil
	case tag == initTagZero:
		return vir.InitZero{}, nil
	case tag == initTagAddressOf:
		kb, err := r.u8()
		if err != nil {
			return nil, err
		}
		idx, err := r.idx()
		if err != nil {
			return nil, err
		}
		if kb != 0 && kb != 1 {
			return nil, fmt.Errorf("vbyte: invalid addr kind byte 0x%02x (§7)", kb)
		}
		target := &vir.InitAddressOf{}
		dc.pending = append(dc.pending, &pendingFixup{kind: int(kb), index: idx, ptr: target})
		return target, nil
	case tag == initTagAggregate:
		n, err := r.uleb()
		if err != nil {
			return nil, err
		}
		elems := make([]vir.ConstInit, 0, n)
		for i := uint64(0); i < n; i++ {
			e, err := decodeConstInit(r, dc)
			if err != nil {
				return nil, err
			}
			elems = append(elems, e)
		}
		return vir.InitAggregate{Elems: elems}, nil
	case tag == initTagByteString:
		data, err := r.bytesVec()
		if err != nil {
			return nil, err
		}
		return vir.InitByteString{Data: data}, nil
	default:
		return nil, fmt.Errorf("vbyte: invalid const_init tag 0x%02x (§7)", tag)
	}
}