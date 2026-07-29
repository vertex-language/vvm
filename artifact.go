// artifact.go
package vvm

import "fmt"

// Artifact is one output of a device build — the .gvir counterpart to
// the single []byte a host Build returns.
//
// The plural is the whole point: a host build converges (many modules,
// one binary), a device build fans out (one module, one artifact per
// declared target). amdgcn emits one artifact per declared architecture
// with no JIT fallback; ptx and msl each emit one covering a floor
// version. Nothing downstream links these together — each is standalone
// input to a vendor toolchain (ptxas, the metal frontend, an AMD
// assembler).
type Artifact struct {
	// Backend is "ptx", "amdgcn", or "msl".
	Backend string

	// Arch is the concrete architecture this artifact was lowered for:
	// "sm_80", "gfx90a", "metal31". Always populated, even for msl and
	// ptx where it's a language/JIT floor rather than a binary target —
	// it's what the module declared, and it's what diagnostics name.
	Arch string

	// Filename is the conventional default name for this artifact,
	// derived from the module name plus the backend's extension (and, for
	// the one backend that fans out per-arch, the arch). Callers writing
	// to a directory should use it; callers writing to an explicit single
	// path should ignore it.
	Filename string

	// Source is the artifact's text — .ptx, .amdtx, or .metal source, as
	// produced by the matching gpu/ir/<backend>/encoding/text printer.
	// Never object code: these are the vendor toolchains' input formats.
	Source []byte

	// Excluded lists the kernels §4.3 capability gating dropped from this
	// artifact. A non-empty Excluded is not an error — gating excludes
	// per-kernel and per-artifact precisely so an unsupported feature on
	// one target doesn't fail the whole module — but it is always worth
	// surfacing, since the resulting artifact is silently missing entry
	// points the source declared.
	Excluded []Exclusion
}

// Exclusion is vvm's own normalized form of a gating exclusion. Each
// gpu/lower backend reports these in its own Result type; this is the
// one shape vvm hands back, in keeping with the repo's "no shared types
// across boundaries" principle — the conversion happens in gpudispatch.go,
// one small switch, the same way dispatch.go converts vvm.Target into
// each linker's own Target.
type Exclusion struct {
	Kernel  string // the kernel that was dropped
	Feature string // the gated feature that wasn't available
}

func (e Exclusion) String() string {
	return fmt.Sprintf("kernel %q excluded: %s unavailable", e.Kernel, e.Feature)
}