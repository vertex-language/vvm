package amdtx

import "strconv"

// Width is a bit width. AMDTX carries widths, not semantic types:
// interpretation lives in the mnemonic, so v_add_f32 and v_add_u32 both take
// .b32 operands (§5).
type Width int

const (
	NoWidth Width = 0

	B32   Width = 32
	B64   Width = 64
	B96   Width = 96
	B128  Width = 128
	B160  Width = 160
	B192  Width = 192
	B224  Width = 224
	B256  Width = 256
	B288  Width = 288
	B320  Width = 320
	B352  Width = 352
	B384  Width = 384
	B512  Width = 512
	B1024 Width = 1024
)

// Dwords returns the register-tuple size in 32-bit registers.
func (w Width) Dwords() int { return int(w) / 32 }

// Bytes returns the width in bytes.
func (w Width) Bytes() int { return int(w) / 8 }

// String returns the dotted width specifier, e.g. ".b128".
func (w Width) String() string {
	if w == NoWidth {
		return ""
	}
	return ".b" + strconv.Itoa(int(w))
}

// Suffix returns the bare mnemonic suffix, e.g. "b128".
func (w Width) Suffix() string { return "b" + strconv.Itoa(int(w)) }

// IsValid reports whether w is a legal register-tuple size: a multiple of
// 32 covering 1 through 12 registers, or exactly 16 or 32 (V13).
func (w Width) IsValid() bool {
	if w <= 0 || w%32 != 0 {
		return false
	}
	d := w.Dwords()
	return (d >= 1 && d <= 12) || d == 16 || d == 32
}

// RegKind is a register file.
type RegKind uint8

const (
	NoRegKind RegKind = iota
	SGPR              // uniform across the wave
	VGPR              // one value per lane
	AGPR              // matrix accumulator; requires has_agprs
	LaneMask          // one bit per lane; physical width from .wave
)

func (k RegKind) String() string {
	return [...]string{"", "sgpr", "vgpr", "agpr", "lanemask"}[k]
}

// RegClass is a declared register class: a file plus a width. LaneMask
// carries no width of its own; it takes one from .wave (§7.4).
type RegClass struct {
	Kind  RegKind
	Width Width
}

// Sgpr, Vgpr and Agpr build a register class of the given width.
func Sgpr(w Width) RegClass { return RegClass{SGPR, w} }
func Vgpr(w Width) RegClass { return RegClass{VGPR, w} }
func Agpr(w Width) RegClass { return RegClass{AGPR, w} }

// Lane is the .lanemask class: the wave-width-independent predicate.
var Lane = RegClass{Kind: LaneMask}

// String returns the dotted class specifier, e.g. ".vgpr.b128".
func (c RegClass) String() string {
	if c.Kind == LaneMask {
		return ".lanemask"
	}
	if c.Kind == NoRegKind {
		return ""
	}
	return "." + c.Kind.String() + c.Width.String()
}

// PhysWidth returns the physical width of the class under wave width w.
func (c RegClass) PhysWidth(w Wave) Width {
	if c.Kind == LaneMask {
		return w.MaskWidth()
	}
	return c.Width
}

// PhysKind returns the register file the class occupies; .lanemask lives in
// SGPRs.
func (c RegClass) PhysKind() RegKind {
	if c.Kind == LaneMask {
		return SGPR
	}
	return c.Kind
}

// IsValid reports whether c names a legal class.
func (c RegClass) IsValid() bool {
	if c.Kind == LaneMask {
		return c.Width == NoWidth
	}
	switch c.Kind {
	case SGPR, VGPR, AGPR:
		return c.Width.IsValid()
	}
	return false
}