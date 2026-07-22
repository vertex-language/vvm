// decode.go
package binary

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Decode parses .vbyte bytes into an unverified *vir.Module. Callers must
// run vir.Verify before handing the module to anything downstream (README
// invariant 3) — decoding checks framing/container integrity, not
// language-level semantics.
func Decode(data []byte) (m *vir.Module, err error) {
	defer func() {
		if p := recover(); p != nil {
			if de, ok := p.(decodeErr); ok {
				m, err = nil, error(de)
				return
			}
			panic(p)
		}
	}()

	d := &decoder{data: data}
	d.readContainer()
	return d.build(), nil
}

type decodeErr error

func fail(format string, args ...any) {
	panic(decodeErr(fmt.Errorf("vbyte: "+format, args...)))
}

// ---------------------------------------------------------------------------
// Container / section table (§F2.2, §F2.3)
// ---------------------------------------------------------------------------

type rawSection struct {
	tag           string
	flags         uint32
	offset        int
	storedLen     int
	uncompressLen int
}

type decoder struct {
	data     []byte
	sections []rawSection
	byTag    map[string][]byte

	strList  []string
	typeList []vir.Type

	// resolved declaration lists, needed to satisfy DECL/IMPORT operand
	// references while functions are decoded.
	m *vir.Module
}

func (d *decoder) readContainer() {
	if len(d.data) < headerSize || !bytes.HasPrefix(d.data, vbyteMagic) {
		fail("bad magic or truncated header")
	}
	pos := len(vbyteMagic)
	fMajor := readU16(d.data, pos)
	pos += 2
	fMinor := readU16(d.data, pos)
	pos += 2
	_ = fMinor
	if fMajor != formatMajor {
		fail("unsupported format_major %d (have %d)", fMajor, formatMajor)
	}
	irMaj := readU16(d.data, pos)
	pos += 2
	if irMaj != irMajor {
		fail("unsupported ir_major %d (have %d)", irMaj, irMajor)
	}
	pos += 2 // ir_minor, decode-permissive (§F7.1)
	pos += 4 // flags
	sectionCount := int(readU32(d.data, pos))
	pos += 4
	pos += 8 // reserved

	if pos != headerSize {
		fail("internal error: header size mismatch")
	}
	if len(d.data) < headerSize+sectionEntry*sectionCount+trailerSize {
		fail("truncated: section table or trailer missing")
	}

	d.byTag = map[string][]byte{}
	seenOrder := []string{}
	for i := 0; i < sectionCount; i++ {
		base := headerSize + i*sectionEntry
		tag := string(d.data[base : base+4])
		flags := readU32(d.data, base+4)
		offset := int(readU64(d.data, base+8))
		storedLen := int(readU32(d.data, base+16))
		uncompressLen := int(readU32(d.data, base+20))
		if flags&sectionFlagZSTD != 0 {
			fail("section %q: ZSTD compression not supported by this reader", tag)
		}
		if offset+storedLen > len(d.data) {
			fail("section %q: payload out of bounds", tag)
		}
		d.sections = append(d.sections, rawSection{tag, flags, offset, storedLen, uncompressLen})
		if isKnownTag(tag) {
			d.byTag[tag] = d.data[offset : offset+storedLen]
		} else if flags&sectionFlagRequired != 0 {
			fail("unrecognized required section %q", tag)
		}
		seenOrder = append(seenOrder, tag)
	}
	if err := checkSectionOrder(seenOrder); err != nil {
		fail("%s", err)
	}

	trailerStart := len(d.data) - trailerSize
	contentHash := d.data[trailerStart : trailerStart+32]
	computed := sha256.Sum256(d.data[:trailerStart])
	if !bytes.Equal(contentHash, computed[:]) {
		fail("content hash mismatch (corrupt or truncated file)")
	}
}

func isKnownTag(tag string) bool {
	for _, t := range sectionOrder {
		if t == tag {
			return true
		}
	}
	return false
}

