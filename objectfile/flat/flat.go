// objectfile/flat/flat.go
package flat

import (
	"bytes"
	"io"
)

// File accumulates Section values and serialises them into a raw binary
// image by concatenating each section's bytes in declaration order.
//
//	f := flat.NewFile()
//	f.SetBaseAddress(0x7C00)
//	f.AddSection(sec)
//	b, err := f.Serialize()
type File struct {
	base     uint64
	sections []Section
}

// NewFile returns a flat File with a base address of 0x0000.
func NewFile() *File { return &File{} }

// SetBaseAddress records the load address of the first output byte.
// Default is 0x0000. This is informational metadata — it does not alter
// the byte layout of the output, since all references must already be
// resolved in Code before AddSection is called.
func (f *File) SetBaseAddress(addr uint64) { f.base = addr }

// AddSection appends one section in declaration order.
func (f *File) AddSection(s Section) { f.sections = append(f.sections, s) }

// Serialize concatenates all accumulated sections into a raw binary blob.
// Safe to call more than once; each call re-serialises from scratch.
func (f *File) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteTo is the io.WriterTo form of Serialize.
func (f *File) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for _, s := range f.sections {
		chunk := sectionChunk(s)
		if len(chunk) == 0 {
			continue
		}
		n, err := w.Write(chunk)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}