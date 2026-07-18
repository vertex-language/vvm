// patch.go — RELA relocation application pass.
package elf

import "fmt"

// Patcher applies a single RELA relocation to in-memory section data.
type Patcher interface {
	Apply(data []byte, off int, relType uint32, P, S uint64, A int64) error
}

// PatchFunc adapts a plain function to Patcher, so arch subpackages can
// register their relocation function directly without a wrapper type.
type PatchFunc func(data []byte, off int, relType uint32, P, S uint64, A int64) error

func (f PatchFunc) Apply(data []byte, off int, relType uint32, P, S uint64, A int64) error {
	return f(data, off, relType, P, S, A)
}

// PatchAll iterates every relocation in every object and writes the computed
// value into the merged output section data. Must be called after
// ResolveSymbolAddresses and PatchPLT so that all VAddrs are final.
func PatchAll(layout *Layout, symtab *SymbolTable, objects []*Object, p Patcher) error {
	for _, obj := range objects {
		for _, rel := range obj.Relocs {
			if err := applyReloc(layout, symtab, obj, rel, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyReloc(layout *Layout, symtab *SymbolTable, obj *Object, rel *ObjectReloc, p Patcher) error {
	if rel.TargetSectionIdx <= 0 || rel.TargetSectionIdx >= len(obj.Sections) {
		return nil
	}
	targetSec := obj.Sections[rel.TargetSectionIdx]
	if targetSec == nil || targetSec.Skip {
		return nil
	}

	ms, ok := layout.SectionByName(targetSec.Name)
	if !ok {
		return nil
	}
	if ms.Flags&SecBSS != 0 {
		return nil
	}

	var pieceOff uint64
	found := false
	for _, pc := range ms.Pieces {
		if pc.Obj == obj && pc.Sec == targetSec {
			pieceOff = pc.Offset
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	P := ms.VAddr + pieceOff + rel.Offset

	var S uint64
	if rel.SymIdx > 0 && int(rel.SymIdx) < len(obj.Symbols) {
		rawSym := obj.Symbols[rel.SymIdx]
		if rawSym != nil {
			switch {
			case rawSym.SectionIdx == SymSecAbs:
				S = rawSym.Value

			case rawSym.Name != "":
				if ts := symtab.Lookup(rawSym.Name); ts != nil {
					S = ts.VAddr
				} else if rawSym.SectionIdx >= 0 && rawSym.SectionIdx < len(obj.Sections) {
					symSec := obj.Sections[rawSym.SectionIdx]
					if symSec != nil {
						if symMs, ok2 := layout.SectionByName(symSec.Name); ok2 {
							for _, pc := range symMs.Pieces {
								if pc.Obj == obj && pc.Sec == symSec {
									S = symMs.VAddr + pc.Offset + rawSym.Value
									break
								}
							}
						}
					}
				}

			case rawSym.SectionIdx >= 0 && rawSym.SectionIdx < len(obj.Sections):
				symSec := obj.Sections[rawSym.SectionIdx]
				if symSec != nil {
					if symMs, ok2 := layout.SectionByName(symSec.Name); ok2 {
						for _, pc := range symMs.Pieces {
							if pc.Obj == obj && pc.Sec == symSec {
								S = symMs.VAddr + pc.Offset + rawSym.Value
								break
							}
						}
					}
				}
			}
		}
	}

	dataOff := int(pieceOff + rel.Offset)
	if dataOff < 0 || dataOff > len(ms.Data) {
		return fmt.Errorf("reloc in %s section %s offset 0x%x: patch offset %d out of bounds (section %d bytes)",
			obj.Name, targetSec.Name, rel.Offset, dataOff, len(ms.Data))
	}

	if err := p.Apply(ms.Data, dataOff, rel.Type, P, S, rel.Addend); err != nil {
		return fmt.Errorf("reloc in %s section %s offset 0x%x: %w",
			obj.Name, targetSec.Name, rel.Offset, err)
	}
	return nil
}