// Package text renders a lowered x86_64.Program as a human-readable x86-64
// assembly listing (Intel syntax) — arrow 6 of the README taxonomy.
//
// A disassembler over exactly the encoding subset lower/x86_64 emits: legacy
// prefixes 66/F0/F3, the REX prefix (0100WRXB — W selects 64-bit operand
// size, R/X/B extend ModRM.reg, SIB.index, ModRM.rm), one-byte opcodes plus
// the 0F map, ModRM with the x86-64 special forms this backend uses:
// mod=00 rm=101 is [RIP+disp32], rm=100 takes a SIB byte (0x24 = RSP/R12
// base, 0x25 under mod=00 = absolute [disp32]). Fixup sites are annotated
// with their symbols and kinds; unrecognized bytes degrade to `db` lines
// rather than failing. Never an input format.
//
// Register spellings, condition-code mnemonics, ModRM/SIB/REX layout
// constants, and the opcode<->mnemonic correspondence are looked up from
// isa/x86_64 — the same facts isa/x86_64/encoder uses to emit bytes — so
// this decoder can't silently drift out of agreement with what mcode
// actually emits.
//
// One wrinkle: isa/x86_64's opcode tables (ALUOpcodes, ShiftExt, Grp3Ext,
// Grp5Ext) are deliberately mnemonic-keyed only, with no opcode->mnemonic
// direction — per that package's README, nothing used to disassemble
// x86-64, so no reverse index was built there. This package is now that
// something, so it builds its own reverse indices once at init time,
// directly from isa/x86_64's forward tables, rather than hand-duplicating
// opcode/mnemonic pairs a second time. Everything else here (the
// byte-walking state machine, prefix handling, REX-bit extraction,
// truncation/error recovery) is this package's own independent traversal,
// deliberately not shared with the encoder's control flow.
package text

import (
	"fmt"
	"sort"
	"strings"

	isax86_64 "github.com/vertex-language/vvm/isa/x86_64"
	x86_64 "github.com/vertex-language/vvm/lower/x86_64"
)

