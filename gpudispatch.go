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

func lowerArtifact(m *gvir.Module, sel DeviceSelector, opts DeviceOptions) ([]byte, []Exclusion, error) {
	switch sel.Backend {

	case BackendPTX:
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
			excl = append(excl, Exclusion{Kernel: x.Kernel, Feature: x.Feature.String()})
		}
		return src, excl, nil

	case BackendAMDGCN:
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
			excl = append(excl, Exclusion{Kernel: x.Kernel, Feature: x.Feature.String()})
		}
		return src, excl, nil

	case BackendMSL:
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
			excl = append(excl, Exclusion{Kernel: x.Kernel, Feature: x.Feature.String()})
		}
		return src, excl, nil
	}

	return nil, nil, fmt.Errorf("vvm: unsupported device backend %q", sel.Backend)
}

func printPTX(m *ptxir.Module) ([]byte, error) {
	src, err := ptxtext.Print(m)
	if err != nil {
		return nil, fmt.Errorf("gpu/ir/ptx/encoding/text: %w", err)
	}
	return []byte(src), nil
}

func printAMDTX(m *amdtxir.Module) ([]byte, error) {
	src, err := amdtxtext.Print(m)
	if err != nil {
		return nil, fmt.Errorf("gpu/ir/amdtx/encoding/text: %w", err)
	}
	return []byte(src), nil
}

func printMSL(m *mslir.Module) ([]byte, error) {
	src, err := msltext.Print(m)
	if err != nil {
		return nil, fmt.Errorf("gpu/ir/msl/encoding/text: %w", err)
	}
	return []byte(src), nil
}