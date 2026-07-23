// aarch64.go
package aarch64

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// The two arches this backend serves. BE-8 keeps the instruction stream
// little-endian, so endianness reaches only globals.go's data words —
// exactly the arm/armeb split.
const (
	ArchName   = "aarch64"
	ArchNameBE = "aarch64_be"
)

// Program is a self-contained description of the lowered module.
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

// FixupKind is this package's relocation vocabulary. It is a superset of
// isa/aarch64/encoder's: every kind the encoder can name is mirrored
// one-for-one, plus FixupAbs64, which it cannot.
//
// The encoder's kinds are all *instruction-word bit-field* patches, because
// the encoder only ever emits instructions. A `global g ptr = addr f` needs
// a whole 64-bit **data** word relocated — a hole with no instruction around
// it — so this package defines its own vocabulary and translates the
// encoder's in an explicit switch, as lower/arm and lower/x86 do and
// lower/x86_64 does not.
type FixupKind int

const (
	FixupCall26 FixupKind = iota
	FixupJump26
	FixupCondBr19
	FixupTestBr14
	FixupAdrPrelPgHi21
	FixupAdrPrelLo21
	FixupAddAbsLo12Nc
	FixupLdSt8AbsLo12Nc
	FixupLdSt16AbsLo12Nc
	FixupLdSt32AbsLo12Nc
	FixupLdSt64AbsLo12Nc

	// FixupAbs64 patches a whole 64-bit data word with S + A. No encoder
	// counterpart: it never appears inside an instruction.
	// (R_AARCH64_ABS64)
	FixupAbs64
)

func (k FixupKind) String() string {
	switch k {
	case FixupCall26:
		return "call26"
	case FixupJump26:
		return "jump26"
	case FixupCondBr19:
		return "condbr19"
	case FixupTestBr14:
		return "tstbr14"
	case FixupAdrPrelPgHi21:
		return "adr_prel_pg_hi21"
	case FixupAdrPrelLo21:
		return "adr_prel_lo21"
	case FixupAddAbsLo12Nc:
		return "add_abs_lo12_nc"
	case FixupLdSt8AbsLo12Nc:
		return "ldst8_abs_lo12_nc"
	case FixupLdSt16AbsLo12Nc:
		return "ldst16_abs_lo12_nc"
	case FixupLdSt32AbsLo12Nc:
		return "ldst32_abs_lo12_nc"
	case FixupLdSt64AbsLo12Nc:
		return "ldst64_abs_lo12_nc"
	case FixupAbs64:
		return "abs64"
	}
	return "fixup?"
}

// Fixup is a hole a downstream object writer must resolve.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}

// fromEncoderKind translates the encoder's vocabulary into this one. It is an
// explicit switch rather than a numeric cast even though the shared kinds are
// declared in the same order: FixupAbs64 is the one variant with no encoder
// equivalent, and a switch fails loudly if either enum gains a case instead
// of silently reinterpreting it.
func fromEncoderKind(k encoder.FixupKind) (FixupKind, error) {
	switch k {
	case encoder.FixupCall26:
		return FixupCall26, nil
	case encoder.FixupJump26:
		return FixupJump26, nil
	case encoder.FixupCondBr19:
		return FixupCondBr19, nil
	case encoder.FixupTestBr14:
		return FixupTestBr14, nil
	case encoder.FixupAdrPrelPgHi21:
		return FixupAdrPrelPgHi21, nil
	case encoder.FixupAdrPrelLo21:
		return FixupAdrPrelLo21, nil
	case encoder.FixupAddAbsLo12Nc:
		return FixupAddAbsLo12Nc, nil
	case encoder.FixupLdSt8AbsLo12Nc:
		return FixupLdSt8AbsLo12Nc, nil
	case encoder.FixupLdSt16AbsLo12Nc:
		return FixupLdSt16AbsLo12Nc, nil
	case encoder.FixupLdSt32AbsLo12Nc:
		return FixupLdSt32AbsLo12Nc, nil
	case encoder.FixupLdSt64AbsLo12Nc:
		return FixupLdSt64AbsLo12Nc, nil
	}
	return 0, fmt.Errorf("encoder fixup kind %d has no counterpart here", int(k))
}

// todo wraps an error meaning "the module is valid, this backend doesn't
// lower that construct yet". A plain fmt.Errorf means the input violated an
// invariant this package is entitled to assume vir.Verify already checked.
func todo(format string, a ...any) error {
	return fmt.Errorf(format+" (TODO)", a...)
}

// ---------------------------------------------------------------------------
// Module-wide indexing.
// ---------------------------------------------------------------------------

