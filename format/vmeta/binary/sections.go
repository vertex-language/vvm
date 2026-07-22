// sections.go
package binary

import (
	"fmt"
	"sort"

	vir "github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// MODU — module name + namespace (§F5.2, §F4.2).
// ---------------------------------------------------------------------------

func encodeModu(strs *stringTable, shape *vir.ModuleShape) []byte {
	buf := putUleb(nil, uint64(strs.intern(shape.ModuleName)))
	return putUleb(buf, uint64(strs.intern(shape.Namespace)))
}

func decodeModu(strs []string, payload []byte) (name, namespace string, err error) {
	idx, n, err := readUleb(payload)
	if err != nil {
		return "", "", err
	}
	name, err = strAt(strs, idx)
	if err != nil {
		return "", "", err
	}
	idx2, n2, err := readUleb(payload[n:])
	if err != nil {
		return "", "", err
	}
	namespace, err = strAt(strs, idx2)
	if err != nil {
		return "", "", err
	}
	if n+n2 != len(payload) {
		return "", "", fmt.Errorf("trailing bytes")
	}
	if name == "" {
		return "", "", fmt.Errorf("module name is absent (§2.1)")
	}
	return name, namespace, nil
}

// ---------------------------------------------------------------------------
// TARG — target triple + tiers (§F5.2, §F4.2). Canonical spellings only
// (§7.1); an alias reaching this layer is a malformed file, not a lenient
// decode.
// ---------------------------------------------------------------------------

func encodeTarg(strs *stringTable, t *vir.Target) ([]byte, error) {
	if !vir.CanonicalArch[t.Arch] {
		return nil, fmt.Errorf("target arch %q is not canonical (§7.1)", t.Arch)
	}
	if !vir.CanonicalOS[t.OS] {
		return nil, fmt.Errorf("target os %q is not canonical (§7.1)", t.OS)
	}
	if t.ABI != "" && !vir.CanonicalABI[t.ABI] {
		return nil, fmt.Errorf("target abi %q is not canonical (§7.1)", t.ABI)
	}
	buf := putUleb(nil, uint64(strs.intern(t.Arch)))
	buf = putUleb(buf, uint64(strs.intern(t.OS)))
	buf = putUleb(buf, uint64(strs.intern(t.ABI)))
	buf = putUleb(buf, uint64(len(t.Tiers)))
	for _, tier := range t.Tiers {
		buf = putUleb(buf, uint64(strs.intern(tier)))
	}
	return buf, nil
}

func decodeTarg(strs []string, payload []byte) (*vir.Target, error) {
	archIdx, n, err := readUleb(payload)
	if err != nil {
		return nil, err
	}
	arch, err := strAt(strs, archIdx)
	if err != nil {
		return nil, err
	}
	osIdx, m, err := readUleb(payload[n:])
	if err != nil {
		return nil, err
	}
	n += m
	os, err := strAt(strs, osIdx)
	if err != nil {
		return nil, err
	}
	abiIdx, m2, err := readUleb(payload[n:])
	if err != nil {
		return nil, err
	}
	n += m2
	abi, err := strAt(strs, abiIdx)
	if err != nil {
		return nil, err
	}
	tierCount, m3, err := readUleb(payload[n:])
	if err != nil {
		return nil, err
	}
	n += m3
	tiers := make([]string, tierCount)
	for i := uint64(0); i < tierCount; i++ {
		idx, mi, err := readUleb(payload[n:])
		if err != nil {
			return nil, err
		}
		n += mi
		if tiers[i], err = strAt(strs, idx); err != nil {
			return nil, err
		}
	}
	if n != len(payload) {
		return nil, fmt.Errorf("trailing bytes")
	}
	if !vir.CanonicalArch[arch] {
		return nil, fmt.Errorf("target arch %q is not canonical (§7.1)", arch)
	}
	if !vir.CanonicalOS[os] {
		return nil, fmt.Errorf("target os %q is not canonical (§7.1)", os)
	}
	if abi != "" && !vir.CanonicalABI[abi] {
		return nil, fmt.Errorf("target abi %q is not canonical (§7.1)", abi)
	}
	return &vir.Target{Arch: arch, OS: os, ABI: abi, Tiers: tiers}, nil
}

// ---------------------------------------------------------------------------
// IMPD — direct import strings (§F5.2). NONSEMANTIC build-graph hint only.
// ---------------------------------------------------------------------------

func encodeImpd(strs *stringTable, imports []string) []byte {
	buf := putUleb(nil, uint64(len(imports)))
	for _, imp := range imports {
		buf = putUleb(buf, uint64(strs.intern(imp)))
	}
	return buf
}

func decodeImpd(strs []string, payload []byte) ([]string, error) {
	count, n, err := readUleb(payload)
	if err != nil {
		return nil, err
	}
	out := make([]string, count)
	for i := uint64(0); i < count; i++ {
		idx, m, err := readUleb(payload[n:])
		if err != nil {
			return nil, err
		}
		n += m
		if out[i], err = strAt(strs, idx); err != nil {
			return nil, err
		}
	}
	if n != len(payload) {
		return nil, fmt.Errorf("trailing bytes")
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// XPRT — export table (§F5.4).
// ---------------------------------------------------------------------------

const (
	xkFn     = 0
	xkGlobal = 1
	xkStruct = 2
	xkConst  = 3
	xkFnSig  = 4
)

func fnFlags(fn vir.FnShape) uint64 {
	var f uint64
	if fn.Variadic {
		f |= 1 << 0
	}
	if attrBit(fn.Attrs, vir.AttributeNoReturn) {
		f |= 1 << 1
	}
	if attrBit(fn.Attrs, vir.AttributeReadonly) {
		f |= 1 << 2
	}
	if attrBit(fn.Attrs, vir.AttributeEntry) {
		f |= 1 << 3
	}
	if attrBit(fn.Attrs, vir.AttributeExternC) {
		f |= 1 << 4
	}
	return f
}

func fnSigFlags(fs vir.FnSigShape) uint64 {
	if fs.Variadic {
		return 1
	}
	return 0
}

func encodeExportHeader(strs *stringTable, kind byte, name, symbol string, hash [32]byte, payload []byte) []byte {
	buf := []byte{kind}
	buf = putUleb(buf, uint64(strs.intern(name)))
	buf = putUleb(buf, uint64(strs.intern(symbol)))
	buf = append(buf, hash[:]...)
	return append(buf, payload...)
}

func encodeParamShape(types *typeTable, resolve structResolver, p vir.Param) ([]byte, error) {
	idx, err := types.intern(p.Type, resolve)
	if err != nil {
		return nil, err
	}
	buf := putUleb(nil, uint64(idx))
	switch {
	case p.ByVal != "":
		attrIdx, err := types.intern(vir.StructType{Name: p.ByVal}, resolve)
		if err != nil {
			return nil, fmt.Errorf("byval[%s]: %w", p.ByVal, err)
		}
		buf = append(buf, 1)
		buf = putUleb(buf, uint64(attrIdx))
	case p.SRet != "":
		attrIdx, err := types.intern(vir.StructType{Name: p.SRet}, resolve)
		if err != nil {
			return nil, fmt.Errorf("sret[%s]: %w", p.SRet, err)
		}
		buf = append(buf, 2)
		buf = putUleb(buf, uint64(attrIdx))
	default:
		buf = append(buf, 0)
		buf = putUleb(buf, 0)
	}
	return buf, nil
}

func encodeXprt(strs *stringTable, types *typeTable, resolve structResolver, shape *vir.ModuleShape) ([]byte, error) {
	type built struct {
		kind byte
		name string
		enc  []byte
	}
	var entries []built

	// MangledSymbol (mangle.go) only reads Namespace/Name off the Module
	// it's given, so a stub with just those two fields is enough.
	modForSymbol := &vir.Module{Namespace: shape.Namespace, Name: shape.ModuleName}

	for _, fn := range shape.Fns {
		h, err := fnShapeHash(fn, resolve)
		if err != nil {
			return nil, fmt.Errorf("fn %s: %w", fn.Name, err)
		}
		symbol := vir.MangledSymbol(modForSymbol, fn.Name, fn.Attrs)
		payload := putUleb(nil, fnFlags(fn))
		payload = putUleb(payload, uint64(len(fn.Params)))
		for _, p := range fn.Params {
			pb, err := encodeParamShape(types, resolve, p)
			if err != nil {
				return nil, fmt.Errorf("fn %s: %w", fn.Name, err)
			}
			payload = append(payload, pb...)
		}
		retIdx, err := types.intern(fn.Ret, resolve)
		if err != nil {
			return nil, fmt.Errorf("fn %s: return type: %w", fn.Name, err)
		}
		payload = putUleb(payload, uint64(retIdx))
		enc := encodeExportHeader(strs, xkFn, fn.Name, symbol, h, payload)
		entries = append(entries, built{xkFn, fn.Name, enc})
	}

	for _, g := range shape.Globals {
		h, err := globalShapeHash(g, resolve)
		if err != nil {
			return nil, fmt.Errorf("global %s: %w", g.Name, err)
		}
		symbol := vir.MangledSymbol(modForSymbol, g.Name, nil)
		typeIdx, err := types.intern(g.Type, resolve)
		if err != nil {
			return nil, fmt.Errorf("global %s: %w", g.Name, err)
		}
		payload := putUleb(nil, uint64(typeIdx))
		payload = append(payload, boolByte(g.TLS))
		// Align isn't carried by vir.GlobalShape (package doc, limitation
		// 3); always written unspecified.
		payload = putUleb(payload, 0)
		enc := encodeExportHeader(strs, xkGlobal, g.Name, symbol, h, payload)
		entries = append(entries, built{xkGlobal, g.Name, enc})
	}

	for _, s := range shape.Structs {
		h, err := structShapeHash(s, resolve)
		if err != nil {
			return nil, fmt.Errorf("struct %s: %w", s.Name, err)
		}
		idx, err := types.intern(vir.StructType{Name: s.Name}, resolve)
		if err != nil {
			return nil, fmt.Errorf("struct %s: %w", s.Name, err)
		}
		payload := putUleb(nil, uint64(idx))
		enc := encodeExportHeader(strs, xkStruct, s.Name, "", h, payload)
		entries = append(entries, built{xkStruct, s.Name, enc})
	}

	for _, c := range shape.Consts {
		h, err := constShapeHash(c, resolve)
		if err != nil {
			return nil, fmt.Errorf("const %s: %w", c.Name, err)
		}
		typeIdx, err := types.intern(c.Type, resolve)
		if err != nil {
			return nil, fmt.Errorf("const %s: %w", c.Name, err)
		}
		payload := putUleb(nil, uint64(typeIdx))
		litb, err := encodeLiteralTyped(strs, c.Value, c.Type)
		if err != nil {
			return nil, fmt.Errorf("const %s: %w", c.Name, err)
		}
		payload = append(payload, litb...)
		enc := encodeExportHeader(strs, xkConst, c.Name, "", h, payload)
		entries = append(entries, built{xkConst, c.Name, enc})
	}

	for _, fs := range shape.FnSigs {
		h, err := fnSigShapeHash(fs, resolve)
		if err != nil {
			return nil, fmt.Errorf("fnsig %s: %w", fs.Name, err)
		}
		payload := putUleb(nil, fnSigFlags(fs))
		payload = putUleb(payload, uint64(len(fs.Params)))
		for _, p := range fs.Params {
			idx, err := types.intern(p, resolve)
			if err != nil {
				return nil, fmt.Errorf("fnsig %s: %w", fs.Name, err)
			}
			payload = putUleb(payload, uint64(idx))
		}
		retIdx, err := types.intern(fs.Ret, resolve)
		if err != nil {
			return nil, fmt.Errorf("fnsig %s: %w", fs.Name, err)
		}
		payload = putUleb(payload, uint64(retIdx))
		enc := encodeExportHeader(strs, xkFnSig, fs.Name, "", h, payload)
		entries = append(entries, built{xkFnSig, fs.Name, enc})
	}

	// Sorted by (kind, name) byte-wise (§F5.4) — reordering declarations
	// in the source is semantically inert for exported shape, so it must
	// not perturb the artifact.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind < entries[j].kind
		}
		return entries[i].name < entries[j].name
	})

	buf := putUleb(nil, uint64(len(entries)))
	for _, e := range entries {
		buf = append(buf, e.enc...)
	}
	return buf, nil
}

