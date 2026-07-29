// Package ptx lowers a verified .gvir device compute-kernel module to a
// structured PTX IR module.
//
// The input is an ir/gvir.Module that has already passed ir/verify: this
// package assumes block ordering, merge annotations, the uniformity analysis,
// name binding and type legality are all settled, and concerns itself only
// with the mapping onto PTX. Anything it cannot express is a §1.1 lowering
// error, returned rather than approximated.
//
// The output is gpu/ir/ptx.Module — IR, not text. Print it with
// gpu/ir/ptx/encoding/text, rewrite it, or embed it.
//
// This is package ptx and it imports gpu/ir/ptx under the same name. Go only
// qualifies imported identifiers, so every ptx.X below is the IR package.
package ptx

import (
	"fmt"
	"strconv"
	"strings"

	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Options configures a lowering. The zero value is the default: the arch and
// ISA are derived from the module's target declaration (§3).
type Options struct {
	// Arch overrides the ptx arch taken from the module's target-decl. It must
	// be a canonical §3 name; aliases are rejected with a diagnostic.
	Arch string

	// ISA overrides the .version directive. Zero selects the lowest version
	// that covers the selected arch.
	ISA ptx.ISAVersion

	// Comments re-emits dropped .gvir annotations (merge points, loc context)
	// as trailing PTX comments. They are whitespace; the text printer emits
	// them only when configured to.
	Comments bool
}

// Exclusion records a kernel dropped from this artifact by §4.3 gating.
type Exclusion struct {
	Kernel  string
	Feature gvir.Feature
}

func (e Exclusion) String() string {
	return fmt.Sprintf("kernel %s uses %s", e.Kernel, e.Feature)
}

// Result is one lowered artifact.
type Result struct {
	Module *ptx.Module
	Arch   string
	Target ptx.Target

	// Excluded lists kernels not emitted here because they use a §4.3 gated
	// feature this arch lacks. Exclusion is not an error: only exclusion from
	// every declared artifact is (§4.3 rule 4), and that is a whole-module
	// judgement ir/verify makes.
	Excluded []Exclusion
}

// Lower lowers m with default options.
func Lower(m *gvir.Module) (*Result, error) { return LowerOptions(m, Options{}) }

// LowerOptions lowers m, producing the single ptx artifact §3 describes.
func LowerOptions(m *gvir.Module, o Options) (*Result, error) {
	if m == nil {
		return nil, fmt.Errorf("gpu/lower/ptx: nil module")
	}
	arch, err := selectArch(m, o.Arch)
	if err != nil {
		return nil, err
	}
	target, err := parseArch(arch)
	if err != nil {
		return nil, err
	}
	isa := o.ISA
	if isa.IsZero() {
		isa = defaultISA(target)
	}

	l := &lowerer{
		gm:        m,
		opts:      o,
		arch:      arch,
		target:    target,
		pm:        ptx.NewModule(isa, target, ptx.Addr64),
		constVar:  map[string]*ptx.Var{},
		constImm:  map[string]ptx.Operand{},
		constType: map[string]gvir.Type{},
		funcs:     map[string]*ptx.Func{},
	}
	if err := l.run(); err != nil {
		return nil, err
	}

	res := &Result{Module: l.pm, Arch: arch, Target: target, Excluded: l.excluded}
	return res, nil
}

// lowerer is the module-wide lowering context.
type lowerer struct {
	gm     *gvir.Module
	pm     *ptx.Module
	opts   Options
	arch   string
	target ptx.Target

	// Module consts. Scalars are additionally inlined at use sites; aggregates
	// exist only as .const variables addressed by name.
	constVar  map[string]*ptx.Var
	constImm  map[string]ptx.Operand
	constType map[string]gvir.Type

	funcs    map[string]*ptx.Func
	dynSmem  *ptx.Var
	excluded []Exclusion
}

func (l *lowerer) run() error {
	// float_profile contract off means strict IEEE with no contraction (§11.6).
	// ptxas contracts by default, so the absence of the flag has to be said out
	// loud; its presence is simply the absence of the pragma.
	if !l.gm.Profile.Contract {
		l.pm.Pragma("nofma")
	}

	if err := l.lowerConsts(); err != nil {
		return err
	}
	keep, err := l.gate()
	if err != nil {
		return err
	}
	if err := l.declareDynamicShared(keep); err != nil {
		return err
	}

	// Declaration order is emission order: .gvir forbids forward references
	// and PTX requires declaration before use, so the two agree already.
	for _, f := range l.gm.Funcs {
		if !keep.funcs[f.Name] {
			continue
		}
		if err := l.lowerFunc(f); err != nil {
			return fmt.Errorf("func %s: %w", f.Name, err)
		}
	}
	for _, k := range l.gm.Kernels {
		if !keep.kernels[k.Name] {
			continue
		}
		if err := l.lowerKernel(k); err != nil {
			return fmt.Errorf("kernel %s: %w", k.Name, err)
		}
	}
	return nil
}

// declareDynamicShared creates the single module-scope extern shared array
// every dynamic_group name aliases. PTX has one dynamic shared window per
// launch, so per-kernel names cannot be per-kernel objects (§8.2).
func (l *lowerer) declareDynamicShared(keep keepSet) error {
	align := 0
	for _, k := range l.gm.Kernels {
		if !keep.kernels[k.Name] || k.DynamicGroup == nil {
			continue
		}
		a := k.DynamicGroup.Align
		if a == 0 {
			a = 16
		}
		if !gvir.ValidAlign(a) {
			return fmt.Errorf("kernel %s: dynamic_group align %d is not a power of two in [1,1024] (§2)", k.Name, a)
		}
		if a > align {
			align = a
		}
	}
	if align == 0 {
		return nil
	}
	l.dynSmem = l.pm.Var(ptx.Var{
		Linkage: ptx.Extern,
		Space:   ptx.Shared,
		Align:   align,
		Type:    ptx.B8,
		Name:    "$dyn_smem",
		Len:     -1, // incomplete array: "[]"
	})
	return nil
}

// ---------------------------------------------------------------------------
// Arch and ISA selection (§3)
// ---------------------------------------------------------------------------

func selectArch(m *gvir.Module, override string) (string, error) {
	arch := override
	if arch == "" {
		b := m.Target.Backend(gvir.BackendPTX)
		if b == nil {
			return "", fmt.Errorf("gpu/lower/ptx: module %q declares no ptx backend (§3)", m.Name)
		}
		if len(b.Archs) == 0 {
			arch = gvir.DefaultArch(gvir.BackendPTX)
		} else {
			arch = b.Archs[0]
		}
	}
	if !gvir.KnownArch(gvir.BackendPTX, arch) {
		if canonical, ok := gvir.ArchAlias(gvir.BackendPTX, arch); ok {
			return "", fmt.Errorf("gpu/lower/ptx: ptx arch %q is an alias, not a canonical name — write %q (§3)", arch, canonical)
		}
		return "", fmt.Errorf("gpu/lower/ptx: unknown ptx arch %q (§3)", arch)
	}
	return arch, nil
}

// parseArch turns a §3 arch name into a ptx.Target. The suffix vocabulary is
// PTX's own (sm_90a, sm_100f), not something .gvir models.
func parseArch(arch string) (ptx.Target, error) {
	s, ok := strings.CutPrefix(arch, "sm_")
	if !ok {
		return ptx.Target{}, fmt.Errorf("gpu/lower/ptx: %q is not an sm_* arch", arch)
	}
	suffix := ptx.Base
	switch {
	case strings.HasSuffix(s, "a"):
		suffix, s = ptx.ArchSpc, s[:len(s)-1]
	case strings.HasSuffix(s, "f"):
		suffix, s = ptx.Family, s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return ptx.Target{}, fmt.Errorf("gpu/lower/ptx: %q has no sm number", arch)
	}
	return ptx.Target{SM: n, Suffix: suffix}, nil
}

// defaultISA is the lowest .version that covers the target. It is a floor, not
// a ceiling: PTX is JIT-forward and a newer driver accepts an older version.
func defaultISA(t ptx.Target) ptx.ISAVersion {
	switch {
	case t.Suffix == ptx.Family:
		return ptx.ISA88
	case t.SM >= 100:
		return ptx.ISA85
	case t.SM >= 90:
		return ptx.ISA80
	case t.SM >= 80:
		return ptx.ISA71
	}
	return ptx.ISA70
}

func todof(format string, args ...any) error {
	return fmt.Errorf("gpu/lower/ptx: "+format, args...)
}