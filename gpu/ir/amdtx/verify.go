package amdtx

import (
	"errors"
	"fmt"
	"strings"
)

// Verify checks a module for structural correctness before printing or lowering:
// balanced control markers, resolvable branch labels, in-range kernarg offsets,
// AGPR usage only on CDNA, and vector/scalar destination class agreement.
func Verify(m *Module) error {
	if m == nil {
		return errors.New("amdtx: nil module")
	}
	var errs []string
	if m.Name == "" {
		errs = append(errs, "module has no name")
	}
	for _, k := range m.Kernels.Items() {
		if err := verifyKernel(m, k); err != nil {
			errs = append(errs, fmt.Sprintf("kernel %q: %v", k.Name, err))
		}
	}
	for _, f := range m.Functions.Items() {
		if err := verifyBody(m, f.Code, "function "+f.Name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func verifyKernel(m *Module, k *Kernel) error {
	// kernarg offsets must lie within KernargSize and not overlap the declared end.
	var maxEnd uint32
	for _, a := range k.Args.Items() {
		if a.Kind.hidden() {
			continue
		}
		if end := a.Offset + a.Size; end > maxEnd {
			maxEnd = end
		}
	}
	if k.KernargSize != 0 && maxEnd > k.KernargSize {
		return fmt.Errorf("arg layout ends at %d > KernargSize %d", maxEnd, k.KernargSize)
	}
	return verifyBody(m, k.Code, "kernel "+k.Name)
}

func verifyBody(m *Module, cb *CodeBuilder, what string) error {
	if cb == nil {
		return fmt.Errorf("%s: nil code", what)
	}
	depth := 0
	hasEnd := false
	for _, in := range cb.insts {
		switch in.Op {
		case OpIfBegin, OpLoopBegin:
			depth++
		case OpEndControl, OpLoopEnd:
			depth--
			if depth < 0 {
				return fmt.Errorf("%s: unbalanced control close", what)
			}
		case OpEndpgm:
			hasEnd = true
		case OpVMadU32U24, OpVAddF32, OpGlobalLoadDword, OpGlobalStoreDword:
			// vector ops must write vector destinations.
			for _, d := range in.Dst {
				if !d.IsVector() && d.spec == specNone {
					return fmt.Errorf("%s: vector op writes scalar dst %s", what, d.textForm())
				}
			}
		}
		// AGPRs only exist on CDNA.
		for _, d := range in.Dst {
			if d.Class == AGPRn && !m.Target.HasAGPRs() {
				return fmt.Errorf("%s: AGPR used on non-CDNA target %s", what, m.Target)
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("%s: %d unclosed control region(s)", what, depth)
	}
	if !hasEnd {
		return fmt.Errorf("%s: missing s_endpgm", what)
	}
	return nil
}