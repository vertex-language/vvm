// gpudispatch.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/gvir"

	amdtxir "github.com/vertex-language/vvm/gpu/ir/amdtx"
	mslir "github.com/vertex-language/vvm/gpu/ir/msl"
	ptxir "github.com/vertex-language/vvm/gpu/ir/ptx"

	amdtxtext "github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/text"
	msltext "github.com/vertex-language/vvm/gpu/ir/msl/encoding/text"
	ptxtext "github.com/vertex-language/vvm/gpu/ir/ptx/encoding/text"

	loweramdtx "github.com/vertex-language/vvm/gpu/lower/amdtx"
	lowermsl "github.com/vertex-language/vvm/gpu/lower/msl"
	lowerptx "github.com/vertex-language/vvm/gpu/lower/ptx"
)

// lowerArtifact runs the device pipeline's two stages — gpu/lower/<backend>
// then gpu/ir/<backend>/encoding/text — for exactly one artifact.
//
// This is dispatch.go's device counterpart, and it's much smaller for a
// structural reason: the host side has to pick a cell out of an
// (arch × format) matrix with real holes in it, so most of dispatch.go is
// coverage errors. Here backend *is* the format: there is no such thing
// as "ptx, but emitted as Mach-O." One switch, three arms, no matrix.
//
// It's also where each backend's own Result type gets normalized into
// vvm's Exclusion — the same boundary conversion dispatch.go does turning
// vvm.Target into each linker's own Target. The three Result types are
// deliberately not a shared interface: they carry different things
// (amdtx's is per-arch, msl's and ptx's aren't), and pretending otherwise
// is the exact "one struct with a comment explaining which convention
// applies" the repo avoids.
func lowerArtifact(m *gvir.Module, sel DeviceSelector, opts DeviceOptions) ([]byte, []Exclusion, error) {
	switch sel.Backend {

	case BackendPTX:
		// PTX is JIT-forward: one artifact covers everything at or above
		// the declared floor, so there's no arch argument to pass — the
		// floor is already in m's target section.
		res, err := lowerptx.LowerOptions(m, lowerptx.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("gpu/lower/ptx: %w", err)
		}
		src, err := printPTX(res.Module)
		if err != nil {
			return nil, nil, err
		}
		excl := make([]Exclusion, 0, len(res.Excluded))
		for _, x := range res.Excluded {
			excl = append(excl, Exclusion{Kernel: x.Kernel, Feature: x.Feature})
		}
		return src, excl, nil

	case BackendAMDGCN:
		// The one backend that fans out: no JIT fallback, so it emits one
		// artifact per declared architecture and needs to be told which
		// one. sel.Arch is always concrete here — it came from
		// declaredArtifacts, which enumerates real declared archs, never a
		// bare "amdgcn".
		if sel.Arch == "" {
			return nil, nil, fmt.Errorf(
				"internal: amdgcn artifact reached lowering with no arch " +
					"(declaredArtifacts should have expanded it)")
		}
		res, err := loweramdtx.LowerOptions(m, sel.Arch, loweramdtx.Options{
			Debug: opts.Debug,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("gpu/lower/amdtx: %w", err)
		}
		src, err := printAMDTX(res.Module)
		if err != nil {
			return nil, nil, err
		}
		excl := make([]Exclusion, 0, len(res.Excluded))
		for _, x := range res.Excluded {
			excl = append(excl, Exclusion{Kernel: x.Kernel, Feature: x.Feature})
		}
		return src, excl, nil

	case BackendMSL:
		// MSL's declared arch is a language floor, not a binary target —
		// one artifact, and instruction selection plus register allocation
		// belong to the Metal compiler downstream, not to us.
		res, err := lowermsl.LowerOptions(m, lowermsl.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("gpu/lower/msl: %w", err)
		}
		src, err := printMSL(res.Module)
		if err != nil {
			return nil, nil, err
		}
		excl := make([]Exclusion, 0, len(res.Excluded))
		for _, x := range res.Excluded {
			excl = append(excl, Exclusion{Kernel: x.Kernel, Feature: x.Feature})
		}
		return src, excl, nil
	}

	return nil, nil, fmt.Errorf("vvm: unsupported device backend %q", sel.Backend)
}

// --- gpu/ir/<backend>/encoding/text --------------------------------------
//
// Each backend has exactly one canonical printer (there's no secondary
// "quick stringification" path that could drift from the grammar model —
// see gpu/ir/README.md), so these are thin, but they keep the import
// aliases and error wrapping out of the switch above.

func printPTX(m *ptxir.Module) ([]byte, error) {
	src, err := ptxtext.Print(m)
	if err != nil {
		return nil, fmt.Errorf("gpu/ir/ptx/encoding/text: %w", err)
	}
	return src, nil
}

func printAMDTX(m *amdtxir.Module) ([]byte, error) {
	src, err := amdtxtext.Print(m)
	if err != nil {
		return nil, fmt.Errorf("gpu/ir/amdtx/encoding/text: %w", err)
	}
	return src, nil
}

func printMSL(m *mslir.Module) ([]byte, error) {
	src, err := msltext.Print(m)
	if err != nil {
		return nil, fmt.Errorf("gpu/ir/msl/encoding/text: %w", err)
	}
	return src, nil
}