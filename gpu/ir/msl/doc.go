// Package msl is an in-memory intermediate representation for Apple Metal
// Shading Language translation units.
//
// MSL is C++ with extensions, so the IR is a declaration/statement/expression
// tree over the MSL grammar — not a flat instruction stream. Every exported
// symbol corresponds to a grammar production. Templates, the preprocessor, and
// address-space qualifiers are modelled as grammar, not as string escapes.
//
// Construction and printing are two separate steps:
//
//	m := msl.NewModule(msl.Metal32)   // build
//	src, err := text.Print(m)         // print (the only encoder)
//
// The package performs no type inference, no operand checking, and no
// structural or version-gating validation. The metal frontend is the
// verifier of record.
package msl