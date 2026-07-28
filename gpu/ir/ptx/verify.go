package ptx

// Severity distinguishes structural errors from advisory findings.
type Severity uint8

const (
	// Error marks IR that cannot produce valid PTX.
	Error Severity = iota
	// Warning marks IR that will assemble on some configurations but
	// contradicts the module's declared version or target.
	Warning
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// Diag is one finding from Verify.
type Diag struct {
	Severity Severity
	Where    string // "vadd[12]", "module", "global lut"
	Msg      string
}

func (d Diag) String() string { return d.Where + ": " + d.Severity.String() + ": " + d.Msg }

// Verify checks a module for structural problems and for features that
// postdate the declared .version or .target. It never mutates the module and
// never stops at the first finding.
func Verify(m *Module) []Diag {
	var ds []Diag
	add := func(s Severity, where, msg string) {
		ds = append(ds, Diag{s, where, msg})
	}

	if m.AddressSize != Addr32 && m.AddressSize != Addr64 {
		add(Error, "module", ".address_size must be 32 or 64")
	}
	if m.Version.IsZero() {
		add(Error, "module", ".version is required")
	}
	if m.BlocksAreClusters && !m.Version.GTE(ISA90) {
		add(Warning, "module", ".blocksareclusters requires .version 9.0 or later")
	}
	if m.Target.Suffix == Family && !m.Version.GTE(ISA88) {
		add(Warning, "module", "family-specific targets require .version 8.8 or later")
	}

	for _, d := range m.decls {
		switch x := d.(type) {
		case *Var:
			verifyVar(x, "global "+x.Name, add)
		case *Kernel:
			verifyCallable(m, &x.Callable, x.Name, true, add)
		case *Func:
			verifyCallable(m, &x.Callable, x.Name, false, add)
			if x.Linkage == Common {
				add(Error, x.Name, ".common linkage applies to variables only")
			}
		}
	}
	return ds
}

func verifyVar(v *Var, where string, add func(Severity, string, string)) {
	if !v.Type.IsValid() {
		add(Error, where, "variable has no type")
	}
	if !v.Space.IsValid() {
		add(Error, where, "variable has no state space")
	}
	if sub := v.Space.SubQual(); sub != NoSub {
		if !legalSub[v.Space.Base()][sub] {
			add(Error, where, "state space "+v.Space.Name()+" does not accept ::"+sub.String())
		}
	}
	if v.Align != 0 && v.Align&(v.Align-1) != 0 {
		add(Error, where, ".align must be a power of two")
	}
	if v.Vec != 0 && v.Vec != 2 && v.Vec != 4 {
		add(Error, where, "vector width must be 2 or 4")
	}
	if v.Space.Base() == RegSpace {
		add(Error, where, "register variables are declared through RegFile, not Var")
	}
}

func verifyCallable(m *Module, c *Callable, name string, isKernel bool, add func(Severity, string, string)) {
	if c.Body == nil {
		if c.Linkage != Extern {
			add(Error, name, "declaration without a body requires .extern linkage")
		}
		return
	}

	cluster := !c.ReqNCTAPerCluster.IsZero() || c.ExplicitCluster || c.MaxClusterRank != 0
	if cluster && m.Target.SM < 90 {
		add(Warning, name, "cluster directives require .target sm_90 or later")
	}
	if cluster && !m.Version.GTE(ISA80) {
		add(Warning, name, "cluster directives require .version 8.0 or later")
	}

	for _, dup := range c.Body.Regs.dups {
		add(Error, name, "duplicate named register %"+dup)
	}
	for _, v := range c.Body.Locals {
		verifyVar(v, name+" local "+v.Name, add)
		switch v.Space.Base() {
		case Local, Shared, ParamSpace:
		default:
			add(Error, name+" local "+v.Name,
				"function-scope variables must be .local, .shared or .param")
		}
	}

	bound := map[*Label]bool{}
	for _, it := range c.Body.items {
		if lb, ok := it.(*LabelBind); ok {
			if bound[lb.Label] {
				add(Error, name, "label "+lb.Label.Name()+" is bound more than once")
			}
			bound[lb.Label] = true
		}
	}

	for i, it := range c.Body.items {
		in, ok := it.(*Instr)
		if !ok {
			continue
		}
		where := name + "[" + itoa(i) + "]"
		verifyInstr(m, in, where, bound, add)
	}
}

func verifyInstr(m *Module, in *Instr, where string, bound map[*Label]bool, add func(Severity, string, string)) {
	if in.Op != OpCustom && !in.Op.IsValid() {
		add(Error, where, "unknown opcode")
		return
	}

	if in.Pred.IsSet() {
		if !in.Pred.Reg.IsValid() {
			add(Error, where, "guard predicate is not a valid register")
		} else if in.Pred.Reg.Type() != Pred {
			add(Error, where, "guard predicate must be a .pred register")
		}
	}

	if n := in.Op.NTypes(); in.Op != OpCustom {
		got := 0
		for _, t := range in.Types {
			if t != NoType {
				got++
			}
		}
		if got != n {
			add(Error, where, in.Base()+" takes "+itoa(n)+" type specifier(s), got "+itoa(got))
		}
	}

	for _, t := range in.Types {
		if t == NoType {
			continue
		}
		if !t.IsValid() {
			add(Error, where, "unknown type specifier")
			continue
		}
		if mi := t.MinISA(); !mi.IsZero() && !m.Version.GTE(mi) {
			add(Warning, where, t.String()+" requires .version "+mi.String())
		}
	}

	if mi := in.Op.MinISA(); !mi.IsZero() && !m.Version.GTE(mi) {
		add(Warning, where, in.Base()+" requires .version "+mi.String())
	}
	if sm := in.Op.MinSM(); sm != 0 && m.Target.SM < sm {
		add(Warning, where, in.Base()+" requires .target sm_"+itoa(sm))
	}

	if in.Q.Vec != 0 && in.Q.Vec != 2 && in.Q.Vec != 4 && in.Q.Vec != 8 {
		add(Error, where, "vector width must be 2, 4 or 8")
	}
	if s := in.Q.Space; s != NoSpace {
		if !s.IsValid() {
			add(Error, where, "unknown state space")
		} else if sub := s.SubQual(); sub != NoSub && !legalSub[s.Base()][sub] {
			add(Error, where, "state space "+s.Name()+" does not accept ::"+sub.String())
		}
	}

	// Mutually exclusive rounding-family qualifiers.
	nRound := 0
	if in.Q.Round != NoRound {
		nRound++
	}
	if in.Q.Approx {
		nRound++
	}
	if in.Q.Full {
		nRound++
	}
	if nRound > 1 {
		add(Error, where, "rounding, .approx and .full are mutually exclusive")
	}

	// fma has no default rounding mode.
	if in.Op == OpFma && in.Q.Round == NoRound {
		add(Error, where, "fma requires an explicit rounding qualifier")
	}
	if in.Op == OpCvta && in.Q.Space == NoSpace {
		add(Error, where, "cvta requires a state space")
	}
	if in.Op == OpAtom && in.Q.Atom == NoAtom {
		add(Error, where, "atom requires an operation qualifier")
	}
	if in.Op == OpRed && in.Q.Atom == NoAtom {
		add(Error, where, "red requires an operation qualifier")
	}
	if in.Op == OpSetp && in.Q.Cmp == NoCmp {
		add(Error, where, "setp requires a comparison qualifier")
	}
	if in.Op == OpShf && (in.Q.Dir == NoDir || in.Q.Shf == NoShf) {
		add(Error, where, "shf requires a direction and .clamp or .wrap")
	}

	// Branch targets must be bound in the same body.
	for _, o := range in.Src {
		if l, ok := o.(*Label); ok && !bound[l] {
			add(Error, where, "branch to unbound label "+l.Name())
		}
	}
}