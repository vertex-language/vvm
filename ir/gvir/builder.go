// builder.go
package gvir

// Builder API. Mirrors the IR one-to-one — it constructs, it doesn't
// check; ir/verify checks. Nothing here validates ordering, names, types,
// merge annotations, or the Join Convention. Op arguments are Opcode
// constants (opcode.go), not strings.
//
// Two things the *type system* does enforce here, because they are
// structural rather than semantic: a kernel's Return takes no operand
// (§6.1), and group/dynamic_group declarations exist only on
// KernelBuilder, since a func may contain neither (§6.4).

func NewModule(name string) *Module {
	return &Module{Version: SpecVersion, Name: name}
}

// AddTarget appends one backend to the target-decl (§3). Omit archs for
// ptx or msl to take the default; amdgcn requires them.
func (m *Module) AddTarget(b BackendName, archs ...string) *Module {
	m.Targets = append(m.Targets, Backend{Name: b, Archs: archs})
	return m
}

// SetFloatProfile declares the module-wide float profile (§11.6).
func (m *Module) SetFloatProfile(p FloatProfile) *Module {
	m.FloatProfile = p
	return m
}

func (m *Module) DeclareStruct(name string, fields ...Field) *Struct {
	s := &Struct{Name: name, Fields: fields}
	m.Structs = append(m.Structs, s)
	return s
}

func (m *Module) DeclareConstant(name string, t Type, init ConstInit) *Constant {
	c := &Constant{Name: name, Type: t, Init: init}
	m.Constants = append(m.Constants, c)
	return c
}

// ---------------------------------------------------------------------------
// Body builder, shared by funcs and kernels.
// ---------------------------------------------------------------------------

// body appends to the current block; Label opens a new one.
type body struct {
	blocks  *[]*Block
	current *Block
}

// Label closes nothing (ir/verify enforces termination) and opens a new
// block.
func (b *body) Label(name string) {
	blk := &Block{Label: name}
	*b.blocks = append(*b.blocks, blk)
	b.current = blk
}

// Merge annotates the current block with `merge L` (§7.2).
func (b *body) Merge(label string) { b.current.Merge = SelectionMerge{Label: label} }

// LoopMerge annotates the current block as a loop header (§7.2).
func (b *body) LoopMerge(exit, cont string) {
	b.current.Merge = LoopMerge{Exit: exit, Continue: cont}
}

func (b *body) appendInstruction(i Instruction) Operand {
	b.current.Lines = append(b.current.Lines, &i)
	return Ident(i.Result)
}

// Emit appends one instruction and returns an ident operand for its
// result. Under the Join Convention a repeated result name is an update,
// not a redefinition (§7.3).
func (b *body) Emit(result string, op Opcode, suffix Type, args ...Operand) Operand {
	return b.appendInstruction(Instruction{Result: result, Op: op, Suffix: suffix, Args: args})
}

// EmitInstruction appends a fully specified instruction — align clauses,
// builtin dim suffixes, barrier execution scopes.
func (b *body) EmitInstruction(i Instruction) Operand { return b.appendInstruction(i) }

func (b *body) Location(file string, line, col int) {
	args := []Operand{StringLiteral(file), IntLiteral(int64(line))}
	if col > 0 {
		args = append(args, IntLiteral(int64(col)))
	}
	b.appendInstruction(Instruction{Op: OpLoc, Args: args})
}

// --- memory (§8) ---

// Alloca declares private storage. Legal only in the entry block before
// any other instruction; the suffix is the allocated type and the result
// is ptr[private] (§8.1).
func (b *body) Alloca(name string, t Type, align int) Operand {
	return b.EmitInstruction(Instruction{Result: name, Op: OpAlloca, Suffix: t, Align: align})
}

func (b *body) Load(n string, t Type, p Operand) Operand { return b.Emit(n, OpLoad, t, p) }

// Store takes its destination first (§8.3).
func (b *body) Store(t Type, dst, v Operand) { b.Emit("", OpStore, t, dst, v) }

