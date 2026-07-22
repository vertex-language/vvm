// File: format/vbyte/binary/rw.go
package binary

import (
	stdbin "encoding/binary"
	"bytes"
	"fmt"
	"math"
)

// reader is a forward-only byte cursor implementing the primitive decodes
// of §2, including the canonical-encoding checks required by §1's "Strict
// Rejection" clause.
type reader struct {
	data []byte
	pos  int
}

func newReader(data []byte) *reader { return &reader{data: data} }

func (r *reader) atEnd() bool     { return r.pos == len(r.data) }
func (r *reader) remaining() int  { return len(r.data) - r.pos }

func (r *reader) u8() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("vbyte: unexpected end of input reading u8")
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) bytesN(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.data) {
		return nil, fmt.Errorf("vbyte: unexpected end of input reading %d bytes", n)
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *reader) bytesVec() ([]byte, error) {
	n, err := r.uleb()
	if err != nil {
		return nil, err
	}
	return r.bytesN(int(n))
}

func (r *reader) f32() (float32, error) {
	b, err := r.bytesN(4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(stdbin.LittleEndian.Uint32(b)), nil
}

func (r *reader) f64() (float64, error) {
	b, err := r.bytesN(8)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(stdbin.LittleEndian.Uint64(b)), nil
}

func (r *reader) boolean() (bool, error) {
	b, err := r.u8()
	if err != nil {
		return false, err
	}
	switch b {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("vbyte: bool byte must be 0 or 1, got 0x%02x (§1 strict rejection)", b)
	}
}

func (r *reader) uleb() (uint64, error) {
	start := r.pos
	var result uint64
	var shift uint
	for {
		if r.pos >= len(r.data) {
			return 0, fmt.Errorf("vbyte: unexpected end of input reading uleb")
		}
		if shift >= 70 {
			return 0, fmt.Errorf("vbyte: uleb overflow")
		}
		b := r.data[r.pos]
		r.pos++
		result |= uint64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			break
		}
	}
	raw := r.data[start:r.pos]
	if !bytes.Equal(raw, putUleb(result)) {
		return 0, fmt.Errorf("vbyte: non-canonical uleb encoding (§1 strict rejection)")
	}
	return result, nil
}

func (r *reader) sleb() (int64, error) {
	start := r.pos
	var result int64
	var shift uint
	var b byte
	for {
		if r.pos >= len(r.data) {
			return 0, fmt.Errorf("vbyte: unexpected end of input reading sleb")
		}
		if shift >= 70 {
			return 0, fmt.Errorf("vbyte: sleb overflow")
		}
		b = r.data[r.pos]
		r.pos++
		result |= int64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			break
		}
	}
	if shift < 64 && b&0x40 != 0 {
		result |= -1 << shift
	}
	raw := r.data[start:r.pos]
	if !bytes.Equal(raw, putSleb(result)) {
		return 0, fmt.Errorf("vbyte: non-canonical sleb encoding (§1 strict rejection)")
	}
	return result, nil
}

// idx reads a table index (§2 `idx`). Which table it refers into is fixed
// by call-site position, never inferred here.
func (r *reader) idx() (int, error) {
	v, err := r.uleb()
	if err != nil {
		return 0, err
	}
	if v > (1 << 31) {
		return 0, fmt.Errorf("vbyte: index too large")
	}
	return int(v), nil
}

// writer is an append-only byte buffer implementing the primitive encodes
// of §2, always producing canonical (minimal-length) LEBs.
type writer struct{ buf []byte }

func newWriter() *writer { return &writer{} }

func (w *writer) bytes() []byte { return w.buf }

func (w *writer) u8(b byte)          { w.buf = append(w.buf, b) }
func (w *writer) bytesRaw(b []byte)  { w.buf = append(w.buf, b...) }
func (w *writer) bytesVec(b []byte)  { w.uleb(uint64(len(b))); w.bytesRaw(b) }

func (w *writer) f32(v float32) {
	var b [4]byte
	stdbin.LittleEndian.PutUint32(b[:], math.Float32bits(v))
	w.buf = append(w.buf, b[:]...)
}

func (w *writer) f64(v float64) {
	var b [8]byte
	stdbin.LittleEndian.PutUint64(b[:], math.Float64bits(v))
	w.buf = append(w.buf, b[:]...)
}

func (w *writer) boolean(v bool) {
	if v {
		w.u8(1)
	} else {
		w.u8(0)
	}
}

func (w *writer) uleb(v uint64) { w.buf = append(w.buf, putUleb(v)...) }
func (w *writer) sleb(v int64)  { w.buf = append(w.buf, putSleb(v)...) }
func (w *writer) idx(i int)     { w.uleb(uint64(i)) }