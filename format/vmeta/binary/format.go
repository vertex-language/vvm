// format.go
package binary

// Package binary implements the .vmeta container format (file_formats.md
// §F5): the binary encoding of a vir.ModuleShape (ir/vir's vmeta.go, Stage
// 0 of §7.3) into the on-disk cross-module shape artifact, and back.
//
// .vmeta shares its low-level container conventions with .vbyte (§F2):
// same header/section-table/trailer shape, same primitive encodings, same
// hash-consed type table. It differs in what it carries — export-tagged
// shapes only, deep-structural rather than nominal, no bodies, no
// compression — per §F5.1.
//
// Known limitations (round-trip is lossy in ways the format spec itself
// mandates, not accidents of this implementation):
//
//  1. STRUCT_S (§F2.6, §F5.3) is structural: field types only, no struct
//     name, no field names. Decode recovers a name for a decoded struct
//     type only by matching its TYPE index against this same file's own
//     XPRT struct exports; a decoded struct type that matches no local
//     export comes back as vir.StructType{Name: "<anonymous>"} — vir.Type
//     is a closed interface (isType() is unexported), so this package
//     cannot mint a new implementation to carry a raw structural shape
//     directly. Field names are synthesized ("_0", "_1", ...) since
//     STRUCT_S never stored them to begin with.
//  2. byval[S]/sret[S] structural expansion (§F5.4 param_shape) carries
//     no struct name either, for the same §7.4 reason ("never by struct
//     name"). Decode cannot repopulate Param.ByVal/Param.SRet's real name
//     string; those fields decode as the placeholder "<structural>" —
//     callers that need the structural attribute should key off the attr
//     kind (byval/sret) plus the expanded type, not the name.
//  3. vir.GlobalShape (ir/vir's vmeta.go) does not currently carry an
//     Align field, so the `align` slot in an encoded global export
//     (§F5.4) is always written as 0 (unspecified) and ignored on
//     decode. §F5.5 excludes align from shape_hash regardless, so this
//     has no effect on Stage B equality — only on round-tripping the
//     codegen hint.
//  4. Literal encoding extends §F2.7 with 0x05 BOOL / 0x06 VECTOR,
//     mirroring format/vbyte/binary's own documented deviation — §6.2
//     scalar consts include i1 (bool) and vec[T,N], which the base §F2.7
//     grammar has no tag for.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	vir "github.com/vertex-language/vvm/ir/vir"
)

const (
	formatMajor uint16 = 1
	formatMinor uint16 = 0
	irMajor     uint16 = 1
	irMinor     uint16 = 9
)

var fileMagic = [8]byte{0x00, 'V', 'M', 'T', 0x0D, 0x0A, 0x1A, 0x0A}

// Section flags (§F2.2).
const (
	flagRequired    uint32 = 1 << 0
	flagZstd        uint32 = 1 << 1
	flagNonSemantic uint32 = 1 << 2
)

// Section tags, in the fixed §F5.2 order.
var (
	tagSTRT = [4]byte{'S', 'T', 'R', 'T'}
	tagTYPE = [4]byte{'T', 'Y', 'P', 'E'}
	tagMODU = [4]byte{'M', 'O', 'D', 'U'}
	tagTARG = [4]byte{'T', 'A', 'R', 'G'}
	tagXPRT = [4]byte{'X', 'P', 'R', 'T'}
	tagIMPD = [4]byte{'I', 'M', 'P', 'D'}
)

// sectionOrder is the required §F5.2 ordering. STRT/TYPE/MODU/XPRT are
// required; TARG/IMPD are optional but must appear in this relative
// position when present.
var sectionOrder = [][4]byte{tagSTRT, tagTYPE, tagMODU, tagTARG, tagXPRT, tagIMPD}

func sectionOrderIndex(tag [4]byte) int {
	for i, t := range sectionOrder {
		if t == tag {
			return i
		}
	}
	return -1
}

// Input is what Encode needs beyond the vir.ModuleShape Stage 0 already
// produced (ir/vir's ExtractShape). ModuleShape is a cross-module-facing
// type and deliberately doesn't carry Target or the producing module's own
// import list (vmeta.go) — both come from the Module directly.
type Input struct {
	Shape *vir.ModuleShape

	// Target, if non-nil, is recorded in TARG for compatibility checking
	// (§F5.2) — not for shape equality, which is target-independent.
	Target *vir.Target

	// Imports lists the producing module's own direct `import` strings
	// (§7.3). Written to IMPD as a NONSEMANTIC build-graph hint only
	// (§F5.2); a reader that ignores it is still correct.
	Imports []string
}

// Result is what Decode reconstructs from a .vmeta file.
type Result struct {
	Shape   *vir.ModuleShape
	Target  *vir.Target
	Imports []string
}

// section is an in-memory section awaiting assembly into the container.
type section struct {
	tag     [4]byte
	flags   uint32
	payload []byte
}