// ---------------------------------------------------------------------------
// XPRT decode
// ---------------------------------------------------------------------------

type rawParam struct {
	typeIdx uint32
	attr    byte // 0 none, 1 byval, 2 sret (fn params only; fnsig params have no attr)
	attrIdx uint32
}

type rawExport struct {
	kind    byte
	name    string
	symbol  string
	hash    [32]byte
	flags   uint64
	params  []rawParam
	retIdx  uint32
	typeIdx uint32
	tls     bool
	align   uint64
	literal vir.Operand
}

func decodeXprtRaw(strs []string, payload []byte) ([]rawExport, error) {
	count, n, err := readUleb(payload)
	if err != nil {
		return nil, err
	}
	out := make([]rawExport, 0, count)
	var lastKind byte
	var lastName string
	first := true
	for i := uint64(0); i < count; i++ {
		if n >= len(payload) {
			return nil, fmt.Errorf("export entry runs past end of section")
		}
		kind := payload[n]
		n++
		if kind > xkFnSig {
			return nil, fmt.Errorf("unrecognized export kind %d", kind)
		}
		nameIdx, m, err := readUleb(payload[n:])
		if err != nil {
			return nil, err
		}
		n += m
		name, err := strAt(strs, nameIdx)
		if err != nil {
			return nil, err
		}
		if !first && (kind < lastKind || (kind == lastKind && name <= lastName)) {
			return nil, fmt.Errorf("export table not sorted by (kind,name) at %q (§F5.4)", name)
		}
		lastKind, lastName, first = kind, name, false

		symIdx, m2, err := readUleb(payload[n:])
		if err != nil {
			return nil, err
		}
		n += m2
		symbol, err := strAt(strs, symIdx)
		if err != nil {
			return nil, err
		}
		if n+32 > len(payload) {
			return nil, fmt.Errorf("truncated shape_hash")
		}
		var hash [32]byte
		copy(hash[:], payload[n:n+32])
		n += 32

		e := rawExport{kind: kind, name: name, symbol: symbol, hash: hash}
		switch kind {
		case xkFn, xkFnSig:
			flags, m3, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m3
			e.flags = flags
			pcount, m4, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m4
			for p := uint64(0); p < pcount; p++ {
				typeIdx, m5, err := readUleb(payload[n:])
				if err != nil {
					return nil, err
				}
				n += m5
				rp := rawParam{typeIdx: uint32(typeIdx)}
				if kind == xkFn {
					if n >= len(payload) {
						return nil, fmt.Errorf("truncated param attr")
					}
					rp.attr = payload[n]
					n++
					attrIdx, m6, err := readUleb(payload[n:])
					if err != nil {
						return nil, err
					}
					n += m6
					rp.attrIdx = uint32(attrIdx)
				}
				e.params = append(e.params, rp)
			}
			retIdx, m7, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m7
			e.retIdx = uint32(retIdx)
		case xkGlobal:
			typeIdx, m3, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m3
			if n >= len(payload) {
				return nil, fmt.Errorf("truncated global tls flag")
			}
			e.tls = payload[n] != 0
			n++
			align, m4, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m4
			e.typeIdx = uint32(typeIdx)
			e.align = align
		case xkStruct:
			typeIdx, m3, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m3
			e.typeIdx = uint32(typeIdx)
		case xkConst:
			typeIdx, m3, err := readUleb(payload[n:])
			if err != nil {
				return nil, err
			}
			n += m3
			e.typeIdx = uint32(typeIdx)
			lit, ln, err := decodeLiteral(strs, payload[n:])
			if err != nil {
				return nil, err
			}
			e.literal = lit
			n += ln
		}
		out = append(out, e)
	}
	if n != len(payload) {
		return nil, fmt.Errorf("trailing bytes after last export entry")
	}
	return out, nil
}

