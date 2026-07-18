package vir

import (
	"fmt"
	"strings"
)

// Verify enforces the §9 obligations in a single forward pass over the
// module, then per-function passes for body shape, type fixation, and the
// Join Convention's definite-assignment analysis (§5).
//
// Coverage notes: obligations that need target-tier data (§9.32 vector
// legality, wide atomics) or deep per-opcode operand typing are checked
// structurally here and marked TODO where the tier tables aren't wired yet.
func Verify(m *Module) error {
	v := &verifier{m: m, names: map[string]string{}}
	return v.run()
}

type verifier struct {
	m     *Module
	names map[string]string // name -> kind ("struct", "fnsig", "const", "global", "extern", "fn", "label")
}

var keywords = map[string]bool{
	"module": true, "target": true, "struct": true, "fnsig": true, "const": true,
	"global": true, "export": true, "tls": true, "extern": true, "link": true,
	"shared": true, "static": true, "framework": true, "fn": true, "end": true,
	"zero": true, "addr": true, "loc": true, "align": true,
	"noreturn": true, "readonly": true, "inline": true, "noinline": true, "cold": true,
	"br": true, "br_if": true, "switch": true, "return": true, "tailcall": true,
	"trap": true, "unreachable": true,
	"relaxed": true, "acquire": true, "release": true, "acqrel": true, "seqcst": true,
	"true": true, "false": true, "null": true,
}

func (v *verifier) declare(name, kind string) error {
	if name == "" {
		return fmt.Errorf("%s with empty name", kind)
	}
	if keywords[name] {
		return fmt.Errorf("%q is a reserved keyword and may not be used as an identifier (§3)", name)
	}
	if prev, ok := v.names[name]; ok {
		return fmt.Errorf("name %q redeclared as %s; already declared as %s (flat namespace, §1.2)", name, kind, prev)
	}
	v.names[name] = kind
	return nil
}

