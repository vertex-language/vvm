// File: format/vbyte/binary/operand.go
package binary

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

var orderingByByte = []string{"relaxed", "acquire", "release", "acqrel", "seqcst"}

func orderingFromByte(b byte) (string, bool) {
	if int(b) < len(orderingByByte) {
		return orderingByByte[b], true
	}
	return "", false
}

func byteFromOrdering(s string) (byte, bool) {
	for i, o := range orderingByByte {
		if o == s {
			return byte(i), true
		}
	}
	return 0, false
}

// encodeOperand encodes a general operand (§8.4). field.ptr's struct-name /
// field-name operands (tags 0x07/0x08) are handled by the caller
// (function.go), not here, since they need sibling-operand context this
// function doesn't have.
func encodeOperand(w *writer, op vir.Operand, fctx *funcCtx, ec *encodeContext) error {
	switch op.Kind {
	case vir.OperandIdent:
		if op.Qualifier != "" {
			w.u8(tagQualified)
			modID, err := ec.strings.id(op.Qualifier)
			if err != nil {
				return err
			}
			nameID, err := ec.strings.id(op.Ident)
			if err != nil {
				return err
			}
			w.idx(modID)
			w.idx(nameID)
			return nil
		}
		if idx, ok := fctx.localID[op.Ident]; ok {
			w.u8(tagLocal)
			w.idx(idx)
			return nil
		}
		if idx, ok := ec.globalIndex[op.Ident]; ok {
			w.u8(tagGlobal)
			w.idx(idx)
			return nil
		}
		if idx, ok := ec.fnIndex[op.Ident]; ok {
			w.u8(tagCallableRef)
			w.u8(0)
			w.idx(idx)
			return nil
		}
		if idx, ok := ec.externFnIndex[op.Ident]; ok {
			w.u8(tagCallableRef)
			w.u8(1)
			w.idx(idx)
			return nil
		}
		return fmt.Errorf("vbyte: unresolved identifier operand %q", op.Ident)
	case vir.OperandInt, vir.OperandFloat, vir.OperandString, vir.OperandBool, vir.OperandNull:
		w.u8(tagLiteral)
		return encodeLiteralOperand(w, op, ec)
	case vir.OperandType:
		w.u8(tagType)
		return encodeType(w, op.Type, ec)
	case vir.OperandOrdering:
		w.u8(tagOrdering)
		b, ok := byteFromOrdering(op.Ordering)
		if !ok {
			return fmt.Errorf("vbyte: unknown ordering %q", op.Ordering)
		}
		w.u8(b)
	case vir.OperandVector:
		w.u8(tagVectorLiteral)
		w.uleb(uint64(len(op.Vector)))
		for _, v := range op.Vector {
			w.sleb(v)
		}
	default:
		return fmt.Errorf("vbyte: unknown operand kind %v", op.Kind)
	}
	return nil
}

func decodeOperand(r *reader, dc *decodeContext, fctx *funcCtx) (vir.Operand, error) {
	tag, err := r.u8()
	if err != nil {
		return vir.Operand{}, err
	}
	switch tag {
	case tagLocal:
		idx, err := r.idx()
		if err != nil {
			return vir.Operand{}, err
		}
		if idx < 0 || idx >= len(fctx.locals) {
			return vir.Operand{}, fmt.Errorf("vbyte: local index %d out of range (§1 strict rejection)", idx)
		}
		return vir.Ident(fctx.locals[idx]), nil
	case tagGlobal:
		idx, err := r.idx()
		if err != nil {
			return vir.Operand{}, err
		}
		if idx < 0 || idx >= len(dc.globals) {
			return vir.Operand{}, fmt.Errorf("vbyte: global index %d out of range (§1 strict rejection)", idx)
		}
		return vir.Ident(dc.globals[idx].Name), nil
	case tagQualified:
		modID, err := r.idx()
		if err != nil {
			return vir.Operand{}, err
		}
		nameID, err := r.idx()
		if err != nil {
			return vir.Operand{}, err
		}
		mod, err := stringAt(dc.strings, modID)
		if err != nil {
			return vir.Operand{}, err
		}
		name, err := stringAt(dc.strings, nameID)
		if err != nil {
			return vir.Operand{}, err
		}
		return vir.QualifiedIdent(mod, name), nil
	case tagLiteral:
		return decodeLiteralOperand(r, dc)
	case tagType:
		t, err := decodeType(r, dc)
		if err != nil {
			return vir.Operand{}, err
		}
		return vir.TypeOperand(t), nil
	case tagOrdering:
		b, err := r.u8()
		if err != nil {
			return vir.Operand{}, err
		}
		ord, ok := orderingFromByte(b)
		if !ok {
			return vir.Operand{}, fmt.Errorf("vbyte: ordering value %d rejected (§8.4)", b)
		}
		return vir.OrderingOperand(ord), nil
	case tagVectorLiteral:
		n, err := r.uleb()
		if err != nil {
			return vir.Operand{}, err
		}
		vals := make([]int64, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := r.sleb()
			if err != nil {
				return vir.Operand{}, err
			}
			vals = append(vals, v)
		}
		return vir.VectorLiteral(vals...), nil
	case tagStructName:
		idx, err := r.idx()
		if err != nil {
			return vir.Operand{}, err
		}
		if idx < 0 || idx >= len(dc.structs) {
			return vir.Operand{}, fmt.Errorf("vbyte: struct index %d out of range", idx)
		}
		return vir.Ident(dc.structs[idx].Name), nil
	case tagFieldName:
		return vir.Operand{}, fmt.Errorf("vbyte: Field Name operand tag is only legal as field.ptr's third operand (§8.4)")
	case tagCallableRef:
		kb, err := r.u8()
		if err != nil {
			return vir.Operand{}, err
		}
		idx, err := r.idx()
		if err != nil {
			return vir.Operand{}, err
		}
		switch kb {
		case 0:
			if idx < 0 || idx >= len(dc.fns) {
				return vir.Operand{}, fmt.Errorf("vbyte: fn index %d out of range", idx)
			}
			return vir.Ident(dc.fns[idx].Name), nil
		case 1:
			if idx < 0 || idx >= len(dc.externFns) {
				return vir.Operand{}, fmt.Errorf("vbyte: extern_fn index %d out of range", idx)
			}
			return vir.Ident(dc.externFns[idx].Name), nil
		default:
			return vir.Operand{}, fmt.Errorf("vbyte: invalid Callable Ref kind byte 0x%02x (§8.4)", kb)
		}
	default:
		return vir.Operand{}, fmt.Errorf("vbyte: unknown operand tag 0x%02x (§1 strict rejection)", tag)
	}
}