// gputarget.go
package vvm

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/gvir"
)

// The three device backend names, as spelled on the CLI and in
// DeviceSelector.Backend. These are vvm's own strings, deliberately not
// gvir.Backend values — same boundary discipline target.go describes for
// vvm.Target vs. the per-format linker Targets. declaredArtifacts below
// is the one place that translates.
const (
	BackendPTX    = "ptx"
	BackendAMDGCN = "amdgcn"
	BackendMSL    = "msl"
)

// DeviceSelector names a device build target: a backend, optionally
// narrowed to one architecture.
//
// Unlike vvm.Target, a selector never *specifies* a target — it only
// *selects* among the targets the module already declared. A .gvir
// module's target section is mandatory and multi-valued, so there is no
// "no declaration, supply one on the command line" case the way there is
// for a pure-compute .vir module. Everything a device build could
// produce is fixed by the source; --target only narrows it.
//
// Spellings:
//
//	ptx              // every declared ptx artifact (there is at most one)
//	ptx:sm_80        // that specific one
//	amdgcn           // every declared amdgcn arch
//	amdgcn:gfx90a    // just that arch
//	msl:metal31
type DeviceSelector struct {
	Backend string // BackendPTX, BackendAMDGCN, or BackendMSL
	Arch    string // "" means "every declared arch for this backend"
}

func (s DeviceSelector) String() string {
	if s.Arch == "" {
		return s.Backend
	}
	return s.Backend + ":" + s.Arch
}

// ParseDeviceSelector parses one "backend[:arch]" spelling. No alias
// resolution happens here: gvir records alias spellings (sm90, gfx11,
// metal3.2) specifically so they can be *rejected* with a useful
// message, never silently rewritten (see ir/gvir/README.md §3). The
// rejection itself happens in selectArtifacts, where the declared list
// is available to compare against.
func ParseDeviceSelector(s string) (DeviceSelector, error) {
	backend, arch, hasArch := strings.Cut(strings.TrimSpace(s), ":")
	backend = strings.TrimSpace(backend)
	arch = strings.TrimSpace(arch)

	switch backend {
	case BackendPTX, BackendAMDGCN, BackendMSL:
	case "":
		return DeviceSelector{}, fmt.Errorf("vvm: empty device target selector")
	default:
		return DeviceSelector{}, fmt.Errorf(
			"vvm: unknown device backend %q (want %s, %s, or %s)",
			backend, BackendPTX, BackendAMDGCN, BackendMSL)
	}
	if hasArch && arch == "" {
		return DeviceSelector{}, fmt.Errorf(
			"vvm: device target %q has a trailing \":\" with no arch — write %q for "+
				"every declared arch, or %q:<arch> for one", s, backend, backend)
	}
	return DeviceSelector{Backend: backend, Arch: arch}, nil
}

// ParseDeviceSelectors parses a comma-separated list, the form --target
// takes on the CLI ("ptx,msl", "amdgcn:gfx90a,ptx:sm_80").
func ParseDeviceSelectors(list string) ([]DeviceSelector, error) {
	var out []DeviceSelector
	for _, part := range strings.Split(list, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		sel, err := ParseDeviceSelector(part)
		if err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("vvm: --target was given but names no device targets")
	}
	return out, nil
}

// toGvirBackend converts vvm's own backend string into gvir's
// BackendKind — the boundary conversion declaredArtifacts does in the
// other direction, needed here because gvir.ArchAlias is keyed by
// BackendKind (aliases are per-backend: "gfx11" only means anything for
// amdgcn). Returns "" for anything outside the three known backends,
// which simply won't match any entry in gvir.ArchAliases.
func toGvirBackend(b string) gvir.BackendKind {
	switch b {
	case BackendPTX:
		return gvir.BackendPTX
	case BackendAMDGCN:
		return gvir.BackendAMDGCN
	case BackendMSL:
		return gvir.BackendMSL
	}
	return ""
}

