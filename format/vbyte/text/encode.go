// encode.go
package text

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
)

// Encode renders m as canonical .vir text (§2.3). It assumes m has
// already passed ir/verify.Verify — like the binary codec, this package
// doesn't validate anything, it just converts.
func Encode(m *vir.Module) ([]byte, error) {
	var b strings.Builder

	b.WriteString("module " + m.Name + "\n")

	if m.Namespace != "" {
		fmt.Fprintf(&b, "namespace %s\n", strconv.Quote(m.Namespace))
	}

	if m.Target != nil {
		fmt.Fprintf(&b, "target %s %s %s", m.Target.Arch, m.Target.OS, m.Target.ABI)
		if len(m.Target.Tiers) > 0 {
			b.WriteString(" [" + strings.Join(m.Target.Tiers, ", ") + "]")
		}
		b.WriteString("\n")
	}

	for _, s := range m.Structs {
		writeStruct(&b, s)
	}
	for _, fs := range m.FunctionSignatures {
		writeFnSig(&b, fs)
	}
	for _, c := range m.Constants {
		writeConst(&b, c)
	}
	for _, g := range m.Globals {
		writeGlobal(&b, g)
	}
	for _, l := range m.Links {
		fmt.Fprintf(&b, "link %s %s\n", string(l.Kind), strconv.Quote(l.Name))
	}
	for _, eg := range m.Externs {
		writeExternGroup(&b, eg)
	}
	for _, im := range m.Imports {
		fmt.Fprintf(&b, "import %s\n", strconv.Quote(im.Path))
	}
	for _, f := range m.Functions {
		writeFunction(&b, f)
	}

	return []byte(b.String()), nil
}

func writeStruct(b *strings.Builder, s *vir.Struct) {
	if s.Export {
		b.WriteString("export ")
	}
	fmt.Fprintf(b, "struct %s(", s.Name)
	for i, f := range s.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s %s", f.Name, f.Type.String())
	}
	b.WriteString(")\n")
}

func writeFnSig(b *strings.Builder, fs *vir.FunctionSignature) {
	if fs.Export {
		b.WriteString("export ")
	}
	fmt.Fprintf(b, "fnsig %s(", fs.Name)
	for i, t := range fs.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(t.String())
	}
	if fs.Variadic {
		if len(fs.Params) > 0 {
			b.WriteString(", ")
		}
		b.WriteString("...")
	}
	fmt.Fprintf(b, ") %s\n", fs.Ret.String())
}

func writeConst(b *strings.Builder, c *vir.Constant) {
	if c.Export {
		b.WriteString("export ")
	}
	fmt.Fprintf(b, "const %s %s = %s\n", c.Name, c.Type.String(), c.Value.String())
}

func writeGlobal(b *strings.Builder, g *vir.Global) {
	if g.Export {
		b.WriteString("export ")
	}
	b.WriteString("global ")
	if g.TLS {
		b.WriteString("tls ")
	}
	fmt.Fprintf(b, "%s %s", g.Name, g.Type.String())
	if g.Align != 0 {
		fmt.Fprintf(b, " align %d", g.Align)
	}
	b.WriteString(" = ")
	writeConstInit(b, g.Init)
	b.WriteString("\n")
}

func writeConstInit(b *strings.Builder, init vir.ConstInit) {
	switch x := init.(type) {
	case vir.InitLiteral:
		b.WriteString(x.Value.String())
	case vir.InitZero:
		b.WriteString("zero")
	case vir.InitAddressOf:
		b.WriteString("addr " + x.Name)
	case vir.InitAggregate:
		b.WriteString("(")
		for i, e := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			writeConstInit(b, e)
		}
		b.WriteString(")")
	case vir.InitByteString:
		b.WriteString(strconv.Quote(string(x.Data)))
	}
}

