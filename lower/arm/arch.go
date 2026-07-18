package arm

// Arch selects which §10.1 A32 architecture Lower serializes for. The two
// share instruction semantics, AAPCS, layout, and fixup shapes; they differ
// only in byte order — of instruction words in Code and of multi-byte
// scalars in global Data.
//
// Object convention for ArchARMEB (deliberate): big-endian code AND
// big-endian data — the classic armeb relocatable-object layout consumed
// by both BE-32 and BE-8 systems. The BE-8 execution format (big-endian
// data, little-endian instructions — the only big-endian mode ARMv7 still
// supports) is produced at final link by word-swapping executable ARM
// regions and setting EF_ARM_BE8, mirroring binutils' --be8. That is
// arrow 5's job (the `link` package), not this one's. Because this backend
// emits pure A32 with no inline literal pools, the link-time swap is a
// uniform 4-byte reversal over .text; `link` should still emit $a/$d
// mapping symbols for third-party BE-8 linker interop.
type Arch string

const (
	ArchARM   Arch = "arm"   // little-endian (canonical §10.1 spelling)
	ArchARMEB Arch = "armeb" // big-endian (canonical §10.1 spelling)
)

// Big reports whether words and scalars are serialized big-endian.
func (a Arch) Big() bool { return a == ArchARMEB }

func (a Arch) valid() bool { return a == ArchARM || a == ArchARMEB }