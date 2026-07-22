// verify.go
// Package verify implements ir/verify: the single place that checks a
// *vir.Module is semantically well-formed (see verify.md). ir/vir only
// builds — it never checks anything (opcode.go's internal opTable exists
// solely to back Opcode.String/ParseOpcode). Every arity/type-constraint/
// dataflow rule described in the spec is re-derived here, reading vir's
// exported types from the outside.
//
// Single-module only. No import awareness, no cross-module anything —
// that's importer's job, which runs Verify on each module before doing
// any cross-module reference checking of its own.
package verify

import (
	"github.com/vertex-language/vvm/ir/vir"
)

// Verify checks m against every single-module invariant documented in
// verify.md, in module-section order (§2.1), so an error always names the
// first rule violated in file order rather than an arbitrary one.
func Verify(m *vir.Module) error {
	names := newNameTable()

	if err := checkTarget(m); err != nil {
		return err
	}
	if err := checkStructs(m, names); err != nil {
		return err
	}
	if err := checkFunctionSignatures(m, names); err != nil {
		return err
	}
	if err := checkConstants(m, names); err != nil {
		return err
	}
	if err := checkGlobals(m, names); err != nil {
		return err
	}
	if err := checkLinks(m); err != nil {
		return err
	}
	if err := checkExterns(m, names); err != nil {
		return err
	}
	if err := checkImports(m); err != nil {
		return err
	}
	if err := checkFunctions(m, names); err != nil {
		return err
	}
	return nil
}