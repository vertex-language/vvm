package pe

import "fmt"

type Piece struct {
	Obj    *Object
	Sec    *ObjectSection
	Offset uint64
}

type MergedSection struct {
	Name     string
	Flags    SectionFlags
	RawType  uint32
	RawFlags uint64
	Align    uint64

	Pieces []Piece
	Data   []byte
	Size   uint64

	VAddr      uint64
	FileOffset uint64
}

type Layout struct {
	Sections  []*MergedSection
	secByName map[string]*MergedSection
}

func (l *Layout) SectionByName(name string) (*MergedSection, bool) {
	s, ok := l.secByName[name]
	return s, ok
}

const layoutPageSize = uint64(0x1000)

func MergeSections(objects []*Object) (*Layout, error) {
	var order []string
	byKey := make(map[string]*MergedSection)

	for _, obj := range objects {
		for _, sec := range obj.Sections {
			if sec == nil || sec.Index == 0 || sec.Name == "" || sec.Skip {
				continue
			}
			ms, exists := byKey[sec.Name]
			if !exists {
				ms = &MergedSection{
					Name:     sec.Name,
					Flags:    sec.Flags,
					RawType:  sec.RawType,
					RawFlags: sec.RawFlags,
					Align:    1,
				}
				byKey[sec.Name] = ms
				order = append(order, sec.Name)
			}
			if sec.Align > ms.Align {
				ms.Align = sec.Align
			}

			var pieceOffset uint64
			if sec.Flags&SecBSS == 0 {
				cur := uint64(len(ms.Data))
				aligned := alignUp(cur, sec.Align)
				for uint64(len(ms.Data)) < aligned {
					ms.Data = append(ms.Data, 0)
				}
				pieceOffset = aligned
				ms.Data = append(ms.Data, sec.Data...)
			} else {
				aligned := alignUp(ms.Size, sec.Align)
				pieceOffset = aligned
				ms.Size = aligned + sec.Size
			}
			ms.Pieces = append(ms.Pieces, Piece{Obj: obj, Sec: sec, Offset: pieceOffset})
		}
	}

	sections := make([]*MergedSection, 0, len(order))
	for _, k := range order {
		ms := byKey[k]
		if ms.Flags&SecBSS == 0 {
			ms.Size = uint64(len(ms.Data))
		}
		sections = append(sections, ms)
	}
	return &Layout{Sections: sections, secByName: byKey}, nil
}

func (l *Layout) AppendAllocSection(name string, data []byte, flags SectionFlags, align uint64) *MergedSection {
	var maxEnd uint64
	for _, ms := range l.Sections {
		if ms.Flags&SecAlloc == 0 {
			continue
		}
		if end := ms.VAddr + alignUp(ms.Size, layoutPageSize); end > maxEnd {
			maxEnd = end
		}
	}
	sec := &MergedSection{
		Name:  name,
		Flags: flags | SecAlloc,
		Data:  data,
		Size:  uint64(len(data)),
		Align: align,
		VAddr: alignUp(maxEnd, layoutPageSize),
	}
	l.Sections = append(l.Sections, sec)
	l.secByName[name] = sec
	return sec
}

func AssignLayout(outputType OutputType, layout *Layout, baseVA uint64) error {
	if baseVA == 0 && outputType == OutputExec {
		baseVA = 0x400000
	}

	fileOff := layoutPageSize
	vaddr := baseVA + fileOff

	var exSecs, roSecs, rwSecs, nonAlloc []*MergedSection
	for _, ms := range layout.Sections {
		if ms.Flags&SecAlloc == 0 {
			nonAlloc = append(nonAlloc, ms)
		} else if ms.Flags&SecWrite != 0 {
			rwSecs = append(rwSecs, ms)
		} else if ms.Flags&SecExec != 0 {
			exSecs = append(exSecs, ms)
		} else {
			roSecs = append(roSecs, ms)
		}
	}

	assign := func(secs []*MergedSection, newSegment bool) {
		if len(secs) == 0 {
			return
		}
		if newSegment {
			fileOff = alignUp(fileOff, layoutPageSize)
		}
		for _, ms := range secs {
			vaddr = alignUp(vaddr, layoutPageSize)
			fileOff = alignUp(fileOff, ms.Align)
			ms.FileOffset = fileOff
			ms.VAddr = vaddr
			if ms.Flags&SecBSS == 0 {
				fileOff += ms.Size
			}
			vaddr += alignUp(ms.Size, layoutPageSize)
		}
	}

	assign(exSecs, false)
	assign(roSecs, len(exSecs) > 0)
	assign(rwSecs, len(exSecs)+len(roSecs) > 0)

	for _, ms := range nonAlloc {
		fileOff = alignUp(fileOff, ms.Align)
		ms.FileOffset = fileOff
		ms.VAddr = 0
		if ms.Flags&SecBSS == 0 {
			fileOff += ms.Size
		}
	}
	return nil
}

func ResolveSymbolAddresses(symtab *SymbolTable, layout *Layout) error {
	for _, sym := range symtab.All() {
		if !sym.IsDefined() || sym.RawSym == nil {
			continue
		}
		raw := sym.RawSym
		switch raw.SectionName {
		case "*ABS*":
			sym.VAddr = raw.Value
			continue
		case "":
			continue
		}
		ms, ok := layout.SectionByName(raw.SectionName)
		if !ok {
			return fmt.Errorf("symbol %q references unknown output section %q", sym.Name, raw.SectionName)
		}
		var pieceOff uint64
		found := false
		for _, p := range ms.Pieces {
			if p.Obj == sym.Object && p.Sec.Name == raw.SectionName {
				pieceOff = p.Offset
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("symbol %q: piece not found in output section %q", sym.Name, raw.SectionName)
		}
		sym.VAddr = ms.VAddr + pieceOff + raw.Value
	}
	return nil
}

func alignUp(v, align uint64) uint64 {
	if align <= 1 {
		return v
	}
	return (v + align - 1) &^ (align - 1)
}