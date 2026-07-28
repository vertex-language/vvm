package ptx

import "fmt"

// ISAVersion is a PTX ISA version, emitted as ".version <Major>.<Minor>".
type ISAVersion struct{ Major, Minor int }

func (v ISAVersion) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// GTE reports whether v is at least o.
func (v ISAVersion) GTE(o ISAVersion) bool {
	return v.Major > o.Major || (v.Major == o.Major && v.Minor >= o.Minor)
}

// IsZero reports whether v is the unset version.
func (v ISAVersion) IsZero() bool { return v.Major == 0 && v.Minor == 0 }

// PTX ISA versions and the CUDA Toolkit release that introduced each.
var (
	ISA70 = ISAVersion{7, 0} // CUDA 11.0
	ISA71 = ISAVersion{7, 1} // CUDA 11.1
	ISA72 = ISAVersion{7, 2} // CUDA 11.2
	ISA73 = ISAVersion{7, 3} // CUDA 11.3
	ISA74 = ISAVersion{7, 4} // CUDA 11.4
	ISA75 = ISAVersion{7, 5} // CUDA 11.5
	ISA76 = ISAVersion{7, 6} // CUDA 11.6
	ISA77 = ISAVersion{7, 7} // CUDA 11.7
	ISA78 = ISAVersion{7, 8} // CUDA 11.8
	ISA80 = ISAVersion{8, 0} // CUDA 12.0
	ISA81 = ISAVersion{8, 1} // CUDA 12.1
	ISA82 = ISAVersion{8, 2} // CUDA 12.2
	ISA83 = ISAVersion{8, 3} // CUDA 12.3
	ISA84 = ISAVersion{8, 4} // CUDA 12.4
	ISA85 = ISAVersion{8, 5} // CUDA 12.5 / 12.6
	ISA86 = ISAVersion{8, 6} // CUDA 12.7
	ISA87 = ISAVersion{8, 7} // CUDA 12.8
	ISA88 = ISAVersion{8, 8} // CUDA 12.9 (family "f" targets)
	ISA90 = ISAVersion{9, 0} // CUDA 13.0 (sm_110, .blocksareclusters)
	ISA91 = ISAVersion{9, 1} // CUDA 13.1
	ISA92 = ISAVersion{9, 2} // CUDA 13.2
	ISA93 = ISAVersion{9, 3} // CUDA 13.3
)

// Suffix distinguishes base, architecture-specific, and family-specific
// targets.
type Suffix uint8

const (
	Base    Suffix = iota // sm_90
	ArchSpc               // sm_90a
	Family                // sm_100f
)

// Target is an sm_* target architecture. Unknown future targets may be
// constructed directly: Target{SM: 130, Suffix: ArchSpc}.
type Target struct {
	SM     int
	Suffix Suffix
}

func (t Target) String() string {
	switch t.Suffix {
	case ArchSpc:
		return fmt.Sprintf("sm_%da", t.SM)
	case Family:
		return fmt.Sprintf("sm_%df", t.SM)
	}
	return fmt.Sprintf("sm_%d", t.SM)
}

var (
	SM50 = Target{SM: 50}
	SM52 = Target{SM: 52}
	SM53 = Target{SM: 53}
	SM60 = Target{SM: 60}
	SM61 = Target{SM: 61}
	SM62 = Target{SM: 62}
	SM70 = Target{SM: 70}
	SM72 = Target{SM: 72}
	SM75 = Target{SM: 75}
	SM80 = Target{SM: 80}
	SM86 = Target{SM: 86}
	SM87 = Target{SM: 87}
	SM89 = Target{SM: 89}
	SM90 = Target{SM: 90}

	SM100 = Target{SM: 100}
	SM103 = Target{SM: 103}
	SM110 = Target{SM: 110} // Jetson Thor; named sm_101 in CUDA 12.8/12.9
	SM120 = Target{SM: 120}
	SM121 = Target{SM: 121}

	// Architecture-specific variants (not forward compatible).
	SM90a  = Target{SM: 90, Suffix: ArchSpc}
	SM100a = Target{SM: 100, Suffix: ArchSpc}
	SM103a = Target{SM: 103, Suffix: ArchSpc}
	SM110a = Target{SM: 110, Suffix: ArchSpc}
	SM120a = Target{SM: 120, Suffix: ArchSpc}
	SM121a = Target{SM: 121, Suffix: ArchSpc}

	// Family-specific variants; require ISA >= 8.8.
	SM100f = Target{SM: 100, Suffix: Family}
	SM103f = Target{SM: 103, Suffix: Family}
	SM110f = Target{SM: 110, Suffix: Family}
	SM120f = Target{SM: 120, Suffix: Family}
	SM121f = Target{SM: 121, Suffix: Family}
)

// TargetOpt is an extra option on the .target directive.
type TargetOpt uint8

const (
	TexmodeUnified TargetOpt = iota + 1
	TexmodeIndependent
	MapF64ToF32
	Debug
)

func (o TargetOpt) String() string {
	switch o {
	case TexmodeUnified:
		return "texmode_unified"
	case TexmodeIndependent:
		return "texmode_independent"
	case MapF64ToF32:
		return "map_f64_to_f32"
	case Debug:
		return "debug"
	}
	return ""
}

// AddrSize is the value of the .address_size directive.
type AddrSize int

const (
	Addr32 AddrSize = 32
	Addr64 AddrSize = 64
)