// encode.go
package binary

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/vertex-language/vvm/ir/vir"
)

// Encode serializes an (assumed verified) module to .vbyte bytes, per
// file_formats.md §F2–§F4. See format.go's header comment for the handful
// of places this implementation extends or deviates from that spec.
func Encode(m *vir.Module) ([]byte, error) {
	e := newEncoder(m)

	sections := []encSection{}
	add := func(tag string, required bool, payload []byte) {
		var flags uint32
		if required {
			flags |= sectionFlagRequired
		}
		sections = append(sections, encSection{tag: tag, flags: flags, payload: payload})
	}

	modu, err := e.buildModu()
	if err != nil {
		return nil, err
	}

	var targ, asmd []byte
	if m.Target != nil {
		targ = e.buildTarg()
	}
	if m.AsmDialect != nil {
		asmd, err = e.buildAsmd()
		if err != nil {
			return nil, err
		}
	}

	stru, err := e.buildStru()
	if err != nil {
		return nil, err
	}
	fsig := e.buildFsig()
	cnst, err := e.buildCnst()
	if err != nil {
		return nil, err
	}
	glob, err := e.buildGlob()
	if err != nil {
		return nil, err
	}
	link, err := e.buildLink()
	if err != nil {
		return nil, err
	}
	extn := e.buildExtn()
	impt := e.buildImpt()
	asmb := e.buildAsmb() // pool payload finalized after functions are built (see below)
	_ = asmb

	fn, locs, asmbFinal, err := e.buildFunc()
	if err != nil {
		return nil, err
	}

	// STRT/TYPE are only knowable once every other section has been built,
	// since building them is what populates the string/type tables.
	strt := e.buildStrt()
	typ := e.buildTypeSection()

	add(tagSTRT, true, strt)
	add(tagTYPE, true, typ)
	add(tagMODU, true, modu)
	if targ != nil {
		add(tagTARG, false, targ)
	}
	if asmd != nil {
		add(tagASMD, false, asmd)
	}
	add(tagSTRU, false, stru)
	add(tagFSIG, false, fsig)
	add(tagCNST, false, cnst)
	add(tagGLOB, false, glob)
	add(tagLINK, false, link)
	add(tagEXTN, false, extn)
	add(tagIMPT, false, impt)
	add(tagASMB, false, asmbFinal)
	add(tagFUNC, false, fn)
	if len(locs) > 0 {
		sections = append(sections, encSection{tag: tagLOCS, flags: sectionFlagNonSemantic, payload: locs})
	}

	hash := semanticHash(sections)
	sections = append(sections, encSection{tag: tagHASH, flags: sectionFlagNonSemantic, payload: hash[:]})

	return assemble(sections)
}

// ---------------------------------------------------------------------------
// Container assembly (§F2.2)
// ---------------------------------------------------------------------------

type encSection struct {
	tag     string
	flags   uint32
	payload []byte
}

func assemble(sections []encSection) ([]byte, error) {
	if len(sections) > 64 {
		return nil, fmt.Errorf("vbyte: %d sections exceeds the 64-section limit (§F2.8)", len(sections))
	}
	headerAndTable := headerSize + sectionEntry*len(sections)
	offset := alignTo8(headerAndTable)

	type placed struct {
		encSection
		offset int
	}
	var placedSecs []placed
	cur := offset
	for _, s := range sections {
		placedSecs = append(placedSecs, placed{s, cur})
		cur = alignTo8(cur + len(s.payload))
	}

	buf := &bytes.Buffer{}
	// Header.
	buf.Write(vbyteMagic)
	writeU16(buf, formatMajor)
	writeU16(buf, formatMinor)
	writeU16(buf, irMajor)
	writeU16(buf, irMinor)
	writeU32(buf, 0) // flags
	writeU32(buf, uint32(len(sections)))
	buf.Write(make([]byte, 8)) // reserved

	// Section table.
	for _, p := range placedSecs {
		tagBytes := []byte(p.tag)
		if len(tagBytes) != 4 {
			return nil, fmt.Errorf("vbyte: internal error: section tag %q is not 4 bytes", p.tag)
		}
		buf.Write(tagBytes)
		writeU32(buf, p.flags)
		writeU64(buf, uint64(p.offset))
		writeU32(buf, uint32(len(p.payload)))
		writeU32(buf, uint32(len(p.payload))) // uncompressed_length == stored_length (no compression)
	}

	// Payloads, 8-byte aligned with zero padding.
	for _, p := range placedSecs {
		for buf.Len() < p.offset {
			buf.WriteByte(0)
		}
		buf.Write(p.payload)
	}
	for buf.Len()%8 != 0 {
		buf.WriteByte(0)
	}

	// Trailer.
	sum := sha256.Sum256(buf.Bytes())
	buf.Write(sum[:])
	writeU32(buf, hashAlgoSHA256)
	buf.Write(make([]byte, 4)) // reserved

	return buf.Bytes(), nil
}

