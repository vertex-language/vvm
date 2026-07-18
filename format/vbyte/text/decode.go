// Package text implements the .vir human-readable form (README arrow 2).
package text

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
)

// Decode parses .vir source into an unverified *vir.Module. Section order is
// enforced here structurally (§1.2), as is basic body shape (one terminator
// per block, nothing after it); everything else is Verify's job.
func Decode(src []byte) (*vir.Module, error) {
	p := &parser{}
	if err := p.lexAll(string(src)); err != nil {
		return nil, err
	}
	return p.parseModule()
}

// ---------------------------------------------------------------------------
// Lexer — one token stream per logical line (line breaks are significant, §1)
// ---------------------------------------------------------------------------

type tokKind int

const (
	tIdent tokKind = iota
	tInt
	tFloat
	tString
	tPunct // one of , ( ) [ ] : = . % $ # + * !
	tEllipsis
)

type tok struct {
	kind tokKind
	text string
}

type line struct {
	num  int
	toks []tok
}

type parser struct {
	lines []line
	pos   int
}

func (p *parser) lexAll(src string) error {
	for i, raw := range strings.Split(src, "\n") {
		toks, err := lexLine(raw)
		if err != nil {
			return fmt.Errorf("line %d: %w", i+1, err)
		}
		if len(toks) > 0 {
			p.lines = append(p.lines, line{num: i + 1, toks: toks})
		}
	}
	return nil
}

