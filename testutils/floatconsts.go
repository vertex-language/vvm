// floatconsts.go
package main

import "math"

var (
	floatNaN     = math.NaN()
	floatNegZero = math.Copysign(0, -1)
)