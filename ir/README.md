# `ir` - Vertex IR Package Family

`ir` contains the intermediate representation types and validation logic used by the Vertex toolchain (`vvm`). It is split into three sibling packages that separate *construction* from *validation*, and *host* from *device* representations.

```go
import "github.com/vertex-language/vvm/ir/vir"
import "github.com/vertex-language/vvm/ir/gvir"
import "github.com/vertex-language/vvm/ir/verify"
```

## Packages

| Package | Role |
| --- | --- |
| [`vir`](./vir/README.md) | Host-side IR. Represents a complete, full-program translation unit — namespaces, globals, links/externs, imports, entry points, and variadic functions. |
| [`gvir`](./gvir/README.md) | Device-side IR. Represents a single GPU kernel module — no host abstractions (no globals, linkage, externs, or imports), with address-space-qualified pointers and backend-specific layout/capability rules (PTX, AMDGCN, MSL). |
| [`verify`](./verify/README.md) | Semantic verification for `vir` modules — the single place that checks structural, typing, naming, and dataflow rules that the IR builders themselves don't enforce. |

## Design Principles

* **Construction vs. validation are strictly separated.** Both `vir` and `gvir` are pure builders: their fluent APIs construct IR objects without enforcing semantic legality. `verify` is the only package that re-derives the language spec's ordering, typing, dataflow, and naming rules by reading `vir`'s exported types from the outside.
* **Fixed section order.** Host modules (`vir`) follow a mandatory declaration order — Structs, Signatures, Constants, Globals, Links, Externs, Imports, Functions — and `verify` walks modules in that same order so errors are always reported deterministically, naming the first rule violated rather than an arbitrary one.
* **Host and device IRs are distinct.** `vir` is a full host translation unit with linkage and entry points; `gvir` is a restricted, flat-namespace device translation unit with no trap semantics, indirect calls, or function pointers. They share structural conventions (closed opcode vocabularies validated at load time, canonical target vocabularies, float-literal formatting rules) but are otherwise independent type systems.
* **Verification is single-module and non-transitive.** `verify.Verify` checks one module in isolation; cross-module import resolution and naming collisions are left to a separate `importer` package, which runs `Verify` on each module first.

## Where to Start

* Building a host program IR → see `vir`'s builder example.
* Building a GPU kernel IR → see `gvir`'s builder example (e.g. the `saxpy` kernel).
* Checking that a constructed `vir.Module` is well-formed before lowering/codegen → call `verify.Verify(m)`.