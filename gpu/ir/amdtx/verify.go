package amdtx

import "strings"

// Severity distinguishes normative errors from warnings. A conforming
// verifier emits warnings but does not reject the module (§0.1).
type Severity uint8

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// Diag is one finding. Rule carries the normative rule number so a failure
// points straight at the paragraph it violates.
type Diag struct {
	Severity Severity
	Rule     string // "V9", "W1", or "" for structural findings
	Where    string // "saxpy[12]", "module", "global lut"
	Msg      string
}

func (d Diag) String() string {
	r := ""
	if d.Rule != "" {
		r = " " + d.Rule
	}
	return d.Where + ": " + d.Severity.String() + r + ": " + d.Msg
}

// Verify checks a module against the normative rules of §16. It never
// mutates the module and never stops at the first finding (P7).
//
// Out of scope, by design: V20 (displacement ranges) and V25 (pinned
// encodings) are checked at lowering against per-target tables; V32 is the
// printer's obligation; V36 is structural, since hidden arguments are never
// represented here.
func Verify(m *Module) []Diag {
	v := &verifier{m: m}
	v.module()
	return v.ds
}

type verifier struct {
	m  *Module
	ds []Diag
}

func (v *verifier) err(rule, where, msg string) {
	v.ds = append(v.ds, Diag{Error, rule, where, msg})
}
func (v *verifier) warn(rule, where, msg string) {
	v.ds = append(v.ds, Diag{Warning, rule, where, msg})
}

// ---- Module ---------------------------------------------------------------

func (v *verifier) module() {
	m := v.m
	if m.Version.IsZero() {
		v.err("V1", "module", ".amdtx version is required")
	}
	if !m.Target.IsValid() {
		v.err("V1", "module", ".target is required and must name a known processor")
	}
	if !m.Wave.IsValid() {
		v.err("V1", "module", ".wave must be 32 or 64")
	}
	if m.Wave == Wave32 && m.Target.IsValid() && !m.Target.SupportsWave32() {
		v.err("V5", "module", ".wave 32 requires GFX10 or later")
	}

	seen := map[string]bool{}
	dup := func(where, name string) {
		if seen[name] {
			v.err("", where, "duplicate module symbol "+name)
		}
		seen[name] = true
	}

	files := map[*File]bool{}
	for _, f := range m.Files() {
		files[f] = true
	}

	for _, d := range m.decls {
		switch x := d.(type) {
		case *Object:
			dup("global "+x.Name, x.Name)
			v.object(x)
		case *Kernel:
			dup(x.Name, x.Name)
			v.kernel(x, files)
		case *Func:
			dup(x.Name, x.Name)
			v.function(x, files)
		}
	}
	v.callGraph()
}

func (v *verifier) object(o *Object) {
	where := "global " + o.Name
	if !o.Width.IsValid() {
		v.err("V13", where, "declared width "+o.Width.String()+" is not a legal tuple size")
	}
	if o.Space != Global && o.Space != Shared {
		v.err("", where, "module objects must be .global or .shared")
	}
	if o.Align != 0 && o.Align&(o.Align-1) != 0 {
		v.err("", where, ".align must be a power of two")
	}
	if o.Len != 0 && len(o.Init) != 0 && len(o.Init) != o.Len {
		v.err("V41", where, "initializer has "+itoa(len(o.Init))+
			" elements, array is declared with "+itoa(o.Len))
	}
}

