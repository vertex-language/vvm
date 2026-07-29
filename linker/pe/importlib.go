package pe

import "fmt"

// ImportKind mirrors IMPORT_OBJECT_* type values from the short-import
// header's TypeInfo field (bits 0-1).
type ImportKind uint8

const (
	ImportKindCode  ImportKind = 0
	ImportKindData  ImportKind = 1
	ImportKindConst ImportKind = 2
)

// ImportNameForm mirrors IMPORT_OBJECT_NAME_* values (TypeInfo bits 2-4) —
// how the linker should decorate/undecorate the symbol name.
type ImportNameForm uint8

const (
	ImportNameOrdinalOnly    ImportNameForm = 0
	ImportNameAsIs           ImportNameForm = 1
	ImportNameNoPrefix       ImportNameForm = 2
	ImportNameUndecorate     ImportNameForm = 3
	ImportNameExportAs       ImportNameForm = 4
)

// ImportSymbol is one symbol described by a short-import-library member.
// DLLName is author-supplied at .lib-build time (via LIB /DEF or an
// /EXPORT declaration when the DLL itself was linked) — this is the
// authoritative source PE tooling actually uses, unlike a target DLL's
// self-reported export-directory name (see shared.go).
type ImportSymbol struct {
	Name     string
	DLLName  string
	Ordinal  uint16
	Kind     ImportKind
	NameForm ImportNameForm
}

// ImportLibrary is a parsed short-format import library.
type ImportLibrary struct {
	Name    string
	Symbols map[string]*ImportSymbol
}

// looksLikeImportMember reports whether data begins with a short-import
// header. Per the documented DUMPBIN recognition rule: Sig1=0x0000,
// Sig2=0xFFFF, and — for a short import object specifically, as opposed to
// an anonymous object — Version=0x0000.
func looksLikeImportMember(data []byte) bool {
	if len(data) < sizeImportObjHdr {
		return false
	}
	sig1 := leU16(data, 0)
	sig2 := leU16(data, 2)
	version := leU16(data, 4)
	return sig1 == 0x0000 && sig2 == 0xFFFF && version == 0x0000
}

// parseImportMember decodes one IMPORT_OBJECT_HEADER member:
//
//	offset  size  field
//	0       2     Sig1            (must be 0x0000)
//	2       2     Sig2            (must be 0xFFFF)
//	4       2     Version         (0 for a short import object)
//	6       2     Machine
//	8       4     TimeDateStamp
//	12      4     SizeOfData      (length of the two NUL-terminated strings that follow)
//	16      2     OrdinalHint
//	18      2     TypeInfo        (bits 0-1: Type: bits 2-4: NameType)
//	20      SizeOfData            symbol name, then DLL name — both NUL-terminated
func parseImportMember(name string, data []byte) (*ImportSymbol, error) {
	if !looksLikeImportMember(data) {
		return nil, fmt.Errorf("%s: not a short import object header", name)
	}
	sizeOfData := leU32(data, 12)
	ordinalHint := leU16(data, 16)
	typeInfo := leU16(data, 18)

	kind := ImportKind(typeInfo & 0x3)
	nameForm := ImportNameForm((typeInfo >> 2) & 0x7)

	if 20+int(sizeOfData) > len(data) {
		return nil, fmt.Errorf("%s: SizeOfData %d exceeds available data", name, sizeOfData)
	}
	strTab := data[20 : 20+int(sizeOfData)]

	symName, next, err := readCStrFrom(strTab, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: reading symbol name: %w", name, err)
	}
	dllName, _, err := readCStrFrom(strTab, next)
	if err != nil {
		return nil, fmt.Errorf("%s: reading DLL name: %w", name, err)
	}

	return &ImportSymbol{
		Name:     symName,
		DLLName:  dllName,
		Ordinal:  ordinalHint,
		Kind:     kind,
		NameForm: nameForm,
	}, nil
}

func readCStrFrom(b []byte, start int) (string, int, error) {
	end := start
	for end < len(b) && b[end] != 0 {
		end++
	}
	if end >= len(b) {
		return "", 0, fmt.Errorf("unterminated string at offset %d", start)
	}
	return string(b[start:end]), end + 1, nil
}

func leU16(b []byte, off int) uint16 { return uint16(b[off]) | uint16(b[off+1])<<8 }

func leU32(b []byte, off int) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

// ParseImportLibrary parses a short-format import library — the same
// ar-container format ParseArchive reads, but with members that are
// IMPORT_OBJECT_HEADER descriptors rather than COFF objects. Members that
// don't match the short-import signature are skipped rather than erroring
// (a real .lib can mix in a genuine COFF object, e.g. for a weak alias),
// mirroring how ParseArchive already skips bookkeeping members like "/"
// and "//".
func ParseImportLibrary(name string, data []byte) (*ImportLibrary, error) {
	entries, _, err := rawArEntries(name, data)
	if err != nil {
		return nil, err
	}

	lib := &ImportLibrary{Name: name, Symbols: make(map[string]*ImportSymbol)}
	for _, e := range entries {
		switch e.rawName {
		case "/", "/SYM64/", "__.SYMDEF", "__.SYMDEF_64", "//":
			continue
		}
		if !looksLikeImportMember(e.data) {
			continue
		}
		sym, err := parseImportMember(e.rawName, e.data)
		if err != nil {
			return nil, fmt.Errorf("import library %q: %w", name, err)
		}
		lib.Symbols[sym.Name] = sym
	}
	return lib, nil
}