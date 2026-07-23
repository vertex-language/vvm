// Package text is the x86-64 debug disassembler: format/asm/x86_64/text.
// It renders a lowered *x86_64.Program (from lower/x86_64) as a human-
// readable listing — machine bytes back into mnemonics — for humans to
// read, never as an input format. There is no matching Decode.
//
// Like every asm/<arch>/text package, this is scoped to exactly the
// encoding subset isa/x86_64/encoder's Encode (and therefore lower/x86_64)
// emits, not a general-purpose x86-64 disassembler. An unrecognized byte
// degrades to a ".byte 0xNN" line rather than failing Encode outright, so
// the listing stays usable even as lower/x86_64 grows past what this
// printer currently recognizes.
package text

import (
	"bytes"
	"fmt"
	"strings"

	enc "github.com/vertex-language/vvm/isa/x86_64/encoder"
	lowerx64 "github.com/vertex-language/vvm/lower/x86_64"
)

// Encode renders p as a debug assembly listing: every function's machine
// code disassembled instruction by instruction, followed by every global's
// data (or zero-fill) with its fixups annotated.
func Encode(p *lowerx64.Program) ([]byte, error) {
	var buf bytes.Buffer
	for i, f := range p.Funcs {
		if i > 0 {
			buf.WriteByte('\n')
		}
		writeFunc(&buf, f)
	}
	if len(p.Funcs) > 0 && len(p.Globals) > 0 {
		buf.WriteByte('\n')
	}
	for i, g := range p.Globals {
		if i > 0 {
			buf.WriteByte('\n')
		}
		writeGlobal(&buf, g)
	}
	return buf.Bytes(), nil
}

func writeFunc(buf *bytes.Buffer, f lowerx64.Func) {
	fmt.Fprintf(buf, "func %s:", f.Name)
	if f.Export {
		buf.WriteString(" export")
	}
	fmt.Fprintf(buf, " align=%d\n", f.Align)

	fixups := make(map[uint32]enc.Fixup, len(f.Fixups))
	for _, fx := range f.Fixups {
		fixups[fx.Offset] = fx
	}
	d := &decoder{code: f.Code, fixups: fixups}

	for d.pos < len(d.code) {
		start := d.pos
		line, err := decodeInsn(d)
		if err != nil {
			// Degrade a single unrecognized/malformed byte to a raw .byte
			// line and resume right after it — the listing stays usable
			// rather than aborting the whole function.
			d.pos = start + 1
			b := f.Code[start]
			fmt.Fprintf(buf, "  %04x  %-24s  .byte 0x%02x\n", start, hexBytes(f.Code[start:start+1]), b)
			continue
		}
		raw := f.Code[start:d.pos]
		fmt.Fprintf(buf, "  %04x  %-24s  %s\n", start, hexBytes(raw), line)
	}
}

func writeGlobal(buf *bytes.Buffer, g lowerx64.Global) {
	fmt.Fprintf(buf, "global %s:", g.Name)
	if g.Export {
		buf.WriteString(" export")
	}
	if g.TLS {
		buf.WriteString(" tls")
	}
	fmt.Fprintf(buf, " size=%d align=%d\n", g.Size, g.Align)

	if g.Data == nil {
		buf.WriteString("  .zero\n")
		return
	}

	fixups := make(map[uint32]enc.Fixup, len(g.Fixups))
	for _, fx := range g.Fixups {
		fixups[fx.Offset] = fx
	}

	for off := 0; off < len(g.Data); {
		if fx, ok := fixups[uint32(off)]; ok {
			width := 4
			if fx.Kind == enc.FixupAbs64 {
				width = 8
			}
			sym := fx.Symbol
			if fx.Addend != 0 {
				sym = fmt.Sprintf("%s%+d", fx.Symbol, fx.Addend)
			}
			fmt.Fprintf(buf, "  %04x  %-24s  .%s %s\n", off, hexBytes(g.Data[off:min(off+width, len(g.Data))]), fx.Kind.String(), sym)
			off += width
			continue
		}
		end := off + 16
		if end > len(g.Data) {
			end = len(g.Data)
		}
		fmt.Fprintf(buf, "  %04x  %s\n", off, hexBytes(g.Data[off:end]))
		off = end
	}
}

func hexBytes(b []byte) string {
	var sb strings.Builder
	for i, x := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02x", x)
	}
	return sb.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}