// declaredArtifacts converts the module's own target section into the
// ordered list of artifacts a full build would produce.
//
// The order is gvir's, not ours, and it matters: artifacts are ordered
// ptx, then amdgcn archs in declaration order, then msl — the order the
// availability bitmask depends on (ir/gvir/README.md §4). Anything that
// reorders this list is wrong even if it produces the same set.
//
// Artifacts() lives on *gvir.Target, not *gvir.Module (targets.go) — a
// module with no target section at all has m.Target == nil, and
// (*gvir.Target).Artifacts() already handles a nil receiver by
// returning nil, so a malformed module falls through cleanly to
// BuildDeviceModule's own "declares no target artifacts" check rather
// than panicking here.
func declaredArtifacts(m *gvir.Module) ([]DeviceSelector, error) {
	arts := m.Target.Artifacts()
	out := make([]DeviceSelector, 0, len(arts))
	for _, a := range arts {
		var backend string
		switch a.Backend {
		case gvir.BackendPTX:
			backend = BackendPTX
		case gvir.BackendAMDGCN:
			backend = BackendAMDGCN
		case gvir.BackendMSL:
			backend = BackendMSL
		default:
			// Fail loudly: a backend gvir knows about and vvm doesn't means
			// this file is out of date, not that the artifact is skippable.
			return nil, fmt.Errorf(
				"vvm: module %q declares backend %v, which this package has no "+
					"lowering route for (known: %s, %s, %s)",
				m.Name, a.Backend, BackendPTX, BackendAMDGCN, BackendMSL)
		}
		out = append(out, DeviceSelector{Backend: backend, Arch: a.Arch})
	}
	return out, nil
}

// selectArtifacts filters declared by sels, preserving declared's order.
// An empty sels means "everything".
//
// Every selector must match at least one declared artifact. A selector
// that matches nothing is an error, not a silent no-op: "I asked for
// sm_90 and got a successful build" must never mean "you got sm_80."
func selectArtifacts(declared, sels []DeviceSelector) ([]DeviceSelector, error) {
	if len(sels) == 0 {
		return declared, nil
	}

	matched := make([]bool, len(sels))
	var out []DeviceSelector
	for _, art := range declared {
		for i, sel := range sels {
			if sel.Backend != art.Backend {
				continue
			}
			if sel.Arch != "" && sel.Arch != art.Arch {
				continue
			}
			matched[i] = true
			out = append(out, art)
			break
		}
	}

	for i, ok := range matched {
		if ok {
			continue
		}
		return nil, unmatchedSelectorError(sels[i], declared)
	}
	return out, nil
}

// unmatchedSelectorError builds the diagnostic for a --target that names
// nothing the module declared. It checks the alias table first, because
// "gfx11 is an alias, write gfx1100" is a far better message than
// "gfx11 is not declared" for what is almost always a spelling mistake
// rather than a genuinely absent target.
func unmatchedSelectorError(sel DeviceSelector, declared []DeviceSelector) error {
	if sel.Arch != "" {
		if canon, isAlias := gvir.ArchAlias(toGvirBackend(sel.Backend), sel.Arch); isAlias {
			return fmt.Errorf(
				"vvm: %s: %q is an alias spelling — write %q (§3)",
				sel, sel.Arch, canon)
		}
	}
	names := make([]string, len(declared))
	for i, d := range declared {
		names[i] = d.String()
	}
	return fmt.Errorf(
		"vvm: %s is not declared by this module (declared: %s)",
		sel, strings.Join(names, ", "))
}

// artifactFilename derives the conventional output name for one
// artifact.
//
// The arch appears in the name only for the backend that actually fans
// out per-arch. amdgcn emits one artifact per declared architecture, so
// "reduce.amdtx" would be ambiguous the moment a module declares two;
// ptx and msl each emit exactly one, so "reduce.ptx" / "reduce.metal"
// stay unambiguous and match what every other tool in those ecosystems
// expects a file to be called.
func artifactFilename(moduleName string, sel DeviceSelector) string {
	switch sel.Backend {
	case BackendPTX:
		return moduleName + ".ptx"
	case BackendMSL:
		return moduleName + ".metal"
	case BackendAMDGCN:
		return moduleName + "." + sel.Arch + ".amdtx"
	default:
		// Unreachable: sel came from declaredArtifacts, which already
		// rejected anything outside the three backends.
		return moduleName + "." + sel.Backend
	}
}