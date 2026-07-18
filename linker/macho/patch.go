package macho

import "fmt"

// StubMap carries the stub-VA → GOT-VA mapping produced by PatchPLT so that
// Patcher.Apply can resolve GOT-relative relocations, without any backend
// holding shared mutable state between the two registry factories.
type StubMap map[uint64]uint64

// Patcher applies a single relocation to a byte slice.
type Patcher interface {
	Apply(data []byte, off int, relType uint32, P, S uint64, A int64, stubs StubMap) error
}

// PatchFunc adapts a plain function to the Patcher interface.
type PatchFunc func(data []byte, off int, relType uint32, P, S uint64, A int64, stubs StubMap) error

func (f PatchFunc) Apply(data []byte, off int, relType uint32, P, S uint64, A int64, stubs StubMap) error {
	return f(data, off, relType, P, S, A, stubs)
}

// PLTPatcher writes arch-specific PLT stubs and reports their entry size.
type PLTPatcher interface {
	StubSize() int
	PatchPLT(pltData, gotData, relaPLT []byte, pltBase, gotBase uint64, syms []PLTEntry) StubMap
}

// PatchAll applies every relocation from every input object to the merged
// output section data. Must be called after AssignLayout, ResolveSymbolAddresses,
// and (if present) the PLT patch phase — stubs is that phase's StubMap, or nil.
func PatchAll(layout *Layout, symtab *SymbolTable, objects []*Object, p Patcher, stubs StubMap) error {
	for _, obj := range objects {
		for _, rel := range obj.Relocs {
			if err := applyOne(layout, symtab, obj, rel, p, stubs); err != nil {
				return fmt.Errorf("%s: %w", obj.Name, err)
			}
		}
	}
	return nil
}

func applyOne(layout *Layout, symtab *SymbolTable, obj *Object, rel *ObjectReloc, p Patcher, stubs StubMap) error {
	if rel.TargetSectionIdx >= len(obj.Sections) {
		return fmt.Errorf("reloc target section index %d out of range", rel.TargetSectionIdx)
	}
	inputSec := obj.Sections[rel.TargetSectionIdx]
	if inputSec == nil || inputSec.Skip {
		return nil
	}

	outSec, ok := layout.SectionByName(inputSec.Name)
	if !ok {
		return nil // GC'd or linker-internal
	}

	var pieceOff uint64
	found := false
	for _, piece := range outSec.Pieces {
		if piece.Obj == obj && piece.Sec == inputSec {
			pieceOff = piece.Offset
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("reloc: piece for %q not found in output section %q", inputSec.Name, outSec.Name)
	}

	patchOff := int(pieceOff + rel.Offset)
	if patchOff >= len(outSec.Data) {
		return fmt.Errorf("reloc patch offset 0x%x out of bounds in %q (size=%d)",
			patchOff, outSec.Name, len(outSec.Data))
	}

	P := outSec.VAddr + pieceOff + rel.Offset
	S, err := resolveRelocSym(rel, obj, symtab, layout)
	if err != nil {
		return err
	}
	return p.Apply(outSec.Data, patchOff, rel.Type, P, uint64(S), rel.Addend, stubs)
}

func resolveRelocSym(rel *ObjectReloc, obj *Object, symtab *SymbolTable, layout *Layout) (int64, error) {
	if rel.SymIdx == 0 {
		if rel.SecRelNum == 0 {
			return 0, nil
		}
		if int(rel.SecRelNum) < len(obj.Sections) {
			sec := obj.Sections[rel.SecRelNum]
			if sec != nil {
				ms, ok := layout.SectionByName(sec.Name)
				if ok {
					for _, p := range ms.Pieces {
						if p.Obj == obj && p.Sec == sec {
							return int64(ms.VAddr + p.Offset), nil
						}
					}
					return int64(ms.VAddr), nil
				}
			}
		}
		return 0, nil
	}

	if int(rel.SymIdx) >= len(obj.Symbols) {
		return 0, fmt.Errorf("reloc symbol index %d out of range", rel.SymIdx)
	}
	raw := obj.Symbols[rel.SymIdx]
	if raw == nil || raw.Name == "" {
		return 0, nil
	}

	if raw.Binding == BindLocal {
		if raw.SectionIdx > 0 && raw.SectionIdx < len(obj.Sections) {
			sec := obj.Sections[raw.SectionIdx]
			if sec != nil {
				ms, ok := layout.SectionByName(sec.Name)
				if ok {
					for _, p := range ms.Pieces {
						if p.Obj == obj && p.Sec == sec {
							return int64(ms.VAddr + p.Offset + raw.Value), nil
						}
					}
					return int64(ms.VAddr + raw.Value), nil
				}
			}
		}
		if raw.SectionIdx == SymSecAbs {
			return int64(raw.Value), nil
		}
		return 0, fmt.Errorf("local symbol %q lacks valid section mapping", raw.Name)
	}

	sym := symtab.Lookup(raw.Name)
	if sym == nil {
		if raw.Binding == BindWeak {
			return 0, nil
		}
		return 0, fmt.Errorf("undefined symbol %q", raw.Name)
	}
	switch sym.Kind {
	case kindDefined, kindCommon, kindShared:
		return int64(sym.VAddr), nil
	case kindUndefined:
		if sym.Weak {
			return 0, nil
		}
		return 0, fmt.Errorf("undefined symbol %q", raw.Name)
	}
	return 0, fmt.Errorf("symbol %q in unexpected state", raw.Name)
}