func decodeXprt(strs []string, rawTypes []rawTypeEntry, moduleName, namespace string, payload []byte) (*vir.ModuleShape, error) {
	raws, err := decodeXprtRaw(strs, payload)
	if err != nil {
		return nil, err
	}

	// Struct exports' `type` field points at the STRUCT_S TYPE entry for
	// that struct — the only place a name is still attached to one
	// (package doc, limitation 1).
	structNameByIdx := map[uint32]string{}
	for _, e := range raws {
		if e.kind == xkStruct {
			structNameByIdx[e.typeIdx] = e.name
		}
	}
	types, err := materializeTypes(rawTypes, func(idx uint32) string {
		if name, ok := structNameByIdx[idx]; ok {
			return name
		}
		return "<anonymous>"
	})
	if err != nil {
		return nil, err
	}
	typeAt := func(idx uint32) (vir.Type, error) {
		if idx == 0 {
			return nil, nil
		}
		if int(idx) >= len(types) {
			return nil, fmt.Errorf("type index %d out of range", idx)
		}
		return types[idx], nil
	}

	shape := &vir.ModuleShape{Namespace: namespace, ModuleName: moduleName}
	for _, e := range raws {
		switch e.kind {
		case xkFn:
			ret, err := typeAt(e.retIdx)
			if err != nil {
				return nil, fmt.Errorf("fn %s: %w", e.name, err)
			}
			params := make([]vir.Param, len(e.params))
			for i, rp := range e.params {
				t, err := typeAt(rp.typeIdx)
				if err != nil {
					return nil, fmt.Errorf("fn %s: param %d: %w", e.name, i, err)
				}
				p := vir.Param{Type: t}
				// The real struct name behind byval/sret doesn't survive
				// structural expansion (package doc, limitation 2).
				switch rp.attr {
				case 1:
					p.ByVal = "<structural>"
				case 2:
					p.SRet = "<structural>"
				}
				params[i] = p
			}
			var attrs []vir.FunctionAttribute
			if e.flags&(1<<1) != 0 {
				attrs = append(attrs, vir.AttributeNoReturn)
			}
			if e.flags&(1<<2) != 0 {
				attrs = append(attrs, vir.AttributeReadonly)
			}
			if e.flags&(1<<3) != 0 {
				attrs = append(attrs, vir.AttributeEntry)
			}
			if e.flags&(1<<4) != 0 {
				attrs = append(attrs, vir.AttributeExternC)
			}
			shape.Fns = append(shape.Fns, vir.FnShape{
				Name: e.name, Params: params, Variadic: e.flags&1 != 0, Ret: ret, Attrs: attrs,
			})
		case xkGlobal:
			t, err := typeAt(e.typeIdx)
			if err != nil {
				return nil, fmt.Errorf("global %s: %w", e.name, err)
			}
			shape.Globals = append(shape.Globals, vir.GlobalShape{Name: e.name, Type: t, TLS: e.tls})
		case xkStruct:
			if _, err := typeAt(e.typeIdx); err != nil {
				return nil, fmt.Errorf("struct %s: %w", e.name, err)
			}
			raw := rawTypes[e.typeIdx]
			fields := make([]vir.Field, len(raw.fields))
			for i, fi := range raw.fields {
				ft, err := typeAt(fi)
				if err != nil {
					return nil, fmt.Errorf("struct %s: field %d: %w", e.name, i, err)
				}
				fields[i] = vir.Field{Name: fmt.Sprintf("_%d", i), Type: ft}
			}
			shape.Structs = append(shape.Structs, vir.StructShape{Name: e.name, Fields: fields})
		case xkConst:
			t, err := typeAt(e.typeIdx)
			if err != nil {
				return nil, fmt.Errorf("const %s: %w", e.name, err)
			}
			shape.Consts = append(shape.Consts, vir.ConstShape{Name: e.name, Type: t, Value: e.literal})
		case xkFnSig:
			ret, err := typeAt(e.retIdx)
			if err != nil {
				return nil, fmt.Errorf("fnsig %s: %w", e.name, err)
			}
			params := make([]vir.Type, len(e.params))
			for i, rp := range e.params {
				pt, err := typeAt(rp.typeIdx)
				if err != nil {
					return nil, fmt.Errorf("fnsig %s: param %d: %w", e.name, i, err)
				}
				params[i] = pt
			}
			shape.FnSigs = append(shape.FnSigs, vir.FnSigShape{Name: e.name, Params: params, Variadic: e.flags&1 != 0, Ret: ret})
		}
	}
	return shape, nil
}