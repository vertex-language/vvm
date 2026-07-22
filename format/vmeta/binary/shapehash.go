// shapehash.go
package binary

import (
	"crypto/sha256"
	"fmt"
	"math"

	vir "github.com/vertex-language/vvm/ir/vir"
)

// shapeHash computes the §F5.5 digest for one export: SHA-256 over a
// canonical, self-contained encoding of exactly the fields §7.4 compares
// — never this file's own STRT/TYPE indices. Two independently-compiled
// .vmeta files for structurally-equal exports must hash identically
// regardless of each file's own declaration order, which is exactly what
// §7.4's "structural equality" promise, and Stage B's cheap hash compare
// (§F5.5), depend on.
func shapeHash(kind byte, canon []byte) [32]byte {
	return sha256.Sum256(append([]byte{kind}, canon...))
}

// canonType recursively encodes t self-containedly (kind tag + payload,
// no table indices), expanding struct references structurally via
// resolve — the same expansion §7.4 requires.
func canonType(t vir.Type, resolve structResolver) ([]byte, error) {
	if t == nil {
		return []byte{0x00}, nil
	}
	switch x := t.(type) {
	case vir.VoidType:
		return []byte{kindVoid}, nil
	case vir.IntType:
		return append([]byte{kindInt}, putUleb(nil, uint64(x.Bits))...), nil
	case vir.FloatType:
		return []byte{kindFloat, byte(x.Bits)}, nil
	case vir.PtrType:
		return []byte{kindPtr}, nil
	case vir.ValistType:
		return []byte{kindValist}, nil
	case vir.VecType:
		elem, err := canonType(x.Elem, resolve)
		if err != nil {
			return nil, err
		}
		buf := append([]byte{kindVec}, putUleb(nil, uint64(x.Len))...)
		return append(buf, elem...), nil
	case vir.ArrayType:
		elem, err := canonType(x.Elem, resolve)
		if err != nil {
			return nil, err
		}
		buf := append([]byte{kindArray}, putUleb(nil, uint64(x.Len))...)
		return append(buf, elem...), nil
	case vir.StructType:
		ss, ok := resolve(x.Name)
		if !ok {
			return nil, fmt.Errorf("struct %q: no export-tagged shape available for structural hashing (§7.4)", x.Name)
		}
		return canonFields(ss.Fields, resolve)
	default:
		return nil, fmt.Errorf("cannot hash type %T", t)
	}
}

func canonFields(fields []vir.Field, resolve structResolver) ([]byte, error) {
	buf := append([]byte{kindStructS}, putUleb(nil, uint64(len(fields)))...)
	for _, f := range fields {
		fb, err := canonType(f.Type, resolve)
		if err != nil {
			return nil, err
		}
		buf = append(buf, fb...)
	}
	return buf, nil
}

// canonParam encodes a Param's structural contribution: its type, plus
// (for byval/sret) the full field-type expansion of the named struct —
// §7.4 compares byval[S]/sret[S] structurally, never by name.
func canonParam(p vir.Param, resolve structResolver) ([]byte, error) {
	tb, err := canonType(p.Type, resolve)
	if err != nil {
		return nil, err
	}
	buf := append([]byte{}, tb...)
	switch {
	case p.ByVal != "":
		ss, ok := resolve(p.ByVal)
		if !ok {
			return nil, fmt.Errorf("byval[%s]: no export-tagged shape available for structural hashing (§7.4)", p.ByVal)
		}
		fb, err := canonFields(ss.Fields, resolve)
		if err != nil {
			return nil, err
		}
		buf = append(buf, 1)
		buf = append(buf, fb...)
	case p.SRet != "":
		ss, ok := resolve(p.SRet)
		if !ok {
			return nil, fmt.Errorf("sret[%s]: no export-tagged shape available for structural hashing (§7.4)", p.SRet)
		}
		fb, err := canonFields(ss.Fields, resolve)
		if err != nil {
			return nil, err
		}
		buf = append(buf, 2)
		buf = append(buf, fb...)
	default:
		buf = append(buf, 0)
	}
	return buf, nil
}

