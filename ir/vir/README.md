# `vir` - Vertex IR

`package vir` provides the in-memory representation of the `.vir` host and device Intermediate Representation (IR). Its sole responsibility is to **construct and describe** the IR; all semantic validation (such as ordering, typing, and name resolution) is deferred to `ir/verify`.

```go
import "github.com/vertex-language/vvm/ir/vir"
```

## Scope & Capabilities

Unlike device-only IRs, `vir` represents a complete, full-program translation unit. Core capabilities include:

* **Program Structure:** Support for namespaces, thread-local storage (TLS) globals, and custom initializers.
* **Dependencies:** Cross-module imports alongside static, shared, and framework link dependencies.
* **Entry Points:** `entry`-attributed functions that auto-wire to synthesized C-runtime (crt) stubs via the `entrypoint.go` mechanism.
* **Variadics:** Functions can accept variadic arguments, utilizing a `valist` cursor to read tail values.

## File Structure

| File | Purpose |
| --- | --- |
| `module.go` | Core IR definitions (`Module`, `Struct`, `Global`, `Function`, `Block`, `Instruction`, Terminators). |
| `types.go` | The type system (scalars, composites, opaque types) and structural equality predicates. |
| `opcode.go` | The closed opcode vocabulary, rigorously validated at package load via `opTable`. |
| `operand.go` | The operand grammar, defining identifiers, literals, and memory orderings. |
| `builder.go` | A fluent, 1:1 API for constructing IR objects without enforcing structural validation. |
| `targets.go` | Canonical architecture, OS, and ABI vocabularies, plus pointer widths and binary formats. |
| `linkfile.go` | Logic for deriving on-disk link filenames dynamically based on target binary formats. |
| `mainsig.go` | Recognizes standard `main()` signature shapes to determine C-runtime staging requirements. |
| `float.go` | Literal formatting guarantees (ensuring every finite float carries a decimal or exponent). |

## Core IR Components

### 1. Types

* **Scalars:** Integers from `i1` through `i128`, floats (`f16`, `f32`, `f64`), and a single, untyped `ptr`.
* **Composites:** Element-typed vectors (`vec`), plus memory-only `struct` and `array` types.
* **Opaque:** The `valist` type is reserved exclusively for target-defined variadic cursors (e.g., as an `alloca` result).

### 2. Opcodes

* Opcodes are strictly typed integer constants, preventing string-based typos during compilation.
* Every operation's arity, numeric constraints, and result rules are centralized in a single `opTable`.
* To ensure data integrity, `init()` enforces development-time invariants by panicking if the opcode table contains duplicates or missing entries.

### 3. Modules & Execution Flow

* A `Module` strictly adheres to a mandatory section order: Structs, Signatures, Constants, Globals, Links, Externs, Imports, Functions.
* The `AttributeEntry` flag marks the platform handoff point and forces a bare symbol, with a limit of one per module.
* Execution logic happens inside a `Block`, which contains a series of body-line instructions and concludes with exactly one terminator (e.g., `Branch`, `Return`, `Switch`).

### 4. Targets & Linking

* `vir` strictly separates canonical target names from build-system aliases (e.g., `amd64` must be resolved to `x86_64` prior to verification).
* Binary formats (ELF, Mach-O, PE) and appropriate file extensions (like `.so`, `.dylib`, `.dll`) are derived automatically from the host OS and the required Link kind.

---

## Builder Example

The `vir` builder API mirrors the IR one-to-one, allowing you to fluently construct modules, functions, and instructions. Here is a simple snippet demonstrating how to build a basic `main` module that links to an external library:

```go
m := vir.NewModule("app")
m.SetNamespace("acme")
m.SetTarget("x86_64", "linux", "gnu")

m.DeclareLink(vir.LinkShared, "SDL2")
sdl := m.DeclareExternGroup("SDL2") // dependency name must match the Link above
sdl.DeclareFunction("SDL_Init",
    []vir.Param{{Name: "flags", Type: vir.I32}}, vir.I32)

fb := m.DeclareFunction("main",
    []vir.Param{
        {Name: "argc", Type: vir.I32},
        {Name: "argv", Type: vir.Ptr},
    },
    vir.I32, false, vir.AttributeEntry)

status := fb.Call("status", "SDL_Init", vir.IntLiteral(0))
fb.Return(status)

sig := vir.RecognizedMainSignature(fb.Function) // Returns: MainSignatureArgcArgv
```