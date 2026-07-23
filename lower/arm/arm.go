// arm.go
package arm

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/arm/encoder"
)

// Program is a self-contained description of one lowered module.
type Program struct {
	Funcs   []Func
	Globals []Global
}

type Func struct {
	Name   string
	Code   []byte
	Align  uint32
	Export bool
	Fixups []Fixup
}

type Global struct {
	Name   string
	Data   []byte // nil for zero (BSS-style) storage
	Size   uint32 // may exceed len(Data) for zero fill
	Align  uint32
	Export bool
	TLS    bool
	Fixups []Fixup
}

// FixupKind is this backend's relocation vocabulary. It is *not*
// encoder.FixupKind, and the difference is not cosmetic: the encoder only
// ever emits instructions, so its three kinds are all word-internal
// bit-field patches. A global initialized with `addr f` needs a whole
// 32-bit word of *data* relocated, which the encoder has no way to name.
// FixupAbs32 is that missing kind; the other three are one-to-one
// translations of the encoder's, done explicitly in fromEncoderFixup so a
// new encoder kind fails loudly instead of being renumbered silently.
type FixupKind int

const (
	FixupPCRel24 FixupKind = iota // B/BL 24-bit word-offset field
	FixupMovwAbs                  // low 16 bits of S+A into a MOVW
	FixupMovtAbs                  // high 16 bits of S+A into a MOVT
	FixupAbs32                    // a whole 32-bit data word := S+A
)

func (k FixupKind) String() string {
	switch k {
	case FixupPCRel24:
		return "pcrel24"
	case FixupMovwAbs:
		return "movw_abs"
	case FixupMovtAbs:
		return "movt_abs"
	case FixupAbs32:
		return "abs32"
	}
	return "fixup?"
}

// Fixup is a hole a downstream object writer must resolve. Offset is
// relative to the start of the Func's Code or the Global's Data.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}

func fromEncoderFixup(f encoder.Fixup) (Fixup, error) {
	var k FixupKind
	switch f.Kind {
	case encoder.FixupPCRel24:
		k = FixupPCRel24
	case encoder.FixupMovwAbs:
		k = FixupMovwAbs
	case encoder.FixupMovtAbs:
		k = FixupMovtAbs
	default:
		return Fixup{}, fmt.Errorf("encoder produced unknown fixup kind %v", f.Kind)
	}
	return Fixup{Offset: f.Offset, Symbol: f.Symbol, Kind: k, Addend: f.Addend}, nil
}

// todo marks a construct that is valid IR this backend does not lower yet,
// distinguishable from a malformed-module error by the suffix.
func todo(format string, a ...any) error {
	return fmt.Errorf(format+" (TODO)", a...)
}

// Lower lowers a verified, import-rewritten module to A32 machine code.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target == nil {
		return nil, fmt.Errorf("module %q declares no target", m.Name)
	}
	switch m.Target.Arch {
	case "arm", "armeb":
	default:
		return nil, fmt.Errorf("lower/arm: target arch is %q, not arm or armeb", m.Target.Arch)
	}
	x := newIndex(m)
	p := &Program{}
	for _, g := range m.Globals {
		lg, err := x.lowerGlobal(g)
		if err != nil {
			return nil, fmt.Errorf("global %s: %w", g.Name, err)
		}
		p.Globals = append(p.Globals, lg)
	}
	for _, f := range m.Functions {
		lf, err := x.lowerFunc(f)
		if err != nil {
			return nil, fmt.Errorf("fn %s: %w", f.Name, err)
		}
		p.Funcs = append(p.Funcs, lf)
	}
	return p, nil
}

// index is the module-wide name lookup every phase shares: what a bare
// ident in operand position refers to, and what ABI symbol a reference
// resolves to.
type index struct {
	m       *vir.Module
	layout  *Layout
	be      bool // armeb: data words are big-endian (instruction words are not)
	globals map[string]*vir.Global
	consts  map[string]*vir.Constant
	sigs    map[string]*vir.FunctionSignature
	funcs   map[string]*vir.Function
	externs map[string]*vir.ExternFunction
	symbols map[string]string // IR name -> ABI symbol, for fn and global
}

func newIndex(m *vir.Module) *index {
	x := &index{
		m:       m,
		be:      m.Target.Arch == "armeb",
		globals: map[string]*vir.Global{},
		consts:  map[string]*vir.Constant{},
		sigs:    map[string]*vir.FunctionSignature{},
		funcs:   map[string]*vir.Function{},
		externs: map[string]*vir.ExternFunction{},
		symbols: map[string]string{},
	}
	structs := map[string]*vir.Struct{}
	for _, s := range m.Structs {
		structs[s.Name] = s
	}
	x.layout = newLayout(structs)
	for _, s := range m.FunctionSignatures {
		x.sigs[s.Name] = s
	}
	for _, c := range m.Constants {
		x.consts[c.Name] = c
	}
	for _, g := range m.Globals {
		x.globals[g.Name] = g
		x.symbols[g.Name] = symbolName(m, g.Name, g.Export, false)
	}
	for _, grp := range m.Externs {
		for _, f := range grp.Functions {
			// An extern names a symbol someone else defined: always bare.
			x.externs[f.Name] = f
			x.symbols[f.Name] = f.Name
		}
	}
	for _, f := range m.Functions {
		x.funcs[f.Name] = f
		bare := f.HasAttribute(vir.AttributeEntry) || f.HasAttribute(vir.AttributeExternC)
		x.symbols[f.Name] = symbolName(m, f.Name, f.Export, bare)
	}
	return x
}

func (x *index) symbol(name string) (string, bool) {
	s, ok := x.symbols[name]
	return s, ok
}

// symbolName implements §6.3. A non-exported definition still needs *a*
// name for the object writer to key on; it gets its bare IR name, which is
// safe because a non-exported symbol is module-local by construction.
//
// NOTE: §6.3's mangling rule is also implemented in ir/vir's mangle.go.
// Two implementations of one spec rule is exactly what DeriveLinkFile
// exists to avoid, and this copy should be deleted in favour of a call
// into vir the moment that helper is exported.
func symbolName(m *vir.Module, name string, export, forceBare bool) string {
	if !export || forceBare || m.Namespace == "" {
		return name
	}
	return mangle(m.Namespace, m.Name, name)
}

// mangle produces the length-prefixed Itanium-style symbol of §6.3:
//
//	namespace "acme/net", module "http", export "get" -> _M4acme3net4http3get
//	module "mathlib", export "add"                    -> _M7mathlib3add
//
// Namespace components split on '/'; an empty namespace contributes
// nothing, which is what makes the second spec example fall out of the
// same code path.
func mangle(ns, module, name string) string {
	var b strings.Builder
	b.WriteString("_M")
	if ns != "" {
		for _, part := range strings.Split(ns, "/") {
			fmt.Fprintf(&b, "%d%s", len(part), part)
		}
	}
	fmt.Fprintf(&b, "%d%s%d%s", len(module), module, len(name), name)
	return b.String()
}