// builder.go
package vir

// Builder API. Mirrors the IR one-to-one — it constructs, it doesn't
// check; ir/verify checks. Nothing here validates ordering, names, or
// types. Op arguments are Opcode constants (opcode.go), not strings — a
// typo like "cltz" is a compile error, not something a verifier has to catch.

func NewModule(name string) *Module { return &Module{Name: name} }

// SetNamespace declares the module's namespace (§2.1 step 2, §6.3). Gates
// symbol mangling for export fn/global (see mangle.go).
func (m *Module) SetNamespace(ns string) *Module {
	m.Namespace = ns
	return m
}

func (m *Module) SetTarget(arch, os, abi string, tiers ...string) *Module {
	m.Target = &Target{Arch: arch, OS: os, ABI: abi, Tiers: tiers}
	return m
}

func (m *Module) DeclareStruct(name string, fields ...Field) *Struct {
	s := &Struct{Name: name, Fields: fields}
	m.Structs = append(m.Structs, s)
	return s
}
func (s *Struct) Exported() *Struct { s.Export = true; return s }

func (m *Module) DeclareFunctionSignature(name string, params []Type, variadic bool, ret Type) *FunctionSignature {
	sig := &FunctionSignature{Name: name, Params: params, Variadic: variadic, Ret: ret}
	m.FunctionSignatures = append(m.FunctionSignatures, sig)
	return sig
}
func (s *FunctionSignature) Exported() *FunctionSignature { s.Export = true; return s }

func (m *Module) DeclareConstant(name string, t Type, value Operand) *Constant {
	c := &Constant{Name: name, Type: t, Value: value}
	m.Constants = append(m.Constants, c)
	return c
}
func (c *Constant) Exported() *Constant { c.Export = true; return c }

func (m *Module) DeclareGlobal(name string, t Type, init ConstInit) *Global {
	g := &Global{Name: name, Type: t, Init: init}
	m.Globals = append(m.Globals, g)
	return g
}

func (g *Global) Exported() *Global     { g.Export = true; return g }
func (g *Global) ThreadLocal() *Global  { g.TLS = true; return g }
func (g *Global) Aligned(n int) *Global { g.Align = n; return g }

func (m *Module) DeclareLink(kind LinkKind, name string) *Link {
	l := &Link{Kind: kind, Name: name}
	m.Links = append(m.Links, l)
	return l
}

func (m *Module) DeclareExternGroup(dependency string) *ExternGroup {
	g := &ExternGroup{Dependency: dependency}
	m.Externs = append(m.Externs, g)
	return g
}

func (g *ExternGroup) DeclareFunction(name string, params []Param, ret Type, attrs ...FunctionAttribute) *ExternFunction {
	f := &ExternFunction{Name: name, Params: params, Ret: ret, Attrs: attrs}
	g.Functions = append(g.Functions, f)
	return f
}

func (f *ExternFunction) SetVariadic() *ExternFunction { f.Variadic = true; return f }

// DeclareImport declares a cross-module `import` (§7.3). path is
// "namespace/module" or bare "module".
func (m *Module) DeclareImport(path string) *Import {
	i := &Import{Path: path}
	m.Imports = append(m.Imports, i)
	return i
}

// FunctionBuilder appends to the current block; Label opens a new one.
type FunctionBuilder struct {
	Function *Function
	current  *Block
}

func (m *Module) DeclareFunction(name string, params []Param, ret Type, export bool, attrs ...FunctionAttribute) *FunctionBuilder {
	if ret == nil {
		ret = Void
	}
	f := &Function{Name: name, Params: params, Ret: ret, Export: export, Attrs: attrs}
	f.Entry = &Block{}
	m.Functions = append(m.Functions, f)
	return &FunctionBuilder{Function: f, current: f.Entry}
}

// SetVariadic marks the function's param-list as ending in "..." (§4.4),
// enabling va_start/va_arg/va_end use inside the body.
func (fb *FunctionBuilder) SetVariadic() *FunctionBuilder {
	fb.Function.Variadic = true
	return fb
}

// Label closes nothing (ir/verify enforces termination) and opens a new block.
func (fb *FunctionBuilder) Label(name string) {
	b := &Block{Label: name}
	fb.Function.Blocks = append(fb.Function.Blocks, b)
	fb.current = b
}

func (fb *FunctionBuilder) appendInstruction(i Instruction) Operand {
	fb.current.Lines = append(fb.current.Lines, &i)
	return Ident(i.Result)
}

// Emit appends one instruction and returns an ident operand for its result.
func (fb *FunctionBuilder) Emit(result string, op Opcode, suffix Type, args ...Operand) Operand {
	return fb.appendInstruction(Instruction{Result: result, Op: op, Suffix: suffix, Args: args})
}

// EmitInstruction appends a fully specified instruction (align clauses,
// fnsig calls, va_start's self-referential Sig token).
func (fb *FunctionBuilder) EmitInstruction(i Instruction) Operand {
	return fb.appendInstruction(i)
}

