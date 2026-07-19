package inlineasm

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/lower/x86_64/mcode"
)

// parseIntelMem supports the narrow subset "[reg]" / "[reg+disp]" /
// "[reg-disp]" of the full intel-mem grammar (§1.1). Index/scale terms and
// the optional ptr-size prefix are rejected explicitly — mcode.Opr has no
// SIB-index field yet. TODO.
func parseIntelMem(text, arch string) (mcode.Reg, int32, error) {
	s := strings.TrimSpace(text)
	if i := strings.LastIndex(s, "]"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "["); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSpace(s)
	if strings.Contains(s, "*") {
		return mcode.RNone, 0, fmt.Errorf("asm: indexed/scaled memory operands not yet supported (TODO): %q", text)
	}

	regName, dispText := s, ""
	if i := strings.IndexAny(s, "+-"); i > 0 {
		regName, dispText = s[:i], s[i:]
	}
	regName = strings.TrimSpace(regName)
	r, _, ok := Register(arch, regName)
	if !ok {
		return mcode.RNone, 0, fmt.Errorf("asm: unknown base register %q in memory operand %q", regName, text)
	}
	if dispText == "" {
		return r, 0, nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(dispText), 10, 32)
	if err != nil {
		return mcode.RNone, 0, fmt.Errorf("asm: bad displacement in memory operand %q: %w", text, err)
	}
	return r, int32(v), nil
}