func writeExternGroup(b *strings.Builder, g *vir.ExternGroup) {
	fmt.Fprintf(b, "extern %s:\n", strconv.Quote(g.Dependency))
	for _, f := range g.Functions {
		b.WriteString("  fn " + f.Name + "(")
		writeParams(b, f.Params, f.Variadic)
		b.WriteString(") " + f.Ret.String())
		writeFnAttrs(b, f.Attrs)
		b.WriteString("\n")
	}
	b.WriteString("end\n")
}

func writeParams(b *strings.Builder, params []vir.Param, variadic bool) {
	for i, prm := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(prm.Name + " " + prm.Type.String())
		if prm.ByVal != "" {
			b.WriteString(" byval[" + prm.ByVal + "]")
		}
		if prm.SRet != "" {
			b.WriteString(" sret[" + prm.SRet + "]")
		}
	}
	if variadic {
		if len(params) > 0 {
			b.WriteString(", ")
		}
		b.WriteString("...")
	}
}

func writeFnAttrs(b *strings.Builder, attrs []vir.FunctionAttribute) {
	for _, a := range attrs {
		b.WriteString(" " + string(a))
	}
}

func writeFunction(b *strings.Builder, f *vir.Function) {
	if f.Export {
		b.WriteString("export ")
	}
	b.WriteString("fn " + f.Name + "(")
	writeParams(b, f.Params, f.Variadic)
	b.WriteString(") " + f.Ret.String())
	writeFnAttrs(b, f.Attrs)
	b.WriteString(":\n")
	for _, blk := range f.AllBlocks() {
		writeBlock(b, blk, blk == f.Entry)
	}
	b.WriteString("end\n")
}

func writeBlock(b *strings.Builder, blk *vir.Block, isEntry bool) {
	if !isEntry {
		b.WriteString(blk.Label + ":\n")
	}
	for _, inst := range blk.Lines {
		writeInstruction(b, inst)
	}
	if blk.Term != nil {
		writeTerminator(b, blk.Term)
	}
}

func opText(i *vir.Instruction) string {
	s := i.Op.String()
	if i.Suffix != nil {
		s += "." + i.Suffix.String()
	} else if i.Sig != "" {
		s += "." + i.Sig
	}
	return s
}

func writeInstruction(b *strings.Builder, inst *vir.Instruction) {
	b.WriteString("  ")
	if inst.Result != "" {
		b.WriteString(inst.Result + " = ")
	}
	b.WriteString(opText(inst))
	if len(inst.Args) > 0 {
		b.WriteString(" ")
		for i, a := range inst.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(a.String())
		}
	}
	if inst.Align != 0 {
		fmt.Fprintf(b, ", align %d", inst.Align)
	}
	b.WriteString("\n")
}

func writeTerminator(b *strings.Builder, t vir.Terminator) {
	b.WriteString("  ")
	switch x := t.(type) {
	case vir.Branch:
		fmt.Fprintf(b, "br %s\n", x.Label)
	case vir.BranchIf:
		fmt.Fprintf(b, "br_if %s, %s, %s\n", x.Cond.String(), x.Then, x.Else)
	case vir.Switch:
		fmt.Fprintf(b, "switch %s, %s", x.Value.String(), x.Default)
		for _, c := range x.Cases {
			fmt.Fprintf(b, ", %d %s", c.Value, c.Label)
		}
		b.WriteString("\n")
	case vir.Return:
		if x.Value != nil {
			fmt.Fprintf(b, "return %s\n", x.Value.String())
		} else {
			b.WriteString("return\n")
		}
	case vir.TailCall:
		if x.Sig != "" {
			b.WriteString("tailcall." + x.Sig)
			for i, a := range x.Args {
				if i == 0 {
					b.WriteString(" " + a.String())
				} else {
					b.WriteString(", " + a.String())
				}
			}
			b.WriteString("\n")
		} else {
			b.WriteString("tailcall " + x.Callee)
			for _, a := range x.Args {
				b.WriteString(", " + a.String())
			}
			b.WriteString("\n")
		}
	case vir.Trap:
		b.WriteString("trap\n")
	case vir.Unreachable:
		b.WriteString("unreachable\n")
	}
}