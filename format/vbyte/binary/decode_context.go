// File: format/vbyte/binary/decode_context.go
package binary

import "github.com/vertex-language/vvm/ir/vir"

// pendingFixup records an InitAddressOf whose target name can only be
// resolved once the whole module has been decoded — specifically, a
// Global (decoded before Functions, per §3.1's fixed section order) whose
// addr initializer targets a Function is a legal forward relocation
// (README §6.2: "relocated pointers to earlier functions/globals"), not a
// value forward-reference subject to declare-before-use.
type pendingFixup struct {
	kind  int // 0 = global, 1 = fn
	index int
	ptr   *vir.InitAddressOf
}

// decodeContext accumulates the index-space tables (§4) as sections are
// decoded, in file order.
type decodeContext struct {
	strings   []string
	structs   []*vir.Struct
	fnsigs    []*vir.FunctionSignature
	consts    []*vir.Constant
	globals   []*vir.Global
	fns       []*vir.Function
	externFns []*vir.ExternFunction // dense across all extern groups (§4)
	pending   []*pendingFixup
}

func newDecodeContext() *decodeContext {
	return &decodeContext{}
}