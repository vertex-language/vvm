// builder.go
package gvir

// Builder API. Mirrors the IR one-to-one — it constructs, it doesn't check;
// ir/verify checks. Nothing here validates block ordering, alloca placement,
// merge annotations, uniformity, name binding or types. Op arguments are
// Opcode constants (opcode.go), not strings — a typo like "cltz" is a
// compile error, not something a verifier has to catch.

func NewModule(name string) *Module {
	return &Module{Version: LanguageVersion, Name: name}
}

func (m *Module) SetVersion(major, minor int) *Module {
	m.Version = Version{Major: major, Minor: minor}
	return m
}

// Backend constructors. ptx and msl take an optional single arch; amdgcn
// requires one or more (§3).
func PTX(arch ...string) Backend    { return Backend{Kind: BackendPTX, Archs: arch} }
func AMDGCN(archs ...string) Backend { return Backend{Kind: BackendAMDGCN, Archs: archs} }
func MSL(arch ...string) Backend    { return Backend{Kind: BackendMSL, Archs: arch} }

func (m *Module) SetTarget(backends ...Backend) *Module {
	m.Target = &Target{Backends: backends}
	return m
}

// SetFloatProfile declares the module-wide float profile (§11.6). The two
// flags are independent: asking for approx does not surrender IEEE
// arithmetic elsewhere.
func (m *Module) SetFloatProfile(contract, approx bool) *Module {
	m.Profile = FloatProfile{Contract: contract, Approx: approx}
	return m
}

func (m *Module) DeclareStruct(name string, fields ...Field) *Struct {
	s := &Struct{Name: name, Fields: fields}
	m.Structs = append(m.Structs, s)
	return s
}

func (m *Module) DeclareConst(name string, t Type, init ConstInit) *Const {
	c := &Const{Name: name, Type: t, Init: init}
	m.Constants = append(m.Constants, c)
	return c
}

// ---------------------------------------------------------------------------
// Body builder — shared by funcs and kernels.
// ---------------------------------------------------------------------------

// BodyBuilder appends to the current block; Label opens a new one.
type BodyBuilder struct {
	body    *Body
	current *Block
}

// Current returns the block instructions are currently appended to.
func (b *BodyBuilder) Current() *Block { return b.current }

// Label closes nothing (ir/verify enforces termination) and opens a new block.
func (b *BodyBuilder) Label(name string) *Block {
	blk := &Block{Label: name}
	b.body.Blocks = append(b.body.Blocks, blk)
	b.current = blk
	return blk
}

// Merge annotates the current block as a selection header reconverging at
// exit (§7.2).
func (b *BodyBuilder) Merge(exit string) {
	b.current.Merge = &Merge{Kind: MergeSelection, Merge: exit}
}

// LoopMerge annotates the current block as a cycle entry (§7.2).
func (b *BodyBuilder) LoopMerge(exit, cont string) {
	b.current.Merge = &Merge{Kind: MergeLoop, Merge: exit, Continue: cont}
}

func (b *BodyBuilder) appendInstruction(i Instruction) Operand {
	b.current.Lines = append(b.current.Lines, &i)
	return Ident(i.Result)
}

// Emit appends one instruction and returns an ident operand for its result.
func (b *BodyBuilder) Emit(result string, op Opcode, suffix Type, args ...Operand) Operand {
	return b.appendInstruction(Instruction{Result: result, Op: op, Suffix: suffix, Args: args})
}

// EmitInstruction appends a fully specified instruction (align clauses,
// dimension suffixes, barrier execution scopes).
func (b *BodyBuilder) EmitInstruction(i Instruction) Operand {
	return b.appendInstruction(i)
}

func (b *BodyBuilder) Location(file string, line, col int) {
	args := []Operand{StringLiteral(file), IntLiteral(int64(line))}
	if col > 0 {
		args = append(args, IntLiteral(int64(col)))
	}
	b.appendInstruction(Instruction{Op: OpLoc, Args: args})
}

// --- Memory ---------------------------------------------------------------

// Alloca declares per-thread scratch (§8.1). Legal only in the entry block
// before any other instruction; the type must be statically sized.
func (b *BodyBuilder) Alloca(name string, t Type, align int) Operand {
	return b.EmitInstruction(Instruction{Result: name, Op: OpAlloca, Suffix: t, Align: align})
}

func (b *BodyBuilder) Load(name string, t Type, p Operand) Operand {
	return b.Emit(name, OpLoad, t, p)
}

// Store takes the destination first (§8.3).
func (b *BodyBuilder) Store(t Type, p, v Operand) { b.Emit("", OpStore, t, p, v) }

func (b *BodyBuilder) LoadAligned(name string, t Type, p Operand, align int) Operand {
	return b.EmitInstruction(Instruction{Result: name, Op: OpLoad, Suffix: t, Args: []Operand{p}, Align: align})
}

