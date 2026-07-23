// build.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/verify"
	"github.com/vertex-language/vvm/ir/vir"
)

// Build runs the full single-module pipeline — decode, verify, lower,
// object, objectwriter, and (unless the target is Flat) link — and
// returns the finished binary. src may be .vbyte or .vir; decodeModule
// sniffs it.
//
// This is the no-imports path. A module that declares any `import` must
// go through BuildGraph/BuildModuleGraph (graph.go) instead — Build
// always runs bare verify.Verify, which has no notion of cross-module
// references at all (that's importer's entire reason to exist), so any
// qualified-ident operand in m fails Verify here rather than resolving.
func Build(src []byte, t Target) ([]byte, error) {
	m, err := decodeModule(src)
	if err != nil {
		return nil, fmt.Errorf("vvm: decode: %w", err)
	}
	return BuildModule(m, t)
}

// BuildModule is Build for a caller already holding a *vir.Module (e.g.
// hand-built via vir's own FunctionBuilder) rather than serialized
// source.
func BuildModule(m *vir.Module, t Target) ([]byte, error) {
	if len(m.Imports) > 0 {
		return nil, fmt.Errorf(
			"vvm: module %q declares %d import(s) — use BuildGraph/BuildModuleGraph, "+
				"which runs importer.Set before lowering", m.Name, len(m.Imports))
	}

	if err := verify.Verify(m); err != nil {
		return nil, fmt.Errorf("vvm: verify: %w", err)
	}

	// Entry-point resolution never mutates m (see entrypoint.go) — a
	// synthesized process-entry stub, when one is needed, is built
	// separately as its own object (crt.Stub) and linked in alongside
	// m's, not spliced into m's IR. So there's no re-Verify step here.
	entrySym, stub, err := resolveEntryPoint(m, t)
	if err != nil {
		return nil, err
	}

	f, err := t.objFormat()
	if err != nil {
		return nil, err
	}

	obj, err := toObjectBytes(m, t, f)
	if err != nil {
		return nil, err
	}

	if t.Flat {
		if stub != nil {
			return nil, fmt.Errorf(
				"vvm: %s: flat targets can't carry an auto-synthesized entry stub "+
					"(flat forbids relocations, so a separate crt object has nowhere "+
					"to be linked in) — name your entry fn \"_start\" instead", t)
		}
		return obj, nil // objectwriter's ToFlat already produced final bytes; no linker involved
	}

	l, err := newLinker([]*vir.Module{m}, t, entrySym)
	if err != nil {
		return nil, err
	}
	if err := l.AddObject("vvm_module.o", obj); err != nil {
		return nil, fmt.Errorf("vvm: add object: %w", err)
	}
	if stub != nil {
		if err := l.AddObject("vvm_crt.o", stub.Object); err != nil {
			return nil, fmt.Errorf("vvm: add crt object: %w", err)
		}
	}

	out, err := l.Link()
	if err != nil {
		return nil, fmt.Errorf("vvm: link: %w", err)
	}
	return out, nil
}