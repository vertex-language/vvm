package inlineasm

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/lower/x86_64/mcode"
)

// attSizeSuffixed marks mnemonics whose AT&T spelling carries a b/w/l/q
// size suffix (movl, addq, ...); the bare mnemonic is what's looked up in
// common.go's tables.
func stripSizeSuffix(m string) string {
	if len(m) > 1 {
		switch m[len(m)-1] {
		case 'b', 'w', 'l', 'q':
			return m[:len(m)-1]
		}
	}
	return m
}

// twoOperandMnemonics tracks which mnemonics take exactly the reorderable
// dst,src shape in AT&T's src-first convention (jumps and single/zero
// -operand mnemonics are left alone).
func isTwoOperandMnemonic(bare string) bool {
	if twoOpALU[bare] || shiftMnem[bare] != "" {
		return true
	}
	switch bare {
	case "mov", "lea", "test", "imul", "xchg":
		return true
	}
	return false
}

// attReorder flips AT&T's src,dst operand order to the canonical dst,src
// order the rest of this package assumes.
func attReorder(mnemonic string, ops []vir.AsmOperand) []vir.AsmOperand {
	bare := stripSizeSuffix(strings.ToLower(mnemonic))
	if len(ops) != 2 || !isTwoOperandMnemonic(bare) {
		return ops
	}
	return []vir.AsmOperand{ops[1], ops[0]}
}

// parseATTMem supports "disp(reg)" / "(reg)" / "-disp(reg)" — the same
// narrow, no-index subset intel.go supports. TODO: index/scale terms.
func parseATTMem(text, arch string) (mcode.Reg, int32, error) {
	s := strings.TrimSpace(text)
	open := strings.Index(s, "(")
	close := strings.LastIndex(s, ")")
	if open < 0 || close < 0 || close < open {
		return mcode.RNone, 0, fmt.Errorf("asm: malformed AT&T memory operand %q", text)
	}
	dispText := strings.TrimSpace(s[:open])
	inner := s[open+1 : close]
	if strings.Contains(inner, ",") {
		return mcode.RNone, 0, fmt.Errorf("asm: indexed/scaled memory operands not yet supported (TODO): %q", text)
	}
	regName := strings.TrimSpace(strings.TrimPrefix(inner, "%"))
	r, _, ok := Register(arch, regName)
	if !ok {
		return mcode.RNone, 0, fmt.Errorf("asm: unknown base register %q in memory operand %q", regName, text)
	}
	if dispText == "" {
		return r, 0, nil
	}
	v, err := strconv.ParseInt(dispText, 10, 32)
	if err != nil {
		return mcode.RNone, 0, fmt.Errorf("asm: bad displacement in memory operand %q: %w", text, err)
	}
	return r, int32(v), nil
}