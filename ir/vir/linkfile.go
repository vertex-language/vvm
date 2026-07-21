// linkfile.go
package vir

// DeriveLinkFile computes the on-disk filename l implies for output format
// f, using exactly the same short/exact-name rules Verify enforces (§7.4):
// a name containing '.' or a path separator is exact and returned
// byte-for-byte; otherwise it's a short name expanded via the table in
// §7.4 (e.g. shared "SDL2" -> "libSDL2.so" on ELF).
//
// Exported so build-layer code (vvm's dispatch.go) can resolve the same
// filename this package's own Verify already validated, rather than
// re-implementing §7.4's derivation table a second time and risking the
// two definitions drifting apart.
func DeriveLinkFile(l *Link, f BinFormat) (string, error) {
	return deriveLinkFile(l, f)
}