// objectfile/flat/write.go
package flat

// sectionChunk returns the complete byte representation of s for flat
// binary output, including alignment tail-padding.
//
// SectionBSS emits VSize zero bytes (self-contained; no header-only
// reservation — unlike ELF, COFF, and Mach-O, flat binary has no loader to
// zero-fill a reservation for it). All other section kinds emit Code
// directly. The result is tail-padded with zeros to the next s.Align
// boundary.
func sectionChunk(s Section) []byte {
	var raw []byte
	if s.Kind == SectionBSS {
		if s.VSize == 0 {
			return nil
		}
		raw = make([]byte, s.VSize) // zero-initialised by Go runtime
	} else {
		if len(s.Code) == 0 {
			return nil
		}
		raw = s.Code
	}

	// Tail-pad to the alignment boundary.
	align := uint64(s.Align)
	if align <= 1 {
		return raw
	}
	size := uint64(len(raw))
	rem := size % align
	if rem == 0 {
		return raw
	}
	padded := make([]byte, size+align-rem) // zero-initialised by Go runtime
	copy(padded, raw)
	return padded
}