func semanticHash(sections []encSection) [32]byte {
	h := sha256.New()
	var hdr [4]byte
	binary.LittleEndian.PutUint16(hdr[0:2], irMajor)
	binary.LittleEndian.PutUint16(hdr[2:4], irMinor)
	h.Write(hdr[:])
	for _, s := range sections {
		if s.flags&sectionFlagNonSemantic != 0 {
			continue
		}
		h.Write([]byte(s.tag))
		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(s.payload)))
		h.Write(lenBuf[:])
		h.Write(s.payload)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func writeU16(buf *bytes.Buffer, v uint16) { var b [2]byte; binary.LittleEndian.PutUint16(b[:], v); buf.Write(b[:]) }
func writeU32(buf *bytes.Buffer, v uint32) { var b [4]byte; binary.LittleEndian.PutUint32(b[:], v); buf.Write(b[:]) }
func writeU64(buf *bytes.Buffer, v uint64) { var b [8]byte; binary.LittleEndian.PutUint64(b[:], v); buf.Write(b[:]) }

// ---------------------------------------------------------------------------
// encoder — owns the shared STRT/TYPE tables and module-level lookup maps.
// ---------------------------------------------------------------------------

type encoder struct {
	m *vir.Module

	strIndex map[string]int // 1-based
	strList  []string

	typeIndex map[string]int // 1-based
	typeList  [][]byte // each entry's raw kind+payload bytes

	structPos map[string]int
	fnSigPos  map[string]int
	constPos  map[string]int
	globalPos map[string]int
	fnPos     map[string]int
	externPos map[string]int // flattened across extern groups
	importPos map[string]int

	asmPool    [][]byte // finalized asm-block payloads, in first-use order
	asmPoolKey map[int]int // identity of *vir.AsmBlock (by pointer, via index trick) -> pool index
}

func newEncoder(m *vir.Module) *encoder {
	e := &encoder{
		m:          m,
		strIndex:   map[string]int{},
		typeIndex:  map[string]int{},
		structPos:  map[string]int{},
		fnSigPos:   map[string]int{},
		constPos:   map[string]int{},
		globalPos:  map[string]int{},
		fnPos:      map[string]int{},
		externPos:  map[string]int{},
		importPos:  map[string]int{},
		asmPoolKey: map[int]int{},
	}
	for i, s := range m.Structs {
		e.structPos[s.Name] = i
	}
	for i, s := range m.FunctionSignatures {
		e.fnSigPos[s.Name] = i
	}
	for i, c := range m.Constants {
		e.constPos[c.Name] = i
	}
	for i, g := range m.Globals {
		e.globalPos[g.Name] = i
	}
	for i, f := range m.Functions {
		e.fnPos[f.Name] = i
	}
	idx := 0
	for _, g := range m.Externs {
		for _, f := range g.Functions {
			e.externPos[f.Name] = idx
			idx++
		}
	}
	for i, imp := range m.Imports {
		e.importPos[imp.Path] = i
	}
	return e
}

func (e *encoder) str(s string) int {
	if s == "" {
		return 0
	}
	if idx, ok := e.strIndex[s]; ok {
		return idx
	}
	e.strList = append(e.strList, s)
	idx := len(e.strList)
	e.strIndex[s] = idx
	return idx
}

func (e *encoder) resolveDecl(name string) (kind byte, index int, ok bool) {
	if i, ok := e.structPos[name]; ok {
		return declStruct, i, true
	}
	if i, ok := e.fnSigPos[name]; ok {
		return declFnSig, i, true
	}
	if i, ok := e.constPos[name]; ok {
		return declConst, i, true
	}
	if i, ok := e.globalPos[name]; ok {
		return declGlobal, i, true
	}
	if i, ok := e.fnPos[name]; ok {
		return declFn, i, true
	}
	if i, ok := e.externPos[name]; ok {
		return declExtern, i, true
	}
	return 0, 0, false
}

// typ interns t and returns its 1-based TYPE-table index (0 for nil/absent).
func (e *encoder) typ(t vir.Type) int {
	if t == nil {
		return 0
	}
	var key string
	var kind byte
	var payload []byte
	switch x := t.(type) {
	case vir.VoidType:
		kind, key = typeKindVoid, "void"
	case vir.IntType:
		kind = typeKindInt
		key = fmt.Sprintf("i%d", x.Bits)
		payload = ulebBytes(uint64(x.Bits))
	case vir.FloatType:
		kind = typeKindFloat
		key = fmt.Sprintf("f%d", x.Bits)
		payload = []byte{byte(x.Bits)}
	case vir.PtrType:
		kind, key = typeKindPtr, "ptr"
	case vir.ValistType:
		kind, key = typeKindValist, "valist"
	case vir.VecType:
		elemIdx := e.typ(x.Elem)
		kind = typeKindVec
		key = fmt.Sprintf("vec[%d,%d]", elemIdx, x.Len)
		payload = append(ulebBytes(uint64(elemIdx)), ulebBytes(uint64(x.Len))...)
	case vir.ArrayType:
		elemIdx := e.typ(x.Elem)
		kind = typeKindArray
		key = fmt.Sprintf("array[%d,%d]", elemIdx, x.Len)
		payload = append(ulebBytes(uint64(elemIdx)), ulebBytes(uint64(x.Len))...)
	case vir.StructType:
		if x.Import != "" {
			impIdx := e.str(x.Import)
			nameIdx := e.str(x.Name)
			kind = typeKindStructN
			key = fmt.Sprintf("struct/import/%d/%d", impIdx, nameIdx)
			payload = append([]byte{structOriginImport}, append(ulebBytes(uint64(impIdx)), ulebBytes(uint64(nameIdx))...)...)
		} else {
			pos := e.structPos[x.Name]
			kind = typeKindStructN
			key = fmt.Sprintf("struct/local/%d", pos)
			payload = append([]byte{structOriginLocal}, ulebBytes(uint64(pos))...)
		}
	default:
		panic(fmt.Sprintf("vbyte: unknown Type %T", t))
	}
	if idx, ok := e.typeIndex[key]; ok {
		return idx
	}
	entry := append([]byte{kind}, payload...)
	e.typeList = append(e.typeList, entry)
	idx := len(e.typeList)
	e.typeIndex[key] = idx
	return idx
}

