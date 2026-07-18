// objectfile/elf/elf.go
package elf

import (
	"bytes"
	"fmt"
	"io"
)

// ── ELF identification constants ──────────────────────────────────────────

const (
	eiClass   = 4
	eiData    = 5
	eiVersion = 6
	eiOSABI   = 7
)

const (
	elfClass32 = 1
	elfClass64 = 2
)

const elfData2LSB = 1
const evCurrent = 1
const etRel = 1

// e_machine values for supported architectures.
const (
	emI386    uint16 = 3
	emX86_64  uint16 = 62
	emAARCH64 uint16 = 183
	emRISCV   uint16 = 243
)

// ── OSABI ─────────────────────────────────────────────────────────────────

// OSABI identifies the OS/ABI written to e_ident[EI_OSABI].
type OSABI uint8

const (
	OSABI_None       OSABI = 0
	OSABI_Linux      OSABI = 3
	OSABI_FreeBSD    OSABI = 9
	OSABI_OpenBSD    OSABI = 12
	OSABI_Standalone OSABI = 255
)

// ── File ──────────────────────────────────────────────────────────────────

// File accumulates Section values and serialises them into a complete ELF
// relocatable object file (ET_REL).
//
//	f := elf.NewFile(elf.TargetLinuxAMD64)
//	f.AddSection(sec)
//	b, err := f.Serialize()
type File struct {
	target   Target
	machine  uint16
	is64     bool
	osabi    OSABI
	dwarf    bool
	gnuStack bool
	sections []Section
}

// NewFile returns an ELF File configured for the given target.
func NewFile(t Target) *File {
	f := &File{
		target:   t,
		osabi:    OSABI_None,
		gnuStack: true, // emit .note.GNU-stack by default (marks stack non-exec)
	}
	switch t.Arch {
	case ArchAMD64:
		f.machine, f.is64 = emX86_64, true
	case ArchARM64:
		f.machine, f.is64 = emAARCH64, true
	case ArchRISCV64:
		f.machine, f.is64 = emRISCV, true
	case ArchX86:
		f.machine, f.is64 = emI386, false
	default:
		f.machine, f.is64 = emX86_64, true
	}
	return f
}

// SetOSABI overrides the EI_OSABI byte in the file header.
// The default is OSABI_None (System V / unspecified).
func (f *File) SetOSABI(o OSABI) { f.osabi = o }

// EnableDWARF controls emission of skeleton .debug_info / .debug_abbrev sections.
// Not yet implemented in write.go's build; reserved for a follow-up.
func (f *File) EnableDWARF(on bool) { f.dwarf = on }

// EnableGNUStack controls emission of a .note.GNU-stack section.
// Default: true — emits the section with no SHF_EXECINSTR, signalling a
// non-executable stack. Set to false to omit the section entirely.
func (f *File) EnableGNUStack(on bool) { f.gnuStack = on }

// AddSection appends one section in declaration order.
func (f *File) AddSection(s Section) { f.sections = append(f.sections, s) }

// Serialize assembles all accumulated sections into a complete ELF
// relocatable object file. Safe to call more than once; each call
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
	var (
		b   []byte
		err error
	)
	if f.is64 {
		b, err = f.build64()
	} else {
		b, err = f.build32()
	}
	if err != nil {
		return 0, err
	}
	n, werr := w.Write(b)
	return int64(n), werr
}

// relocType maps a RelocKind to the ELF-native r_type number for the
// file's e_machine. Returns an error for unsupported (kind, machine) pairs
// rather than guessing — e.g. RelocRISCVHI20 on emX86_64 is a programming
// error in the caller, not something to silently coerce.
func (f *File) relocType(k RelocKind) (uint32, error) {
	switch f.machine {
	case emX86_64:
		switch k {
		case RelocAbs64:
			return rX86_64_64, nil
		case RelocAbs32:
			return rX86_64_32, nil
		case RelocPCRel32:
			return rX86_64_PC32, nil
		case RelocPLT32:
			return rX86_64_PLT32, nil
		case RelocGOTLoad:
			return rX86_64_GOTPCREL, nil
		case RelocTLSGD:
			return rX86_64_TLSGD, nil
		case RelocTLSIE:
			return rX86_64_GOTTPOFF, nil
		case RelocTLSLE:
			return rX86_64_TPOFF32, nil
		}
	case emAARCH64:
		switch k {
		case RelocAbs64:
			return rAARCH64_ABS64, nil
		case RelocAbs32:
			return rAARCH64_ABS32, nil
		case RelocPCRel26:
			return rAARCH64_CALL26, nil
		case RelocADRPage21:
			return rAARCH64_ADR_PREL_PG_HI21, nil
		case RelocAddOff12:
			return rAARCH64_ADD_ABS_LO12_NC, nil
		case RelocGOTPage21:
			return rAARCH64_ADR_GOT_PAGE, nil
		case RelocGOTOff12:
			return rAARCH64_LD64_GOT_LO12_NC, nil
		case RelocTLSGD:
			return rAARCH64_TLSGD_ADR_PAGE21, nil
		case RelocTLSIE:
			return rAARCH64_TLSIE_ADR_GOTTPREL_PAGE21, nil
		case RelocTLSLE:
			return rAARCH64_TLSLE_ADD_TPREL_LO12_NC, nil
		}
	case emI386:
		switch k {
		case RelocAbs32:
			return r386_32, nil
		case RelocPCRel32:
			return r386_PC32, nil
		case RelocGOTLoad:
			return r386_GOT32, nil
		}
	case emRISCV:
		switch k {
		case RelocAbs64:
			return rRISCV_64, nil
		case RelocAbs32:
			return rRISCV_32, nil
		case RelocRISCVCall:
			return rRISCV_CALL_PLT, nil
		case RelocRISCVHI20:
			return rRISCV_HI20, nil
		case RelocRISCVLO12I:
			return rRISCV_LO12_I, nil
		case RelocRISCVLO12S:
			return rRISCV_LO12_S, nil
		case RelocTLSGD:
			return rRISCV_TLS_GD_HI20, nil
		case RelocTLSIE:
			return rRISCV_TLS_GOT_HI20, nil
		case RelocTLSLE:
			return rRISCV_TPREL_HI20, nil
		}
	}
	return 0, fmt.Errorf("elf: unsupported relocation %v for e_machine %d", k, f.machine)
}