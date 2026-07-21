package text

import (
	"fmt"
	"strconv"

	"github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// Per-dialect asm syntax seam (§4). Adding or fixing a dialect's mnemonic/
// operand/memory grammar touches only its own asm_<dialect>.go file; the
// binding/code-section shape in asm.go stays dialect-agnostic.
// ---------------------------------------------------------------------------

type dialectSyntax interface {
	parseOperand(c *lc, arch string) (vir.AsmOperand, error)
	encodeOperand(op vir.AsmOperand) string
	regIdent(reg string) string // '%' prefix for AT&T, bare elsewhere
}

var dialects = map[vir.AsmDialect]dialectSyntax{
	vir.DialectIntel:  intelSyntax{},
	vir.DialectATT:    attSyntax{},
	vir.DialectA32:    armSyntax{},
	vir.DialectT32:    armSyntax{},
	vir.DialectNative: armSyntax{},
}

func isPtrSize(s string) bool {
	switch s {
	case "byte", "word", "dword", "qword", "xmmword", "ymmword", "zmmword":
		return true
	}
	return false
}

func regTableHas(arch, name string) bool {
	t := vir.RegisterTableForArchitecture(arch)
	if t == nil {
		return false
	}
	_, ok := t[name]
	return ok
}

func parseImmValue(c *lc) (vir.Operand, error) {
	tk, ok := c.next()
	if !ok {
		return vir.Operand{}, c.l.errf("expected immediate value")
	}
	switch tk.kind {
	case tInt:
		v, err := strconv.ParseInt(tk.text, 10, 64)
		if err != nil {
			return vir.Operand{}, c.l.errf("bad integer %q", tk.text)
		}
		return vir.IntLiteral(v), nil
	case tFloat:
		v, err := strconv.ParseFloat(tk.text, 64)
		if err != nil {
			return vir.Operand{}, c.l.errf("bad float %q", tk.text)
		}
		return vir.FloatLiteral(v), nil
	case tIdent:
		return vir.Ident(tk.text), nil
	}
	return vir.Operand{}, c.l.errf("bad immediate operand %q", tk.text)
}

func literalOperand(tk tok) (vir.Operand, error) {
	switch tk.kind {
	case tInt:
		v, err := strconv.ParseInt(tk.text, 10, 64)
		if err != nil {
			return vir.Operand{}, fmt.Errorf("bad integer %q: %v", tk.text, err)
		}
		return vir.IntLiteral(v), nil
	case tFloat:
		v, err := strconv.ParseFloat(tk.text, 64)
		if err != nil {
			return vir.Operand{}, fmt.Errorf("bad float %q: %v", tk.text, err)
		}
		return vir.FloatLiteral(v), nil
	}
	return vir.Operand{}, fmt.Errorf("bad literal token %q", tk.text)
}

// readAsmMemory consumes a full memory operand (optional intel ptr-size
// prefix, then a bracketed/parenthesized expression, then an optional ARM
// '!' writeback marker) and returns it as raw text — AsmOperand.Memory is
// documented as verbatim dialect-specific addressing text, so no further
// structure needs to be preserved. Shared across dialects: it is pure
// token/bracket matching with no dialect-specific meaning of its own.
func readAsmMemory(c *lc, disp string) (string, error) {
	var toks []tok
	if disp != "" {
		toks = append(toks, tok{kind: tInt, text: disp})
	}
	if tk, ok := c.peek(); ok && tk.kind == tIdent && isPtrSize(tk.text) {
		c.next()
		toks = append(toks, tk)
		ptk, ok := c.next()
		if !ok || ptk.kind != tIdent || ptk.text != "ptr" {
			return "", c.l.errf("expected 'ptr' after size specifier %q", tk.text)
		}
		toks = append(toks, ptk)
	}
	open, ok := c.next()
	if !ok || open.kind != tPunct || (open.text != "[" && open.text != "(") {
		return "", c.l.errf("expected '[' or '(' to start memory operand")
	}
	want := "]"
	if open.text == "(" {
		want = ")"
	}
	toks = append(toks, open)
	depth := 1
	for {
		tk, ok := c.next()
		if !ok {
			return "", c.l.errf("unterminated memory operand, expected %q", want)
		}
		toks = append(toks, tk)
		if tk.kind == tPunct {
			switch tk.text {
			case "[", "(":
				depth++
			case "]", ")":
				depth--
			}
		}
		if depth == 0 {
			break
		}
	}
	if c.accept(tPunct, "!") {
		toks = append(toks, tok{kind: tPunct, text: "!"})
	}
	return joinAsmTokens(toks), nil
}

func joinAsmTokens(toks []tok) string {
	var b []byte
	noSpaceBefore := map[string]bool{",": true, ")": true, "]": true, "!": true, ":": true}
	noSpaceAfter := map[string]bool{"(": true, "[": true, "%": true, "$": true, "#": true}
	prevNoSpaceAfter := false
	for i, tk := range toks {
		if i > 0 {
			if !prevNoSpaceAfter && !noSpaceBefore[tk.text] {
				b = append(b, ' ')
			}
		}
		b = append(b, tk.text...)
		prevNoSpaceAfter = tk.kind == tPunct && noSpaceAfter[tk.text]
	}
	return string(b)
}