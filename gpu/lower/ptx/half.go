package ptx

import "math"

// PTX takes 16-bit float immediates as bit patterns, so f16 and bf16 literals
// are converted here. §2 requires correct rounding to the destination format
// and promises two conforming frontends produce identical bits from identical
// text, so both conversions round to nearest, ties to even.

// f16bits converts v to an IEEE binary16 bit pattern.
func f16bits(v float64) uint16 {
	f := float32(v)
	b := math.Float32bits(f)
	sign := uint16(b >> 16 & 0x8000)
	exp := int32(b>>23&0xff) - 127
	man := b & 0x7fffff

	switch {
	case b&0x7fffffff == 0:
		return sign // ±0

	case exp == 128: // Inf or NaN
		if man != 0 {
			return sign | 0x7e00 // quiet NaN
		}
		return sign | 0x7c00

	case exp > 15:
		return sign | 0x7c00 // overflow to infinity

	case exp < -25:
		return sign // underflow to zero

	case exp < -14:
		// Subnormal: shift the implicit bit into place and round.
		shift := uint32(-14-exp) + 13
		full := man | 0x800000
		out := full >> shift
		rem := full & ((1 << shift) - 1)
		half := uint32(1) << (shift - 1)
		if rem > half || (rem == half && out&1 == 1) {
			out++
		}
		return sign | uint16(out)

	default:
		out := uint32(exp+15)<<10 | man>>13
		rem := man & 0x1fff
		// A carry out of the mantissa walks into the exponent, and out of the
		// exponent into infinity, which is the correct rounded result.
		if rem > 0x1000 || (rem == 0x1000 && out&1 == 1) {
			out++
		}
		return sign | uint16(out)
	}
}

// bf16bits converts v to a bfloat16 bit pattern: the top 16 bits of the f32
// encoding, rounded to nearest with ties to even.
func bf16bits(v float64) uint16 {
	f := float32(v)
	b := math.Float32bits(f)
	if b&0x7f800000 == 0x7f800000 && b&0x7fffff != 0 {
		return uint16(b>>16) | 0x0040 // keep NaN quiet
	}
	lsb := (b >> 16) & 1
	b += 0x7fff + lsb
	return uint16(b >> 16)
}