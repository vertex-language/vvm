// Package text renders a lowered x86.Program as a human-readable IA-32
// assembly listing (Intel syntax) — arrow 6 of the README taxonomy.
//
// Because Program carries finished machine bytes (deliberately — the seam
// stays minimal), this is implemented as a disassembler over exactly the
// encoding subset lower/x86 emits: no prefixes beyond 66/F0/F3, no SIB
// beyond the ESP escape 0x24, one-byte opcodes plus the 0F map. Fixup sites
// are annotated with their symbols and kinds; unrecognized bytes degrade to
// `db` lines rather than failing, so the listing stays useful if the encoder
// grows ahead of this printer. Never an input format.
package text

import (
	"fmt"
	"sort"
	"strings"

	x86 "github.com/vertex-language/vvm/lower/x86"
)

// Encode produces the debug listing for a lowered program.
func Encode(p *x86.Program) ([]byte, error) {
	var w strings.Builder
	w.WriteString("// vvm debug listing — IA-32 (lower/x86 subset), Intel syntax, not assemblable input\n")
	for i := range p.Funcs {
		writeFunc(&w, &p.Funcs[i])
	}
	for i := range p.Globals {
		writeGlobal(&w, &p.Globals[i])
	}
	return []byte(w.String()), nil
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func writeFunc(w *strings.Builder, f *x86.Func) {
	tag := ""
	if f.Export {
		tag = " export"
	}
	fmt.Fprintf(w, "\nfn %s:%s  // size=%d align=%d fixups=%d\n",
		f.Name, tag, len(f.Code), f.Align, len(f.Fixups))

	d := &dis{b: f.Code, fx: map[int]x86.Fixup{}}
	for _, fx := range f.Fixups {
		d.fx[int(fx.Offset)] = fx
	}
	for d.pos < len(d.b) {
		start := d.pos
		text, err := d.decodeOne()
		if err != nil {
			d.pos = start
			text = fmt.Sprintf("db 0x%02x", d.b[d.pos])
			d.pos++
		}
		fmt.Fprintf(w, "  %08x  %-21s %s\n", start, hexBytes(d.b[start:d.pos]), text)
	}
}

func hexBytes(b []byte) string {
	var s strings.Builder
	for i, v := range b {
		if i > 0 {
			s.WriteByte(' ')
		}
		fmt.Fprintf(&s, "%02x", v)
	}
	if len(b) > 7 { // keep the column readable on long encodings
		return s.String()[:20] + "+"
	}
	return s.String()
}

// ---------------------------------------------------------------------------
// Decoder — exactly the lower/x86 encoding subset
// ---------------------------------------------------------------------------

var reg32 = [8]string{"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi"}
var reg16 = [8]string{"ax", "cx", "dx", "bx", "sp", "bp", "si", "di"}
var reg8 = [8]string{"al", "cl", "dl", "bl", "ah", "ch", "dh", "bh"}
var ccName = [16]string{"o", "no", "b", "ae", "e", "ne", "be", "a",
	"s", "ns", "p", "np", "l", "ge", "le", "g"}

func regName(n byte, size string) string {
	switch size {
	case "byte":
		return reg8[n]
	case "word":
		return reg16[n]
	}
	return reg32[n]
}

type truncated struct{}

type dis struct {
	b   []byte
	pos int
	fx  map[int]x86.Fixup
}

func (d *dis) u8() byte {
	if d.pos >= len(d.b) {
		panic(truncated{})
	}
	v := d.b[d.pos]
	d.pos++
	return v
}

func (d *dis) u32() uint32 {
	if d.pos+4 > len(d.b) {
		panic(truncated{})
	}
	v := uint32(d.b[d.pos]) | uint32(d.b[d.pos+1])<<8 |
		uint32(d.b[d.pos+2])<<16 | uint32(d.b[d.pos+3])<<24
	d.pos += 4
	return v
}

// sym32 reads a 32-bit field, replacing it with its fixup annotation when
// one covers this offset.
func (d *dis) sym32() string {
	pos := d.pos
	v := d.u32()
	if fx, ok := d.fx[pos]; ok {
		return fmt.Sprintf("%s<%s%+d>", fx.Symbol, fx.Kind, fx.Addend)
	}
	return fmt.Sprintf("0x%x", v)
}

// rm decodes a ModRM byte (+SIB, +disp) into (reg field, r/m operand text).
func (d *dis) rm(size string) (byte, string) {
	m := d.u8()
	mod, reg, rm := m>>6, m>>3&7, m&7
	if mod == 3 {
		return reg, regName(rm, size)
	}
	if mod == 0 && rm == 5 { // [disp32] absolute
		return reg, fmt.Sprintf("%s ptr [%s]", size, d.sym32())
	}
	base := reg32[rm]
	if rm == 4 {
		d.u8() // SIB — lower/x86 only ever emits 0x24 (ESP base, no index)
		base = "esp"
	}
	disp := int32(0)
	switch mod {
	case 1:
		disp = int32(int8(d.u8()))
	case 2:
		disp = int32(d.u32())
	}
	if disp == 0 {
		return reg, fmt.Sprintf("%s ptr [%s]", size, base)
	}
	sign, v := "+", disp
	if disp < 0 {
		sign, v = "-", -disp
	}
	return reg, fmt.Sprintf("%s ptr [%s%s0x%x]", size, base, sign, v)
}

func (d *dis) rel32() string {
	pos := d.pos
	if fx, ok := d.fx[pos]; ok {
		d.u32()
		return fmt.Sprintf("%s<%s%+d>", fx.Symbol, fx.Kind, fx.Addend)
	}
	rel := int32(d.u32())
	return fmt.Sprintf("0x%x", d.pos+int(rel))
}

var aluMR = map[byte]string{0x01: "add", 0x09: "or", 0x21: "and", 0x29: "sub", 0x31: "xor", 0x39: "cmp"}
var aluRM = map[byte]string{0x03: "add", 0x0B: "or", 0x23: "and", 0x2B: "sub", 0x33: "xor", 0x3B: "cmp"}
var aluExt = [8]string{"add", "or", "?", "?", "and", "sub", "xor", "cmp"}
var shiftName = [8]string{"rol", "ror", "?", "?", "shl", "shr", "?", "sar"}
var grp3Name = [8]string{"?", "?", "not", "neg", "mul", "imul", "div", "idiv"}

func (d *dis) decodeOne() (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(truncated); ok {
				err = fmt.Errorf("truncated")
				return
			}
			panic(r)
		}
	}()

	size := "dword"
	lock, rep := "", false
	// Legacy prefixes in the order lower/x86 emits them.
