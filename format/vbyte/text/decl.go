package text

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// Module preamble: header, target, asmdialect, structs, fnsigs, consts,
// globals, links, externs (§1.2). Function bodies live in func.go; asm
// blocks live in asm.go/asm_*.go.
// ---------------------------------------------------------------------------

func isGlobalLine(l *line) bool {
	f := first(l)
	if f == "global" {
		return true
	}
	return f == "export" && len(l.toks) > 1 && l.toks[1].kind == tIdent && l.toks[1].text == "global"
}

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
	if l := p.peek(); l != nil && first(l) == "asmdialect" {
		p.next()
		if err := parseAsmDialect(&lc{l: l}, m); err != nil {
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

// parseAsmDialect parses the module-wide `asmdialect dialect` line (§1.1,
// §1.2 rule 11). It only checks that the token names a known dialect;
// whether that dialect is valid for the module's architecture is Verify's
// job (§9.34).
func parseAsmDialect(c *lc, m *vir.Module) error {
	c.accept(tIdent, "asmdialect")
	tok, err := c.expectIdent()
	if err != nil {
		return err
	}
	d := vir.AsmDialect(tok)
	switch d {
	case vir.DialectIntel, vir.DialectATT, vir.DialectA32, vir.DialectT32, vir.DialectNative:
	default:
		return c.l.errf("unknown asm dialect %q", tok)
	}
	if !c.done() {
		return c.l.errf("trailing tokens after asmdialect declaration")
	}
	m.AsmDialect = &d
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

// parseFnHead parses `name(params) ret attrs*` after the fn keyword. Shared
// by extern-fn lines (above) and fn definitions (func.go).
//
// Note: `entry` is accepted here structurally for both extern-fn and fn
// definitions, since this parser function is shared and the grammar's
// fn-attr production (§1.1) lists `entry` unconditionally. Whether `entry`
// is actually *meaningful* only on fn definitions (vs. extern fn, where it
// wouldn't make sense) is left to Verify (§9.4a) rather than rejected here
// at parse time.
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
		case "noreturn", "readonly", "inline", "noinline", "cold", "entry":
			attrs = append(attrs, vir.FunctionAttribute(tk.text))
			c.next()
		default:
			return name, params, variadic, ret, attrs, nil
		}
	}
	return name, params, variadic, ret, attrs, nil
}

// ---------------------------------------------------------------------------
// Encoding: header, structs, fnsigs, consts, globals, links, externs.
// ---------------------------------------------------------------------------

func encodeHeader(w func(string, ...any), m *vir.Module) {
	w("module %s\n", m.Name)
	if t := m.Target; t != nil {
		w("target %s %s", t.Arch, t.OS)
		if t.ABI != "" {
			w(" %s", t.ABI)
		}
		if len(t.Tiers) > 0 {
			w(" [%s]", strings.Join(t.Tiers, ", "))
		}
		w("\n")
	}
	if m.AsmDialect != nil {
		w("asmdialect %s\n", *m.AsmDialect)
	}
}

func encodeStructsSection(w func(string, ...any), m *vir.Module) {
	for _, s := range m.Structs {
		parts := make([]string, len(s.Fields))
		for i, f := range s.Fields {
			parts[i] = f.Name + " " + f.Type.String()
		}
		w("struct %s(%s)\n", s.Name, strings.Join(parts, ", "))
	}
}

func encodeFnSigsSection(w func(string, ...any), m *vir.Module) {
	// fnsig params are bare Types — fnsigs never name their parameters.
	for _, s := range m.FunctionSignatures {
		parts := make([]string, 0, len(s.Params)+1)
		for _, p := range s.Params {
			parts = append(parts, p.String())
		}
		if s.Variadic {
			parts = append(parts, "...")
		}
		w("fnsig %s(%s) %s\n", s.Name, strings.Join(parts, ", "), s.Ret.String())
	}
}

func encodeConstsSection(w func(string, ...any), m *vir.Module) {
	for _, c := range m.Constants {
		w("const %s %s = %s\n", c.Name, c.Type.String(), c.Value.String())
	}
}

func encodeGlobalsSection(w func(string, ...any), m *vir.Module) {
	for _, g := range m.Globals {
		if g.Export {
			w("export ")
		}
		w("global ")
		if g.TLS {
			w("tls ")
		}
		w("%s %s", g.Name, g.Type.String())
		if g.Align != 0 {
			w(" align %d", g.Align)
		}
		w(" = %s\n", encodeInit(g.Init))
	}
}

func encodeLinksSection(w func(string, ...any), m *vir.Module) {
	for _, l := range m.Links {
		w("link %s %q\n", l.Kind, l.Name)
	}
}

func encodeExternsSection(w func(string, ...any), m *vir.Module) {
	for _, g := range m.Externs {
		if g.Dependency == "" {
			w("extern :\n")
		} else {
			w("extern %q :\n", g.Dependency)
		}
		for _, f := range g.Functions {
			attrs := encodeAttrs(f.Attrs)
			params := encodeParams(f.Params, f.Variadic)
			w("    fn %s(%s) %s%s\n", f.Name, params, f.Ret.String(), attrs)
		}
		w("end\n")
	}
}

func encodeInit(i vir.ConstInit) string {
	switch x := i.(type) {
	case vir.InitZero:
		return "zero"
	case vir.InitLiteral:
		return x.Value.String()
	case vir.InitAddressOf:
		return "addr " + x.Name
	case vir.InitByteString:
		return quoteBytes(x.Data)
	case vir.InitAggregate:
		parts := make([]string, len(x.Elems))
		for j, e := range x.Elems {
			parts[j] = encodeInit(e)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return "<bad init>"
}

func quoteBytes(data []byte) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range data {
		switch c {
		case 0:
			b.WriteString(`\0`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			if c < 0x20 || c > 0x7e {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func encodeParams(ps []vir.Param, variadic bool) string {
	parts := make([]string, 0, len(ps)+1)
	for _, p := range ps {
		s := p.Name + " " + p.Type.String()
		if p.ByVal != "" {
			s += " byval[" + p.ByVal + "]"
		}
		if p.SRet != "" {
			s += " sret[" + p.SRet + "]"
		}
		parts = append(parts, s)
	}
	if variadic {
		parts = append(parts, "...")
	}
	return strings.Join(parts, ", ")
}

func encodeAttrs(attrs []vir.FunctionAttribute) string {
	s := ""
	for _, a := range attrs {
		s += " " + string(a)
	}
	if s != "" {
		s += " "
	} else {
		s = " "
	}
	return s
}