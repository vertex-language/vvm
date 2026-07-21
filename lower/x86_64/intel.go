// lower/x86_64/intel.go
package x86_64

import (
	"fmt"
	"strconv"
	"strings"

	isax86_64 "github.com/vertex-language/vvm/isa/x86_64"
)

// parseIntelMem supports "[reg]" / "[reg+disp]" / "[reg-disp]"
// — the same narrow, no-index subset att.go supports.
func parseIntelMem(text, arch string) (isax86_64.Reg, int32, error) {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return isax86_64.RNone, 0, fmt.Errorf("asm: malformed Intel memory operand %q", text)
	}
	
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if strings.Contains(inner, "*") {
		return isax86_64.RNone, 0, fmt.Errorf("asm: indexed/scaled memory operands not yet supported (TODO): %q", text)
	}

	plusIdx := strings.LastIndex(inner, "+")
	minusIdx := strings.LastIndex(inner, "-")

	splitIdx := plusIdx
	sign := 1
	if minusIdx > plusIdx {
		splitIdx = minusIdx
		sign = -1
	}

	if splitIdx < 0 {
		r, _, ok := Register(arch, inner)
		if !ok {
			return isax86_64.RNone, 0, fmt.Errorf("asm: unknown base register %q in memory operand %q", inner, text)
		}
		return r, 0, nil
	}

	regName := strings.TrimSpace(inner[:splitIdx])
	r, _, ok := Register(arch, regName)
	if !ok {
		return isax86_64.RNone, 0, fmt.Errorf("asm: unknown base register %q in memory operand %q", regName, text)
	}

	dispText := strings.TrimSpace(inner[splitIdx+1:])
	v, err := strconv.ParseInt(dispText, 0, 32)
	if err != nil {
		return isax86_64.RNone, 0, fmt.Errorf("asm: bad displacement in memory operand %q: %w", text, err)
	}

	return r, int32(int64(sign) * v), nil
}