package inlineasm

import (
	"fmt"

	"github.com/vertex-language/vvm/lower/x86/mcode"
	"github.com/vertex-language/vvm/ir/vir"
)

// resolveImmediate converts a parsed asm-immediate's underlying vir.Operand
// into an mcode.Opr. Ident operands are resolved through the caller's
// SymbolResolver (§4 Addresses); floats aren't yet supported in a raw
// operand position (TODO).
func resolveImmediate(o vir.Operand, resolver SymbolResolver) (mcode.Opr, error) {
	switch o.Kind {
	case vir.OperandInt:
		return mcode.Imm(o.Int), nil
	case vir.OperandBool:
		if o.Bool {
			return mcode.Imm(1), nil
		}
		return mcode.Imm(0), nil
	case vir.OperandNull:
		return mcode.Imm(0), nil
	case vir.OperandIdent:
		if resolver == nil {
			return mcode.Opr{}, fmt.Errorf("asm: %q used as an operand needs module context to resolve", o.Ident)
		}
		return resolver.Resolve(o.Ident)
	case vir.OperandFloat:
		return mcode.Opr{}, fmt.Errorf("asm: floating-point immediates are not lowered by this backend (TODO)")
	}
	return mcode.Opr{}, fmt.Errorf("asm: operand kind not legal in this position")
}

// jccTable maps jump mnemonics (shared across both dialects — condition
// mnemonics don't vary by syntax dialect) to condition codes.
var jccTable = map[string]byte{
	"je": mcode.CondE, "jz": mcode.CondE,
	"jne": mcode.CondNE, "jnz": mcode.CondNE,
	"jl": mcode.CondL, "jnge": mcode.CondL,
	"jle": mcode.CondLE, "jng": mcode.CondLE,
	"jg": mcode.CondG, "jnle": mcode.CondG,
	"jge": mcode.CondGE, "jnl": mcode.CondGE,
	"jb": mcode.CondB, "jc": mcode.CondB, "jnae": mcode.CondB,
	"jbe": mcode.CondBE, "jna": mcode.CondBE,
	"ja": mcode.CondA, "jnbe": mcode.CondA,
	"jae": mcode.CondAE, "jnc": mcode.CondAE, "jnb": mcode.CondAE,
	"jo": mcode.CondO, "jno": mcode.CondNO,
	"js": mcode.CondS, "jns": mcode.CondNS,
}

func lowerJump(line vir.AsmCodeLine, isJcc bool, cc byte, label func(string) string) ([]mcode.Inst, error) {
	if len(line.Operands) != 1 || line.Operands[0].Kind != vir.AsmOperandKindLabel {
		return nil, fmt.Errorf("asm: %q expects a single local-label operand (§9.39)", line.Mnemonic)
	}
	lbl := label(line.Operands[0].Label)
	if isJcc {
		return []mcode.Inst{{Op: "jcc", CC: cc, Lbl: lbl}}, nil
	}
	return []mcode.Inst{{Op: "jmp", Lbl: lbl}}, nil
}

// lowerMnemonic implements the shared, dialect-independent instruction
// semantics once operands have been parsed and reordered into canonical
// (dst, src) form by the calling dialect. This is intentionally a modest,
// curated mnemonic table covering the common integer instructions that
// appear in real-world inline asm — a full per-dialect mnemonic/operand-
// shape table (§9.38) is otherwise TODO, as the verifier itself notes.
func lowerMnemonic(mnemonic string, ops []mcode.Opr) ([]mcode.Inst, error) {
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
		return []mcode.Inst{{Op: "mov", D: ops[0], S: ops[1], Sz: 4}}, nil

	case "add", "sub", "and", "or", "xor", "cmp":
		if err := need(2); err != nil {
			return nil, err
		}
		return []mcode.Inst{{Op: mnemonic, D: ops[0], S: ops[1]}}, nil

	case "test":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[0].Kind != mcode.OReg || ops[1].Kind != mcode.OReg {
			return nil, fmt.Errorf("asm: test requires two registers")
		}
		return []mcode.Inst{{Op: "test", D: ops[0], S: ops[1]}}, nil

	case "lea":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[1].Kind != mcode.OMem {
			return nil, fmt.Errorf("asm: lea's source must be a memory operand")
		}
		return []mcode.Inst{{Op: "lea", D: ops[0], S: ops[1]}}, nil

	case "imul":
		switch len(ops) {
		case 1:
			return []mcode.Inst{{Op: "imul32", S: ops[0]}}, nil
		case 2:
			return []mcode.Inst{{Op: "imul", D: ops[0], S: ops[1]}}, nil
		case 3:
			if ops[2].Kind != mcode.OImm {
				return nil, fmt.Errorf("asm: imul's third operand must be an immediate")
			}
			return []mcode.Inst{{Op: "imul3", D: ops[0], S: ops[1], Imm: ops[2].Imm}}, nil
		}
		return nil, fmt.Errorf("asm: %q expects 1, 2, or 3 operands, got %d", mnemonic, len(ops))

	case "mul":
		if err := need(1); err != nil {
			return nil, err
		}
		return []mcode.Inst{{Op: "mul32", S: ops[0]}}, nil

	case "not", "neg", "div", "idiv":
		if err := need(1); err != nil {
			return nil, err
		}
		return []mcode.Inst{{Op: mnemonic, S: ops[0]}}, nil

	case "inc", "dec", "bswap":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != mcode.OReg {
			return nil, fmt.Errorf("asm: %q requires a register operand", mnemonic)
		}
		return []mcode.Inst{{Op: mnemonic, D: ops[0]}}, nil

	case "push":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != mcode.OReg && ops[0].Kind != mcode.OImm {
			return nil, fmt.Errorf("asm: push requires a register or immediate operand")
		}
		return []mcode.Inst{{Op: "push", S: ops[0]}}, nil

	case "pop":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != mcode.OReg {
			return nil, fmt.Errorf("asm: pop requires a register operand")
		}
		return []mcode.Inst{{Op: "pop", D: ops[0]}}, nil

	case "shl", "shr", "sar", "rol", "ror":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[1].Kind == mcode.OImm {
			return []mcode.Inst{{Op: mnemonic, D: ops[0], S: ops[1], Sz: 4}}, nil
		}
		if ops[1].Kind != mcode.OReg || ops[1].Reg != mcode.RECX {
			return nil, fmt.Errorf("asm: %q's shift count must be an immediate or cl/%%cl", mnemonic)
		}
		return []mcode.Inst{{Op: mnemonic, D: ops[0], Sz: 4}}, nil

	case "int":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != mcode.OImm {
			return nil, fmt.Errorf("asm: int requires an immediate operand")
		}
		return []mcode.Inst{{Op: "int", Imm: ops[0].Imm}}, nil

	case "nop":
		if err := need(0); err != nil {
			return nil, err
		}
		return []mcode.Inst{{Op: "nop"}}, nil

	case "cdq":
		if err := need(0); err != nil {
			return nil, err
		}
		return []mcode.Inst{{Op: "cdq"}}, nil

	case "syscall", "sysenter":
		return nil, fmt.Errorf("asm: %q has no 32-bit x86 encoding; use `int 0x80` for a Linux/*BSD-style trap", mnemonic)
	}
	return nil, fmt.Errorf("asm: mnemonic %q not lowered by this backend (§9.38 mnemonic table is a curated subset)", mnemonic)
}