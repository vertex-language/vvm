// File: format/vbyte/binary/decls.go
package binary

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

func decodeHeader(r *reader, m *vir.Module, dc *decodeContext) error {
	id, err := r.idx()
	if err != nil {
		return err
	}
	name, err := stringAt(dc.strings, id)
	if err != nil {
		return err
	}
	m.Name = name
	return nil
}

func decodeNamespace(r *reader, m *vir.Module, dc *decodeContext) error {
	id, err := r.idx()
	if err != nil {
		return err
	}
	ns, err := stringAt(dc.strings, id)
	if err != nil {
		return err
	}
	m.Namespace = ns
	return nil
}

func encodeTarget(w *writer, t *vir.Target, ec *encodeContext) error {
	archID, err := ec.strings.id(t.Arch)
	if err != nil {
		return err
	}
	osID, err := ec.strings.id(t.OS)
	if err != nil {
		return err
	}
	w.idx(archID)
	w.idx(osID)

	hasABI := t.ABI != ""
	w.boolean(hasABI)
	if hasABI {
		abiID, err := ec.strings.id(t.ABI)
		if err != nil {
			return err
		}
		w.idx(abiID)
	}

	hasTiers := len(t.Tiers) > 0
	w.boolean(hasTiers)
	if hasTiers {
		w.uleb(uint64(len(t.Tiers)))
		for _, tier := range t.Tiers {
			id, err := ec.strings.id(tier)
			if err != nil {
				return err
			}
			w.idx(id)
		}
	}
	return nil
}

func decodeTarget(r *reader, m *vir.Module, dc *decodeContext) error {
	archID, err := r.idx()
	if err != nil {
		return err
	}
	osID, err := r.idx()
	if err != nil {
		return err
	}
	arch, err := stringAt(dc.strings, archID)
	if err != nil {
		return err
	}
	os, err := stringAt(dc.strings, osID)
	if err != nil {
		return err
	}
	hasABI, err := r.boolean()
	if err != nil {
		return err
	}
	abi := ""
	if hasABI {
		abiID, err := r.idx()
		if err != nil {
			return err
		}
		abi, err = stringAt(dc.strings, abiID)
		if err != nil {
			return err
		}
	}
	hasTiers, err := r.boolean()
	if err != nil {
		return err
	}
	var tiers []string
	if hasTiers {
		n, err := r.uleb()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("vbyte: has_tiers=1 with an empty vec is a decode error (§5)")
		}
		for i := uint64(0); i < n; i++ {
			tid, err := r.idx()
			if err != nil {
				return err
			}
			tier, err := stringAt(dc.strings, tid)
			if err != nil {
				return err
			}
			tiers = append(tiers, tier)
		}
	}
	m.Target = &vir.Target{Arch: arch, OS: os, ABI: abi, Tiers: tiers}
	return nil
}

func encodeStruct(w *writer, s *vir.Struct, ec *encodeContext) error {
	w.boolean(s.Export)
	id, err := ec.strings.id(s.Name)
	if err != nil {
		return err
	}
	w.idx(id)
	w.uleb(uint64(len(s.Fields)))
	for _, f := range s.Fields {
		fid, err := ec.strings.id(f.Name)
		if err != nil {
			return err
		}
		w.idx(fid)
		if err := encodeType(w, f.Type, ec); err != nil {
			return err
		}
	}
	return nil
}

func decodeStructs(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		export, err := r.boolean()
		if err != nil {
			return err
		}
		nameID, err := r.idx()
		if err != nil {
			return err
		}
		name, err := stringAt(dc.strings, nameID)
		if err != nil {
			return err
		}
		fn, err := r.uleb()
		if err != nil {
			return err
		}
		fields := make([]vir.Field, 0, fn)
		for j := uint64(0); j < fn; j++ {
			fnameID, err := r.idx()
			if err != nil {
				return err
			}
			fname, err := stringAt(dc.strings, fnameID)
			if err != nil {
				return err
			}
			typ, err := decodeType(r, dc)
			if err != nil {
				return err
			}
			fields = append(fields, vir.Field{Name: fname, Type: typ})
		}
		s := &vir.Struct{Name: name, Export: export, Fields: fields}
		m.Structs = append(m.Structs, s)
		dc.structs = append(dc.structs, s)
	}
	return nil
}