prefixes:
	for {
		switch d.b[d.pos] {
		case 0x66:
			d.u8()
			size = "word"
		case 0xF0:
			d.u8()
			lock = "lock "
		case 0xF3:
			d.u8()
			rep = true
		default:
			break prefixes
		}
	}

	op := d.u8()
	switch {
	case rep && op == 0xA4:
		return "rep movsb", nil
	case rep && op == 0xAA:
		return "rep stosb", nil

	case op == 0x55:
		return "push ebp", nil
	case op >= 0x50 && op <= 0x57:
		return "push " + reg32[op-0x50], nil
	case op >= 0x58 && op <= 0x5F:
		return "pop " + reg32[op-0x58], nil
	case op == 0xC3:
		return "ret", nil
	case op == 0x99:
		return "cdq", nil
	case op == 0xFC:
		return "cld", nil
	case op == 0xFD:
		return "std", nil

	case op >= 0xB8 && op <= 0xBF: // mov r32, imm32 (possibly a symbol address)
		return fmt.Sprintf("mov %s, %s", reg32[op-0xB8], d.sym32()), nil

	case op == 0x88 || op == 0x89: // mov r/m, r
		if op == 0x88 {
			size = "byte"
		}
		reg, rm := d.rm(size)
		return fmt.Sprintf("mov %s, %s", rm, regName(reg, size)), nil
	case op == 0x8B: // mov r32, r/m32
		reg, rm := d.rm("dword")
		return fmt.Sprintf("mov %s, %s", reg32[reg], rm), nil
	case op == 0xC7: // mov r/m32, imm32
		_, rm := d.rm("dword")
		return fmt.Sprintf("mov %s, %s", rm, d.sym32()), nil
	case op == 0x8D: // lea
		reg, rm := d.rm("dword")
		return fmt.Sprintf("lea %s, %s", reg32[reg], rm), nil

	case aluMR[op] != "":
		reg, rm := d.rm(size)
		return fmt.Sprintf("%s %s, %s", aluMR[op], rm, regName(reg, size)), nil
	case aluRM[op] != "":
		reg, rm := d.rm(size)
		return fmt.Sprintf("%s %s, %s", aluRM[op], regName(reg, size), rm), nil
	case op == 0x81: // alu r/m32, imm32
		ext, rm := d.rm("dword")
		return fmt.Sprintf("%s %s, %s", aluExt[ext], rm, d.sym32()), nil

	case op == 0x85: // test r/m32, r32
		reg, rm := d.rm("dword")
		return fmt.Sprintf("test %s, %s", rm, reg32[reg]), nil
	case op == 0x69: // imul r32, r/m32, imm32
		reg, rm := d.rm("dword")
		return fmt.Sprintf("imul %s, %s, %s", reg32[reg], rm, d.sym32()), nil
	case op == 0xF7: // group 3
		ext, rm := d.rm("dword")
		return fmt.Sprintf("%s %s", grp3Name[ext], rm), nil

	case op == 0xC0 || op == 0xC1: // shift r/m, imm8
		if op == 0xC0 {
			size = "byte"
		}
		ext, rm := d.rm(size)
		return fmt.Sprintf("%s %s, %d", shiftName[ext], rm, d.u8()), nil
	case op == 0xD2 || op == 0xD3: // shift r/m, cl
		if op == 0xD2 {
			size = "byte"
		}
		ext, rm := d.rm(size)
		return fmt.Sprintf("%s %s, cl", shiftName[ext], rm), nil

	case op == 0xE8:
		return "call " + d.rel32(), nil
	case op == 0xE9:
		return "jmp " + d.rel32(), nil
	case op == 0xFF: // call/jmp r
		m := d.u8()
		mod, ext, rm := m>>6, m>>3&7, m&7
		if mod == 3 && ext == 2 {
			return "call " + reg32[rm], nil
		}
		if mod == 3 && ext == 4 {
			return "jmp " + reg32[rm], nil
		}
		return "", fmt.Errorf("unknown FF form")
	case op == 0x87: // xchg r/m32, r32 (implicitly locked with memory)
		reg, rm := d.rm("dword")
		return fmt.Sprintf("xchg %s, %s", rm, reg32[reg]), nil

	case op == 0x0F:
		op2 := d.u8()
		switch {
		case op2 == 0x0B:
			return "ud2", nil
		case op2 == 0xAE && d.u8() == 0xF0:
			return "mfence", nil
		case op2 == 0xB6 || op2 == 0xB7: // movzx
			src := "byte"
			if op2 == 0xB7 {
				src = "word"
			}
			reg, rm := d.rm(src)
			return fmt.Sprintf("movzx %s, %s", reg32[reg], rm), nil
		case op2 == 0xBE || op2 == 0xBF: // movsx
			src := "byte"
			if op2 == 0xBF {
				src = "word"
			}
			reg, rm := d.rm(src)
			return fmt.Sprintf("movsx %s, %s", reg32[reg], rm), nil
		case op2 == 0xAF:
			reg, rm := d.rm("dword")
			return fmt.Sprintf("imul %s, %s", reg32[reg], rm), nil
		case op2 == 0xB8 && rep: // popcnt (F3 0F B8)
			reg, rm := d.rm("dword")
			return fmt.Sprintf("popcnt %s, %s", reg32[reg], rm), nil
		case op2 == 0xBC:
			reg, rm := d.rm("dword")
			return fmt.Sprintf("bsf %s, %s", reg32[reg], rm), nil
		case op2 == 0xBD:
			reg, rm := d.rm("dword")
			return fmt.Sprintf("bsr %s, %s", reg32[reg], rm), nil
		case op2 >= 0x40 && op2 <= 0x4F: // cmovcc
			reg, rm := d.rm("dword")
			return fmt.Sprintf("cmov%s %s, %s", ccName[op2-0x40], reg32[reg], rm), nil
		case op2 >= 0x80 && op2 <= 0x8F: // jcc rel32
			return fmt.Sprintf("j%s %s", ccName[op2-0x80], d.rel32()), nil
		case op2 >= 0x90 && op2 <= 0x9F: // setcc r/m8
			_, rm := d.rm("byte")
			return fmt.Sprintf("set%s %s", ccName[op2-0x90], rm), nil
		case op2 >= 0xC8 && op2 <= 0xCF:
			return "bswap " + reg32[op2-0xC8], nil
		case op2 == 0xC1: // xadd (only ever emitted with lock)
			reg, rm := d.rm("dword")
			return fmt.Sprintf("%sxadd %s, %s", lock, rm, reg32[reg]), nil
		case op2 == 0xB1: // cmpxchg (only ever emitted with lock)
			reg, rm := d.rm("dword")
			return fmt.Sprintf("%scmpxchg %s, %s", lock, rm, reg32[reg]), nil
		}
		return "", fmt.Errorf("unknown 0F %02x", op2)
	}
	return "", fmt.Errorf("unknown opcode %02x", op)
}