func (v *verifier) run() error {
	m := v.m
	if m.Name == "" {
		return fmt.Errorf("module has no name (§1.2 rule 1)")
	}

	// §9.7 — target declaration.
	if m.Target != nil {
		t := m.Target
		if !CanonicalArch[t.Arch] {
			if canon, alias := ArchAliases[t.Arch]; alias {
				return fmt.Errorf("target arch %q is a rejected alias; use %q (§10.5)", t.Arch, canon)
			}
			return fmt.Errorf("target arch %q is not a canonical architecture (§10.1)", t.Arch)
		}
		if !CanonicalOS[t.OS] {
			if canon, alias := OSAliases[t.OS]; alias {
				return fmt.Errorf("target os %q is a rejected alias; use %q (§10.5)", t.OS, canon)
			}
			return fmt.Errorf("target os %q is not a canonical OS (§10.2)", t.OS)
		}
		if t.ABI != "" && !CanonicalABI[t.ABI] {
			return fmt.Errorf("target abi %q is not a canonical ABI (§10.3)", t.ABI)
		}
		// TODO(§10.4): validate tier-list entries against per-target tier tables.
	}
	if len(m.Links) > 0 && m.Target == nil {
		return fmt.Errorf("module has a link section but no target declaration (§1.2 rule 10)")
	}

	// Structs.
	for _, s := range m.Structs {
		if err := v.declare(s.Name, "struct"); err != nil {
			return err
		}
		if len(s.Fields) == 0 {
			return fmt.Errorf("struct %s has no fields", s.Name)
		}
		seen := map[string]bool{}
		for _, f := range s.Fields {
			if seen[f.Name] {
				return fmt.Errorf("struct %s: duplicate field %q", s.Name, f.Name)
			}
			seen[f.Name] = true
			if err := v.checkFieldType(s.Name, f); err != nil {
				return err
			}
		}
	}

	// FnSigs.
	for _, sig := range m.FnSigs {
		if err := v.declare(sig.Name, "fnsig"); err != nil {
			return err
		}
		for _, p := range sig.Params {
			if !IsValueType(p) {
				return fmt.Errorf("fnsig %s: parameter type %s is not a value type", sig.Name, p)
			}
		}
		if sig.Ret == nil {
			return fmt.Errorf("fnsig %s: missing return type", sig.Name)
		}
	}

	// Consts (§9.5): scalars only, one literal.
	for _, c := range m.Consts {
		if err := v.declare(c.Name, "const"); err != nil {
			return err
		}
		if IsAggregate(c.Type) || IsVoid(c.Type) {
			return fmt.Errorf("const %s: type %s is not a scalar (§8)", c.Name, c.Type)
		}
		if err := v.checkLiteral(c.Value, c.Type, "const "+c.Name); err != nil {
			return err
		}
	}

	// Globals (§9.4–§9.5).
	for _, g := range m.Globals {
		if err := v.declare(g.Name, "global"); err != nil {
			return err
		}
		if g.Align != 0 && !isPow2(g.Align) {
			return fmt.Errorf("global %s: align %d is not a power of two", g.Name, g.Align)
		}
		if g.TLS && m.Target != nil && m.Target.OS == "none" {
			// TODO(§1.2 rule 7): allow when a tier supplies a TLS convention.
			return fmt.Errorf("global %s: tls on os=none requires a TLS-capable feature tier", g.Name)
		}
		if err := v.checkInit(g.Init, g.Type, "global "+g.Name); err != nil {
			return err
		}
	}

	// Links (§9.8).
	format := FormatELF
	if m.Target != nil {
		format = FormatOf(m.Target.OS)
	}
	derived := map[string]string{} // derived filename -> original string
	for _, l := range m.Links {
		switch l.Kind {
		case LinkStatic, LinkShared, LinkFramework:
		default:
			return fmt.Errorf("link %q: unknown kind %q (aliases like dylib/dll/so are rejected, §10.5)", l.Name, l.Kind)
		}
		if l.Kind == LinkFramework {
			if format != FormatMachO {
				return fmt.Errorf("link framework %q: frameworks are Mach-O only (§7.4)", l.Name)
			}
			if isExactName(l.Name) {
				return fmt.Errorf("link framework %q: framework strings must be short names (§7.4)", l.Name)
			}
		}
		file, err := deriveLinkFile(l, format)
		if err != nil {
			return err
		}
		if prev, dup := derived[file]; dup {
			return fmt.Errorf("duplicate link dependency: %q and %q both derive %q (§7.4)", prev, l.Name, file)
		}
		derived[file] = l.Name
	}

	// Extern groups (§9.9).
	linkStrings := map[string]bool{}
	for _, l := range m.Links {
		linkStrings[l.Name] = true
	}
	claimed := map[string]bool{}
	for _, g := range m.Externs {
		if g.Dep == "" {
			if m.Target != nil && (m.Target.OS == "none" || m.Target.OS == "uefi") {
				return fmt.Errorf("anonymous extern group rejected on os=%s (§1.2 rule 9)", m.Target.OS)
			}
		} else {
			if !linkStrings[g.Dep] {
				return fmt.Errorf("extern group %q: no matching link declaration (§1.2 rule 9)", g.Dep)
			}
			if claimed[g.Dep] {
				return fmt.Errorf("extern group %q: link string already claimed by another group (§1.2 rule 9)", g.Dep)
			}
			claimed[g.Dep] = true
		}
		if len(g.Fns) == 0 {
			return fmt.Errorf("empty extern group %q rejected (§1.2 rule 9)", g.Dep)
		}
		for _, f := range g.Fns {
			if err := v.declare(f.Name, "extern"); err != nil {
				return err
			}
			if err := v.checkParams(f.Name, f.Params, f.Ret, true); err != nil {
				return err
			}
		}
	}

	// Functions.
	for _, f := range m.Funcs {
		if err := v.declare(f.Name, "fn"); err != nil {
			return err
		}
		if err := v.checkParams(f.Name, f.Params, f.Ret, false); err != nil {
			return err
		}
		if f.Variadic() {
			return fmt.Errorf("fn %s: variadics are rejected in fn definitions (§1.2 rule 5)", f.Name)
		}
	}
	// Labels join the flat namespace (§1.3 rule 4) — declare before bodies.
	for _, f := range m.Funcs {
		for _, b := range f.Blocks {
			if err := v.declare(b.Label, "label"); err != nil {
				return err
			}
		}
	}
	for _, f := range m.Funcs {
		if err := v.verifyFunc(f); err != nil {
			return fmt.Errorf("fn %s: %w", f.Name, err)
		}
	}
	return nil
}

