// File: format/vbyte/binary/encode_context.go
package binary

import "github.com/vertex-language/vvm/ir/vir"

// encodeContext holds the string table and the index-space lookups
// (structs/fnsigs/globals/functions/extern_fns) needed to translate
// ir/vir's name-based references into the binary format's index-based
// ones (§4).
type encodeContext struct {
	module        *vir.Module
	strings       *stringTable
	structIndex   map[string]int
	fnsigIndex    map[string]int
	globalIndex   map[string]int
	fnIndex       map[string]int
	externFnIndex map[string]int
}

func newEncodeContext(m *vir.Module) (*encodeContext, error) {
	ec := &encodeContext{
		module:        m,
		strings:       newStringTable(),
		structIndex:   map[string]int{},
		fnsigIndex:    map[string]int{},
		globalIndex:   map[string]int{},
		fnIndex:       map[string]int{},
		externFnIndex: map[string]int{},
	}

	for i, s := range m.Structs {
		ec.structIndex[s.Name] = i
	}
	for i, fs := range m.FunctionSignatures {
		ec.fnsigIndex[fs.Name] = i
	}
	for i, g := range m.Globals {
		ec.globalIndex[g.Name] = i
	}
	for i, fn := range m.Functions {
		ec.fnIndex[fn.Name] = i
	}
	idx := 0
	for _, eg := range m.Externs {
		for _, f := range eg.Functions {
			ec.externFnIndex[f.Name] = idx
			idx++
		}
	}

	collectStrings(m, ec.strings)
	return ec, nil
}

// collectStrings walks the entire module, interning every string that will
// need a StringTable idx, so the table can be fully written before any
// section that references it.
func collectStrings(m *vir.Module, st *stringTable) {
	st.intern(m.Name)
	if m.Namespace != "" {
		st.intern(m.Namespace)
	}
	if m.Target != nil {
		st.intern(m.Target.Arch)
		st.intern(m.Target.OS)
		if m.Target.ABI != "" {
			st.intern(m.Target.ABI)
		}
		for _, t := range m.Target.Tiers {
			st.intern(t)
		}
	}
	for _, s := range m.Structs {
		st.intern(s.Name)
		for _, f := range s.Fields {
			st.intern(f.Name)
			collectTypeStrings(f.Type, st)
		}
	}
	for _, fs := range m.FunctionSignatures {
		st.intern(fs.Name)
		for _, p := range fs.Params {
			collectTypeStrings(p, st)
		}
		collectTypeStrings(fs.Ret, st)
	}
	for _, c := range m.Constants {
		st.intern(c.Name)
		collectTypeStrings(c.Type, st)
		collectOperandStrings(c.Value, st)
	}
	for _, g := range m.Globals {
		st.intern(g.Name)
		collectTypeStrings(g.Type, st)
		collectConstInitStrings(g.Init, st)
	}
	for _, l := range m.Links {
		st.intern(l.Name)
	}
	for _, eg := range m.Externs {
		st.intern(eg.Dependency)
		for _, f := range eg.Functions {
			st.intern(f.Name)
			for _, p := range f.Params {
				st.intern(p.Name)
				collectTypeStrings(p.Type, st)
			}
			collectTypeStrings(f.Ret, st)
		}
	}
	for _, im := range m.Imports {
		st.intern(im.Path)
	}
	for _, fn := range m.Functions {
		st.intern(fn.Name)
		for _, p := range fn.Params {
			st.intern(p.Name)
			collectTypeStrings(p.Type, st)
		}
		collectTypeStrings(fn.Ret, st)
		if len(fn.Blocks) > 0 || fn.Entry != nil {
			st.intern("") // label_names[0] entry placeholder (§4)
		}
		for _, b := range fn.AllBlocks() {
			for _, inst := range b.Lines {
				if inst.Op == vir.OpVaStart {
					st.intern(inst.Sig)
				}
				if inst.Suffix != nil {
					collectTypeStrings(inst.Suffix, st)
				}
				for _, a := range inst.Args {
					collectOperandStrings(a, st)
				}
			}
		}
	}
}

func collectTypeStrings(t vir.Type, st *stringTable) {
	switch v := t.(type) {
	case vir.StructType:
		if v.Import != "" {
			st.intern(v.Import)
			st.intern(v.Name)
		}
	case vir.VecType:
		collectTypeStrings(v.Elem, st)
	case vir.ArrayType:
		collectTypeStrings(v.Elem, st)
	}
}

func collectOperandStrings(op vir.Operand, st *stringTable) {
	switch op.Kind {
	case vir.OperandString:
		st.intern(op.Str)
	case vir.OperandType:
		collectTypeStrings(op.Type, st)
	case vir.OperandIdent:
		if op.Qualifier != "" {
			st.intern(op.Qualifier)
			st.intern(op.Ident)
		}
	}
}

func collectConstInitStrings(ci vir.ConstInit, st *stringTable) {
	switch v := ci.(type) {
	case vir.InitLiteral:
		collectOperandStrings(v.Value, st)
	case vir.InitAggregate:
		for _, e := range v.Elems {
			collectConstInitStrings(e, st)
		}
	}
}