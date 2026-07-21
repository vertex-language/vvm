// asm.go lowers a verified vir.AsmBlock into an Inst sequence. It supports
// the two x86 dialects (intel, att); a32/t32/native have no x86 meaning
// and are never reached here — vir.IsDialectValidForArchitecture rejects
// them upstream, before Verify even succeeds.
package x86

import (
	"fmt"
	"strings"

	isax86 "github.com/vertex-language/vvm/isa/x86"
	"github.com/vertex-language/vvm/ir/vir"
)

// SymbolResolver lets the asm lowerer resolve an ident used in an asm-line
// immediate position back to the enclosing module's compile-time
// entities, exactly like an ordinary instruction operand would: a
// global/fn/extern-fn name yields its address, a const name yields its
// value, and anything else is treated as a local value's home slot.
type SymbolResolver interface {
	Resolve(ident string) (Opr, error)
}

// asmDialect is the per-syntax-dialect plugin: register/addressing
// parsing plus operand reordering for one asm-line grammar. The actual
// per-mnemonic instruction semantics are dialect-independent and live in
// lowerMnemonic below — only surface syntax and operand order differ
// between Intel and AT&T.
type asmDialect interface {
	Register(name string) (r isax86.Reg, widthBits int, ok bool)
	Lower(line vir.AsmCodeLine, label func(string) string) ([]Inst, error)
}

var dialectFactories = map[vir.AsmDialect]func(SymbolResolver) asmDialect{
	vir.DialectIntel: func(r SymbolResolver) asmDialect { return intelDialect{resolver: r} },
	vir.DialectATT:   func(r SymbolResolver) asmDialect { return attDialect{resolver: r} },
}

// LowerBlock translates one verified vir.AsmBlock into an Inst sequence:
// it materializes `in` bindings from their value slots into the bound
// physical registers, lowers the code body via the module's asm dialect,
// and writes `out` bindings back to their value slots. `clobber` bindings
// need no code — the frame gives every vir value its own stack slot (no
// cross-block register residency), so a clobbered physical register holds
// nothing the rest of the function depends on surviving. Per §9.41, the
// block is a full optimization/memory barrier; that property falls out
// for free here because isel emits this whole sequence as one indivisible
// span and every live value crosses the boundary through its own memory
// slot. uniqueLabel maps an asm-local label name to a function-unique
// label (§9.39 scoping).
func LowerBlock(dialect vir.AsmDialect, arch string, a *vir.AsmBlock, resolver SymbolResolver, uniqueLabel func(string) string) ([]Inst, error) {
	if arch != "x86" {
		return nil, fmt.Errorf("asm: inline assembly is only lowered for arch \"x86\" (32-bit); got %q", arch)
	}
	factory, ok := dialectFactories[dialect]
	if !ok {
		return nil, fmt.Errorf("asm: no x86 lowering for dialect %q", dialect)
	}
	d := factory(resolver)

	var insts []Inst
	boundOut := map[string]isax86.Reg{}
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
			insts = append(insts, Inst{Op: "mov", D: R(r), S: Slot(bind.Ident), Sz: 4})
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
			insts = append(insts, Inst{Op: "label", Lbl: uniqueLabel(line.LabelDeclaration)})
			continue
		}
		li, err := d.Lower(line, uniqueLabel)
		if err != nil {
			return nil, err
		}
		insts = append(insts, li...)
	}

	for _, ident := range outOrder {
		insts = append(insts, Inst{Op: "mov", D: Slot(ident), S: R(boundOut[ident]), Sz: 4})
	}
	return insts, nil
}

// physicalSlot maps a vir.RegisterInfo's canonical physical-slot name onto
// this 32-bit backend's isax86.Reg. Registers whose slot has no 32-bit-mode
// encoding (r8..r15) are simply absent.
var physicalSlot = map[string]isax86.Reg{
	"RAX": isax86.REAX, "RCX": isax86.RECX, "RDX": isax86.REDX, "RBX": isax86.REBX,
	"RSP": isax86.RESP, "RBP": isax86.REBP, "RSI": isax86.RESI, "RDI": isax86.REDI,
}

