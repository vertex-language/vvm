package text

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
)

// Encode prints a *vir.Module as .vir source in canonical section order.
// It assumes a verified module; it never re-checks (README invariant 3).
func Encode(m *vir.Module) ([]byte, error) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

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

	for _, s := range m.Structs {
		parts := make([]string, len(s.Fields))
		for i, f := range s.Fields {
			parts[i] = f.Name + " " + f.Type.String()
		}
		w("struct %s(%s)\n", s.Name, strings.Join(parts, ", "))
	}
	for _, s := range m.FnSigs {
		parts := make([]string, 0, len(s.Params)+1)
		for _, p := range s.Params {
			parts = append(parts, p.String())
		}
		if s.Variadic {
			parts = append(parts, "...")
		}
		w("fnsig %s(%s) %s\n", s.Name, strings.Join(parts, ", "), s.Ret)
	}
	for _, c := range m.Consts {
		w("const %s %s = %s\n", c.Name, c.Type, c.Value)
	}
	for _, g := range m.Globals {
		if g.Export {
			w("export ")
		}
		w("global ")
		if g.TLS {
			w("tls ")
		}
		w("%s %s", g.Name, g.Type)
		if g.Align != 0 {
			w(" align %d", g.Align)
		}
		w(" = %s\n", encodeInit(g.Init))
	}
	for _, l := range m.Links {
		w("link %s %q\n", l.Kind, l.Name)
	}
	for _, g := range m.Externs {
		if g.Dep == "" {
			w("extern :\n")
		} else {
			w("extern %q :\n", g.Dep)
		}
		for _, f := range g.Fns {
			w("    fn %s(%s) %s%s\n", f.Name, encodeParams(f.Params, f.Variadic), f.Ret, encodeAttrs(f.Attrs))
		}
		w("end\n")
	}
	for _, f := range m.Funcs {
		if f.Export {
			w("export ")
		}
		w("fn %s(%s) %s%s:\n", f.Name, encodeParams(f.Params, false), f.Ret, encodeAttrs(f.Attrs))
		for _, blk := range f.AllBlocks() {
			if blk.Label != "" {
				w("%s:\n", blk.Label)
			}
			for _, i := range blk.Insts {
				w("    %s\n", encodeInst(i))
			}
			w("    %s\n", encodeTerm(blk.Term))
		}
		w("end\n")
	}
	return []byte(b.String()), nil
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

func encodeAttrs(attrs []vir.FnAttr) string {
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

func encodeInit(i vir.ConstInit) string {
	switch x := i.(type) {
	case vir.InitZero:
		return "zero"
	case vir.InitLit:
		return x.Value.String()
	case vir.InitAddr:
		return "addr " + x.Name
	case vir.InitBytes:
		return quoteBytes(x.Data)
	case vir.InitAgg:
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

func encodeInst(i vir.Inst) string {
	var b strings.Builder
	if i.Result != "" {
		b.WriteString(i.Result + " = ")
	}
	b.WriteString(i.Op)
	if i.Suffix != nil {
		b.WriteString("." + i.Suffix.String())
	} else if i.Sig != "" {
		b.WriteString("." + i.Sig)
	}
	for j, a := range i.Args {
		if j == 0 {
			b.WriteString(" ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(a.String())
	}
	if i.Align != 0 {
		fmt.Fprintf(&b, ", align %d", i.Align)
	}
	return b.String()
}

func encodeTerm(t vir.Terminator) string {
	switch x := t.(type) {
	case vir.Br:
		return "br " + x.Label
	case vir.BrIf:
		return fmt.Sprintf("br_if %s, %s, %s", x.Cond, x.Then, x.Else)
	case vir.Switch:
		s := fmt.Sprintf("switch %s, %s", x.Value, x.Default)
		for _, c := range x.Cases {
			s += fmt.Sprintf(", %d %s", c.Value, c.Label)
		}
		return s
	case vir.Return:
		if x.Value == nil {
			return "return"
		}
		return "return " + x.Value.String()
	case vir.TailCall:
		if x.Callee != "" {
			s := "tailcall " + x.Callee
			for _, a := range x.Args {
				s += ", " + a.String()
			}
			return s
		}
		s := "tailcall." + x.Sig
		for j, a := range x.Args {
			if j == 0 {
				s += " " + a.String()
			} else {
				s += ", " + a.String()
			}
		}
		return s
	case vir.Trap:
		return "trap"
	case vir.Unreachable:
		return "unreachable"
	}
	return "<bad terminator>"
}