func (b *body) LoadAligned(n string, t Type, p Operand, align int) Operand {
	return b.EmitInstruction(Instruction{Result: n, Op: OpLoad, Suffix: t, Args: []Operand{p}, Align: align})
}
func (b *body) StoreAligned(t Type, dst, v Operand, align int) {
	b.EmitInstruction(Instruction{Op: OpStore, Suffix: t, Args: []Operand{dst, v}, Align: align})
}

// IndexPointer is byte pointer arithmetic; offset is i64 and the result
// keeps p's address space (§8.3).
func (b *body) IndexPointer(n string, p, offset Operand) Operand {
	return b.Emit(n, OpIndex, AnyPtr, p, offset)
}

// FieldPointer addresses field k of the pointed-to struct by literal index.
func (b *body) FieldPointer(n string, p Operand, k int) Operand {
	return b.Emit(n, OpField, AnyPtr, p, IntLiteral(int64(k)))
}

func (b *body) Memcopy(dst, src, length Operand) { b.Emit("", OpMemcopy, nil, dst, src, length) }
func (b *body) Memmove(dst, src, length Operand) { b.Emit("", OpMemmove, nil, dst, src, length) }
func (b *body) Memset(dst, byteVal, length Operand) {
	b.Emit("", OpMemset, nil, dst, byteVal, length)
}

// --- arithmetic conveniences (thin wrappers over Emit) ---

func (b *body) Add(n string, t Type, x, y Operand) Operand { return b.Emit(n, OpAdd, t, x, y) }
func (b *body) Sub(n string, t Type, x, y Operand) Operand { return b.Emit(n, OpSub, t, x, y) }
func (b *body) Mul(n string, t Type, x, y Operand) Operand { return b.Emit(n, OpMul, t, x, y) }
func (b *body) Fma(n string, t Type, x, y, z Operand) Operand {
	return b.Emit(n, OpFma, t, x, y, z)
}
func (b *body) Select(n string, t Type, c, x, y Operand) Operand {
	return b.Emit(n, OpSelect, t, c, x, y)
}

// --- builtins (§9) ---

// Builtin reads an execution builtin. Pass DimNone for the unsuffixed,
// linearized form.
func (b *body) Builtin(n string, op Opcode, dim Dim) Operand {
	return b.EmitInstruction(Instruction{Result: n, Op: op, Dim: dim})
}

// --- synchronization (§10) ---

// Barrier emits `barrier.<exec>` or `barrier.<exec>, <mem>`. Pass
// ScopeNone's zero value via mem == "" to omit the memory scope, which
// defaults it to the execution scope.
func (b *body) Barrier(exec ExecScope, mem Scope) {
	i := Instruction{Op: OpBarrier, Exec: exec}
	if mem != "" {
		i.Args = []Operand{ScopeOperand(mem)}
	}
	b.EmitInstruction(i)
}

// Atomic emits a read-modify-write with its trailing scope operand. There
// is no ordering argument — atomics are relaxed in v1 (§10.2).
func (b *body) Atomic(n string, op Opcode, t Type, p, v Operand, s Scope) Operand {
	return b.Emit(n, op, t, p, v, ScopeOperand(s))
}

func (b *body) AtomicLoad(n string, t Type, p Operand, s Scope) Operand {
	return b.Emit(n, OpAtomicLoad, t, p, ScopeOperand(s))
}
func (b *body) AtomicStore(t Type, p, v Operand, s Scope) {
	b.Emit("", OpAtomicStore, t, p, v, ScopeOperand(s))
}

// Cmpxchg yields the OLD value at p, not a success flag — test it with
// eq.<T> against expected (§10.2).
func (b *body) Cmpxchg(n string, t Type, p, expected, desired Operand, s Scope) Operand {
	return b.Emit(n, OpCmpxchg, t, p, expected, desired, ScopeOperand(s))
}

func (b *body) Fence(s Scope, o Ordering) {
	b.Emit("", OpFence, nil, ScopeOperand(s), OrderingOperand(o))
}

// --- calls (§11.7) ---