// resolveRegister looks a register token up in vir's own x86 register
// table — the same table the verifier already checked it against (§9.35)
// — and maps it onto this backend's physical register. It defensively
// strips a leading '%' in case the AT&T sigil survived into the IR.
func resolveRegister(name string) (r isax86.Reg, widthBits int, err error) {
	name = strings.TrimPrefix(name, "%")
	info, ok := vir.RegisterTableForArchitecture("x86")[name]
	if !ok {
		return isax86.RNone, 0, fmt.Errorf("asm: register %q is not in the x86 register table (§9.35)", name)
	}
	pr, ok := physicalSlot[info.PhysicalSlot]
	if !ok {
		return isax86.RNone, 0, fmt.Errorf("asm: register %q (physical %s) has no 32-bit encoding; this backend only lowers arch \"x86\"", name, info.PhysicalSlot)
	}
	return pr, info.WidthBits, nil
}

// resolveImmediate converts a parsed asm-immediate's underlying
// vir.Operand into an Opr. Ident operands are resolved through the
// caller's SymbolResolver (§4 Addresses); floats aren't yet supported in a
// raw operand position (TODO).
func resolveImmediate(o vir.Operand, resolver SymbolResolver) (Opr, error) {
	switch o.Kind {
	case vir.OperandInt:
		return Imm(o.Int), nil
	case vir.OperandBool:
		if o.Bool {
			return Imm(1), nil
		}
		return Imm(0), nil
	case vir.OperandNull:
		return Imm(0), nil
	case vir.OperandIdent:
		if resolver == nil {
			return Opr{}, fmt.Errorf("asm: %q used as an operand needs module context to resolve", o.Ident)
		}
		return resolver.Resolve(o.Ident)
	case vir.OperandFloat:
		return Opr{}, fmt.Errorf("asm: floating-point immediates are not lowered by this backend (TODO)")
	}
	return Opr{}, fmt.Errorf("asm: operand kind not legal in this position")
}

// jccTable maps jump mnemonics (shared across both dialects — condition
// mnemonics don't vary by syntax dialect) to isax86 condition codes.
var jccTable = map[string]byte{
	"je": isax86.CondE, "jz": isax86.CondE,
	"jne": isax86.CondNE, "jnz": isax86.CondNE,
	"jl": isax86.CondL, "jnge": isax86.CondL,
	"jle": isax86.CondLE, "jng": isax86.CondLE,
	"jg": isax86.CondG, "jnle": isax86.CondG,
	"jge": isax86.CondGE, "jnl": isax86.CondGE,
	"jb": isax86.CondB, "jc": isax86.CondB, "jnae": isax86.CondB,
	"jbe": isax86.CondBE, "jna": isax86.CondBE,
	"ja": isax86.CondA, "jnbe": isax86.CondA,
	"jae": isax86.CondAE, "jnc": isax86.CondAE, "jnb": isax86.CondAE,
	"jo": isax86.CondO, "jno": isax86.CondNO,
	"js": isax86.CondS, "jns": isax86.CondNS,
}

func lowerJump(line vir.AsmCodeLine, isJcc bool, cc byte, label func(string) string) ([]Inst, error) {
	if len(line.Operands) != 1 || line.Operands[0].Kind != vir.AsmOperandKindLabel {
		return nil, fmt.Errorf("asm: %q expects a single local-label operand (§9.39)", line.Mnemonic)
	}
	lbl := label(line.Operands[0].Label)
	if isJcc {
		return []Inst{{Op: "jcc", CC: cc, Lbl: lbl}}, nil
	}
	return []Inst{{Op: "jmp", Lbl: lbl}}, nil
}

