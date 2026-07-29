// value.go
package msl

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// binding is one §7.3 name: a hoisted function-scope local that every
// assignment writes and every read names.
type binding struct {
	gname   string
	mname   string
	typ     gvir.Type
	mtyp    msl.Type
	pointee gvir.Type // best-effort provenance for field.ptr; nil when unknown
}

// fnLowerer holds per-function lowering state.
type fnLowerer struct {
	l    *lowerer
	fn   *msl.Function
	kern *gvir.Kernel // nil when lowering a func
	body *gvir.Body

	decls    []msl.Stmt // hoisted declarations, spliced in at the top
	names    map[string]*binding
	builtins map[gvir.Opcode]msl.Expr
	dynLen   msl.Expr // dynamic_group_size parameter, registered on demand
	local    map[string]bool
	tmp      int
}

func newFn(l *lowerer, fn *msl.Function, body *gvir.Body, k *gvir.Kernel) *fnLowerer {
	return &fnLowerer{
		l: l, fn: fn, kern: k, body: body,
		names:    map[string]*binding{},
		builtins: map[gvir.Opcode]msl.Expr{},
		local:    map[string]bool{},
	}
}

// define binds name on first assignment and returns the same binding
// thereafter. The first assignment permanently fixes the type (§7.3 rule 2).
func (f *fnLowerer) define(name string, t gvir.Type) (*binding, error) {
	if name == "" {
		return nil, fmt.Errorf("instruction result has no name")
	}
	if b, ok := f.names[name]; ok {
		if !gvir.Equal(b.typ, t) {
			return nil, fmt.Errorf("%s is rebound from %s to %s — the first assignment fixes the type, and address space is part of it (§7.3)", name, b.typ, t)
		}
		return b, nil
	}
	if !gvir.IsValueType(t) {
		return nil, fmt.Errorf("%s is not a value type and cannot be held in a named value (§4.7)", t)
	}
	mt, err := f.l.typeOf(t)
	if err != nil {
		return nil, err
	}
	b := &binding{gname: name, mname: f.valueName(name), typ: t, mtyp: mt}
	f.names[name] = b
	f.decls = append(f.decls, &msl.VarDecl{Type: mt, Name: b.mname})
	return b, nil
}

// defineParam binds a name to an MSL parameter, which is already an assignable
// local in C++ and therefore needs no hoisted declaration.
func (f *fnLowerer) defineParam(name string, t gvir.Type) (*binding, error) {
	mt, err := f.l.typeOf(t)
	if err != nil {
		return nil, err
	}
	b := &binding{gname: name, mname: f.valueName(name), typ: t, mtyp: mt}
	f.names[name] = b
	return b, nil
}

func (b *binding) ref() msl.Expr { return msl.Name(b.mname) }

// operand materializes one §2 operand.
func (f *fnLowerer) operand(o gvir.Operand) (msl.Expr, error) {
	switch o.Kind {
	case gvir.OperandIdent:
		if b, ok := f.names[o.Ident]; ok {
			return b.ref(), nil
		}
		if c := f.l.src.ConstByName(o.Ident); c != nil {
			return msl.Name(c.Name), nil
		}
		return msl.Expr{}, fmt.Errorf("%s is read on a path that does not assign it, or is not declared (§7.3)", o.Ident)
	case gvir.OperandInt:
		if o.Int < 0 {
			return msl.I(o.Int), nil
		}
		return msl.U(uint64(o.Int)), nil
	case gvir.OperandFloat:
		return floatLiteral(o, 32), nil
	case gvir.OperandBool:
		return msl.B(o.Bool), nil
	case gvir.OperandNull:
		return msl.Name("nullptr"), nil
	}
	return msl.Expr{}, fmt.Errorf("operand %s is not a value", o)
}

// operandType reports an operand's .gvir type when it has one. Literals do not:
// their type comes from the instruction's suffix.
func (f *fnLowerer) operandType(o gvir.Operand) (gvir.Type, bool) {
	if o.Kind != gvir.OperandIdent {
		return nil, false
	}
	if b, ok := f.names[o.Ident]; ok {
		return b.typ, true
	}
	if c := f.l.src.ConstByName(o.Ident); c != nil {
		return c.Type, true
	}
	return nil, false
}

// pointerSpace reports the address space of a pointer-valued operand.
func (f *fnLowerer) pointerSpace(o gvir.Operand) (msl.AddressSpace, error) {
	t, ok := f.operandType(o)
	if !ok {
		return "", fmt.Errorf("%s is not a bound pointer value; its address space is unknown (§5)", o)
	}
	p, ok := t.(gvir.PtrType)
	if !ok {
		return "", fmt.Errorf("%s is %s, not a pointer", o, t)
	}
	return mslSpace(p.Space)
}

