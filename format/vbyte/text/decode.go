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
// enforced here structurally (§1.2); everything else is Verify's job.
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
	tPunct // one of , ( ) [ ] : = .
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
		case c == ',' || c == '(' || c == ')' || c == '[' || c == ']' || c == ':' || c == '=' || c == '.':
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
	// `export global ...` vs `export fn ...`
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
	m.DeclareStruct(name, fields...)
	return nil
}

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
	m.DeclareFnSig(name, params, variadic, ret)
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
	m.DeclareConst(name, t, val)
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
	g.Export, g.TLS, g.Align = export, tls, align
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
		return vir.InitAddr{Name: name}, nil
	}
	if tk, ok := c.peek(); ok && tk.kind == tString {
		c.next()
		return vir.InitBytes{Data: []byte(tk.text)}, nil
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
		return vir.InitAgg{Elems: elems}, nil
	}
	o, err := parseOperand(c)
	if err != nil {
		return nil, err
	}
	return vir.InitLit{Value: o}, nil
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
		f := g.Fn(name, params, ret, attrs...)
		f.Variadic = variadic
		if !c.done() {
			return l.errf("trailing tokens on extern fn line")
		}
	}
}

// parseFnHead parses `name(params) ret attrs*` after the fn keyword.
func parseFnHead(c *lc) (string, []vir.Param, bool, vir.Type, []vir.FnAttr, error) {
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
	var attrs []vir.FnAttr
	for {
		tk, ok := c.peek()
		if !ok || tk.kind != tIdent {
			break
		}
		switch tk.text {
		case "noreturn", "readonly", "inline", "noinline", "cold":
			attrs = append(attrs, vir.FnAttr(tk.text))
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
	fb := m.DeclareFn(name, params, ret, export, attrs...)

	for {
		l := p.next()
		if l == nil {
			return fmt.Errorf("fn %s: unterminated (missing end)", name)
		}
		f := first(l)
		switch {
		case f == "end":
			return nil
		case len(l.toks) == 2 && l.toks[0].kind == tIdent && l.toks[1].kind == tPunct && l.toks[1].text == ":":
			fb.Label(l.toks[0].text)
		case terminatorWords[f]:
			t, err := parseTerminator(&lc{l: l})
			if err != nil {
				return err
			}
			fb.EmitInst(vir.Inst{}) // placeholder removed below
			cur := currentBlock(fb)
			cur.Insts = cur.Insts[:len(cur.Insts)-1]
			if cur.Term != nil {
				return l.errf("code after terminator is rejected as unreachable (§1.3 rule 2)")
			}
			cur.Term = t
		default:
			inst, err := parseInst(&lc{l: l})
			if err != nil {
				return err
			}
			cur := currentBlock(fb)
			if cur.Term != nil {
				return l.errf("instruction after terminator (§1.3 rule 2)")
			}
			fb.EmitInst(inst)
		}
	}
}

func currentBlock(fb *vir.FuncBuilder) *vir.Block {
	if n := len(fb.Func.Blocks); n > 0 {
		return fb.Func.Blocks[n-1]
	}
	return fb.Func.Entry
}

func parseTerminator(c *lc) (vir.Terminator, error) {
	kw, _ := c.expectIdent()
	switch kw {
	case "br":
		l, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		return vir.Br{Label: l}, nil
	case "br_if":
		cond, err := parseOperand(c)
		if err != nil {
			return nil, err
		}
		if err := c.expectPunct(","); err != nil {
			return nil, err
		}
		t, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		if err := c.expectPunct(","); err != nil {
			return nil, err
		}
		e, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		return vir.BrIf{Cond: cond, Then: t, Else: e}, nil
	case "switch":
		v, err := parseOperand(c)
		if err != nil {
			return nil, err
		}
		if err := c.expectPunct(","); err != nil {
			return nil, err
		}
		def, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		sw := vir.Switch{Value: v, Default: def}
		for c.accept(tPunct, ",") {
			n, err := expectInt64(c)
			if err != nil {
				return nil, err
			}
			lbl, err := c.expectIdent()
			if err != nil {
				return nil, err
			}
			sw.Cases = append(sw.Cases, vir.SwitchCase{Value: n, Label: lbl})
		}
		return sw, nil
	case "return":
		if c.done() {
			return vir.Return{}, nil
		}
		o, err := parseOperand(c)
		if err != nil {
			return nil, err
		}
		return vir.Return{Value: &o}, nil
	case "tailcall":
		if c.accept(tPunct, ".") {
			sig, err := c.expectIdent()
			if err != nil {
				return nil, err
			}
			args, err := parseOperandList(c)
			if err != nil {
				return nil, err
			}
			return vir.TailCall{Sig: sig, Args: args}, nil
		}
		callee, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		var args []vir.Operand
		for c.accept(tPunct, ",") {
			o, err := parseOperand(c)
			if err != nil {
				return nil, err
			}
			args = append(args, o)
		}
		return vir.TailCall{Callee: callee, Args: args}, nil
	case "trap":
		return vir.Trap{}, nil
	case "unreachable":
		return vir.Unreachable{}, nil
	}
	return nil, c.l.errf("unknown terminator %q", kw)
}

func parseInst(c *lc) (vir.Inst, error) {
	var inst vir.Inst
	// loc line?
	if tk, ok := c.peek(); ok && tk.text == "loc" && tk.kind == tIdent {
		c.next()
		args, err := parseOperandList(c)
		if err != nil {
			return inst, err
		}
		return vir.Inst{Op: "loc", Args: args}, nil
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
	if n := len(args); n >= 2 && args[n-2].Kind == vir.OIdent && args[n-2].Ident == "align" && args[n-1].Kind == vir.OInt {
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
		return vir.Int(v), nil
	case tFloat:
		c.next()
		v, err := strconv.ParseFloat(tk.text, 64)
		if err != nil {
			return vir.Operand{}, c.l.errf("bad float %q: %v", tk.text, err)
		}
		return vir.Flt(v), nil
	case tString:
		c.next()
		return vir.Str(tk.text), nil
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
			return vir.VecLit(lanes...), nil
		}
	case tIdent:
		switch tk.text {
		case "true":
			c.next()
			return vir.Bl(true), nil
		case "false":
			c.next()
			return vir.Bl(false), nil
		case "null":
			c.next()
			return vir.Null(), nil
		case "NaN":
			c.next()
			return vir.Flt(math.NaN()), nil
		case "Inf":
			c.next()
			return vir.Flt(math.Inf(1)), nil
		}
		if orderings[tk.text] {
			c.next()
			return vir.Ord(tk.text), nil
		}
		// Type in operand position (index.ptr)?
		save := c.i
		if t, err := parseType(c); err == nil {
			return vir.Ty(t), nil
		}
		c.i = save
		c.next()
		return vir.V(tk.text), nil
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