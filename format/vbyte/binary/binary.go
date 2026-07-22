// File: format/vbyte/binary/binary.go
package binary

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// magicBytes is the fixed 4-byte module prefix ("vir\0", §3).
var magicBytes = []byte{0x76, 0x69, 0x72, 0x00}

const (
	versionMajor byte = 2
	versionMinor byte = 2
)

// Section IDs (§3.1). secStringTable is exempt from the ascending-ID rule
// and only ever legal in the carve-out position (§3.2).
const (
	secHeader      = 0x00
	secNamespace   = 0x01
	secTarget      = 0x02
	secStructs     = 0x03
	secFnSigs      = 0x04
	secConsts      = 0x05
	secGlobals     = 0x06
	secLinks       = 0x07
	secExterns     = 0x08
	secImports     = 0x09
	secFunctions   = 0x0A
	secStringTable = 0x0B
)

// Operand tags (§8.4). tagGlobal (0x0A) is an implementation extension: §4's
// index-space table documents global_idx as used for "global operands" (not
// just addr initializers), but §8.4 enumerates no such tag. We add one here
// rather than silently misencoding bare global references (see package
// doc / top-level notes for rationale).
const (
	tagLocal         = 0x01
	tagQualified     = 0x02
	tagLiteral       = 0x03
	tagType          = 0x04
	tagOrdering      = 0x05
	tagVectorLiteral = 0x06
	tagStructName    = 0x07
	tagFieldName     = 0x08
	tagCallableRef   = 0x09
	tagGlobal        = 0x0A
)

// Encode serializes m to its .vbyte binary encoding. m is assumed to have
// already passed ir/verify.Verify — this package performs no semantic
// validation, only structural encoding (see format/README.md).
func Encode(m *vir.Module) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("vbyte: nil module")
	}
	ec, err := newEncodeContext(m)
	if err != nil {
		return nil, err
	}

	w := newWriter()
	w.bytesRaw(magicBytes)
	w.u8(versionMajor)
	w.u8(versionMinor)

	// StringTable carve-out: immediately after the version header (§3.2).
	stw := newWriter()
	stw.uleb(uint64(len(ec.strings.list)))
	for _, s := range ec.strings.list {
		stw.bytesVec([]byte(s))
	}
	writeSection(w, secStringTable, stw.bytes())

	// Header (§3.1) — required exactly once.
	hw := newWriter()
	nameID, err := ec.strings.id(m.Name)
	if err != nil {
		return nil, err
	}
	hw.idx(nameID)
	writeSection(w, secHeader, hw.bytes())

	if m.Namespace != "" {
		nw := newWriter()
		id, err := ec.strings.id(m.Namespace)
		if err != nil {
			return nil, err
		}
		nw.idx(id)
		writeSection(w, secNamespace, nw.bytes())
	}

	if len(m.Links) > 0 && m.Target == nil {
		return nil, fmt.Errorf("vbyte: Links present but Target absent (§3.1)")
	}
	if m.Target != nil {
		tw := newWriter()
		if err := encodeTarget(tw, m.Target, ec); err != nil {
			return nil, err
		}
		writeSection(w, secTarget, tw.bytes())
	}

	if len(m.Structs) > 0 {
		sw := newWriter()
		sw.uleb(uint64(len(m.Structs)))
		for _, s := range m.Structs {
			if err := encodeStruct(sw, s, ec); err != nil {
				return nil, err
			}
		}
		writeSection(w, secStructs, sw.bytes())
	}

	if len(m.FunctionSignatures) > 0 {
		fw := newWriter()
		fw.uleb(uint64(len(m.FunctionSignatures)))
		for _, fs := range m.FunctionSignatures {
			if err := encodeFnSig(fw, fs, ec); err != nil {
				return nil, err
			}
		}
		writeSection(w, secFnSigs, fw.bytes())
	}

	if len(m.Constants) > 0 {
		cw := newWriter()
		cw.uleb(uint64(len(m.Constants)))
		for _, c := range m.Constants {
			if err := encodeConst(cw, c, ec); err != nil {
				return nil, err
			}
		}
		writeSection(w, secConsts, cw.bytes())
	}

	if len(m.Globals) > 0 {
		gw := newWriter()
		gw.uleb(uint64(len(m.Globals)))
		for _, g := range m.Globals {
			if err := encodeGlobal(gw, g, ec); err != nil {
				return nil, err
			}
		}
		writeSection(w, secGlobals, gw.bytes())
	}

	if len(m.Links) > 0 {
		lw := newWriter()
		lw.uleb(uint64(len(m.Links)))
		for _, l := range m.Links {
			if err := encodeLink(lw, l, ec); err != nil {
				return nil, err
			}
		}
		writeSection(w, secLinks, lw.bytes())
	}

	if len(m.Externs) > 0 {
		ew := newWriter()
		ew.uleb(uint64(len(m.Externs)))
		for _, eg := range m.Externs {
			if err := encodeExternGroup(ew, eg, ec); err != nil {
				return nil, err
			}
		}
		writeSection(w, secExterns, ew.bytes())
	}

	if len(m.Imports) > 0 {
		iw := newWriter()
		iw.uleb(uint64(len(m.Imports)))
		for _, im := range m.Imports {
			id, err := ec.strings.id(im.Path)
			if err != nil {
				return nil, err
			}
			iw.idx(id)
		}
		writeSection(w, secImports, iw.bytes())
	}

	if len(m.Functions) > 0 {
		fw := newWriter()
		fw.uleb(uint64(len(m.Functions)))
		for _, fn := range m.Functions {
			if err := encodeFunction(fw, fn, ec); err != nil {
				return nil, err
			}
		}
		writeSection(w, secFunctions, fw.bytes())
	}

	return w.bytes(), nil
}

