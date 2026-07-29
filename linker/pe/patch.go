package pe

import "fmt"

type Patcher interface {
	Apply(data []byte, off int, relType uint32, P, S uint64, A int64) error
}

type BaseRelocCollector interface {
	BaseRelocSites() []BaseRelocSite
}

type CoreBaseSetter interface {
	SetCoreBase(uint64)
}

// IATLayoutSetter lets an ImportPatcher receive the computed IAT slot
// layout at Link() time — it's a genuine per-link property (depends on the
// actual set of imported symbols this link gathered), same reasoning as
// CoreBaseSetter.
type IATLayoutSetter interface {
	SetIATLayout(*IATLayout)
}

func PatchAll(layout *Layout, symtab *SymbolTable, objects []*Object, patcher Patcher) error {
	for _, obj := range objects {
		for _, rel := range obj.Relocs {
			if err := applyOne(layout, symtab, obj, rel, patcher); err != nil {
				return fmt.Errorf("%s: %w", obj.Name, err)
			}
		}
	}
	return nil
}

func applyOne(layout *Layout, symtab *SymbolTable, obj *Object, rel *ObjectReloc, patcher Patcher) error {
	if rel.TargetSectionIdx >= len(obj.Sections) {
		return fmt.Errorf("reloc target section index %d out of range", rel.TargetSectionIdx)
	}
	inputSec := obj.Sections[rel.TargetSectionIdx]
	if inputSec == nil || inputSec.Skip {
		return nil
	}

	outSec, ok := layout.SectionByName(inputSec.Name)
	if !ok {
		return nil
	}

	var pieceOff uint64
	found := false
	for _, p := range outSec.Pieces {
		if p.Obj == obj && p.Sec == inputSec {
			pieceOff = p.Offset
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
	S, err := resolveRelocSym(layout, rel, obj, symtab)
	if err != nil {
		return err
	}
	return patcher.Apply(outSec.Data, patchOff, rel.Type, P, uint64(S), rel.Addend)
}

func resolveRelocSym(layout *Layout, rel *ObjectReloc, obj *Object, symtab *SymbolTable) (int64, error) {
	if rel.SymIdx == 0 {
		return 0, nil
	}
	if int(rel.SymIdx) >= len(obj.Symbols) {
		return 0, fmt.Errorf("reloc symbol index %d out of range", rel.SymIdx)
	}
	raw := obj.Symbols[rel.SymIdx]
	if raw == nil || raw.Name == "" {
		return 0, nil
	}

	if raw.StorageClass.IsLocal() {
		if raw.SectionName == "" {
			return int64(raw.Value), nil
		}
		ms, ok := layout.SectionByName(raw.SectionName)
		if !ok {
			return 0, fmt.Errorf("local symbol %q: output section %q not found", raw.Name, raw.SectionName)
		}
		for _, p := range ms.Pieces {
			if p.Obj == obj && p.Sec.Name == raw.SectionName {
				return int64(ms.VAddr + p.Offset + raw.Value), nil
			}
		}
		return int64(ms.VAddr + raw.Value), nil
	}

	sym := symtab.Lookup(raw.Name)
	if sym == nil {
		if raw.StorageClass.IsWeak() {
			return 0, nil
		}
		return 0, fmt.Errorf("undefined symbol %q", raw.Name)
	}
	switch sym.Kind {
	case kindDefined, kindCommon, kindShared, kindImport:
		return int64(sym.VAddr), nil
	case kindUndefined:
		if sym.Weak {
			return 0, nil
		}
		return 0, fmt.Errorf("undefined symbol %q", raw.Name)
	}
	return 0, fmt.Errorf("symbol %q in unexpected state", raw.Name)
}