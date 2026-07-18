// float.go
package vir

import (
	"math"
	"strconv"
)

func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	s := strconv.FormatFloat(v, 'g', -1, 64)
	// Grammar requires a '.' in finite float literals (§1.1).
	if !containsDotOrExp(s) {
		s += ".0"
	}
	return s
}

func containsDotOrExp(s string) bool {
	for _, c := range s {
		if c == '.' || c == 'e' || c == 'E' {
			return true
		}
	}
	return false
}