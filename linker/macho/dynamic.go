package macho

import "fmt"

type PLTEntry struct {
	Name string
	Sym  *TableSymbol
	Idx  int
}

func collectPLTSymbols(symtab *SymbolTable, objects []*Object) []PLTEntry {
	referenced := make(map[string]bool)
	for _, obj := range objects {
		for _, rel := range obj.Relocs {
			if int(rel.SymIdx) < len(obj.Symbols) && obj.Symbols[rel.SymIdx] != nil {
				if name := obj.Symbols[rel.SymIdx].Name; name != "" {
					referenced[name] = true
				}
			}
		}
	}
	var out []PLTEntry
	seen := make(map[string]bool)
	for _, obj := range objects {
		for _, raw := range obj.Symbols {
			if raw == nil || raw.Name == "" || seen[raw.Name] || !referenced[raw.Name] {
				continue
			}
			sym := symtab.Lookup(raw.Name)
			if sym == nil || !sym.IsShared() {
				continue
			}
			seen[raw.Name] = true
			out = append(out, PLTEntry{Name: raw.Name, Sym: sym, Idx: len(out)})
		}
	}
	return out
}

const (
	sectionStubs = "__TEXT/__stubs"
	sectionGOT   = "__DATA/__got"
	gotEntrySize = 8
)

// injectMachoPLT adds __TEXT/__stubs and __DATA/__got to the layout, sized
// per the registered PLTPatcher's stub width for this target.
func injectMachoPLT(pp PLTPatcher, layout *Layout, syms []PLTEntry) error {
	n := len(syms)
	if n == 0 {
		return nil
	}
	stubSz := pp.StubSize()

	stubs := &MergedSection{
		Name:     sectionStubs,
		Flags:    SecAlloc | SecExec,
		RawType:  uint32(S_SYMBOL_STUBS),
		RawFlags: uint64(S_SYMBOL_STUBS | S_ATTR_PURE_INSTRUCTIONS | S_ATTR_SOME_INSTRUCTIONS),
		Data:     make([]byte, n*stubSz),
		Size:     uint64(n * stubSz),
		Align:    4,
	}
	got := &MergedSection{
		Name:     sectionGOT,
		Flags:    SecAlloc | SecWrite,
		RawType:  uint32(S_NON_LAZY_SYMBOL_POINTERS),
		RawFlags: uint64(S_NON_LAZY_SYMBOL_POINTERS),
		Data:     make([]byte, n*gotEntrySize),
		Size:     uint64(n * gotEntrySize),
		Align:    8,
	}
	layout.Sections = append(layout.Sections, stubs, got)
	layout.secByName[stubs.Name] = stubs
	layout.secByName[got.Name] = got
	return nil
}

// patchPLT fills PLT stubs and GOT slots, returning the StubMap that Apply
// will need for GOT-relative relocations.
func patchPLT(pp PLTPatcher, layout *Layout, syms []PLTEntry) (StubMap, error) {
	pltSec, ok1 := layout.SectionByName(sectionStubs)
	gotSec, ok2 := layout.SectionByName(sectionGOT)
	if !ok1 || !ok2 {
		return nil, nil
	}
	stubs := pp.PatchPLT(pltSec.Data, gotSec.Data, nil, pltSec.VAddr, gotSec.VAddr, syms)
	for _, sym := range syms {
		if stubVA, ok := stubs[pltSec.VAddr+uint64(sym.Idx)*uint64(pp.StubSize())]; ok {
			_ = stubVA
		}
		sym.Sym.VAddr = pltSec.VAddr + uint64(sym.Idx)*uint64(pp.StubSize())
	}
	return stubs, nil
}

// ── dyld LINKEDIT builders (format-agnostic across arches) ───────────────────

func BuildRebaseInfo() []byte { return []byte{REBASE_OPCODE_DONE} }

func BuildExportTrie() []byte { return []byte{0x00, 0x00} }

type trieEntry struct {
	name string
	addr uint64
}

func BuildExportTrieForSymbols(exports map[string]uint64) []byte {
	if len(exports) == 0 {
		return BuildExportTrie()
	}
	entries := make([]trieEntry, 0, len(exports))
	for k, v := range exports {
		entries = append(entries, trieEntry{k, v})
	}
	return buildLinearTrie(entries)
}

