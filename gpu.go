// gpu.go
package vvm

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/gvir"
)

// DeviceOptions controls a .gvir build. Every field's zero value is the
// "full build, nothing special" default: every declared artifact, every
// kernel, no debug info, gating exclusions reported but not fatal.
type DeviceOptions struct {
	// Select narrows the build to a subset of what the module declares.
	// Empty means every declared artifact. See DeviceSelector — these
	// select, they never specify.
	Select []DeviceSelector

	// Kernels narrows the build to specific kernels by name. Empty means
	// every kernel in the module. A name that matches nothing is an
	// error, not a silent no-op.
	Kernels []string

	// Debug requests source-location emission where the backend supports
	// it. Today that's amdtx only (.file/.loc directives); ptx and msl
	// ignore it. Not an error to set for a build that includes them — a
	// mixed --target selection shouldn't have to be split in two just to
	// get debug info out of the one backend that has it.
	Debug bool

	// StrictGating turns §4.3 capability exclusions into a build failure.
	// The default (false) matches gpu/lower's own stance: gating excludes
	// a kernel from one artifact and keeps going, because whole-module
	// judgments belong to a verifier. StrictGating is for CI, where an
	// artifact silently missing an entry point is exactly the thing you
	// wanted the build to catch.
	StrictGating bool
}

// BuildDevice runs the full device pipeline for one .gvir module and
// returns every artifact it produced.
//
// This is the device counterpart to Build, and the shape difference is
// the whole story: Build converges (modules in, one binary out),
// BuildDevice fans out (one module in, one artifact per declared target
// out). There is deliberately no BuildDeviceGraph — .gvir has no host
// surface at all: no entry point, no globals, no imports, no links, no
// namespaces (see ir/gvir/README.md). A device module is kernels, funcs,
// structs and consts, full stop, so there is no import graph to resolve
// and nothing for a linker to do.
//
// Note what is *not* in this pipeline: verification. ir/verify does not
// yet cover gvir — name binding, merge annotations, the uniformity
// analysis and §4.3 capability gating all still need to land there — so
// this function hands a decoded-but-unverified module straight to
// gpu/lower. The verify.Verify call goes immediately below the decode,
// once it exists.
func BuildDevice(src []byte, opts DeviceOptions) ([]Artifact, error) {
	m, err := decodeDeviceModule(src)
	if err != nil {
		return nil, fmt.Errorf("vvm: decode: %w", err)
	}
	// TODO: verify.VerifyDevice(m) belongs here, before any lowering.
	return BuildDeviceModule(m, opts)
}

// BuildDeviceModule is BuildDevice for a caller already holding a
// *gvir.Module (e.g. hand-built via gvir's own builder API) rather than
// serialized source.
func BuildDeviceModule(m *gvir.Module, opts DeviceOptions) ([]Artifact, error) {
	declared, err := declaredArtifacts(m)
	if err != nil {
		return nil, err
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf(
			"vvm: module %q declares no target artifacts — a .gvir target section is "+
				"mandatory and must name at least one backend", m.Name)
	}

	selected, err := selectArtifacts(declared, opts.Select)
	if err != nil {
		return nil, err
	}

	lm := m
	if len(opts.Kernels) > 0 {
		lm, err = restrictKernels(m, opts.Kernels)
		if err != nil {
			return nil, err
		}
	}

	out := make([]Artifact, 0, len(selected))
	for _, sel := range selected {
		src, excluded, err := lowerArtifact(lm, sel, opts)
		if err != nil {
			return nil, fmt.Errorf("vvm: %s: %w", sel, err)
		}
		out = append(out, Artifact{
			Backend:  sel.Backend,
			Arch:     sel.Arch,
			Filename: artifactFilename(m.Name, sel),
			Source:   src,
			Excluded: excluded,
		})
	}

	if opts.StrictGating {
		if err := gatingError(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeviceTargets reports the artifacts src's own target section declares,
// in gvir's canonical artifact order, without lowering anything.
//
// This is ModuleTarget's device counterpart: same job — "what does this
// file say it's for, before I commit to building it" — but it returns a
// list, because a device target section is mandatory and multi-valued.
// There's no (t, ok) shape here for the same reason: a .gvir module that
// declares no targets is malformed, not "unconstrained."
func DeviceTargets(src []byte) ([]DeviceSelector, error) {
	m, err := decodeDeviceModule(src)
	if err != nil {
		return nil, fmt.Errorf("vvm: decode: %w", err)
	}
	return declaredArtifacts(m)
}

// restrictKernels returns a shallow copy of m carrying only the named
// kernels.
//
// Shallow is intentional and safe: nothing downstream mutates the module
// (gpu/lower reads it and builds a separate target IR), so the copy can
// share Funcs, Structs and Consts with the original. Device funcs that
// only the dropped kernels called are left in place rather than
// reachability-pruned — an unreferenced .func in the output is inert,
// and computing reachability here would duplicate analysis each backend's
// gating.go already does properly against the real call graph.
func restrictKernels(m *gvir.Module, names []string) (*gvir.Module, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}

	cp := *m
	cp.Kernels = nil
	found := make(map[string]bool, len(names))
	for _, k := range m.Kernels {
		if want[k.Name] {
			cp.Kernels = append(cp.Kernels, k)
			found[k.Name] = true
		}
	}

	for _, n := range names {
		if !found[n] {
			return nil, fmt.Errorf(
				"vvm: no kernel %q in module %q", n, m.Name)
		}
	}
	return &cp, nil
}

// gatingError renders every artifact's exclusions as one failure, for
// StrictGating. All of them, not just the first: a CI run should learn
// the whole story in one build, not one exclusion per re-run.
func gatingError(arts []Artifact) error {
	var lines []string
	for _, a := range arts {
		for _, x := range a.Excluded {
			lines = append(lines, fmt.Sprintf(
				"  %s:%s: kernel %q excluded (%s unavailable)",
				a.Backend, a.Arch, x.Kernel, x.Feature))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return fmt.Errorf(
		"vvm: capability gating excluded %d kernel(s) and strict gating is on:\n%s",
		len(lines), strings.Join(lines, "\n"))
}