// lexLine tokenizes both ordinary module-grammar lines and asm-block lines.
// Asm operands (registers, immediates, memory refs) are "independently
// lexed" per §4, but we still use this single lexer for them — it just
// additionally recognizes the punctuation asm syntax needs ('%','$','#',
// '+','*','!'); the asm-specific *parsing* (readAsmMemory etc.) is what
// actually gives those characters their dialect-specific meaning.
func lexLine(s string) ([]tok, error) {
	var toks []tok
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			return toks, nil // comment to end of line (§3)
		case c == '"':
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					if j+1 >= len(s) {
						return nil, fmt.Errorf("unterminated escape")
					}
					switch s[j+1] {
					case '0':
						b.WriteByte(0)
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case '\\':
						b.WriteByte('\\')
					case '"':
						b.WriteByte('"')
					case 'x':
						if j+3 >= len(s) {
							return nil, fmt.Errorf("bad \\x escape")
						}
						v, err := strconv.ParseUint(s[j+2:j+4], 16, 8)
						if err != nil {
							return nil, fmt.Errorf("bad \\x escape: %w", err)
						}
						b.WriteByte(byte(v))
						j += 2
					default:
						return nil, fmt.Errorf("unknown escape \\%c", s[j+1])
					}
					j += 2
				} else {
					b.WriteByte(s[j])
					j++
				}
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, tok{tString, b.String()})
			i = j + 1
		case strings.HasPrefix(s[i:], "..."):
			toks = append(toks, tok{tEllipsis, "..."})
			i += 3
		case strings.HasPrefix(s[i:], "-Inf"):
			// float-literal alt "-Inf" (§1.1); must be checked before the
			// generic '-'-prefixed-number case below, which requires a digit.
			toks = append(toks, tok{tIdent, "-Inf"})
			i += 4
		case c == ',' || c == '(' || c == ')' || c == '[' || c == ']' || c == ':' ||
			c == '=' || c == '.' || c == '%' || c == '$' || c == '#' || c == '+' ||
			c == '*' || c == '!':
			toks = append(toks, tok{tPunct, string(c)})
			i++
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			if c == '-' {
				j++
				if j >= len(s) || s[j] < '0' || s[j] > '9' {
					return nil, fmt.Errorf("stray '-'")
				}
			}
			isFloat := false
			for j < len(s) {
				d := s[j]
				if d >= '0' && d <= '9' {
					j++
				} else if d == '.' && j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9' {
					isFloat = true
					j++
				} else if (d == 'e' || d == 'E') && isFloat {
					j++
					if j < len(s) && s[j] == '-' {
						j++
					}
				} else {
					break
				}
			}
			k := tInt
			if isFloat {
				k = tFloat
			}
			toks = append(toks, tok{k, s[i:j]})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			toks = append(toks, tok{tIdent, s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentChar(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// ---------------------------------------------------------------------------
// Line cursor helpers
// ---------------------------------------------------------------------------

func (p *parser) peek() *line {
	if p.pos >= len(p.lines) {
		return nil
	}
	return &p.lines[p.pos]
}

func (p *parser) next() *line {
	l := p.peek()
	if l != nil {
		p.pos++
	}
	return l
}

func (l *line) errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", l.num, fmt.Sprintf(format, args...))
}

// cursor within one line
type lc struct {
	l *line
	i int
}

func (c *lc) peek() (tok, bool) {
	if c.i >= len(c.l.toks) {
		return tok{}, false
	}
	return c.l.toks[c.i], true
}
func (c *lc) peekN(n int) (tok, bool) {
	idx := c.i + n
	if idx < 0 || idx >= len(c.l.toks) {
		return tok{}, false
	}
	return c.l.toks[idx], true
}
func (c *lc) next() (tok, bool) {
	t, ok := c.peek()
	if ok {
		c.i++
	}
	return t, ok
}
func (c *lc) accept(kind tokKind, text string) bool {
	t, ok := c.peek()
	if ok && t.kind == kind && (text == "" || t.text == text) {
		c.i++
		return true
	}
	return false
}
func (c *lc) expectIdent() (string, error) {
	t, ok := c.next()
	if !ok || t.kind != tIdent {
		return "", c.l.errf("expected identifier")
	}
	return t.text, nil
}
func (c *lc) expectPunct(p string) error {
	if !c.accept(tPunct, p) {
		return c.l.errf("expected %q", p)
	}
	return nil
}
func (c *lc) done() bool { _, ok := c.peek(); return !ok }

// ---------------------------------------------------------------------------
// Module structure
// ---------------------------------------------------------------------------

func (p *parser) parseModule() (*vir.Module, error) {
	l := p.next()
	if l == nil {
		return nil, fmt.Errorf("empty input")
	}
	c := &lc{l: l}
	if !c.accept(tIdent, "module") {
		return nil, l.errf("first line must be a module header (§1.2 rule 1)")
	}
	name, err := c.expectIdent()
	if err != nil {
		return nil, err
	}
	if !c.done() {
		return nil, l.errf("trailing tokens after module header")
	}
	m := vir.NewModule(name)

	if l := p.peek(); l != nil && first(l) == "target" {
		p.next()
		if err := parseTarget(&lc{l: l}, m); err != nil {
			return nil, err
		}
	}
	for l := p.peek(); l != nil && first(l) == "struct"; l = p.peek() {
		p.next()
		if err := parseStruct(&lc{l: l}, m); err != nil {
			return nil, err
		}
	}
	for l := p.peek(); l != nil && first(l) == "fnsig"; l = p.peek() {
		p.next()
		if err := parseFnSig(&lc{l: l}, m); err != nil {
			return nil, err
		}
	}
	for l := p.peek(); l != nil && first(l) == "const"; l = p.peek() {
		p.next()
		if err := parseConst(&lc{l: l}, m); err != nil {
			return nil, err
		}
	}
	for l := p.peek(); l != nil && isGlobalLine(l); l = p.peek() {
		p.next()
		if err := parseGlobal(&lc{l: l}, m); err != nil {
			return nil, err
		}
	}
	for l := p.peek(); l != nil && first(l) == "link"; l = p.peek() {
		p.next()
		if err := parseLink(&lc{l: l}, m); err != nil {
			return nil, err
		}
	}
	for l := p.peek(); l != nil && first(l) == "extern"; l = p.peek() {
		if err := p.parseExternGroup(m); err != nil {
			return nil, err
		}
	}
	for l := p.peek(); l != nil; l = p.peek() {
		if first(l) != "fn" && first(l) != "export" {
			return nil, l.errf("unexpected %q: sections never interleave (§1.2)", first(l))
		}
		if err := p.parseFn(m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func first(l *line) string {
	if len(l.toks) > 0 && l.toks[0].kind == tIdent {
		return l.toks[0].text
	}
	return ""
}

func isGlobalLine(l *line) bool {
	f := first(l)
	if f == "global" {
		return true
	}
	return f == "export" && len(l.toks) > 1 && l.toks[1].kind == tIdent && l.toks[1].text == "global"
}

func parseTarget(c *lc, m *vir.Module) error {
	c.accept(tIdent, "target")
	arch, err := c.expectIdent()
	if err != nil {
		return err
	}
	osName, err := c.expectIdent()
	if err != nil {
		return err
	}
	t := &vir.Target{Arch: arch, OS: osName}
	if tk, ok := c.peek(); ok && tk.kind == tIdent {
		c.next()
		t.ABI = tk.text
	}
	if c.accept(tPunct, "[") {
		for {
			id, err := c.expectIdent()
			if err != nil {
				return err
			}
			t.Tiers = append(t.Tiers, id)
			if c.accept(tPunct, "]") {
				break
			}
			if err := c.expectPunct(","); err != nil {
				return err
			}
		}
	}
	if !c.done() {
		return c.l.errf("trailing tokens after target declaration")
	}
	m.Target = t
	return nil
}

func parseStruct(c *lc, m *vir.Module) error {
	c.accept(tIdent, "struct")
	name, err := c.expectIdent()
	if err != nil {
		return err
	}
	if err := c.expectPunct("("); err != nil {
		return err
	}
	var fields []vir.Field
	for {
		fn, err := c.expectIdent()
		if err != nil {
			return err
		}
		ft, err := parseType(c)
		if err != nil {
			return err
		}
		fields = append(fields, vir.Field{Name: fn, Type: ft})
		if c.accept(tPunct, ")") {
			break
		}
		if err := c.expectPunct(","); err != nil {
			return err
		}
	}
	if !c.done() {
		return c.l.errf("trailing tokens after struct declaration")
	}
	m.DeclareStruct(name, fields...)
	return nil
}

// fnsig params are bare types — fnsigs never name their parameters (§1.1).
func parseFnSig(c *lc, m *vir.Module) error {
	c.accept(tIdent, "fnsig")
	name, err := c.expectIdent()
	if err != nil {
		return err
	}
	if err := c.expectPunct("("); err != nil {
		return err
	}
	var params []vir.Type
	variadic := false
	if !c.accept(tPunct, ")") {
		for {
			if c.accept(tEllipsis, "") {
				variadic = true
				if err := c.expectPunct(")"); err != nil {
					return err
				}
				break
			}
			t, err := parseType(c)
			if err != nil {
				return err
			}
			params = append(params, t)
			if c.accept(tPunct, ")") {
				break
			}
			if err := c.expectPunct(","); err != nil {
				return err
			}
		}
	}
	ret, err := parseType(c)
	if err != nil {
		return err
	}
	if !c.done() {
		return c.l.errf("trailing tokens after fnsig")
	}
	m.DeclareFunctionSignature(name, params, variadic, ret)
	return nil
}

func parseConst(c *lc, m *vir.Module) error {
	c.accept(tIdent, "const")
	name, err := c.expectIdent()
	if err != nil {
		return err
	}
	t, err := parseType(c)
	if err != nil {
		return err
	}
	if err := c.expectPunct("="); err != nil {
		return err
	}
	val, err := parseOperand(c)
	if err != nil {
		return err
	}
	if !c.done() {
		return c.l.errf("trailing tokens after const")
	}
	m.DeclareConstant(name, t, val)
	return nil
}

func parseGlobal(c *lc, m *vir.Module) error {
	export := c.accept(tIdent, "export")
	c.accept(tIdent, "global")
	tls := c.accept(tIdent, "tls")
	name, err := c.expectIdent()
	if err != nil {
		return err
	}
	t, err := parseType(c)
	if err != nil {
		return err
	}
	align := 0
	if c.accept(tIdent, "align") {
		align, err = expectInt(c)
		if err != nil {
			return err
		}
	}
	if err := c.expectPunct("="); err != nil {
		return err
	}
	init, err := parseConstInit(c)
	if err != nil {
		return err
	}
	if !c.done() {
		return c.l.errf("trailing tokens after global initializer")
	}
	g := m.DeclareGlobal(name, t, init)
	g.Export = export
	g.TLS = tls
	g.Align = align
	return nil
}

func parseConstInit(c *lc) (vir.ConstInit, error) {
	if c.accept(tIdent, "zero") {
		return vir.InitZero{}, nil
	}
	if c.accept(tIdent, "addr") {
		name, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		return vir.InitAddressOf{Name: name}, nil
	}
	if tk, ok := c.peek(); ok && tk.kind == tString {
		c.next()
		return vir.InitByteString{Data: []byte(tk.text)}, nil
	}
	if c.accept(tPunct, "(") {
		var elems []vir.ConstInit
		for {
			e, err := parseConstInit(c)
			if err != nil {
				return nil, err
			}
			elems = append(elems, e)
			if c.accept(tPunct, ")") {
				break
			}
			if err := c.expectPunct(","); err != nil {
				return nil, err
			}
		}
		return vir.InitAggregate{Elems: elems}, nil
	}
	o, err := parseOperand(c)
	if err != nil {
		return nil, err
	}
	return vir.InitLiteral{Value: o}, nil
}

func parseLink(c *lc, m *vir.Module) error {
	c.accept(tIdent, "link")
	kind, err := c.expectIdent()
	if err != nil {
		return err
	}
	tk, ok := c.next()
	if !ok || tk.kind != tString {
		return c.l.errf("link expects a string literal")
	}
	if !c.done() {
		return c.l.errf("trailing tokens after link")
	}
	m.DeclareLink(vir.LinkKind(kind), tk.text)
	return nil
}

func (p *parser) parseExternGroup(m *vir.Module) error {
	l := p.next()
	c := &lc{l: l}
	c.accept(tIdent, "extern")
	dep := ""
	if tk, ok := c.peek(); ok && tk.kind == tString {
		c.next()
		dep = tk.text
	}
	if err := c.expectPunct(":"); err != nil {
		return err
	}
	if !c.done() {
		return l.errf("trailing tokens after extern header")
	}
	g := m.DeclareExternGroup(dep)
	for {
		l := p.next()
		if l == nil {
			return fmt.Errorf("unterminated extern group (missing end)")
		}
		if first(l) == "end" {
			return nil
		}
		c := &lc{l: l}
		if !c.accept(tIdent, "fn") {
			return l.errf("extern groups contain only fn lines (§1.2 rule 9)")
		}
		name, params, variadic, ret, attrs, err := parseFnHead(c)
		if err != nil {
			return err
		}
		if !c.done() {
			return l.errf("trailing tokens on extern fn line")
		}
		f := g.DeclareFunction(name, params, ret, attrs...)
		if variadic {
			f.SetVariadic()
		}
	}
}

// parseFnHead parses `name(params) ret attrs*` after the fn keyword.
func parseFnHead(c *lc) (string, []vir.Param, bool, vir.Type, []vir.FunctionAttribute, error) {
	name, err := c.expectIdent()
	if err != nil {
		return "", nil, false, nil, nil, err
	}
	if err := c.expectPunct("("); err != nil {
		return "", nil, false, nil, nil, err
	}
	var params []vir.Param
	variadic := false
	if !c.accept(tPunct, ")") {
		for {
			if c.accept(tEllipsis, "") {
				variadic = true
				if err := c.expectPunct(")"); err != nil {
					return "", nil, false, nil, nil, err
				}
				break
			}
			pn, err := c.expectIdent()
			if err != nil {
				return "", nil, false, nil, nil, err
			}
			pt, err := parseType(c)
			if err != nil {
				return "", nil, false, nil, nil, err
			}
			prm := vir.Param{Name: pn, Type: pt}
			for {
				if c.accept(tIdent, "byval") || c.accept(tIdent, "sret") {
					attr := c.l.toks[c.i-1].text
					if err := c.expectPunct("["); err != nil {
						return "", nil, false, nil, nil, err
					}
					sn, err := c.expectIdent()
					if err != nil {
						return "", nil, false, nil, nil, err
					}
					if err := c.expectPunct("]"); err != nil {
						return "", nil, false, nil, nil, err
					}
					if attr == "byval" {
						prm.ByVal = sn
					} else {
						prm.SRet = sn
					}
					continue
				}
				break
			}
			params = append(params, prm)
			if c.accept(tPunct, ")") {
				break
			}
			if err := c.expectPunct(","); err != nil {
				return "", nil, false, nil, nil, err
			}
		}
	}
	ret, err := parseType(c)
	if err != nil {
		return "", nil, false, nil, nil, err
	}
	var attrs []vir.FunctionAttribute
	for {
		tk, ok := c.peek()
		if !ok || tk.kind != tIdent {
			break
		}
		switch tk.text {
		case "noreturn", "readonly", "inline", "noinline", "cold":
			attrs = append(attrs, vir.FunctionAttribute(tk.text))
			c.next()
		default:
			return name, params, variadic, ret, attrs, nil
		}
	}
	return name, params, variadic, ret, attrs, nil
}

// ---------------------------------------------------------------------------
// Function bodies
// ---------------------------------------------------------------------------

var terminatorWords = map[string]bool{
	"br": true, "br_if": true, "switch": true, "return": true,
	"tailcall": true, "trap": true, "unreachable": true,
}

func (p *parser) parseFn(m *vir.Module) error {
	l := p.next()
	c := &lc{l: l}
	export := c.accept(tIdent, "export")
	if !c.accept(tIdent, "fn") {
		return l.errf("expected fn")
	}
	name, params, variadic, ret, attrs, err := parseFnHead(c)
	if err != nil {
		return err
	}
	if variadic {
		return l.errf("variadics are rejected in fn definitions (§1.2 rule 5)")
	}
	if err := c.expectPunct(":"); err != nil {
		return err
	}
	if !c.done() {
		return l.errf("trailing tokens on fn signature line")
	}

	arch := ""
	if m.Target != nil {
		arch = m.Target.Arch
	}

	fb := m.DeclareFunction(name, params, ret, export, attrs...)
	terminated := false
	for {
		l := p.next()
		if l == nil {
			return fmt.Errorf("fn %s: unterminated (missing end)", name)
		}
		f := first(l)
		switch {
		case f == "end":
			if !terminated {
				return l.errf("fn %s: block ended without a terminator (§1.3 rule 2)", name)
			}
			return nil
		case len(l.toks) == 2 && l.toks[0].kind == tIdent && l.toks[1].kind == tPunct && l.toks[1].text == ":":
			if !terminated {
				return l.errf("fn %s: block must end with a terminator before next label (§1.3 rule 2)", name)
			}
			fb.Label(l.toks[0].text)
			terminated = false
		case f == "asm":
			if terminated {
				return l.errf("fn %s: asm block after terminator (§1.3 rule 2)", name)
			}
			if err := p.parseAsm(l, fb, arch); err != nil {
				return err
			}
		case terminatorWords[f]:
			if terminated {
				return l.errf("fn %s: multiple terminators in one block (§1.3 rule 2)", name)
			}
			if err := applyTerminator(&lc{l: l}, fb); err != nil {
				return err
			}
			terminated = true
		default:
			if terminated {
				return l.errf("fn %s: instruction after terminator (§1.3 rule 2)", name)
			}
			inst, err := parseInst(&lc{l: l})
			if err != nil {
				return err
			}
			fb.EmitInstruction(inst)
		}
	}
}

func applyTerminator(c *lc, fb *vir.FunctionBuilder) error {
	kw, _ := c.expectIdent()
	switch kw {
	case "br":
		lbl, err := c.expectIdent()
		if err != nil {
			return err
		}
		if !c.done() {
			return c.l.errf("trailing tokens after br")
		}
		fb.Branch(lbl)
	case "br_if":
		cond, err := parseOperand(c)
		if err != nil {
			return err
		}
		if err := c.expectPunct(","); err != nil {
			return err
		}
		then, err := c.expectIdent()
		if err != nil {
			return err
		}
		if err := c.expectPunct(","); err != nil {
			return err
		}
		els, err := c.expectIdent()
		if err != nil {
			return err
		}
		if !c.done() {
			return c.l.errf("trailing tokens after br_if")
		}
		fb.BranchIf(cond, then, els)
	case "switch":
		v, err := parseOperand(c)
		if err != nil {
			return err
		}
		if err := c.expectPunct(","); err != nil {
			return err
		}
		def, err := c.expectIdent()
		if err != nil {
			return err
		}
		var cases []vir.SwitchCase
		for c.accept(tPunct, ",") {
			n, err := expectInt64(c)
			if err != nil {
				return err
			}
			lbl, err := c.expectIdent()
			if err != nil {
				return err
			}
			cases = append(cases, vir.SwitchCase{Value: n, Label: lbl})
		}
		if !c.done() {
			return c.l.errf("trailing tokens after switch")
		}
		fb.Switch(v, def, cases...)
	case "return":
		if c.done() {
			fb.Return()
			return nil
		}
		o, err := parseOperand(c)
		if err != nil {
			return err
		}
		if !c.done() {
			return c.l.errf("trailing tokens after return")
		}
		fb.Return(o)
	case "tailcall":
		if c.accept(tPunct, ".") {
			sig, err := c.expectIdent()
			if err != nil {
				return err
			}
			args, err := parseOperandList(c)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return c.l.errf("indirect tailcall requires a callee pointer operand")
			}
			fb.TailCallIndirect(sig, args[0], args[1:]...)
			return nil
		}
		callee, err := c.expectIdent()
		if err != nil {
			return err
		}
		var args []vir.Operand
		for c.accept(tPunct, ",") {
			o, err := parseOperand(c)
			if err != nil {
				return err
			}
			args = append(args, o)
		}
		if !c.done() {
			return c.l.errf("trailing tokens after tailcall")
		}
		fb.TailCall(callee, args...)
	case "trap":
		if !c.done() {
			return c.l.errf("trailing tokens after trap")
		}
		fb.Trap()
	case "unreachable":
		if !c.done() {
			return c.l.errf("trailing tokens after unreachable")
		}
		fb.Unreachable()
	default:
		return c.l.errf("unknown terminator %q", kw)
	}
	return nil
}

func parseInst(c *lc) (vir.Instruction, error) {
	var inst vir.Instruction

	// loc-line is its own grammar production: space-separated, no commas.
	if tk, ok := c.peek(); ok && tk.kind == tIdent && tk.text == "loc" {
		c.next()
		ftok, ok := c.next()
		if !ok || ftok.kind != tString {
			return inst, c.l.errf("loc: expected file string literal")
		}
		ltok, ok := c.next()
		if !ok || ltok.kind != tInt {
			return inst, c.l.errf("loc: expected line number")
		}
		lineNo, err := strconv.ParseInt(ltok.text, 10, 64)
		if err != nil {
			return inst, c.l.errf("loc: bad line number %q", ltok.text)
		}
		args := []vir.Operand{vir.StringLiteral(ftok.text), vir.IntLiteral(lineNo)}
		if !c.done() {
			ctok, ok := c.next()
			if !ok || ctok.kind != tInt {
				return inst, c.l.errf("loc: expected column number")
			}
			col, err := strconv.ParseInt(ctok.text, 10, 64)
			if err != nil {
				return inst, c.l.errf("loc: bad column number %q", ctok.text)
			}
			args = append(args, vir.IntLiteral(col))
		}
		if !c.done() {
			return inst, c.l.errf("loc: trailing tokens")
		}
		return vir.Instruction{Op: "loc", Args: args}, nil
	}

	// result name?
	if len(c.l.toks) > 1 && c.l.toks[0].kind == tIdent &&
		c.l.toks[1].kind == tPunct && c.l.toks[1].text == "=" {
		inst.Result = c.l.toks[0].text
		c.i = 2
	}
	op, err := c.expectIdent()
	if err != nil {
		return inst, err
	}
	inst.Op = op
	if c.accept(tPunct, ".") {
		// Suffix is a type or a fnsig name (§4).
		save := c.i
		if t, terr := parseType(c); terr == nil {
			inst.Suffix = t
		} else {
			c.i = save
			sig, err := c.expectIdent()
			if err != nil {
				return inst, err
			}
			inst.Sig = sig
		}
	}
	args, err := parseOperandList(c)
	if err != nil {
		return inst, err
	}
	// Trailing ", align N" (§1.1 align-clause) — spelled as ident+int operands.
	if n := len(args); n >= 2 && args[n-2].Kind == vir.OperandIdent && args[n-2].Ident == "align" && args[n-1].Kind == vir.OperandInt {
		inst.Align = int(args[n-1].Int)
		args = args[:n-2]
	}
	inst.Args = args
	return inst, nil
}

func parseOperandList(c *lc) ([]vir.Operand, error) {
	var out []vir.Operand
	if c.done() {
		return out, nil
	}
	for {
		o, err := parseOperand(c)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
		if !c.accept(tPunct, ",") {
			break
		}
	}
	if !c.done() {
		return nil, c.l.errf("trailing tokens")
	}
	return out, nil
}

var orderings = map[string]bool{
	"relaxed": true, "acquire": true, "release": true, "acqrel": true, "seqcst": true,
}

func parseOperand(c *lc) (vir.Operand, error) {
	tk, ok := c.peek()
	if !ok {
		return vir.Operand{}, c.l.errf("expected operand")
	}
	switch tk.kind {
	case tInt:
		c.next()
		v, err := strconv.ParseInt(tk.text, 10, 64)
		if err != nil {
			return vir.Operand{}, c.l.errf("bad integer %q: %v", tk.text, err)
		}
		return vir.IntLiteral(v), nil
	case tFloat:
		c.next()
		v, err := strconv.ParseFloat(tk.text, 64)
		if err != nil {
			return vir.Operand{}, c.l.errf("bad float %q: %v", tk.text, err)
		}
		return vir.FloatLiteral(v), nil
	case tString:
		c.next()
		return vir.StringLiteral(tk.text), nil
	case tPunct:
		if tk.text == "(" { // vector literal
			c.next()
			var lanes []int64
			for {
				n, err := expectInt64(c)
				if err != nil {
					return vir.Operand{}, err
				}
				lanes = append(lanes, n)
				if c.accept(tPunct, ")") {
					break
				}
				if err := c.expectPunct(","); err != nil {
					return vir.Operand{}, err
				}
			}
			return vir.VectorLiteral(lanes...), nil
		}
	case tIdent:
		switch tk.text {
		case "true":
			c.next()
			return vir.BoolLiteral(true), nil
		case "false":
			c.next()
			return vir.BoolLiteral(false), nil
		case "null":
			c.next()
			return vir.NullLiteral(), nil
		case "NaN":
			c.next()
			return vir.FloatLiteral(math.NaN()), nil
		case "Inf":
			c.next()
			return vir.FloatLiteral(math.Inf(1)), nil
		case "-Inf":
			c.next()
			return vir.FloatLiteral(math.Inf(-1)), nil
		}
		if orderings[tk.text] {
			c.next()
			return vir.OrderingOperand(tk.text), nil
		}
		// Type in operand position (index.ptr)?
		save := c.i
		if t, err := parseType(c); err == nil {
			return vir.TypeOperand(t), nil
		}
		c.i = save
		c.next()
		return vir.Ident(tk.text), nil
	}
	return vir.Operand{}, c.l.errf("unexpected token %q in operand position", tk.text)
}

func expectInt(c *lc) (int, error) {
	n, err := expectInt64(c)
	return int(n), err
}

func expectInt64(c *lc) (int64, error) {
	tk, ok := c.next()
	if !ok || tk.kind != tInt {
		return 0, c.l.errf("expected integer literal")
	}
	return strconv.ParseInt(tk.text, 10, 64)
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

func parseType(c *lc) (vir.Type, error) {
	tk, ok := c.peek()
	if !ok || tk.kind != tIdent {
		return nil, c.l.errf("expected type")
	}
	switch tk.text {
	case "ptr":
		c.next()
		return vir.Ptr, nil
	case "void":
		c.next()
		return vir.Void, nil
	case "struct":
		c.next()
		n, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		return vir.StructType{Name: n}, nil
	case "vec", "array":
		kw := tk.text
		c.next()
		if err := c.expectPunct("["); err != nil {
			return nil, err
		}
		elem, err := parseType(c)
		if err != nil {
			return nil, err
		}
		if err := c.expectPunct(","); err != nil {
			return nil, err
		}
		n, err := expectInt(c)
		if err != nil {
			return nil, err
		}
		if err := c.expectPunct("]"); err != nil {
			return nil, err
		}
		if kw == "vec" {
			return vir.VecType{Elem: elem, Len: n}, nil
		}
		return vir.ArrayType{Elem: elem, Len: n}, nil
	}
	// iN / fN
	s := tk.text
	if len(s) >= 2 && (s[0] == 'i' || s[0] == 'f') && s[1] >= '1' && s[1] <= '9' {
		bits, err := strconv.Atoi(s[1:])
		if err == nil {
			c.next()
			if s[0] == 'i' {
				return vir.IntType{Bits: bits}, nil
			}
			switch bits {
			case 16, 32, 64:
				return vir.FloatType{Bits: bits}, nil
			}
			return nil, c.l.errf("illegal float width f%d", bits)
		}
	}
	return nil, c.l.errf("%q is not a type", s)
}

// ---------------------------------------------------------------------------
// Inline assembly (§4). Structural only, per README/verify.go: mnemonic and
// operand-shape legality (§9.38) is explicitly not required at this layer.
// ---------------------------------------------------------------------------

func (p *parser) parseAsm(header *line, fb *vir.FunctionBuilder, arch string) error {
	c := &lc{l: header}
	c.accept(tIdent, "asm")
	dialectTok, err := c.expectIdent()
	if err != nil {
		return err
	}
	dialect := vir.AsmDialect(dialectTok)
	switch dialect {
	case vir.DialectIntel, vir.DialectATT, vir.DialectA32, vir.DialectT32, vir.DialectNative:
	default:
		return header.errf("unknown asm dialect %q", dialectTok)
	}
	if err := c.expectPunct(":"); err != nil {
		return err
	}
	if !c.done() {
		return header.errf("trailing tokens after asm header")
	}

	ab := fb.BeginAsm(dialect)

bindings:
	for {
		bl := p.next()
		if bl == nil {
			return fmt.Errorf("unterminated asm block (missing code:/end)")
		}
		bc := &lc{l: bl}
		switch first(bl) {
		case "in":
			bc.next()
			reg, err := parseRegIdent(bc)
			if err != nil {
				return err
			}
			if err := bc.expectPunct("="); err != nil {
				return err
			}
			ident, err := bc.expectIdent()
			if err != nil {
				return err
			}
			if !bc.done() {
				return bl.errf("trailing tokens after in-binding")
			}
			ab.In(reg, ident)
		case "out":
			bc.next()
			reg, err := parseRegIdent(bc)
			if err != nil {
				return err
			}
			if err := bc.expectPunct("="); err != nil {
				return err
			}
			ident, err := bc.expectIdent()
			if err != nil {
				return err
			}
			if !bc.done() {
				return bl.errf("trailing tokens after out-binding")
			}
			ab.Out(reg, ident)
		case "clobber":
			bc.next()
			var regs []string
			for {
				r, err := parseRegIdent(bc)
				if err != nil {
					return err
				}
				regs = append(regs, r)
				if bc.done() {
					break
				}
				if err := bc.expectPunct(","); err != nil {
					return err
				}
			}
			ab.Clobber(regs...)
		case "code":
			bc.next()
			if err := bc.expectPunct(":"); err != nil {
				return err
			}
			if !bc.done() {
				return bl.errf("trailing tokens after code:")
			}
			break bindings
		default:
			return bl.errf("expected asm binding (in/out/clobber) or code:, got %q", first(bl))
		}
	}

	for {
		cl := p.next()
		if cl == nil {
			return fmt.Errorf("unterminated asm code section (missing end)")
		}
		if first(cl) == "end" {
			ab.End()
			return nil
		}
		codeLine, err := parseAsmCodeLine(cl, arch, dialect)
		if err != nil {
			return err
		}
		ab.Code(codeLine)
	}
}

// parseRegIdent parses "%"? ident, stripping the AT&T '%' prefix; the
// canonical register name (without '%') is what AsmBinding.Register stores.
func parseRegIdent(c *lc) (string, error) {
	c.accept(tPunct, "%")
	return c.expectIdent()
}

func parseAsmCodeLine(l *line, arch string, dialect vir.AsmDialect) (vir.AsmCodeLine, error) {
	if len(l.toks) == 2 && l.toks[0].kind == tIdent && l.toks[1].kind == tPunct && l.toks[1].text == ":" {
		return vir.AsmLabelDeclaration(l.toks[0].text), nil
	}
	c := &lc{l: l}
	mnem, err := c.expectIdent()
	if err != nil {
		return vir.AsmCodeLine{}, l.errf("expected mnemonic or label declaration")
	}
	var ops []vir.AsmOperand
	for !c.done() {
		op, err := parseAsmOperand(c, arch, dialect)
		if err != nil {
			return vir.AsmCodeLine{}, err
		}
		ops = append(ops, op)
		if c.done() {
			break
		}
		if err := c.expectPunct(","); err != nil {
			return vir.AsmCodeLine{}, err
		}
	}
	return vir.AsmInstructionLine(mnem, ops...), nil
}

func parseAsmOperand(c *lc, arch string, dialect vir.AsmDialect) (vir.AsmOperand, error) {
	switch dialect {
	case vir.DialectATT:
		return parseAsmOperandATT(c)
	case vir.DialectIntel:
		return parseAsmOperandIntel(c, arch)
	default: // a32, t32, native
		return parseAsmOperandARM(c, arch)
	}
}

func parseAsmOperandATT(c *lc) (vir.AsmOperand, error) {
	tk, ok := c.peek()
	if !ok {
		return vir.AsmOperand{}, c.l.errf("expected asm operand")
	}
	switch {
	case tk.kind == tPunct && tk.text == "%":
		c.next()
		reg, err := c.expectIdent()
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmRegister(reg), nil
	case tk.kind == tPunct && tk.text == "$":
		c.next()
		imm, err := parseImmValue(c)
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmImmediate(imm), nil
	case tk.kind == tPunct && tk.text == "(":
		text, err := readAsmMemory(c, "")
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmMemory(text), nil
	case tk.kind == tInt:
		if n, ok2 := c.peekN(1); ok2 && n.kind == tPunct && n.text == "(" {
			disp := tk.text
			c.next()
			text, err := readAsmMemory(c, disp)
			if err != nil {
				return vir.AsmOperand{}, err
			}
			return vir.AsmMemory(text), nil
		}
		c.next()
		v, err := strconv.ParseInt(tk.text, 10, 64)
		if err != nil {
			return vir.AsmOperand{}, c.l.errf("bad integer %q", tk.text)
		}
		return vir.AsmImmediate(vir.IntLiteral(v)), nil
	case tk.kind == tIdent:
		c.next()
		return vir.AsmLabelReference(tk.text), nil
	}
	return vir.AsmOperand{}, c.l.errf("unrecognized asm operand %q", tk.text)
}

func parseAsmOperandIntel(c *lc, arch string) (vir.AsmOperand, error) {
	tk, ok := c.peek()
	if !ok {
		return vir.AsmOperand{}, c.l.errf("expected asm operand")
	}
	switch {
	case tk.kind == tIdent && isPtrSize(tk.text):
		text, err := readAsmMemory(c, "")
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmMemory(text), nil
	case tk.kind == tPunct && tk.text == "[":
		text, err := readAsmMemory(c, "")
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmMemory(text), nil
	case tk.kind == tInt || tk.kind == tFloat:
		c.next()
		imm, err := literalOperand(tk)
		if err != nil {
			return vir.AsmOperand{}, c.l.errf("%v", err)
		}
		return vir.AsmImmediate(imm), nil
	case tk.kind == tIdent:
		c.next()
		if regTableHas(arch, tk.text) {
			return vir.AsmRegister(tk.text), nil
		}
		return vir.AsmLabelReference(tk.text), nil
	}
	return vir.AsmOperand{}, c.l.errf("unrecognized asm operand %q", tk.text)
}

func parseAsmOperandARM(c *lc, arch string) (vir.AsmOperand, error) {
	tk, ok := c.peek()
	if !ok {
		return vir.AsmOperand{}, c.l.errf("expected asm operand")
	}
	switch {
	case tk.kind == tPunct && tk.text == "[":
		text, err := readAsmMemory(c, "")
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmMemory(text), nil
	case tk.kind == tPunct && tk.text == "#":
		c.next()
		imm, err := parseImmValue(c)
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmImmediate(imm), nil
	case tk.kind == tInt || tk.kind == tFloat:
		c.next()
		imm, err := literalOperand(tk)
		if err != nil {
			return vir.AsmOperand{}, c.l.errf("%v", err)
		}
		return vir.AsmImmediate(imm), nil
	case tk.kind == tIdent:
		c.next()
		if regTableHas(arch, tk.text) {
			return vir.AsmRegister(tk.text), nil
		}
		return vir.AsmLabelReference(tk.text), nil
	}
	return vir.AsmOperand{}, c.l.errf("unrecognized asm operand %q", tk.text)
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
// structure needs to be preserved.
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
	var b strings.Builder
	noSpaceBefore := map[string]bool{",": true, ")": true, "]": true, "!": true, ":": true}
	noSpaceAfter := map[string]bool{"(": true, "[": true, "%": true, "$": true, "#": true}
	prevNoSpaceAfter := false
	for i, tk := range toks {
		if i > 0 {
			if !prevNoSpaceAfter && !noSpaceBefore[tk.text] {
				b.WriteByte(' ')
			}
		}
		b.WriteString(tk.text)
		prevNoSpaceAfter = tk.kind == tPunct && noSpaceAfter[tk.text]
	}
	return b.String()
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