func BuildBindInfo(pltSyms []string, gotSec *MergedSection, dataSegIndex uint32, neededLibs []string, req *emitRequest) []byte {
	if len(pltSyms) == 0 || gotSec == nil {
		return []byte{BIND_OPCODE_DONE}
	}
	ordinalOf := make(map[string]int, len(neededLibs))
	for i, s := range neededLibs {
		ordinalOf[s] = i + 1
	}
	dataBase := dataSegVMAddr(req)

	var buf []byte
	prevSegOff := uint64(0)
	firstEntry := true

	for i, symName := range pltSyms {
		gotOffset := uint64(i) * gotEntrySize
		segOff := gotSec.VAddr - dataBase + gotOffset

		ordinal := 1
		if sym := req.Symtab.Lookup(symName); sym != nil && sym.Lib != nil {
			if ord, ok := ordinalOf[sym.Lib.Soname]; ok {
				ordinal = ord
			}
		}

		if ordinal > 0 && ordinal <= 15 {
			buf = append(buf, BIND_OPCODE_SET_DYLIB_ORDINAL_IMM|uint8(ordinal))
		} else {
			buf = append(buf, BIND_OPCODE_SET_DYLIB_ORDINAL_ULEB)
			buf = appendULEB128(buf, uint64(ordinal))
		}

		buf = append(buf, BIND_OPCODE_SET_SYMBOL_TRAILING_FLAGS_IMM|0x00)
		buf = append(buf, []byte(symName)...)
		buf = append(buf, 0x00)
		buf = append(buf, BIND_OPCODE_SET_TYPE_IMM|BIND_TYPE_POINTER)

		if firstEntry {
			buf = append(buf, BIND_OPCODE_SET_SEGMENT_AND_OFFSET_ULEB|uint8(dataSegIndex))
			buf = appendULEB128(buf, segOff)
		} else {
			delta := segOff - prevSegOff - gotEntrySize
			if delta != 0 {
				buf = append(buf, BIND_OPCODE_ADD_ADDR_ULEB)
				buf = appendULEB128(buf, delta)
			}
		}
		prevSegOff = segOff
		firstEntry = false
		buf = append(buf, BIND_OPCODE_DO_BIND)
	}

	buf = append(buf, BIND_OPCODE_DONE)
	for len(buf)%8 != 0 {
		buf = append(buf, 0)
	}
	return buf
}

func dataSegVMAddr(req *emitRequest) uint64 {
	for _, sec := range req.Layout.Sections {
		if sec.Flags&SecAlloc != 0 && sec.Flags&SecWrite != 0 {
			return sec.VAddr
		}
	}
	return 0
}

type trieEdge struct {
	label string
	child *trieNode
}
type trieNode struct {
	edges     []trieEdge
	hasExport bool
	addr      uint64
}

func buildLinearTrie(entries []trieEntry) []byte {
	root := &trieNode{}
	for _, e := range entries {
		insertTrie(root, e.name, e.addr)
	}
	var buf []byte
	serializeTrie(root, &buf)
	return buf
}

func insertTrie(node *trieNode, name string, addr uint64) {
	if name == "" {
		node.hasExport = true
		node.addr = addr
		return
	}
	for i := range node.edges {
		e := &node.edges[i]
		common := commonPrefix(e.label, name)
		if common == 0 {
			continue
		}
		if common == len(e.label) {
			insertTrie(e.child, name[common:], addr)
			return
		}
		newChild := &trieNode{edges: []trieEdge{{label: e.label[common:], child: e.child}}}
		e.label = e.label[:common]
		e.child = newChild
		insertTrie(newChild, name[common:], addr)
		return
	}
	child := &trieNode{}
	node.edges = append(node.edges, trieEdge{label: name, child: child})
	insertTrie(child, "", addr)
}

func commonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func serializeTrie(node *trieNode, buf *[]byte) {
	var terminal []byte
	if node.hasExport {
		flagBytes := appendULEB128(nil, EXPORT_SYMBOL_FLAGS_KIND_REGULAR)
		addrBytes := appendULEB128(nil, node.addr)
		terminal = appendULEB128(nil, uint64(len(flagBytes)+len(addrBytes)))
		terminal = append(terminal, flagBytes...)
		terminal = append(terminal, addrBytes...)
	} else {
		terminal = []byte{0x00}
	}
	*buf = append(*buf, terminal...)
	*buf = append(*buf, uint8(len(node.edges)))
	for _, e := range node.edges {
		*buf = append(*buf, []byte(e.label)...)
		*buf = append(*buf, 0x00)
		childOff := len(*buf) + 5
		*buf = appendULEB128(*buf, uint64(childOff))
	}
	for _, e := range node.edges {
		serializeTrie(e.child, buf)
	}
}

var _ = fmt.Sprintf // keep fmt import if unused elsewhere