func (e *encoder) buildStrt() []byte {
	w := &bytesW{}
	w.uleb(uint64(len(e.strList)))
	for _, s := range e.strList {
		w.uleb(uint64(len(s)))
		w.raw([]byte(s))
	}
	return w.buf.Bytes()
}

func (e *encoder) buildTypeSection() []byte {
	w := &bytesW{}
	w.uleb(uint64(len(e.typeList)))
	for _, entry := range e.typeList {
		w.raw(entry)
	}
	return w.buf.Bytes()
}

// ---------------------------------------------------------------------------
// secWriter — a section's payload buffer with access to the shared tables.
// ---------------------------------------------------------------------------

type bytesW struct{ buf bytes.Buffer }

func (w *bytesW) raw(p []byte)  { w.buf.Write(p) }
func (w *bytesW) b(v byte)      { w.buf.WriteByte(v) }
func (w *bytesW) bool(v bool)   { if v { w.b(1) } else { w.b(0) } }
func (w *bytesW) uleb(v uint64) { w.raw(ulebBytes(v)) }
func (w *bytesW) sleb(v int64)  { w.raw(slebBytes(v)) }
func (w *bytesW) u32(v uint32)  { var b [4]byte; binary.LittleEndian.PutUint32(b[:], v); w.raw(b[:]) }
func (w *bytesW) u64(v uint64)  { var b [8]byte; binary.LittleEndian.PutUint64(b[:], v); w.raw(b[:]) }

type secWriter struct {
	*encoder
	bytesW
}

func (e *encoder) newSec() *secWriter { return &secWriter{encoder: e} }

func (w *secWriter) str(s string)  { w.uleb(uint64(w.encoder.str(s))) }
func (w *secWriter) typ(t vir.Type) { w.uleb(uint64(w.encoder.typ(t))) }

// value encodes a literal-ish Operand (INT/FLOAT/STRING/NULL/BOOL/VECTOR).
// ctxType is best-effort context for choosing a float width; nil defaults to 64.
func (w *secWriter) value(o vir.Operand, ctxType vir.Type) error {
	switch o.Kind {
	case vir.OperandInt:
		w.b(valueTagInt)
		w.sleb(o.Int)
	case vir.OperandFloat:
		w.b(valueTagFloat)
		width := floatWidth(ctxType)
		w.b(width)
		writeFloatBits(&w.bytesW, o.Float, width)
	case vir.OperandString:
		w.b(valueTagString)
		w.str(o.Str)
	case vir.OperandNull:
		w.b(valueTagNull)
	case vir.OperandBool:
		w.b(valueTagBool)
		w.bool(o.Bool)
	case vir.OperandVector:
		w.b(valueTagVector)
		w.uleb(uint64(len(o.Vector)))
		for _, e := range o.Vector {
			w.sleb(e)
		}
	default:
		return fmt.Errorf("vbyte: operand kind %d is not a literal value", o.Kind)
	}
	return nil
}

func floatWidth(t vir.Type) byte {
	if ft, ok := t.(vir.FloatType); ok {
		return byte(ft.Bits)
	}
	return 64
}

func writeFloatBits(w *bytesW, v float64, width byte) {
	switch width {
	case 16:
		w.raw(f16Bytes(v)) // simplified conversion; see format.go deviation #9
	case 32:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(float32(v)))
		w.raw(b[:])
	default:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		w.raw(b[:])
	}
}

func f16Bytes(v float64) []byte {
	// TODO: correctly-rounded float32->float16; this truncates the mantissa.
	bits32 := math.Float32bits(float32(v))
	sign := uint16((bits32 >> 16) & 0x8000)
	exp := int32((bits32>>23)&0xFF) - 127 + 15
	mant := uint16((bits32 >> 13) & 0x3FF)
	var bits16 uint16
	switch {
	case exp <= 0:
		bits16 = sign
	case exp >= 0x1F:
		bits16 = sign | 0x7C00
	default:
		bits16 = sign | uint16(exp)<<10 | mant
	}
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], bits16)
	return b[:]
}

// ---------------------------------------------------------------------------
// uleb/sleb (§F2.1) — minimal-length LEB128
// ---------------------------------------------------------------------------

