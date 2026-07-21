package aarch64

// StageBytes returns the byte size of the argument-staging area for n
// pointer/integer-width arguments, kept 16-byte aligned per AAPCS64.
func StageBytes(n int) int32 { return int32((8*n + 15) &^ 15) }

// RegArgs returns how many of n arguments are passed in x0-x7; the rest
// go on the stack, first stack argument at SP at the call.
func RegArgs(n int) int {
	if n > 8 {
		return 8
	}
	return n
}