func (fb *FunctionBuilder) Location(file string, line, col int) {
	args := []Operand{StringLiteral(file), IntLiteral(int64(line))}
	if col > 0 {
		args = append(args, IntLiteral(int64(col)))
	}
	fb.appendInstruction(Instruction{Op: OpLoc, Args: args})
}

// Common conveniences (thin wrappers over Emit).
func (fb *FunctionBuilder) Add(n string, t Type, a, b Operand) Operand { return fb.Emit(n, OpAdd, t, a, b) }
func (fb *FunctionBuilder) Sub(n string, t Type, a, b Operand) Operand { return fb.Emit(n, OpSub, t, a, b) }
func (fb *FunctionBuilder) Mul(n string, t Type, a, b Operand) Operand { return fb.Emit(n, OpMul, t, a, b) }
func (fb *FunctionBuilder) Load(n string, t Type, p Operand) Operand   { return fb.Emit(n, OpLoad, t, p) }
func (fb *FunctionBuilder) Store(t Type, p, v Operand)                 { fb.Emit("", OpStore, t, p, v) }
func (fb *FunctionBuilder) Alloca(n string, size Operand, align int) Operand {
	return fb.EmitInstruction(Instruction{Result: n, Op: OpAlloca, Suffix: Ptr, Args: []Operand{size}, Align: align})
}

// AllocaValist declares a fresh valist slot (§4.4, §5.1) — the sole legal
// way to create one. No size/align operand: unlike alloca.ptr, its layout
// is target-defined and not something a frontend sizes.
func (fb *FunctionBuilder) AllocaValist(n string) Operand {
	return fb.EmitInstruction(Instruction{Result: n, Op: OpAlloca, Suffix: Valist})
}

func (fb *FunctionBuilder) FieldPointer(n string, p Operand, structName, field string) Operand {
	return fb.Emit(n, OpField, Ptr, p, Ident(structName), Ident(field))
}
func (fb *FunctionBuilder) IndexPointer(n string, p Operand, elem Type, idx Operand) Operand {
	return fb.Emit(n, OpIndex, Ptr, p, TypeOperand(elem), idx)
}
func (fb *FunctionBuilder) Call(n, callee string, args ...Operand) Operand {
	return fb.Emit(n, OpCall, nil, append([]Operand{Ident(callee)}, args...)...)
}
func (fb *FunctionBuilder) CallImported(n, importPath, callee string, args ...Operand) Operand {
	return fb.Emit(n, OpCall, nil, append([]Operand{QualifiedIdent(importPath, callee)}, args...)...)
}
func (fb *FunctionBuilder) CallIndirect(n, sig string, fp Operand, args ...Operand) Operand {
	return fb.EmitInstruction(Instruction{Result: n, Op: OpCall, Sig: sig, Args: append([]Operand{fp}, args...)})
}

// Syscall executes a hardware-level system call trap (§4.2).
func (fb *FunctionBuilder) Syscall(n string, ret Type, sysNo Operand, args ...Operand) Operand {
	return fb.Emit(n, OpSyscall, ret, append([]Operand{sysNo}, args...)...)
}

// VaStart initializes dst (a prior alloca.valist result) for reading
// arguments after lastNamed, the function's final declared parameter
// (§4.4). selfSig is decorative/self-referential — checked structurally
// against the enclosing function, not looked up in FunctionSignatures.
func (fb *FunctionBuilder) VaStart(selfSig, dst, lastNamed string) {
	fb.EmitInstruction(Instruction{Op: OpVaStart, Sig: selfSig, Args: []Operand{Ident(dst), Ident(lastNamed)}})
}

// VaArg reads the next variadic argument from src as type t (§4.4).
func (fb *FunctionBuilder) VaArg(n string, t Type, src Operand) Operand {
	return fb.Emit(n, OpVaArg, t, src)
}

// VaEnd closes src (§4.4); required before it's legally re-va_start-able
// or before a returning terminator.
func (fb *FunctionBuilder) VaEnd(src Operand) {
	fb.Emit("", OpVaEnd, nil, src)
}

// Terminators.
func (fb *FunctionBuilder) Branch(label string) { fb.current.Term = Branch{Label: label} }
func (fb *FunctionBuilder) BranchIf(c Operand, then, els string) {
	fb.current.Term = BranchIf{Cond: c, Then: then, Else: els}
}
func (fb *FunctionBuilder) Switch(v Operand, def string, cases ...SwitchCase) {
	fb.current.Term = Switch{Value: v, Default: def, Cases: cases}
}
func (fb *FunctionBuilder) Return(v ...Operand) {
	if len(v) == 0 {
		fb.current.Term = Return{}
		return
	}
	val := v[0]
	fb.current.Term = Return{Value: &val}
}
func (fb *FunctionBuilder) TailCall(callee string, args ...Operand) {
	fb.current.Term = TailCall{Callee: callee, Args: args}
}
func (fb *FunctionBuilder) TailCallIndirect(sig string, fp Operand, args ...Operand) {
	fb.current.Term = TailCall{Sig: sig, Args: append([]Operand{fp}, args...)}
}
func (fb *FunctionBuilder) Trap()        { fb.current.Term = Trap{} }
func (fb *FunctionBuilder) Unreachable() { fb.current.Term = Unreachable{} }