# msl

The `msl` package provides a structured, in-memory Intermediate Representation (IR) for Apple Metal Shading Language (MSL) translation units.

It models `.metal` files explicitly — includes, using-directives, module-scope constants, structs, function-constants, and stage functions (`kernel`, `vertex`, `fragment`, and friends), down through statement and expression bodies. The IR focuses purely on structure; all text formatting and generation logic is delegated to the `msl/encoding/text` package.

## Design Principles

- **Grammar-driven.** Every exported symbol in the API corresponds directly to a construct in the MSL grammar. Address spaces, attributes, and templates are modelled as typed grammar, not string escapes.
- **Declare-before-use, by construction.** `Module.Decls` is a single ordered list rather than per-kind buckets. MSL requires declarations to precede their use — `Module.Add`, `Constant`, `Alias`, and friends append in call order, so a generated `constant Params kDefaults = {...};` naturally follows its `struct Params`.
- **Editable bodies.** A `Block` supports standard array mutations — `.Append()`, `.InsertBefore()`, `.Replace()`, `.Remove()` — alongside fluent emit methods (`Let`, `Assign`, `If`, `For`, `Range`, `Switch`, ...) so passes can build and rewrite statement lists directly.
- **No implicit inference.** The package performs no type checking and no operand validation. `msl.Verify` handles structural and revision-gating validation, but the `metal` frontend remains the verifier of record.
- **Name hygiene as a pass, not a side effect.** `Resolve` disambiguates shadowed declarations (`sum`, `sum_1`, `sum_2`, ...) on demand, so splicing a detached function body into a module stays safe until you explicitly ask for renaming.
- **Version is explicit.** `Version` maps directly to the `-std=` flag, the `__METAL_VERSION__` macro, and the feature floors `Verify` enforces. There is no default revision — `NewModule` requires one.

## Quick Start

The example below builds a module, declares a kernel with buffer-bound parameters, emits a bounds-checked body, verifies it, and renders the final source text.

```go
package main

import (
	"log"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/gpu/ir/msl/encoding/text"
)

func main() {
	// A module requires a language revision; it seeds <metal_stdlib> and
	// `using namespace metal;` automatically.
	m := msl.NewModule(msl.Metal30)

	k := msl.NewKernel("vector_add")

	// Params return an Expr, so the reference can be used directly in the body.
	a := k.Param("a", msl.Ptr(msl.Device, msl.Const(msl.Float)), msl.Buffer(0))
	b := k.Param("b", msl.Ptr(msl.Device, msl.Const(msl.Float)), msl.Buffer(1))
	c := k.Param("c", msl.Ptr(msl.Device, msl.Float), msl.Buffer(2))
	tid := k.Param("tid", msl.UInt, msl.ThreadPositionInGrid)

	body := k.Body
	n := body.Let(msl.UInt, "n", msl.U(1<<20)) // stand-in for a real bound

	body.If(tid.Ge(n), func(b *msl.Block) {
		b.Return()
	})
	body.Assign(c.At(tid), a.At(tid).Add(b.At(tid)))

	m.Add(k)

	// Verify checks structure and revision gating (e.g. an attribute that
	// postdates the module's -std=).
	for _, diag := range msl.Verify(m) {
		log.Println(diag)
	}

	// Resolve disambiguates any shadowed names before printing.
	msl.Resolve(m)

	// Print is the only encoder: .metal source is the wire format the metal
	// frontend consumes.
	src, err := text.Print(m)
	if err != nil {
		log.Fatal(err)
	}

	log.Println(src)
}
```

## Advanced Usage

### The `Raw` Escape Hatches

MSL surfaces new stdlib functions, attributes, and scalar spellings faster than any typed package can track. Every layer of the IR has an untyped escape hatch that still participates in walking, resolution, and printing:

```go
// Module scope.
m.Add(&msl.RawDecl{Text: "// hand-written epilogue"})

// Statement scope.
body.Raw("simdgroup_barrier(mem_flags::mem_threadgroup);")

// Expressions.
x := msl.Raw("as_type<float>(bits)")

// Attributes without a typed constructor.
p.Attrs = append(p.Attrs, msl.RawAttr("some_new_attribute", "1"))

// Scalars and types newer than this package's ScalarType table.
t := msl.ScalarType("float8_e4m3")
```

`RawAttr` and unlisted `ScalarType` spellings pass through `Verify` unchecked — they are accepted anywhere and never version-gated, since the package has no floor to check them against.

### Version Gating

`Version.GTE` and `Module.VersionGate` model MSL's `#if __METAL_VERSION__ >= N` convention directly, for code that must ship against more than one OS floor:

```go
m.VersionGate(msl.Metal40,
	[]msl.Decl{tensorPath},
	[]msl.Decl{bufferFallbackPath},
)
```

`Verify` cross-checks version-gated attributes and types (`bfloat`, `auto`, tensors, cooperative tensors, `[[stitchable]]`, ...) against the module's declared `Version` and emits warnings — not errors — when a feature postdates the floor, since a `VersionGate` branch may intentionally target a newer revision than the module's baseline.