// Encode serializes in into a .vmeta container (§F5). The module must
// already have passed vir.Verify — this package does not re-verify,
// matching format/vbyte's own "neither codec re-validates" stance.
func Encode(in Input) ([]byte, error) {
	if in.Shape == nil {
		return nil, fmt.Errorf("vmeta: encode: nil ModuleShape")
	}
	if in.Shape.ModuleName == "" {
		return nil, fmt.Errorf("vmeta: encode: module has no name (§2.1)")
	}

	strs := newStringTable()
	types := newTypeTable()
	resolve := localStructResolver(in.Shape)

	moduPayload := encodeModu(strs, in.Shape)

	var targPayload []byte
	if in.Target != nil {
		p, err := encodeTarg(strs, in.Target)
		if err != nil {
			return nil, err
		}
		targPayload = p
	}

	xprtPayload, err := encodeXprt(strs, types, resolve, in.Shape)
	if err != nil {
		return nil, err
	}

	impdPayload := encodeImpd(strs, in.Imports)

	secs := []section{
		{tag: tagSTRT, flags: flagRequired, payload: strs.encode()},
		{tag: tagTYPE, flags: flagRequired, payload: types.encode()},
		{tag: tagMODU, flags: flagRequired, payload: moduPayload},
	}
	if in.Target != nil {
		secs = append(secs, section{tag: tagTARG, payload: targPayload})
	}
	secs = append(secs, section{tag: tagXPRT, flags: flagRequired, payload: xprtPayload})
	if len(in.Imports) > 0 {
		secs = append(secs, section{tag: tagIMPD, flags: flagNonSemantic, payload: impdPayload})
	}

	return assemble(secs)
}

// assemble writes header + section table + 8-byte-aligned payloads +
// trailer (§F2.2), computing the trailer's content hash over everything
// preceding it. .vmeta never compresses (§F5.1), so stored_length ==
// uncompressed_length always.
func assemble(secs []section) ([]byte, error) {
	if len(secs) > 64 {
		return nil, fmt.Errorf("vmeta: encode: %d sections exceeds the 64-section limit (§F2.8)", len(secs))
	}

	headerLen := 32
	tableLen := 24 * len(secs)
	buf := make([]byte, headerLen+tableLen)

	copy(buf[0:8], fileMagic[:])
	binary.LittleEndian.PutUint16(buf[8:10], formatMajor)
	binary.LittleEndian.PutUint16(buf[10:12], formatMinor)
	binary.LittleEndian.PutUint16(buf[12:14], irMajor)
	binary.LittleEndian.PutUint16(buf[14:16], irMinor)
	binary.LittleEndian.PutUint32(buf[16:20], 0) // flags: none defined yet
	binary.LittleEndian.PutUint32(buf[20:24], uint32(len(secs)))
	// buf[24:32] stays zero (reserved).

	offset := uint64(headerLen + tableLen)
	entryOff := headerLen
	for _, s := range secs {
		pad := align8(offset) - offset
		for i := uint64(0); i < pad; i++ {
			buf = append(buf, 0)
		}
		offset += pad
		buf = append(buf, s.payload...)

		entry := buf[entryOff : entryOff+24]
		copy(entry[0:4], s.tag[:])
		binary.LittleEndian.PutUint32(entry[4:8], s.flags)
		binary.LittleEndian.PutUint64(entry[8:16], offset)
		binary.LittleEndian.PutUint32(entry[16:20], uint32(len(s.payload)))
		binary.LittleEndian.PutUint32(entry[20:24], uint32(len(s.payload)))
		entryOff += 24

		offset += uint64(len(s.payload))
	}

	pad := align8(offset) - offset
	for i := uint64(0); i < pad; i++ {
		buf = append(buf, 0)
	}

	sum := sha256.Sum256(buf)
	trailer := make([]byte, 40)
	copy(trailer[0:32], sum[:])
	binary.LittleEndian.PutUint32(trailer[32:36], 1) // hash_algo: SHA-256
	// trailer[36:40] reserved, zero.
	buf = append(buf, trailer...)

	return buf, nil
}

func align8(n uint64) uint64 { return (n + 7) &^ 7 }

