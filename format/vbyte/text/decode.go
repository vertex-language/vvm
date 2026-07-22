// decode.go
package text

import (
	"fmt"
	"math"
	"strconv"

	"github.com/vertex-language/vvm/ir/vir"
)

// Decode parses .vir text into an unverified *vir.Module. Structure and
// syntax are checked; semantics (name resolution, type checking, control
// flow, opcode legality) are not — that's ir/verify's job, always run
// separately by the caller (see format/README.md).
func Decode(src []byte) (m *vir.Module, err error) {
	toks, terr := tokenize(src)
	if terr != nil {
		return nil, terr
	}
	p := &parser{toks: toks}
	defer func() {
		if r := recover(); r != nil {
			if pe, ok := r.(parseError); ok {
				m, err = nil, pe
				return
			}
			panic(r)
		}
	}()
	m = p.parseModule()
	if p.cur().kind != tEOF {
		p.fail("unexpected trailing content %v", p.cur())
	}
	return m, nil
}

type parseError struct{ msg string }

func (e parseError) Error() string { return e.msg }

type parser struct {
	toks []token
	pos  int
}

func (p *parser) cur() token { return p.toks[p.pos] }

func (p *parser) tokAt(offset int) token {
	idx := p.pos + offset
	if idx >= len(p.toks) {
		return p.toks[len(p.toks)-1] // tEOF
	}
	return p.toks[idx]
}

func (p *parser) advance() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) fail(format string, args ...interface{}) {
	panic(parseError{fmt.Sprintf("line %d: %s", p.cur().line, fmt.Sprintf(format, args...))})
}

func (p *parser) isIdent(s string) bool {
	t := p.cur()
	return t.kind == tIdent && t.s == s
}

func (p *parser) isPunct(s string) bool {
	t := p.cur()
	return t.kind == tPunct && t.s == s
}

func (p *parser) peekIdentAt(offset int) string {
	t := p.tokAt(offset)
	if t.kind != tIdent {
		return ""
	}
	return t.s
}

func (p *parser) expectPunct(s string) {
	if !p.isPunct(s) {
		p.fail("expected %q, got %v", s, p.cur())
	}
	p.advance()
}

func (p *parser) expectIdentVal(s string) {
	if !p.isIdent(s) {
		p.fail("expected %q, got %v", s, p.cur())
	}
	p.advance()
}

func (p *parser) expectIdent() string {
	t := p.cur()
	if t.kind != tIdent {
		p.fail("expected identifier, got %v", t)
	}
	p.advance()
	return t.s
}

func (p *parser) expectString() string {
	t := p.cur()
	if t.kind != tString {
		p.fail("expected string literal, got %v", t)
	}
	p.advance()
	return t.s
}

func (p *parser) consumeExport() bool {
	if p.isIdent("export") {
		p.advance()
		return true
	}
	return false
}

func (p *parser) isEllipsis() bool {
	return p.tokAt(0).kind == tPunct && p.tokAt(0).s == "." &&
		p.tokAt(1).kind == tPunct && p.tokAt(1).s == "." &&
		p.tokAt(2).kind == tPunct && p.tokAt(2).s == "."
}

func (p *parser) consumeEllipsis() {
	p.advance()
	p.advance()
	p.advance()
}

func isSectionKeyword(s string) bool {
	switch s {
	case "struct", "fnsig", "const", "global", "link", "extern", "import", "fn", "export", "target", "namespace":
		return true
	}
	return false
}

var terminatorKeywords = map[string]bool{
	"br": true, "br_if": true, "switch": true, "return": true,
	"tailcall": true, "trap": true, "unreachable": true,
}

func isTerminatorKeyword(t token) bool {
	return t.kind == tIdent && terminatorKeywords[t.s]
}

var orderingSet = map[string]bool{
	"relaxed": true, "acquire": true, "release": true, "acqrel": true, "seqcst": true,
}

var fnAttrNames = map[string]vir.FunctionAttribute{
	"noreturn": vir.AttributeNoReturn,
	"readonly": vir.AttributeReadonly,
	"inline":   vir.AttributeInline,
	"noinline": vir.AttributeNoInline,
	"cold":     vir.AttributeCold,
	"entry":    vir.AttributeEntry,
	"extern_c": vir.AttributeExternC,
}

