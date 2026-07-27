package ptx

import "fmt"

// ISAVersion is a PTX ISA version emitted as ".version <Major>.<Minor>".
type ISAVersion struct{ Major, Minor int }

func (v ISAVersion) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// GTE reports whether v >= o.
func (v ISAVersion) GTE(o ISAVersion) bool {
	return v.Major > o.Major || (v.Major == o.Major && v.Minor >= o.Minor)
}

var (
	ISA70 = ISAVersion{7, 0} // CUDA 11.0: sm_80, cp.async, mbarrier
	ISA78 = ISAVersion{7, 8} // CUDA 11.8: sm_89, sm_90
	ISA80 = ISAVersion{8, 0} // CUDA 12.0: sm_90a, wgmma, clusters
	ISA85 = ISAVersion{8, 5} // CUDA 12.5/12.6
	ISA87 = ISAVersion{8, 7} // CUDA 12.8: sm_120
	ISA88 = ISAVersion{8, 8} // CUDA 12.9: family targets (f suffix)
	ISA90 = ISAVersion{9, 0} // CUDA 13.0: sm_110, .blocksareclusters
	ISA93 = ISAVersion{9, 3} // CUDA 13.3: current default
)

// Target is an sm_* architecture. Suffix is 0, 'a' (arch-specific) or
// 'f' (family-specific). Unknown future targets may be constructed
// directly: Target{Base: 130, Suffix: 'a'}.
type Target struct {
	Base   int
	Suffix rune
}

func (t Target) String() string {
	if t.Suffix == 0 {
		return fmt.Sprintf("sm_%d", t.Base)
	}
	return fmt.Sprintf("sm_%d%c", t.Base, t.Suffix)
}

var (
	SM50 = Target{Base: 50}
	SM52 = Target{Base: 52}
	SM60 = Target{Base: 60}
	SM61 = Target{Base: 61}
	SM70 = Target{Base: 70}
	SM75 = Target{Base: 75}
	SM80 = Target{Base: 80}
	SM86 = Target{Base: 86}
	SM89 = Target{Base: 89}
	SM90 = Target{Base: 90}

	SM100 = Target{Base: 100}
	SM103 = Target{Base: 103}
	SM110 = Target{Base: 110}
	SM120 = Target{Base: 120}
	SM121 = Target{Base: 121}

	// Arch-specific variants.
	SM90a  = Target{Base: 90, Suffix: 'a'}
	SM100a = Target{Base: 100, Suffix: 'a'}
	SM103a = Target{Base: 103, Suffix: 'a'}
	SM110a = Target{Base: 110, Suffix: 'a'}
	SM120a = Target{Base: 120, Suffix: 'a'}
	SM121a = Target{Base: 121, Suffix: 'a'}

	// Family-specific variants (ISA >= 8.8).
	SM100f = Target{Base: 100, Suffix: 'f'}
	SM101f = Target{Base: 101, Suffix: 'f'}
	SM103f = Target{Base: 103, Suffix: 'f'}
	SM110f = Target{Base: 110, Suffix: 'f'}
	SM120f = Target{Base: 120, Suffix: 'f'}
	SM121f = Target{Base: 121, Suffix: 'f'}
)

// TargetOpt is an extra option on the .target directive.
type TargetOpt string

const (
	TexmodeUnified     TargetOpt = "texmode_unified"
	TexmodeIndependent TargetOpt = "texmode_independent"
	MapF64ToF32        TargetOpt = "map_f64_to_f32"
	Debug              TargetOpt = "debug"
)

// FileTable holds .file entries for .loc debug directives. Indices are
// 1-based, matching PTX.
type FileTable struct{ Names []string }

// Add appends a file and returns its 1-based index.
func (ft *FileTable) Add(name string) int {
	ft.Names = append(ft.Names, name)
	return len(ft.Names)
}

// Module is the root of the IR: one .ptx translation unit.
type Module struct {
	Version     ISAVersion
	Target      Target
	TargetOpts  []TargetOpt
	AddressSize int // 32 or 64

	BlocksAreClusters bool // .blocksareclusters (ISA >= 9.0)

	Globals   VariableList
	Kernels   KernelList
	Functions FunctionList
	Files     FileTable
}

// NewModule returns a module with the current defaults:
// .version 9.3, .target sm_90, .address_size 64.
func NewModule() *Module {
	return &Module{Version: ISA93, Target: SM90, AddressSize: 64}
}

func (m *Module) SetVersion(v ISAVersion) *Module { m.Version = v; return m }
func (m *Module) SetTarget(t Target) *Module      { m.Target = t; return m }

// KernelList is an ordered collection of kernels.
type KernelList struct{ Items []*Kernel }

func (l *KernelList) Add(k *Kernel) { l.Items = append(l.Items, k) }

// FunctionList is an ordered collection of device functions.
type FunctionList struct{ Items []*Function }

func (l *FunctionList) Add(f *Function) { l.Items = append(l.Items, f) }