// objectfile/macho/macho.go
package macho

import (
	"bytes"
	"io"
)

// ── CPU / magic constants ─────────────────────────────────────────────────────

const (
	mhMagic64 uint32 = 0xFEEDFACF // MH_MAGIC_64 (little-endian host)

	cpuTypeX86_64 int32 = 0x01000007 // CPU_TYPE_X86_64
	cpuTypeARM64  int32 = 0x0100000C // CPU_TYPE_ARM64

	cpuSubtypeAll int32 = 0x00000000 // CPU_SUBTYPE_ALL / CPU_SUBTYPE_ARM64_ALL

	mhObject                uint32 = 0x1    // MH_OBJECT
	mhSubsectionsViaSymbols uint32 = 0x2000 // MH_SUBSECTIONS_VIA_SYMBOLS
)

// ── Platform constants for LC_BUILD_VERSION ───────────────────────────────────

// Platform identifies the Darwin platform for LC_BUILD_VERSION.
type Platform uint32

const (
	MacOS    Platform = 1  // PLATFORM_MACOS
	IOS      Platform = 2  // PLATFORM_IOS
	TVOS     Platform = 3  // PLATFORM_TVOS
	WatchOS  Platform = 4  // PLATFORM_WATCHOS
	VisionOS Platform = 11 // PLATFORM_VISIONOS
)

// ── File ──────────────────────────────────────────────────────────────────────

// File accumulates Section values and serialises them into a complete
// Mach-O relocatable object file (MH_OBJECT).
//
//	f := macho.NewFile(macho.TargetDarwinARM64)
//	f.SetMinOS(macho.MacOS, 14, 0)
//	f.AddSection(sec)
//	b, err := f.Serialize()
type File struct {
	target     Target
	cpuType    int32
	cpuSubtype int32
	sections   []Section

	// LC_BUILD_VERSION (optional)
	buildVersion bool
	bvPlatform   Platform
	bvMinOS      uint32 // packed X.Y.Z as (X<<16)|(Y<<8)|Z
	bvSDK        uint32 // same packing; set equal to bvMinOS for .o files

	// codesignReserve is accepted and stored but not yet consulted by
	// write.go's build — no __LINKEDIT space is reserved for an ad-hoc
	// signature yet. Reserved for a follow-up, same status as
	// elf.File.EnableDWARF.
	codesignReserve bool
}

// NewFile returns a Mach-O File configured for the given target.
//
// Supported targets: TargetDarwinAMD64, TargetDarwinARM64.
func NewFile(t Target) *File {
	f := &File{target: t, cpuSubtype: cpuSubtypeAll}
	switch t.Arch {
	case ArchARM64:
		f.cpuType = cpuTypeARM64
	default:
		f.cpuType = cpuTypeX86_64
	}
	return f
}

// SetMinOS configures LC_BUILD_VERSION. Call before the first AddSection.
//
//	f.SetMinOS(macho.MacOS, 14, 0) // macOS 14.0
func (f *File) SetMinOS(platform Platform, major, minor uint8) {
	f.buildVersion = true
	f.bvPlatform = platform
	f.bvMinOS = (uint32(major) << 16) | (uint32(minor) << 8)
	f.bvSDK = f.bvMinOS // SDK == minOS is accepted by ld for relocatable objects
}

// EnableCodesignReserve reserves space in __LINKEDIT for an ad-hoc codesign
// signature so codesign(1) can attach without a full re-link.
// Not yet implemented in write.go's build; reserved for a follow-up.
func (f *File) EnableCodesignReserve(on bool) { f.codesignReserve = on }

// AddSection appends one section in declaration order.
func (f *File) AddSection(s Section) { f.sections = append(f.sections, s) }

// Serialize assembles all accumulated sections into a complete Mach-O
// object file. Safe to call more than once; each call re-serialises from
// scratch.
func (f *File) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteTo is the io.WriterTo form of Serialize.
func (f *File) WriteTo(w io.Writer) (int64, error) {
	b, err := f.build()
	if err != nil {
		return 0, err
	}
	n, werr := w.Write(b)
	return int64(n), werr
}