// text.go
// Package text is the A32 debug disassembler: it renders a lowered
// arm.Program as a human-readable listing by decoding each instruction
// word back into a mnemonic, using the same isa/arm tables
// isa/arm/encoder assembled it from (see the format README's "asm —
// encode-only" section).
//
// There is no Decode here and none is planned: this package is scoped to
// exactly the encoding subset lower/arm's Encode switch (encode.go,
// isel.go, and friends) emits, not to A32 in general. A word outside that
// subset degrades to a `.word` line rather than failing Encode — see
// decodeInst.
package text

import (
	"fmt"
	"strings"

	lowerarm "github.com/vertex-language/vvm/lower/arm"
)

// Encode renders p as a listing: one section per global, one per
// function, in module order — mirroring vbyte's "same order as Lower"
// convention even though nothing here round-trips.
func Encode(p *lowerarm.Program) ([]byte, error) {
	var b strings.Builder
	for i, g := range p.Globals {
		if i > 0 {
			b.WriteByte('\n')
		}
		writeGlobal(&b, g)
	}
	if len(p.Globals) > 0 && len(p.Funcs) > 0 {
		b.WriteByte('\n')
	}
	for i, f := range p.Funcs {
		if i > 0 {
			b.WriteByte('\n')
		}
		if err := writeFunc(&b, f); err != nil {
			return nil, fmt.Errorf("func %s: %w", f.Name, err)
		}
	}
	return []byte(b.String()), nil
}

// writeFunc decodes one function's Code word by word. Code is always a
// whole number of 4-byte instructions (isa/arm.InstrBytes): a length that
// isn't is not a malformed instruction stream, it's a malformed Program,
// so it's reported rather than silently truncated.
func writeFunc(b *strings.Builder, f lowerarm.Func) error {
	if len(f.Code)%4 != 0 {
		return fmt.Errorf("code length %d is not a multiple of the 4-byte instruction width", len(f.Code))
	}
	fmt.Fprintf(b, "%s:", f.Name)
	if f.Export {
		fmt.Fprintf(b, " ; export, align %d\n", f.Align)
	} else {
		fmt.Fprintf(b, " ; align %d\n", f.Align)
	}

	fx := fixupsByOffset(f.Fixups)
	for off := 0; off < len(f.Code); off += 4 {
		w := getLE32(f.Code[off:])
		var fxp *lowerarm.Fixup
		if r, ok := fx[uint32(off)]; ok {
			r := r
			fxp = &r
		}
		decoded, usedFixup := decodeInst(uint32(off), w, fxp)
		comment := ""
		if fxp != nil && !usedFixup {
			// Only branch/movw/movt ever carry a fixup in function code
			// (arm.go's fromEncoderFixup); this is a safety net, not the
			// expected path.
			comment = relocComment(*fxp)
		}
		writeLine(b, uint32(off), w, decoded, comment)
	}
	return nil
}

// writeGlobal dumps a global's bytes as-is: globals.go already wrote them
// in the target's final byte order (armeb's data-word big-endianness
// included), so there is no endianness decision left to make here — just
// a hex dump, plus the fixups listed separately since a data relocation
// isn't an instruction to decode.
func writeGlobal(b *strings.Builder, g lowerarm.Global) {
	fmt.Fprintf(b, "%s:", g.Name)
	tags := []string{fmt.Sprintf("size %d", g.Size), fmt.Sprintf("align %d", g.Align)}
	if g.Export {
		tags = append(tags, "export")
	}
	if g.TLS {
		tags = append(tags, "tls")
	}
	fmt.Fprintf(b, " ; %s\n", strings.Join(tags, ", "))

	if g.Data == nil {
		fmt.Fprintf(b, "  ; zero-filled (bss)\n")
		return
	}
	for off := 0; off < len(g.Data); off += 16 {
		end := off + 16
		if end > len(g.Data) {
			end = len(g.Data)
		}
		fmt.Fprintf(b, "  %08x: %s\n", off, hexBytes(g.Data[off:end]))
	}
	for _, fx := range g.Fixups {
		fmt.Fprintf(b, "  ; %s\n", relocComment(fx))
	}
}

func writeLine(b *strings.Builder, off, w uint32, decoded, comment string) {
	if decoded == "" {
		// decodeInst's degrade path: an unrecognized word, printed rather
		// than dropped (see the format README's "degrade, don't fail").
		decoded = fmt.Sprintf(".word   0x%08x", w)
	}
	if comment == "" {
		fmt.Fprintf(b, "  %08x: %08x    %s\n", off, w, decoded)
		return
	}
	fmt.Fprintf(b, "  %08x: %08x    %-28s ; %s\n", off, w, decoded, comment)
}

func relocComment(f lowerarm.Fixup) string {
	if f.Addend == 0 {
		return fmt.Sprintf("reloc %s %s", f.Kind, f.Symbol)
	}
	sign, addend := "+", f.Addend
	if addend < 0 {
		sign, addend = "-", -addend
	}
	return fmt.Sprintf("reloc %s %s%s%d", f.Kind, f.Symbol, sign, addend)
}

func fixupsByOffset(fx []lowerarm.Fixup) map[uint32]lowerarm.Fixup {
	m := make(map[uint32]lowerarm.Fixup, len(fx))
	for _, f := range fx {
		m[f.Offset] = f
	}
	return m
}

func hexBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, " ")
}

func getLE32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}