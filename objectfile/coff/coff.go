// objectfile/coff/coff.go
//
// Package coff produces COFF relocatable object files (raw .obj, no MS-DOS
// stub) from this package's own Section values.
//
// Supported targets:
//   - coff.TargetWindowsAMD64 → IMAGE_FILE_MACHINE_AMD64 (0x8664)
//   - coff.TargetWindowsARM64 → IMAGE_FILE_MACHINE_ARM64 (0xAA64)
//
// File layout
//
//	┌─────────────────────────────────────┐
//	│ COFF File Header       (20 bytes)   │
//	├─────────────────────────────────────┤
//	│ Section Headers    (N × 40 bytes)   │
//	├─────────────────────────────────────┤
//	│ .text  raw bytes   (padded to align)│
//	│ .text  relocs      (10 bytes each)  │
//	│ .data  raw bytes                    │
//	│ .data  relocs                       │
//	│  …                                  │
//	│ (.bss and zero-TLS: no file bytes)  │
//	├─────────────────────────────────────┤
//	│ Symbol table   (18 bytes × nSyms)   │
//	├─────────────────────────────────────┤
//	│ String table   (4-byte size + data) │
//	└─────────────────────────────────────┘
//
// Implicit addends
//
// COFF stores no addend field in relocation records. Serialize therefore
// patches Reloc.Addend into the instruction bytes at Reloc.Offset before
// emitting the section, and records zero in the relocation table entry.
//
// COMDAT (FlagLinkOnce)
//
// FlagLinkOnce emits IMAGE_SCN_LNK_COMDAT on the section and attaches an
// auxiliary section record (IMAGE_COMDAT_SELECT_ANY) to the section symbol.
// The COMDAT key is the name of the first BindingGlobal symbol in the
// section; that name is also used as the section header name so the linker
// can match identical definitions across translation units.
//
// DLLExport
//
// Any Symbol with DLLExport == true causes a .drectve section carrying
// /EXPORT:<name> linker directives to be appended at Serialize time.
package coff

import (
	"bytes"
	"io"
)

// ── Machine identifiers ───────────────────────────────────────────────────────

const (
	machineAMD64 uint16 = 0x8664 // IMAGE_FILE_MACHINE_AMD64
	machineARM64 uint16 = 0xAA64 // IMAGE_FILE_MACHINE_ARM64
)

// ── Subsystem ─────────────────────────────────────────────────────────────────

// Subsystem identifies the intended Windows subsystem. It is informational
// for a COFF object file (no Optional Header is emitted) but is recorded so
// callers that later produce an image can query it via File.Subsystem.
type Subsystem uint16

const (
	SubsystemUnknown Subsystem = 0  // IMAGE_SUBSYSTEM_UNKNOWN
	SubsystemNative  Subsystem = 1  // IMAGE_SUBSYSTEM_NATIVE
	SubsystemWindows Subsystem = 2  // IMAGE_SUBSYSTEM_WINDOWS_GUI
	SubsystemConsole Subsystem = 3  // IMAGE_SUBSYSTEM_WINDOWS_CUI (default)
	SubsystemEFI     Subsystem = 10 // IMAGE_SUBSYSTEM_EFI_APPLICATION
	SubsystemBootApp Subsystem = 16 // IMAGE_SUBSYSTEM_WINDOWS_BOOT_APPLICATION
)

// ── File ──────────────────────────────────────────────────────────────────────

// File accumulates Section values and serialises them into a complete COFF
// relocatable object file.
//
//	f := coff.NewFile(coff.TargetWindowsAMD64)
//	f.AddSection(sec)
//	b, err := f.Serialize()
//
// Format-specific options (subsystem) must be set before the first
// AddSection call.
type File struct {
	machine   uint16
	subsystem Subsystem
	sections  []Section
}

// NewFile returns a COFF File configured for the given target.
// The target must be TargetWindowsAMD64 or TargetWindowsARM64; any other
// Arch defaults to AMD64.
func NewFile(target Target) *File {
	f := &File{subsystem: SubsystemConsole}
	if target.Arch == ArchARM64 {
		f.machine = machineARM64
	} else {
		f.machine = machineAMD64
	}
	return f
}

// Subsystem returns the currently configured Windows subsystem.
func (f *File) Subsystem() Subsystem { return f.subsystem }

// SetSubsystem records the intended Windows subsystem.
// Must be called before the first AddSection call.
func (f *File) SetSubsystem(s Subsystem) { f.subsystem = s }

// AddSection appends one section in declaration order.
func (f *File) AddSection(s Section) { f.sections = append(f.sections, s) }

// Serialize assembles all accumulated sections into a complete COFF object
// file and returns the raw bytes. Safe to call more than once; each call
// re-serialises from scratch.
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