// callable is everything a call site needs about a target: its ABI symbol and
// its declared shape.
type callable struct {
	sym      string
	params   []vir.Param
	variadic bool
	ret      vir.Type
}

type index struct {
	m      *vir.Module
	layout *Layout

	funcs   map[string]*callable
	sigs    map[string]*vir.FunctionSignature
	globals map[string]*vir.Global
	consts  map[string]*vir.Constant
	symOf   map[string]string // global/function IR name -> ABI symbol

	// stackVarargs selects the variadic convention. See callconv.go.
	stackVarargs bool
	bigEndian    bool
	os           string
}

func newIndex(m *vir.Module) (*index, error) {
	ix := &index{
		m:       m,
		layout:  newLayout(m),
		funcs:   map[string]*callable{},
		sigs:    map[string]*vir.FunctionSignature{},
		globals: map[string]*vir.Global{},
		consts:  map[string]*vir.Constant{},
		symOf:   map[string]string{},
		os:      m.Target.OS,
	}
	ix.bigEndian = m.Target.Arch == ArchNameBE
	ix.stackVarargs = m.Target.ABI == "aapcs64" || vir.FormatOf(m.Target.OS) == vir.FormatMachO

	for _, s := range m.FunctionSignatures {
		ix.sigs[s.Name] = s
	}
	for _, c := range m.Constants {
		ix.consts[c.Name] = c
	}
	for _, g := range m.Globals {
		ix.globals[g.Name] = g
		ix.symOf[g.Name] = ix.symbolFor(g.Name, g.Export, nil)
	}
	for _, grp := range m.Externs {
		for _, f := range grp.Functions {
			// An extern's symbol is its declared name, always bare: it names
			// something a foreign toolchain produced.
			ix.funcs[f.Name] = &callable{sym: f.Name, params: f.Params, variadic: f.Variadic, ret: f.Ret}
			ix.symOf[f.Name] = f.Name
		}
	}
	for _, f := range m.Functions {
		sym := ix.symbolFor(f.Name, f.Export, f.Attrs)
		ix.funcs[f.Name] = &callable{sym: sym, params: f.Params, variadic: f.Variadic, ret: f.Ret}
		ix.symOf[f.Name] = sym
	}
	return ix, nil
}

// symbolFor computes the ABI-visible symbol for an export (§6.3). An
// unnamespaced module, an `entry` function, and an `extern_c` function all
// emit a bare name; anything else in a namespaced module mangles
// length-prefixed Itanium-style.
func (ix *index) symbolFor(name string, export bool, attrs []vir.FunctionAttribute) string {
	if !export || ix.m.Namespace == "" {
		return name
	}
	for _, a := range attrs {
		if a == vir.AttributeEntry || a == vir.AttributeExternC {
			return name
		}
	}
	return mangle(ix.m.Namespace, ix.m.Name, name)
}

func mangle(ns, mod, name string) string {
	var b strings.Builder
	b.WriteString("_M")
	for _, part := range strings.Split(ns, "/") {
		if part == "" {
			continue
		}
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteString(part)
	}
	b.WriteString(strconv.Itoa(len(mod)))
	b.WriteString(mod)
	b.WriteString(strconv.Itoa(len(name)))
	b.WriteString(name)
	return b.String()
}

// ---------------------------------------------------------------------------
// Entry point.
// ---------------------------------------------------------------------------

// Lower lowers a verified module into A64 machine code. The module must have
// passed vir.Verify and, for multi-file modules, importer.Rewrite: a
// qualified operand or an unresolved import reaching here is a bug upstream,
// not something this package repairs.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target == nil {
		return nil, fmt.Errorf("module %q declares no target", m.Name)
	}
	switch m.Target.Arch {
	case ArchName, ArchNameBE:
	default:
		return nil, fmt.Errorf("module %q targets %q, not %s/%s", m.Name, m.Target.Arch, ArchName, ArchNameBE)
	}

	ix, err := newIndex(m)
	if err != nil {
		return nil, err
	}

	p := &Program{}
	for _, g := range m.Globals {
		lg, err := lowerGlobal(ix, g)
		if err != nil {
			return nil, fmt.Errorf("global %s: %w", g.Name, err)
		}
		p.Globals = append(p.Globals, *lg)
	}
	for _, f := range m.Functions {
		lf, err := lowerFunc(ix, f)
		if err != nil {
			return nil, fmt.Errorf("fn %s: %w", f.Name, err)
		}
		p.Funcs = append(p.Funcs, *lf)
	}
	return p, nil
}