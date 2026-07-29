// encode.go
package text

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/gvir"
)

// Encode renders m as canonical .gvir text (§2). It assumes m has already
// passed ir/verify.Verify — like the .vir codec, this package doesn't
// validate anything, it just converts. Sections are written in the
// mandatory §2 order, which is also gvir.Module's field order.
//
// A module with no target is written without a target-decl even though §2
// requires one: silently inventing a target would be worse, and ir/verify
// rejects such a module long before it reaches here.
func Encode(m *gvir.Module) ([]byte, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "gvir %d.%d\n", m.Version.Major, m.Version.Minor)
	fmt.Fprintf(&b, "module %s\n", m.Name)

	if m.Target != nil {
		writeTarget(&b, m.Target)
	}
	if m.Profile.Declared() {
		writeFloatProfile(&b, m.Profile)
	}

	for _, s := range m.Structs {
		writeStruct(&b, s)
	}
	for _, c := range m.Constants {
		writeConst(&b, c)
	}
	for _, f := range m.Funcs {
		writeFunc(&b, f)
	}
	for _, k := range m.Kernels {
		writeKernel(&b, k)
	}

	return []byte(b.String()), nil
}

func writeTarget(b *strings.Builder, t *gvir.Target) {
	b.WriteString("target ")
	for i, backend := range t.Backends {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(string(backend.Kind))
		if len(backend.Archs) > 0 {
			b.WriteString("[" + strings.Join(backend.Archs, ", ") + "]")
		}
	}
	b.WriteString("\n")
}

func writeFloatProfile(b *strings.Builder, p gvir.FloatProfile) {
	var flags []string
	if p.Contract {
		flags = append(flags, "contract")
	}
	if p.Approx {
		flags = append(flags, "approx")
	}
	b.WriteString("float_profile " + strings.Join(flags, ", ") + "\n")
}

func writeStruct(b *strings.Builder, s *gvir.Struct) {
	fmt.Fprintf(b, "struct %s(", s.Name)
	for i, f := range s.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s %s", f.Name, f.Type)
	}
	b.WriteString(")\n")
}

func writeConst(b *strings.Builder, c *gvir.Const) {
	fmt.Fprintf(b, "const %s %s = ", c.Name, c.Type)
	writeConstInit(b, c.Init)
	b.WriteString("\n")
}

func writeConstInit(b *strings.Builder, init gvir.ConstInit) {
	switch x := init.(type) {
	case gvir.InitLiteral:
		b.WriteString(x.Value.String())
	case gvir.InitZero:
		b.WriteString("zero")
	case gvir.InitAggregate:
		b.WriteString("(")
		for i, e := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			writeConstInit(b, e)
		}
		b.WriteString(")")
	}
}

func writeParams(b *strings.Builder, params []gvir.Param) {
	for i, prm := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s %s", prm.Name, prm.Type)
	}
}

func writeFunc(b *strings.Builder, f *gvir.Func) {
	fmt.Fprintf(b, "func %s(", f.Name)
	writeParams(b, f.Params)
	b.WriteString(") ")
	if f.Ret != nil {
		b.WriteString(f.Ret.String())
	} else {
		b.WriteString("void")
	}
	if f.Readonly {
		b.WriteString(" readonly")
	}
	b.WriteString(":\n")
	writeBody(b, &f.Body)
	b.WriteString("end\n")
}

func writeKernel(b *strings.Builder, k *gvir.Kernel) {
	fmt.Fprintf(b, "kernel %s(", k.Name)
	writeParams(b, k.Params)
	b.WriteString(")")

	// Attribute order is fixed here so the text is canonical; §2 does not
	// pin one.
	if g := k.GroupSize; g != nil {
		fmt.Fprintf(b, " group_size %d,%d,%d", g.X, g.Y, g.Z)
	}
	if k.MaxGroupSize != 0 {
		fmt.Fprintf(b, " max_group_size %d", k.MaxGroupSize)
	}
	if k.SubgroupSize != 0 {
		fmt.Fprintf(b, " subgroup_size %d", k.SubgroupSize)
	}
	if d := k.DynamicGroup; d != nil {
		fmt.Fprintf(b, " dynamic_group %s", d.Name)
		if d.Align != 0 {
			fmt.Fprintf(b, " align %d", d.Align)
		}
	}
	b.WriteString(":\n")

	for _, g := range k.Groups {
		fmt.Fprintf(b, "  group %s %s", g.Name, g.Type)
		if g.Align != 0 {
			fmt.Fprintf(b, " align %d", g.Align)
		}
		b.WriteString("\n")
	}

	writeBody(b, &k.Body)
	b.WriteString("end\n")
}

