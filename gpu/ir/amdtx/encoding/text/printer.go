// Package text implements the canonical AMDTX text form (§15).
//
// The printer is a pure formatter. It runs verification first and refuses to
// print a module that does not verify; it derives every mapping from IR data
// rather than from re-parsing its own output; and it emits no comments at
// all. Instr.Comment and Raw.Comment are dropped, which is what makes
// print(parse(text)) lossy on comments by design while parse(print(m)) stays
// exact (P5, V32).
package text

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
)

const indentUnit = "    " // rule 2: four spaces per nesting level

// InvalidModuleError reports that printing was refused because the module
// does not verify. Only Error-severity diagnostics block printing; warnings
// (W1, W2) do not.
type InvalidModuleError struct{ Diags []amdtx.Diag }

func (e *InvalidModuleError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "amdtx/text: refusing to print an invalid module (%d error", len(e.Diags))
	if len(e.Diags) != 1 {
		b.WriteByte('s')
	}
	b.WriteByte(')')
	for _, d := range e.Diags {
		b.WriteString("\n\t" + d.String())
	}
	return b.String()
}

// Print renders m as canonical AMDTX text. It verifies first and returns an
// *InvalidModuleError without producing output if the module is invalid.
func Print(m *amdtx.Module) (string, error) {
	var b strings.Builder
	if err := Fprint(&b, m); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Fprint is Print writing to w.
func Fprint(w io.Writer, m *amdtx.Module) error {
	if m == nil {
		return fmt.Errorf("amdtx/text: nil module")
	}
	var bad []amdtx.Diag
	for _, d := range amdtx.Verify(m) {
		if d.Severity == amdtx.Error {
			bad = append(bad, d)
		}
	}
	if len(bad) > 0 {
		return &InvalidModuleError{Diags: bad}
	}
	_, err := io.WriteString(w, PrintUnchecked(m))
	return err
}

// PrintUnchecked formats m without verifying it. It exists so a failing
// module can be inspected in a diagnostic; its output is not guaranteed to
// be a conforming .amdtx module and it is not the canonical printer of §15.
func PrintUnchecked(m *amdtx.Module) string {
	p := &printer{}
	p.module(m)
	return p.b.String()
}

// ---- Printer state --------------------------------------------------------

type printer struct {
	b     strings.Builder
	depth int
}

func (p *printer) line(s string) {
	if s != "" {
		for i := 0; i < p.depth; i++ {
			p.b.WriteString(indentUnit)
		}
		p.b.WriteString(s)
	}
	p.b.WriteByte('\n')
}

func (p *printer) blank() { p.b.WriteByte('\n') }

// ---- Module ---------------------------------------------------------------

func (p *printer) module(m *amdtx.Module) {
	if m == nil {
		return
	}

	// §3.1: preamble, file table, module objects, bodies.
	p.line(".amdtx " + m.Version.String() + ";")
	p.line(".target " + m.Target.Name() + ";")
	p.line(".wave " + m.Wave.String() + ";")

	if files := m.Files(); len(files) > 0 {
		p.blank()
		for _, f := range files {
			p.line(".file " + itoa(f.Index) + " " + strconv.Quote(f.Name) + ";")
		}
	}

	var objects []*amdtx.Object
	for _, d := range m.Decls() {
		if o, ok := d.(*amdtx.Object); ok {
			objects = append(objects, o)
		}
	}
	if len(objects) > 0 {
		p.blank()
		for _, o := range objects {
			p.object(o)
		}
	}

	// Rule 10: exactly one blank line between bodies, none at end of file.
	for _, d := range m.Decls() {
		switch x := d.(type) {
		case *amdtx.Kernel:
			p.blank()
			p.kernel(x)
		case *amdtx.Func:
			p.blank()
			p.function(x)
		}
	}
}

// object prints a module-scope .global or .shared declaration.
//
// The bracket is always printed, and an omitted length stays omitted: an
// Object with Len 0 takes its length from Init or from
// .dynamic_group_segment, so printing the derived length would make
// parse(print(m)) differ from m in the Len field (V32).
func (p *printer) object(o *amdtx.Object) {
	var b strings.Builder
	if s := o.Linkage.String(); s != "" {
		b.WriteString(s + " ")
	}
	b.WriteString(o.Space.String() + " ")
	if o.Align != 0 {
		b.WriteString(".align " + itoa(o.Align) + " ")
	}
	b.WriteString(o.Width.String() + " ")
	b.WriteString(o.Name)
	b.WriteByte('[')
	if o.Len != 0 {
		b.WriteString(itoa(o.Len))
	}
	b.WriteByte(']')
	if len(o.Init) > 0 {
		b.WriteString(" = {" + joinOperands(o.Init) + "}")
	}
	b.WriteByte(';')
	p.line(b.String())
}

// ---- Bodies ---------------------------------------------------------------

func (p *printer) kernel(k *amdtx.Kernel) {
	head := ""
	if s := k.Linkage.String(); s != "" {
		head = s + " "
	}
	head += ".kernel " + k.Name

	if len(k.Params) == 0 {
		p.line(head + "() {")
	} else {
		p.line(head + "(")
		p.depth++
		lines := paramLines(k.Params)
		for i, ln := range lines {
			if i < len(lines)-1 {
				p.line(ln + ",")
			} else {
				p.line(ln)
			}
		}
		p.depth--
		p.line(") {")
	}

	p.depth++
	wrote := p.launch(k.Launch)
	wrote = p.regs(k.Body, wrote) || wrote
	p.body(k.Body, wrote)
	p.depth--
	p.line("}")
}

func (p *printer) function(f *amdtx.Func) {
	params := make([]string, len(f.Params))
	for i, fp := range f.Params {
		params[i] = fp.Class.String() + " %" + fp.Name
	}
	p.line(".func " + f.Name + "(" + strings.Join(params, ", ") + ") {")

	p.depth++
	wrote := p.regs(f.Body, false)
	p.body(f.Body, wrote)
	p.depth--
	p.line("}")
}

func (p *printer) body(b *amdtx.Body, wrote bool) {
	if b == nil || b.Len() == 0 {
		return
	}
	if wrote {
		p.blank()
	}
	p.items(b)
}

// launch prints the launch-directive block. Rule 3 column-aligns the values
// within a body; rule 9 omits zero-valued directives. It reports whether
// anything was printed.
func (p *printer) launch(l amdtx.Launch) bool {
	type dir struct{ name, val string }
	var ds []dir
	add := func(name, val string) { ds = append(ds, dir{name, val}) }

	if l.KernargSize != 0 {
		add(".kernarg_size", itoa(l.KernargSize))
	}
	if l.KernargAlign != 0 {
		add(".kernarg_align", itoa(l.KernargAlign))
	}
	if l.GroupSegmentSize != 0 {
		add(".group_segment_size", itoa(l.GroupSegmentSize))
	}
	if l.DynamicGroupSegment {
		add(".dynamic_group_segment", "") // boolean flag: presence is the value
	}
	if l.PrivateSegmentSize != 0 {
		add(".private_segment_size", itoa(l.PrivateSegmentSize))
	}
	if !l.ReqdWorkgroupSize.IsZero() {
		add(".reqd_workgroup_size", l.ReqdWorkgroupSize.String())
	}
	if l.MaxFlatWorkgroupSize != 0 {
		add(".max_flat_workgroup_size", itoa(l.MaxFlatWorkgroupSize))
	}
	if l.WavesPerEU != [2]int{} {
		add(".waves_per_eu", itoa(l.WavesPerEU[0])+", "+itoa(l.WavesPerEU[1]))
	}
	if l.KernargPreload != 0 {
		add(".kernarg_preload", itoa(l.KernargPreload))
	}
	if len(ds) == 0 {
		return false
	}

	// Values end at a common column; the valueless flag takes no part in the
	// computation and is printed bare.
	col := 0
	for _, d := range ds {
		if d.val == "" {
			continue
		}
		if n := len(d.name) + 1 + len(d.val); n > col {
			col = n
		}
	}
	for _, d := range ds {
		if d.val == "" {
			p.line(d.name + ";")
			continue
		}
		pad := col - len(d.name) - len(d.val)
		if pad < 1 {
			pad = 1
		}
		p.line(d.name + strings.Repeat(" ", pad) + d.val + ";")
	}
	return true
}

// regs prints the .reg declarations, grouped by class in first-appearance
// order and never reordered within a class (rule 4). It reports whether
// anything was printed.
func (p *printer) regs(b *amdtx.Body, wrote bool) bool {
	if b == nil || b.Regs == nil {
		return false
	}
	decls := b.Regs.Decls()
	if len(decls) == 0 {
		return false
	}

	var order []amdtx.RegClass
	byClass := map[amdtx.RegClass][]amdtx.RegDecl{}
	for _, d := range decls {
		if _, ok := byClass[d.Class]; !ok {
			order = append(order, d.Class)
		}
		byClass[d.Class] = append(byClass[d.Class], d)
	}

	col := 0
	for _, c := range order {
		if n := len(".reg " + c.String()); n > col {
			col = n
		}
	}

	if wrote {
		p.blank()
	}
	for _, c := range order {
		head := ".reg " + c.String()
		pad := strings.Repeat(" ", col-len(head)+1)
		for _, d := range byClass[c] {
			p.line(head + pad + regNames(d) + ";")
		}
	}
	return true
}

func regNames(d amdtx.RegDecl) string {
	if d.Block != nil {
		return "%" + d.Block.Prefix + "<" + itoa(d.Block.Count) + ">"
	}
	names := make([]string, len(d.Names))
	for i, n := range d.Names {
		names[i] = "%" + n
	}
	return strings.Join(names, ", ")
}

// ---- Statements -----------------------------------------------------------

func (p *printer) items(b *amdtx.Body) {
	its := b.Items()
	for i, it := range its {
		switch x := it.(type) {
		case *amdtx.Instr:
			p.line(instrText(x))

		case *amdtx.LabelBind:
			p.line(x.Label.Name() + ":")

		case *amdtx.LocDir:
			// §14, W2: a trailing .loc attaches to nothing and is dropped.
			if i == len(its)-1 {
				continue
			}
			p.line(locText(x))

		case *amdtx.IfStmt:
			head := "if"
			if s := x.Assert.String(); s != "" {
				head += " " + s
			}
			p.line(head + " " + opText(x.Guard) + " {")
			p.depth++
			p.items(x.Then)
			p.depth--
			if x.Else != nil {
				p.line("} else {")
				p.depth++
				p.items(x.Else)
				p.depth--
			}
			p.line("}")

		case *amdtx.LoopStmt:
			p.line("loop {")
			p.depth++
			p.items(x.Body)
			p.depth--
			p.line("}")

		case *amdtx.BreakIf:
			p.line(guardStmt("breakif", x.Guard, x.Assert))

		case *amdtx.ContinueIf:
			p.line(guardStmt("continueif", x.Guard, x.Assert))

		case *amdtx.Raw:
			p.raw(x)
		}
	}
}

func guardStmt(kw string, g amdtx.Operand, a amdtx.Uniformity) string {
	s := kw
	if u := a.String(); u != "" {
		s += " " + u
	}
	return s + " " + opText(g) + ";"
}

func locText(d *amdtx.LocDir) string {
	idx := 0
	if d.File != nil {
		idx = d.File.Index
	}
	s := ".loc " + itoa(idx) + " " + itoa(d.Line)
	if d.Col != 0 { // the column is optional in the grammar
		s += " " + itoa(d.Col)
	}
	return s + ";"
}

// instrText renders one instruction: mnemonic, operands, fence ordering and
// scope, modifiers, then the pinned encoding (§3.3, §8.3, §9.4). The
// mnemonic is derived from Op and Width, never stored, so equivalent IR
// prints identically. Comments are not printed.
func instrText(in *amdtx.Instr) string {
	var b strings.Builder
	b.WriteString(in.Mnemonic())

	if ops := in.Operands(); len(ops) > 0 {
		b.WriteString(" " + joinOperands(ops))
	}
	// Fence carries its ordering and scope in place of operands (§12.4).
	if s := in.Ord.String(); s != "" {
		b.WriteString(" " + s)
	}
	if s := in.Scope.String(); s != "" {
		b.WriteString(" " + s)
	}
	for _, m := range in.Mods {
		b.WriteString(" " + m.Text())
	}
	if e := in.Enc.Text(); e != "" { // rule 8: .enc(auto) is never printed
		b.WriteString(" " + e)
	}
	b.WriteByte(';')
	return b.String()
}

// raw prints an escape hatch. The def/use/clobber clauses go on their own
// continuation lines, in the order the substitution index runs over them
// (§13): defs first, then uses, then clobbers.
func (p *printer) raw(r *amdtx.Raw) {
	head := ""
	if r.IsBytes() {
		words := make([]string, len(r.Bytes))
		for i, w := range r.Bytes {
			words[i] = fmt.Sprintf("0x%08x", w)
		}
		head = "rawbytes " + strings.Join(words, ", ")
	} else {
		head = "raw " + strconv.Quote(r.Text)
	}

	var clauses []string
	if len(r.Defs) > 0 {
		clauses = append(clauses, ".def("+joinOperands(r.Defs)+")")
	}
	if len(r.Uses) > 0 {
		clauses = append(clauses, ".use("+joinOperands(r.Uses)+")")
	}
	if len(r.Clobbers) > 0 {
		clauses = append(clauses, ".clobber("+joinOperands(r.Clobbers)+")")
	}
	if len(clauses) == 0 {
		p.line(head + ";")
		return
	}

	p.line(head)
	p.depth++
	for i, c := range clauses {
		if i == len(clauses)-1 {
			p.line(c + ";")
		} else {
			p.line(c)
		}
	}
	p.depth--
}

// ---- Parameters -----------------------------------------------------------

// paramLines renders a kernel parameter list one parameter per line, with
// the qualifier and width columns aligned across the list (§18).
func paramLines(ps []*amdtx.Param) []string {
	quals := make([]string, len(ps))
	widths := make([]string, len(ps))
	qCol, wCol := 0, 0

	for i, p := range ps {
		var q []string
		if s := p.Kind.String(); s != "" {
			q = append(q, s)
		}
		if s := p.Space.String(); s != "" {
			q = append(q, s)
		}
		if s := p.Access.String(); s != "" {
			q = append(q, s)
		}
		quals[i] = strings.Join(q, " ")
		widths[i] = p.Width.String()
		if n := len(quals[i]); n > qCol {
			qCol = n
		}
		if n := len(widths[i]); n > wCol {
			wCol = n
		}
	}

	out := make([]string, len(ps))
	for i, p := range ps {
		var b strings.Builder
		b.WriteString(".param")
		if qCol > 0 {
			b.WriteString(" " + pad(quals[i], qCol))
		}
		b.WriteString(" " + pad(widths[i], wCol))
		if p.Align != 0 { // align-qual follows the width (§3.3)
			b.WriteString(" .align " + itoa(p.Align))
		}
		b.WriteString(" " + p.Name)
		out[i] = b.String()
	}
	return out
}

// ---- Small helpers --------------------------------------------------------

func joinOperands(ops []amdtx.Operand) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		parts[i] = opText(o)
	}
	return strings.Join(parts, ", ")
}

func opText(o amdtx.Operand) string {
	if o == nil {
		return "<nil>"
	}
	return o.Text()
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func itoa(n int) string { return strconv.Itoa(n) }