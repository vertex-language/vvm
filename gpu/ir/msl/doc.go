// Package msl is an in-memory intermediate representation for Apple Metal
// Shading Language translation units.
//
// MSL is C++ with extensions, so the IR is a declaration/statement/expression
// tree over the MSL grammar — not a flat instruction stream. Every exported
// symbol corresponds to a grammar production. Templates, the preprocessor, and
// address-space qualifiers are modelled as grammar, not as string escapes.
//
// Construction, checking, and printing are three separate steps:
//
//	m := msl.NewModule(msl.Metal32)   // build
//	diags := msl.Verify(m)            // check (structure + version gating)
//	src, err := text.Print(m)         // print (the only encoder)
//
// The package performs no type inference and no operand checking. The metal
// frontend is the verifier of record.
package msl