// looksLikeType reports whether ident text s spells one of the type
// productions (§3), as opposed to an ordinary open-vocabulary identifier
// (fnsig name, struct/field/label/link/fn name).
func looksLikeType(s string) bool {
	switch s {
	case "ptr", "void", "valist", "vec", "array", "struct", "f16", "f32", "f64":
		return true
	}
	if len(s) > 1 && s[0] == 'i' {
		for _, c := range s[1:] {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Literals, types, operands
// ---------------------------------------------------------------------------

func (p *parser) parseIntLiteral() int64 {
	neg := false
	if p.isPunct("-") {
		neg = true
		p.advance()
	}
	t := p.cur()
	if t.kind != tInt {
		p.fail("expected integer literal, got %v", t)
	}
	p.advance()
	if neg {
		return -t.i
	}
	return t.i
}

func (p *parser) parseLiteral() vir.Operand {
	neg := false
	if p.isPunct("-") {
		neg = true
		p.advance()
	}
	t := p.cur()
	switch {
	case t.kind == tInt:
		p.advance()
		v := t.i
		if neg {
			v = -v
		}
		return vir.IntLiteral(v)
	case t.kind == tFloat:
		p.advance()
		v := t.f
		if neg {
			v = -v
		}
		return vir.FloatLiteral(v)
	case t.kind == tString && !neg:
		p.advance()
		return vir.StringLiteral(t.s)
	case t.kind == tIdent && t.s == "NaN" && !neg:
		p.advance()
		return vir.FloatLiteral(math.NaN())
	case t.kind == tIdent && t.s == "Inf":
		p.advance()
		if neg {
			return vir.FloatLiteral(math.Inf(-1))
		}
		return vir.FloatLiteral(math.Inf(1))
	case t.kind == tIdent && t.s == "true" && !neg:
		p.advance()
		return vir.BoolLiteral(true)
	case t.kind == tIdent && t.s == "false" && !neg:
		p.advance()
		return vir.BoolLiteral(false)
	case t.kind == tIdent && t.s == "null" && !neg:
		p.advance()
		return vir.NullLiteral()
	}
	p.fail("expected literal, got %v", t)
	panic("unreachable")
}

func (p *parser) parseType() vir.Type {
	t := p.cur()
	if t.kind != tIdent {
		p.fail("expected type, got %v", t)
	}
	s := t.s
	switch {
	case s == "ptr":
		p.advance()
		return vir.Ptr
	case s == "void":
		p.advance()
		return vir.Void
	case s == "valist":
		p.advance()
		return vir.Valist
	case s == "struct":
		p.advance()
		name := p.expectIdent()
		return vir.StructType{Name: name}
	case s == "vec":
		p.advance()
		p.expectPunct("[")
		elem := p.parseType()
		p.expectPunct(",")
		ln := p.parseIntLiteral()
		p.expectPunct("]")
		return vir.VecType{Elem: elem, Len: int(ln)}
	case s == "array":
		p.advance()
		p.expectPunct("[")
		elem := p.parseType()
		p.expectPunct(",")
		ln := p.parseIntLiteral()
		p.expectPunct("]")
		return vir.ArrayType{Elem: elem, Len: int(ln)}
	case s == "f16":
		p.advance()
		return vir.F16
	case s == "f32":
		p.advance()
		return vir.F32
	case s == "f64":
		p.advance()
		return vir.F64
	case len(s) > 1 && s[0] == 'i':
		bits, err := strconv.Atoi(s[1:])
		if err != nil {
			p.fail("bad integer type %q", s)
		}
		p.advance()
		return vir.IntType{Bits: bits}
	}
	p.fail("unknown type %q", s)
	panic("unreachable")
}

// startsOperand reports whether the current token could begin an operand
// — used where the grammar makes an operand or operand-list optional
// (e.g. "return" with no value).
func (p *parser) startsOperand() bool {
	t := p.cur()
	switch t.kind {
	case tInt, tFloat, tString:
		return true
	case tPunct:
		return t.s == "-" || t.s == "("
	case tIdent:
		if t.s == "end" || t.s == "align" {
			return false
		}
		return true
	}
	return false
}

func (p *parser) parseOperand() vir.Operand {
	if p.isPunct("-") {
		return p.parseLiteral()
	}
	t := p.cur()
	switch t.kind {
	case tInt, tFloat, tString:
		return p.parseLiteral()
	case tIdent:
		switch t.s {
		case "true", "false", "null", "NaN", "Inf":
			return p.parseLiteral()
		}
		if orderingSet[t.s] {
			p.advance()
			return vir.OrderingOperand(t.s)
		}
		if looksLikeType(t.s) {
			return vir.TypeOperand(p.parseType())
		}
		name := p.expectIdent()
		if p.isPunct(".") {
			p.advance()
			second := p.expectIdent()
			return vir.QualifiedIdent(name, second)
		}
		return vir.Ident(name)
	case tPunct:
		if t.s == "(" {
			p.advance()
			var vals []int64
			if !p.isPunct(")") {
				vals = append(vals, p.parseIntLiteral())
				for p.isPunct(",") {
					p.advance()
					vals = append(vals, p.parseIntLiteral())
				}
			}
			p.expectPunct(")")
			return vir.VectorLiteral(vals...)
		}
	}
	p.fail("expected operand, got %v", t)
	panic("unreachable")
}

func (p *parser) parseConstInit() vir.ConstInit {
	if p.isIdent("zero") {
		p.advance()
		return vir.InitZero{}
	}
	if p.isIdent("addr") {
		p.advance()
		name := p.expectIdent()
		return vir.InitAddressOf{Name: name}
	}
	if p.isPunct("(") {
		p.advance()
		var elems []vir.ConstInit
		if !p.isPunct(")") {
			elems = append(elems, p.parseConstInit())
			for p.isPunct(",") {
				p.advance()
				elems = append(elems, p.parseConstInit())
			}
		}
		p.expectPunct(")")
		return vir.InitAggregate{Elems: elems}
	}
	if p.cur().kind == tString {
		s := p.cur().s
		p.advance()
		return vir.InitByteString{Data: []byte(s)}
	}
	return vir.InitLiteral{Value: p.parseLiteral()}
}

// ---------------------------------------------------------------------------
// Fields, params, attributes
// ---------------------------------------------------------------------------

func (p *parser) parseField() vir.Field {
	name := p.expectIdent()
	t := p.parseType()
	return vir.Field{Name: name, Type: t}
}

func (p *parser) parseParam() vir.Param {
	name := p.expectIdent()
	t := p.parseType()
	var byval, sret string
	for p.isIdent("byval") || p.isIdent("sret") {
		if p.isIdent("byval") {
			p.advance()
			p.expectPunct("[")
			byval = p.expectIdent()
			p.expectPunct("]")
		} else {
			p.advance()
			p.expectPunct("[")
			sret = p.expectIdent()
			p.expectPunct("]")
		}
	}
	return vir.Param{Name: name, Type: t, ByVal: byval, SRet: sret}
}

func (p *parser) parseParamList() ([]vir.Param, bool) {
	var params []vir.Param
	if p.isPunct(")") {
		return params, false
	}
	if p.isEllipsis() {
		p.consumeEllipsis()
		return params, true
	}
	params = append(params, p.parseParam())
	for p.isPunct(",") {
		p.advance()
		if p.isEllipsis() {
			p.consumeEllipsis()
			return params, true
		}
		params = append(params, p.parseParam())
	}
	return params, false
}

func (p *parser) parseFnAttrs() []vir.FunctionAttribute {
	var attrs []vir.FunctionAttribute
	for p.cur().kind == tIdent {
		a, ok := fnAttrNames[p.cur().s]
		if !ok {
			break
		}
		attrs = append(attrs, a)
		p.advance()
	}
	return attrs
}

// ---------------------------------------------------------------------------
// Function bodies: blocks, instructions, terminators
// ---------------------------------------------------------------------------

func (p *parser) parseInstructionLine(fb *vir.FunctionBuilder) {
	result := ""
	if p.cur().kind == tIdent && p.tokAt(1).kind == tPunct && p.tokAt(1).s == "=" {
		result = p.expectIdent()
		p.expectPunct("=")
	}
	opName := p.expectIdent()
	op, ok := vir.ParseOpcode(opName)
	if !ok {
		p.fail("unknown opcode %q", opName)
	}
	var suffix vir.Type
	sig := ""
	if p.isPunct(".") {
		p.advance()
		t := p.cur()
		if t.kind != tIdent {
			p.fail("expected type or identifier after '.', got %v", t)
		}
		if looksLikeType(t.s) {
			suffix = p.parseType()
		} else {
			sig = p.expectIdent()
		}
	}
	var args []vir.Operand
	align := 0
	if p.startsOperand() {
		args = append(args, p.parseOperand())
		for p.isPunct(",") {
			p.advance()
			if p.isIdent("align") {
				p.advance()
				align = int(p.parseIntLiteral())
				break
			}
			args = append(args, p.parseOperand())
		}
	} else if p.isPunct(",") {
		p.advance()
		if !p.isIdent("align") {
			p.fail("unexpected ','")
		}
		p.advance()
		align = int(p.parseIntLiteral())
	}
	fb.EmitInstruction(vir.Instruction{
		Result: result, Op: op, Suffix: suffix, Sig: sig, Args: args, Align: align,
	})
}

func (p *parser) parseTerminator(fb *vir.FunctionBuilder) {
	kw := p.expectIdent()
	switch kw {
	case "br":
		fb.Branch(p.expectIdent())
	case "br_if":
		cond := p.parseOperand()
		p.expectPunct(",")
		then := p.expectIdent()
		p.expectPunct(",")
		els := p.expectIdent()
		fb.BranchIf(cond, then, els)
	case "switch":
		val := p.parseOperand()
		p.expectPunct(",")
		def := p.expectIdent()
		var cases []vir.SwitchCase
		for p.isPunct(",") {
			p.advance()
			cv := p.parseIntLiteral()
			lbl := p.expectIdent()
			cases = append(cases, vir.SwitchCase{Value: cv, Label: lbl})
		}
		fb.Switch(val, def, cases...)
	case "return":
		if p.startsOperand() {
			fb.Return(p.parseOperand())
		} else {
			fb.Return()
		}
	case "tailcall":
		if p.isPunct(".") {
			p.advance()
			sig := p.expectIdent()
			fp := p.parseOperand()
			var args []vir.Operand
			for p.isPunct(",") {
				p.advance()
				args = append(args, p.parseOperand())
			}
			fb.TailCallIndirect(sig, fp, args...)
		} else {
			callee := p.expectIdent()
			var args []vir.Operand
			for p.isPunct(",") {
				p.advance()
				args = append(args, p.parseOperand())
			}
			fb.TailCall(callee, args...)
		}
	case "trap":
		fb.Trap()
	case "unreachable":
		fb.Unreachable()
	default:
		p.fail("unknown terminator %q", kw)
	}
}

func (p *parser) parseFunctionBody(fb *vir.FunctionBuilder) {
	for {
		if p.isIdent("end") {
			p.advance()
			return
		}
		if p.cur().kind == tIdent {
			next := p.tokAt(1)
			if next.kind == tPunct && next.s == ":" {
				name := p.expectIdent()
				p.expectPunct(":")
				fb.Label(name)
				continue
			}
		}
		if isTerminatorKeyword(p.cur()) {
			p.parseTerminator(fb)
			continue
		}
		p.parseInstructionLine(fb)
	}
}

// ---------------------------------------------------------------------------
// Module (§2.1 fixed section order)
// ---------------------------------------------------------------------------

func (p *parser) parseModule() *vir.Module {
	p.expectIdentVal("module")
	name := p.expectIdent()
	m := vir.NewModule(name)

	if p.isIdent("namespace") {
		p.advance()
		m.SetNamespace(p.expectString())
	}

	if p.isIdent("target") {
		p.advance()
		arch := p.expectIdent()
		os := p.expectIdent()
		abi := ""
		if p.cur().kind == tIdent && !isSectionKeyword(p.cur().s) {
			abi = p.expectIdent()
		}
		var tiers []string
		if p.isPunct("[") {
			p.advance()
			tiers = append(tiers, p.expectIdent())
			for p.isPunct(",") {
				p.advance()
				tiers = append(tiers, p.expectIdent())
			}
			p.expectPunct("]")
		}
		m.SetTarget(arch, os, abi, tiers...)
	}

	for p.isIdent("struct") || (p.isIdent("export") && p.peekIdentAt(1) == "struct") {
		exported := p.consumeExport()
		p.expectIdentVal("struct")
		name := p.expectIdent()
		p.expectPunct("(")
		var fields []vir.Field
		if !p.isPunct(")") {
			fields = append(fields, p.parseField())
			for p.isPunct(",") {
				p.advance()
				fields = append(fields, p.parseField())
			}
		}
		p.expectPunct(")")
		s := m.DeclareStruct(name, fields...)
		if exported {
			s.Exported()
		}
	}

	for p.isIdent("fnsig") || (p.isIdent("export") && p.peekIdentAt(1) == "fnsig") {
		exported := p.consumeExport()
		p.expectIdentVal("fnsig")
		name := p.expectIdent()
		p.expectPunct("(")
		var params []vir.Type
		variadic := false
		if !p.isPunct(")") {
			if p.isEllipsis() {
				p.consumeEllipsis()
				variadic = true
			} else {
				params = append(params, p.parseType())
				for p.isPunct(",") {
					p.advance()
					if p.isEllipsis() {
						p.consumeEllipsis()
						variadic = true
						break
					}
					params = append(params, p.parseType())
				}
			}
		}
		p.expectPunct(")")
		ret := p.parseType()
		fs := m.DeclareFunctionSignature(name, params, variadic, ret)
		if exported {
			fs.Exported()
		}
	}

	for p.isIdent("const") || (p.isIdent("export") && p.peekIdentAt(1) == "const") {
		exported := p.consumeExport()
		p.expectIdentVal("const")
		name := p.expectIdent()
		t := p.parseType()
		p.expectPunct("=")
		val := p.parseLiteral()
		c := m.DeclareConstant(name, t, val)
		if exported {
			c.Exported()
		}
	}

	for p.isIdent("global") || (p.isIdent("export") && p.peekIdentAt(1) == "global") {
		exported := p.consumeExport()
		p.expectIdentVal("global")
		tls := false
		if p.isIdent("tls") {
			p.advance()
			tls = true
		}
		name := p.expectIdent()
		t := p.parseType()
		align := 0
		if p.isIdent("align") {
			p.advance()
			align = int(p.parseIntLiteral())
		}
		p.expectPunct("=")
		init := p.parseConstInit()
		g := m.DeclareGlobal(name, t, init)
		if exported {
			g.Exported()
		}
		if tls {
			g.ThreadLocal()
		}
		if align != 0 {
			g.Aligned(align)
		}
	}

	for p.isIdent("link") {
		p.advance()
		kindStr := p.expectIdent()
		var kind vir.LinkKind
		switch kindStr {
		case "static":
			kind = vir.LinkStatic
		case "shared":
			kind = vir.LinkShared
		case "framework":
			kind = vir.LinkFramework
		default:
			p.fail("unknown link kind %q", kindStr)
		}
		m.DeclareLink(kind, p.expectString())
	}

	for p.isIdent("extern") {
		p.advance()
		dep := p.expectString()
		p.expectPunct(":")
		g := m.DeclareExternGroup(dep)
		for !p.isIdent("end") {
			p.expectIdentVal("fn")
			name := p.expectIdent()
			p.expectPunct("(")
			params, variadic := p.parseParamList()
			p.expectPunct(")")
			ret := p.parseType()
			attrs := p.parseFnAttrs()
			ef := g.DeclareFunction(name, params, ret, attrs...)
			if variadic {
				ef.SetVariadic()
			}
		}
		p.expectIdentVal("end")
	}

	for p.isIdent("import") {
		p.advance()
		m.DeclareImport(p.expectString())
	}

	for p.isIdent("fn") || (p.isIdent("export") && p.peekIdentAt(1) == "fn") {
		exported := p.consumeExport()
		p.expectIdentVal("fn")
		name := p.expectIdent()
		p.expectPunct("(")
		params, variadic := p.parseParamList()
		p.expectPunct(")")
		ret := p.parseType()
		attrs := p.parseFnAttrs()
		p.expectPunct(":")
		fb := m.DeclareFunction(name, params, ret, exported, attrs...)
		if variadic {
			fb.SetVariadic()
		}
		p.parseFunctionBody(fb)
	}

	return m
}