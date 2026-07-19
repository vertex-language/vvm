// builder.go
package vir

// Builder API. Mirrors the IR one-to-one — it constructs, it doesn't check;
// Verify checks (README). Nothing here validates ordering, names, or types.

func NewModule(name string) *Module { return &Module{Name: name} }

func (m *Module) SetTarget(arch, os, abi string, tiers ...string) *Module {
	m.Target = &Target{Arch: arch, OS: os, ABI: abi, Tiers: tiers}
	return m
}

// SetAsmDialect declares the module-wide asmdialect (§1.2 rule 11). Required
// if the module contains any asm blocks; Verify checks that the dialect is
// valid for the module's target architecture.
func (m *Module) SetAsmDialect(d AsmDialect) *Module {
	m.AsmDialect = &d
	return m
}

func (m *Module) DeclareStruct(name string, fields ...Field) *Struct {
	s := &Struct{Name: name, Fields: fields}
	m.Structs = append(m.Structs, s)
	return s
}

func (m *Module) DeclareFunctionSignature(name string, params []Type, variadic bool, ret Type) *FunctionSignature {
	sig := &FunctionSignature{Name: name, Params: params, Variadic: variadic, Ret: ret}
	m.FunctionSignatures = append(m.FunctionSignatures, sig)
	return sig
}

func (m *Module) DeclareConstant(name string, t Type, value Operand) *Constant {
	c := &Constant{Name: name, Type: t, Value: value}
	m.Constants = append(m.Constants, c)
	return c
}

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

// Label closes nothing (Verify enforces termination) and opens a new block.
func (fb *FunctionBuilder) Label(name string) {
	b := &Block{Label: name}
	fb.Function.Blocks = append(fb.Function.Blocks, b)
	fb.current = b
}

func (fb *FunctionBuilder) appendInstruction(i Instruction) Operand {
	fb.current.Lines = append(fb.current.Lines, BodyLine{Instruction: &i})
	return Ident(i.Result)
}

// Emit appends one instruction and returns an ident operand for its result.
func (fb *FunctionBuilder) Emit(result, op string, suffix Type, args ...Operand) Operand {
	return fb.appendInstruction(Instruction{Result: result, Op: op, Suffix: suffix, Args: args})
}

// EmitInstruction appends a fully specified instruction (align clauses,
// fnsig calls).
func (fb *FunctionBuilder) EmitInstruction(i Instruction) Operand {
	return fb.appendInstruction(i)
}

func (fb *FunctionBuilder) Location(file string, line, col int) {
	args := []Operand{StringLiteral(file), IntLiteral(int64(line))}
	if col > 0 {
		args = append(args, IntLiteral(int64(col)))
	}
	fb.appendInstruction(Instruction{Op: "loc", Args: args})
}

// Common conveniences (thin wrappers over Emit).
func (fb *FunctionBuilder) Add(n string, t Type, a, b Operand) Operand { return fb.Emit(n, "add", t, a, b) }
func (fb *FunctionBuilder) Sub(n string, t Type, a, b Operand) Operand { return fb.Emit(n, "sub", t, a, b) }
func (fb *FunctionBuilder) Mul(n string, t Type, a, b Operand) Operand { return fb.Emit(n, "mul", t, a, b) }
func (fb *FunctionBuilder) Load(n string, t Type, p Operand) Operand   { return fb.Emit(n, "load", t, p) }
func (fb *FunctionBuilder) Store(t Type, p, v Operand)                 { fb.Emit("", "store", t, p, v) }
func (fb *FunctionBuilder) Alloca(n string, size Operand, align int) Operand {
	return fb.EmitInstruction(Instruction{Result: n, Op: "alloca", Suffix: Ptr, Args: []Operand{size}, Align: align})
}
func (fb *FunctionBuilder) FieldPointer(n string, p Operand, structName, field string) Operand {
	return fb.Emit(n, "field", Ptr, p, Ident(structName), Ident(field))
}
func (fb *FunctionBuilder) IndexPointer(n string, p Operand, elem Type, idx Operand) Operand {
	return fb.Emit(n, "index", Ptr, p, TypeOperand(elem), idx)
}
func (fb *FunctionBuilder) Call(n, callee string, args ...Operand) Operand {
	return fb.Emit(n, "call", nil, append([]Operand{Ident(callee)}, args...)...)
}
func (fb *FunctionBuilder) CallIndirect(n, sig string, fp Operand, args ...Operand) Operand {
	return fb.EmitInstruction(Instruction{Result: n, Op: "call", Sig: sig, Args: append([]Operand{fp}, args...)})
}

// Syscall executes a hardware-level system call trap (§4 Calls & Control).
// sysNo is the syscall number; up to six scalar args follow.
func (fb *FunctionBuilder) Syscall(n string, ret Type, sysNo Operand, args ...Operand) Operand {
	return fb.Emit(n, "syscall", ret, append([]Operand{sysNo}, args...)...)
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

// ---------------------------------------------------------------------------
// Inline assembly builder (§4).
// ---------------------------------------------------------------------------

// AsmBuilder accumulates one asm block's bindings and code before it is
// appended to the enclosing function's current basic block via End. The
// dialect governing the block's `code:` syntax comes from the module-level
// AsmDialect declaration (§1.2 rule 11), not from the block itself.
type AsmBuilder struct {
	owner *FunctionBuilder
	block AsmBlock
}

// BeginAsm starts a new inline-assembly block. Its syntax is governed by
// the enclosing module's AsmDialect (set via Module.SetAsmDialect).
func (fb *FunctionBuilder) BeginAsm() *AsmBuilder {
	return &AsmBuilder{owner: fb, block: AsmBlock{}}
}

func (ab *AsmBuilder) In(register, ident string) *AsmBuilder {
	ab.block.Bindings = append(ab.block.Bindings, AsmBinding{Kind: BindingIn, Register: register, Ident: ident})
	return ab
}

func (ab *AsmBuilder) Out(register, ident string) *AsmBuilder {
	ab.block.Bindings = append(ab.block.Bindings, AsmBinding{Kind: BindingOut, Register: register, Ident: ident})
	return ab
}

func (ab *AsmBuilder) Clobber(registers ...string) *AsmBuilder {
	ab.block.Bindings = append(ab.block.Bindings, AsmBinding{Kind: BindingClobber, Registers: registers})
	return ab
}

func (ab *AsmBuilder) Code(lines ...AsmCodeLine) *AsmBuilder {
	ab.block.Code = append(ab.block.Code, lines...)
	return ab
}

// End closes the asm block and appends it to the enclosing function's
// current basic block, in place, as an ordinary body-line.
func (ab *AsmBuilder) End() {
	finished := ab.block
	ab.owner.current.Lines = append(ab.owner.current.Lines, BodyLine{Asm: &finished})
}