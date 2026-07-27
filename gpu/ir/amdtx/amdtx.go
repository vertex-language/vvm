// Package amdtx (AMD Thread eXecution) is an in-memory intermediate representation
// for AMD GPU compute kernels, shaped like ptx: virtual registers only, typed
// generic ops, module/kernel/param structure, and a canonical text form (.amdtx).
//
// The module preamble mirrors ptx's fixed three-directive opening
// (.version / .target / .address_size) — see NVIDIA PTX ISA §11.1 — but carries
// AMD concepts: a single gfx target per module and a wavefront width. Everything
// here is virtual and target-shaped-but-target-agnostic; the physical,
// amdgcn-specific work lives below in lower/amdgcn, asm/amdgcn and encoding.
package amdtx

import "fmt"

// Target identifies a single amdgcn GPU (one gfx target per module).
type Target int

const (
	GFX900  Target = iota // gfx900  Vega    GCN5      wave64
	GFX90A                // gfx90a  CDNA2    MI200     wave64, AGPRs
	GFX942                // gfx942  CDNA3    MI300     wave64, AGPRs, MFMA
	GFX1030               // gfx1030 RDNA2    WGP mode  wave32
	GFX1100               // gfx1100 RDNA3    WGP mode  wave32, dual-issue VALU
)

// gfxName is the canonical "gfxNNNN" string used in .amdtx text and ELF metadata.
func (t Target) String() string {
	switch t {
	case GFX900:
		return "gfx900"
	case GFX90A:
		return "gfx90a"
	case GFX942:
		return "gfx942"
	case GFX1030:
		return "gfx1030"
	case GFX1100:
		return "gfx1100"
	}
	return fmt.Sprintf("gfx?(%d)", int(t))
}

// Triple is the full amdgcn target triple, as it appears in .amdgcn_target and
// the ELF metadata note (e.g. "amdgcn-amd-amdhsa--gfx1100").
func (t Target) Triple() string { return "amdgcn-amd-amdhsa--" + t.String() }

// Family groups targets into their ISA generation.
type Family int

const (
	FamGCN5 Family = iota
	FamCDNA2
	FamCDNA3
	FamRDNA2
	FamRDNA3
)

// String names the ISA generation.
func (f Family) String() string {
	switch f {
	case FamGCN5:
		return "gcn5"
	case FamCDNA2:
		return "cdna2"
	case FamCDNA3:
		return "cdna3"
	case FamRDNA2:
		return "rdna2"
	case FamRDNA3:
		return "rdna3"
	}
	return "?"
}

// Family reports the ISA generation of a target.
func (t Target) Family() Family {
	switch t {
	case GFX900:
		return FamGCN5
	case GFX90A:
		return FamCDNA2
	case GFX942:
		return FamCDNA3
	case GFX1030:
		return FamRDNA2
	case GFX1100:
		return FamRDNA3
	}
	return FamGCN5
}

// IsRDNA reports whether the target is an RDNA (GFX10+) part, which uses WGP mode
// and a 32-bit EXEC mask by default (wave32).
func (t Target) IsRDNA() bool { return t.Family() == FamRDNA2 || t.Family() == FamRDNA3 }

// HasAGPRs reports whether the target has accumulation VGPRs (CDNA).
func (t Target) HasAGPRs() bool { return t.Family() == FamCDNA2 || t.Family() == FamCDNA3 }

// HasMFMA reports whether the target has matrix (MFMA) instructions (CDNA).
func (t Target) HasMFMA() bool { return t.Family() == FamCDNA2 || t.Family() == FamCDNA3 }

// DefaultWave is the natural wave size for the target.
func (t Target) DefaultWave() Wave {
	if t.IsRDNA() {
		return Wave32
	}
	return Wave64
}

// mach returns the EF_AMDGPU_MACH code for the target (ELF e_flags low byte).
// Values from llvm/include/llvm/BinaryFormat/ELF.h.
func (t Target) mach() uint32 {
	switch t {
	case GFX900:
		return 0x02c
	case GFX90A:
		return 0x03f
	case GFX942:
		return 0x04c
	case GFX1030:
		return 0x036
	case GFX1100:
		return 0x041
	}
	return 0
}

// Mach exposes the ELF EF_AMDGPU_MACH code (used by the encoders).
func (t Target) Mach() uint32 { return t.mach() }

// Wave is a wavefront size. The hardware requires a power of two in 1..64;
// only 32 and 64 are used by the targets modeled here.
type Wave int

const (
	WaveDefault Wave = 0
	Wave32      Wave = 32
	Wave64      Wave = 64
)

// Lanes returns the concrete lane count, resolving WaveDefault against target.
func (w Wave) Lanes(t Target) int {
	if w == WaveDefault {
		return int(t.DefaultWave())
	}
	return int(w)
}

// Log2 returns the wavefront size expressed as a power of two (the form the
// kernel descriptor stores). 32 -> 5, 64 -> 6.
func (w Wave) Log2(t Target) int {
	switch w.Lanes(t) {
	case 32:
		return 5
	case 64:
		return 6
	}
	return 6
}

// AddrSize is the pointer width in bits declared by .address_size (ptx §11.1.3).
// amdgcn is 64-bit flat/global; scratch may be 32-bit but the module-level
// address size is always 64 for the targets here.
type AddrSize int

const (
	Addr64 AddrSize = 64
	Addr32 AddrSize = 32
)

// AMDTXVersion is the .amdtx front-end format version.
type AMDTXVersion int

const (
	AMDTX10 AMDTXVersion = 10 // .amdtx 1.0
)

func (v AMDTXVersion) String() string { return fmt.Sprintf("%d.%d", v/10, v%10) }

// Module is a single-target compilation unit: a set of kernels and functions.
type Module struct {
	Name         string
	Target       Target
	AMDTXVersion AMDTXVersion
	AddrSize     AddrSize // .address_size (always Addr64 for these targets)
	Wave         Wave     // module-level default; kernels may override

	Kernels   kernelList
	Functions functionList
	Files     fileTable // source files for .loc debug info
}

// NewModule creates a module for a specific GPU target with sensible defaults.
func NewModule(name string, t Target) *Module {
	return &Module{
		Name:         name,
		Target:       t,
		AMDTXVersion: AMDTX10,
		AddrSize:     Addr64,
		Wave:         t.DefaultWave(),
	}
}

type kernelList struct{ items []*Kernel }

func (l *kernelList) Add(k *Kernel)    { l.items = append(l.items, k) }
func (l *kernelList) Items() []*Kernel { return l.items }
func (l *kernelList) Len() int         { return len(l.items) }

type functionList struct{ items []*Function }

func (l *functionList) Add(f *Function)    { l.items = append(l.items, f) }
func (l *functionList) Items() []*Function { return l.items }
func (l *functionList) Len() int           { return len(l.items) }

// fileTable interns source file names for .loc.
type fileTable struct{ names []string }

func (t *fileTable) Add(name string) int {
	for i, n := range t.names {
		if n == name {
			return i
		}
	}
	t.names = append(t.names, name)
	return len(t.names) - 1
}

func (t *fileTable) Name(i int) string {
	if i < 0 || i >= len(t.names) {
		return ""
	}
	return t.names[i]
}

func (t *fileTable) Items() []string { return t.names }