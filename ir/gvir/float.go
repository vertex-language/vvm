// float.go
package gvir

import (
	"math"
	"strconv"
	"strings"
)

// Float literal formatting. §2 requires dec-float to carry digits on both
// sides of a '.', with an exponent whose sign may only ever be '-'; Go's
// FormatFloat produces neither shape unaided, so every finite literal goes
// through normalizeDecFloat.
//
// hex-float is exact by construction and is the portable spelling for a bit
// pattern (§2, §13): two conforming frontends produce identical bits from
// identical text.

func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return normalizeDecFloat(strconv.FormatFloat(v, 'g', -1, 64))
}

// FormatFloatBits emits the shortest decimal that round-trips to v in a
// destination format of the given width — 16, 32 or 64. A frontend that
// knows the destination type should use this so the correctly-rounded
// conversion §2 mandates is also the shortest one.
func FormatFloatBits(v float64, bits int) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	if bits != 32 && bits != 64 {
		bits = 64 // f16 and bf16 have no strconv width; 64 round-trips safely
	}
	return normalizeDecFloat(strconv.FormatFloat(v, 'g', -1, bits))
}

// formatHexFloat emits the exact hex-float spelling of v.
func formatHexFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return normalizeHexExponent(strconv.FormatFloat(v, 'x', -1, 64))
}

// HexFloat is the exported spelling helper: the text it returns converts to
// v bit for bit on any conforming frontend.
func HexFloat(v float64) string { return formatHexFloat(v) }

// normalizeDecFloat reshapes Go's output into the §2 dec-float production:
// "-"? digits "." digits ("e" "-"? digits)?
func normalizeDecFloat(s string) string {
	mant, exp := s, ""
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant, exp = s[:i], s[i+1:]
	}
	switch {
	case !strings.Contains(mant, "."):
		mant += ".0"
	case strings.HasSuffix(mant, "."):
		mant += "0"
	}
	if strings.HasPrefix(mant, ".") {
		mant = "0" + mant
	} else if strings.HasPrefix(mant, "-.") {
		mant = "-0" + mant[1:]
	}
	if exp == "" {
		return mant
	}
	neg := false
	exp = strings.TrimPrefix(exp, "+")
	if strings.HasPrefix(exp, "-") {
		neg, exp = true, exp[1:]
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		return mant // exponent 0: the mantissa alone says the same thing
	}
	if neg {
		return mant + "e-" + exp
	}
	return mant + "e" + exp
}

// normalizeHexExponent strips the '+' and leading zeros Go writes after 'p';
// the §2 hex-float exponent is decimal digits with an optional '-' only.
func normalizeHexExponent(s string) string {
	i := strings.IndexByte(s, 'p')
	if i < 0 {
		return s
	}
	head, exp := s[:i], s[i+1:]
	neg := false
	exp = strings.TrimPrefix(exp, "+")
	if strings.HasPrefix(exp, "-") {
		neg, exp = true, exp[1:]
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	if neg && exp != "0" {
		return head + "p-" + exp
	}
	return head + "p" + exp
}