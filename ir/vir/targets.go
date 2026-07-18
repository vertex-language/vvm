// targets.go
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

// ---------------------------------------------------------------------------
// Inline assembly: dialect legality and register tables (§4, §10.6).
// ---------------------------------------------------------------------------

// DialectsForArchitecture lists which asm dialects are valid for an
// architecture (§4 Extensibility, §9.34). An arch absent from this map has
// no known assembly story yet and rejects all asm blocks.
var DialectsForArchitecture = map[string][]AsmDialect{
	"x86_64": {DialectIntel, DialectATT},
	"x86":    {DialectIntel, DialectATT},
	"arm":    {DialectA32, DialectT32},
	"armeb":  {DialectA32, DialectT32},

	"aarch64":    {DialectNative},
	"aarch64_be": {DialectNative},
	// TODO(§4): riscv/powerpc/mips/loongarch/s390x are native-dialect
	// architectures per the grammar, but no register table is wired for
	// them yet (below), so asm blocks on these targets are structurally
	// rejected until that data lands.
}

func IsDialectValidForArchitecture(arch string, d AsmDialect) bool {
	for _, x := range DialectsForArchitecture[arch] {
		if x == d {
			return true
		}
	}
	return false
}

// RegisterClass groups a register by hardware role (§4 Register table shape).
type RegisterClass string

const (
	RegisterClassGeneralPurpose RegisterClass = "gpr"
	RegisterClassVector         RegisterClass = "vector"
	RegisterClassFlags          RegisterClass = "flags"
)

// RegisterInfo is one row of a dialect's register table (§4).
type RegisterInfo struct {
	WidthBits      int
	Class          RegisterClass
	PhysicalSlot   string
	Reserved       bool
}

// X86RegisterTable covers the common x86_64 GPRs at both their 64-bit and
// 32-bit (sub-register) spellings, per the spec's own rax/eax example.
var X86RegisterTable = map[string]RegisterInfo{
	"rax": {64, RegisterClassGeneralPurpose, "RAX", false}, "eax": {32, RegisterClassGeneralPurpose, "RAX", false},
	"rbx": {64, RegisterClassGeneralPurpose, "RBX", false}, "ebx": {32, RegisterClassGeneralPurpose, "RBX", false},
	"rcx": {64, RegisterClassGeneralPurpose, "RCX", false}, "ecx": {32, RegisterClassGeneralPurpose, "RCX", false},
	"rdx": {64, RegisterClassGeneralPurpose, "RDX", false}, "edx": {32, RegisterClassGeneralPurpose, "RDX", false},
	"rsi": {64, RegisterClassGeneralPurpose, "RSI", false}, "esi": {32, RegisterClassGeneralPurpose, "RSI", false},
	"rdi": {64, RegisterClassGeneralPurpose, "RDI", false}, "edi": {32, RegisterClassGeneralPurpose, "RDI", false},
	"rbp": {64, RegisterClassGeneralPurpose, "RBP", false}, "ebp": {32, RegisterClassGeneralPurpose, "RBP", false},
	"rsp": {64, RegisterClassGeneralPurpose, "RSP", true}, "esp": {32, RegisterClassGeneralPurpose, "RSP", true},
	"r8": {64, RegisterClassGeneralPurpose, "R8", false}, "r8d": {32, RegisterClassGeneralPurpose, "R8", false},
	"r9": {64, RegisterClassGeneralPurpose, "R9", false}, "r9d": {32, RegisterClassGeneralPurpose, "R9", false},
	"r10": {64, RegisterClassGeneralPurpose, "R10", false}, "r10d": {32, RegisterClassGeneralPurpose, "R10", false},
	"r11": {64, RegisterClassGeneralPurpose, "R11", false}, "r11d": {32, RegisterClassGeneralPurpose, "R11", false},
	"r12": {64, RegisterClassGeneralPurpose, "R12", false}, "r12d": {32, RegisterClassGeneralPurpose, "R12", false},
	"r13": {64, RegisterClassGeneralPurpose, "R13", false}, "r13d": {32, RegisterClassGeneralPurpose, "R13", false},
	"r14": {64, RegisterClassGeneralPurpose, "R14", false}, "r14d": {32, RegisterClassGeneralPurpose, "R14", false},
	"r15": {64, RegisterClassGeneralPurpose, "R15", false}, "r15d": {32, RegisterClassGeneralPurpose, "R15", false},
}

// AArch64RegisterTable covers the GPRs (x-form/w-form) plus the reserved
// frame pointer / link register, per the spec's x0/x30 example.
var AArch64RegisterTable = map[string]RegisterInfo{
	"sp": {64, RegisterClassGeneralPurpose, "SP", true},
}

func init() {
	for i := 0; i <= 28; i++ {
		name64 := registerName("x", i)
		name32 := registerName("w", i)
		phys := registerName("X", i)
		AArch64RegisterTable[name64] = RegisterInfo{64, RegisterClassGeneralPurpose, phys, false}
		AArch64RegisterTable[name32] = RegisterInfo{32, RegisterClassGeneralPurpose, phys, false}
	}
	AArch64RegisterTable["x29"] = RegisterInfo{64, RegisterClassGeneralPurpose, "X29", true} // frame pointer
	AArch64RegisterTable["w29"] = RegisterInfo{32, RegisterClassGeneralPurpose, "X29", true}
	AArch64RegisterTable["x30"] = RegisterInfo{64, RegisterClassGeneralPurpose, "X30", true} // link register
	AArch64RegisterTable["w30"] = RegisterInfo{32, RegisterClassGeneralPurpose, "X30", true}
}

func registerName(prefix string, n int) string {
	digits := [...]string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
		"20", "21", "22", "23", "24", "25", "26", "27", "28", "29", "30"}
	return prefix + digits[n]
}

// ARMRegisterTable covers the 32-bit ARM (a32/t32) integer register file.
var ARMRegisterTable = map[string]RegisterInfo{
	"r0": {32, RegisterClassGeneralPurpose, "R0", false}, "r1": {32, RegisterClassGeneralPurpose, "R1", false},
	"r2": {32, RegisterClassGeneralPurpose, "R2", false}, "r3": {32, RegisterClassGeneralPurpose, "R3", false},
	"r4": {32, RegisterClassGeneralPurpose, "R4", false}, "r5": {32, RegisterClassGeneralPurpose, "R5", false},
	"r6": {32, RegisterClassGeneralPurpose, "R6", false}, "r7": {32, RegisterClassGeneralPurpose, "R7", false},
	"r8": {32, RegisterClassGeneralPurpose, "R8", false}, "r9": {32, RegisterClassGeneralPurpose, "R9", false},
	"r10": {32, RegisterClassGeneralPurpose, "R10", false}, "r11": {32, RegisterClassGeneralPurpose, "R11", false},
	"r12": {32, RegisterClassGeneralPurpose, "R12", false},
	"sp":  {32, RegisterClassGeneralPurpose, "R13", true},
	"lr":  {32, RegisterClassGeneralPurpose, "R14", true},
	"pc":  {32, RegisterClassGeneralPurpose, "R15", true},
}

// RegisterTableForArchitecture returns the register table for arch, or nil
// if none is wired yet (in which case asm blocks on that arch are
// rejected, §9.34/35).
func RegisterTableForArchitecture(arch string) map[string]RegisterInfo {
	switch arch {
	case "x86_64", "x86":
		return X86RegisterTable
	case "aarch64", "aarch64_be":
		return AArch64RegisterTable
	case "arm", "armeb":
		return ARMRegisterTable
	}
	return nil
}