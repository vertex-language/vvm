package inlineasm

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/lower/x86/mcode"
	"github.com/vertex-language/vvm/ir/vir"
)

type attDialect struct{ resolver SymbolResolver }

// attMemRe implements the att-mem grammar (§1.1):
//
//	("-")? int-literal? "(" (reg-ident)? ("," reg-ident ("," int-literal)?)? ")"
var attMemRe = regexp.MustCompile(
	`^(-)?([0-9]+)?` +
		`\(\s*%?([A-Za-z][A-Za-z0-9]*)?\s*` +
		`(?:,\s*%?([A-Za-z][A-Za-z0-9]*)\s*(?:,\s*([0-9]+))?)?\s*\)$`)

var attBaseMnemonics = map[string]bool{
	"mov": true, "add": true, "sub": true, "and": true, "or": true, "xor": true,
	"cmp": true, "test": true, "lea": true, "imul": true, "mul": true,
	"not": true, "neg": true, "div": true, "idiv": true, "inc": true, "dec": true,
	"push": true, "pop": true, "shl": true, "shr": true, "sar": true,
	"rol": true, "ror": true, "bswap": true,
}

// stripATTSizeSuffix strips AT&T's trailing operand-size letter (movl,
// addl, ...). Only the 32-bit ('l') forms are lowered; 8/16-bit ('b'/'w')
// are TODO and 64-bit ('q') is rejected outright — this backend is x86
// (32-bit) only.
func stripATTSizeSuffix(m string) (string, error) {
	if attBaseMnemonics[m] || len(m) < 2 {
		return m, nil
	}
	suffix := m[len(m)-1]
	base := m[:len(m)-1]
	if !attBaseMnemonics[base] {
		return m, nil // jmp/jcc/int/nop/cdq/syscall etc. carry no size suffix
	}
	switch suffix {
	case 'l':
		return base, nil
	case 'b', 'w':
		return "", fmt.Errorf("asm: %q (%d-bit) not yet lowered; only 32-bit (%q) forms are supported (TODO)", m, map[byte]int{'b': 8, 'w': 16}[suffix], base+"l")
	case 'q':
		return "", fmt.Errorf("asm: %q (64-bit) not supported; this backend only lowers 32-bit x86", m)
	}
	return m, nil
}

func (d attDialect) parseMem(text string) (mcode.Opr, error) {
	text = strings.TrimSpace(text)
	m := attMemRe.FindStringSubmatch(text)
	if m == nil {
		return mcode.Opr{}, fmt.Errorf("asm: %q is not a valid AT&T memory operand (§1.1 att-mem)", text)
	}
	var disp int64
	if m[2] != "" {
		var err error
		disp, err = strconv.ParseInt(m[2], 10, 32)
		if err != nil {
			return mcode.Opr{}, fmt.Errorf("asm: bad displacement in %q: %w", text, err)
		}
		if m[1] == "-" {
			disp = -disp
		}
	}
	base := mcode.RNone
	baseWidth := 32
	if m[3] != "" {
		r, w, err := resolveRegister(m[3])
		if err != nil {
			return mcode.Opr{}, err
		}
		base, baseWidth = r, w
	}
	if baseWidth != 32 {
		return mcode.Opr{}, fmt.Errorf("asm: base register in %q is %d-bit; this backend only lowers 32-bit x86", text, baseWidth)
	}
	if m[4] == "" {
		if base == mcode.RNone {
			return mcode.Opr{}, fmt.Errorf("asm: %q has neither a base nor an index register", text)
		}
		return mcode.Mem(base, int32(disp)), nil
	}
	idx, iw, err := resolveRegister(m[4])
	if err != nil {
		return mcode.Opr{}, err
	}
	if iw != 32 {
		return mcode.Opr{}, fmt.Errorf("asm: index register in %q is %d-bit; this backend only lowers 32-bit x86", text, iw)
	}
	scale := int64(1)
	if m[5] != "" {
		scale, err = strconv.ParseInt(m[5], 10, 8)
		if err != nil {
			return mcode.Opr{}, fmt.Errorf("asm: bad scale in %q: %w", text, err)
		}
	}
	return mcode.MemIndexed(base, idx, byte(scale), int32(disp)), nil
}

func (d attDialect) Register(name string) (mcode.Reg, int, bool) {
	r, w, err := resolveRegister(name)
	return r, w, err == nil
}

func (d attDialect) resolveOperand(o vir.AsmOperand) (mcode.Opr, error) {
	switch o.Kind {
	case vir.AsmOperandKindRegister:
		r, w, err := resolveRegister(o.Register)
		if err != nil {
			return mcode.Opr{}, err
		}
		if w != 32 {
			return mcode.Opr{}, fmt.Errorf("asm: register %%%s is %d-bit; this backend only lowers 32-bit x86", o.Register, w)
		}
		return mcode.R(r), nil
	case vir.AsmOperandKindImmediate:
		return resolveImmediate(o.Immediate, d.resolver)
	case vir.AsmOperandKindMemory:
		return d.parseMem(o.Memory)
	}
	return mcode.Opr{}, fmt.Errorf("asm: a label operand was used where a value operand was expected")
}

// Lower reorders AT&T's src-first operands into the canonical (dst, src)
// order lowerMnemonic expects, then delegates to it.
func (d attDialect) Lower(line vir.AsmCodeLine, label func(string) string) ([]mcode.Inst, error) {
	if cc, ok := jccTable[line.Mnemonic]; ok {
		return lowerJump(line, true, cc, label)
	}
	if line.Mnemonic == "jmp" {
		return lowerJump(line, false, 0, label)
	}
	mnemonic, err := stripATTSizeSuffix(line.Mnemonic)
	if err != nil {
		return nil, err
	}
	ops := make([]mcode.Opr, len(line.Operands))
	for i, o := range line.Operands {
		r, err := d.resolveOperand(o)
		if err != nil {
			return nil, err
		}
		ops[i] = r
	}
	switch {
	case len(ops) == 2:
		ops[0], ops[1] = ops[1], ops[0]
	case len(ops) == 3 && mnemonic == "imul":
		// AT&T: imul $imm, src, dst -> canonical dst, src, imm
		ops[0], ops[1], ops[2] = ops[2], ops[1], ops[0]
	}
	return lowerMnemonic(mnemonic, ops)
}