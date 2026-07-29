package pe

// ── Machine types ─────────────────────────────────────────────────────────────

const (
	imageMachineI386  = uint16(0x014C)
	imageMachineARMNT = uint16(0x01C4)
	imageMachineAMD64 = uint16(0x8664)
	imageMachineARM64 = uint16(0xAA64)
)

// ── Optional header magic ─────────────────────────────────────────────────────

const imageNTOptionalHdr64Magic = uint16(0x020B)

// ── COFF file characteristics ─────────────────────────────────────────────────

const (
	imageFileRelocsStripped    = uint16(0x0001)
	imageFileExecutableImage   = uint16(0x0002)
	imageFileLargeAddressAware = uint16(0x0020)
	imageFileDLL               = uint16(0x2000)
)

// ── PE section characteristics ────────────────────────────────────────────────

const (
	imageSCNCntCode              = uint32(0x00000020)
	imageSCNCntInitializedData   = uint32(0x00000040)
	imageSCNCntUninitializedData = uint32(0x00000080)
	imageSCNLnkInfo              = uint32(0x00000200)
	imageSCNLnkRemove            = uint32(0x00000800)
	imageSCNMemDiscardable       = uint32(0x02000000)
	imageSCNMemExecute           = uint32(0x20000000)
	imageSCNMemRead              = uint32(0x40000000)
	imageSCNMemWrite             = uint32(0x80000000)
)

// ── Data directory indices ────────────────────────────────────────────────────

const (
	dirExport    = 0
	dirImport    = 1
	dirException = 3
	dirBaseReloc = 5
	dirIAT       = 12
	dirCount     = 16
)

// ── Subsystem ─────────────────────────────────────────────────────────────────

type Subsystem uint16

const (
	SubsystemWindowsGUI           Subsystem = 2
	SubsystemWindowsCUI           Subsystem = 3
	SubsystemEFIApplication       Subsystem = 10
	SubsystemEFIBootServiceDriver Subsystem = 11
	SubsystemEFIRuntimeDriver     Subsystem = 12
	SubsystemEFIROM               Subsystem = 13
)

func defaultSubsystem(t Target) Subsystem {
	if t.OS == OSUEFI {
		return SubsystemEFIApplication
	}
	return SubsystemWindowsCUI
}

// ── DLL characteristics ───────────────────────────────────────────────────────

const (
	imageDllCharHighEntropyVA       = uint16(0x0020)
	imageDllCharDynamicBase         = uint16(0x0040)
	imageDllCharNXCompat            = uint16(0x0100)
	imageDllCharNoSEH               = uint16(0x0400)
	imageDllCharTerminalServerAware = uint16(0x8000)
)

// ── AMD64 COFF relocation types ───────────────────────────────────────────────

const (
	RelAMD64Absolute = uint32(0x0000)
	RelAMD64Addr64   = uint32(0x0001)
	RelAMD64Addr32   = uint32(0x0002)
	RelAMD64Addr32NB = uint32(0x0003)
	RelAMD64Rel32    = uint32(0x0004)
	RelAMD64Rel32_1  = uint32(0x0005)
	RelAMD64Rel32_2  = uint32(0x0006)
	RelAMD64Rel32_3  = uint32(0x0007)
	RelAMD64Rel32_4  = uint32(0x0008)
	RelAMD64Rel32_5  = uint32(0x0009)
	RelAMD64Section  = uint32(0x000A)
	RelAMD64SecRel   = uint32(0x000B)
	RelAMD64SecRel7  = uint32(0x000C)
	RelAMD64Token    = uint32(0x000D)
)

// ── ARM64 COFF relocation types ───────────────────────────────────────────────

const (
	RelARM64Absolute      = uint32(0x0000)
	RelARM64Addr32        = uint32(0x0001)
	RelARM64Addr32NB      = uint32(0x0002)
	RelARM64Branch26      = uint32(0x0003)
	RelARM64PagebaseRel21 = uint32(0x0004)
	RelARM64Rel21         = uint32(0x0005)
	RelARM64PageOffset12A = uint32(0x0006)
	RelARM64PageOffset12L = uint32(0x0007)
	RelARM64SecRel        = uint32(0x0008)
	RelARM64SecRelLow12A  = uint32(0x0009)
	RelARM64SecRelHigh12A = uint32(0x000A)
	RelARM64SecRelLow12L  = uint32(0x000B)
	RelARM64Token         = uint32(0x000C)
	RelARM64Section       = uint32(0x000D)
	RelARM64Addr64        = uint32(0x000E)
	RelARM64Branch19      = uint32(0x000F)
	RelARM64Branch14      = uint32(0x0010)
	RelARM64Rel32         = uint32(0x0011)
)

// ── Base relocation entry types ───────────────────────────────────────────────

const (
	baseRelocAbsolute = 0
	baseRelocDir64    = 10
)

// ── Structure sizes ───────────────────────────────────────────────────────────

const (
	sizeDOSStub        = 64
	sizePESig          = 4
	sizeCOFFHdr        = 20
	sizeOptHdr64       = 240
	sizeSectionHdr     = 40
	sizeImportDesc     = 20
	sizeExportDir      = 40 // sizeof(IMAGE_EXPORT_DIRECTORY)
	sizeImportObjHdr   = 20 // sizeof(IMPORT_OBJECT_HEADER)
	sizeBaseRelocBlock = 8
)

// ── PE file / section alignment ───────────────────────────────────────────────

const (
	peFileAlign = uint64(0x200)
	peSectAlign = uint64(0x1000)
)

func imageBaseFor(ot OutputType) uint64 {
	switch ot {
	case OutputExec:
		return 0x0000000000400000
	case OutputPIE:
		return 0x0000000140000000
	default: // OutputShared
		return 0x0000000010000000
	}
}

func coreBaseVA(ot OutputType) uint64 {
	if ot == OutputExec {
		return 0x400000
	}
	return 0
}

func toRVA(va, base uint64) uint32 { return uint32(va - base) }