func writeBody(b *strings.Builder, body *gvir.Body) {
	for _, blk := range body.AllBlocks() {
		writeBlock(b, blk)
	}
}

func writeBlock(b *strings.Builder, blk *gvir.Block) {
	if blk.Label != "" {
		b.WriteString(blk.Label + ":\n")
	}
	// A Merge on the entry block is outside the §2 grammar; it is written
	// rather than dropped, because dropping it would silently mutate the
	// module (module.go leaves that question to ir/verify).
	if mg := blk.Merge; mg != nil {
		switch mg.Kind {
		case gvir.MergeLoop:
			fmt.Fprintf(b, "  loop_merge %s, %s\n", mg.Merge, mg.Continue)
		default:
			fmt.Fprintf(b, "  merge %s\n", mg.Merge)
		}
	}
	for _, inst := range blk.Lines {
		writeInstruction(b, inst)
	}
	if blk.Term != nil {
		writeTerminator(b, blk.Term)
	}
}

// opText spells the mnemonic and its one suffix. Exactly one suffix channel
// is populated per opcode (module.go), so the order of these tests is not
// load-bearing.
func opText(i *gvir.Instruction) string {
	s := i.Op.String()
	switch {
	case i.Suffix != nil:
		s += "." + i.Suffix.String()
	case i.Dim != gvir.DimNone:
		s += "." + string(i.Dim)
	case i.Exec != gvir.ExecNone:
		s += "." + string(i.Exec)
	}
	return s
}

func writeInstruction(b *strings.Builder, inst *gvir.Instruction) {
	b.WriteString("  ")
	if inst.Result != "" {
		b.WriteString(inst.Result + " = ")
	}

	switch inst.Op {
	case gvir.OpLoc:
		// loc-line operands are space separated (§2).
		b.WriteString("loc")
		for _, a := range inst.Args {
			b.WriteString(" " + a.String())
		}
		b.WriteString("\n")
		return
	case gvir.OpBarrier:
		// barrier-inst attaches its memory scope with a comma (§10.1).
		b.WriteString(opText(inst))
		for _, a := range inst.Args {
			b.WriteString(", " + a.String())
		}
		b.WriteString("\n")
		return
	}

	b.WriteString(opText(inst))
	for i, a := range inst.Args {
		if i == 0 {
			b.WriteString(" ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(a.String())
	}
	if inst.Align != 0 {
		// An alloca-line's align clause carries no comma; an inst's does (§2).
		if inst.Op == gvir.OpAlloca {
			fmt.Fprintf(b, " align %d", inst.Align)
		} else {
			fmt.Fprintf(b, ", align %d", inst.Align)
		}
	}
	b.WriteString("\n")
}

func writeTerminator(b *strings.Builder, t gvir.Terminator) {
	b.WriteString("  ")
	switch x := t.(type) {
	case gvir.Br:
		fmt.Fprintf(b, "br %s\n", x.Label)
	case gvir.BrIf:
		fmt.Fprintf(b, "br_if %s, %s, %s\n", x.Cond, x.Then, x.Else)
	case gvir.Switch:
		fmt.Fprintf(b, "switch %s, %s", x.Value, x.Default)
		for _, c := range x.Cases {
			fmt.Fprintf(b, ", %d %s", c.Value, c.Label)
		}
		b.WriteString("\n")
	case gvir.Return:
		if x.Value != nil {
			fmt.Fprintf(b, "return %s\n", *x.Value)
		} else {
			b.WriteString("return\n")
		}
	case gvir.Unreachable:
		b.WriteString("unreachable\n")
	}
}