func (b *BodyBuilder) StoreAligned(t Type, p, v Operand, align int) {
	b.EmitInstruction(Instruction{Op: OpStore, Suffix: t, Args: []Operand{p, v}, Align: align})
}

// IndexPointer is byte pointer arithmetic keeping p's address space (§8.3).
func (b *BodyBuilder) IndexPointer(name string, p, byteOffset Operand) Operand {
	return b.Emit(name, OpIndex, PtrWord, p, byteOffset)
}

// FieldPointer addresses field k of the pointed-to struct. k is a literal
// index, not a name — Struct.FieldIndex resolves one from the other.
func (b *BodyBuilder) FieldPointer(name string, p Operand, k int) Operand {
	return b.Emit(name, OpField, PtrWord, p, IntLiteral(int64(k)))
}

func (b *BodyBuilder) Memcopy(dst, src, length Operand) {
	b.Emit("", OpMemcopy, nil, dst, src, length)
}
func (b *BodyBuilder) Memmove(dst, src, length Operand) {
	b.Emit("", OpMemmove, nil, dst, src, length)
}
func (b *BodyBuilder) Memset(dst, byteVal, length Operand) {
	b.Emit("", OpMemset, nil, dst, byteVal, length)
}

// --- Arithmetic conveniences (thin wrappers over Emit) ---------------------

func (b *BodyBuilder) Add(n string, t Type, x, y Operand) Operand { return b.Emit(n, OpAdd, t, x, y) }
func (b *BodyBuilder) Sub(n string, t Type, x, y Operand) Operand { return b.Emit(n, OpSub, t, x, y) }
func (b *BodyBuilder) Mul(n string, t Type, x, y Operand) Operand { return b.Emit(n, OpMul, t, x, y) }
func (b *BodyBuilder) Compare(n string, op Opcode, t Type, x, y Operand) Operand {
	return b.Emit(n, op, t, x, y)
}
func (b *BodyBuilder) Select(n string, t Type, cond, a, c Operand) Operand {
	return b.Emit(n, OpSelect, t, cond, a, c)
}
func (b *BodyBuilder) Convert(n string, op Opcode, dst Type, v Operand) Operand {
	return b.Emit(n, op, dst, v)
}

// --- Vectors --------------------------------------------------------------

// Extract reads element k of a vector; vecType is the vector type and the
// result is its element type.
func (b *BodyBuilder) Extract(n string, vecType Type, v Operand, k int) Operand {
	return b.Emit(n, OpExtract, vecType, v, IntLiteral(int64(k)))
}
func (b *BodyBuilder) Insert(n string, vecType Type, v Operand, k int, x Operand) Operand {
	return b.Emit(n, OpInsert, vecType, v, IntLiteral(int64(k)), x)
}
func (b *BodyBuilder) Splat(n string, vecType Type, x Operand) Operand {
	return b.Emit(n, OpSplat, vecType, x)
}
func (b *BodyBuilder) Swizzle(n string, vecType Type, a, c Operand, mask ...int64) Operand {
	args := []Operand{a, c}
	for _, k := range mask {
		args = append(args, IntLiteral(k))
	}
	return b.Emit(n, OpSwizzle, vecType, args...)
}

// --- Synchronization ------------------------------------------------------

// Barrier emits barrier.<exec>[, <mem>] (§10.1). Omitting mem defaults the
// memory scope to the execution scope.
func (b *BodyBuilder) Barrier(exec ExecScope, mem ...Scope) {
	i := Instruction{Op: OpBarrier, Exec: exec}
	if len(mem) > 0 {
		i.Args = []Operand{ScopeOperand(mem[0])}
	}
	b.EmitInstruction(i)
}

func (b *BodyBuilder) Fence(scope Scope, ordering Ordering) {
	b.Emit("", OpFence, nil, ScopeOperand(scope), OrderingOperand(ordering))
}

// Atomic emits a scoped RMW or atomic load/store. Atomics are relaxed and
// take no ordering operand (§10.2).
func (b *BodyBuilder) AtomicLoad(n string, t Type, p Operand, s Scope) Operand {
	return b.Emit(n, OpAtomicLoad, t, p, ScopeOperand(s))
}
func (b *BodyBuilder) AtomicStore(t Type, p, v Operand, s Scope) {
	b.Emit("", OpAtomicStore, t, p, v, ScopeOperand(s))
}
func (b *BodyBuilder) AtomicRMW(n string, op Opcode, t Type, p, v Operand, s Scope) Operand {
	return b.Emit(n, op, t, p, v, ScopeOperand(s))
}

// Cmpxchg yields the old value at p, not a success flag (§10.2).
func (b *BodyBuilder) Cmpxchg(n string, t Type, p, expected, desired Operand, s Scope) Operand {
	return b.Emit(n, OpCmpxchg, t, p, expected, desired, ScopeOperand(s))
}