// Decode parses data into an unverified *vir.Module. Callers must run
// ir/verify.Verify before trusting the result (see format/README.md).
func Decode(data []byte) (*vir.Module, error) {
	r := newReader(data)

	magic, err := r.bytesN(4)
	if err != nil {
		return nil, fmt.Errorf("vbyte: %w", err)
	}
	for i := range magicBytes {
		if magic[i] != magicBytes[i] {
			return nil, fmt.Errorf("vbyte: bad magic (expected \"vir\\0\")")
		}
	}
	major, err := r.u8()
	if err != nil {
		return nil, err
	}
	if _, err := r.u8(); err != nil { // minor: not rejection grounds by itself
		return nil, err
	}
	if major != versionMajor {
		return nil, fmt.Errorf("vbyte: unknown major version %d (§1 strict rejection)", major)
	}

	m := &vir.Module{}
	dc := newDecodeContext()

	first := true
	lastID := -1
	seenHeader := false

	for !r.atEnd() {
		id, err := r.u8()
		if err != nil {
			return nil, err
		}
		length, err := r.uleb()
		if err != nil {
			return nil, err
		}
		body, err := r.bytesN(int(length))
		if err != nil {
			return nil, err
		}
		sr := newReader(body)

		if id == secStringTable {
			if !first {
				return nil, fmt.Errorf("vbyte: StringTable must appear immediately after the version header (§3.2)")
			}
			list, err := decodeStringTable(sr)
			if err != nil {
				return nil, err
			}
			dc.strings = list
			first = false
			continue
		}
		first = false

		if id > secFunctions {
			return nil, fmt.Errorf("vbyte: unknown section id 0x%02x (§1 strict rejection)", id)
		}
		if int(id) <= lastID {
			return nil, fmt.Errorf("vbyte: sections must appear in strictly ascending order (§3.1)")
		}
		lastID = int(id)

		switch id {
		case secHeader:
			if err := decodeHeader(sr, m, dc); err != nil {
				return nil, err
			}
			seenHeader = true
		case secNamespace:
			if err := decodeNamespace(sr, m, dc); err != nil {
				return nil, err
			}
		case secTarget:
			if err := decodeTarget(sr, m, dc); err != nil {
				return nil, err
			}
		case secStructs:
			if err := decodeStructs(sr, m, dc); err != nil {
				return nil, err
			}
		case secFnSigs:
			if err := decodeFnSigs(sr, m, dc); err != nil {
				return nil, err
			}
		case secConsts:
			if err := decodeConsts(sr, m, dc); err != nil {
				return nil, err
			}
		case secGlobals:
			if err := decodeGlobals(sr, m, dc); err != nil {
				return nil, err
			}
		case secLinks:
			if err := decodeLinks(sr, m, dc); err != nil {
				return nil, err
			}
		case secExterns:
			if err := decodeExterns(sr, m, dc); err != nil {
				return nil, err
			}
		case secImports:
			if err := decodeImports(sr, m, dc); err != nil {
				return nil, err
			}
		case secFunctions:
			if err := decodeFunctions(sr, m, dc); err != nil {
				return nil, err
			}
		}
		if !sr.atEnd() {
			return nil, fmt.Errorf("vbyte: trailing bytes within section 0x%02x (§1 strict rejection)", id)
		}
	}

	if !seenHeader {
		return nil, fmt.Errorf("vbyte: Header section (0x00) is required (§3.1)")
	}
	if len(m.Links) > 0 && m.Target == nil {
		return nil, fmt.Errorf("vbyte: Links present but Target absent (§3.1)")
	}

	for _, fx := range dc.pending {
		switch fx.kind {
		case 0:
			if fx.index < 0 || fx.index >= len(dc.globals) {
				return nil, fmt.Errorf("vbyte: addr global index out of range")
			}
			fx.ptr.Name = dc.globals[fx.index].Name
		case 1:
			if fx.index < 0 || fx.index >= len(dc.fns) {
				return nil, fmt.Errorf("vbyte: addr function index out of range")
			}
			fx.ptr.Name = dc.fns[fx.index].Name
		default:
			return nil, fmt.Errorf("vbyte: invalid addr kind byte")
		}
	}

	return m, nil
}

func writeSection(w *writer, id byte, body []byte) {
	w.u8(id)
	w.uleb(uint64(len(body)))
	w.bytesRaw(body)
}