func (f *Func) Variadic() bool { return false } // grammar can't express it; kept for symmetry

func (v *verifier) checkParams(fn string, params []Param, ret Type, isExtern bool) error {
	for i, p := range params {
		if p.ByVal != "" || p.SRet != "" {
			if !IsPtr(p.Type) {
				return fmt.Errorf("%s: byval/sret only on ptr params (§9.28)", fn)
			}
		}
		if p.SRet != "" {
			if i != 0 {
				return fmt.Errorf("%s: sret must be the first parameter (§9.28)", fn)
			}
			if !IsVoid(ret) {
				return fmt.Errorf("%s: sret requires void return (§9.28)", fn)
			}
		}
		for _, n := range []string{p.ByVal, p.SRet} {
			if n != "" && v.names[n] != "struct" {
				return fmt.Errorf("%s: byval/sret names undeclared struct %q (§9.28)", fn, n)
			}
		}
		if !IsValueType(p.Type) {
			return fmt.Errorf("%s: parameter %q has non-value type %s", fn, p.Name, p.Type)
		}
	}
	return nil
}

func (v *verifier) checkFieldType(structName string, f Field) error {
	return v.checkSizedType(f.Type, fmt.Sprintf("struct %s field %s", structName, f.Name))
}

func (v *verifier) checkSizedType(t Type, ctx string) error {
	switch x := t.(type) {
	case VoidType:
		return fmt.Errorf("%s: void is not a sized type", ctx)
	case StructType:
		if v.names[x.Name] != "struct" {
			return fmt.Errorf("%s: struct %q not declared on an earlier line (§1.2 rule 2)", ctx, x.Name)
		}
	case ArrayType:
		if x.Len <= 0 {
			return fmt.Errorf("%s: array length must be positive", ctx)
		}
		return v.checkSizedType(x.Elem, ctx)
	case VecType:
		if x.Len <= 0 {
			return fmt.Errorf("%s: vector length must be positive", ctx)
		}
	}
	return nil
}

func (v *verifier) checkLiteral(o Operand, t Type, ctx string) error {
	switch o.Kind {
	case OInt:
		if !IsInt(t) {
			return fmt.Errorf("%s: integer literal for non-integer type %s", ctx, t)
		}
	case OFloat:
		if !IsFloat(t) {
			return fmt.Errorf("%s: float literal for non-float type %s", ctx, t)
		}
	case OBool:
		if !Equal(t, I1) {
			return fmt.Errorf("%s: bool literal requires i1", ctx)
		}
	case ONull:
		if !IsPtr(t) {
			return fmt.Errorf("%s: null requires ptr", ctx)
		}
	case OVecLit:
		vt, ok := t.(VecType)
		if !ok || len(o.Vec) != vt.Len {
			return fmt.Errorf("%s: vector literal doesn't match type %s", ctx, t)
		}
	default:
		return fmt.Errorf("%s: not a literal", ctx)
	}
	return nil
}

