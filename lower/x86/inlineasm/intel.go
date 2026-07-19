package inlineasm

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/lower/x86/mcode"
	"github.com/vertex-language/vvm/ir/vir"
)

type intelDialect struct{ resolver SymbolResolver }

// intelMemRe implements the intel-mem grammar (§1.1):
//
//	(ptr-size "ptr")? "[" reg-ident ("+" reg-ident ("*" int-literal)?)? (("+"|"-") int-literal)? "]"
var intelMemRe = regexp.MustCompile(
	`^(?:(byte|word|dword|qword|xmmword|ymmword|zmmword)\s+ptr\s+)?` +
		`\[\s*([A-Za-z][A-Za-z0-9]*)\s*` +
		`(?:\+\s*([A-Za-z][A-Za-z0-9]*)\s*(?:\*\s*([0-9]+))?)?\s*` +
		`(?:([+-])\s*([0-9]+))?\s*\]$`)

var intelSizeBits = map[string]int{"byte": 8, "word": 16, "dword": 32, "qword": 64, "xmmword": 128, "ymmword": 256, "zmmword": 512}

func (d intelDialect) parseMem(text string) (mcode.Opr, error) {
	m := intelMemRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return mcode.Opr{}, fmt.Errorf("asm: %q is not a valid intel memory operand (§1.1 intel-mem)", text)
	}
	if sz, ok := intelSizeBits[m[1]]; ok && sz > 32 {
		return mcode.Opr{}, fmt.Errorf("asm: %s-bit memory operand %q not supported by this 32-bit backend", m[1], text)
	}
	base, w, err := resolveRegister(m[2])
	if err != nil {
		return mcode.Opr{}, err
	}
	if w != 32 {
		return mcode.Opr{}, fmt.Errorf("asm: base register %q in %q is %d-bit; this backend only lowers 32-bit x86", m[2], text, w)
	}
	opr := mcode.Mem(base, 0)
	if m[3] != "" {
		idx, iw, err := resolveRegister(m[3])
		if err != nil {
			return mcode.Opr{}, err
		}
		if iw != 32 {
			return mcode.Opr{}, fmt.Errorf("asm: index register %q in %q is %d-bit; this backend only lowers 32-bit x86", m[3], text, iw)
		}
		scale := int64(1)
		if m[4] != "" {
			scale, err = strconv.ParseInt(m[4], 10, 8)
			if err != nil {
				return mcode.Opr{}, fmt.Errorf("asm: bad scale in %q: %w", text, err)
			}
		}
		opr = mcode.MemIndexed(base, idx, byte(scale), 0)
	}
	if m[5] != "" {
		disp, err := strconv.ParseInt(m[6], 10, 32)
		if err != nil {
			return mcode.Opr{}, fmt.Errorf("asm: bad displacement in %q: %w", text, err)
		}
		if m[5] == "-" {
			disp = -disp
		}
		opr.Disp = int32(disp)
	}
	return opr, nil
}

func (d intelDialect) Register(name string) (mcode.Reg, int, bool) {
	r, w, err := resolveRegister(name)
	return r, w, err == nil
}

func (d intelDialect) resolveOperand(o vir.AsmOperand) (mcode.Opr, error) {
	switch o.Kind {
	case vir.AsmOperandKindRegister:
		r, w, err := resolveRegister(o.Register)
		if err != nil {
			return mcode.Opr{}, err
		}
		if w != 32 {
			return mcode.Opr{}, fmt.Errorf("asm: register %q is %d-bit; this backend only lowers 32-bit x86", o.Register, w)
		}
		return mcode.R(r), nil
	case vir.AsmOperandKindImmediate:
		return resolveImmediate(o.Immediate, d.resolver)
	case vir.AsmOperandKindMemory:
		return d.parseMem(o.Memory)
	}
	return mcode.Opr{}, fmt.Errorf("asm: a label operand was used where a value operand was expected")
}

// Lower dispatches jumps directly (their operand is a bare label, not a
// value), otherwise resolves operands and delegates to the shared mnemonic
// table. Intel operand order is already (dst, src) — the same canonical
// order lowerMnemonic expects — so no reordering happens here; att.go is
// the one that has to swap.
func (d intelDialect) Lower(line vir.AsmCodeLine, label func(string) string) ([]mcode.Inst, error) {
	if cc, ok := jccTable[line.Mnemonic]; ok {
		return lowerJump(line, true, cc, label)
	}
	if line.Mnemonic == "jmp" {
		return lowerJump(line, false, 0, label)
	}
	ops := make([]mcode.Opr, len(line.Operands))
	for i, o := range line.Operands {
		r, err := d.resolveOperand(o)
		if err != nil {
			return nil, err
		}
		ops[i] = r
	}
	return lowerMnemonic(line.Mnemonic, ops)
}