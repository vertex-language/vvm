// Package msl is an in-memory intermediate representation (IR) for Apple
// Metal Shading Language (MSL) translation units. It models the structure
// of a .metal source file — language-version header, includes, module
// constants, structs, and function bodies — without any formatting logic.
// Text printing lives in the encoding/text sub-package.
//
// See the README for the full design rationale. In short: text is the
// wire format (the metal compiler consumes .metal source), the body IR
// is a statement tree over a small expression IR, there is no register
// model, thread indices are attributed parameters, and this package does
// no type inference — the metal frontend is the verifier of record.
package msl