// amdtx.go
package amdtx

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Options controls artifact generation. Everything here is a choice about what
// to emit, never about what the emitted module means.
type Options struct {
	// Debug emits the .file table and the .loc directives the module's loc
	// lines carry (§14). Off by default: the canonical printer drops
	// comments but not .loc, so this changes the artifact.
	Debug bool
}

// Exclusion records a kernel dropped from this artifact by §4.3 gating. It is
// not an error here; exclusion from *every* artifact is, and that judgement
// belongs to ir/verify.
type Exclusion struct {
	Kernel  string
	Feature gvir.Feature
}

// Result is one lowered artifact: one gfx target, one AMDTX module.
type Result struct {
	Module   *amdtx.Module
	Arch     string
	Target   amdtx.Target
	Wave     amdtx.Wave
	Excluded []Exclusion
}

// Lower lowers m for one declared amdgcn arch with default options.
func Lower(m *gvir.Module, arch string) (*Result, error) {
	return LowerOptions(m, arch, Options{})
}

// LowerAll lowers every amdgcn arch the module declares, in declaration
// order — the order the availability bitmask depends on (§3).
func LowerAll(m *gvir.Module, o Options) ([]*Result, error) {
	if m == nil || m.Target == nil {
		return nil, fmt.Errorf("amdtx: module declares no target (§3)")
	}
	b := m.Target.Backend(gvir.BackendAMDGCN)
	if b == nil {
		return nil, fmt.Errorf("amdtx: module %s declares no amdgcn backend (§3)", m.Name)
	}
	if len(b.Archs) == 0 {
		return nil, fmt.Errorf("amdtx: the amdgcn arch list is required and produces one artifact per arch (§3)")
	}
	out := make([]*Result, 0, len(b.Archs))
	for _, arch := range b.Archs {
		r, err := LowerOptions(m, arch, o)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// LowerOptions is Lower with explicit options.
func LowerOptions(m *gvir.Module, arch string, o Options) (*Result, error) {
	if m == nil {
		return nil, fmt.Errorf("amdtx: nil module")
	}
	if m.Target == nil {
		return nil, fmt.Errorf("amdtx: module %s declares no target (§3)", m.Name)
	}
	b := m.Target.Backend(gvir.BackendAMDGCN)
	if b == nil {
		return nil, fmt.Errorf("amdtx: module %s declares no amdgcn backend (§3)", m.Name)
	}
	found := false
	for _, a := range b.Archs {
		if a == arch {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("amdtx: arch %q is not declared by module %s (declared: %s)",
			arch, m.Name, strings.Join(b.Archs, ", "))
	}
	if !gvir.KnownArch(gvir.BackendAMDGCN, arch) {
		if canonical, isAlias := gvir.ArchAlias(gvir.BackendAMDGCN, arch); isAlias {
			return nil, fmt.Errorf("amdtx: %q is an alias, not a canonical arch name — write %q (§3)", arch, canonical)
		}
		return nil, fmt.Errorf("amdtx: unknown amdgcn arch %q (§3)", arch)
	}
	t, ok := amdtx.TargetByName(arch)
	if !ok {
		return nil, fmt.Errorf("amdtx: %q is a valid .gvir arch but has no row in the AMDTX §4 processor "+
			"table; adding it is a spec revision (a table row, a counter profile, an inline-constant "+
			"profile, an encoding table and an EF_AMDGPU_MACH value), not a lowering decision", arch)
	}

	l := &lowerer{
		src:    m,
		opts:   o,
		arch:   arch,
		target: t,
		consts: map[string]*amdtx.Object{},
		funcs:  map[string]*amdtx.Func{},
	}
	if err := l.module(); err != nil {
		return nil, err
	}
	return l.res, nil
}

// lowerer holds the per-artifact state. One lowerer produces one Result.
type lowerer struct {
	src  *gvir.Module
	opts Options

	arch   string
	target amdtx.Target
	wave   amdtx.Wave

	out    *amdtx.Module
	res    *Result
	consts map[string]*amdtx.Object
	funcs  map[string]*amdtx.Func

	emitted map[string]bool // kernels surviving §4.3 gating
	reached map[string]bool // funcs reachable from an emitted kernel
}

func (l *lowerer) module() error {
	if err := l.gate(); err != nil {
		return err
	}
	w, err := l.selectWave()
	if err != nil {
		return err
	}
	l.wave = w
	l.out = amdtx.NewModule(amdtx.V10, l.target, w)
	l.res = &Result{Module: l.out, Arch: l.arch, Target: l.target, Wave: w}
	l.res.Excluded = l.exclusions()

	if l.opts.Debug {
		l.files()
	}
	if err := l.constants(); err != nil {
		return err
	}

	// Funcs first: AMDTX requires declaration before use (§3.2), and .gvir
	// already forbids forward references, so source order is legal order.
	for _, f := range l.src.Funcs {
		if !l.reached[f.Name] {
			continue // §4.3 rule 3: an unreached func is dropped
		}
		out := amdtx.NewFunc(f.Name)
		l.funcs[f.Name] = out
		l.out.Add(out)
	}
	for _, f := range l.src.Funcs {
		if !l.reached[f.Name] {
			continue
		}
		if err := l.function(f); err != nil {
			return fmt.Errorf("func %s: %w", f.Name, err)
		}
	}
	for _, k := range l.src.Kernels {
		if !l.emitted[k.Name] {
			continue
		}
		if err := l.kernel(k); err != nil {
			return fmt.Errorf("kernel %s: %w", k.Name, err)
		}
	}
	return nil
}

// selectWave resolves the module-scope .wave from the per-kernel
// subgroup_size attributes of the kernels that survive gating.
//
// AMDTX fixes one wave width per module (P1, §4.1) because every
// width-dependent rule follows from it; .gvir attaches subgroup_size to a
// kernel. Two emitted kernels asking for different widths therefore cannot
// share an artifact, and saying so is better than silently picking one.
func (l *lowerer) selectWave() (amdtx.Wave, error) {
	want := 0
	var by string
	for _, k := range l.src.Kernels {
		if !l.emitted[k.Name] || k.SubgroupSize == 0 {
			continue
		}
		if want != 0 && k.SubgroupSize != want {
			return 0, fmt.Errorf("kernels %s and %s request subgroup_size %d and %d; AMDTX fixes one "+
				".wave per module (P1, §4.1), so they cannot share the %s artifact",
				by, k.Name, want, k.SubgroupSize, l.arch)
		}
		want, by = k.SubgroupSize, k.Name
	}
	switch want {
	case 0:
		return l.target.DefaultWave(), nil
	case 32:
		if !l.target.SupportsWave32() {
			return 0, fmt.Errorf("kernel %s requests subgroup_size 32; .wave 32 is legal only on GFX10 "+
				"and later and %s is %s (V5)", by, l.arch, l.target.Family())
		}
		return amdtx.Wave32, nil
	case 64:
		// .wave 64 is legal on every target: RDNA parts support it through
		// the wavefrontsize64 feature (§4.1).
		return amdtx.Wave64, nil
	}
	return 0, fmt.Errorf("kernel %s requests subgroup_size %d; amdgcn wave widths are 32 and 64 (§4.1)", by, want)
}

// files registers every .file the module's loc lines reference, in
// first-appearance order, so that every .file precedes every .loc (V2).
func (l *lowerer) files() {
	for _, k := range l.src.Kernels {
		if l.emitted[k.Name] {
			l.filesOf(&k.Body)
		}
	}
	for _, f := range l.src.Funcs {
		if l.reached[f.Name] {
			l.filesOf(&f.Body)
		}
	}
}

func (l *lowerer) filesOf(b *gvir.Body) {
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			if in.Op == gvir.OpLoc && len(in.Args) > 0 && in.Args[0].Kind == gvir.OperandString {
				l.out.File(in.Args[0].Str)
			}
		}
	}
}

// constants lowers module-scope consts. Scalar consts are compile-time values
// and are materialized at each use, so only aggregates need storage; AMDTX
// module objects are .global or .shared only, and a read-only .global is the
// closest thing to .gvir's constant space that the object grammar admits.
func (l *lowerer) constants() error {
	for _, c := range l.src.Constants {
		if !gvir.IsAggregate(c.Type) {
			continue
		}
		size, err := l.src.SizeOf(c.Type)
		if err != nil {
			return fmt.Errorf("const %s: %w", c.Name, err)
		}
		align, err := l.src.AlignOf(c.Type)
		if err != nil {
			return fmt.Errorf("const %s: %w", c.Name, err)
		}
		if size%4 != 0 {
			return fmt.Errorf("const %s occupies %d bytes; AMDTX object widths are multiples of 32 bits (§5)", c.Name, size)
		}
		init, err := flattenInit(c.Init)
		if err != nil {
			return fmt.Errorf("const %s: %w", c.Name, err)
		}
		o := l.out.Object(amdtx.Object{
			Linkage: amdtx.Local,
			Space:   amdtx.Global,
			Align:   align,
			Width:   amdtx.B32,
			Name:    c.Name,
			Len:     size / 4,
			Init:    init,
		})
		l.consts[c.Name] = o
	}
	return nil
}

// flattenInit lowers a const-init tree to the flat dword init-list AMDTX
// object declarations take. A zero initializer stays omitted: an object with
// no init-list is the spelling for it, and V41 only constrains lengths that
// are actually written.
func flattenInit(init gvir.ConstInit) ([]amdtx.Operand, error) {
	switch x := init.(type) {
	case gvir.InitZero, nil:
		return nil, nil
	case gvir.InitLiteral:
		if x.Value.Kind != gvir.OperandInt {
			return nil, fmt.Errorf("only integer initializers lower to an AMDTX init-list today")
		}
		return []amdtx.Operand{amdtx.Imm(x.Value.Int)}, nil
	case gvir.InitAggregate:
		var out []amdtx.Operand
		for _, e := range x.Elems {
			sub, err := flattenInit(e)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported const initializer form")
}