func attrBit(attrs []vir.FunctionAttribute, a vir.FunctionAttribute) bool {
	for _, x := range attrs {
		if x == a {
			return true
		}
	}
	return false
}

func fnShapeHash(fs vir.FnShape, resolve structResolver) ([32]byte, error) {
	var buf []byte
	buf = putUleb(buf, uint64(len(fs.Params)))
	buf = append(buf, boolByte(fs.Variadic))
	for _, p := range fs.Params {
		pb, err := canonParam(p, resolve)
		if err != nil {
			return [32]byte{}, err
		}
		buf = append(buf, pb...)
	}
	retb, err := canonType(fs.Ret, resolve)
	if err != nil {
		return [32]byte{}, err
	}
	buf = append(buf, retb...)
	buf = append(buf, boolByte(attrBit(fs.Attrs, vir.AttributeNoReturn)))
	buf = append(buf, boolByte(attrBit(fs.Attrs, vir.AttributeReadonly)))
	return shapeHash(0 /* fn */, buf), nil
}

func fnSigShapeHash(fs vir.FnSigShape, resolve structResolver) ([32]byte, error) {
	var buf []byte
	buf = putUleb(buf, uint64(len(fs.Params)))
	buf = append(buf, boolByte(fs.Variadic))
	for _, p := range fs.Params {
		pb, err := canonType(p, resolve)
		if err != nil {
			return [32]byte{}, err
		}
		buf = append(buf, pb...)
	}
	retb, err := canonType(fs.Ret, resolve)
	if err != nil {
		return [32]byte{}, err
	}
	buf = append(buf, retb...)
	return shapeHash(4 /* fnsig */, buf), nil
}

func globalShapeHash(gs vir.GlobalShape, resolve structResolver) ([32]byte, error) {
	tb, err := canonType(gs.Type, resolve)
	if err != nil {
		return [32]byte{}, err
	}
	buf := append(append([]byte{}, tb...), boolByte(gs.TLS))
	return shapeHash(1 /* global */, buf), nil
}

func structShapeHash(ss vir.StructShape, resolve structResolver) ([32]byte, error) {
	buf, err := canonFields(ss.Fields, resolve)
	if err != nil {
		return [32]byte{}, err
	}
	return shapeHash(2 /* struct */, buf), nil
}

func constShapeHash(cs vir.ConstShape, resolve structResolver) ([32]byte, error) {
	tb, err := canonType(cs.Type, resolve)
	if err != nil {
		return [32]byte{}, err
	}
	lb, err := canonLiteral(cs.Value)
	if err != nil {
		return [32]byte{}, err
	}
	buf := append(append([]byte{}, tb...), lb...)
	return shapeHash(3 /* const */, buf), nil
}

// canonLiteral mirrors encodeLiteral but never touches a string table —
// string content is hashed inline so the digest doesn't depend on this
// file's own STRT interning order.
func canonLiteral(o vir.Operand) ([]byte, error) {
	switch o.Kind {
	case vir.OperandInt:
		return append([]byte{litInt}, putSleb(nil, o.Int)...), nil
	case vir.OperandFloat:
		buf := []byte{litFloat}
		return putSleb(buf, int64(math.Float64bits(o.Float))), nil
	case vir.OperandString:
		buf := []byte{litString}
		buf = putUleb(buf, uint64(len(o.Str)))
		return append(buf, o.Str...), nil
	case vir.OperandNull:
		return []byte{litNull}, nil
	case vir.OperandBool:
		return []byte{litBool, boolByte(o.Bool)}, nil
	case vir.OperandVector:
		buf := []byte{litVector}
		buf = putUleb(buf, uint64(len(o.Vector)))
		for _, v := range o.Vector {
			buf = putSleb(buf, v)
		}
		return buf, nil
	default:
		return nil, fmt.Errorf("canonLiteral: operand kind %d is not a literal", o.Kind)
	}
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}