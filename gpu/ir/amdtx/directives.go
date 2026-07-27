package amdtx

import "fmt"

// This file exposes the module/kernel directive data the text printer needs,
// so the encoding/text/amdtx package stays a pure formatter. It is the AMDTX
// analogue of ptx's module directives (§11.1) and performance-tuning
// directives (§11.4), mapped onto AMDHSA descriptor / metadata fields.

// ModuleDirectives is the fixed three-line preamble, ptx-style.
type ModuleDirectives struct {
	Version   string // .amdtx  <maj.min>
	Target    string // .target gfxNNNN
	AddrSize  int    // .address_size 64
	Wave      int    // .wave 32|64  (AMD-specific 4th line)
	WaveLog2  int    // wavefront size as power of two (descriptor form)
}

// Directives returns the module preamble values.
func (m *Module) Directives() ModuleDirectives {
	return ModuleDirectives{
		Version:  m.AMDTXVersion.String(),
		Target:   m.Target.String(),
		AddrSize: int(m.AddrSize),
		Wave:     m.Wave.Lanes(m.Target),
		WaveLog2: m.Wave.Log2(m.Target),
	}
}

// KernelDirective is one ".name value" launch/ABI directive line for a kernel.
type KernelDirective struct {
	Name  string
	Value string
}

// Directives returns the ABI + launch-bound directives to print inside a
// kernel body, in canonical order. Zero-valued fields are omitted. This is the
// AMD counterpart to ptx's .reqntid / .maxntid / .minnctapersm.
func (k *Kernel) Directives(t Target) []KernelDirective {
	var d []KernelDirective
	add := func(n, v string) { d = append(d, KernelDirective{n, v}) }

	if w := k.EffectiveWave(t); w != 0 {
		add(".wave", fmt.Sprintf("%d", w))
	}
	if k.GroupSegmentFixedSize != 0 {
		add(".group_segment", fmt.Sprintf("%d", k.GroupSegmentFixedSize))
	}
	if k.PrivateSegmentFixedSize != 0 {
		add(".private_segment", fmt.Sprintf("%d", k.PrivateSegmentFixedSize))
	}
	if k.KernargSize != 0 {
		add(".kernarg_size", fmt.Sprintf("%d", k.KernargSize))
	}
	if k.KernargAlign != 0 && k.KernargAlign != 8 {
		add(".kernarg_align", fmt.Sprintf("%d", k.KernargAlign))
	}
	if s := k.ReqdWorkGroupSize; s != [3]uint32{} {
		add(".reqd_workgroup_size", fmt.Sprintf("%d, %d, %d", s[0], s[1], s[2]))
	}
	if k.MaxFlatWorkGroup != 0 {
		add(".max_flat_workgroup", fmt.Sprintf("%d", k.MaxFlatWorkGroup))
	}
	if e := k.WavesPerEU; e != [2]uint32{} {
		add(".waves_per_eu", fmt.Sprintf("%d, %d", e[0], e[1]))
	}
	return d
}

// ArgDirective renders a single .param line's type/space qualifiers for the
// printer, keeping the AMD kind → ptx-style qualifier mapping in the root pkg.
func (a KernelArg) ArgDirective() string {
	switch a.Kind {
	case ArgGlobalBuffer:
		return fmt.Sprintf(".param .buffer .%s .%s %s",
			a.AddrSpace, a.resolvedType(), a.Name)
	case ArgDynamicShared:
		return fmt.Sprintf(".param .dynshared .%s .%s %s",
			a.AddrSpace, a.resolvedType(), a.Name)
	case ArgImage:
		return fmt.Sprintf(".param .image .%s %s", a.Access, a.Name)
	case ArgSampler:
		return fmt.Sprintf(".param .sampler %s", a.Name)
	default:
		return fmt.Sprintf(".param .%s %s", a.resolvedType(), a.Name)
	}
}