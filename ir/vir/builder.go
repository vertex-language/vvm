package vir

// Builder API. Mirrors the IR one-to-one — it constructs, it doesn't check;
// Verify checks (README). Nothing here validates ordering, names, or types.

func NewModule(name string) *Module { return &Module{Name: name} }

func (m *Module) SetTarget(arch, os, abi string, tiers ...string) *Module {
	m.Target = &Target{Arch: arch, OS: os, ABI: abi, Tiers: tiers}
	return m
}

func (m *Module) DeclareStruct(name string, fields ...Field) *Struct {
	s := &Struct{Name: name, Fields: fields}
	m.Structs = append(m.Structs, s)
	return s
}

func (m *Module) DeclareFnSig(name string, params []Type, variadic bool, ret Type) *FnSig {
	sig := &FnSig{Name: name, Params: params, Variadic: variadic, Ret: ret}
	m.FnSigs = append(m.FnSigs, sig)
	return sig
}

func (m *Module) DeclareConst(name string, t Type, value Operand) *Const {
	c := &Const{Name: name, Type: t, Value: value}
	m.Consts = append(m.Consts, c)
	return c
}

func (m *Module) DeclareGlobal(name string, t Type, init ConstInit) *Global {
	g := &Global{Name: name, Type: t, Init: init}
	m.Globals = append(m.Globals, g)
	return g
}

func (g *Global) Exported() *Global      { g.Export = true; return g }
func (g *Global) ThreadLocal() *Global   { g.TLS = true; return g }
func (g *Global) Aligned(n int) *Global  { g.Align = n; return g }

func (m *Module) DeclareLink(kind LinkKind, name string) *Link {
	l := &Link{Kind: kind, Name: name}
	m.Links = append(m.Links, l)
	return l
}

func (m *Module) DeclareExternGroup(dep string) *ExternGroup {
	g := &ExternGroup{Dep: dep}
	m.Externs = append(m.Externs, g)
	return g
}

func (g *ExternGroup) Fn(name string, params []Param, ret Type, attrs ...FnAttr) *ExternFn {
	f := &ExternFn{Name: name, Params: params, Ret: ret, Attrs: attrs}
	g.Fns = append(g.Fns, f)
	return f
}

func (f *ExternFn) SetVariadic() *ExternFn { f.Variadic = true; return f }

// FuncBuilder appends to the current block; Label opens a new one.
type FuncBuilder struct {
	Func *Func
	cur  *Block
}

func (m *Module) DeclareFn(name string, params []Param, ret Type, export bool, attrs ...FnAttr) *FuncBuilder {
	if ret == nil {
		ret = Void
	}
	f := &Func{Name: name, Params: params, Ret: ret, Export: export, Attrs: attrs}
	f.Entry = &Block{}
	m.Funcs = append(m.Funcs, f)
	return &FuncBuilder{Func: f, cur: f.Entry}
}

// Label closes nothing (Verify enforces termination) and opens a new block.
func (fb *FuncBuilder) Label(name string) {
	b := &Block{Label: name}
	fb.Func.Blocks = append(fb.Func.Blocks, b)
	fb.cur = b
}

// Emit appends one instruction and returns an ident operand for its result.
func (fb *FuncBuilder) Emit(result, op string, suffix Type, args ...Operand) Operand {
	fb.cur.Insts = append(fb.cur.Insts, Inst{Result: result, Op: op, Suffix: suffix, Args: args})
	return V(result)
}

// EmitInst appends a fully specified instruction (align clauses, fnsig calls).
func (fb *FuncBuilder) EmitInst(i Inst) Operand {
	fb.cur.Insts = append(fb.cur.Insts, i)
	return V(i.Result)
}

func (fb *FuncBuilder) Loc(file string, line, col int) {
	args := []Operand{Str(file), Int(int64(line))}
	if col > 0 {
		args = append(args, Int(int64(col)))
	}
	fb.cur.Insts = append(fb.cur.Insts, Inst{Op: "loc", Args: args})
}

// Common conveniences (thin wrappers over Emit).
func (fb *FuncBuilder) Add(n string, t Type, a, b Operand) Operand { return fb.Emit(n, "add", t, a, b) }
func (fb *FuncBuilder) Sub(n string, t Type, a, b Operand) Operand { return fb.Emit(n, "sub", t, a, b) }
func (fb *FuncBuilder) Mul(n string, t Type, a, b Operand) Operand { return fb.Emit(n, "mul", t, a, b) }
func (fb *FuncBuilder) Load(n string, t Type, p Operand) Operand   { return fb.Emit(n, "load", t, p) }
func (fb *FuncBuilder) Store(t Type, p, v Operand)                 { fb.Emit("", "store", t, p, v) }
func (fb *FuncBuilder) Alloca(n string, size Operand, align int) Operand {
	return fb.EmitInst(Inst{Result: n, Op: "alloca", Suffix: Ptr, Args: []Operand{size}, Align: align})
}
func (fb *FuncBuilder) FieldPtr(n string, p Operand, structName, field string) Operand {
	return fb.Emit(n, "field", Ptr, p, V(structName), V(field))
}
func (fb *FuncBuilder) IndexPtr(n string, p Operand, elem Type, idx Operand) Operand {
	return fb.Emit(n, "index", Ptr, p, Ty(elem), idx)
}
func (fb *FuncBuilder) Call(n, callee string, args ...Operand) Operand {
	return fb.Emit(n, "call", nil, append([]Operand{V(callee)}, args...)...)
}
func (fb *FuncBuilder) CallIndirect(n, sig string, fp Operand, args ...Operand) Operand {
	return fb.EmitInst(Inst{Result: n, Op: "call", Sig: sig, Args: append([]Operand{fp}, args...)})
}

// Terminators.
func (fb *FuncBuilder) Br(label string)            { fb.cur.Term = Br{Label: label} }
func (fb *FuncBuilder) BrIf(c Operand, t, e string) { fb.cur.Term = BrIf{Cond: c, Then: t, Else: e} }
func (fb *FuncBuilder) Switch(v Operand, def string, cases ...SwitchCase) {
	fb.cur.Term = Switch{Value: v, Default: def, Cases: cases}
}
func (fb *FuncBuilder) Return(v ...Operand) {
	if len(v) == 0 {
		fb.cur.Term = Return{}
		return
	}
	val := v[0]
	fb.cur.Term = Return{Value: &val}
}
func (fb *FuncBuilder) TailCall(callee string, args ...Operand) {
	fb.cur.Term = TailCall{Callee: callee, Args: args}
}
func (fb *FuncBuilder) TailCallIndirect(sig string, fp Operand, args ...Operand) {
	fb.cur.Term = TailCall{Sig: sig, Args: append([]Operand{fp}, args...)}
}
func (fb *FuncBuilder) Trap()        { fb.cur.Term = Trap{} }
func (fb *FuncBuilder) Unreachable() { fb.cur.Term = Unreachable{} }