func ulebBytes(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
		} else {
			out = append(out, b)
			break
		}
	}
	return out
}

func slebBytes(v int64) []byte {
	var out []byte
	more := true
	for more {
		b := byte(v & 0x7F)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			more = false
		} else {
			b |= 0x80
		}
		out = append(out, b)
	}
	return out
}

// ---------------------------------------------------------------------------
// MODU / TARG / ASMD (§F4.1)
// ---------------------------------------------------------------------------

func (e *encoder) buildModu() ([]byte, error) {
	if e.m.Name == "" {
		return nil, fmt.Errorf("vbyte: module has no name (§2.1)")
	}
	w := e.newSec()
	w.str(e.m.Name)
	w.str(e.m.Namespace)
	return w.buf.Bytes(), nil
}

func (e *encoder) buildTarg() []byte {
	w := e.newSec()
	t := e.m.Target
	w.str(t.Arch)
	w.str(t.OS)
	w.str(t.ABI)
	w.uleb(uint64(len(t.Tiers)))
	for _, tier := range t.Tiers {
		w.str(tier)
	}
	return w.buf.Bytes()
}

func (e *encoder) buildAsmd() ([]byte, error) {
	code, ok := asmDialectCode[*e.m.AsmDialect]
	if !ok {
		return nil, fmt.Errorf("vbyte: unknown asm dialect %q", *e.m.AsmDialect)
	}
	w := e.newSec()
	w.b(code)
	return w.buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// STRU / FSIG / CNST / GLOB (§F4.2)
// ---------------------------------------------------------------------------

func (e *encoder) buildStru() ([]byte, error) {
	w := e.newSec()
	w.uleb(uint64(len(e.m.Structs)))
	for _, s := range e.m.Structs {
		w.str(s.Name)
		var flags byte
		if s.Export {
			flags |= declFlagExport
		}
		w.b(flags)
		w.uleb(uint64(len(s.Fields)))
		for _, f := range s.Fields {
			w.str(f.Name)
			w.typ(f.Type)
		}
	}
	return w.buf.Bytes(), nil
}

func (e *encoder) buildFsig() []byte {
	w := e.newSec()
	w.uleb(uint64(len(e.m.FunctionSignatures)))
	for _, sig := range e.m.FunctionSignatures {
		w.str(sig.Name)
		var flags byte
		if sig.Export {
			flags |= declFlagExport
		}
		if sig.Variadic {
			flags |= declFlagVariadic
		}
		w.b(flags)
		w.uleb(uint64(len(sig.Params)))
		for _, p := range sig.Params {
			w.typ(p)
		}
		w.typ(sig.Ret)
	}
	return w.buf.Bytes()
}

func (e *encoder) buildCnst() ([]byte, error) {
	w := e.newSec()
	w.uleb(uint64(len(e.m.Constants)))
	for _, c := range e.m.Constants {
		w.str(c.Name)
		var flags byte
		if c.Export {
			flags |= declFlagExport
		}
		w.b(flags)
		w.typ(c.Type)
		if err := w.value(c.Value, c.Type); err != nil {
			return nil, fmt.Errorf("vbyte: const %s: %w", c.Name, err)
		}
	}
	return w.buf.Bytes(), nil
}

func (e *encoder) buildGlob() ([]byte, error) {
	w := e.newSec()
	w.uleb(uint64(len(e.m.Globals)))
	for _, g := range e.m.Globals {
		w.str(g.Name)
		var flags byte
		if g.Export {
			flags |= declFlagExport
		}
		if g.TLS {
			flags |= declFlagTLS
		}
		w.b(flags)
		w.typ(g.Type)
		w.uleb(uint64(g.Align))
		if err := w.constInit(g.Init, g.Type); err != nil {
			return nil, fmt.Errorf("vbyte: global %s: %w", g.Name, err)
		}
	}
	return w.buf.Bytes(), nil
}

func (w *secWriter) constInit(init vir.ConstInit, t vir.Type) error {
	switch x := init.(type) {
	case nil:
		return fmt.Errorf("missing initializer")
	case vir.InitZero:
		w.b(initTagZero)
	case vir.InitLiteral:
		w.b(initTagLiteral)
		return w.value(x.Value, t)
	case vir.InitAddressOf:
		w.b(initTagAddr)
		kind, idx, ok := w.encoder.resolveDecl(x.Name)
		if !ok {
			return fmt.Errorf("addr references unknown name %q", x.Name)
		}
		w.b(kind)
		w.uleb(uint64(idx))
	case vir.InitByteString:
		w.b(initTagByteString)
		w.uleb(uint64(len(x.Data)))
		w.raw(x.Data)
	case vir.InitAggregate:
		w.b(initTagAggregate)
		w.uleb(uint64(len(x.Elems)))
		for i, elem := range x.Elems {
			et := w.encoder.aggregateElemType(t, i)
			if err := w.constInit(elem, et); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown ConstInit %T", init)
	}
	return nil
}

func (e *encoder) aggregateElemType(t vir.Type, i int) vir.Type {
	switch x := t.(type) {
	case vir.ArrayType:
		return x.Elem
	case vir.StructType:
		if x.Import == "" {
			for _, s := range e.m.Structs {
				if s.Name == x.Name && i < len(s.Fields) {
					return s.Fields[i].Type
				}
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// LINK / EXTN / IMPT (§F4.2)
// ---------------------------------------------------------------------------

func (e *encoder) buildLink() ([]byte, error) {
	w := e.newSec()
	w.uleb(uint64(len(e.m.Links)))
	for _, l := range e.m.Links {
		code, ok := linkKindCode[l.Kind]
		if !ok {
			return nil, fmt.Errorf("vbyte: unknown link kind %q", l.Kind)
		}
		w.b(code)
		w.str(l.Name)
	}
	return w.buf.Bytes(), nil
}

func (e *encoder) buildExtn() []byte {
	w := e.newSec()
	w.uleb(uint64(len(e.m.Externs)))
	for _, g := range e.m.Externs {
		w.str(g.Dependency)
		w.uleb(uint64(len(g.Functions)))
		for _, f := range g.Functions {
			w.str(f.Name)
			w.uleb(uint64(len(f.Params)))
			for _, p := range f.Params {
				w.param(p)
			}
			w.bool(f.Variadic)
			w.typ(f.Ret)
			w.uleb(encodeAttrBits(f.Attrs))
		}
	}
	return w.buf.Bytes()
}

func (w *secWriter) param(p vir.Param) {
	w.str(p.Name)
	w.typ(p.Type)
	switch {
	case p.ByVal != "":
		w.b(paramAttrByVal)
		w.uleb(uint64(w.encoder.structPos[p.ByVal]))
	case p.SRet != "":
		w.b(paramAttrSRet)
		w.uleb(uint64(w.encoder.structPos[p.SRet]))
	default:
		w.b(paramAttrNone)
		w.uleb(0)
	}
}

func (e *encoder) buildImpt() []byte {
	w := e.newSec()
	w.uleb(uint64(len(e.m.Imports)))
	for _, imp := range e.m.Imports {
		w.str(imp.Path)
	}
	return w.buf.Bytes()
}

// ---------------------------------------------------------------------------
// ASMB pool (§F4.4, deviation #4: structural code, not re-parsed text)
// ---------------------------------------------------------------------------

// internAsmBlock encodes a (already fully realized) asm block into the pool
// and returns its index, deduping by content.
func (e *encoder) internAsmBlock(a *vir.AsmBlock, locals map[string]int) (int, error) {
	w := e.newSec()

	// Coalesce all clobber bindings into one (deviation #8); keep in/out order.
	var clobberRegs []string
	w.uleb(0) // placeholder count, patched below
	bindingBuf := &bytesW{}
	count := 0
	for _, b := range a.Bindings {
		switch b.Kind {
		case vir.BindingIn, vir.BindingOut:
			code := bindingKindCode[b.Kind]
			bindingBuf.b(code)
			bindingBuf.uleb(uint64(len(b.Register)))
			bindingBuf.raw([]byte(b.Register))
			idx, ok := locals[b.Ident]
			if !ok {
				return 0, fmt.Errorf("asm binding references unknown local %q", b.Ident)
			}
			bindingBuf.uleb(uint64(idx))
			count++
		case vir.BindingClobber:
			clobberRegs = append(clobberRegs, b.Registers...)
		}
	}
	for _, reg := range clobberRegs {
		bindingBuf.b(bindingKindCode[vir.BindingClobber])
		bindingBuf.uleb(uint64(len(reg)))
		bindingBuf.raw([]byte(reg))
		bindingBuf.uleb(0)
		count++
	}

	final := &bytesW{}
	final.uleb(uint64(count))
	final.raw(bindingBuf.buf.Bytes())
	final.uleb(uint64(len(a.Code)))
	for _, line := range a.Code {
		if err := writeAsmCodeLine(final, line); err != nil {
			return 0, err
		}
	}

	payload := final.buf.Bytes()
	key := string(payload)
	for i, existing := range e.asmPool {
		if string(existing) == key {
			return i, nil
		}
	}
	e.asmPool = append(e.asmPool, payload)
	return len(e.asmPool) - 1, nil
}

func writeAsmCodeLine(w *bytesW, line vir.AsmCodeLine) error {
	if line.LabelDeclaration != "" {
		w.b(1)
		w.uleb(uint64(len(line.LabelDeclaration)))
		w.raw([]byte(line.LabelDeclaration))
		return nil
	}
	w.b(0)
	w.uleb(uint64(len(line.Mnemonic)))
	w.raw([]byte(line.Mnemonic))
	w.uleb(uint64(len(line.Operands)))
	for _, op := range line.Operands {
		if err := writeAsmOperand(w, op); err != nil {
			return err
		}
	}
	return nil
}

func writeAsmOperand(w *bytesW, op vir.AsmOperand) error {
	switch op.Kind {
	case vir.AsmOperandKindRegister:
		w.b(asmOperandKindRegister)
		w.uleb(uint64(len(op.Register)))
		w.raw([]byte(op.Register))
	case vir.AsmOperandKindImmediate:
		w.b(asmOperandKindImmediate)
		sw := &secWriter{bytesW: *w}
		if err := sw.value(op.Immediate, nil); err != nil {
			return err
		}
		*w = sw.bytesW
	case vir.AsmOperandKindMemory:
		w.b(asmOperandKindMemory)
		w.uleb(uint64(len(op.Memory)))
		w.raw([]byte(op.Memory))
	case vir.AsmOperandKindLabel:
		w.b(asmOperandKindLabel)
		w.uleb(uint64(len(op.Label)))
		w.raw([]byte(op.Label))
	default:
		return fmt.Errorf("unknown AsmOperandKind %q", op.Kind)
	}
	return nil
}

func (e *encoder) buildAsmb() []byte { return nil } // finalized in buildFunc; see buildFunc's return

// ---------------------------------------------------------------------------
// FUNC + LOCS (§F4.3, §F4.5)
// ---------------------------------------------------------------------------

type locEntry struct {
	funcIdx, blockIdx, instIdx int
	file                       string
	line, col                  int
}

func (e *encoder) buildFunc() (funcSection, locSection, asmbSection []byte, err error) {
	fnW := e.newSec()
	fnW.uleb(uint64(len(e.m.Functions)))

	var allLocs []locEntry

	for fi, f := range e.m.Functions {
		body := &bytesW{}
		locals, localTypes, err := e.buildLocalTable(f)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("vbyte: fn %s: %w", f.Name, err)
		}

		body.str(f.Name)
		body.uleb(encodeFuncFlags(f))
		body.uleb(uint64(len(f.Params)))
		for _, p := range f.Params {
			sw := &secWriter{encoder: e, bytesW: *body}
			sw.param(p)
			*body = sw.bytesW
		}
		typW := &secWriter{encoder: e}
		typW.typ(f.Ret)
		body.raw(typW.buf.Bytes())

		body.uleb(uint64(len(locals)))
		for i, name := range locals {
			sw := &secWriter{encoder: e, bytesW: *body}
			sw.str(name)
			sw.typ(localTypes[i])
			*body = sw.bytesW
		}

		blocks := f.AllBlocks()
		body.uleb(uint64(len(blocks)))

		localIdx := map[string]int{}
		for i, n := range locals {
			localIdx[n] = i
		}

		for bi, b := range blocks {
			blockBuf := &bytesW{}
			sw := &secWriter{encoder: e}
			if b.Label == "" {
				sw.uleb(0)
			} else {
				sw.str(b.Label)
			}
			blockBuf.raw(sw.buf.Bytes())

			instBuf := &bytesW{}
			instCount := 0
			for _, ln := range b.Lines {
				switch {
				case ln.Instruction != nil && ln.Instruction.Op == vir.OpLoc:
					args := ln.Instruction.Args
					file := ""
					line := 0
					col := 0
					if len(args) > 0 {
						file = args[0].Str
					}
					if len(args) > 1 {
						line = int(args[1].Int)
					}
					if len(args) > 2 {
						col = int(args[2].Int)
					}
					allLocs = append(allLocs, locEntry{fi, bi, instCount, file, line, col})
				case ln.Instruction != nil:
					if err := e.writeInst(instBuf, f, ln.Instruction, localIdx); err != nil {
						return nil, nil, nil, fmt.Errorf("vbyte: fn %s: %w", f.Name, err)
					}
					instCount++
				case ln.Asm != nil:
					idx, err := e.internAsmBlock(ln.Asm, localIdx)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("vbyte: fn %s: %w", f.Name, err)
					}
					instBuf.uleb(pseudoOpcodeAsmBlock)
					instBuf.uleb(0) // result_type absent
					instBuf.uleb(0) // result_local none
					instBuf.uleb(1) // operand_count
					instBuf.b(operandAsm)
					instBuf.uleb(uint64(idx))
					instBuf.uleb(0) // align
					instCount++
				}
			}

			sw2 := &secWriter{encoder: e}
			sw2.uleb(uint64(instCount))
			blockBuf.raw(sw2.buf.Bytes())
			blockBuf.raw(instBuf.buf.Bytes())

			termBuf := &bytesW{}
			labelIdx := map[string]int{}
			for i, bb := range f.Blocks {
				labelIdx[bb.Label] = i + 1 // +1 since block 0 is the entry block
			}
			if err := e.writeTerm(termBuf, f, b.Term, localIdx, labelIdx); err != nil {
				return nil, nil, nil, fmt.Errorf("vbyte: fn %s: %w", f.Name, err)
			}
			blockBuf.raw(termBuf.buf.Bytes())

			body.raw(blockBuf.buf.Bytes())
		}

		lenPrefixed := &bytesW{}
		lenPrefixed.uleb(uint64(body.buf.Len()))
		lenPrefixed.raw(body.buf.Bytes())
		fnW.raw(lenPrefixed.buf.Bytes())
	}

	// Delta-encode LOCS (§F4.5, deviation #5: sleb for block/inst deltas).
	sort.SliceStable(allLocs, func(i, j int) bool {
		a, b := allLocs[i], allLocs[j]
		if a.funcIdx != b.funcIdx {
			return a.funcIdx < b.funcIdx
		}
		if a.blockIdx != b.blockIdx {
			return a.blockIdx < b.blockIdx
		}
		return a.instIdx < b.instIdx
	})
	locW := e.newSec()
	locW.uleb(uint64(len(allLocs)))
	pf, pb, pi := 0, 0, 0
	for _, l := range allLocs {
		locW.uleb(uint64(l.funcIdx - pf))
		locW.sleb(int64(l.blockIdx - pb))
		locW.sleb(int64(l.instIdx - pi))
		locW.str(l.file)
		locW.uleb(uint64(l.line))
		locW.uleb(uint64(l.col))
		pf, pb, pi = l.funcIdx, l.blockIdx, l.instIdx
	}

	asmbW := e.newSec()
	asmbW.uleb(uint64(len(e.asmPool)))
	for _, blk := range e.asmPool {
		asmbW.raw(blk)
	}

	return fnW.buf.Bytes(), locW.buf.Bytes(), asmbW.buf.Bytes(), nil
}

type funcSection = []byte
type locSection = []byte

// buildLocalTable assigns local indices: params first (0..n-1), then every
// new instruction result in textual order, matching the Join Convention
// (§4.3) the verifier already checked.
func (e *encoder) buildLocalTable(f *vir.Function) (names []string, types []vir.Type, err error) {
	seen := map[string]bool{}
	for _, p := range f.Params {
		names = append(names, p.Name)
		types = append(types, p.Type)
		seen[p.Name] = true
	}
	for _, b := range f.AllBlocks() {
		for _, ln := range b.Lines {
			if ln.Instruction != nil {
				inst := ln.Instruction
				if inst.Op == vir.OpLoc || inst.Result == "" || seen[inst.Result] {
					continue
				}
				rt, ok := e.localResultType(f, inst)
				if !ok {
					return nil, nil, fmt.Errorf("cannot determine result type of %q (op %s)", inst.Result, inst.Op)
				}
				names = append(names, inst.Result)
				types = append(types, rt)
				seen[inst.Result] = true
			}
			if ln.Asm != nil {
				for _, bind := range ln.Asm.Bindings {
					if bind.Kind == vir.BindingOut && !seen[bind.Ident] {
						names = append(names, bind.Ident)
						types = append(types, vir.Ptr) // best-effort; asm out-binding width isn't known here
						seen[bind.Ident] = true
					}
				}
			}
		}
	}
	return names, types, nil
}

// localResultType mirrors vir.Verify's resultType logic closely enough to
// recover the type the verifier already fixed for inst.Result. Since Encode
// assumes the module already passed Verify, a "not ok" here indicates
// either an unverified module or a call-return-type this package doesn't
// resolve (cross-module calls — left for when .vmeta/Stage A wiring lands).
func (e *encoder) localResultType(f *vir.Function, i *vir.Instruction) (vir.Type, bool) {
	switch i.Op {
	case vir.OpMin, vir.OpMax, vir.OpAlloca, vir.OpSyscall, vir.OpVaArg:
		if i.Suffix == nil {
			return nil, false
		}
		return i.Suffix, true
	case vir.OpCall:
		if i.Sig != "" {
			for _, s := range e.m.FunctionSignatures {
				if s.Name == i.Sig {
					return s.Ret, true
				}
			}
			return nil, false
		}
		if len(i.Args) == 0 || i.Args[0].Kind != vir.OperandIdent || i.Args[0].IsQualified() {
			return nil, false // cross-module call return type needs Stage A shapes; not resolved here yet
		}
		callee := i.Args[0].Ident
		for _, g := range e.m.Externs {
			for _, ef := range g.Functions {
				if ef.Name == callee {
					return ef.Ret, true
				}
			}
		}
		for _, fn := range e.m.Functions {
			if fn.Name == callee {
				return fn.Ret, true
			}
		}
		return nil, false
	case vir.OpVaStart, vir.OpVaEnd, vir.OpStore, vir.OpStoreVol, vir.OpMemcopy, vir.OpMemmove,
		vir.OpMemset, vir.OpAtomicStore, vir.OpFence, vir.OpMaskedStore, vir.OpScatter, vir.OpPrefetch:
		return vir.Void, true
	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge, vir.OpUlt, vir.OpUgt,
		vir.OpUle, vir.OpUge, vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe,
		vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO, vir.OpUMulO, vir.OpSMulO:
		if vt, ok := i.Suffix.(vir.VecType); ok {
			return vir.VecType{Elem: vir.I1, Len: vt.Len}, true
		}
		return vir.I1, true
	case vir.OpExtract, vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax, vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		if vt, ok := i.Suffix.(vir.VecType); ok {
			return vt.Elem, true
		}
		return nil, false
	default:
		if i.Suffix == nil {
			return nil, false
		}
		return i.Suffix, true
	}
}

// ---------------------------------------------------------------------------
// Instructions / operands / terminators (§F4.3)
// ---------------------------------------------------------------------------

func (e *encoder) writeInst(w *bytesW, f *vir.Function, i *vir.Instruction, locals map[string]int) error {
	num, ok := opcodeNumber[i.Op]
	if !ok {
		return fmt.Errorf("opcode %s has no assigned wire number", i.Op)
	}
	w.uleb(num)

	sw := &secWriter{encoder: e, bytesW: *w}
	sw.typ(i.Suffix)
	*w = sw.bytesW

	if i.Result != "" {
		idx, ok := locals[i.Result]
		if !ok {
			return fmt.Errorf("result %q has no assigned local index", i.Result)
		}
		w.uleb(uint64(idx) + 1)
	} else {
		w.uleb(0)
	}

	w.uleb(uint64(len(i.Args)))
	for argIdx, a := range i.Args {
		if err := e.writeOperand(w, i, argIdx, a, locals); err != nil {
			return err
		}
	}
	w.uleb(uint64(i.Align))
	return nil
}

func (e *encoder) writeOperand(w *bytesW, i *vir.Instruction, argIdx int, o vir.Operand, locals map[string]int) error {
	// field.ptr's third argument names a struct field, not a module-level
	// or local identifier (§9.24) — encode it as a bare string.
	if i.Op == vir.OpField && argIdx == 2 && o.Kind == vir.OperandIdent {
		w.b(operandLiteral)
		sw := &secWriter{encoder: e, bytesW: *w}
		if err := sw.value(vir.StringLiteral(o.Ident), nil); err != nil {
			return err
		}
		*w = sw.bytesW
		return nil
	}

	switch o.Kind {
	case vir.OperandIdent:
		if o.Qualifier != "" {
			w.b(operandImport)
			idx, ok := e.importPos[o.Qualifier]
			if !ok {
				return fmt.Errorf("operand references undeclared import %q", o.Qualifier)
			}
			w.uleb(uint64(idx))
			sw := &secWriter{encoder: e, bytesW: *w}
			sw.str(o.Ident)
			*w = sw.bytesW
			return nil
		}
		if idx, ok := locals[o.Ident]; ok {
			w.b(operandLocal)
			w.uleb(uint64(idx))
			return nil
		}
		kind, idx, ok := e.resolveDecl(o.Ident)
		if !ok {
			return fmt.Errorf("identifier %q is neither a local nor a known declaration", o.Ident)
		}
		w.b(operandDecl)
		w.b(kind)
		w.uleb(uint64(idx))
	case vir.OperandType:
		w.b(operandType)
		sw := &secWriter{encoder: e, bytesW: *w}
		sw.typ(o.Type)
		*w = sw.bytesW
	case vir.OperandOrdering:
		w.b(operandOrdering)
		code, ok := orderingCodes[o.Ordering]
		if !ok {
			return fmt.Errorf("unknown ordering %q", o.Ordering)
		}
		w.b(code)
	default:
		w.b(operandLiteral)
		sw := &secWriter{encoder: e, bytesW: *w}
		if err := sw.value(o, i.Suffix); err != nil {
			return err
		}
		*w = sw.bytesW
	}
	return nil
}

func (e *encoder) writeTerm(w *bytesW, f *vir.Function, t vir.Terminator, locals, labels map[string]int) error {
	switch x := t.(type) {
	case vir.Branch:
		w.uleb(termBranch)
		w.uleb(uint64(labels[x.Label]))
	case vir.BranchIf:
		w.uleb(termBranchIf)
		if err := e.writeOperand(w, &vir.Instruction{}, -1, x.Cond, locals); err != nil {
			return err
		}
		w.uleb(uint64(labels[x.Then]))
		w.uleb(uint64(labels[x.Else]))
	case vir.Switch:
		w.uleb(termSwitch)
		if err := e.writeOperand(w, &vir.Instruction{}, -1, x.Value, locals); err != nil {
			return err
		}
		w.uleb(uint64(labels[x.Default]))
		w.uleb(uint64(len(x.Cases)))
		for _, c := range x.Cases {
			w.sleb(c.Value)
			w.uleb(uint64(labels[c.Label]))
		}
	case vir.Return:
		w.uleb(termReturn)
		if x.Value != nil {
			w.b(1)
			if err := e.writeOperand(w, &vir.Instruction{}, -1, *x.Value, locals); err != nil {
				return err
			}
		} else {
			w.b(0)
		}
	case vir.TailCall:
		w.uleb(termTailCall)
		if x.Sig != "" {
			w.b(tailCallIndirect)
			idx, ok := e.fnSigPos[x.Sig]
			if !ok {
				return fmt.Errorf("tailcall references unknown fnsig %q", x.Sig)
			}
			w.uleb(uint64(idx))
		} else {
			w.b(tailCallDirect)
			kind, idx, ok := e.resolveDecl(x.Callee)
			if !ok {
				return fmt.Errorf("tailcall references unknown callee %q", x.Callee)
			}
			w.b(kind)
			w.uleb(uint64(idx))
		}
		w.uleb(uint64(len(x.Args)))
		for _, a := range x.Args {
			if err := e.writeOperand(w, &vir.Instruction{}, -1, a, locals); err != nil {
				return err
			}
		}
	case vir.Trap:
		w.uleb(termTrap)
	case vir.Unreachable:
		w.uleb(termUnreachable)
	default:
		return fmt.Errorf("unknown Terminator %T", t)
	}
	return nil
}