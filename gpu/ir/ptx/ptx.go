// Package ptx is a structured, in-memory IR for NVIDIA PTX (Parallel Thread
// Execution) modules.
//
// The IR models a .ptx translation unit: module directives, module-scoped
// variables, kernels (.entry), device functions (.func), and instruction
// bodies. It carries no formatting logic; text printing lives in
// ptx/encoding/text.
//
// Design rules, in order of precedence:
//
//   - Everything in the API corresponds to something in the PTX grammar.
//     There are no convenience types drawn from Go's type system.
//   - Instructions are values, not strings. An Instr holds an Op, a typed
//     Quals struct, positional Types, and Operands. The mnemonic is derived
//     on demand in canonical qualifier order, so equivalent IR always prints
//     byte-identically regardless of the order qualifiers were supplied.
//   - Bodies are editable. Emit methods return *Instr and Body supports
//     insert/replace/remove, so analysis and rewrite passes are ordinary Go.
//   - No inference. This package does not type-check operand compatibility
//     or infer rounding modes. Verify reports structural and version-gating
//     problems; ptxas remains the verifier of record.
//
// PTX has no binary wire format: the .ptx text is the interchange format
// consumed by ptxas and the CUDA driver. Decoding is out of scope.
package ptx