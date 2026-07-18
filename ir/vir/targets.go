package vir

// Canonical target vocabularies (§10.1–§10.3) and the alias tables that are
// resolved only at the build-system boundary (§10.5). The verifier consults
// the canonical sets and *rejects* anything found in the alias maps.

var CanonicalArch = map[string]bool{
	"x86": true, "x86_64": true,
	"arm": true, "armeb": true,
	"aarch64": true, "aarch64_be": true,
	"riscv32": true, "riscv64": true,
	"powerpc": true, "powerpc64": true, "powerpc64le": true,
	"mips32": true, "mips32el": true, "mips64": true, "mips64el": true,
	"loongarch64": true,
	"s390x":       true,
}

var ArchAliases = map[string]string{
	"i386": "x86", "i686": "x86", "amd64": "x86_64", "x64": "x86_64",
	"arm32": "arm",
	"arm64": "aarch64", "arm64e": "aarch64", "arm64ec": "aarch64",
	"rv32": "riscv32", "rv64": "riscv64",
	"ppc": "powerpc", "ppc64": "powerpc64",
	"mips": "mips32", "mipsel": "mips32el",
	"systemz": "s390x",
}

var CanonicalOS = map[string]bool{
	"linux": true, "macos": true, "ios": true, "watchos": true,
	"tvos": true, "visionos": true, "windows": true, "android": true,
	"freebsd": true, "netbsd": true, "openbsd": true,
	"uefi": true, "none": true,
}

var OSAliases = map[string]string{
	"darwin": "macos", "win32": "windows", "nt": "windows",
	"bsd": "freebsd", "freestanding": "none", "bare": "none", "baremetal": "none",
}

var CanonicalABI = map[string]bool{
	"gnu": true, "musl": true, "msvc": true,
	"eabi": true, "eabihf": true, "aapcs64": true, "macho": true,
}

// PointerBits returns the address width an arch fixes (§10.1).
func PointerBits(arch string) int {
	switch arch {
	case "x86", "arm", "armeb", "riscv32", "powerpc", "mips32", "mips32el":
		return 32
	}
	return 64
}

// BinFormat classifies the object/link format an OS implies (§7.4 tables).
type BinFormat int

const (
	FormatELF BinFormat = iota
	FormatMachO
	FormatPE
)

func FormatOf(os string) BinFormat {
	switch os {
	case "macos", "ios", "watchos", "tvos", "visionos":
		return FormatMachO
	case "windows", "uefi":
		return FormatPE
	}
	return FormatELF
}