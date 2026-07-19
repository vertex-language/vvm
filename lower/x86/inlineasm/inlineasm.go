// Package inlineasm lowers a verified vir.AsmBlock (§4) into an mcode.Inst
// sequence for this 32-bit x86 lower. It supports the two x86 dialects
// (intel, att); a32/t32/native have no x86 meaning and are never reached
// here — vir.IsDialectValidForArchitecture (§9.34) rejects them upstream,
// before Verify even succeeds.
package inlineasm

import (
	"fmt"

	"github.com/vertex-language/vvm/lower/x86/mcode"
	"github.com/vertex-language/vvm/ir/vir"
)

// SymbolResolver lets the lowerer resolve an ident used in an asm-line
// immediate position back to the enclosing module's compile-time entities,
// exactly like an ordinary instruction operand would (§4 Addresses): a
// global/fn/extern-fn name yields its address, a const name yields its
// value, and anything else is treated as a local value's home slot.
type SymbolResolver interface {
	Resolve(ident string) (mcode.Opr, error)
}

// Dialect is the per-syntax-dialect plugin: register/addressing parsing
// plus operand reordering for one asm-line grammar (§1.1, §4). The actual
// per-mnemonic instruction semantics are dialect-independent and live in
// common.go's lowerMnemonic — only surface syntax and operand order differ
// between Intel and AT&T.
type Dialect interface {
	Register(name string) (r mcode.Reg, widthBits int, ok bool)
	Lower(line vir.AsmCodeLine, label func(string) string) ([]mcode.Inst, error)
}

var dialectFactories = map[vir.AsmDialect]func(SymbolResolver) Dialect{
	vir.DialectIntel: func(r SymbolResolver) Dialect { return intelDialect{resolver: r} },
	vir.DialectATT:   func(r SymbolResolver) Dialect { return attDialect{resolver: r} },
}

// LowerBlock translates one verified vir.AsmBlock into an mcode.Inst
// sequence: it materializes `in` bindings from their value slots into the
// bound physical registers, lowers the code body via the module's asm
// dialect, and writes `out` bindings back to their value slots. `clobber`
// bindings need no code — the frame gives every vir value its own stack
// slot (no cross-block register residency), so a clobbered physical
// register holds nothing the rest of the function depends on surviving.
// Per §9.41, the block is a full optimization/memory barrier; that
// property falls out for free here because isel emits this whole sequence
// as one indivisible span and every live value crosses the boundary
// through its own memory slot, so nothing needs explicit fencing at this
// layer. uniqueLabel maps an asm-local label name to a function-unique
// mcode label (§9.39 scoping).
func LowerBlock(dialect vir.AsmDialect, arch string, a *vir.AsmBlock, resolver SymbolResolver, uniqueLabel func(string) string) ([]mcode.Inst, error) {
	if arch != "x86" {
		return nil, fmt.Errorf("asm: inline assembly is only lowered for arch \"x86\" (32-bit); got %q", arch)
	}
	factory, ok := dialectFactories[dialect]
	if !ok {
		return nil, fmt.Errorf("asm: no x86 lowering for dialect %q", dialect)
	}
	d := factory(resolver)

	var insts []mcode.Inst
	boundOut := map[string]mcode.Reg{}
	var outOrder []string

	for _, bind := range a.Bindings {
		switch bind.Kind {
		case vir.BindingIn:
			r, w, ok := d.Register(bind.Register)
			if !ok {
				return nil, fmt.Errorf("asm: register %q not recognized by this backend (§9.35)", bind.Register)
			}
			if w != 32 {
				return nil, fmt.Errorf("asm: register %q is %d-bit; this backend only lowers 32-bit x86", bind.Register, w)
			}
			insts = append(insts, mcode.Inst{Op: "mov", D: mcode.R(r), S: mcode.Slot(bind.Ident), Sz: 4})
		case vir.BindingOut:
			r, w, ok := d.Register(bind.Register)
			if !ok {
				return nil, fmt.Errorf("asm: register %q not recognized by this backend (§9.35)", bind.Register)
			}
			if w != 32 {
				return nil, fmt.Errorf("asm: register %q is %d-bit; this backend only lowers 32-bit x86", bind.Register, w)
			}
			boundOut[bind.Ident] = r
			outOrder = append(outOrder, bind.Ident)
		case vir.BindingClobber:
			// No code needed — see doc comment above.
		}
	}

	for _, line := range a.Code {
		if line.LabelDeclaration != "" {
			insts = append(insts, mcode.Inst{Op: "label", Lbl: uniqueLabel(line.LabelDeclaration)})
			continue
		}
		li, err := d.Lower(line, uniqueLabel)
		if err != nil {
			return nil, err
		}
		insts = append(insts, li...)
	}

	for _, ident := range outOrder {
		insts = append(insts, mcode.Inst{Op: "mov", D: mcode.Slot(ident), S: mcode.R(boundOut[ident]), Sz: 4})
	}
	return insts, nil
}