// checkSectionOrder enforces §F4.1: known sections, when present, must
// appear in exactly the canonical order (subsequence check — optional
// sections may be absent, but present ones can't be reordered or duplicated).
func checkSectionOrder(seen []string) error {
	last := -1
	present := map[string]bool{}
	for _, tag := range seen {
		if !isKnownTag(tag) {
			continue
		}
		if present[tag] {
			return fmt.Errorf("duplicate section %q", tag)
		}
		present[tag] = true
		pos := indexOf(sectionOrder, tag)
		if pos < last {
			return fmt.Errorf("section %q out of order", tag)
		}
		last = pos
	}
	for _, req := range []string{tagSTRT, tagTYPE, tagMODU} {
		if !present[req] {
			return fmt.Errorf("missing required section %q", req)
		}
	}
	return nil
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

func readU16(b []byte, off int) uint16 { return binary.LittleEndian.Uint16(b[off:]) }
func readU32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
func readU64(b []byte, off int) uint64 { return binary.LittleEndian.Uint64(b[off:]) }

// ---------------------------------------------------------------------------
// reader — a section payload cursor
// ---------------------------------------------------------------------------

type reader struct {
	data []byte
	pos  int
}

func newReaderFor(d *decoder, tag string) *reader {
	return &reader{data: d.byTag[tag]}
}

func (r *reader) b() byte {
	if r.pos >= len(r.data) {
		fail("unexpected end of section input")
	}
	v := r.data[r.pos]
	r.pos++
	return v
}
func (r *reader) bool() bool { return r.b() != 0 }
func (r *reader) raw(n int) []byte {
	if r.pos+n > len(r.data) {
		fail("read of %d bytes exceeds section input", n)
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out
}
func (r *reader) uleb() uint64 {
	var result uint64
	var shift uint
	for {
		b := r.b()
		result |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return result
		}
		shift += 7
		if shift >= 70 {
			fail("uleb overflow")
		}
	}
}
func (r *reader) sleb() int64 {
	var result int64
	var shift uint
	var b byte
	for {
		b = r.b()
		result |= int64(b&0x7F) << shift
		shift += 7
		if b&0x80 == 0 {
			break
		}
		if shift >= 70 {
			fail("sleb overflow")
		}
	}
	if shift < 64 && b&0x40 != 0 {
		result |= -1 << shift
	}
	return result
}
func (r *reader) u32() uint32 { v := readU32(r.data, r.pos); r.pos += 4; return v }
func (r *reader) u64() uint64 { v := readU64(r.data, r.pos); r.pos += 8; return v }
func (r *reader) bytesField() []byte {
	n := int(r.uleb())
	return r.raw(n)
}

// str resolves a uleb STRT index (0 = absent).
func (d *decoder) str(r *reader) string {
	idx := r.uleb()
	if idx == 0 {
		return ""
	}
	if int(idx) > len(d.strList) {
		fail("string index %d out of range", idx)
	}
	return d.strList[idx-1]
}

// typ resolves a uleb TYPE index (0 = absent/nil).
func (d *decoder) typ(r *reader) vir.Type {
	idx := r.uleb()
	if idx == 0 {
		return nil
	}
	if int(idx) > len(d.typeList) {
		fail("type index %d out of range", idx)
	}
	return d.typeList[idx-1]
}

// ---------------------------------------------------------------------------
// build — dispatch every section into a *vir.Module
// ---------------------------------------------------------------------------

func (d *decoder) build() *vir.Module {
	d.readStrt()
	d.readType()

	m := &vir.Module{}
	d.m = m

	mr := newReaderFor(d, tagMODU)
	m.Name = d.str(mr)
	m.Namespace = d.str(mr)

	if payload, ok := d.byTag[tagTARG]; ok {
		_ = payload
		tr := newReaderFor(d, tagTARG)
		t := &vir.Target{Arch: d.str(tr), OS: d.str(tr), ABI: d.str(tr)}
		for n := tr.uleb(); n > 0; n-- {
			t.Tiers = append(t.Tiers, d.str(tr))
		}
		m.Target = t
	}
	if _, ok := d.byTag[tagASMD]; ok {
		ar := newReaderFor(d, tagASMD)
		dial, ok := asmDialectByCode[ar.b()]
		if !ok {
			fail("unknown asm dialect code")
		}
		m.AsmDialect = &dial
	}

	if _, ok := d.byTag[tagSTRU]; ok {
		sr := newReaderFor(d, tagSTRU)
		for n := sr.uleb(); n > 0; n-- {
			s := &vir.Struct{Name: d.str(sr)}
			flags := sr.b()
			s.Export = flags&declFlagExport != 0
			for k := sr.uleb(); k > 0; k-- {
				s.Fields = append(s.Fields, vir.Field{Name: d.str(sr), Type: d.typ(sr)})
			}
			m.Structs = append(m.Structs, s)
		}
	}

	if _, ok := d.byTag[tagFSIG]; ok {
		fr := newReaderFor(d, tagFSIG)
		for n := fr.uleb(); n > 0; n-- {
			sig := &vir.FunctionSignature{Name: d.str(fr)}
			flags := fr.b()
			sig.Export = flags&declFlagExport != 0
			sig.Variadic = flags&declFlagVariadic != 0
			for k := fr.uleb(); k > 0; k-- {
				sig.Params = append(sig.Params, d.typ(fr))
			}
			sig.Ret = d.typ(fr)
			m.FunctionSignatures = append(m.FunctionSignatures, sig)
		}
	}

	if _, ok := d.byTag[tagCNST]; ok {
		cr := newReaderFor(d, tagCNST)
		for n := cr.uleb(); n > 0; n-- {
			c := &vir.Constant{Name: d.str(cr)}
			flags := cr.b()
			c.Export = flags&declFlagExport != 0
			c.Type = d.typ(cr)
			c.Value = d.value(cr, c.Type)
			m.Constants = append(m.Constants, c)
		}
	}

	if _, ok := d.byTag[tagGLOB]; ok {
		gr := newReaderFor(d, tagGLOB)
		for n := gr.uleb(); n > 0; n-- {
			g := &vir.Global{Name: d.str(gr)}
			flags := gr.b()
			g.Export = flags&declFlagExport != 0
			g.TLS = flags&declFlagTLS != 0
			g.Type = d.typ(gr)
			g.Align = int(gr.uleb())
			g.Init = d.constInit(gr, g.Type)
			m.Globals = append(m.Globals, g)
		}
	}

	if _, ok := d.byTag[tagLINK]; ok {
		lr := newReaderFor(d, tagLINK)
		for n := lr.uleb(); n > 0; n-- {
			kind, ok := linkKindByCode[lr.b()]
			if !ok {
				fail("unknown link kind code")
			}
			m.Links = append(m.Links, &vir.Link{Kind: kind, Name: d.str(lr)})
		}
	}

	if _, ok := d.byTag[tagEXTN]; ok {
		er := newReaderFor(d, tagEXTN)
		for n := er.uleb(); n > 0; n-- {
			g := &vir.ExternGroup{Dependency: d.str(er)}
			for k := er.uleb(); k > 0; k-- {
				f := &vir.ExternFunction{Name: d.str(er)}
				for p := er.uleb(); p > 0; p-- {
					f.Params = append(f.Params, d.param(er))
				}
				f.Variadic = er.bool()
				f.Ret = d.typ(er)
				f.Attrs = decodeAttrBits(er.uleb())
				g.Functions = append(g.Functions, f)
			}
			m.Externs = append(m.Externs, g)
		}
	}

	if _, ok := d.byTag[tagIMPT]; ok {
		ir := newReaderFor(d, tagIMPT)
		for n := ir.uleb(); n > 0; n-- {
			m.Imports = append(m.Imports, &vir.Import{Path: d.str(ir)})
		}
	}

	var asmPool [][]byte
	if _, ok := d.byTag[tagASMB]; ok {
		ar := newReaderFor(d, tagASMB)
		for n := ar.uleb(); n > 0; n-- {
			// Each pool entry's own length isn't framed explicitly, so we
			// decode it in place and slice out exactly the bytes consumed.
			start := ar.pos
			d.skipAsmBlock(ar)
			asmPool = append(asmPool, ar.data[start:ar.pos])
		}
	}

	var locs []decodedLoc
	if _, ok := d.byTag[tagLOCS]; ok {
		lr := newReaderFor(d, tagLOCS)
		pf, pb, pi := 0, 0, 0
		for n := lr.uleb(); n > 0; n-- {
			pf += int(lr.uleb())
			pb += int(lr.sleb())
			pi += int(lr.sleb())
			file := d.str(lr)
			line := int(lr.uleb())
			col := int(lr.uleb())
			locs = append(locs, decodedLoc{pf, pb, pi, file, line, col})
		}
	}

	if _, ok := d.byTag[tagFUNC]; ok {
		fr := newReaderFor(d, tagFUNC)
		for fi := 0; fr.pos < len(fr.data); fi++ {
			f := d.readFunction(fr, asmPool, fi, locs)
			m.Functions = append(m.Functions, f)
		}
	}

	return m
}

type decodedLoc struct {
	funcIdx, blockIdx, instIdx int
	file                       string
	line, col                  int
}

// ---------------------------------------------------------------------------
// STRT / TYPE
// ---------------------------------------------------------------------------

func (d *decoder) readStrt() {
	r := newReaderFor(d, tagSTRT)
	n := r.uleb()
	for i := uint64(0); i < n; i++ {
		length := int(r.uleb())
		d.strList = append(d.strList, string(r.raw(length)))
	}
}

func (d *decoder) readType() {
	r := newReaderFor(d, tagTYPE)
	n := r.uleb()
	for i := uint64(0); i < n; i++ {
		kind := r.b()
		var t vir.Type
		switch kind {
		case typeKindVoid:
			t = vir.Void
		case typeKindInt:
			t = vir.IntType{Bits: int(r.uleb())}
		case typeKindFloat:
			t = vir.FloatType{Bits: int(r.b())}
		case typeKindPtr:
			t = vir.Ptr
		case typeKindValist:
			t = vir.Valist
		case typeKindVec:
			elemIdx := r.uleb()
			ln := r.uleb()
			t = vir.VecType{Elem: d.mustType(elemIdx), Len: int(ln)}
		case typeKindArray:
			elemIdx := r.uleb()
			ln := r.uleb()
			t = vir.ArrayType{Elem: d.mustType(elemIdx), Len: int(ln)}
		case typeKindStructN:
			origin := r.b()
			if origin == structOriginImport {
				impIdx := r.uleb()
				nameIdx := r.uleb()
				t = vir.StructType{Import: d.strList[impIdx-1], Name: d.strList[nameIdx-1]}
			} else {
				pos := r.uleb()
				_ = pos
				// The struct's Name isn't resolvable yet (STRU decodes
				// after TYPE); record the position and patch names in a
				// second pass once STRU is available.
				t = pendingLocalStruct{pos: int(pos)}
			}
		default:
			fail("unknown type kind %d", kind)
		}
		d.typeList = append(d.typeList, t)
	}
	// Patch pendingLocalStruct placeholders once STRU is decoded, in build().
}

// pendingLocalStruct is a decode-time-only placeholder; build() resolves
// these to real vir.StructType values once STRU has been read (TYPE
// necessarily decodes before STRU per §F4.1's STRT/TYPE-first ordering).
type pendingLocalStruct struct{ pos int }

func (pendingLocalStruct) String() string { return "<pending struct>" }
func (pendingLocalStruct) isType()        {}

func (d *decoder) mustType(idx uint64) vir.Type {
	if idx == 0 || int(idx) > len(d.typeList) {
		fail("type index %d out of range", idx)
	}
	return d.typeList[idx-1]
}

// resolvePendingStructs patches pendingLocalStruct placeholders now that
// m.Structs is populated. Called right after STRU decoding.
func (d *decoder) resolvePendingStructs() {
	for i, t := range d.typeList {
		if p, ok := t.(pendingLocalStruct); ok {
			if p.pos < 0 || p.pos >= len(d.m.Structs) {
				fail("struct type references out-of-range struct index %d", p.pos)
			}
			d.typeList[i] = vir.StructType{Name: d.m.Structs[p.pos].Name}
		}
	}
}

// ---------------------------------------------------------------------------
// value / const_init (§F2.7 + extensions #2/#3)
// ---------------------------------------------------------------------------

func (d *decoder) value(r *reader, ctxType vir.Type) vir.Operand {
	switch r.b() {
	case valueTagInt:
		return vir.IntLiteral(r.sleb())
	case valueTagFloat:
		width := r.b()
		return vir.FloatLiteral(d.readFloatBits(r, width))
	case valueTagString:
		return vir.StringLiteral(d.str(r))
	case valueTagNull:
		return vir.NullLiteral()
	case valueTagBool:
		return vir.BoolLiteral(r.bool())
	case valueTagVector:
		n := r.uleb()
		vals := make([]int64, 0, n)
		for i := uint64(0); i < n; i++ {
			vals = append(vals, r.sleb())
		}
		return vir.VectorLiteral(vals...)
	default:
		fail("unknown value tag")
		return vir.Operand{}
	}
}

func (d *decoder) readFloatBits(r *reader, width byte) float64 {
	switch width {
	case 16:
		bits16 := binary.LittleEndian.Uint16(r.raw(2))
		return f16ToFloat64(bits16)
	case 32:
		bits32 := binary.LittleEndian.Uint32(r.raw(4))
		return float64(math.Float32frombits(bits32))
	default:
		bits64 := binary.LittleEndian.Uint64(r.raw(8))
		return math.Float64frombits(bits64)
	}
}

func f16ToFloat64(bits uint16) float64 {
	sign := uint32(bits&0x8000) << 16
	exp := (bits >> 10) & 0x1F
	mant := uint32(bits & 0x3FF)
	var bits32 uint32
	switch {
	case exp == 0:
		bits32 = sign
	case exp == 0x1F:
		bits32 = sign | 0x7F800000 | mant<<13
	default:
		bits32 = sign | (uint32(exp)-15+127)<<23 | mant<<13
	}
	return float64(math.Float32frombits(bits32))
}

func (d *decoder) constInit(r *reader, t vir.Type) vir.ConstInit {
	switch r.b() {
	case initTagZero:
		return vir.InitZero{}
	case initTagLiteral:
		return vir.InitLiteral{Value: d.value(r, t)}
	case initTagAddr:
		kind := r.b()
		idx := r.uleb()
		return vir.InitAddressOf{Name: d.declName(kind, int(idx))}
	case initTagByteString:
		return vir.InitByteString{Data: r.bytesField()}
	case initTagAggregate:
		n := r.uleb()
		var elems []vir.ConstInit
		for i := uint64(0); i < n; i++ {
			et := d.aggregateElemTypeDecode(t, int(i))
			elems = append(elems, d.constInit(r, et))
		}
		return vir.InitAggregate{Elems: elems}
	default:
		fail("unknown const_init tag")
		return nil
	}
}

func (d *decoder) aggregateElemTypeDecode(t vir.Type, i int) vir.Type {
	switch x := t.(type) {
	case vir.ArrayType:
		return x.Elem
	case vir.StructType:
		if x.Import == "" {
			for _, s := range d.m.Structs {
				if s.Name == x.Name && i < len(s.Fields) {
					return s.Fields[i].Type
				}
			}
		}
	}
	return nil
}

func (d *decoder) declName(kind byte, idx int) string {
	switch kind {
	case declStruct:
		return d.m.Structs[idx].Name
	case declFnSig:
		return d.m.FunctionSignatures[idx].Name
	case declConst:
		return d.m.Constants[idx].Name
	case declGlobal:
		return d.m.Globals[idx].Name
	case declFn:
		return d.m.Functions[idx].Name
	case declExtern:
		i := 0
		for _, g := range d.m.Externs {
			for _, f := range g.Functions {
				if i == idx {
					return f.Name
				}
				i++
			}
		}
		fail("extern decl index %d out of range", idx)
	}
	fail("unknown decl kind %d", kind)
	return ""
}

// ---------------------------------------------------------------------------
// Params / attrs
// ---------------------------------------------------------------------------

func (d *decoder) param(r *reader) vir.Param {
	p := vir.Param{Name: d.str(r), Type: d.typ(r)}
	attr := r.b()
	structIdx := r.uleb()
	switch attr {
	case paramAttrByVal:
		p.ByVal = d.m.Structs[structIdx].Name
	case paramAttrSRet:
		p.SRet = d.m.Structs[structIdx].Name
	}
	return p
}

// ---------------------------------------------------------------------------
// ASMB pool (structural; deviation #4)
// ---------------------------------------------------------------------------

// skipAsmBlock advances r past one pool entry (used to slice out raw bytes
// for later re-parsing once each function's local table is known).
func (d *decoder) skipAsmBlock(r *reader) {
	n := r.uleb()
	for i := uint64(0); i < n; i++ {
		r.b()       // kind
		l := r.uleb()
		r.raw(int(l)) // register name
		r.uleb()    // local index
	}
	lines := r.uleb()
	for i := uint64(0); i < lines; i++ {
		if r.b() == 1 {
			l := r.uleb()
			r.raw(int(l))
			continue
		}
		l := r.uleb()
		r.raw(int(l)) // mnemonic
		ops := r.uleb()
		for j := uint64(0); j < ops; j++ {
			d.skipAsmOperand(r)
		}
	}
}

func (d *decoder) skipAsmOperand(r *reader) {
	switch r.b() {
	case asmOperandKindRegister, asmOperandKindMemory, asmOperandKindLabel:
		l := r.uleb()
		r.raw(int(l))
	case asmOperandKindImmediate:
		d.value(r, nil)
	default:
		fail("unknown asm operand kind")
	}
}

func (d *decoder) parseAsmBlock(payload []byte, localNames []string) *vir.AsmBlock {
	r := &reader{data: payload}
	a := &vir.AsmBlock{}
	n := r.uleb()
	for i := uint64(0); i < n; i++ {
		kindByte := r.b()
		kind, ok := bindingKindByCode[kindByte]
		if !ok {
			fail("unknown asm binding kind")
		}
		reg := string(r.bytesField())
		localIdx := r.uleb()
		if kind == vir.BindingClobber {
			addClobber(a, reg)
			continue
		}
		ident := ""
		if int(localIdx) < len(localNames) {
			ident = localNames[localIdx]
		}
		a.Bindings = append(a.Bindings, vir.AsmBinding{Kind: kind, Register: reg, Ident: ident})
	}
	lines := r.uleb()
	for i := uint64(0); i < lines; i++ {
		if r.b() == 1 {
			a.Code = append(a.Code, vir.AsmLabelDeclaration(string(r.bytesField())))
			continue
		}
		mnemonic := string(r.bytesField())
		ops := r.uleb()
		var operands []vir.AsmOperand
		for j := uint64(0); j < ops; j++ {
			operands = append(operands, d.parseAsmOperand(r))
		}
		a.Code = append(a.Code, vir.AsmInstructionLine(mnemonic, operands...))
	}
	return a
}

func addClobber(a *vir.AsmBlock, reg string) {
	for i := range a.Bindings {
		if a.Bindings[i].Kind == vir.BindingClobber {
			a.Bindings[i].Registers = append(a.Bindings[i].Registers, reg)
			return
		}
	}
	a.Bindings = append(a.Bindings, vir.AsmBinding{Kind: vir.BindingClobber, Registers: []string{reg}})
}

func (d *decoder) parseAsmOperand(r *reader) vir.AsmOperand {
	switch r.b() {
	case asmOperandKindRegister:
		return vir.AsmRegister(string(r.bytesField()))
	case asmOperandKindImmediate:
		return vir.AsmImmediate(d.value(r, nil))
	case asmOperandKindMemory:
		return vir.AsmMemory(string(r.bytesField()))
	case asmOperandKindLabel:
		return vir.AsmLabelReference(string(r.bytesField()))
	default:
		fail("unknown asm operand kind")
		return vir.AsmOperand{}
	}
}

// ---------------------------------------------------------------------------
// Functions / blocks / instructions / terminators
// ---------------------------------------------------------------------------

func (d *decoder) readFunction(fr *reader, asmPool [][]byte, fnIdx int, locs []decodedLoc) *vir.Function {
	_ = fr.uleb() // byte_length: unused here since we parse linearly anyway
	f := &vir.Function{}
	f.Name = d.str(fr)
	flags := fr.uleb()
	export, variadic, attrs := decodeFuncFlags(flags)
	f.Export, f.Variadic, f.Attrs = export, variadic, attrs

	nParams := fr.uleb()
	for i := uint64(0); i < nParams; i++ {
		f.Params = append(f.Params, d.param(fr))
	}
	f.Ret = d.typ(fr)

	nLocals := fr.uleb()
	var localNames []string
	for i := uint64(0); i < nLocals; i++ {
		name := d.str(fr)
		d.typ(fr) // local type: informative only; not stored on vir.Function directly
		localNames = append(localNames, name)
	}

	nBlocks := fr.uleb()
	blocks := make([]*vir.Block, 0, nBlocks)
	for bi := uint64(0); bi < nBlocks; bi++ {
		label := d.str(fr)
		b := &vir.Block{Label: label}
		instCount := fr.uleb()

		locsForBlock := locsFor(locs, fnIdx, int(bi))
		nextLocIdx := 0

		emitPendingLocs := func(upTo int) {
			for nextLocIdx < len(locsForBlock) && locsForBlock[nextLocIdx].instIdx == upTo {
				l := locsForBlock[nextLocIdx]
				b.Lines = append(b.Lines, vir.BodyLine{Instruction: &vir.Instruction{
					Op:   vir.OpLoc,
					Args: locArgs(l),
				}})
				nextLocIdx++
			}
		}

		for ii := 0; ii < int(instCount); ii++ {
			emitPendingLocs(ii)
			opNum := fr.uleb()
			if opNum == pseudoOpcodeAsmBlock {
				fr.uleb() // result_type (absent)
				fr.uleb() // result_local (none)
				n := fr.uleb()
				if n != 1 {
					fail("asm pseudo-instruction expects exactly one operand")
				}
				kindByte := fr.b()
				if kindByte != operandAsm {
					fail("asm pseudo-instruction operand must be kind ASM")
				}
				idx := fr.uleb()
				fr.uleb() // align (unused)
				if int(idx) >= len(asmPool) {
					fail("asm block index %d out of range", idx)
				}
				block := d.parseAsmBlock(asmPool[idx], localNames)
				b.Lines = append(b.Lines, vir.BodyLine{Asm: block})
				continue
			}
			op, ok := opcodeByNumber[opNum]
			if !ok {
				fail("unknown opcode number %d", opNum)
			}
			suffix := d.typ(fr)
			resultLocal := fr.uleb()
			nArgs := fr.uleb()
			inst := &vir.Instruction{Op: op, Suffix: suffix}
			if resultLocal > 0 {
				idx := int(resultLocal - 1)
				if idx < len(localNames) {
					inst.Result = localNames[idx]
				}
			}
			for a := uint64(0); a < nArgs; a++ {
				inst.Args = append(inst.Args, d.readOperand(fr, op, int(a), localNames))
			}
			inst.Align = int(fr.uleb())
			b.Lines = append(b.Lines, vir.BodyLine{Instruction: inst})
		}
		emitPendingLocs(int(instCount))

		b.Term = d.readTerm(fr, localNames)
		blocks = append(blocks, b)
	}

	if len(blocks) > 0 {
		f.Entry = blocks[0]
		f.Blocks = blocks[1:]
	}
	return f
}

func locsFor(locs []decodedLoc, fnIdx, blockIdx int) []decodedLoc {
	var out []decodedLoc
	for _, l := range locs {
		if l.funcIdx == fnIdx && l.blockIdx == blockIdx {
			out = append(out, l)
		}
	}
	return out
}

func locArgs(l decodedLoc) []vir.Operand {
	args := []vir.Operand{vir.StringLiteral(l.file), vir.IntLiteral(int64(l.line))}
	if l.col > 0 {
		args = append(args, vir.IntLiteral(int64(l.col)))
	}
	return args
}

func (d *decoder) readOperand(r *reader, op vir.Opcode, argIdx int, localNames []string) vir.Operand {
	kind := r.b()
	switch kind {
	case operandLocal:
		idx := r.uleb()
		if int(idx) >= len(localNames) {
			fail("local operand index %d out of range", idx)
		}
		return vir.Ident(localNames[idx])
	case operandDecl:
		declKind := r.b()
		idx := r.uleb()
		return vir.Ident(d.declName(declKind, int(idx)))
	case operandImport:
		impIdx := r.uleb()
		name := d.str(r)
		if int(impIdx) >= len(d.m.Imports) {
			fail("import operand index %d out of range", impIdx)
		}
		return vir.QualifiedIdent(d.m.Imports[impIdx].Path, name)
	case operandType:
		return vir.TypeOperand(d.typ(r))
	case operandOrdering:
		code := r.b()
		if int(code) >= len(orderingNames) {
			fail("unknown ordering code %d", code)
		}
		return vir.OrderingOperand(orderingNames[code])
	case operandLiteral:
		return d.value(r, nil)
	default:
		fail("unknown operand kind %d", kind)
		return vir.Operand{}
	}
}

func (d *decoder) readTerm(r *reader, localNames []string) vir.Terminator {
	labelName := func(idx uint64) string {
		if idx == 0 {
			fail("terminator label 0 (entry block) is not a valid branch target")
		}
		return "" // patched below via labelByIndex closure captured at call sites
	}
	_ = labelName

	switch r.uleb() {
	case termBranch:
		idx := r.uleb()
		return vir.Branch{Label: d.blockLabelPlaceholder(idx)}
	case termBranchIf:
		cond := d.readOperand(r, vir.OpInvalid, -1, localNames)
		then := r.uleb()
		els := r.uleb()
		return vir.BranchIf{Cond: cond, Then: d.blockLabelPlaceholder(then), Else: d.blockLabelPlaceholder(els)}
	case termSwitch:
		val := d.readOperand(r, vir.OpInvalid, -1, localNames)
		def := r.uleb()
		n := r.uleb()
		sw := vir.Switch{Value: val, Default: d.blockLabelPlaceholder(def)}
		for i := uint64(0); i < n; i++ {
			cv := r.sleb()
			cl := r.uleb()
			sw.Cases = append(sw.Cases, vir.SwitchCase{Value: cv, Label: d.blockLabelPlaceholder(cl)})
		}
		return sw
	case termReturn:
		if r.b() == 1 {
			v := d.readOperand(r, vir.OpInvalid, -1, localNames)
			return vir.Return{Value: &v}
		}
		return vir.Return{}
	case termTailCall:
		tc := vir.TailCall{}
		if r.b() == tailCallIndirect {
			idx := r.uleb()
			tc.Sig = d.m.FunctionSignatures[idx].Name
		} else {
			kind := r.b()
			idx := r.uleb()
			tc.Callee = d.declName(kind, int(idx))
		}
		n := r.uleb()
		for i := uint64(0); i < n; i++ {
			tc.Args = append(tc.Args, d.readOperand(r, vir.OpInvalid, -1, localNames))
		}
		return tc
	case termTrap:
		return vir.Trap{}
	case termUnreachable:
		return vir.Unreachable{}
	default:
		fail("unknown terminator tag")
		return nil
	}
}

// blockLabelPlaceholder resolves a 1-based block index (0 reserved for the
// unbranchable-to entry block) to the label string decoded for that block.
// Labels are decoded in order before terminators reference them, but a
// terminator can reference a later block (forward branch), so this needs
// the function's full block list — resolved via decoder-scoped state set
// by readFunction for the duration of that function's decode.
func (d *decoder) blockLabelPlaceholder(idx uint64) string {
	if int(idx) >= len(d.currentFuncLabels) {
		fail("branch target index %d out of range", idx)
	}
	return d.currentFuncLabels[idx]
}