func encodeFnSig(w *writer, fs *vir.FunctionSignature, ec *encodeContext) error {
	w.boolean(fs.Export)
	id, err := ec.strings.id(fs.Name)
	if err != nil {
		return err
	}
	w.idx(id)
	w.uleb(uint64(len(fs.Params)))
	for _, p := range fs.Params {
		if err := encodeType(w, p, ec); err != nil {
			return err
		}
	}
	w.boolean(fs.Variadic)
	return encodeType(w, fs.Ret, ec)
}

func decodeFnSigs(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		export, err := r.boolean()
		if err != nil {
			return err
		}
		nameID, err := r.idx()
		if err != nil {
			return err
		}
		name, err := stringAt(dc.strings, nameID)
		if err != nil {
			return err
		}
		pn, err := r.uleb()
		if err != nil {
			return err
		}
		params := make([]vir.Type, 0, pn)
		for j := uint64(0); j < pn; j++ {
			t, err := decodeType(r, dc)
			if err != nil {
				return err
			}
			params = append(params, t)
		}
		variadic, err := r.boolean()
		if err != nil {
			return err
		}
		ret, err := decodeType(r, dc)
		if err != nil {
			return err
		}
		fs := &vir.FunctionSignature{Name: name, Export: export, Params: params, Variadic: variadic, Ret: ret}
		m.FunctionSignatures = append(m.FunctionSignatures, fs)
		dc.fnsigs = append(dc.fnsigs, fs)
	}
	return nil
}

func encodeConst(w *writer, c *vir.Constant, ec *encodeContext) error {
	w.boolean(c.Export)
	id, err := ec.strings.id(c.Name)
	if err != nil {
		return err
	}
	w.idx(id)
	if err := encodeType(w, c.Type, ec); err != nil {
		return err
	}
	return encodeLiteralOperand(w, c.Value, ec)
}

func decodeConsts(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		export, err := r.boolean()
		if err != nil {
			return err
		}
		nameID, err := r.idx()
		if err != nil {
			return err
		}
		name, err := stringAt(dc.strings, nameID)
		if err != nil {
			return err
		}
		typ, err := decodeType(r, dc)
		if err != nil {
			return err
		}
		val, err := decodeLiteralOperand(r, dc)
		if err != nil {
			return err
		}
		c := &vir.Constant{Name: name, Export: export, Type: typ, Value: val}
		m.Constants = append(m.Constants, c)
		dc.consts = append(dc.consts, c)
	}
	return nil
}

func encodeAlignByte(w *writer, align int) error {
	if align == 0 {
		w.u8(0)
		return nil
	}
	if align&(align-1) != 0 {
		return fmt.Errorf("vbyte: alignment %d is not a power of two (§5)", align)
	}
	lz := 0
	for v := align; v > 1; v >>= 1 {
		lz++
	}
	w.u8(byte(lz + 1))
	return nil
}

func decodeAlignByte(r *reader) (int, error) {
	b, err := r.u8()
	if err != nil {
		return 0, err
	}
	if b == 0 {
		return 0, nil
	}
	return 1 << (b - 1), nil
}

func encodeGlobal(w *writer, g *vir.Global, ec *encodeContext) error {
	w.boolean(g.Export)
	id, err := ec.strings.id(g.Name)
	if err != nil {
		return err
	}
	w.idx(id)
	w.boolean(g.TLS)
	if err := encodeType(w, g.Type, ec); err != nil {
		return err
	}
	if err := encodeAlignByte(w, g.Align); err != nil {
		return err
	}
	return encodeConstInit(w, g.Init, ec)
}

func decodeGlobals(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		export, err := r.boolean()
		if err != nil {
			return err
		}
		nameID, err := r.idx()
		if err != nil {
			return err
		}
		name, err := stringAt(dc.strings, nameID)
		if err != nil {
			return err
		}
		tls, err := r.boolean()
		if err != nil {
			return err
		}
		typ, err := decodeType(r, dc)
		if err != nil {
			return err
		}
		align, err := decodeAlignByte(r)
		if err != nil {
			return err
		}
		init, err := decodeConstInit(r, dc)
		if err != nil {
			return err
		}
		g := &vir.Global{Name: name, Export: export, TLS: tls, Type: typ, Align: align, Init: init}
		m.Globals = append(m.Globals, g)
		dc.globals = append(dc.globals, g)
	}
	return nil
}