// --- Collectives and builtins ---------------------------------------------

func (b *BodyBuilder) Shuffle(n string, op Opcode, t Type, v, lane Operand) Operand {
	return b.Emit(n, op, t, v, lane)
}
func (b *BodyBuilder) BroadcastFirst(n string, t Type, v Operand) Operand {
	return b.Emit(n, OpBroadcastFirst, t, v)
}
func (b *BodyBuilder) Ballot(n string, cond Operand) Operand {
	return b.Emit(n, OpBallot, nil, cond)
}
func (b *BodyBuilder) SubReduce(n string, op Opcode, t Type, v Operand) Operand {
	return b.Emit(n, op, t, v)
}

// Builtin emits an execution builtin (§9). Pass DimNone for the unsuffixed,
// linearized form.
func (b *BodyBuilder) Builtin(n string, op Opcode, dim Dim) Operand {
	return b.EmitInstruction(Instruction{Result: n, Op: op, Dim: dim})
}

// --- Calls ----------------------------------------------------------------

// Call emits a direct call to a previously defined func (§6.4). There is no
// indirect form: function pointers do not exist in this IR.
func (b *BodyBuilder) Call(n, callee string, args ...Operand) Operand {
	return b.Emit(n, OpCall, nil, append([]Operand{Ident(callee)}, args...)...)
}

// --- Terminators ----------------------------------------------------------

func (b *BodyBuilder) Br(label string) { b.current.Term = Br{Label: label} }
func (b *BodyBuilder) BrIf(cond Operand, then, els string) {
	b.current.Term = BrIf{Cond: cond, Then: then, Else: els}
}
func (b *BodyBuilder) Switch(v Operand, def string, cases ...SwitchCase) {
	b.current.Term = Switch{Value: v, Default: def, Cases: cases}
}

// Return with no operand is the kernel and void-func form (§6.1).
func (b *BodyBuilder) Return(v ...Operand) {
	if len(v) == 0 {
		b.current.Term = Return{}
		return
	}
	val := v[0]
	b.current.Term = Return{Value: &val}
}
func (b *BodyBuilder) Unreachable() { b.current.Term = Unreachable{} }

// ---------------------------------------------------------------------------
// Funcs and kernels
// ---------------------------------------------------------------------------

type FuncBuilder struct {
	Func *Func
	BodyBuilder
}

func (m *Module) DeclareFunc(name string, params []Param, ret Type) *FuncBuilder {
	if ret == nil {
		ret = Void
	}
	f := &Func{Name: name, Params: params, Ret: ret}
	f.Entry = &Block{}
	m.Funcs = append(m.Funcs, f)
	return &FuncBuilder{Func: f, BodyBuilder: BodyBuilder{body: &f.Body, current: f.Entry}}
}

// Readonly asserts the function writes through no pointer reachable from its
// arguments (§6.4). Violating it is UB (§12.8).
func (fb *FuncBuilder) Readonly() *FuncBuilder {
	fb.Func.Readonly = true
	return fb
}

type KernelBuilder struct {
	Kernel *Kernel
	BodyBuilder
}

func (m *Module) DeclareKernel(name string, params ...Param) *KernelBuilder {
	k := &Kernel{Name: name, Params: params}
	k.Entry = &Block{}
	m.Kernels = append(m.Kernels, k)
	return &KernelBuilder{Kernel: k, BodyBuilder: BodyBuilder{body: &k.Body, current: k.Entry}}
}

// GroupSize declares the exact required group shape (§6.1). It is a
// host-checked contract, not UB: the generated launcher rejects a mismatch.
func (kb *KernelBuilder) GroupSize(x, y, z int) *KernelBuilder {
	kb.Kernel.GroupSize = &GroupShape{X: x, Y: y, Z: z}
	return kb
}

func (kb *KernelBuilder) MaxGroupSize(n int) *KernelBuilder {
	kb.Kernel.MaxGroupSize = n
	return kb
}

// SubgroupSize requests a specific subgroup width (§9.2). Gated: kernels
// carrying it are excluded from every msl artifact (§4.3).
func (kb *KernelBuilder) SubgroupSize(n int) *KernelBuilder {
	kb.Kernel.SubgroupSize = n
	return kb
}

// DynamicGroup declares the one launch-sized group allocation (§6.1). The
// name binds to ptr[group] in the entry block.
func (kb *KernelBuilder) DynamicGroup(name string, align int) *KernelBuilder {
	kb.Kernel.DynamicGroup = &DynamicGroup{Name: name, Align: align}
	return kb
}

// Group declares one statically sized group allocation (§8.2).
// Zero-initialization is not guaranteed.
func (kb *KernelBuilder) Group(name string, t Type, align int) *GroupVar {
	g := &GroupVar{Name: name, Type: t, Align: align}
	kb.Kernel.Groups = append(kb.Kernel.Groups, g)
	return g
}