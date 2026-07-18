package aarch64

// Arch selects which §10.1 A64 architecture Lower serializes for. Unlike
// lower/arm, the two archs differ only in the byte order of multi-byte
// scalars in global Data: A64 instruction words are architecturally
// little-endian in both endiannesses (the architecture is bi-endian for
// data only), so there is no BE-32/BE-8 distinction, no link-time .text
// word swap, and no $a/$d mapping-symbol interop concern for code. Code
// bytes are byte-for-byte identical for both archs.
//
// Big() must therefore never be consulted by the instruction encoder —
// only by the global-data writer (isel.go's dataw) and the downstream ELF
// container (EI_DATA, e_flags), which is arrow 5's job.
type Arch string

const (
	ArchAArch64   Arch = "aarch64"    // little-endian data (canonical §10.1 spelling)
	ArchAArch64BE Arch = "aarch64_be" // big-endian data (canonical §10.1 spelling)
)

// Big reports whether multi-byte scalars in global Data are serialized
// big-endian. It says nothing about Code, which is always little-endian.
func (a Arch) Big() bool { return a == ArchAArch64BE }

func (a Arch) valid() bool { return a == ArchAArch64 || a == ArchAArch64BE }