// callGraph rejects recursion, which is what guarantees inlining terminates
// (V4).
func (v *verifier) callGraph() {
	edges := map[*Func][]*Func{}
	for _, f := range v.m.Funcs() {
		fn := f
		WalkBody(f.Body, func(in *Instr) bool {
			if in.Op == OpCall && len(in.Src) > 0 {
				if callee, ok := in.Src[0].(*Func); ok {
					edges[fn] = append(edges[fn], callee)
				}
			}
			return true
		})
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := map[*Func]int{}
	var walk func(*Func)
	walk = func(f *Func) {
		state[f] = gray
		for _, c := range edges[f] {
			switch state[c] {
			case gray:
				v.err("V4", f.Name, "recursive call to "+c.Name)
			case white:
				walk(c)
			}
		}
		state[f] = black
	}
	for _, f := range v.m.Funcs() {
		if state[f] == white {
			walk(f)
		}
	}
}

// ---- Bodies ---------------------------------------------------------------

func (v *verifier) kernel(k *Kernel, files map[*File]bool) {
	v.launch(k)
	v.kernarg(k)
	v.body(k.Name, k.Body, true, files)
}

func (v *verifier) function(f *Func, files map[*File]bool) {
	v.body(f.Name, f.Body, false, files)
}

func (v *verifier) launch(k *Kernel) {
	l, where := k.Launch, k.Name
	if l.KernargAlign != 0 {
		if l.KernargAlign < 4 || l.KernargAlign&(l.KernargAlign-1) != 0 {
			v.err("V31", where, ".kernarg_align must be a power of two of at least 4")
		}
	}
	if l.MaxFlatWorkgroupSize != 0 && (l.MaxFlatWorkgroupSize < 1 || l.MaxFlatWorkgroupSize > 1024) {
		v.err("V31", where, ".max_flat_workgroup_size must lie in [1, 1024]")
	}
	if !l.ReqdWorkgroupSize.IsZero() && l.MaxFlatWorkgroupSize != 0 &&
		l.ReqdWorkgroupSize.Product() > l.MaxFlatWorkgroupSize {
		v.err("V31", where, ".reqd_workgroup_size product exceeds .max_flat_workgroup_size")
	}
	if l.WavesPerEU != [2]int{} {
		lo, hi := l.WavesPerEU[0], l.WavesPerEU[1]
		if lo < 1 || lo > hi {
			v.err("V31", where, ".waves_per_eu must satisfy 1 <= min <= max")
		}
		if max := v.m.Target.MaxWavesPerEU(); max > 0 && hi > max {
			v.err("V31", where, ".waves_per_eu max exceeds the target's occupancy range")
		}
	}
	if l.KernargPreload != 0 {
		if !v.m.Target.HasKernargPreload() {
			v.err("V31", where, ".kernarg_preload requires a target with preload support")
		}
		if l.KernargPreload < 0 || l.KernargPreload > 127 {
			v.err("V31", where, ".kernarg_preload must lie in [0, 127] dwords")
		}
	}
}

func (v *verifier) kernarg(k *Kernel) {
	for _, s := range k.KernargLayout() {
		p := s.Param
		where := k.Name + " param " + p.Name
		if p.Align != 0 {
			if p.Align&(p.Align-1) != 0 {
				v.err("V34", where, ".align must be a power of two")
			}
			if p.Align < p.NaturalAlign() {
				v.err("V34", where, ".align is weaker than the parameter's natural alignment")
			}
		}
		if !p.Width.IsValid() {
			v.err("V13", where, "parameter width is not a legal size")
		}
		if k.Launch.KernargSize != 0 && s.Offset+s.Size > k.Launch.KernargSize {
			v.err("V33", where, "parameter extends past .kernarg_size")
		}
		if p.Kind != NoParamKind {
			v.warn("", where, "kernarg layout for "+p.Kind.String()+
				" parameters is unspecified in AMDTX 1.0")
		}
	}
}

// bodyCtx carries the position-dependent facts an instruction check needs.
type bodyCtx struct {
	name      string
	regs      *RegFile
	block     *Body
	isKernel  bool
	loop      int
	divergent int
}

func (v *verifier) body(name string, b *Body, isKernel bool, files map[*File]bool) {
	if b == nil {
		v.err("", name, "body is required; AMDTX 1.0 has no external linkage")
		return
	}
	for _, d := range b.Regs.dups {
		v.err("", name, "duplicate register name %"+d)
	}
	for _, d := range b.Regs.Decls() {
		if !d.Class.IsValid() {
			v.err("V13", name, "illegal register class "+d.Class.String())
		}
		if d.Class.Kind == AGPR && !v.m.Target.HasAGPRs() {
			v.err("V14", name, ".agpr requires an AGPR-capable target")
		}
	}

	v.controlForm(name, b)
	v.labels(name, b)

	if !terminates(b, isKernel) {
		if isKernel {
			v.err("V23", name, "kernel body must terminate with s_endpgm on every exit path")
		} else {
			v.err("V24", name, "function body must terminate with ret on every exit path")
		}
	}

	v.items(bodyCtx{name: name, regs: b.Regs, block: b, isKernel: isKernel}, b, files)
}

// controlForm rejects mixing structured regions and explicit labels within
// one body (V26).
func (v *verifier) controlForm(name string, b *Body) {
	var structured, explicit bool
	var scan func(*Body)
	scan = func(x *Body) {
		for _, it := range x.items {
			switch y := it.(type) {
			case *IfStmt:
				structured = true
				scan(y.Then)
				if y.Else != nil {
					scan(y.Else)
				}
			case *LoopStmt:
				structured = true
				scan(y.Body)
			case *BreakIf, *ContinueIf:
				structured = true
			case *LabelBind:
				explicit = true
			case *Instr:
				if Classify(y.Mnemonic()) == ClassSFlow &&
					strings.HasPrefix(y.Mnemonic(), "s_branch") ||
					strings.HasPrefix(y.Mnemonic(), "s_cbranch") {
					explicit = true
				}
			}
		}
	}
	scan(b)
	if structured && explicit {
		v.err("V26", name, "structured regions and explicit labels cannot be mixed in one body")
	}
}

// labels checks label uniqueness, resolution, and that no branch crosses a
// structured-region boundary (V29, V30).
func (v *verifier) labels(name string, b *Body) {
	bound := map[*Label]*Body{}
	seen := map[string]bool{}
	type branch struct {
		l   *Label
		blk *Body
	}
	var branches []branch

	var scan func(*Body)
	scan = func(x *Body) {
		for _, it := range x.items {
			switch y := it.(type) {
			case *LabelBind:
				if _, ok := bound[y.Label]; ok || seen[y.Label.Name()] {
					v.err("V29", name, "label "+y.Label.Name()+" is bound more than once")
				}
				seen[y.Label.Name()] = true
				bound[y.Label] = x
			case *Instr:
				for _, o := range y.Src {
					if l, ok := o.(*Label); ok {
						branches = append(branches, branch{l, x})
					}
				}
			case *IfStmt:
				scan(y.Then)
				if y.Else != nil {
					scan(y.Else)
				}
			case *LoopStmt:
				scan(y.Body)
			}
		}
	}
	scan(b)

	for _, br := range branches {
		blk, ok := bound[br.l]
		if !ok {
			v.err("V29", name, "branch to unbound label "+br.l.Name())
			continue
		}
		if blk != br.blk {
			v.err("V30", name, "branch to "+br.l.Name()+" crosses a structured-region boundary")
		}
	}
}

func (v *verifier) items(ctx bodyCtx, b *Body, files map[*File]bool) {
	pending := map[*regInfo]CounterKind{}

	for i, it := range b.items {
		where := ctx.name + "[" + itoa(i) + "]"
		switch x := it.(type) {
		case *Instr:
			v.instr(ctx, x, where)
			v.pendingLoads(ctx, x, where, pending)
		case *Raw:
			v.raw(ctx, x, where)
			pending = map[*regInfo]CounterKind{}
		case *LabelBind:
			pending = map[*regInfo]CounterKind{}
		case *LocDir:
			if x.File == nil || !files[x.File] {
				v.err("V2", where, ".loc refers to an undeclared .file")
			}
			if i == len(b.items)-1 {
				v.warn("W2", where, "trailing .loc with no following instruction")
			}
		case *IfStmt:
			v.guard(ctx, x.Guard, x.Assert, where, "if")
			sub := ctx
			sub.block = x.Then
			if isDivergentGuard(x.Guard) {
				sub.divergent++
			}
			v.items(sub, x.Then, files)
			if x.Else != nil {
				sub.block = x.Else
				v.items(sub, x.Else, files)
			}
			pending = map[*regInfo]CounterKind{}
		case *LoopStmt:
			sub := ctx
			sub.block = x.Body
			sub.loop++
			v.items(sub, x.Body, files)
			pending = map[*regInfo]CounterKind{}
		case *BreakIf:
			if ctx.loop == 0 {
				v.err("V28", where, "breakif requires an enclosing loop")
			}
			v.guard(ctx, x.Guard, x.Assert, where, "breakif")
		case *ContinueIf:
			if ctx.loop == 0 {
				v.err("V28", where, "continueif requires an enclosing loop")
			}
			v.guard(ctx, x.Guard, x.Assert, where, "continueif")
		}
	}
}

func isDivergentGuard(o Operand) bool {
	r, ok := o.(Reg)
	return ok && r.Class().Kind == LaneMask
}

// guard checks a structured guard operand and its optional uniformity
// assertion. An assertion that disagrees with the operand's class is a
// verification failure; assertions that cannot lie are the only ones worth
// having (§10.1).
func (v *verifier) guard(ctx bodyCtx, o Operand, a Uniformity, where, what string) {
	if o == nil {
		v.err("", where, what+" requires a guard operand")
		return
	}
	v.checkRegs(ctx, o, where)

	uniform := true
	switch g := o.(type) {
	case Reg:
		uniform = g.Class().Kind != LaneMask
	case SReg:
		uniform = g != Exec && g != VCC
	}
	switch a {
	case UniformGuard:
		if !uniform {
			v.err("", where, ".uniform contradicts the guard's register class")
		}
	case DivergentGuard:
		if uniform {
			v.err("", where, ".divergent contradicts the guard's register class")
		}
	}
}

// ---- Instructions ---------------------------------------------------------

func (v *verifier) instr(ctx bodyCtx, in *Instr, where string) {
	if in.Op != OpCustom && !in.Op.IsValid() {
		v.err("", where, "unknown opcode")
		return
	}
	mn := in.Mnemonic()
	class := Classify(mn)

	for _, o := range in.Operands() {
		v.checkRegs(ctx, o, where)
	}

	v.destClass(in, mn, class, where)
	v.addressClass(in, class, where)
	v.dataWidth(in, mn, class, where)
	v.gating(in, mn, where)
	v.literals(in, where)
	v.crossLane(in, mn, where)
	v.counters(in, where)

	// V15: divergence is expressed structurally, never by writing %exec.
	for _, d := range in.Dst {
		if s, ok := d.(SReg); ok && s == Exec {
			v.err("V15", where, "direct writes to %exec are prohibited outside raw")
		}
	}

	// V21: all waves of the workgroup must reach s_barrier.
	if in.Op == OpBarrier && ctx.divergent > 0 {
		v.err("V21", where, "s_barrier must not appear inside divergent control flow")
	}

	// V38: a bare fence is rejected.
	if in.Op == OpFence {
		if in.Ord == NoOrdering || !in.Scope.IsValid() {
			v.err("V38", where, "fence requires both an ordering and a scope")
		}
	}

	// V3 / V4: calls target functions only, and never a register.
	if in.Op == OpCall && len(in.Src) > 0 {
		switch t := in.Src[0].(type) {
		case *Kernel:
			v.err("V3", where, "kernels are dispatch entry points and are not callable")
		case *Func:
			// checked in callGraph
			_ = t
		case Reg, SReg:
			v.err("V4", where, "indirect calls are rejected in AMDTX 1.0")
		}
	}
}

// checkRegs enforces V16 (declared before use) and V40 (slice bounds).
func (v *verifier) checkRegs(ctx bodyCtx, o Operand, where string) {
	operandRegs(o, func(r Reg) {
		if !r.IsValid() {
			v.err("V16", where, "operand register is not declared")
			return
		}
		if r.r.file != ctx.regs {
			v.err("V16", where, "register "+r.Text()+" belongs to another body's register file")
			return
		}
		if lo, hi, ok := r.SliceBounds(); ok {
			n := r.Class().Width.Dwords()
			if r.Class().Kind == LaneMask {
				n = v.m.Wave.MaskWidth().Dwords()
			}
			if lo < 0 || hi < lo || hi >= n {
				v.err("V40", where, "sub-register slice "+r.Text()+
					" exceeds the declared tuple of "+itoa(n)+" dwords")
			}
		}
	})
}

// destClass enforces V6 and V7.
func (v *verifier) destClass(in *Instr, mn string, class Class, where string) {
	readback := mn == "v_readfirstlane_b32" || mn == "v_readlane_b32"
	for _, d := range in.Dst {
		kind, ok := destKind(d)
		if !ok {
			continue
		}
		switch class {
		case ClassSALU, ClassSMEM:
			if kind != SGPR {
				v.err("V6", where, "scalar instruction must write an SGPR, %scc or %m0")
			}
		case ClassVALU:
			if kind == SGPR && !readback && !isLaneMask(d) && !isVCC(d) {
				v.err("V7", where, "vector instruction must write a VGPR, AGPR, %vcc or a .lanemask")
			}
		}
	}
}

func destKind(o Operand) (RegKind, bool) {
	switch x := o.(type) {
	case Reg:
		return x.Kind(), true
	case SReg:
		return x.Kind(), true
	}
	return NoRegKind, false
}

func isLaneMask(o Operand) bool {
	r, ok := o.(Reg)
	return ok && r.Class().Kind == LaneMask
}

func isVCC(o Operand) bool {
	s, ok := o.(SReg)
	return ok && (s == VCC || s == SCC || s == M0)
}

// addressClass enforces V8.
func (v *verifier) addressClass(in *Instr, class Class, where string) {
	for _, s := range in.Src {
		m, ok := s.(Mem)
		if !ok {
			continue
		}
		kind, known := destKind(m.Base)
		if !known {
			continue
		}
		switch class {
		case ClassSMEM:
			if kind != SGPR {
				v.err("V8", where, "scalar memory requires an SGPR base address")
			}
		case ClassVMEM, ClassLDS:
			if kind != VGPR {
				v.err("V8", where, "vector memory requires a VGPR address register")
			}
		}
	}
}

// dataWidth enforces V9: memory access width equals data-register width.
func (v *verifier) dataWidth(in *Instr, mn string, class Class, where string) {
	if class != ClassSMEM && class != ClassVMEM && class != ClassLDS {
		return
	}
	w := WidthOfMnemonic(mn)
	if w == NoWidth {
		return
	}
	if !w.IsValid() {
		v.err("V13", where, "mnemonic width "+w.String()+" is not a legal tuple size")
	}
	for _, o := range in.Operands() {
		r, ok := o.(Reg)
		if !ok || r.Class().Kind == LaneMask {
			continue
		}
		if r.Width() != NoWidth && r.Width() != w {
			v.err("V9", where, "access width "+w.String()+
				" does not match data register width "+r.Width().String())
		}
		return // the first register operand is the data register
	}
}

// gating enforces V10, V11 and V12.
func (v *verifier) gating(in *Instr, mn string, where string) {
	t := v.m.Target
	feat := featNone
	if in.Op != OpCustom {
		feat = opTable[in.Op].feature
	}
	switch {
	case strings.HasPrefix(mn, "v_mfma"):
		feat = featCDNA
	case strings.HasPrefix(mn, "v_wmma"):
		feat = featGFX11
	}
	switch feat {
	case featCDNA:
		if !t.IsCDNA() {
			v.err("V10", where, mn+" requires a CDNA target")
		}
	case featGFX11:
		if !t.Family().GTE(GFX11) {
			v.err("V11", where, mn+" requires GFX11 or later")
		}
	case featVSCnt:
		if !t.HasVSCnt() {
			v.err("V12", where, "waitcnt_vscnt requires GFX10 or GFX11")
		}
	}
	for _, o := range in.Operands() {
		if r, ok := o.(Reg); ok && r.Class().Kind == AGPR && !t.HasAGPRs() {
			v.err("V14", where, ".agpr operand on a target without AGPRs")
		}
	}
}

// literals enforces V17, V18 and V19.
func (v *verifier) literals(in *Instr, where string) {
	lits := map[uint64]bool{}
	for _, o := range in.Src {
		val, isLit := literalOf(o)
		if !isLit {
			continue
		}
		lits[val] = true
		if imm, ok := o.(Imm); ok && !imm.Fits32() {
			if in.Enc == VOP3 || in.Enc == VOP3P {
				v.err("V19", where, "VOP3/VOP3P encodings do not permit 64-bit literals")
			} else {
				v.warn("V17", where, "literal does not fit a single dword")
			}
		}
		if (in.Enc == VOP3 || in.Enc == VOP3P) && !v.m.Target.Family().GTE(GFX10) {
			v.err("V19", where, "VOP3/VOP3P encodings permit no literals before GFX10")
		}
	}
	if len(lits) > 1 {
		v.err("V18", where, "an instruction may use only one literal dword")
	}
}

// crossLane enforces V22.
func (v *verifier) crossLane(in *Instr, mn string, where string) {
	if !strings.HasPrefix(mn, "v_readlane") && !strings.HasPrefix(mn, "v_writelane") &&
		!strings.HasPrefix(mn, "v_permlane") {
		return
	}
	limit := v.m.Wave.Bits()
	for _, o := range in.Src {
		if imm, ok := o.(Imm); ok {
			if imm < 0 || int(imm) >= limit {
				v.err("V22", where, "lane index "+imm.Text()+
					" is not below the active wave width")
			}
		}
	}
}

// counters enforces V37 and the vscnt separation rule.
func (v *verifier) counters(in *Instr, where string) {
	for _, o := range in.Src {
		c, ok := o.(Counter)
		if !ok {
			continue
		}
		if c.Kind == VSCnt {
			v.err("V12", where, "vscnt is not part of waitcnt; use waitcnt_vscnt")
			continue
		}
		max := v.m.Target.CounterMax(c.Kind)
		if max < 0 {
			v.err("V37", where, c.Kind.String()+" does not exist on this target")
		} else if c.N < 0 || c.N > max {
			v.err("V37", where, c.Kind.String()+"("+itoa(c.N)+
				") is outside the target's range of 0.."+itoa(max))
		}
	}
	if in.Op == OpWaitcntVScnt {
		for _, o := range in.Src {
			if imm, ok := o.(Imm); ok {
				if max := v.m.Target.CounterMax(VSCnt); max >= 0 &&
					(imm < 0 || int(imm) > max) {
					v.err("V37", where, "vscnt value outside the target's range")
				}
			}
		}
	}
}

// raw enforces V39. An undeclared write is undefined behaviour, so the
// verifier rejects it wherever it can prove it.
func (v *verifier) raw(ctx bodyCtx, r *Raw, where string) {
	for _, o := range append(append([]Operand{}, r.Defs...), append(r.Uses, r.Clobbers...)...) {
		v.checkRegs(ctx, o, where)
	}
	if r.Text == "" && r.Bytes == nil {
		v.err("", where, "raw requires either text or bytes")
	}
	n := len(r.Defs) + len(r.Uses)
	for i := 0; i+1 < len(r.Text); i++ {
		if r.Text[i] != '%' || r.Text[i+1] < '0' || r.Text[i+1] > '9' {
			continue
		}
		idx := 0
		j := i + 1
		for ; j < len(r.Text) && r.Text[j] >= '0' && r.Text[j] <= '9'; j++ {
			idx = idx*10 + int(r.Text[j]-'0')
		}
		if idx >= n {
			v.err("V39", where, "raw references %"+itoa(idx)+
				" but only "+itoa(n)+" def/use operands are declared")
		}
		i = j - 1
	}
	if len(r.Defs) == 0 && len(r.Clobbers) == 0 && r.Text != "" {
		v.warn("V39", where, "raw declares no defs or clobbers; confirm it writes nothing")
	}
}

// pendingLoads implements W1: reading a register with a pending un-waited
// load is a warning, not an error, because an optional autowait pass may be
// configured to fix it before verification.
func (v *verifier) pendingLoads(ctx bodyCtx, in *Instr, where string, pending map[*regInfo]CounterKind) {
	mn := in.Mnemonic()

	if in.Op == OpWaitcnt {
		for _, o := range in.Src {
			if c, ok := o.(Counter); ok && c.N == 0 {
				for r, k := range pending {
					if k == c.Kind {
						delete(pending, r)
					}
				}
			}
		}
		return
	}
	if in.Op == OpWaitcntVScnt {
		for r, k := range pending {
			if k == VSCnt {
				delete(pending, r)
			}
		}
		return
	}

	for _, s := range in.Src {
		operandRegs(s, func(r Reg) {
			if r.r == nil {
				return
			}
			if _, ok := pending[r.r]; ok {
				v.warn("W1", where, "reads "+r.Text()+" with a pending un-waited load")
				delete(pending, r.r)
			}
		})
	}

	if k := PendingCounter(mn); k != NoCounter && IsLoad(mn) {
		for _, d := range in.Dst {
			if r, ok := d.(Reg); ok && r.r != nil {
				pending[r.r] = k
			}
		}
	}
}

// ---- Termination ----------------------------------------------------------

// terminates reports whether every path out of b ends in the required
// terminator (V23, V24). It is conservative: a body whose last statement is
// not a terminator is rejected even when a branch makes it unreachable.
func terminates(b *Body, isKernel bool) bool {
	for i := len(b.items) - 1; i >= 0; i-- {
		switch x := b.items[i].(type) {
		case *LocDir:
			continue
		case *Instr:
			if isKernel {
				return x.Op == OpEndPgm
			}
			return x.Op == OpRet
		case *IfStmt:
			return x.Else != nil &&
				terminates(x.Then, isKernel) && terminates(x.Else, isKernel)
		default:
			return false
		}
	}
	return false
}