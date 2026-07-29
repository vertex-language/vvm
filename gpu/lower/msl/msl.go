// Package msl lowers a verified .gvir device module (ir/gvir.Module) to a
// structured MSL IR module (gpu/ir/msl.Module).
//
// It produces one artifact: .gvir declares at most one msl arch, and that arch
// is a language floor rather than a binary target (§3).
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Synthesized identifiers all carry this prefix; value.go mangles user names
// that would collide with it.
const (
	prefix      = "gvir_"
	argsParam   = prefix + "args"
	submaskName = prefix + "submask"
)

// Options controls lowering. The zero value is the default configuration.
type Options struct {
	// Arch overrides the arch taken from the module's target declaration.
	Arch string
	// Comments emits block labels and `loc` lines as trailing comments.
	Comments bool
}

// Exclusion records a kernel dropped from this artifact under §4.3 rule 2.
type Exclusion struct {
	Kernel  string
	Feature gvir.Feature
}

func (x Exclusion) String() string {
	return fmt.Sprintf("kernel %s: %s unavailable", x.Kernel, x.Feature)
}

// Result is one lowering output.
type Result struct {
	Module   *msl.Module
	Arch     string
	Version  msl.Version
	Excluded []Exclusion
}

// Lower lowers m with the default options.
func Lower(m *gvir.Module) (*Result, error) { return LowerOptions(m, Options{}) }

// LowerOptions lowers m to a single MSL module. A returned error is a §1.1
// lowering error: this artifact fails, and the rest of the module is unaffected.
func LowerOptions(m *gvir.Module, o Options) (*Result, error) {
	if m == nil {
		return nil, fmt.Errorf("msl: nil module")
	}
	arch, err := selectArch(m, o.Arch)
	if err != nil {
		return nil, err
	}
	ver, ok := archVersion[arch]
	if !ok {
		return nil, fmt.Errorf("msl: arch %q has no language revision", arch)
	}
	l := &lowerer{
		src: m, arch: arch, ver: ver, opts: o,
		emit:    map[string]bool{},
		structs: map[string]*gvir.Struct{},
		names:   map[string]bool{},
	}
	if err := l.run(); err != nil {
		return nil, err
	}
	return &Result{Module: l.out, Arch: arch, Version: ver, Excluded: l.excluded}, nil
}

// archVersion maps the §3 msl language floors to IR revisions.
var archVersion = map[string]msl.Version{
	"metal30": msl.Metal30,
	"metal31": msl.Metal31,
	"metal32": msl.Metal32,
}

func selectArch(m *gvir.Module, override string) (string, error) {
	arch := override
	if arch == "" {
		b := m.Target.Backend(gvir.BackendMSL)
		if b == nil {
			return "", fmt.Errorf("msl: module %q declares no msl target (§3)", m.Name)
		}
		if len(b.Archs) > 0 {
			arch = b.Archs[0]
		} else {
			arch = gvir.DefaultArch(gvir.BackendMSL)
		}
	}
	if !gvir.KnownArch(gvir.BackendMSL, arch) {
		if canonical, isAlias := gvir.ArchAlias(gvir.BackendMSL, arch); isAlias {
			return "", fmt.Errorf("msl: arch %q is an alias, not a canonical name — write %q (§3)", arch, canonical)
		}
		return "", fmt.Errorf("msl: unknown arch %q (§3)", arch)
	}
	return arch, nil
}

// lowerer holds module-level lowering state.
type lowerer struct {
	src  *gvir.Module
	out  *msl.Module
	arch string
	ver  msl.Version
	opts Options

	excluded []Exclusion
	emit     map[string]bool         // kernels and funcs emitted into this artifact
	structs  map[string]*gvir.Struct // structs actually emitted, for field.ptr
	names    map[string]bool         // module-scope names, for synthesized-name hygiene
}

func (l *lowerer) run() error {
	l.out = msl.NewModule(l.ver)

	// §2 gives .gvir a flat module-wide namespace; reserve every name in it so
	// synthesized module-scope names (argument-buffer structs) cannot collide.
	for _, s := range l.src.Structs {
		l.names[s.Name] = true
	}
	for _, c := range l.src.Constants {
		l.names[c.Name] = true
	}
	for _, f := range l.src.Funcs {
		l.names[f.Name] = true
	}
	for _, k := range l.src.Kernels {
		l.names[k.Name] = true
	}

	l.out.Add(&msl.CommentDecl{Text: fmt.Sprintf(
		"gvir %s module %s, lowered for %s", l.src.Version, l.src.Name, l.arch)})
	l.floatProfile()

	// §4.6: submask is the opaque per-subgroup lane mask; MSL spells its bit
	// pattern simd_vote::vote_t. Aliased once so bodies stay readable.
	l.out.Alias(submaskName, msl.Named("simd_vote::vote_t"))

	if err := l.gate(); err != nil {
		return err
	}
	if err := l.lowerStructs(); err != nil {
		return err
	}
	if err := l.lowerConsts(); err != nil {
		return err
	}
	for _, f := range l.src.Funcs {
		if !l.emit[f.Name] {
			continue
		}
		if err := l.lowerFunc(f); err != nil {
			return fmt.Errorf("func %s: %w", f.Name, err)
		}
	}
	for _, k := range l.src.Kernels {
		if !l.emit[k.Name] {
			continue
		}
		if err := l.lowerKernel(k); err != nil {
			return fmt.Errorf("kernel %s: %w", k.Name, err)
		}
	}
	return nil
}

// floatProfile realizes §11.6. Both flags are module-wide and both default off;
// Metal compiles with fast math by default, so "off" has to be said out loud.
func (l *lowerer) floatProfile() {
	if l.src.Profile.Contract {
		l.out.Add(&msl.CommentDecl{
			Text: "float_profile contract: mul+add fusion is left to the Metal frontend"})
		return
	}
	l.out.Add(&msl.CommentDecl{
		Text: "float_profile: strict IEEE, no contraction — compile with -fno-fast-math"})
	l.out.VersionGate(msl.Metal32,
		[]msl.Decl{&msl.RawDecl{Text: "#pragma METAL fp math_mode(safe)"}}, nil)
}

// unique returns a module-scope name derived from base that collides with
// nothing already declared.
func (l *lowerer) unique(base string) string {
	name := base
	for i := 1; l.names[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	l.names[name] = true
	return name
}