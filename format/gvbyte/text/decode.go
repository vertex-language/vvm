// decode.go

// Package text converts between *gvir.Module and canonical .gvir text
// (§2). Like the .vir codec it is a converter, not a checker: Decode
// produces an unverified module and Encode assumes one that already passed
// ir/verify.Verify, which the caller always runs separately.
package text

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/gvir"
)

// Decode parses .gvir text into an unverified *gvir.Module. Structure and
// syntax are checked; semantics (name binding, types, merge annotations,
// the uniformity analysis, capability gating, opcode legality) are not —
// that is ir/verify's job.
func Decode(src []byte) (m *gvir.Module, err error) {
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

// atLineEnd reports whether the current line is finished. Every .gvir
// construct occupies exactly one line (§2), so this is what bounds an
// operand list, a `return` with no value, and an operandless builtin.
func (p *parser) atLineEnd() bool {
	k := p.cur().kind
	return k == tEOL || k == tEOF
}

func (p *parser) skipEOL() {
	for p.cur().kind == tEOL {
		p.advance()
	}
}

func (p *parser) endLine() {
	if !p.atLineEnd() {
		p.fail("unexpected %v at end of line", p.cur())
	}
	p.skipEOL()
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

var terminatorKeywords = map[string]bool{
	"br": true, "br_if": true, "switch": true,
	"return": true, "unreachable": true,
}

func isTerminatorKeyword(t token) bool {
	return t.kind == tIdent && terminatorKeywords[t.s]
}

func isDimIdent(t token) bool {
	return t.kind == tIdent && (t.s == "x" || t.s == "y" || t.s == "z")
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

// parseLiteral reads a §2 literal: int, float, bool or null. String
// literals are deliberately absent — they appear only in `loc` (§2), which
// has its own production and its own parse path.
func (p *parser) parseLiteral() gvir.Operand {
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
		return gvir.IntLiteral(v)
	case t.kind == tFloat:
		p.advance()
		v := t.f
		if neg {
			v = -v
		}
		// The hex spelling is exact by construction and is the portable way
		// to pin a bit pattern (§2), so which form the source used is part
		// of its meaning and is carried through rather than re-derived.
		if t.hex {
			return gvir.HexFloatLiteral(v)
		}
		return gvir.FloatLiteral(v)
	case t.kind == tIdent && t.s == "NaN" && !neg:
		p.advance()
		return gvir.FloatLiteral(math.NaN())
	case t.kind == tIdent && t.s == "Inf":
		p.advance()
		if neg {
			return gvir.FloatLiteral(math.Inf(-1))
		}
		return gvir.FloatLiteral(math.Inf(1))
	case t.kind == tIdent && t.s == "true" && !neg:
		p.advance()
		return gvir.BoolLiteral(true)
	case t.kind == tIdent && t.s == "false" && !neg:
		p.advance()
		return gvir.BoolLiteral(false)
	case t.kind == tIdent && t.s == "null" && !neg:
		p.advance()
		return gvir.NullLiteral()
	}
	p.fail("expected literal, got %v", t)
	panic("unreachable")
}

// parseOperand reads the §2 operand grammar: ident | literal | ordering |
// scope. There are no type operands, no qualified idents and no vector
// literals in .gvir.
//
// The two closed vocabularies win over the ident production: `atomic_add.i32
// p, v, group` is only parseable if `group` is read as a scope. A value
// named `group`, `grid`, `none` or one of the orderings is therefore not
// representable in the text format, which the flat §2 namespace does not
// otherwise forbid.
func (p *parser) parseOperand() gvir.Operand {
	if p.isPunct("-") {
		return p.parseLiteral()
	}
	t := p.cur()
	switch t.kind {
	case tInt, tFloat:
		return p.parseLiteral()
	case tIdent:
		switch t.s {
		case "true", "false", "null", "NaN", "Inf":
			return p.parseLiteral()
		}
		if gvir.CanonicalOrderings[gvir.Ordering(t.s)] {
			p.advance()
			return gvir.OrderingOperand(gvir.Ordering(t.s))
		}
		if gvir.CanonicalScopes[gvir.Scope(t.s)] {
			p.advance()
			return gvir.ScopeOperand(gvir.Scope(t.s))
		}
		p.advance()
		return gvir.Ident(t.s)
	}
	p.fail("expected operand, got %v", t)
	panic("unreachable")
}

func intTypeBits(s string) (int, bool) {
	if len(s) < 2 || s[0] != 'i' {
		return 0, false
	}
	bits, err := strconv.Atoi(s[1:])
	if err != nil {
		return 0, false
	}
	return bits, true
}

func floatTypeOf(s string) (gvir.FloatType, bool) {
	brain := false
	rest := ""
	switch {
	case strings.HasPrefix(s, "bf"):
		brain, rest = true, s[2:]
	case strings.HasPrefix(s, "f"):
		rest = s[1:]
	default:
		return gvir.FloatType{}, false
	}
	bits, err := strconv.Atoi(rest)
	if err != nil {
		return gvir.FloatType{}, false
	}
	return gvir.FloatType{Bits: bits, Brain: brain}, true
}

// parseType reads any §2 type spelling: the storable `type` production, the
// three value-only forms (i1, vec[i1,N], submask), the bare `ptr` suffix
// word, and `void`. Which of those is legal in a given position — a struct
// field, a kernel parameter, a `const` — is a §4 rule and belongs to
// ir/verify, so the same routine serves every context. Widths are likewise
// not screened here: `i7` parses and is rejected there.
func (p *parser) parseType() gvir.Type {
	t := p.cur()
	if t.kind != tIdent {
		p.fail("expected type, got %v", t)
	}
	switch t.s {
	case "void":
		p.advance()
		return gvir.Void
	case "submask":
		p.advance()
		return gvir.Submask
	case "struct":
		p.advance()
		return gvir.StructType{Name: p.expectIdent()}
	case "ptr":
		p.advance()
		if !p.isPunct("[") {
			return gvir.PtrWord // the bare suffix word (§8.3, §11.4)
		}
		p.advance()
		space := p.expectIdent()
		if !gvir.CanonicalSpaces[gvir.AddrSpace(space)] {
			p.fail("unknown address space %q (§5)", space)
		}
		p.expectPunct("]")
		return gvir.PtrType{Space: gvir.AddrSpace(space)}
	case "vec":
		p.advance()
		p.expectPunct("[")
		elem := p.parseType()
		p.expectPunct(",")
		ln := p.parseIntLiteral()
		p.expectPunct("]")
		return gvir.VecType{Elem: elem, Len: int(ln)}
	case "array":
		p.advance()
		p.expectPunct("[")
		elem := p.parseType()
		p.expectPunct(",")
		ln := p.parseIntLiteral()
		p.expectPunct("]")
		return gvir.ArrayType{Elem: elem, Len: int(ln)}
	}
	if bits, ok := intTypeBits(t.s); ok {
		p.advance()
		return gvir.IntType{Bits: bits}
	}
	if ft, ok := floatTypeOf(t.s); ok {
		p.advance()
		return ft
	}
	p.fail("unknown type %q", t.s)
	panic("unreachable")
}

func (p *parser) parseConstInit() gvir.ConstInit {
	if p.isIdent("zero") {
		p.advance()
		return gvir.InitZero{}
	}
	if p.isPunct("(") {
		p.advance()
		elems := []gvir.ConstInit{p.parseConstInit()}
		for p.isPunct(",") {
			p.advance()
			elems = append(elems, p.parseConstInit())
		}
		p.expectPunct(")")
		return gvir.InitAggregate{Elems: elems}
	}
	return gvir.InitLiteral{Value: p.parseLiteral()}
}

// ---------------------------------------------------------------------------
// Module header (§2 fixed section order)
// ---------------------------------------------------------------------------

func (p *parser) parseModule() *gvir.Module {
	p.skipEOL()

	p.expectIdentVal("gvir")
	major, minor := p.parseVersion()
	p.endLine()

	p.expectIdentVal("module")
	name := p.expectIdent()
	p.endLine()

	m := gvir.NewModule(name).SetVersion(major, minor)

	// target-decl is mandatory (§2): a .gvir module without one is not a
	// module, so this is structure, not semantics.
	p.expectIdentVal("target")
	backends := []gvir.Backend{p.parseBackend()}
	for p.isPunct(",") {
		p.advance()
		backends = append(backends, p.parseBackend())
	}
	m.SetTarget(backends...)
	p.endLine()

	if p.isIdent("float_profile") {
		p.advance()
		contract, approx := p.parseFloatProfile()
		m.SetFloatProfile(contract, approx)
		p.endLine()
	}

	for p.isIdent("struct") {
		p.parseStructDecl(m)
	}
	for p.isIdent("const") {
		p.parseConstDecl(m)
	}
	for p.isIdent("func") {
		p.parseFuncDef(m)
	}
	for p.isIdent("kernel") {
		p.parseKernelDef(m)
	}

	return m
}

// parseVersion reads `MAJOR "." MINOR`. The lexer has no way to know that
// "1.0" here is a version pair rather than a dec-float, so the raw text is
// split back apart; the spaced spelling is accepted too.
func (p *parser) parseVersion() (int, int) {
	if t := p.cur(); t.kind == tFloat {
		p.advance()
		dot := strings.IndexByte(t.s, '.')
		if dot < 0 {
			p.fail("expected a MAJOR.MINOR version, got %v", t)
		}
		major, err1 := strconv.Atoi(t.s[:dot])
		minor, err2 := strconv.Atoi(t.s[dot+1:])
		if err1 != nil || err2 != nil {
			p.fail("bad version declaration %q", t.s)
		}
		return major, minor
	}
	major := int(p.parseIntLiteral())
	p.expectPunct(".")
	minor := int(p.parseIntLiteral())
	return major, minor
}

func (p *parser) parseBackend() gvir.Backend {
	name := p.expectIdent()
	var kind gvir.BackendKind
	switch name {
	case string(gvir.BackendPTX):
		kind = gvir.BackendPTX
	case string(gvir.BackendAMDGCN):
		kind = gvir.BackendAMDGCN
	case string(gvir.BackendMSL):
		kind = gvir.BackendMSL
	default:
		p.fail("unknown backend %q — expected ptx, amdgcn or msl (§3)", name)
	}
	var archs []string
	if p.isPunct("[") {
		p.advance()
		archs = append(archs, p.parseArch())
		for p.isPunct(",") {
			p.advance()
			archs = append(archs, p.parseArch())
		}
		p.expectPunct("]")
	}
	// An amdgcn backend with no arch list is a verification error, not a
	// syntax error (§3, targets.go) — it is recorded as written and left
	// to ir/verify.
	return gvir.Backend{Kind: kind, Archs: archs}
}

// parseArch accepts the dotted alias spellings (`metal3.2`) as well as
// canonical names. §3 rejects aliases, but rejecting them *here* would
// bury gvir.ArchAlias's "write metal32" diagnostic under a syntax error, so
// the spelling is preserved verbatim for ir/verify to reject properly.
func (p *parser) parseArch() string {
	s := p.expectIdent()
	for p.isPunct(".") {
		p.advance()
		t := p.cur()
		if t.kind != tInt {
			p.fail("expected an arch name, got %v", t)
		}
		p.advance()
		s += "." + t.s
	}
	return s
}

func (p *parser) parseFloatProfile() (contract, approx bool) {
	for {
		switch flag := p.expectIdent(); flag {
		case "contract":
			contract = true
		case "approx":
			approx = true
		default:
			p.fail("unknown float_profile flag %q — expected contract or approx (§11.6)", flag)
		}
		if !p.isPunct(",") {
			return contract, approx
		}
		p.advance()
	}
}

func (p *parser) parseStructDecl(m *gvir.Module) {
	p.expectIdentVal("struct")
	name := p.expectIdent()
	p.expectPunct("(")
	if p.isPunct(")") {
		p.fail("struct %s: a struct declaration carries at least one field (§2)", name)
	}
	fields := []gvir.Field{p.parseField()}
	for p.isPunct(",") {
		p.advance()
		fields = append(fields, p.parseField())
	}
	p.expectPunct(")")
	p.endLine()
	m.DeclareStruct(name, fields...)
}

func (p *parser) parseField() gvir.Field {
	name := p.expectIdent()
	return gvir.Field{Name: name, Type: p.parseType()}
}

func (p *parser) parseConstDecl(m *gvir.Module) {
	p.expectIdentVal("const")
	name := p.expectIdent()
	t := p.parseType()
	p.expectPunct("=")
	init := p.parseConstInit()
	p.endLine()
	m.DeclareConst(name, t, init)
}

func (p *parser) parseParamList() []gvir.Param {
	if p.isPunct(")") {
		return nil
	}
	params := []gvir.Param{p.parseParam()}
	for p.isPunct(",") {
		p.advance()
		params = append(params, p.parseParam())
	}
	return params
}

func (p *parser) parseParam() gvir.Param {
	name := p.expectIdent()
	return gvir.Param{Name: name, Type: p.parseType()}
}

// ---------------------------------------------------------------------------
// Funcs and kernels (§6)
// ---------------------------------------------------------------------------

func (p *parser) parseFuncDef(m *gvir.Module) {
	p.expectIdentVal("func")
	name := p.expectIdent()
	p.expectPunct("(")
	params := p.parseParamList()
	p.expectPunct(")")
	ret := p.parseType()
	fb := m.DeclareFunc(name, params, ret)
	if p.isIdent("readonly") {
		p.advance()
		fb.Readonly()
	}
	p.expectPunct(":")
	p.endLine()
	p.parseBody(&fb.BodyBuilder)
}

func (p *parser) parseKernelDef(m *gvir.Module) {
	p.expectIdentVal("kernel")
	name := p.expectIdent()
	p.expectPunct("(")
	params := p.parseParamList()
	p.expectPunct(")")
	kb := m.DeclareKernel(name, params...)

attrs:
	for p.cur().kind == tIdent {
		switch p.cur().s {
		case "group_size":
			p.advance()
			x := int(p.parseIntLiteral())
			p.expectPunct(",")
			y := int(p.parseIntLiteral())
			p.expectPunct(",")
			z := int(p.parseIntLiteral())
			kb.GroupSize(x, y, z)
		case "max_group_size":
			p.advance()
			kb.MaxGroupSize(int(p.parseIntLiteral()))
		case "subgroup_size":
			p.advance()
			kb.SubgroupSize(int(p.parseIntLiteral()))
		case "dynamic_group":
			p.advance()
			gname := p.expectIdent()
			align := 0
			if p.isIdent("align") {
				p.advance()
				align = int(p.parseIntLiteral())
			}
			kb.DynamicGroup(gname, align)
		default:
			break attrs
		}
	}
	p.expectPunct(":")
	p.endLine()

	// group-decl* precedes the entry block (§2). A body line could in
	// principle bind a value named `group`, but only as `group = ...`, so
	// the following ident is what distinguishes a declaration.
	for p.isIdent("group") && p.tokAt(1).kind == tIdent {
		p.advance()
		gname := p.expectIdent()
		t := p.parseType()
		align := 0
		if p.isIdent("align") {
			p.advance()
			align = int(p.parseIntLiteral())
		}
		p.endLine()
		kb.Group(gname, t, align)
	}

	p.parseBody(&kb.BodyBuilder)
}

// ---------------------------------------------------------------------------
// Bodies: blocks, merge annotations, body-lines, terminators (§7)
// ---------------------------------------------------------------------------

func (p *parser) parseBody(b *gvir.BodyBuilder) {
	// §2 attaches merge-decl to `block`, never to `entry-block`. Accepting
	// one here anyway keeps a module that carries it round-trippable and
	// leaves the question where module.go puts it: with ir/verify.
	p.parseMergeDecl(b)

	for {
		if p.cur().kind == tEOF {
			p.fail("unexpected end of input: missing \"end\"")
		}
		if p.isIdent("end") {
			p.advance()
			p.endLine()
			return
		}
		if p.cur().kind == tIdent && p.tokAt(1).kind == tPunct && p.tokAt(1).s == ":" {
			label := p.expectIdent()
			p.expectPunct(":")
			p.endLine()
			b.Label(label)
			p.parseMergeDecl(b)
			continue
		}
		if isTerminatorKeyword(p.cur()) {
			p.parseTerminator(b)
			p.endLine()
			continue
		}
		p.parseBodyLine(b)
		p.endLine()
	}
}

func (p *parser) parseMergeDecl(b *gvir.BodyBuilder) {
	switch {
	case p.isIdent("merge") && p.tokAt(1).kind == tIdent:
		p.advance()
		b.Merge(p.expectIdent())
		p.endLine()
	case p.isIdent("loop_merge") && p.tokAt(1).kind == tIdent:
		p.advance()
		exit := p.expectIdent()
		p.expectPunct(",")
		cont := p.expectIdent()
		b.LoopMerge(exit, cont)
		p.endLine()
	}
}

// parseBodyLine reads an inst, a builtin-inst, a barrier-inst, an
// alloca-line or a loc-line — every §2 body-line shape.
func (p *parser) parseBodyLine(b *gvir.BodyBuilder) {
	result := ""
	if p.cur().kind == tIdent && p.tokAt(1).kind == tPunct && p.tokAt(1).s == "=" {
		result = p.expectIdent()
		p.expectPunct("=")
	}

	opName := p.expectIdent()
	op, ok := gvir.ParseOpcode(opName)
	if !ok {
		p.fail("unknown opcode %q", opName)
	}

	if op == gvir.OpLoc {
		if result != "" {
			p.fail("loc produces no value (§2)")
		}
		p.parseLocLine(b)
		return
	}

	inst := gvir.Instruction{Result: result, Op: op}

	if p.isPunct(".") {
		p.advance()
		switch {
		case op == gvir.OpBarrier:
			// barrier-inst is its own production (§10.1): the suffix is an
			// execution scope, which is why the opcode is special-cased
			// here rather than read off an exported suffix kind.
			scope := gvir.ExecScope(p.expectIdent())
			if scope != gvir.ExecSubgroup && scope != gvir.ExecGroup {
				p.fail("barrier execution scope must be subgroup or group (§10.1), got %q", string(scope))
			}
			inst.Exec = scope
		case op.AcceptsDim() && isDimIdent(p.cur()):
			inst.Dim = gvir.Dim(p.expectIdent())
		default:
			inst.Suffix = p.parseType()
		}
	}

	if op == gvir.OpBarrier {
		// The optional memory scope is comma-attached, not an operand list.
		if p.isPunct(",") {
			p.advance()
			inst.Args = append(inst.Args, p.parseOperand())
		}
	} else {
		inst.Args, inst.Align = p.parseOperandsAndAlign()
	}

	b.EmitInstruction(inst)
}

// parseOperandsAndAlign reads the operand list and the align clause. §2
// spells the clause two ways — `, align N` on an inst and a bare `align N`
// on an alloca-line — so the comma is required only between operands and
// both shapes fall out of the same loop. A value named `align` is not
// representable, exactly as in the .vir codec.
func (p *parser) parseOperandsAndAlign() ([]gvir.Operand, int) {
	var args []gvir.Operand
	align := 0
	for !p.atLineEnd() {
		if len(args) > 0 {
			p.expectPunct(",")
		}
		if p.isIdent("align") {
			p.advance()
			align = int(p.parseIntLiteral())
			break
		}
		args = append(args, p.parseOperand())
	}
	return args, align
}

// parseLocLine reads `loc string-literal int-literal int-literal?` — space
// separated, not a comma list (§2).
func (p *parser) parseLocLine(b *gvir.BodyBuilder) {
	file := p.expectString()
	args := []gvir.Operand{
		gvir.StringLiteral(file),
		gvir.IntLiteral(p.parseIntLiteral()),
	}
	if !p.atLineEnd() {
		args = append(args, gvir.IntLiteral(p.parseIntLiteral()))
	}
	b.EmitInstruction(gvir.Instruction{Op: gvir.OpLoc, Args: args})
}

func (p *parser) parseTerminator(b *gvir.BodyBuilder) {
	switch kw := p.expectIdent(); kw {
	case "br":
		b.Br(p.expectIdent())
	case "br_if":
		cond := p.parseOperand()
		p.expectPunct(",")
		then := p.expectIdent()
		p.expectPunct(",")
		els := p.expectIdent()
		b.BrIf(cond, then, els)
	case "switch":
		val := p.parseOperand()
		p.expectPunct(",")
		def := p.expectIdent()
		var cases []gvir.SwitchCase
		for p.isPunct(",") {
			p.advance()
			cv := p.parseIntLiteral()
			lbl := p.expectIdent()
			cases = append(cases, gvir.SwitchCase{Value: cv, Label: lbl})
		}
		b.Switch(val, def, cases...)
	case "return":
		if p.atLineEnd() {
			b.Return()
		} else {
			b.Return(p.parseOperand())
		}
	case "unreachable":
		b.Unreachable()
	default:
		p.fail("unknown terminator %q", kw)
	}
}