// Decode parses a .vmeta container back into a Result. Framing errors
// (bad magic, truncation, non-canonical varints, bad trailer hash, ...)
// are returned as plain errors; this package doesn't implement the full
// E-BIN-/E-META- taxonomy (file_formats.md §F8.2) on its own, since that
// spans packages this one doesn't depend on.
func Decode(data []byte) (*Result, error) {
	if len(data) < 32+40 {
		return nil, fmt.Errorf("vmeta: decode: file too short to hold header+trailer")
	}
	if string(data[0:8]) != string(fileMagic[:]) {
		return nil, fmt.Errorf("vmeta: decode: bad magic")
	}
	if fMajor := binary.LittleEndian.Uint16(data[8:10]); fMajor != formatMajor {
		return nil, fmt.Errorf("vmeta: decode: format_major %d unsupported (reader supports %d)", fMajor, formatMajor)
	}
	if irMaj := binary.LittleEndian.Uint16(data[12:14]); irMaj != irMajor {
		return nil, fmt.Errorf("vmeta: decode: ir_major %d unsupported (reader supports %d)", irMaj, irMajor)
	}
	for _, b := range data[24:32] {
		if b != 0 {
			return nil, fmt.Errorf("vmeta: decode: nonzero reserved header byte")
		}
	}
	secCount := binary.LittleEndian.Uint32(data[20:24])
	if secCount > 64 {
		return nil, fmt.Errorf("vmeta: decode: section count %d exceeds limit (§F2.8)", secCount)
	}

	trailer := data[len(data)-40:]
	body := data[:len(data)-40]
	sum := sha256.Sum256(body)
	for i := 0; i < 32; i++ {
		if trailer[i] != sum[i] {
			return nil, fmt.Errorf("vmeta: decode: content hash mismatch (truncated or corrupt file)")
		}
	}
	if algo := binary.LittleEndian.Uint32(trailer[32:36]); algo != 1 {
		return nil, fmt.Errorf("vmeta: decode: unsupported hash_algo %d", algo)
	}
	for _, b := range trailer[36:40] {
		if b != 0 {
			return nil, fmt.Errorf("vmeta: decode: nonzero reserved trailer byte")
		}
	}

	tableStart := 32
	tableLen := int(secCount) * 24
	if tableStart+tableLen > len(body) {
		return nil, fmt.Errorf("vmeta: decode: section table runs past end of file")
	}

	payloads := map[[4]byte][]byte{}
	lastOrder := -1
	for i := 0; i < int(secCount); i++ {
		e := data[tableStart+i*24 : tableStart+i*24+24]
		var tag [4]byte
		copy(tag[:], e[0:4])
		flags := binary.LittleEndian.Uint32(e[4:8])
		off := binary.LittleEndian.Uint64(e[8:16])
		stored := binary.LittleEndian.Uint32(e[16:20])
		uncompressed := binary.LittleEndian.Uint32(e[20:24])

		if flags&flagZstd != 0 {
			return nil, fmt.Errorf("vmeta: decode: section %q is zstd-compressed, never legal in .vmeta (§F5.1)", tag)
		}
		if stored != uncompressed {
			return nil, fmt.Errorf("vmeta: decode: section %q stored/uncompressed length mismatch without compression", tag)
		}
		if off%8 != 0 {
			return nil, fmt.Errorf("vmeta: decode: section %q offset %d is not 8-byte aligned", tag, off)
		}
		if off+uint64(stored) > uint64(len(body)) {
			return nil, fmt.Errorf("vmeta: decode: section %q payload runs past end of file", tag)
		}
		if _, dup := payloads[tag]; dup {
			return nil, fmt.Errorf("vmeta: decode: duplicate section %q", tag)
		}

		idx := sectionOrderIndex(tag)
		if idx < 0 {
			if flags&flagRequired != 0 {
				return nil, fmt.Errorf("vmeta: decode: unrecognized required section %q", tag)
			}
			continue // unknown, not required: forward-compatible skip (§F7.1)
		}
		if idx < lastOrder {
			return nil, fmt.Errorf("vmeta: decode: section %q out of order (§F5.2)", tag)
		}
		lastOrder = idx
		payloads[tag] = body[off : off+uint64(stored)]
	}

	strtPayload, ok := payloads[tagSTRT]
	if !ok {
		return nil, fmt.Errorf("vmeta: decode: missing required STRT section")
	}
	strs, err := decodeStringTable(strtPayload)
	if err != nil {
		return nil, fmt.Errorf("vmeta: decode: STRT: %w", err)
	}

	typePayload, ok := payloads[tagTYPE]
	if !ok {
		return nil, fmt.Errorf("vmeta: decode: missing required TYPE section")
	}
	rawTypes, err := decodeTypeTable(typePayload)
	if err != nil {
		return nil, fmt.Errorf("vmeta: decode: TYPE: %w", err)
	}

	moduPayload, ok := payloads[tagMODU]
	if !ok {
		return nil, fmt.Errorf("vmeta: decode: missing required MODU section")
	}
	name, namespace, err := decodeModu(strs, moduPayload)
	if err != nil {
		return nil, fmt.Errorf("vmeta: decode: MODU: %w", err)
	}

	var target *vir.Target
	if targPayload, ok := payloads[tagTARG]; ok {
		t, err := decodeTarg(strs, targPayload)
		if err != nil {
			return nil, fmt.Errorf("vmeta: decode: TARG: %w", err)
		}
		target = t
	}

	xprtPayload, ok := payloads[tagXPRT]
	if !ok {
		return nil, fmt.Errorf("vmeta: decode: missing required XPRT section")
	}
	shape, err := decodeXprt(strs, rawTypes, name, namespace, xprtPayload)
	if err != nil {
		return nil, fmt.Errorf("vmeta: decode: XPRT: %w", err)
	}

	var imports []string
	if impdPayload, ok := payloads[tagIMPD]; ok {
		imports, err = decodeImpd(strs, impdPayload)
		if err != nil {
			return nil, fmt.Errorf("vmeta: decode: IMPD: %w", err)
		}
	}

	return &Result{Shape: shape, Target: target, Imports: imports}, nil
}