// Call is direct only, to a previously defined func.
func (b *body) Call(n, callee string, args ...Operand) Operand {
	return b.Emit(n, OpCall, nil, append([]Operand{Ident(callee)}, args...)...)
}

// --- terminators (§2) ---

func (b *body) Br(label string) { b.current.Term = Br{Label: label} }
func (b *body) BrIf(c Operand, then, els string) {
	b.current.Term = BrIf{Cond: c, Then: then, Else: els}
}
func (b *body) Switch(v Operand, def string, cases ...SwitchCase) {
	b.current.Term = Switch{Value: v, Default: def, Cases: cases}
}
func (b *body) Unreachable() { b.current.Term = Unreachable{} }

func (b *body) ret(v *Operand) { b.current.Term = Return{Value: v} }

// ---------------------------------------------------------------------------
// Funcs.
// ---------------------------------------------------------------------------

type FuncBuilder struct {
	Func *Func
	body
}

func (m *Module) DeclareFunc(name string, params []Param, ret Type, attrs ...FuncAttr) *FuncBuilder {
	if ret == nil {
		ret = Void
	}
	f := &Func{Name: name, Params: params, Ret: ret, Attrs: attrs}
	f.Entry = &Block{}
	m.Funcs = append(m.Funcs, f)
	fb := &FuncBuilder{Func: f}
	fb.blocks = &f.Blocks
	fb.current = f.Entry
	return fb
}

// Return terminates with an optional value; omit it for a void func.
func (fb *FuncBuilder) Return(v ...Operand) {
	if len(v) == 0 {
		fb.ret(nil)
		return
	}
	val := v[0]
	fb.ret(&val)
}

// ---------------------------------------------------------------------------
// Kernels.
// ---------------------------------------------------------------------------

type KernelBuilder struct {
	Kernel *Kernel
	body
}

func (m *Module) DeclareKernel(name string, params ...Param) *KernelBuilder {
	k := &Kernel{Name: name, Params: params}
	k.Entry = &Block{}
	m.Kernels = append(m.Kernels, k)
	kb := &KernelBuilder{Kernel: k}
	kb.blocks = &k.Blocks
	kb.current = k.Entry
	return kb
}

// GroupSize sets the exact required group shape (§6.1). Normative.
func (kb *KernelBuilder) GroupSize(x, y, z int) *KernelBuilder {
	kb.Kernel.GroupSize = &GroupShape{X: x, Y: y, Z: z}
	return kb
}

func (kb *KernelBuilder) MaxGroupSize(n int) *KernelBuilder {
	kb.Kernel.MaxGroupSize = n
	return kb
}

// MinGroupsPerUnit is advisory and never affects semantics (§6.1).
func (kb *KernelBuilder) MinGroupsPerUnit(n int) *KernelBuilder {
	kb.Kernel.MinGroupsPerUnit = n
	return kb
}

// SubgroupSize requests a specific subgroup width. Gated: a kernel using
// it is excluded from every msl artifact (§4.3, §9.2).
func (kb *KernelBuilder) SubgroupSize(n int) *KernelBuilder {
	kb.Kernel.SubgroupSize = n
	return kb
}

// DynamicGroup declares the kernel's one launch-sized allocation and
// returns the ptr[group] it binds in the entry block (§6.1, §8.2).
func (kb *KernelBuilder) DynamicGroup(name string, align int) Operand {
	kb.Kernel.Dynamic = &DynamicGroup{Name: name, Align: align}
	return Ident(name)
}

// Group declares statically sized group memory. Zero-initialization is not
// guaranteed (§8.2). The returned operand names its address as ptr[group].
func (kb *KernelBuilder) Group(name string, t Type, align int) Operand {
	kb.Kernel.Groups = append(kb.Kernel.Groups, &GroupDecl{Name: name, Type: t, Align: align})
	return Ident(name)
}

// Return terminates a kernel. It takes no operand: kernels implicitly
// return void (§6.1).
func (kb *KernelBuilder) Return() { kb.ret(nil) }