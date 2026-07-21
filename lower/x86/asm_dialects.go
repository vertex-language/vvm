package x86

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	isax86 "github.com/vertex-language/vvm/isa/x86"
	"github.com/vertex-language/vvm/ir/vir"
)

// --- Intel -------------------------------------------------------------

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

func (d intelDialect) parseMem(text string) (Opr, error) {
	m := intelMemRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return Opr{}, fmt.Errorf("asm: %q is not a valid intel memory operand (§1.1 intel-mem)", text)
	}
	if sz, ok := intelSizeBits[m[1]]; ok && sz > 32 {
		return Opr{}, fmt.Errorf("asm: %s-bit memory operand %q not supported by this 32-bit backend", m[1], text)
	}
	base, w, err := resolveRegister(m[2])
	if err != nil {
		return Opr{}, err
	}
	if w != 32 {
		return Opr{}, fmt.Errorf("asm: base register %q in %q is %d-bit; this backend only lowers 32-bit x86", m[2], text, w)
	}
	opr := Mem(base, 0)
	if m[3] != "" {
		idx, iw, err := resolveRegister(m[3])
		if err != nil {
			return Opr{}, err
		}
		if iw != 32 {
			return Opr{}, fmt.Errorf("asm: index register %q in %q is %d-bit; this backend only lowers 32-bit x86", m[3], text, iw)
		}
		scale := int64(1)
		if m[4] != "" {
			scale, err = strconv.ParseInt(m[4], 10, 8)
			if err != nil {
				return Opr{}, fmt.Errorf("asm: bad scale in %q: %w", text, err)
			}
		}
		opr = MemIndexed(base, idx, byte(scale), 0)
	}
	if m[5] != "" {
		disp, err := strconv.ParseInt(m[6], 10, 32)
		if err != nil {
			return Opr{}, fmt.Errorf("asm: bad displacement in %q: %w", text, err)
		}
		if m[5] == "-" {
			disp = -disp
		}
		opr.Disp = int32(disp)
	}
	return opr, nil
}

func (d intelDialect) Register(name string) (isax86.Reg, int, bool) {
	r, w, err := resolveRegister(name)
	return r, w, err == nil
}

func (d intelDialect) resolveOperand(o vir.AsmOperand) (Opr, error) {
	switch o.Kind {
	case vir.AsmOperandKindRegister:
		r, w, err := resolveRegister(o.Register)
		if err != nil {
			return Opr{}, err
		}
		if w != 32 {
			return Opr{}, fmt.Errorf("asm: register %q is %d-bit; this backend only lowers 32-bit x86", o.Register, w)
		}
		return R(r), nil
	case vir.AsmOperandKindImmediate:
		return resolveImmediate(o.Immediate, d.resolver)
	case vir.AsmOperandKindMemory:
		return d.parseMem(o.Memory)
	}
	return Opr{}, fmt.Errorf("asm: a label operand was used where a value operand was expected")
}

// Lower dispatches jumps directly (their operand is a bare label, not a
// value), otherwise resolves operands and delegates to the shared
// mnemonic table. Intel operand order is already (dst, src) — the
// canonical order lowerMnemonic expects — so no reordering happens here;
// attDialect is the one that has to swap.
func (d intelDialect) Lower(line vir.AsmCodeLine, label func(string) string) ([]Inst, error) {
	if cc, ok := jccTable[line.Mnemonic]; ok {
		return lowerJump(line, true, cc, label)
	}
	if line.Mnemonic == "jmp" {
		return lowerJump(line, false, 0, label)
	}
	ops := make([]Opr, len(line.Operands))
	for i, o := range line.Operands {
		r, err := d.resolveOperand(o)
		if err != nil {
			return nil, err
		}
		ops[i] = r
	}
	return lowerMnemonic(line.Mnemonic, ops)
}

// --- AT&T ----------------------------------------------------------------

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

func (d attDialect) parseMem(text string) (Opr, error) {
	text = strings.TrimSpace(text)
	m := attMemRe.FindStringSubmatch(text)
	if m == nil {
		return Opr{}, fmt.Errorf("asm: %q is not a valid AT&T memory operand (§1.1 att-mem)", text)
	}
	var disp int64
	if m[2] != "" {
		var err error
		disp, err = strconv.ParseInt(m[2], 10, 32)
		if err != nil {
			return Opr{}, fmt.Errorf("asm: bad displacement in %q: %w", text, err)
		}
		if m[1] == "-" {
			disp = -disp
		}
	}
	base := isax86.RNone
	baseWidth := 32
	if m[3] != "" {
		r, w, err := resolveRegister(m[3])
		if err != nil {
			return Opr{}, err
		}
		base, baseWidth = r, w
	}
	if baseWidth != 32 {
		return Opr{}, fmt.Errorf("asm: base register in %q is %d-bit; this backend only lowers 32-bit x86", text, baseWidth)
	}
	if m[4] == "" {
		if base == isax86.RNone {
			return Opr{}, fmt.Errorf("asm: %q has neither a base nor an index register", text)
		}
		return Mem(base, int32(disp)), nil
	}
	idx, iw, err := resolveRegister(m[4])
	if err != nil {
		return Opr{}, err
	}
	if iw != 32 {
		return Opr{}, fmt.Errorf("asm: index register in %q is %d-bit; this backend only lowers 32-bit x86", text, iw)
	}
	scale := int64(1)
	if m[5] != "" {
		scale, err = strconv.ParseInt(m[5], 10, 8)
		if err != nil {
			return Opr{}, fmt.Errorf("asm: bad scale in %q: %w", text, err)
		}
	}
	return MemIndexed(base, idx, byte(scale), int32(disp)), nil
}

func (d attDialect) Register(name string) (isax86.Reg, int, bool) {
	r, w, err := resolveRegister(name)
	return r, w, err == nil
}

func (d attDialect) resolveOperand(o vir.AsmOperand) (Opr, error) {
	switch o.Kind {
	case vir.AsmOperandKindRegister:
		r, w, err := resolveRegister(o.Register)
		if err != nil {
			return Opr{}, err
		}
		if w != 32 {
			return Opr{}, fmt.Errorf("asm: register %%%s is %d-bit; this backend only lowers 32-bit x86", o.Register, w)
		}
		return R(r), nil
	case vir.AsmOperandKindImmediate:
		return resolveImmediate(o.Immediate, d.resolver)
	case vir.AsmOperandKindMemory:
		return d.parseMem(o.Memory)
	}
	return Opr{}, fmt.Errorf("asm: a label operand was used where a value operand was expected")
}

// Lower reorders AT&T's src-first operands into the canonical (dst, src)
// order lowerMnemonic expects, then delegates to it.
func (d attDialect) Lower(line vir.AsmCodeLine, label func(string) string) ([]Inst, error) {
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
	ops := make([]Opr, len(line.Operands))
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