// lowerMnemonic implements the shared, dialect-independent instruction
// semantics once operands have been parsed and reordered into canonical
// (dst, src) form by the calling dialect. This is intentionally a modest,
// curated mnemonic table covering the common integer instructions that
// appear in real-world inline asm — a full per-dialect mnemonic/operand-
// shape table (§9.38) is otherwise TODO, as the verifier itself notes.
func lowerMnemonic(mnemonic string, ops []Opr) ([]Inst, error) {
	need := func(n int) error {
		if len(ops) != n {
			return fmt.Errorf("asm: %q expects %d operand(s), got %d", mnemonic, n, len(ops))
		}
		return nil
	}
	switch mnemonic {
	case "mov":
		if err := need(2); err != nil {
			return nil, err
		}
		return []Inst{{Op: "mov", D: ops[0], S: ops[1], Sz: 4}}, nil

	case "add", "sub", "and", "or", "xor", "cmp":
		if err := need(2); err != nil {
			return nil, err
		}
		return []Inst{{Op: mnemonic, D: ops[0], S: ops[1]}}, nil

	case "test":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg || ops[1].Kind != OReg {
			return nil, fmt.Errorf("asm: test requires two registers")
		}
		return []Inst{{Op: "test", D: ops[0], S: ops[1]}}, nil

	case "lea":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[1].Kind != OMem {
			return nil, fmt.Errorf("asm: lea's source must be a memory operand")
		}
		return []Inst{{Op: "lea", D: ops[0], S: ops[1]}}, nil

	case "imul":
		switch len(ops) {
		case 1:
			return []Inst{{Op: "imul32", S: ops[0]}}, nil
		case 2:
			return []Inst{{Op: "imul", D: ops[0], S: ops[1]}}, nil
		case 3:
			if ops[2].Kind != OImm {
				return nil, fmt.Errorf("asm: imul's third operand must be an immediate")
			}
			return []Inst{{Op: "imul3", D: ops[0], S: ops[1], Imm: ops[2].Imm}}, nil
		}
		return nil, fmt.Errorf("asm: %q expects 1, 2, or 3 operands, got %d", mnemonic, len(ops))

	case "mul":
		if err := need(1); err != nil {
			return nil, err
		}
		return []Inst{{Op: "mul32", S: ops[0]}}, nil

	case "not", "neg", "div", "idiv":
		if err := need(1); err != nil {
			return nil, err
		}
		return []Inst{{Op: mnemonic, S: ops[0]}}, nil

	case "inc", "dec", "bswap":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg {
			return nil, fmt.Errorf("asm: %q requires a register operand", mnemonic)
		}
		return []Inst{{Op: mnemonic, D: ops[0]}}, nil

	case "push":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg && ops[0].Kind != OImm {
			return nil, fmt.Errorf("asm: push requires a register or immediate operand")
		}
		return []Inst{{Op: "push", S: ops[0]}}, nil

	case "pop":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg {
			return nil, fmt.Errorf("asm: pop requires a register operand")
		}
		return []Inst{{Op: "pop", D: ops[0]}}, nil

	case "shl", "shr", "sar", "rol", "ror":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[1].Kind == OImm {
			return []Inst{{Op: mnemonic, D: ops[0], S: ops[1], Sz: 4}}, nil
		}
		if ops[1].Kind != OReg || ops[1].Reg != isax86.RECX {
			return nil, fmt.Errorf("asm: %q's shift count must be an immediate or cl/%%cl", mnemonic)
		}
		return []Inst{{Op: mnemonic, D: ops[0], Sz: 4}}, nil

	case "int":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OImm {
			return nil, fmt.Errorf("asm: int requires an immediate operand")
		}
		return []Inst{{Op: "int", Imm: ops[0].Imm}}, nil

	case "nop":
		if err := need(0); err != nil {
			return nil, err
		}
		return []Inst{{Op: "nop"}}, nil

	case "cdq":
		if err := need(0); err != nil {
			return nil, err
		}
		return []Inst{{Op: "cdq"}}, nil

	case "syscall", "sysenter":
		return nil, fmt.Errorf("asm: %q has no 32-bit x86 encoding; use `int 0x80` for a Linux/*BSD-style trap", mnemonic)
	}
	return nil, fmt.Errorf("asm: mnemonic %q not lowered by this backend (§9.38 mnemonic table is a curated subset)", mnemonic)
}