func encodeLink(w *writer, l *vir.Link, ec *encodeContext) error {
	var kb byte
	switch l.Kind {
	case vir.LinkStatic:
		kb = 0
	case vir.LinkShared:
		kb = 1
	case vir.LinkFramework:
		kb = 2
	default:
		return fmt.Errorf("vbyte: unknown link kind %q", l.Kind)
	}
	w.u8(kb)
	id, err := ec.strings.id(l.Name)
	if err != nil {
		return err
	}
	w.idx(id)
	return nil
}

func decodeLinks(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		kb, err := r.u8()
		if err != nil {
			return err
		}
		var kind vir.LinkKind
		switch kb {
		case 0:
			kind = vir.LinkStatic
		case 1:
			kind = vir.LinkShared
		case 2:
			kind = vir.LinkFramework
		default:
			return fmt.Errorf("vbyte: invalid lib_kind byte 0x%02x (§5)", kb)
		}
		nameID, err := r.idx()
		if err != nil {
			return err
		}
		name, err := stringAt(dc.strings, nameID)
		if err != nil {
			return err
		}
		m.Links = append(m.Links, &vir.Link{Kind: kind, Name: name})
	}
	return nil
}

func encodeParamAttr(w *writer, p vir.Param, ec *encodeContext) error {
	switch {
	case p.ByVal != "":
		w.u8(1)
		idx, ok := ec.structIndex[p.ByVal]
		if !ok {
			return fmt.Errorf("vbyte: byval references unknown struct %q", p.ByVal)
		}
		w.idx(idx)
	case p.SRet != "":
		w.u8(2)
		idx, ok := ec.structIndex[p.SRet]
		if !ok {
			return fmt.Errorf("vbyte: sret references unknown struct %q", p.SRet)
		}
		w.idx(idx)
	default:
		w.u8(0)
	}
	return nil
}

func encodeParam(w *writer, p vir.Param, ec *encodeContext) error {
	id, err := ec.strings.id(p.Name)
	if err != nil {
		return err
	}
	w.idx(id)
	if err := encodeType(w, p.Type, ec); err != nil {
		return err
	}
	return encodeParamAttr(w, p, ec)
}

func decodeParamAttr(r *reader, dc *decodeContext) (byval, sret string, err error) {
	kind, err := r.u8()
	if err != nil {
		return "", "", err
	}
	switch kind {
	case 0:
		return "", "", nil
	case 1:
		idx, err := r.idx()
		if err != nil {
			return "", "", err
		}
		if idx < 0 || idx >= len(dc.structs) {
			return "", "", fmt.Errorf("vbyte: byval struct index %d out of range", idx)
		}
		return dc.structs[idx].Name, "", nil
	case 2:
		idx, err := r.idx()
		if err != nil {
			return "", "", err
		}
		if idx < 0 || idx >= len(dc.structs) {
			return "", "", fmt.Errorf("vbyte: sret struct index %d out of range", idx)
		}
		return "", dc.structs[idx].Name, nil
	default:
		return "", "", fmt.Errorf("vbyte: invalid param_attr kind byte 0x%02x (§5)", kind)
	}
}

func decodeParam(r *reader, dc *decodeContext) (vir.Param, error) {
	nameID, err := r.idx()
	if err != nil {
		return vir.Param{}, err
	}
	name, err := stringAt(dc.strings, nameID)
	if err != nil {
		return vir.Param{}, err
	}
	typ, err := decodeType(r, dc)
	if err != nil {
		return vir.Param{}, err
	}
	byval, sret, err := decodeParamAttr(r, dc)
	if err != nil {
		return vir.Param{}, err
	}
	return vir.Param{Name: name, Type: typ, ByVal: byval, SRet: sret}, nil
}

// fnAttrBit maps FunctionAttribute -> bit position (§5 fn_attr_bits).
var fnAttrBit = map[vir.FunctionAttribute]uint{
	vir.AttributeNoReturn: 0,
	vir.AttributeReadonly: 1,
	vir.AttributeInline:   2,
	vir.AttributeNoInline: 3,
	vir.AttributeCold:     4,
	vir.AttributeEntry:    5,
	vir.AttributeExternC:  6,
}

var bitFnAttr = map[uint]vir.FunctionAttribute{
	0: vir.AttributeNoReturn,
	1: vir.AttributeReadonly,
	2: vir.AttributeInline,
	3: vir.AttributeNoInline,
	4: vir.AttributeCold,
	5: vir.AttributeEntry,
	6: vir.AttributeExternC,
}

