package arm

// Arch selects which §10.1 A32 architecture Lower serializes for. The two
// share instruction semantics, AAPCS, layout, and fixup shapes; they differ
// only in byte order — of instruction words in Code and of multi-byte
// scalars in global Data.
type Arch string

const (
	ArchARM   Arch = "arm"   // little-endian (canonical §10.1 spelling)
	ArchARMEB Arch = "armeb" // big-endian (canonical §10.1 spelling)
)

// Big reports whether words and scalars are serialized big-endian.
func (a Arch) Big() bool { return a == ArchARMEB }

func (a Arch) valid() bool { return a == ArchARM || a == ArchARMEB }