// ---------------------------------------------------------------------------
// Globals
// ---------------------------------------------------------------------------

func writeGlobal(w *strings.Builder, g *x86.Global) {
	tags := ""
	if g.Export {
		tags += " export"
	}
	if g.TLS {
		tags += " tls"
	}
	fmt.Fprintf(w, "\nglobal %s:%s  // size=%d align=%d\n", g.Name, tags, g.Size, g.Align)
	if g.Data == nil {
		fmt.Fprintf(w, "  .zero %d\n", g.Size)
		return
	}

	fxs := append([]x86.Fixup(nil), g.Fixups...)
	sort.Slice(fxs, func(i, j int) bool { return fxs[i].Offset < fxs[j].Offset })
	pos, fi := 0, 0
	for pos < len(g.Data) {
		if fi < len(fxs) && int(fxs[fi].Offset) == pos {
			fx := fxs[fi]
			fmt.Fprintf(w, "  .long %s%+d  // %s\n", fx.Symbol, fx.Addend, fx.Kind)
			pos += 4 // all IA-32 data fixups are 32-bit fields
			fi++
			continue
		}
		end := len(g.Data)
		if fi < len(fxs) {
			end = int(fxs[fi].Offset)
		}
		writeBytes(w, g.Data[pos:end], pos)
		pos = end
	}
	if int(g.Size) > len(g.Data) {
		fmt.Fprintf(w, "  .zero %d\n", int(g.Size)-len(g.Data))
	}
}

func writeBytes(w *strings.Builder, b []byte, base int) {
	for len(b) > 0 {
		// Compress an all-zero tail of useful length.
		if allZero(b) && len(b) >= 8 {
			fmt.Fprintf(w, "  .zero %d\n", len(b))
			return
		}
		n := len(b)
		if n > 8 {
			n = 8
		}
		var hex, ascii strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				hex.WriteString(", ")
			}
			fmt.Fprintf(&hex, "0x%02x", b[i])
			if b[i] >= 0x20 && b[i] < 0x7F {
				ascii.WriteByte(b[i])
			} else {
				ascii.WriteByte('.')
			}
		}
		fmt.Fprintf(w, "  .byte %-46s // %04x %q\n", hex.String(), base, ascii.String())
		b = b[n:]
		base += n
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}