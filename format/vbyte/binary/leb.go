// File: format/vbyte/binary/leb.go
package binary

// putUleb returns the canonical (minimal-length) unsigned LEB128 encoding
// of v (§2).
func putUleb(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			break
		}
	}
	return out
}

// putSleb returns the canonical (minimal-length) signed LEB128 encoding of
// v (§2).
func putSleb(v int64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		signBitSet := b&0x40 != 0
		if (v == 0 && !signBitSet) || (v == -1 && signBitSet) {
			out = append(out, b)
			break
		}
		out = append(out, b|0x80)
	}
	return out
}