func (f *fnLowerer) arg(in *gvir.Instruction, i int) (msl.Expr, error) {
	if i >= len(in.Args) {
		return msl.Expr{}, fmt.Errorf("%s takes at least %d operands, got %d", in.Op, i+1, len(in.Args))
	}
	return f.operand(in.Args[i])
}

// assign binds the instruction's result to the mechanically derived result type
// and writes the expression into it.
func (f *fnLowerer) assign(out *msl.Block, in *gvir.Instruction, e msl.Expr) error {
	t, ok := in.Op.ResultType(in.Suffix, in.Dim)
	if !ok {
		return fmt.Errorf("%s has no mechanically derived result type", in.Op)
	}
	return f.assignAs(out, in.Result, t, e)
}

func (f *fnLowerer) assignAs(out *msl.Block, name string, t gvir.Type, e msl.Expr) error {
	b, err := f.define(name, t)
	if err != nil {
		return err
	}
	out.Assign(b.ref(), e)
	return nil
}

// constOf spells a constant in t's MSL type: T(x), which splats for vectors.
func (f *fnLowerer) constOf(t gvir.Type, e msl.Expr) (msl.Expr, error) {
	mt, err := f.l.typeOf(t)
	if err != nil {
		return msl.Expr{}, err
	}
	return msl.Cast(mt, e), nil
}

func (f *fnLowerer) zero(t gvir.Type) (msl.Expr, error) { return f.constOf(t, msl.U(0)) }
func (f *fnLowerer) one(t gvir.Type) (msl.Expr, error)  { return f.constOf(t, msl.U(1)) }

// toSigned reads the same bits as a signed value, for the §11 opcodes that ask
// for the signed interpretation.
func (f *fnLowerer) toSigned(t gvir.Type, e msl.Expr) (msl.Expr, error) {
	st, err := f.l.signedTypeOf(t)
	if err != nil {
		return msl.Expr{}, err
	}
	return msl.Cast(st, e), nil
}

// floatLiteral spells a float operand. hex-float is exact by construction and
// is preserved verbatim (§2, §13 "Literals").
func floatLiteral(o gvir.Operand, bits int) msl.Expr {
	switch {
	case o.Hex:
		return msl.Raw(o.String())
	}
	switch s := o.String(); s {
	case "NaN":
		return msl.Raw("NAN")
	case "Inf":
		return msl.Raw("INFINITY")
	case "-Inf":
		return msl.Raw("-INFINITY")
	default:
		if bits <= 32 {
			return msl.Raw(s + "f") // Metal has no double; say so explicitly
		}
		return msl.Raw(s)
	}
}

func (f *fnLowerer) tempName(base string) string {
	f.tmp++
	return fmt.Sprintf("%s%s%d", prefix, base, f.tmp)
}

// storageName is the backing object behind an alloca, a group declaration, or a
// by-value struct parameter — distinct from the pointer value that names it.
func (f *fnLowerer) storageName(gname string) string {
	return prefix + "s_" + gname
}

// valueName mangles a .gvir identifier into one that is safe in MSL. §2 gives
// .gvir a flat namespace with no shadowing, so there is nothing to
// disambiguate here — only keywords and the synthesized prefix to dodge.
func (f *fnLowerer) valueName(n string) string {
	if reserved[n] || strings.HasPrefix(n, prefix) {
		return n + "_"
	}
	return n
}

var reserved = map[string]bool{
	// C++ and MSL keywords.
	"alignas": true, "auto": true, "bool": true, "break": true, "case": true,
	"char": true, "class": true, "const": true, "constexpr": true, "continue": true,
	"default": true, "do": true, "double": true, "else": true, "enum": true,
	"explicit": true, "extern": true, "false": true, "float": true, "for": true,
	"goto": true, "if": true, "inline": true, "int": true, "long": true,
	"namespace": true, "new": true, "nullptr": true, "operator": true,
	"private": true, "public": true, "return": true, "short": true, "signed": true,
	"sizeof": true, "static": true, "struct": true, "switch": true, "template": true,
	"this": true, "true": true, "typedef": true, "typename": true, "union": true,
	"unsigned": true, "using": true, "void": true, "volatile": true, "while": true,
	// MSL address spaces, qualifiers and common stdlib spellings.
	"kernel": true, "vertex": true, "fragment": true, "device": true,
	"constant": true, "threadgroup": true, "thread": true, "ray_data": true,
	"object_data": true, "half": true, "bfloat": true, "uchar": true,
	"ushort": true, "uint": true, "ulong": true, "metal": true, "access": true,
	"sampler": true, "texture2d": true, "imageblock": true, "simd_vote": true,
	"min": true, "max": true, "abs": true, "clamp": true, "select": true,
	"fma": true, "sqrt": true, "rotate": true, "popcount": true, "clz": true,
	"ctz": true, "as_type": true, "main": true,
}