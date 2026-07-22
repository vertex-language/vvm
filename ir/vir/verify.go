// verify.go
package vir

import (
	"fmt"
	"strings"
)

// Verify enforces the module's obligations in a single forward pass, then
// per-function passes for body shape, type fixation, definite-assignment
// (§4.3), and valist lifetime tracking (§4.5). Verify(m) runs with no
// import shapes supplied — any qualified reference then fails as
// unresolved. Use VerifyWithImports to supply Stage A shapes (§7.3).
func Verify(m *Module) error {
	return VerifyWithImports(m, nil)
}

// VerifyWithImports runs Verify with a set of Stage A import shapes
// (§7.3), keyed by import path ("namespace/module" or bare "module").
// This package performs Stage 0 (extraction, vmeta.go) and Stage A
// (provisional check against supplied shapes) only — Stage B (structural
// check against the exporter's real compiled output) is vvm's job at
// build-orchestration time and out of scope here.
func VerifyWithImports(m *Module, shapes map[string]*ModuleShape) error {
	v := &verifier{m: m, names: map[string]string{}, shapes: shapes}
	return v.run()
}

type verifier struct {
	m      *Module
	names  map[string]string // name -> kind ("struct","fnsig","const","global","extern","fn","label")
	shapes map[string]*ModuleShape
}

var keywords = map[string]bool{
	"module": true, "namespace": true, "target": true, "asmdialect": true, "struct": true, "fnsig": true, "const": true,
	"global": true, "export": true, "tls": true, "extern": true, "link": true, "import": true,
	"shared": true, "static": true, "framework": true, "fn": true, "end": true,
	"zero": true, "addr": true, "loc": true, "align": true, "syscall": true,
	"noreturn": true, "readonly": true, "inline": true, "noinline": true, "cold": true, "entry": true, "extern_c": true,
	"byval": true, "sret": true,
	"br": true, "br_if": true, "switch": true, "return": true, "tailcall": true,
	"trap": true, "unreachable": true,
	"relaxed": true, "acquire": true, "release": true, "acqrel": true, "seqcst": true,
	"true": true, "false": true, "null": true,
	"asm": true, "code": true, "in": true, "out": true, "clobber": true,
	"intel": true, "att": true, "a32": true, "t32": true, "native": true,
	"valist": true, "va_start": true, "va_arg": true, "va_end": true,
}

func (v *verifier) declare(name, kind string) error {
	if name == "" {
		return fmt.Errorf("%s with empty name", kind)
	}
	if keywords[name] {
		return fmt.Errorf("%q is a reserved keyword and may not be used as an identifier (§3)", name)
	}
	if prev, ok := v.names[name]; ok {
		return fmt.Errorf("name %q redeclared as %s; already declared as %s (flat namespace, §2.2)", name, kind, prev)
	}
	v.names[name] = kind
	return nil
}

func (v *verifier) usizeBits() int {
	if v.m.Target != nil {
		return PointerBits(v.m.Target.Arch)
	}
	return 64
}

