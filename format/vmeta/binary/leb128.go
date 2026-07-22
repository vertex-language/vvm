// leb128.go
package binary

import "fmt"

// putUleb appends v's minimal-length uleb128 encoding to buf (§F2.1).
func putUleb(buf []byte, v uint64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			buf = append(buf, b|0x80)
		} else {
			return append(buf, b)
		}
	}
}

// putSleb appends v's minimal-length sleb128 encoding to buf (§F2.1).
func putSleb(buf []byte, v int64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		signBit := b&0x40 != 0
		if (v == 0 && !signBit) || (v == -1 && signBit) {
			return append(buf, b)
		}
		buf = append(buf, b|0x80)
	}
}

// readUleb reads one uleb128 value from the front of data, returning the
// value and the number of bytes consumed. Non-minimal (redundant
// continuation byte) encodings are rejected — §F2.1 requires minimal
// length so a module has exactly one byte encoding.
func readUleb(data []byte) (uint64, int, error) {
	var val uint64
	var n int
	for shift := uint(0); ; shift += 7 {
		if n >= len(data) {
			return 0, 0, fmt.Errorf("uleb128: truncated")
		}
		b := data[n]
		n++
		if shift >= 63 && (b&0x7f) > 1 {
			return 0, 0, fmt.Errorf("uleb128: overflows 64 bits")
		}
		val |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
	}
	if check := putUleb(nil, val); len(check) != n {
		return 0, 0, fmt.Errorf("uleb128: non-minimal encoding")
	}
	return val, n, nil
}

// readSleb is readUleb's signed counterpart.
func readSleb(data []byte) (int64, int, error) {
	var val int64
	var n int
	var shift uint
	var b byte
	for {
		if n >= len(data) {
			return 0, 0, fmt.Errorf("sleb128: truncated")
		}
		b = data[n]
		n++
		val |= int64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			break
		}
	}
	if shift < 64 && b&0x40 != 0 {
		val |= -1 << shift
	}
	if check := putSleb(nil, val); len(check) != n {
		return 0, 0, fmt.Errorf("sleb128: non-minimal encoding")
	}
	return val, n, nil
}