func encodeAttrBits(attrs []vir.FunctionAttribute) (byte, error) {
	var b byte
	for _, a := range attrs {
		pos, ok := fnAttrBit[a]
		if !ok {
			return 0, fmt.Errorf("vbyte: unknown function attribute %q", a)
		}
		b |= 1 << pos
	}
	if b&(1<<5) != 0 && b&(1<<6) != 0 {
		return 0, fmt.Errorf("vbyte: entry and extern_c are mutually exclusive (§5)")
	}
	return b, nil
}

func decodeAttrBits(b byte) ([]vir.FunctionAttribute, error) {
	if b&0x80 != 0 {
		return nil, fmt.Errorf("vbyte: reserved bit 7 of fn_attr_bits is set (§5)")
	}
	if b&(1<<5) != 0 && b&(1<<6) != 0 {
		return nil, fmt.Errorf("vbyte: entry and extern_c set together is a decode error (§5)")
	}
	var attrs []vir.FunctionAttribute
	for pos := uint(0); pos <= 6; pos++ {
		if b&(1<<pos) != 0 {
			attrs = append(attrs, bitFnAttr[pos])
		}
	}
	return attrs, nil
}

func encodeExternGroup(w *writer, eg *vir.ExternGroup, ec *encodeContext) error {
	depID, err := ec.strings.id(eg.Dependency)
	if err != nil {
		return err
	}
	w.idx(depID)
	w.uleb(uint64(len(eg.Functions)))
	for _, f := range eg.Functions {
		if err := encodeExternFunction(w, f, ec); err != nil {
			return err
		}
	}
	return nil
}

func encodeExternFunction(w *writer, f *vir.ExternFunction, ec *encodeContext) error {
	id, err := ec.strings.id(f.Name)
	if err != nil {
		return err
	}
	w.idx(id)
	w.uleb(uint64(len(f.Params)))
	for _, p := range f.Params {
		if err := encodeParam(w, p, ec); err != nil {
			return err
		}
	}
	w.boolean(f.Variadic)
	if err := encodeType(w, f.Ret, ec); err != nil {
		return err
	}
	bits, err := encodeAttrBits(f.Attrs)
	if err != nil {
		return err
	}
	w.u8(bits)
	return nil
}

func decodeExterns(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		depID, err := r.idx()
		if err != nil {
			return err
		}
		dep, err := stringAt(dc.strings, depID)
		if err != nil {
			return err
		}
		eg := &vir.ExternGroup{Dependency: dep}
		fn, err := r.uleb()
		if err != nil {
			return err
		}
		for j := uint64(0); j < fn; j++ {
			f, err := decodeExternFunction(r, dc)
			if err != nil {
				return err
			}
			eg.Functions = append(eg.Functions, f)
			dc.externFns = append(dc.externFns, f)
		}
		m.Externs = append(m.Externs, eg)
	}
	return nil
}

func decodeExternFunction(r *reader, dc *decodeContext) (*vir.ExternFunction, error) {
	nameID, err := r.idx()
	if err != nil {
		return nil, err
	}
	name, err := stringAt(dc.strings, nameID)
	if err != nil {
		return nil, err
	}
	pn, err := r.uleb()
	if err != nil {
		return nil, err
	}
	params := make([]vir.Param, 0, pn)
	for i := uint64(0); i < pn; i++ {
		p, err := decodeParam(r, dc)
		if err != nil {
			return nil, err
		}
		params = append(params, p)
	}
	variadic, err := r.boolean()
	if err != nil {
		return nil, err
	}
	ret, err := decodeType(r, dc)
	if err != nil {
		return nil, err
	}
	bits, err := r.u8()
	if err != nil {
		return nil, err
	}
	attrs, err := decodeAttrBits(bits)
	if err != nil {
		return nil, err
	}
	return &vir.ExternFunction{Name: name, Params: params, Variadic: variadic, Ret: ret, Attrs: attrs}, nil
}

func decodeImports(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		id, err := r.idx()
		if err != nil {
			return err
		}
		path, err := stringAt(dc.strings, id)
		if err != nil {
			return err
		}
		m.Imports = append(m.Imports, &vir.Import{Path: path})
	}
	return nil
}