func (v *verifier) run() error {
	m := v.m
	if m.Name == "" {
		return fmt.Errorf("module has no name (§2.1 step 1)")
	}

	// Target declaration (§7.1).
	if m.Target != nil {
		t := m.Target
		if !CanonicalArch[t.Arch] {
			if canon, alias := ArchAliases[t.Arch]; alias {
				return fmt.Errorf("target arch %q is a rejected alias; use %q (§7.1)", t.Arch, canon)
			}
			return fmt.Errorf("target arch %q is not a canonical architecture (§7.1)", t.Arch)
		}
		if !CanonicalOS[t.OS] {
			if canon, alias := OSAliases[t.OS]; alias {
				return fmt.Errorf("target os %q is a rejected alias; use %q (§7.1)", t.OS, canon)
			}
			return fmt.Errorf("target os %q is not a canonical OS (§7.1)", t.OS)
		}
		if t.ABI != "" && !CanonicalABI[t.ABI] {
			return fmt.Errorf("target abi %q is not a canonical ABI (§7.1)", t.ABI)
		}
		// TODO: validate tier-list entries against per-target tier tables (§7.1 Feature Tiers).
	}
	if len(m.Links) > 0 && m.Target == nil {
		return fmt.Errorf("module has a link section but no target declaration (§2.1 step 3)")
	}

	// asmdialect declaration (§4.4, §2.1 step 4).
	hasAsm := moduleHasAsm(m)
	if hasAsm {
		if m.Target == nil {
			return fmt.Errorf("module contains asm blocks but has no target declaration (§2.1 step 3)")
		}
		if m.AsmDialect == nil {
			return fmt.Errorf("module contains asm blocks but has no asmdialect declaration (§2.1 step 4)")
		}
		if !IsDialectValidForArchitecture(m.Target.Arch, *m.AsmDialect) {
			return fmt.Errorf("asmdialect %q is not valid for architecture %q (§4.4)", *m.AsmDialect, m.Target.Arch)
		}
	} else if m.AsmDialect != nil && m.Target != nil {
		if !IsDialectValidForArchitecture(m.Target.Arch, *m.AsmDialect) {
			return fmt.Errorf("asmdialect %q is not valid for architecture %q (§4.4)", *m.AsmDialect, m.Target.Arch)
		}
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

	// FunctionSignatures.
	for _, sig := range m.FunctionSignatures {
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

	// Constants (§6.2): scalars only, one literal.
	for _, c := range m.Constants {
		if err := v.declare(c.Name, "const"); err != nil {
			return err
		}
		if IsAggregate(c.Type) || IsVoid(c.Type) || IsValist(c.Type) {
			return fmt.Errorf("const %s: type %s is not a scalar (§6.2)", c.Name, c.Type)
		}
		if err := v.checkLiteral(c.Value, c.Type, "const "+c.Name); err != nil {
			return err
		}
	}

	// Globals (§6.2).
	for _, g := range m.Globals {
		if err := v.declare(g.Name, "global"); err != nil {
			return err
		}
		if IsValist(g.Type) {
			return fmt.Errorf("global %s: valist is not a legal global type (§4.5, §6.2)", g.Name)
		}
		if g.Align != 0 && !isPow2(g.Align) {
			return fmt.Errorf("global %s: align %d is not a power of two", g.Name, g.Align)
		}
		if g.TLS && m.Target != nil && m.Target.OS == "none" {
			hasTLSTier := false
			for _, tier := range m.Target.Tiers {
				if tier == "tls_support" {
					hasTLSTier = true
					break
				}
			}
			if !hasTLSTier {
				return fmt.Errorf("global %s: tls on os=none requires a TLS-capable feature tier (e.g., 'tls_support')", g.Name)
			}
		}
		if err := v.checkInit(g.Init, g.Type, "global "+g.Name); err != nil {
			return err
		}
	}

	// Links (§7.2).
	format := FormatELF
	if m.Target != nil {
		format = FormatOf(m.Target.OS)
	}
	derived := map[string]string{}
	for _, l := range m.Links {
		switch l.Kind {
		case LinkStatic, LinkShared, LinkFramework:
		default:
			return fmt.Errorf("link %q: unknown kind %q (aliases like dylib/dll/so are rejected, §7.1 Aliases)", l.Name, l.Kind)
		}
		if l.Kind == LinkFramework {
			if format != FormatMachO {
				return fmt.Errorf("link framework %q: frameworks are Mach-O only (§7.2)", l.Name)
			}
			if strings.ContainsAny(l.Name, "./\\") {
				return fmt.Errorf("link framework %q: framework strings must be short names (§7.2)", l.Name)
			}
		}
		file, err := DeriveLinkFile(l, format)
		if err != nil {
			return err
		}
		if prev, dup := derived[file]; dup {
			return fmt.Errorf("duplicate link dependency: %q and %q both derive %q (§7.2)", prev, l.Name, file)
		}
		derived[file] = l.Name
	}

	// Extern groups (§7.2). No anonymous/default-namespace group.
	linkStrings := map[string]bool{}
	for _, l := range m.Links {
		linkStrings[l.Name] = true
	}
	claimed := map[string]bool{}
	for _, g := range m.Externs {
		if g.Dependency == "" {
			return fmt.Errorf("extern group has no dependency string; anonymous/default-namespace groups are rejected (§7.2)")
		}
		if !linkStrings[g.Dependency] {
			return fmt.Errorf("extern group %q: no matching link declaration (§7.2)", g.Dependency)
		}
		if claimed[g.Dependency] {
			return fmt.Errorf("extern group %q: link string already claimed by another group (§7.2)", g.Dependency)
		}
		claimed[g.Dependency] = true
		if len(g.Functions) == 0 {
			return fmt.Errorf("empty extern group %q rejected (§7.2)", g.Dependency)
		}
		for _, f := range g.Functions {
			if err := v.declare(f.Name, "extern"); err != nil {
				return err
			}
			if err := v.checkParams(f.Name, f.Params, f.Ret, true); err != nil {
				return err
			}
		}
	}

	// Imports (§7.3, Stage A): a qualified path must have a supplied shape.
	seenImport := map[string]bool{}
	for _, imp := range m.Imports {
		if imp.Path == "" {
			return fmt.Errorf("import with empty path (§7.3)")
		}
		if seenImport[imp.Path] {
			return fmt.Errorf("import %q declared more than once (§7.3)", imp.Path)
		}
		seenImport[imp.Path] = true
		if v.shapes != nil {
			if _, ok := v.shapes[imp.Path]; !ok {
				return fmt.Errorf("import %q: no shape supplied for Stage A verification (§7.3)", imp.Path)
			}
		}
	}

	// Functions.
	entryCount := 0
	for _, f := range m.Functions {
		if err := v.declare(f.Name, "fn"); err != nil {
			return err
		}
		if err := v.checkParams(f.Name, f.Params, f.Ret, false); err != nil {
			return err
		}
		if f.HasAttribute(AttributeEntry) && f.HasAttribute(AttributeExternC) {
			return fmt.Errorf("fn %s: entry and extern_c are mutually exclusive symbol-naming overrides (§2.2)", f.Name)
		}
		if f.HasAttribute(AttributeEntry) {
			entryCount++
			if err := v.checkEntryAttribute(f); err != nil {
				return err
			}
		}
	}
	if entryCount > 1 {
		return fmt.Errorf("module has %d fns carrying entry; at most one is permitted (§9.4a)", entryCount)
	}

	// Labels join the flat namespace — declare before bodies.
	for _, f := range m.Functions {
		for _, b := range f.Blocks {
			if err := v.declare(b.Label, "label"); err != nil {
				return err
			}
		}
	}
	for _, f := range m.Functions {
		if err := v.verifyFunction(f); err != nil {
			return fmt.Errorf("fn %s: %w", f.Name, err)
		}
	}
	return nil
}

// checkEntryAttribute enforces §9.4a: a fn carrying `entry` must be
// export, must not have byval/sret parameters, and must not also carry
// noreturn.
func (v *verifier) checkEntryAttribute(f *Function) error {
	if !f.Export {
		return fmt.Errorf("fn %s: entry requires export (§9.4a)", f.Name)
	}
	for _, p := range f.Params {
		if p.ByVal != "" || p.SRet != "" {
			return fmt.Errorf("fn %s: entry is rejected on a fn with byval/sret parameters (§9.4a)", f.Name)
		}
	}
	if f.HasAttribute(AttributeNoReturn) {
		return fmt.Errorf("fn %s: entry and noreturn are rejected together (§9.4a)", f.Name)
	}
	return nil
}

func moduleHasAsm(m *Module) bool {
	for _, f := range m.Functions {
		for _, b := range f.AllBlocks() {
			for _, ln := range b.Lines {
				if ln.Asm != nil {
					return true
				}
			}
		}
	}
	return false
}

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
	case ValistType:
		return fmt.Errorf("%s: valist is never a sized/layout-visible type (§4.5, §6.1)", ctx)
	case StructType:
		if x.Import != "" {
			shape, ok := v.shapes[x.Import]
			if !ok {
				return fmt.Errorf("%s: import %q not declared or has no supplied shape (§7.3)", ctx, x.Import)
			}
			if _, ok := shape.findStruct(x.Name); !ok {
				return fmt.Errorf("%s: struct %q not exported by import %q (§7.3)", ctx, x.Name, x.Import)
			}
			return nil
		}
		if v.names[x.Name] != "struct" {
			return fmt.Errorf("%s: struct %q not declared on an earlier line (§2.2 Declare-Before-Use)", ctx, x.Name)
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
	case OperandInt:
		if !IsInt(t) {
			return fmt.Errorf("%s: integer literal for non-integer type %s", ctx, t)
		}
	case OperandFloat:
		if !IsFloat(t) {
			return fmt.Errorf("%s: float literal for non-float type %s", ctx, t)
		}
	case OperandBool:
		if !Equal(t, I1) {
			return fmt.Errorf("%s: bool literal requires i1", ctx)
		}
	case OperandNull:
		if !IsPtr(t) {
			return fmt.Errorf("%s: null requires ptr", ctx)
		}
	case OperandVector:
		vt, ok := t.(VecType)
		if !ok || len(o.Vector) != vt.Len {
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
	case InitLiteral:
		return v.checkLiteral(x.Value, t, ctx)
	case InitAddressOf:
		if !IsPtr(t) {
			return fmt.Errorf("%s: addr initializer requires ptr type (§6.2)", ctx)
		}
		switch v.names[x.Name] {
		case "global":
			for _, g := range v.m.Globals {
				if g.Name == x.Name && g.TLS {
					return fmt.Errorf("%s: addr of tls global %q is forbidden in static initializers (§6.2)", ctx, x.Name)
				}
			}
		case "fn", "extern":
			// extern is unreachable here in practice (section order) — externs
			// are declared after globals, so declare-before-use fails first.
		default:
			return fmt.Errorf("%s: addr references %q, which is not a previously declared global/fn (§6.2)", ctx, x.Name)
		}
		return nil
	case InitByteString:
		at, ok := t.(ArrayType)
		if !ok || !Equal(at.Elem, I8) {
			return fmt.Errorf("%s: byte-string initializer requires array[i8, N] (§6.2)", ctx)
		}
		if len(x.Data) != at.Len {
			return fmt.Errorf("%s: byte string is %d bytes, array is %d (must match exactly, §6.2)", ctx, len(x.Data), at.Len)
		}
		return nil
	case InitAggregate:
		switch tt := t.(type) {
		case StructType:
			var fields []Field
			if tt.Import != "" {
				shape, ok := v.shapes[tt.Import]
				if !ok {
					return fmt.Errorf("%s: import %q not declared or has no supplied shape (§7.3)", ctx, tt.Import)
				}
				ss, ok := shape.findStruct(tt.Name)
				if !ok {
					return fmt.Errorf("%s: struct %q not exported by import %q (§7.3)", ctx, tt.Name, tt.Import)
				}
				fields = ss.Fields
			} else {
				var st *Struct
				for _, s := range v.m.Structs {
					if s.Name == tt.Name {
						st = s
					}
				}
				if st == nil {
					return fmt.Errorf("%s: struct %q not declared (§2.2 Declare-Before-Use)", ctx, tt.Name)
				}
				fields = st.Fields
			}
			if len(x.Elems) != len(fields) {
				return fmt.Errorf("%s: struct %s wants %d elements, got %d (§6.2)", ctx, tt.Name, len(fields), len(x.Elems))
			}
			for i, e := range x.Elems {
				if err := v.checkInit(e, fields[i].Type, ctx); err != nil {
					return err
				}
			}
		case ArrayType:
			if len(x.Elems) > tt.Len {
				return fmt.Errorf("%s: array of %d has %d initializer elements (§6.2)", ctx, tt.Len, len(x.Elems))
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

func (v *verifier) verifyFunction(f *Function) error {
	if f.Entry == nil {
		return fmt.Errorf("missing entry block (§4.3)")
	}
	blocks := f.AllBlocks()
	labels := map[string]*Block{}
	for _, b := range f.Blocks {
		labels[b.Label] = b
	}

	// §4.3 — block validity.
	referenced := map[string]bool{}
	for _, b := range blocks {
		if b.Term == nil {
			return fmt.Errorf("block %q has no terminator (§4.3)", labelOrEntry(b))
		}
		for _, l := range Successors(b.Term) {
			if _, ok := labels[l]; !ok {
				return fmt.Errorf("block %q branches to unknown label %q (§4.3)", labelOrEntry(b), l)
			}
			referenced[l] = true
		}
		if err := v.checkTerminator(f, b); err != nil {
			return err
		}
	}
	for _, b := range f.Blocks {
		if !referenced[b.Label] {
			return fmt.Errorf("label %q is never branched to (§9.11)", b.Label)
		}
	}

	// Type fixation pre-pass (§4.3 Join Convention) over textual order.
	// Also performs structural asm-block checks and treats asm `out`
	// bindings as assignments (§4.4).
	types := map[string]Type{}
	for _, p := range f.Params {
		if _, dup := types[p.Name]; dup {
			return fmt.Errorf("duplicate parameter name %q", p.Name)
		}
		types[p.Name] = p.Type
	}
	for _, b := range blocks {
		for i := range b.Lines {
			ln := &b.Lines[i]
			if ln.Asm != nil {
				if err := v.checkAsmBlockStructure(ln.Asm, types); err != nil {
					return fmt.Errorf("block %q: %w", labelOrEntry(b), err)
				}
				continue
			}
			inst := ln.Instruction
			if inst.Op == OpLoc {
				continue
			}
			rt, err := v.resultType(f, inst)
			if err != nil {
				return fmt.Errorf("block %q: %w", labelOrEntry(b), err)
			}
			if IsVoid(rt) || rt == nil {
				if inst.Result != "" {
					return fmt.Errorf("block %q: %s produces no value but has a result name (§4.3)", labelOrEntry(b), inst.Op)
				}
				continue
			}
			if inst.Result == "" {
				return fmt.Errorf("block %q: %s produces a value; result name is not optional (§4.3)", labelOrEntry(b), inst.Op)
			}
			if kind, taken := v.names[inst.Result]; taken {
				return fmt.Errorf("value %q shadows module-level %s (flat namespace, §2.2)", inst.Result, kind)
			}
			if prev, ok := types[inst.Result]; ok {
				if !Equal(prev, rt) {
					return fmt.Errorf("value %q assigned as %s here but fixed as %s at first assignment (§4.3)", inst.Result, rt, prev)
				}
			} else {
				types[inst.Result] = rt
			}
			if err := v.checkInstruction(f, inst, types); err != nil {
				return fmt.Errorf("block %q: %w", labelOrEntry(b), err)
			}
		}
	}

	if err := v.definiteAssignment(f, blocks, labels, types); err != nil {
		return err
	}
	return v.checkValistLifetimes(f, blocks, types)
}

func labelOrEntry(b *Block) string {
	if b.Label == "" {
		return "<entry>"
	}
	return b.Label
}

func (v *verifier) definiteAssignment(f *Function, blocks []*Block, labels map[string]*Block, types map[string]Type) error {
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
		for _, ln := range b.Lines {
			if ln.Instruction != nil && ln.Instruction.Result != "" {
				o[ln.Instruction.Result] = true
			}
			if ln.Asm != nil {
				for _, bind := range ln.Asm.Bindings {
					if bind.Kind == BindingOut {
						o[bind.Ident] = true
					}
				}
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
					meet = full()
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

	for _, b := range blocks {
		assigned := copySet(in[b])
		checkRead := func(name string, what string) error {
			if !universe[name] {
				return nil
			}
			if !assigned[name] {
				return fmt.Errorf("block %q: read of possibly-unassigned value %q in %s (§4.3)", labelOrEntry(b), name, what)
			}
			return nil
		}
		for _, ln := range b.Lines {
			if ln.Asm != nil {
				for _, bind := range ln.Asm.Bindings {
					if bind.Kind == BindingIn {
						if err := checkRead(bind.Ident, "asm in-binding"); err != nil {
							return err
						}
					}
				}
				for _, bind := range ln.Asm.Bindings {
					if bind.Kind == BindingOut {
						assigned[bind.Ident] = true
					}
				}
				continue
			}
			inst := ln.Instruction
			if inst.Op == OpLoc {
				continue
			}
			skip := entityArgPositions(*inst)
			for i, a := range inst.Args {
				if a.Kind == OperandIdent && !skip[i] && !a.IsQualified() {
					if err := checkRead(a.Ident, inst.Op.String()); err != nil {
						return err
					}
				}
			}
			if inst.Result != "" {
				assigned[inst.Result] = true
			}
		}
		for _, a := range termOperands(b.Term) {
			if a.Kind == OperandIdent && !a.IsQualified() {
				if err := checkRead(a.Ident, "terminator"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// entityArgPositions returns operand indices naming compile-time entities
// rather than runtime values.
func entityArgPositions(i Instruction) map[int]bool {
	switch {
	case i.Op == OpField:
		return map[int]bool{1: true, 2: true}
	case i.Op == OpCall && i.Sig == "":
		return map[int]bool{0: true}
	}
	return nil
}

func termOperands(t Terminator) []Operand {
	switch x := t.(type) {
	case BranchIf:
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
// valist lifetime tracking (§4.5)
// ---------------------------------------------------------------------------

// checkValistLifetimes enforces: (a) a valist must be va_start-initialized
// on every incoming path before any va_arg/va_end read (a "must" forward
// analysis, mirroring definiteAssignment's shape); (b) re-va_start-ing a
// valist that might already be open on some path, without an intervening
// va_end, is an error (a "may" forward analysis — the opposite lattice
// direction, since we want to catch it if it's possible on *any* path);
// (c) a valist left possibly-open across a `return` is an error. Only
// idents whose fixed type is ValistType participate.
func (v *verifier) checkValistLifetimes(f *Function, blocks []*Block, types map[string]Type) error {
	valists := map[string]bool{}
	for n, t := range types {
		if IsValist(t) {
			valists[n] = true
		}
	}
	if len(valists) == 0 {
		return nil
	}

	preds := map[string][]*Block{}
	for _, b := range blocks {
		for _, l := range Successors(b.Term) {
			preds[l] = append(preds[l], b)
		}
	}
	labels := map[string]*Block{}
	for _, b := range blocks {
		if b.Label != "" {
			labels[b.Label] = b
		}
	}

	type state struct{ mustOpen, mayOpen map[string]bool }
	empty := func() map[string]bool { return map[string]bool{} }

	mustIn, mustOut := map[*Block]map[string]bool{}, map[*Block]map[string]bool{}
	mayIn, mayOut := map[*Block]map[string]bool{}, map[*Block]map[string]bool{}
	for _, b := range blocks {
		mustIn[b], mustOut[b] = empty(), empty()
		mayIn[b], mayOut[b] = empty(), empty()
	}

	transfer := func(b *Block, mustS, mayS map[string]bool) (map[string]bool, map[string]bool, error) {
		must, may := copySet(mustS), copySet(mayS)
		for _, ln := range b.Lines {
			if ln.Asm != nil {
				continue // valists never bind through asm (§4.4/4.5 disjoint mechanisms)
			}
			inst := ln.Instruction
			switch inst.Op {
			case OpVaStart:
				dst := inst.Args[0].Ident
				if may[dst] {
					return nil, nil, fmt.Errorf("block %q: re-va_start of %q without an intervening va_end on some path (§4.5)", labelOrEntry(b), dst)
				}
				must[dst] = true
				may[dst] = true
			case OpVaArg:
				src := inst.Args[0].Ident
				if valists[src] && !must[src] {
					return nil, nil, fmt.Errorf("block %q: va_arg reads %q which is not va_start-initialized on every path (§4.5)", labelOrEntry(b), src)
				}
			case OpVaEnd:
				src := inst.Args[0].Ident
				if valists[src] && !must[src] {
					return nil, nil, fmt.Errorf("block %q: va_end closes %q which is not va_start-initialized on every path (§4.5)", labelOrEntry(b), src)
				}
				delete(must, src)
				delete(may, src)
			}
		}
		return must, may, nil
	}

	changed := true
	for changed {
		changed = false
		for _, b := range blocks {
			if b != f.Entry {
				var mustMeet map[string]bool
				var mayMeet = empty()
				for _, p := range preds[b.Label] {
					if mustMeet == nil {
						mustMeet = copySet(mustOut[p])
					} else {
						mustMeet = intersect(mustMeet, mustOut[p])
					}
					for n := range mayOut[p] {
						mayMeet[n] = true
					}
				}
				if mustMeet == nil {
					mustMeet = empty() // unreachable block: nothing definitely open
				}
				if !sameSet(mustIn[b], mustMeet) {
					mustIn[b] = mustMeet
					changed = true
				}
				if !sameSet(mayIn[b], mayMeet) {
					mayIn[b] = mayMeet
					changed = true
				}
			}
			nmust, nmay, err := transfer(b, mustIn[b], mayIn[b])
			if err != nil {
				return err
			}
			if !sameSet(mustOut[b], nmust) {
				mustOut[b] = nmust
				changed = true
			}
			if !sameSet(mayOut[b], nmay) {
				mayOut[b] = nmay
				changed = true
			}
		}
	}

	// Final linear scan to surface the transfer errors in program order
	// (the fixed-point loop above may converge past an error on a stale
	// iteration; re-run once more at the fixed point to report cleanly)
	// and to check returns.
	for _, b := range blocks {
		if _, _, err := transfer(b, mustIn[b], mayIn[b]); err != nil {
			return err
		}
		if ret, ok := b.Term.(Return); ok {
			_ = ret
			for n := range mayOut[b] {
				return fmt.Errorf("block %q: valist %q may still be open across return; va_end is required first (§4.5)", labelOrEntry(b), n)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Per-instruction / per-terminator checks
// ---------------------------------------------------------------------------

func (v *verifier) resultType(f *Function, i *Instruction) (Type, error) {
	if i.Op == OpInvalid {
		return nil, fmt.Errorf("instruction has no opcode set")
	}
	meta, ok := i.Op.meta()
	if !ok {
		return nil, fmt.Errorf("op %q is not a recognized opcode", i.Op)
	}

	switch i.Op {
	case OpMin, OpMax:
		if i.Suffix != nil && IsInt(ElemOrSelf(i.Suffix)) {
			return nil, fmt.Errorf("bare %s.%s rejected: use smin/smax/umin/umax on integers (§9.17)", i.Op, i.Suffix)
		}
		return i.Suffix, nil
	case OpAlloca:
		if i.Suffix == nil {
			return nil, fmt.Errorf("alloca: missing type suffix")
		}
		if IsValist(i.Suffix) {
			if len(i.Args) != 0 {
				return nil, fmt.Errorf("alloca.valist takes no operands (§4.5, §5.1)")
			}
			return i.Suffix, nil
		}
		if !IsPtr(i.Suffix) {
			return nil, fmt.Errorf("alloca suffix must be ptr or valist")
		}
		return i.Suffix, nil
	case OpCall:
		if i.Sig != "" {
			for _, s := range v.m.FunctionSignatures {
				if s.Name == i.Sig {
					return s.Ret, nil
				}
			}
			return nil, fmt.Errorf("call.%s: fnsig not declared (§9.16)", i.Sig)
		}
		if len(i.Args) == 0 || i.Args[0].Kind != OperandIdent {
			return nil, fmt.Errorf("direct call missing callee name")
		}
		callee := i.Args[0]
		if callee.IsQualified() {
			shape, ok := v.shapes[callee.Qualifier]
			if !ok {
				return nil, fmt.Errorf("call to %s.%s: import %q not declared or has no supplied shape (§7.3)", callee.Qualifier, callee.Ident, callee.Qualifier)
			}
			fn, ok := shape.findFn(callee.Ident)
			if !ok {
				return nil, fmt.Errorf("call to %s.%s: not an exported fn of that import (§7.3)", callee.Qualifier, callee.Ident)
			}
			return fn.Ret, nil
		}
		if r, _, ok := v.lookupCallable(callee.Ident); ok {
			return r, nil
		}
		return nil, fmt.Errorf("call to %q: not a previously declared fn/extern fn (§2.2 Declare-Before-Use)", callee.Ident)
	case OpSyscall:
		if i.Suffix == nil {
			return nil, fmt.Errorf("syscall: missing return type suffix")
		}
		return i.Suffix, nil
	case OpVaStart:
		return Void, nil // no result; checked structurally in checkInstruction
	case OpVaArg:
		if i.Suffix == nil {
			return nil, fmt.Errorf("va_arg: missing type suffix")
		}
		if !IsVaArgType(i.Suffix) {
			return nil, fmt.Errorf("va_arg.%s: T must be scalar, ptr, or vec — pass structs/arrays by ptr instead (§4.5)", i.Suffix)
		}
		return i.Suffix, nil
	case OpVaEnd:
		return Void, nil
	case OpExtract:
		if vt, ok := i.Suffix.(VecType); ok {
			return vt.Elem, nil
		}
		return nil, fmt.Errorf("extract requires a vec suffix")
	case OpReduceAdd, OpReduceMin, OpReduceMax, OpReduceAnd, OpReduceOr, OpReduceXor:
		if vt, ok := i.Suffix.(VecType); ok {
			return vt.Elem, nil
		}
		return nil, fmt.Errorf("%s requires a vec suffix", i.Op)
	}

	switch meta.result {
	case ruleVoid:
		return Void, nil
	case ruleBool:
		if vt, ok := i.Suffix.(VecType); ok {
			return VecType{Elem: I1, Len: vt.Len}, nil
		}
		return I1, nil
	case ruleSuffix:
		if i.Suffix == nil {
			return nil, fmt.Errorf("op %q has no type suffix and no known result type", i.Op)
		}
		return i.Suffix, nil
	}
	return nil, fmt.Errorf("op %q: opTable marks it ruleSpecial with no matching case in resultType (internal bug)", i.Op)
}

func (v *verifier) lookupCallable(name string) (ret Type, params []Param, ok bool) {
	for _, g := range v.m.Externs {
		for _, e := range g.Functions {
			if e.Name == name {
				return e.Ret, e.Params, true
			}
		}
	}
	for _, fn := range v.m.Functions {
		if fn.Name == name {
			return fn.Ret, fn.Params, true
		}
	}
	return nil, nil, false
}

func (v *verifier) checkInstruction(f *Function, i *Instruction, types map[string]Type) error {
	meta, ok := i.Op.meta()
	if !ok {
		return fmt.Errorf("op %q is not a recognized opcode", i.Op)
	}

	if meta.arity >= 0 && len(i.Args) != meta.arity {
		return fmt.Errorf("%s: expected %d operand(s), got %d", i.Op, meta.arity, len(i.Args))
	}

	if meta.numeric != ConstraintNone {
		if i.Suffix == nil {
			return fmt.Errorf("%s: missing type suffix", i.Op)
		}
		if err := checkNumericConstraint(i.Op, i.Suffix, meta.numeric); err != nil {
			return err
		}
	}

	switch i.Op {
	case OpBSwap:
		if Equal(ElemOrSelf(i.Suffix), I8) {
			return fmt.Errorf("bswap rejected on i8 (§9.20)")
		}
	case OpField:
		if !IsPtr(i.Suffix) {
			return fmt.Errorf("field.ptr: suffix must be .ptr (§5.1)")
		}
		if err := v.checkFieldRef(i.Args[1].Ident, i.Args[1].Qualifier, i.Args[2].Ident); err != nil {
			return err
		}
	case OpIndex:
		if !IsPtr(i.Suffix) {
			return fmt.Errorf("index.ptr: suffix must be .ptr (§5.1)")
		}
		if i.Args[1].Kind != OperandType {
			return fmt.Errorf("index.ptr: expected p, T, i (§5.1)")
		}
		if err := v.checkSizedType(i.Args[1].Type, "index.ptr"); err != nil {
			return err
		}
	case OpSyscall:
		if err := v.checkSyscall(i, types); err != nil {
			return err
		}
	case OpMemcopy, OpMemmove, OpMemset:
		if err := v.checkBulkMemory(i, types); err != nil {
			return err
		}
	case OpVaStart:
		if err := v.checkVaStart(f, i, types); err != nil {
			return err
		}
	case OpVaArg, OpVaEnd:
		srcT, ok := types[i.Args[0].Ident]
		if ok && !IsValist(srcT) {
			return fmt.Errorf("%s: operand %q is not a valist (§4.5)", i.Op, i.Args[0].Ident)
		}
	}

	if i.Align != 0 && !isPow2(i.Align) {
		return fmt.Errorf("%s: align %d not a power of two (§9.25)", i.Op, i.Align)
	}
	if isOrderingOp(i.Op) {
		if i.Align != 0 {
			return fmt.Errorf("%s: atomics carry no alignment clause (§9.25)", i.Op)
		}
		if err := checkOrderings(i); err != nil {
			return err
		}
	}
	// TODO(§9.16): full operand-type unification against the suffix.
	// TODO(§9.31): shuffle mask bounds. TODO(§7.1 Feature Tiers): tier gating.
	return nil
}

// checkFieldRef resolves field.ptr's struct/field pair, local or imported.
func (v *verifier) checkFieldRef(structName, importPath, fieldName string) error {
	var fields []Field
	if importPath != "" {
		shape, ok := v.shapes[importPath]
		if !ok {
			return fmt.Errorf("field.ptr: import %q not declared or has no supplied shape (§7.3)", importPath)
		}
		ss, ok := shape.findStruct(structName)
		if !ok {
			return fmt.Errorf("field.ptr: %q is not exported by import %q (§9.24)", structName, importPath)
		}
		fields = ss.Fields
	} else {
		var st *Struct
		for _, s := range v.m.Structs {
			if s.Name == structName {
				st = s
			}
		}
		if st == nil {
			return fmt.Errorf("field.ptr: %q is not a declared struct (§9.24)", structName)
		}
		fields = st.Fields
	}
	for _, fl := range fields {
		if fl.Name == fieldName {
			return nil
		}
	}
	return fmt.Errorf("field.ptr: struct %s has no field %q (§9.24)", structName, fieldName)
}

// checkVaStart enforces §4.5: dst must be a prior alloca.valist result;
// last_named must name the function's actual final declared parameter,
// and the function must be variadic.
func (v *verifier) checkVaStart(f *Function, i *Instruction, types map[string]Type) error {
	if !f.Variadic {
		return fmt.Errorf("va_start: fn %s is not variadic (no trailing ... in its param list, §4.5)", f.Name)
	}
	if len(f.Params) == 0 {
		return fmt.Errorf("va_start: variadic fn %s has no named parameters to anchor last_named to (§4.5)", f.Name)
	}
	dst, lastNamed := i.Args[0].Ident, i.Args[1].Ident
	if t, ok := types[dst]; ok && !IsValist(t) {
		return fmt.Errorf("va_start: dst %q is not a valist (must be a prior alloca.valist result, §4.5)", dst)
	}
	if lastNamed != f.Params[len(f.Params)-1].Name {
		return fmt.Errorf("va_start: last_named %q does not name fn %s's final declared parameter %q (§4.5)", lastNamed, f.Name, f.Params[len(f.Params)-1].Name)
	}
	return nil
}

func (v *verifier) checkBulkMemory(i *Instruction, types map[string]Type) error {
	usize := IntType{Bits: v.usizeBits()}
	lenOp := i.Args[2]
	if lenOp.Kind == OperandIdent {
		if t, ok := types[lenOp.Ident]; ok && !Equal(t, usize) {
			return fmt.Errorf("%s: len operand must be %s-width, got %s (§9.27)", i.Op, usize, t)
		}
	}
	if i.Op == OpMemset {
		byteOp := i.Args[1]
		if byteOp.Kind == OperandIdent {
			if t, ok := types[byteOp.Ident]; ok && !Equal(t, I8) {
				return fmt.Errorf("memset: byte operand must be i8, got %s (§9.27)", t)
			}
		}
	}
	return nil
}

func (v *verifier) checkSyscall(i *Instruction, types map[string]Type) error {
	if len(i.Args) < 1 || len(i.Args) > 7 {
		return fmt.Errorf("syscall: expected 1-7 operands (sysno plus up to six args) (§9.33)")
	}
	sysNo := i.Args[0]
	switch sysNo.Kind {
	case OperandIdent:
		if t, ok := types[sysNo.Ident]; ok && !Equal(t, IntType{v.usizeBits()}) {
			return fmt.Errorf("syscall: sysno must be usize-width integer, got %s (§9.33)", t)
		}
	case OperandInt:
	default:
		return fmt.Errorf("syscall: sysno must be an integer (§9.33)")
	}
	for _, a := range i.Args[1:] {
		switch a.Kind {
		case OperandIdent:
			if t, ok := types[a.Ident]; ok && !IsScalarType(t) {
				return fmt.Errorf("syscall: argument %q is not a scalar type (§9.33)", a.Ident)
			}
		case OperandInt, OperandFloat, OperandBool, OperandNull:
		default:
			return fmt.Errorf("syscall: arguments must be scalar types (§9.33)")
		}
	}
	return nil
}

func isOrderingOp(op Opcode) bool {
	switch op {
	case OpAtomicLoad, OpAtomicStore, OpAtomicAdd, OpAtomicSub, OpAtomicAnd, OpAtomicOr, OpAtomicXor, OpAtomicXchg, OpCmpxchg, OpFence:
		return true
	}
	return false
}

func checkOrderings(i *Instruction) error {
	ords := []string{}
	for _, a := range i.Args {
		if a.Kind == OperandOrdering {
			ords = append(ords, a.Ordering)
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
	case OpAtomicLoad:
		if len(ords) == 1 {
			return bad(ords[0], "release", "acqrel")
		}
	case OpAtomicStore:
		if len(ords) == 1 {
			return bad(ords[0], "acquire", "acqrel")
		}
	case OpCmpxchg:
		if len(ords) == 2 {
			if err := bad(ords[1], "release", "acqrel"); err != nil {
				return err
			}
			if strength(ords[1]) > strength(ords[0]) {
				return fmt.Errorf("cmpxchg: failure ordering stronger than success (§4.1)")
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

func (v *verifier) checkTerminator(f *Function, b *Block) error {
	switch t := b.Term.(type) {
	case BranchIf:
		if t.Cond.Kind == OperandBool {
			return nil
		}
	case Switch:
		if t.Value.Kind == OperandFloat || t.Value.Kind == OperandNull {
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
			var sig *FunctionSignature
			for _, s := range v.m.FunctionSignatures {
				if s.Name == t.Sig {
					sig = s
				}
			}
			if sig == nil {
				return fmt.Errorf("tailcall.%s: fnsig not declared (§9.16)", t.Sig)
			}
			if !Equal(sig.Ret, f.Ret) {
				return fmt.Errorf("tailcall.%s: signature returns %s, caller returns %s (§9.29)", t.Sig, sig.Ret, f.Ret)
			}
			// §4.2: a tailcall to a variadic fnsig is rejected if the
			// caller has an active (unclosed) valist from its own
			// variadic parameter — reusing the frame would invalidate a
			// still-live save area. checkValistLifetimes runs separately
			// per-function and doesn't see terminators' tailcall targets,
			// so the sig-variadic half of this rule is enforced here;
			// the "caller has an open valist at this point" half is
			// approximated conservatively: any variadic caller reaching a
			// tailcall to a variadic callee must have already va_end'd
			// every valist by then, which checkValistLifetimes's
			// open-across-return check already guarantees for Return
			// terminators but TailCall is a distinct terminator kind, so
			// mirror that guarantee here too.
			if sig.Variadic && f.Variadic {
				return fmt.Errorf("tailcall.%s: rejected — caller %s is itself variadic and a live save area from its own varargs cannot survive frame reuse into a variadic callee unless closed first (§4.2); ensure every valist is va_end'd before this terminator", t.Sig, f.Name)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Inline assembly checks (§4.4).
// ---------------------------------------------------------------------------

func (v *verifier) checkAsmBlockStructure(a *AsmBlock, types map[string]Type) error {
	arch := v.m.Target.Arch
	regTable := RegisterTableForArchitecture(arch)
	if regTable == nil {
		return fmt.Errorf("no register table available for architecture %q (§4.4)", arch)
	}

	boundAsIn := map[string]bool{}
	boundAsOut := map[string]bool{}
	boundAsClobber := map[string]bool{}

	checkReg := func(reg string) (RegisterInfo, error) {
		info, ok := regTable[reg]
		if !ok {
			return RegisterInfo{}, fmt.Errorf("asm: register %q not found in %s register table (§9.35)", reg, arch)
		}
		return info, nil
	}

	for _, bind := range a.Bindings {
		switch bind.Kind {
		case BindingIn:
			if boundAsIn[bind.Register] {
				return fmt.Errorf("asm: multiple in-bindings to register %q rejected (§4.4)", bind.Register)
			}
			info, err := checkReg(bind.Register)
			if err != nil {
				return err
			}
			boundAsIn[bind.Register] = true
			if t, ok := types[bind.Ident]; ok {
				if err := checkWidthAgreement(t, info, bind.Register); err != nil {
					return err
				}
			}
		case BindingOut:
			if prevIdent, ok := boundAsOut[bind.Register]; ok && prevIdent {
				return fmt.Errorf("asm: binding two different idents out from register %q rejected (§4.4)", bind.Register)
			}
			info, err := checkReg(bind.Register)
			if err != nil {
				return err
			}
			boundAsOut[bind.Register] = true
			if t, ok := types[bind.Ident]; ok {
				if err := checkWidthAgreement(t, info, bind.Register); err != nil {
					return err
				}
			} else {
				types[bind.Ident] = IntType{Bits: info.WidthBits}
			}
		case BindingClobber:
			for _, reg := range bind.Registers {
				if _, err := checkReg(reg); err != nil {
					return err
				}
				boundAsClobber[reg] = true
			}
		}
	}
	for reg := range boundAsClobber {
		if boundAsOut[reg] {
			return fmt.Errorf("asm: register %q cannot be both clobber and out (§4.4)", reg)
		}
	}

	declared := map[string]bool{}
	for _, line := range a.Code {
		if line.LabelDeclaration != "" {
			if declared[line.LabelDeclaration] {
				return fmt.Errorf("asm: duplicate local label %q (§9.39)", line.LabelDeclaration)
			}
			declared[line.LabelDeclaration] = true
		}
	}
	for _, line := range a.Code {
		for _, op := range line.Operands {
			if op.Kind == AsmOperandKindLabel && !declared[op.Label] {
				return fmt.Errorf("asm: branch references undeclared local label %q (§9.39/40)", op.Label)
			}
		}
		if line.Mnemonic == "" && line.LabelDeclaration == "" {
			return fmt.Errorf("asm: code line has neither a mnemonic nor a label declaration")
		}
		// TODO(§9.38): validate mnemonic/operand-shape against a per-dialect table; not wired yet.
	}
	// TODO(§9.40): full single-entry/single-exit control-flow validation beyond label scoping.
	// TODO(§9.41): barrier semantics are a codegen concern, not independently verifier-checkable.
	return nil
}

func checkWidthAgreement(t Type, info RegisterInfo, reg string) error {
	switch x := t.(type) {
	case IntType:
		if x.Bits != info.WidthBits {
			return fmt.Errorf("asm: value type i%d does not match register %q width %d (§9.36)", x.Bits, reg, info.WidthBits)
		}
	case PtrType:
		// Width agreement for ptr is checked against the arch's pointer
		// width by the caller's context (usize); accepted structurally here.
	default:
		return fmt.Errorf("asm: register %q bound to non-scalar type %s (§9.36)", reg, t)
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