// Encode produces the debug listing for a lowered program.
func Encode(p *x86_64.Program) ([]byte, error) {
	var w strings.Builder
	w.WriteString("// vvm debug listing — x86-64 (lower/x86_64 subset), Intel syntax, not assemblable input\n")
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

func writeFunc(w *strings.Builder, f *x86_64.Func) {
	tag := ""
	if f.Export {
		tag = " export"
	}
	fmt.Fprintf(w, "\nfn %s:%s  // size=%d align=%d fixups=%d\n",
		f.Name, tag, len(f.Code), f.Align, len(f.Fixups))

	d := &dis{b: f.Code, fx: map[int]x86_64.Fixup{}}
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
		fmt.Fprintf(w, "  %08x  %-24s %s\n", start, hexBytes(d.b[start:d.pos]), text)
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
	if len(b) > 8 {
		return s.String()[:23] + "+"
	}
	return s.String()
}

// ---------------------------------------------------------------------------
// Facts derived from isa/x86_64
// ---------------------------------------------------------------------------

// widthBytes translates this package's size-name convention ("byte"/
// "word"/"qword"/dword-or-anything-else) into the byte width
// isa/x86_64.NameForWidth understands.
func widthBytes(size string) int {
	switch size {
	case "byte":
		return 1
	case "word":
		return 2
	case "qword":
		return 8
	}
	return 4
}

func regName(n byte, size string) string {
	return isax86_64.NameForWidth(isax86_64.Reg(n), widthBytes(size))
}

// aluByMR/aluByRM/aluByExt, shiftByExt, grp3ByExt, and grp5ByExt are this
// package's reverse indices over isa/x86_64's mnemonic-keyed opcode
// tables, built once at init time so they can't drift from the forward
// direction isa/x86_64/encoder consumes.
var (
	aluByMR  = map[byte]string{}
	aluByRM  = map[byte]string{}
	aluByExt = [8]string{"?", "?", "?", "?", "?", "?", "?", "?"}

	shiftByExt = [8]string{"?", "?", "?", "?", "?", "?", "?", "?"}
	grp3ByExt  = [8]string{"?", "?", "?", "?", "?", "?", "?", "?"}
	grp5ByExt  = [8]string{"?", "?", "?", "?", "?", "?", "?", "?"}
)

func init() {
	for mnem, op := range isax86_64.ALUOpcodes {
		aluByMR[op.MR] = mnem
		aluByRM[op.RM] = mnem
		aluByExt[op.Ext] = mnem
	}
	for mnem, ext := range isax86_64.ShiftExt {
		shiftByExt[ext] = mnem
	}
	for mnem, ext := range isax86_64.Grp3Ext {
		grp3ByExt[ext] = displayGrp3(mnem)
	}
	for mnem, ext := range isax86_64.Grp5Ext {
		grp5ByExt[ext] = mnem
	}
	// FF's other extensions this encoder can reach via register operands
	// (call_r/jmp_r) are named individually in isa/x86_64 rather than
	// carried in Grp5Ext (see that package's opcodes.go doc comment).
	grp5ByExt[isax86_64.OpCallRegExt] = "call"
	grp5ByExt[isax86_64.OpJmpRegExt] = "jmp"
	// FF /6 (push r/m64) has no isa/x86_64 constant at all — this
	// encoder only ever emits push via the 50+r register form (OpPushBase)
	// — but the memory/register r/m form is unambiguous and worth
	// recognizing if it ever shows up in bytes this printer decodes.
	grp5ByExt[6] = "push"
}

// displayGrp3 translates isa/x86_64.Grp3Ext's mul1/imul1 spellings (kept
// disambiguated from the two/three-operand imul forms at the source of
// truth) back to the real one-operand assembly mnemonics mul/imul.
func displayGrp3(mnem string) string {
	switch mnem {
	case "mul1":
		return "mul"
	case "imul1":
		return "imul"
	}
	return mnem
}

// ---------------------------------------------------------------------------
// Decoder — exactly the lower/x86_64 encoding subset
// ---------------------------------------------------------------------------

type truncated struct{}

type dis struct {
	b   []byte
	pos int
	fx  map[int]x86_64.Fixup

	rexW, rexR, rexB byte // current instruction's REX bits
}

func (d *dis) u8() byte {
	if d.pos >= len(d.b) {
		panic(truncated{})
	}
	v := d.b[d.pos]
	d.pos++
	return v
}

// peek reports the next byte without consuming it — used where a case
// needs to branch on part of a ModRM byte (e.g. the /digit opcode
// extension) before deciding which operand width to decode it with.
func (d *dis) peek() byte {
	if d.pos >= len(d.b) {
		panic(truncated{})
	}
	return d.b[d.pos]
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

func (d *dis) u64() uint64 {
	lo := uint64(d.u32())
	return lo | uint64(d.u32())<<32
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

// rm decodes ModRM (+SIB, +disp) into (extended reg/ext field, r/m operand
// text), using isa/x86_64's ModRM/SIB field-layout constants throughout.
// For group opcodes the returned "reg" value is really a 3-bit opcode
// extension, not a register — callers that use it that way should mask
// with &7, since REX.R has no meaning there but is harmlessly folded in
// and cancels back out under the mask.
func (d *dis) rm(size string) (byte, string) {
	m := d.u8()
	mod, regRaw, rmRaw := isax86_64.UnpackModRM(m)
	reg := regRaw | d.rexR<<3
	if mod == isax86_64.ModReg {
		return reg, regName(rmRaw|d.rexB<<3, size)
	}
	if mod == isax86_64.ModDisp0 && rmRaw == isax86_64.RMRipOrDisp32 { // [RIP+disp32] — REX.B does not affect this form
		pos := d.pos
		v := d.u32()
		if fx, ok := d.fx[pos]; ok {
			return reg, fmt.Sprintf("%s ptr [rip+%s<%s%+d>]", size, fx.Symbol, fx.Kind, fx.Addend)
		}
		return reg, fmt.Sprintf("%s ptr [rip%+d]", size, int32(v))
	}
	var base string
	if rmRaw == isax86_64.RMNeedsSIB { // SIB
		sib := d.u8()
		sibBase := sib & 7
		if mod == isax86_64.ModDisp0 && sibBase == isax86_64.SIBBaseEscape { // absolute [disp32], base none
			return reg, fmt.Sprintf("%s ptr [%s]", size, d.sym32())
		}
		base = isax86_64.Name64[sibBase|d.rexB<<3] // lower/x86_64 never uses an index reg
	} else {
		base = isax86_64.Name64[rmRaw|d.rexB<<3]
	}
	disp := int32(0)
	switch mod {
	case isax86_64.ModDisp8:
		disp = int32(int8(d.u8()))
	case isax86_64.ModDisp32:
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
	d.rexW, d.rexR, d.rexB = 0, 0, 0
	// Legacy prefixes, then REX immediately before the opcode.
prefixes:
	for {
		switch v := d.peek(); {
		case v == isax86_64.PrefixOperandSize:
			d.u8()
			size = "word"
		case v == isax86_64.PrefixLock:
			d.u8()
			lock = "lock "
		case v == isax86_64.PrefixRep:
			d.u8()
			rep = true
		case v >= 0x40 && v <= 0x4F: // REX prefix (0100WRXB); isa/x86_64 has no unpack helper for
			// this direction (only PackREX), so this decoder extracts the
			// bits itself — the same relationship as ModRM/SIB unpacking
			// having no encoder-side counterpart.
			r := d.u8()
			d.rexW, d.rexR, d.rexB = r>>3&1, r>>2&1, r&1
			break prefixes // REX is last; opcode follows
		default:
			break prefixes
		}
	}
	if d.rexW == 1 {
		size = "qword"
	}

	op := d.u8()
	switch {
	case rep && op == isax86_64.OpRepMovsb:
		return "rep movsb", nil
	case rep && op == isax86_64.OpRepStosb:
		return "rep stosb", nil

	case op == isax86_64.OpNop:
		return "nop", nil
	case op == isax86_64.OpRet:
		return "ret", nil
	case op == isax86_64.OpCdq:
		if d.rexW == 1 {
			return "cqo", nil
		}
		return "cdq", nil
	case op == isax86_64.OpCld:
		return "cld", nil
	case op == isax86_64.OpStd:
		return "std", nil

	case op >= isax86_64.OpPushBase && op <= isax86_64.OpPushBase+7:
		return "push " + isax86_64.Name64[op-isax86_64.OpPushBase|d.rexB<<3], nil
	case op >= isax86_64.OpPopBase && op <= isax86_64.OpPopBase+7:
		return "pop " + isax86_64.Name64[op-isax86_64.OpPopBase|d.rexB<<3], nil

	case op >= isax86_64.OpMovImmR && op <= isax86_64.OpMovImmR+7: // mov r, imm32 / movabs r64, imm64
		r := regName(op-isax86_64.OpMovImmR|d.rexB<<3, size)
		if d.rexW == 1 {
			return fmt.Sprintf("movabs %s, 0x%x", r, d.u64()), nil
		}
		return fmt.Sprintf("mov %s, %s", r, d.sym32()), nil

	case op == isax86_64.OpMovRM8 || op == isax86_64.OpMovRM: // mov r/m, r
		if op == isax86_64.OpMovRM8 {
			size = "byte"
		}
		reg, rm := d.rm(size)
		return fmt.Sprintf("mov %s, %s", rm, regName(reg, size)), nil
	case op == isax86_64.OpMovMR: // mov r, r/m
		reg, rm := d.rm(size)
		return fmt.Sprintf("mov %s, %s", regName(reg, size), rm), nil
	case op == isax86_64.OpMovImm32: // mov r/m, imm32 (sign-extended under REX.W)
		_, rm := d.rm(size)
		return fmt.Sprintf("mov %s, %s", rm, d.sym32()), nil
	case op == isax86_64.OpLea: // lea (always 64-bit in this backend; RIP form common)
		reg, rm := d.rm("qword")
		rm = strings.TrimPrefix(rm, "qword ptr ")
		return fmt.Sprintf("lea %s, %s", isax86_64.Name64[reg], rm), nil
	case op == isax86_64.OpMovsxd: // movsxd r64, r/m32
		reg, rm := d.rm("dword")
		return fmt.Sprintf("movsxd %s, %s", isax86_64.Name64[reg], rm), nil

	case aluByMR[op] != "":
		reg, rm := d.rm(size)
		return fmt.Sprintf("%s %s, %s", aluByMR[op], rm, regName(reg, size)), nil
	case aluByRM[op] != "":
		reg, rm := d.rm(size)
		return fmt.Sprintf("%s %s, %s", aluByRM[op], regName(reg, size), rm), nil
	case op == 0x81: // alu r/m, imm32 — no isa/x86_64 constant; the encoder
		// itself reaches this opcode as a bare literal too (see encode.go),
		// keyed off ALUOpcodes[...].Ext rather than a named opcode byte.
		ext, rm := d.rm(size)
		return fmt.Sprintf("%s %s, %s", aluByExt[ext&7], rm, d.sym32()), nil

	case op == isax86_64.OpTest: // test r/m, r
		reg, rm := d.rm(size)
		return fmt.Sprintf("test %s, %s", rm, regName(reg, size)), nil
	case op == isax86_64.OpImul3: // imul r, r/m, imm32
		reg, rm := d.rm(size)
		return fmt.Sprintf("imul %s, %s, %s", regName(reg, size), rm, d.sym32()), nil
	case op == 0xF7: // group 3 — bare literal in the encoder too, keyed off Grp3Ext
		ext, rm := d.rm(size)
		return fmt.Sprintf("%s %s", grp3ByExt[ext&7], rm), nil

	case op == 0xC0 || op == 0xC1: // shift r/m, imm8 — bare literals in the encoder too
		if op == 0xC0 {
			size = "byte"
		}
		ext, rm := d.rm(size)
		return fmt.Sprintf("%s %s, %d", shiftByExt[ext&7], rm, d.u8()), nil
	case op == 0xD2 || op == 0xD3: // shift r/m, cl
		if op == 0xD2 {
			size = "byte"
		}
		ext, rm := d.rm(size)
		return fmt.Sprintf("%s %s, cl", shiftByExt[ext&7], rm), nil

	case op == isax86_64.OpCallRel32:
		return "call " + d.rel32(), nil
	case op == isax86_64.OpJmpRel32:
		return "jmp " + d.rel32(), nil

	case op == 0xFF: // group 5: inc/dec/call/jmp/push, register or memory r/m
		ext := (d.peek() >> 3) & 7
		sz := size
		if ext == isax86_64.OpCallRegExt || ext == isax86_64.OpJmpRegExt || ext == 6 {
			// Near call/jmp/push indirect default to 64-bit operand size
			// in long mode regardless of REX.W/legacy size prefix.
			sz = "qword"
		}
		extOut, rm := d.rm(sz)
		name := grp5ByExt[extOut&7]
		if name == "?" {
			return "", fmt.Errorf("unknown FF /%d form", extOut&7)
		}
		return name + " " + rm, nil

	case op == isax86_64.OpXchg: // xchg r/m, r (implicitly locked with memory)
		reg, rm := d.rm(size)
		return fmt.Sprintf("xchg %s, %s", rm, regName(reg, size)), nil

	case op == 0x0F:
		op2 := d.u8()
		switch {
		case op2 == isax86_64.OpUD2Lo:
			return "ud2", nil
		case op2 == isax86_64.OpSyscallLo:
			return "syscall", nil
		case op2 == isax86_64.OpMfence && d.u8() == 0xF0:
			return "mfence", nil
		case op2 == isax86_64.OpMovzx8 || op2 == isax86_64.OpMovzx16: // movzx (zero-extends to 64)
			src := "byte"
			if op2 == isax86_64.OpMovzx16 {
				src = "word"
			}
			reg, rm := d.rm(src)
			return fmt.Sprintf("movzx %s, %s", regName(reg, "dword"), rm), nil
		case op2 == isax86_64.OpMovsx8 || op2 == isax86_64.OpMovsx16: // movsx (REX.W: to 64)
			src := "byte"
			if op2 == isax86_64.OpMovsx16 {
				src = "word"
			}
			reg, rm := d.rm(src)
			return fmt.Sprintf("movsx %s, %s", regName(reg, size), rm), nil
		case op2 == isax86_64.OpImulRM:
			reg, rm := d.rm(size)
			return fmt.Sprintf("imul %s, %s", regName(reg, size), rm), nil
		case op2 == isax86_64.OpPopcntLo && rep: // popcnt (F3 [REX] 0F B8)
			reg, rm := d.rm(size)
			return fmt.Sprintf("popcnt %s, %s", regName(reg, size), rm), nil
		case op2 == isax86_64.OpBsf:
			reg, rm := d.rm(size)
			return fmt.Sprintf("bsf %s, %s", regName(reg, size), rm), nil
		case op2 == isax86_64.OpBsr:
			reg, rm := d.rm(size)
			return fmt.Sprintf("bsr %s, %s", regName(reg, size), rm), nil
		case op2 >= isax86_64.OpCmovccBase && op2 <= isax86_64.OpCmovccBase+15: // cmovcc
			reg, rm := d.rm(size)
			return fmt.Sprintf("cmov%s %s, %s", isax86_64.CondName[op2-isax86_64.OpCmovccBase], regName(reg, size), rm), nil
		case op2 >= isax86_64.OpJccBase && op2 <= isax86_64.OpJccBase+15: // jcc rel32
			return fmt.Sprintf("j%s %s", isax86_64.CondName[op2-isax86_64.OpJccBase], d.rel32()), nil
		case op2 >= isax86_64.OpSetccBase && op2 <= isax86_64.OpSetccBase+15: // setcc r/m8
			_, rm := d.rm("byte")
			return fmt.Sprintf("set%s %s", isax86_64.CondName[op2-isax86_64.OpSetccBase], rm), nil
		case op2 >= isax86_64.OpBswapBase && op2 <= isax86_64.OpBswapBase+7:
			return "bswap " + regName(op2-isax86_64.OpBswapBase|d.rexB<<3, size), nil
		case op2 == isax86_64.OpLockXadd: // xadd (only ever emitted with lock)
			reg, rm := d.rm(size)
			return fmt.Sprintf("%sxadd %s, %s", lock, rm, regName(reg, size)), nil
		case op2 == isax86_64.OpLockCmpxchg: // cmpxchg (only ever emitted with lock)
			reg, rm := d.rm(size)
			return fmt.Sprintf("%scmpxchg %s, %s", lock, rm, regName(reg, size)), nil
		}
		return "", fmt.Errorf("unknown 0F %02x", op2)
	}
	return "", fmt.Errorf("unknown opcode %02x", op)
}

// ---------------------------------------------------------------------------
// Globals
// ---------------------------------------------------------------------------

func writeGlobal(w *strings.Builder, g *x86_64.Global) {
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

	fxs := append([]x86_64.Fixup(nil), g.Fixups...)
	sort.Slice(fxs, func(i, j int) bool { return fxs[i].Offset < fxs[j].Offset })
	pos, fi := 0, 0
	for pos < len(g.Data) {
		if fi < len(fxs) && int(fxs[fi].Offset) == pos {
			fx := fxs[fi]
			if fx.Kind == x86_64.FixupAbs64 {
				fmt.Fprintf(w, "  .quad %s%+d  // %s\n", fx.Symbol, fx.Addend, fx.Kind)
				pos += 8
			} else {
				fmt.Fprintf(w, "  .long %s%+d  // %s\n", fx.Symbol, fx.Addend, fx.Kind)
				pos += 4
			}
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