# crt

`github.com/vertex-language/vvm/crt`

Hand-encoded process-entry stubs ("_start"-style functions), built as
real relocatable objects, for the (arch, os) combinations vvm knows how
to auto-wire a recognized `main()` signature for.

## Why this isn't a `*vir.Module`

Reading the process's incoming stack pointer at the very first
instruction of a program has no `vir` opcode. `ir/vir`'s §4 instruction
vocabulary is closed and spec-fixed on purpose, and deliberately has no
operation for "the raw value some register held on entry, before any
parameter binding happened" — that's not an oversight, it's the same
"strict semantics, minimal UB, one behavior per opcode" stance §1 states
for everything else in the language.

Every real toolchain solves this the same way: crt0 is hand-assembled,
not compiled from the language it bootstraps. This package is vvm's
crt0. It's built directly against each target's raw instruction
encoding — one layer below `vir` entirely — and handed to the linker as
an ordinary object file (via `objectfile/<format>`) alongside the
module's own.

## Registration

Each `(arch, os)` pair that has a stub registers a `BuildFunc` via
`Register` in its own file's `init()` — additive, same shape as
`linker/elf`'s `RegisterPatcher` and friends. `entrypoint.go` in the
parent `vvm` package calls `Lookup` and fails loudly, rather than
guessing, when nothing is registered for the requested target.

Only `x86_64`/`linux` is implemented today (`x86_64_linux_stub.go`).
Every other `(arch, os)` combination requires the module to name its
entry function `"_start"` and write the raw process-entry sequence by
hand — see `entrypoint.go`'s gate for exactly when that's required.