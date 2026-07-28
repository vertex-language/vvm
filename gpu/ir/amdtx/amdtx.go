// Package amdtx is a structured, in-memory IR for AMDTX modules.
//
// AMDTX is a virtual, target-shaped IR for AMD GPU compute kernels: it
// occupies the niche PTX does for NVIDIA, but owns its own lowering
// pipeline because amdgcn has no virtual-register form.
//
// The IR models an .amdtx translation unit: the preamble (.amdtx, .target,
// .wave), the file table, module-scope .global/.shared objects, and .kernel
// and .func bodies. It carries no formatting logic; text printing lives in
// amdtx/encoding/text.
//
// Design rules, in order of precedence:
//
//   - Everything in the API corresponds to something in the AMDTX grammar.
//     There are no convenience types drawn from Go's type system.
//   - Instructions are values, not strings. An Instr holds an Op, a data
//     width, operands, modifiers and an optional pinned encoding. The
//     mnemonic is derived on demand, so equivalent IR prints identically.
//   - Widths, not types. A register carries .bN; interpretation lives in
//     the mnemonic. Load and store mnemonics derive their width suffix from
//     the data register, so V9 holds by construction.
//   - Structured control flow is a first-class item, not a label pattern.
//     EXEC-mask expansion is the lowering pipeline's business (P2).
//   - No implicit synchronisation. Adjacency conveys nothing (P6); waits
//     and fences are instructions you emit.
//   - Verify accepts or rejects; it never rewrites (P7). Encoding selection
//     (V25) and displacement ranges (V20) are lowering obligations and are
//     out of scope here.
package amdtx