func (v *verifier) checkInit(init ConstInit, t Type, ctx string) error {
	switch x := init.(type) {
	case nil:
		return fmt.Errorf("%s: missing initializer", ctx)
	case InitZero:
		return nil
	case InitLit:
		return v.checkLiteral(x.Value, t, ctx)
	case InitAddr:
		if !IsPtr(t) {
			return fmt.Errorf("%s: addr initializer requires ptr type (§8)", ctx)
		}
		switch v.names[x.Name] {
		case "global":
			for _, g := range v.m.Globals {
				if g.Name == x.Name && g.TLS {
					return fmt.Errorf("%s: addr of tls global %q is forbidden in static initializers (§1.2 rule 7)", ctx, x.Name)
				}
			}
		case "fn", "extern":
			// Note: extern is unreachable here in practice (section order, §8) —
			// externs are declared after globals, so declare-before-use fails first.
		default:
			return fmt.Errorf("%s: addr references %q, which is not a previously declared global/fn (§8)", ctx, x.Name)
		}
		return nil
	case InitBytes:
		at, ok := t.(ArrayType)
		if !ok || !Equal(at.Elem, I8) {
			return fmt.Errorf("%s: byte-string initializer requires array[i8, N] (§8)", ctx)
		}
		if len(x.Data) != at.Len {
			return fmt.Errorf("%s: byte string is %d bytes, array is %d (must match exactly, §8)", ctx, len(x.Data), at.Len)
		}
		return nil
	case InitAgg:
		switch tt := t.(type) {
		case StructType:
			var st *Struct
			for _, s := range v.m.Structs {
				if s.Name == tt.Name {
					st = s
				}
			}
			if st == nil {
				return fmt.Errorf("%s: struct %q not declared (§1.2 rule 2)", ctx, tt.Name)
			}
			if len(x.Elems) != len(st.Fields) {
				return fmt.Errorf("%s: struct %s wants %d elements, got %d (§8)", ctx, tt.Name, len(st.Fields), len(x.Elems))
			}
			for i, e := range x.Elems {
				if err := v.checkInit(e, st.Fields[i].Type, ctx); err != nil {
					return err
				}
			}
		case ArrayType:
			if len(x.Elems) > tt.Len {
				return fmt.Errorf("%s: array of %d has %d initializer elements (§8)", ctx, tt.Len, len(x.Elems))
			}
			for _, e := range x.Elems {
				if err := v.checkInit(e, tt.Elem, ctx); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("%s: aggregate initializer for non-aggregate type %s", ctx, t)
		}
		return nil
	}
	return fmt.Errorf("%s: unknown initializer form", ctx)
}

// ---------------------------------------------------------------------------
// Function bodies
// ---------------------------------------------------------------------------

func (v *verifier) verifyFunc(f *Func) error {
	if f.Entry == nil {
		return fmt.Errorf("missing entry block (§1.3 rule 1)")
	}
	blocks := f.AllBlocks()
	labels := map[string]*Block{}
	for _, b := range f.Blocks {
		labels[b.Label] = b
	}

	// §9.10 — block validity.
	referenced := map[string]bool{}
	for _, b := range blocks {
		if b.Term == nil {
			return fmt.Errorf("block %q has no terminator (§1.3 rule 2)", labelOrEntry(b))
		}
		if b.Label != "" && len(b.Insts) == 0 {
			// A lone terminator satisfies "at least one line" (§1.3 rule 3).
		}
		for _, l := range Successors(b.Term) {
			if _, ok := labels[l]; !ok {
				return fmt.Errorf("block %q branches to unknown label %q (§1.3 rule 4)", labelOrEntry(b), l)
			}
			referenced[l] = true
		}
		if err := v.checkTerm(f, b); err != nil {
			return err
		}
	}
	// §9.11 — every label targeted by at least one branch.
	for _, b := range f.Blocks {
		if !referenced[b.Label] {
			return fmt.Errorf("label %q is never branched to (§9.11)", b.Label)
		}
	}

	// Type fixation pre-pass (§5 rule 2 / §9.14) over textual order.
	types := map[string]Type{}
	for _, p := range f.Params {
		if _, dup := types[p.Name]; dup {
			return fmt.Errorf("duplicate parameter name %q", p.Name)
		}
		types[p.Name] = p.Type
	}
	for _, b := range blocks {
		for i := range b.Insts {
			inst := &b.Insts[i]
			if inst.Op == "loc" {
				continue
			}
			rt, err := v.resultType(f, inst)
			if err != nil {
				return fmt.Errorf("block %q: %w", labelOrEntry(b), err)
			}
			if IsVoid(rt) || rt == nil {
				if inst.Result != "" {
					return fmt.Errorf("block %q: %s produces no value but has a result name (§1.3 rule 6)", labelOrEntry(b), inst.Op)
				}
				continue
			}
			if inst.Result == "" {
				return fmt.Errorf("block %q: %s produces a value; result name is not optional (§1.3 rule 6)", labelOrEntry(b), inst.Op)
			}
			if kind, taken := v.names[inst.Result]; taken {
				return fmt.Errorf("value %q shadows module-level %s (flat namespace, §1.2)", inst.Result, kind)
			}
			if prev, ok := types[inst.Result]; ok {
				if !Equal(prev, rt) {
					return fmt.Errorf("value %q assigned as %s here but fixed as %s at first assignment (§5 rule 2)", inst.Result, rt, prev)
				}
			} else {
				types[inst.Result] = rt
			}
			if err := v.checkInst(f, inst, types); err != nil {
				return fmt.Errorf("block %q: %w", labelOrEntry(b), err)
			}
		}
	}

	// §5 rule 5 / §9.15 — definite assignment, forward must-analysis.
	return v.definiteAssignment(f, blocks, labels, types)
}

func labelOrEntry(b *Block) string {
	if b.Label == "" {
		return "<entry>"
	}
	return b.Label
}

func (v *verifier) definiteAssignment(f *Func, blocks []*Block, labels map[string]*Block, types map[string]Type) error {
	universe := map[string]bool{}
	for n := range types {
		universe[n] = true
	}
	full := func() map[string]bool {
		s := make(map[string]bool, len(universe))
		for n := range universe {
			s[n] = true
		}
		return s
	}
	preds := map[string][]*Block{}
	for _, b := range blocks {
		for _, l := range Successors(b.Term) {
			preds[l] = append(preds[l], b)
		}
	}
	in := map[*Block]map[string]bool{}
	out := map[*Block]map[string]bool{}
	for _, b := range blocks {
		in[b], out[b] = full(), full()
	}
	entryIn := map[string]bool{}
	for _, p := range f.Params {
		entryIn[p.Name] = true
	}
	in[f.Entry] = entryIn

	transfer := func(b *Block, s map[string]bool) map[string]bool {
		o := make(map[string]bool, len(s))
		for n := range s {
			o[n] = true
		}
		for _, i := range b.Insts {
			if i.Result != "" {
				o[i.Result] = true
			}
		}
		return o
	}
	changed := true
	for changed {
		changed = false
		for _, b := range blocks {
			if b != f.Entry {
				var meet map[string]bool
				for _, p := range preds[b.Label] {
					if meet == nil {
						meet = copySet(out[p])
					} else {
						meet = intersect(meet, out[p])
					}
				}
				if meet == nil {
					meet = full() // unreachable block: vacuously ⊤
				}
				if !sameSet(in[b], meet) {
					in[b] = meet
					changed = true
				}
			}
			no := transfer(b, in[b])
			if !sameSet(out[b], no) {
				out[b] = no
				changed = true
			}
		}
	}

	// Linear scan: check reads.
	for _, b := range blocks {
		assigned := copySet(in[b])
		checkRead := func(name string, what string) error {
			if !universe[name] {
				return nil // module-level entity, not a local value
			}
			if !assigned[name] {
				return fmt.Errorf("block %q: read of possibly-unassigned value %q in %s (§5 rule 3)", labelOrEntry(b), name, what)
			}
			return nil
		}
		for _, inst := range b.Insts {
			if inst.Op == "loc" {
				continue
			}
			skip := entityArgPositions(inst)
			for i, a := range inst.Args {
				if a.Kind == OIdent && !skip[i] {
					if err := checkRead(a.Ident, inst.Op); err != nil {
						return err
					}
				}
			}
			if inst.Result != "" {
				assigned[inst.Result] = true
			}
		}
		for _, a := range termOperands(b.Term) {
			if a.Kind == OIdent {
				if err := checkRead(a.Ident, "terminator"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// entityArgPositions returns operand indices that name compile-time entities
// rather than runtime values (§1.1 note on operand positions).
func entityArgPositions(i Inst) map[int]bool {
	switch {
	case i.Op == "field": // field.ptr p, S, f
		return map[int]bool{1: true, 2: true}
	case i.Op == "call" && i.Sig == "": // direct call: callee is not an operand position
		return map[int]bool{0: true}
	}
	return nil
}

func termOperands(t Terminator) []Operand {
	switch x := t.(type) {
	case BrIf:
		return []Operand{x.Cond}
	case Switch:
		return []Operand{x.Value}
	case Return:
		if x.Value != nil {
			return []Operand{*x.Value}
		}
	case TailCall:
		return x.Args
	}
	return nil
}

// ---------------------------------------------------------------------------
// Per-instruction / per-terminator checks
// ---------------------------------------------------------------------------

var intOnlyBin = set("udiv", "sdiv", "urem", "srem", "and", "or", "xor",
	"shl", "lshr", "ashr", "rotl", "rotr",
	"uadd_sat", "sadd_sat", "usub_sat", "ssub_sat", "umulh", "smulh",
	"smin", "smax", "umin", "umax")
var overflowPreds = set("uaddo", "saddo", "usubo", "ssubo", "umulo", "smulo")
var numBin = set("add", "sub", "mul")
var floatOnlyUnary = set("sqrt", "fma", "copysign", "floor", "ceil", "trunc_f", "nearest")
var intCmps = set("eq", "ne", "slt", "sgt", "sle", "sge", "ult", "ugt", "ule", "uge")
var floatCmps = set("lt", "gt", "le", "ge")
var conversions = set("trunc", "sext", "zext", "fdemote", "fpromote", "bitcast",
	"sfromint", "ufromint", "stoint", "utoint", "stoint_sat", "utoint_sat")

func set(ss ...string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// resultType computes the type a producing instruction yields, or Void.
func (v *verifier) resultType(f *Func, i *Inst) (Type, error) {
	op := i.Op
	switch {
	case op == "store" || op == "store_vol" || op == "atomic_store" ||
		op == "memcopy" || op == "memmove" || op == "memset" ||
		op == "fence" || op == "prefetch" || op == "masked_store" || op == "scatter":
		return Void, nil
	case op == "min" || op == "max":
		if i.Suffix != nil && IsInt(ElemOrSelf(i.Suffix)) {
			return nil, fmt.Errorf("bare %s.%s rejected: use smin/smax/umin/umax on integers (§9.17)", op, i.Suffix)
		}
		return i.Suffix, nil
	case intCmps[op] || floatCmps[op] || overflowPreds[op]:
		if vt, ok := i.Suffix.(VecType); ok {
			return VecType{Elem: I1, Len: vt.Len}, nil
		}
		return I1, nil
	case op == "call" || op == "asm":
		if i.Sig != "" { // indirect: suffix names a fnsig (§9.16)
			for _, s := range v.m.FnSigs {
				if s.Name == i.Sig {
					return s.Ret, nil
				}
			}
			return nil, fmt.Errorf("call.%s: fnsig not declared (§9.16)", i.Sig)
		}
		if op == "asm" {
			if i.Suffix == nil {
				return Void, nil
			}
			return i.Suffix, nil
		}
		if len(i.Args) == 0 || i.Args[0].Kind != OIdent {
			return nil, fmt.Errorf("direct call missing callee name")
		}
		callee := i.Args[0].Ident
		if r, ps, ok := v.lookupCallable(callee); ok {
			_ = ps
			return r, nil
		}
		return nil, fmt.Errorf("call to %q: not a previously declared fn/extern fn (§1.2 rule 2)", callee)
	case op == "extract":
		if vt, ok := i.Suffix.(VecType); ok {
			return vt.Elem, nil
		}
		return nil, fmt.Errorf("extract requires a vec suffix")
	case op == "reduce_add" || op == "reduce_min" || op == "reduce_max" ||
		op == "reduce_and" || op == "reduce_or" || op == "reduce_xor":
		if vt, ok := i.Suffix.(VecType); ok {
			return vt.Elem, nil
		}
		return nil, fmt.Errorf("%s requires a vec suffix", op)
	case i.Suffix != nil:
		return i.Suffix, nil
	}
	return nil, fmt.Errorf("op %q has no type suffix and no known result type", op)
}

func (v *verifier) lookupCallable(name string) (ret Type, params []Param, ok bool) {
	for _, g := range v.m.Externs {
		for _, e := range g.Fns {
			if e.Name == name {
				return e.Ret, e.Params, true
			}
		}
	}
	for _, fn := range v.m.Funcs {
		if fn.Name == name {
			return fn.Ret, fn.Params, true
		}
	}
	return nil, nil, false
}

func (v *verifier) checkInst(f *Func, i *Inst, types map[string]Type) error {
	op := i.Op
	elem := Type(nil)
	if i.Suffix != nil {
		elem = ElemOrSelf(i.Suffix)
	}
	switch {
	case numBin[op], intOnlyBin[op], overflowPreds[op]:
		if len(i.Args) != 2 {
			return fmt.Errorf("%s: expected 2 operands", op)
		}
		if intOnlyBin[op] || overflowPreds[op] {
			if elem == nil || !IsInt(elem) {
				return fmt.Errorf("%s legal only on iN / vec[iN, W] (§9.18)", op)
			}
		}
	case op == "bswap":
		if Equal(elem, I8) {
			return fmt.Errorf("bswap rejected on i8 (§9.20)")
		}
	case op == "alloca":
		if !IsPtr(i.Suffix) {
			return fmt.Errorf("alloca suffix must be ptr")
		}
	case op == "field":
		if len(i.Args) != 3 {
			return fmt.Errorf("field.ptr: expected p, S, f")
		}
		sName := i.Args[1].Ident
		var st *Struct
		for _, s := range v.m.Structs {
			if s.Name == sName {
				st = s
			}
		}
		if st == nil {
			return fmt.Errorf("field.ptr: %q is not a declared struct (§9.24)", sName)
		}
		if _, ok := st.Field(i.Args[2].Ident); !ok {
			return fmt.Errorf("field.ptr: struct %s has no field %q (§9.24)", sName, i.Args[2].Ident)
		}
	case op == "index":
		if len(i.Args) != 3 || i.Args[1].Kind != OType {
			return fmt.Errorf("index.ptr: expected p, T, i (§9.24)")
		}
		if err := v.checkSizedType(i.Args[1].Type, "index.ptr"); err != nil {
			return err
		}
	case conversions[op]:
		if len(i.Args) != 1 {
			return fmt.Errorf("%s: expected 1 operand", op)
		}
	case op == "switchless": // placeholder, never hit
	}
	if i.Align != 0 && !isPow2(i.Align) {
		return fmt.Errorf("%s: align %d not a power of two (§9.25)", op, i.Align)
	}
	if strings.HasPrefix(op, "atomic_") || op == "cmpxchg" {
		if i.Align != 0 {
			return fmt.Errorf("%s: atomics carry no alignment clause (§9.25)", op)
		}
		if err := checkOrderings(i); err != nil {
			return err
		}
	}
	// TODO(§9.16): full operand-type unification against the suffix.
	// TODO(§9.31): shuffle mask bounds. TODO(§9.32): tier gating.
	return nil
}

func checkOrderings(i *Inst) error {
	ords := []string{}
	for _, a := range i.Args {
		if a.Kind == OOrdering {
			ords = append(ords, a.Ord)
		}
	}
	bad := func(o string, disallowed ...string) error {
		for _, d := range disallowed {
			if o == d {
				return fmt.Errorf("%s: ordering %q not permitted (§9.26)", i.Op, o)
			}
		}
		return nil
	}
	switch i.Op {
	case "atomic_load":
		if len(ords) == 1 {
			return bad(ords[0], "release", "acqrel")
		}
	case "atomic_store":
		if len(ords) == 1 {
			return bad(ords[0], "acquire", "acqrel")
		}
	case "cmpxchg":
		if len(ords) == 2 {
			if err := bad(ords[1], "release", "acqrel"); err != nil {
				return err
			}
			if strength(ords[1]) > strength(ords[0]) {
				return fmt.Errorf("cmpxchg: failure ordering stronger than success (§4)")
			}
		}
	}
	return nil
}

func strength(o string) int {
	switch o {
	case "relaxed":
		return 0
	case "acquire", "release":
		return 1
	case "acqrel":
		return 2
	case "seqcst":
		return 3
	}
	return -1
}

func (v *verifier) checkTerm(f *Func, b *Block) error {
	switch t := b.Term.(type) {
	case BrIf:
		// §9.21 — cond must be i1; only checkable when the operand is typed.
		if t.Cond.Kind == OBool {
			return nil
		}
	case Switch:
		if t.Value.Kind == OFloat || t.Value.Kind == ONull {
			return fmt.Errorf("switch operand must be iN (§9.22)")
		}
		seen := map[int64]bool{}
		for _, c := range t.Cases {
			if seen[c.Value] {
				return fmt.Errorf("switch: duplicate case value %d (§9.22)", c.Value)
			}
			seen[c.Value] = true
		}
	case Return:
		if t.Value == nil && !IsVoid(f.Ret) {
			return fmt.Errorf("return without value in non-void function")
		}
		if t.Value != nil && IsVoid(f.Ret) {
			return fmt.Errorf("return with value in void function")
		}
	case TailCall:
		if t.Callee != "" {
			ret, params, ok := v.lookupCallable(t.Callee)
			if !ok {
				return fmt.Errorf("tailcall to undeclared %q", t.Callee)
			}
			if !Equal(ret, f.Ret) {
				return fmt.Errorf("tailcall: callee returns %s, caller returns %s (§9.29)", ret, f.Ret)
			}
			for _, p := range params {
				if p.ByVal != "" || p.SRet != "" {
					return fmt.Errorf("tailcall: callee has byval/sret params (§9.29)")
				}
			}
		} else {
			found := false
			for _, s := range v.m.FnSigs {
				if s.Name == t.Sig {
					found = true
					if !Equal(s.Ret, f.Ret) {
						return fmt.Errorf("tailcall.%s: signature returns %s, caller returns %s (§9.29)", t.Sig, s.Ret, f.Ret)
					}
				}
			}
			if !found {
				return fmt.Errorf("tailcall.%s: fnsig not declared (§9.16)", t.Sig)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Link-name derivation (§7.4)
// ---------------------------------------------------------------------------

func isExactName(s string) bool {
	return strings.ContainsAny(s, "./\\")
}

func deriveLinkFile(l *Link, f BinFormat) (string, error) {
	if isExactName(l.Name) {
		if err := checkExactExtension(l, f); err != nil {
			return "", err
		}
		return l.Name, nil
	}
	switch l.Kind {
	case LinkShared:
		switch f {
		case FormatELF:
			return "lib" + l.Name + ".so", nil
		case FormatMachO:
			return "lib" + l.Name + ".dylib", nil
		case FormatPE:
			return l.Name + ".dll", nil
		}
	case LinkStatic:
		if f == FormatPE {
			return l.Name + ".lib", nil
		}
		return "lib" + l.Name + ".a", nil
	case LinkFramework:
		return l.Name + ".framework/" + l.Name, nil
	}
	return "", fmt.Errorf("link %q: cannot derive filename", l.Name)
}

func checkExactExtension(l *Link, f BinFormat) error {
	n := l.Name
	ok := false
	switch l.Kind {
	case LinkShared:
		switch f {
		case FormatELF:
			ok = strings.Contains(n, ".so") // .so plus optional version components
		case FormatMachO:
			ok = strings.HasSuffix(n, ".dylib")
		case FormatPE:
			ok = strings.HasSuffix(n, ".dll")
		}
	case LinkStatic:
		ok = strings.HasSuffix(n, ".a") || strings.HasSuffix(n, ".lib")
	}
	if !ok {
		return fmt.Errorf("link %s %q: extension does not agree with kind for target format (§7.4)", l.Kind, n)
	}
	return nil
}

// ---------------------------------------------------------------------------
// small set helpers
// ---------------------------------------------------------------------------

func isPow2(n int) bool { return n > 0 && n&(n-1) == 0 }

func copySet(s map[string]bool) map[string]bool {
	o := make(map[string]bool, len(s))
	for k := range s {
		o[k] = true
	}
	return o
}

func intersect(a, b map[string]bool) map[string]bool {
	o := map[string]bool{}
	for k := range a {
		if b[k] {
			o[k] = true
		}
	}
	return o
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}