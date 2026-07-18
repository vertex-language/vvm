package macho

import (
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	fatMagic   = uint32(0xCAFEBABE)
	fatMagic64 = uint32(0xCAFEBABF)
)

// Slice is one thin (already-linked) Mach-O object for one Target.
type Slice struct {
	Target Target
	Data   []byte
}

// Lipo composes independently-linked thin slices into a universal binary —
// the same operation `lipo -create` performs. Uses fat_arch_64 automatically
// for any slice whose file offset would exceed 4 GiB.
func Lipo(slices []Slice) ([]byte, error) {
	if len(slices) == 0 {
		return nil, fmt.Errorf("macho: Lipo: no slices")
	}

	sorted := make([]Slice, len(slices))
	copy(sorted, slices)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci, _ := cpuTypeSubtype(sorted[i].Target)
		cj, _ := cpuTypeSubtype(sorted[j].Target)
		return ci < cj
	})

	const align = uint64(1 << 14) // 16 KiB, standard lipo alignment
	headerSize := uint64(8)
	need64 := false

	offsets := make([]uint64, len(sorted))
	off := alignUp64(headerSize+uint64(len(sorted))*20, align) // provisional 32-bit stride
	for i, s := range sorted {
		off = alignUp64(off, align)
		offsets[i] = off
		off += uint64(len(s.Data))
		if off > 0xFFFFFFFF {
			need64 = true
		}
	}

	entrySize := uint64(20)
	if need64 {
		entrySize = 32
		off = alignUp64(headerSize+uint64(len(sorted))*entrySize, align)
		for i, s := range sorted {
			off = alignUp64(off, align)
			offsets[i] = off
			off += uint64(len(s.Data))
		}
	}

	buf := make([]byte, off)
	be := binary.BigEndian
	if need64 {
		be.PutUint32(buf[0:], fatMagic64)
	} else {
		be.PutUint32(buf[0:], fatMagic)
	}
	be.PutUint32(buf[4:], uint32(len(sorted)))

	pos := headerSize
	for i, s := range sorted {
		cputype, cpusubtype := cpuTypeSubtype(s.Target)
		be.PutUint32(buf[pos:], uint32(cputype))
		be.PutUint32(buf[pos+4:], uint32(cpusubtype))
		if need64 {
			be.PutUint64(buf[pos+8:], offsets[i])
			be.PutUint64(buf[pos+16:], uint64(len(s.Data)))
			be.PutUint32(buf[pos+24:], 14) // align log2
			be.PutUint32(buf[pos+28:], 0)  // reserved
			pos += 32
		} else {
			be.PutUint32(buf[pos+8:], uint32(offsets[i]))
			be.PutUint32(buf[pos+12:], uint32(len(s.Data)))
			be.PutUint32(buf[pos+16:], 14)
			pos += 20
		}
	}

	for i, s := range sorted {
		copy(buf[offsets[i]:], s.Data)
	}
	return buf, nil
}

// ParseFat splits a universal binary back into its per-arch slices.
// NOTE: fat_arch only records cputype/cpusubtype, not SDK/deployment-target/
// environment — those live in each slice's own LC_BUILD_VERSION. ParseFat
// resolves Arch from cputype/cpusubtype only; callers who need the full
// Target should parse each returned Data with the object/dylib parsers.
func ParseFat(name string, data []byte) ([]Slice, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("%s: too short for a fat header", name)
	}
	be := binary.BigEndian
	magic := be.Uint32(data[0:])
	nfat := be.Uint32(data[4:])

	var slices []Slice
	switch magic {
	case fatMagic:
		pos := 8
		for i := uint32(0); i < nfat; i++ {
			if pos+20 > len(data) {
				return nil, fmt.Errorf("%s: fat_arch[%d] out of bounds", name, i)
			}
			cputype := int32(be.Uint32(data[pos:]))
			cpusubtype := int32(be.Uint32(data[pos+4:]))
			off := be.Uint32(data[pos+8:])
			size := be.Uint32(data[pos+12:])
			arch, err := archFromCPU(cputype, cpusubtype)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if int(off+size) > len(data) {
				return nil, fmt.Errorf("%s: slice %d data out of bounds", name, i)
			}
			slices = append(slices, Slice{Target: Target{Arch: arch}, Data: data[off : off+size]})
			pos += 20
		}
	case fatMagic64:
		pos := 8
		for i := uint32(0); i < nfat; i++ {
			if pos+32 > len(data) {
				return nil, fmt.Errorf("%s: fat_arch_64[%d] out of bounds", name, i)
			}
			cputype := int32(be.Uint32(data[pos:]))
			cpusubtype := int32(be.Uint32(data[pos+4:]))
			off := be.Uint64(data[pos+8:])
			size := be.Uint64(data[pos+16:])
			arch, err := archFromCPU(cputype, cpusubtype)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if off+size > uint64(len(data)) {
				return nil, fmt.Errorf("%s: slice %d data out of bounds", name, i)
			}
			slices = append(slices, Slice{Target: Target{Arch: arch}, Data: data[off : off+size]})
			pos += 32
		}
	default:
		return nil, fmt.Errorf("%s: not a fat binary (magic 0x%X)", name, magic)
	}
	return slices, nil
}

func archFromCPU(cputype, cpusubtype int32) (Arch, error) {
	switch cputype {
	case CPU_TYPE_AMD64:
		return ArchX86_64, nil
	case CPU_TYPE_ARM64:
		if cpusubtype == CPU_SUBTYPE_ARM64E {
			return ArchARM64E, nil
		}
		return ArchARM64, nil
	case CPU_TYPE_ARM64_32:
		return ArchARM64_32, nil
	}
	return 0, fmt.Errorf("unrecognized cputype 0x%X/subtype %d", cputype, cpusubtype)
}