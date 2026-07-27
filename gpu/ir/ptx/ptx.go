// Package ptx is an in-memory intermediate representation (IR) for NVIDIA
// PTX (Parallel Thread Execution) modules. It models the structure of a
// .ptx translation unit — version/target/address-size header, global
// variables, kernels, device functions, and instruction bodies — without
// any formatting logic. Text printing lives in ptx/encoding/text.
//
// PTX has no binary wire format: the .ptx text is the interchange format
// consumed by ptxas and the CUDA driver. Decoding (parsing) is out of
// scope for v1.
package ptx