package x86_64

// The REX prefix. It is a single byte with the fixed high nibble 0x40 and
// four payload bits; it must sit immediately before the opcode (or the 0F
// escape), after any legacy/mandatory prefix. It does two jobs at once:
// select 64-bit operand size (REX.W) and supply the fourth bit of each
// register field (REX.R/X/B) so the sixteen GPRs are reachable.
//
// A REX prefix is emitted only when something needs it: a 64-bit operand
// (REX.W), an extended register r8-r15 in any field, or a byte operand on
// spl/bpl/sil/dil. When none of those hold, no REX byte is emitted at all —
// and emitting a spurious one is not harmless, because it would silently
// reclassify an ah/ch/dh/bh byte operand into spl/bpl/sil/dil.
const (
	REXBase byte = 0x40 // fixed 0100 high nibble; a bare 0x40 is a legal no-op REX

	REXB byte = 0x01 // extends ModRM.rm, SIB.base, or an opcode-embedded reg
	REXX byte = 0x02 // extends SIB.index
	REXR byte = 0x04 // extends ModRM.reg
	REXW byte = 0x08 // 64-bit operand size
)

// PackREX builds a REX byte. w selects 64-bit operand size; r, x, b are the
// high bits of the ModRM.reg, SIB.index, and ModRM.rm/SIB.base/opcode-reg
// fields respectively (each true iff the corresponding register is r8-r15).
func PackREX(w, r, x, b bool) byte {
	rex := REXBase
	if w {
		rex |= REXW
	}
	if r {
		rex |= REXR
	}
	if x {
		rex |= REXX
	}
	if b {
		rex |= REXB
	}
	return rex
}

// IsREX reports whether b is a REX prefix byte (high nibble 0100). In long
// mode this claims the whole 0x40-0x4F range, which is why the one-byte
// inc/dec short forms that occupied those opcodes in 32-bit mode do not
// exist here: they have to be spelled with the ModRM group-5 forms
// (0xFF /0 and /1) instead.
func IsREX(b byte) bool { return b&0xF0 == REXBase }