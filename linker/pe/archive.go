package pe

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	arMagic   = "!<arch>\n"
	arHdrSize = 60
	arFmag    = "`\n"
)

// arEntry is one raw, uninterpreted ar-container member: header parsed,
// name resolved through the long-name table if needed, contents sliced —
// but nothing about the *meaning* of those bytes decided yet. Shared
// between ParseArchive (static archives: real COFF object members) and
// ParseImportLibrary (import libraries: IMPORT_OBJECT_HEADER members) —
// both are the same ar container on this platform; see README, "Two kinds
// of .lib." Only the member contents differ, so only the parsers of those
// contents differ.
type arEntry struct {
	hdrOffset int
	rawName   string
	data      []byte
}

// rawArEntries splits an ar (`!<arch>\n`-magic) container into its member
// entries. It does not interpret any member's contents.
func rawArEntries(name string, data []byte) ([]arEntry, []byte, error) {
	if len(data) < len(arMagic) || string(data[:len(arMagic)]) != arMagic {
		return nil, nil, fmt.Errorf("archive %q: bad magic", name)
	}

	var entries []arEntry
	var longNameTable []byte

	pos := len(arMagic)
	for pos+arHdrSize <= len(data) {
		hdrOffset := pos
		hdr := data[pos : pos+arHdrSize]

		if string(hdr[58:60]) != arFmag {
			return nil, nil, fmt.Errorf("archive %q: bad ar_fmag at offset 0x%x", name, pos)
		}

		rawName := strings.TrimRight(string(hdr[0:16]), " ")
		sizeStr := strings.TrimRight(string(hdr[48:58]), " ")
		size, err := strconv.Atoi(sizeStr)
		if err != nil || size < 0 {
			return nil, nil, fmt.Errorf("archive %q: invalid ar_size %q at 0x%x", name, sizeStr, pos)
		}

		dataStart := pos + arHdrSize
		dataEnd := dataStart + size
		if dataEnd > len(data) {
			return nil, nil, fmt.Errorf("archive %q: member data out of bounds at 0x%x", name, pos)
		}

		memberData := data[dataStart:dataEnd]
		if rawName == "//" {
			longNameTable = make([]byte, size)
			copy(longNameTable, memberData)
		}
		entries = append(entries, arEntry{hdrOffset, rawName, memberData})
		pos = dataEnd
		if pos%2 != 0 {
			pos++
		}
	}
	return entries, longNameTable, nil
}

// ── Static archives ──────────────────────────────────────────────────────────

type ArchiveMember struct {
	Name  string
	data  []byte
	obj   *Object
	parse func(name string, data []byte) (*Object, error)
}

func (m *ArchiveMember) Object() (*Object, error) {
	if m.obj != nil {
		return m.obj, nil
	}
	obj, err := m.parse(m.Name, m.data)
	if err != nil {
		return nil, fmt.Errorf("archive member %q: %w", m.Name, err)
	}
	m.obj = obj
	return obj, nil
}

type Archive struct {
	Name     string
	Members  []*ArchiveMember
	symIndex map[string]int
}

func (a *Archive) MemberForSymbol(sym string) *ArchiveMember {
	if idx, ok := a.symIndex[sym]; ok {
		return a.Members[idx]
	}
	return nil
}

// ParseArchive parses a static archive (real COFF object members). For
// import libraries, see ParseImportLibrary in importlib.go instead —
// they share this same container format but not this parser, since their
// members aren't COFF objects at all.
func ParseArchive(name string, data []byte, parseObject func(string, []byte) (*Object, error)) (*Archive, error) {
	entries, longNameTable, err := rawArEntries(name, data)
	if err != nil {
		return nil, err
	}

	ar := &Archive{Name: name, symIndex: make(map[string]int)}
	offsetToMemberIdx := make(map[int]int)

	for _, e := range entries {
		switch e.rawName {
		case "/", "/SYM64/", "__.SYMDEF", "__.SYMDEF_64", "//":
			continue
		}
		n := arDecodeName(e.rawName, longNameTable)
		if n == "" {
			continue
		}
		idx := len(ar.Members)
		offsetToMemberIdx[e.hdrOffset] = idx
		ar.Members = append(ar.Members, &ArchiveMember{Name: n, data: e.data, parse: parseObject})
	}

	for _, e := range entries {
		switch e.rawName {
		case "/", "__.SYMDEF":
			arParseSymIndex32(e.data, ar, offsetToMemberIdx)
		case "/SYM64/", "__.SYMDEF_64":
			arParseSymIndex64(e.data, ar, offsetToMemberIdx)
		}
	}

	if len(ar.symIndex) == 0 {
		for idx, m := range ar.Members {
			obj, err := m.Object()
			if err != nil {
				continue
			}
			for _, sym := range obj.Symbols {
				if sym == nil || sym.Name == "" {
					continue
				}
				if !sym.StorageClass.IsExternal() && !sym.StorageClass.IsWeak() {
					continue
				}
				if sym.SectionIdx == SymSecUndef {
					continue
				}
				if _, exists := ar.symIndex[sym.Name]; !exists {
					ar.symIndex[sym.Name] = idx
				}
			}
		}
	}

	return ar, nil
}

func arDecodeName(rawName string, longNames []byte) string {
	if strings.HasPrefix(rawName, "/") && len(rawName) > 1 && rawName[1] != '/' {
		offStr := strings.TrimRight(rawName[1:], "/ ")
		n, err := strconv.Atoi(offStr)
		if err != nil || longNames == nil || n >= len(longNames) {
			return ""
		}
		end := n
		for end < len(longNames) && longNames[end] != '/' {
			end++
		}
		return string(longNames[n:end])
	}
	return strings.TrimRight(rawName, "/ ")
}

func arParseSymIndex32(data []byte, ar *Archive, offsetToMember map[int]int) {
	if len(data) < 4 {
		return
	}
	nsyms := arU32BE(data, 0)
	tableSize := 4 + int(nsyms)*4
	if len(data) < tableSize {
		return
	}
	offsets := make([]uint32, nsyms)
	for i := range offsets {
		offsets[i] = arU32BE(data, 4+i*4)
	}
	strtab := data[tableSize:]
	pos := 0
	for i := 0; i < int(nsyms); i++ {
		end := pos
		for end < len(strtab) && strtab[end] != 0 {
			end++
		}
		sym := string(strtab[pos:end])
		pos = end + 1
		if idx, ok := offsetToMember[int(offsets[i])]; ok {
			ar.symIndex[sym] = idx
		}
	}
}

func arParseSymIndex64(data []byte, ar *Archive, offsetToMember map[int]int) {
	if len(data) < 8 {
		return
	}
	nsyms := arU64BE(data, 0)
	tableSize := 8 + int(nsyms)*8
	if len(data) < tableSize {
		return
	}
	offsets := make([]uint64, nsyms)
	for i := range offsets {
		offsets[i] = arU64BE(data, 8+i*8)
	}
	strtab := data[tableSize:]
	pos := 0
	for i := 0; i < int(nsyms); i++ {
		end := pos
		for end < len(strtab) && strtab[end] != 0 {
			end++
		}
		sym := string(strtab[pos:end])
		pos = end + 1
		if idx, ok := offsetToMember[int(offsets[i])]; ok {
			ar.symIndex[sym] = idx
		}
	}
}

func arU32BE(b []byte, off int) uint32 {
	return uint32(b[off])<<24 | uint32(b[off+1])<<16 | uint32(b[off+2])<<8 | uint32(b[off+3])
}

func arU64BE(b []byte, off int) uint64 {
	return uint64(arU32BE(b, off))<<32 | uint64(arU32BE(b, off+4))
}