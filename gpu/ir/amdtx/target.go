package amdtx

// Family is the amdgcn generation. Wait-counter ranges, wave32 support and
// encoding tables are keyed off it.
type Family uint8

const (
	NoFamily Family = iota
	GFX9
	GFX10
	GFX11
)

func (f Family) String() string {
	return [...]string{"", "gfx9", "gfx10", "gfx11"}[f]
}

// GTE reports whether f is at least o.
func (f Family) GTE(o Family) bool { return f >= o && f != NoFamily }

// Arch is the marketing/microarchitecture line within a family. It is what
// distinguishes CDNA (MFMA, AGPRs, unified VGPR file) from RDNA.
type Arch uint8

const (
	NoArch Arch = iota
	GCN5
	CDNA2
	CDNA3
	RDNA2
	RDNA3
)

func (a Arch) String() string {
	return [...]string{"", "gcn5", "cdna2", "cdna3", "rdna2", "rdna3"}[a]
}

// Matrix is the matrix-core instruction family a target provides.
type Matrix uint8

const (
	NoMatrix Matrix = iota
	MFMA
	WMMA
)

// ScratchModel is the flat-scratch addressing model recorded per processor.
type ScratchModel uint8

const (
	NoScratch ScratchModel = iota
	ScratchAbsolute
	ScratchArchitected
)

// Target is the processor selected by .target. Exactly one per module (P1).
type Target uint8

const (
	NoTarget Target = iota
	GFX900
	GFX90A
	GFX942
	GFX1030
	GFX1100
)

type targetInfo struct {
	name        string
	family      Family
	arch        Arch
	defaultWave Wave
	wave32      bool
	agprs       bool
	matrix      Matrix
	vscnt       bool
	scratch     ScratchModel
	packedTID   bool
	preload     bool
	mach        uint32
	vmMax       int
	lgkmMax     int
	vsMax       int
	wavesPerEU  int
}

// targetTable is the §4 processor table. Adding a generation is a spec
// revision: a row here, a counter profile, an inline-constant profile, a
// per-target encoding table, and the EF_AMDGPU_MACH value.
var targetTable = map[Target]targetInfo{
	GFX900: {"gfx900", GFX9, GCN5, Wave64, false, false, NoMatrix, false,
		ScratchAbsolute, false, false, 0x02c, 63, 15, 0, 8},
	GFX90A: {"gfx90a", GFX9, CDNA2, Wave64, false, true, MFMA, false,
		ScratchAbsolute, true, true, 0x03f, 63, 15, 0, 8},
	GFX942: {"gfx942", GFX9, CDNA3, Wave64, false, true, MFMA, false,
		ScratchArchitected, true, true, 0x04c, 63, 15, 0, 8},
	GFX1030: {"gfx1030", GFX10, RDNA2, Wave32, true, false, NoMatrix, true,
		ScratchAbsolute, false, false, 0x036, 63, 63, 63, 16},
	GFX1100: {"gfx1100", GFX11, RDNA3, Wave32, true, false, WMMA, true,
		ScratchArchitected, true, false, 0x041, 63, 63, 63, 16},
}

func (t Target) String() string          { return targetTable[t].name }
func (t Target) Name() string            { return targetTable[t].name }
func (t Target) Family() Family          { return targetTable[t].family }
func (t Target) Arch() Arch              { return targetTable[t].arch }
func (t Target) DefaultWave() Wave       { return targetTable[t].defaultWave }
func (t Target) SupportsWave32() bool    { return targetTable[t].wave32 }
func (t Target) HasAGPRs() bool          { return targetTable[t].agprs }
func (t Target) Matrix() Matrix          { return targetTable[t].matrix }
func (t Target) HasVSCnt() bool          { return targetTable[t].vscnt }
func (t Target) Scratch() ScratchModel   { return targetTable[t].scratch }
func (t Target) PackedTID() bool         { return targetTable[t].packedTID }
func (t Target) HasKernargPreload() bool { return targetTable[t].preload }
func (t Target) Mach() uint32            { return targetTable[t].mach }
func (t Target) MaxWavesPerEU() int      { return targetTable[t].wavesPerEU }

// IsCDNA reports whether the target is a CDNA part, which is the gate on
// MFMA (V10) and on the unified VGPR/AGPR budget.
func (t Target) IsCDNA() bool {
	a := targetTable[t].arch
	return a == CDNA2 || a == CDNA3
}

// IsValid reports whether t names a modelled processor.
func (t Target) IsValid() bool { _, ok := targetTable[t]; return ok }

// CounterMax returns the largest legal value for a wait counter on t, or -1
// if the counter does not exist on this target (V37).
func (t Target) CounterMax(k CounterKind) int {
	i := targetTable[t]
	switch k {
	case VMCnt:
		return i.vmMax
	case LGKMCnt:
		return i.lgkmMax
	case VSCnt:
		if !i.vscnt {
			return -1
		}
		return i.vsMax
	}
	return -1
}

// TargetByName resolves a gfx identifier, for the parser's benefit.
func TargetByName(s string) (Target, bool) {
	for t, i := range targetTable {
		if i.name == s {
			return t, true
		}
	}
	return NoTarget, false
}