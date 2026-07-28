# MSL — IR and Text Specification

**Version 1.0 · Target audience: implementers of the IR, `Verify`, `Resolve`, the text printer, and any pass over `msl.Module`**

---

## 0. Front Matter

### 0.1 Document conventions

**MUST**, **MUST NOT**, **SHALL**, **SHOULD**, and **MAY** carry RFC 2119 meanings. Rules labelled **V***n* are `Verify` errors; rules labelled **W***n* are `Verify` warnings — the module still prints. The complete index is §16.

Sections marked *(informative)* are non-normative.

### 0.2 The Apple baseline rule

The normative definition of the language is Apple's *Metal Shading Language Specification*. This document does not redefine MSL; it specifies **the subset the `msl` package models** and **the exact text the printer emits**. Where the package narrows, normalises, or declines to model something, the departure is called out inline as a **Model deviation** with a justification.

Adding an exported symbol that has no counterpart in the MSL grammar is a package defect. Convenience types drawn from Go's type system are not a reason to deviate.

### 0.3 Normative references

* Apple *Metal Shading Language Specification* — the language of record.
* Apple *Metal Feature Set Tables* — per-family availability, which this package does **not** model.
* Xcode `metal` / `metallib` toolchain — the `-std=` ↔ OS mapping in §4.

The `metal` frontend is the **verifier of record**. `Verify` is a fast structural and revision-gating pre-pass, never a substitute. In particular the package performs no type checking, no overload resolution, no implicit-conversion analysis, and no address-space compatibility checking.

### 0.4 File extensions

| Extension | Content | Direction |
|---|---|---|
| `.metal` | MSL source text | **out only** |
| `.h` | shared header included by `.metal` | out only (as `Include{Local: true}`) |
| `.air` / `.metallib` | frontend / linker output | out of scope |

**Model deviation:** unlike a round-tripping IR, the package has **no parser**. MSL has no binary IR the package targets — the `.metal` text *is* the interchange format consumed by the frontend and by `newLibraryWithSource:` — so decoding is out of scope and there is no `parse(print(m)) == m` obligation. The corresponding guarantee is §15's byte-stability rule instead.

---

## 1. Scope and Design Principles

The `msl` package is a structured, in-memory IR for one `.metal` translation unit: includes, using-directives, aliases, module-scope constants and function constants, structs, and stage functions, down through statement and expression bodies. It carries no formatting logic; text lives in `msl/encoding/text`.

**P1 — Apple only.** One language revision per module. There is no cross-vendor shader abstraction and no portable-IR layer.

**P2 — Grammar-driven, with open leaves.** Every exported symbol corresponds to a construct in the MSL grammar. Address spaces, attributes, and template arguments are modelled as typed grammar, not as string escapes.

> **Model deviation (deliberate, and the central difference from the `ptx` package):** the *leaf spellings* — `ScalarType`, `TextureType.Kind`, `AttrName`, `BinOp`, `UnOp`, `AssignOp` — are Go string types rather than closed enums. MSL is C++ and its stdlib surface grows faster than any table tracks it, so an unlisted spelling MUST remain reachable without a package change. The cost is that invalid text *can* be constructed by string manipulation (`ScalarType("flaot")` is representable) and that `Verify` cannot gate what it does not know (§5.1, §7.4). This is accepted; the frontend is the verifier of record.

**P3 — A tree, not an instruction stream.** MSL is C++ with extensions, so the IR is a declaration/statement/expression tree over the grammar. There is no flat body, no opcode table, and no mnemonic derivation.

**P4 — Address space is part of the type.** `Ptr`, `Ref`, `VarDecl.Space`, and `TensorType.Space` carry it. It is never a property of an operation, because it is not one in MSL.

**P5 — Expressions are trees; methods are sugar.** `Expr` wraps an `ExprNode` so operators can be written postfix (`a.At(i).Add(b.At(i))`), which reads in evaluation order where nested constructors read inside-out. Precedence lives in exactly one table (`binPrec`), and the printer parenthesises from it (§11.3).

**P6 — Bodies are editable.** `Block` supports `Append`, `InsertBefore`, `Replace`, `Remove` alongside the fluent emit methods. Emit methods return the constructed node (`*If`, `*For`, `*Assign`, …) or an `Expr` referring to what was declared, so analysis and rewrite passes are ordinary Go.

**P7 — No inference.** The package does not type-check expressions, resolve overloads, insert conversions, or legalise. `Verify` reports structural and gating problems only, never mutates, and never stops at the first finding.

**P8 — Name hygiene is a pass, not a side effect.** `Resolve` disambiguates shadowed declarations on demand. Building a body detached and splicing it into a function is therefore safe: names are disambiguated once, when asked for (§12).

**P9 — Version is explicit.** `NewModule` requires a revision. It is the `-std=` floor, the `__METAL_VERSION__` value, and the thing `Verify` gates against. There is no default, because a defaulted revision silently decides which OS releases can load the resulting library.

**P10 — Escape hatches stay structural.** `RawDecl`, `RawStmt`, `RawExpr`, `RawAttr`, and unlisted `ScalarType` spellings are ordinary IR nodes: they walk, resolve, verify, and print exactly like modelled ones. Only their *contents* are opaque.

---

## 2. Lexical Structure *(of the emitted text)*

Case-sensitive C++ lexis. Keywords, type spellings, and attribute names are lowercase.

| Token class | Form |
|---|---|
| Identifier | `[A-Za-z_][A-Za-z0-9_]*` |
| Integer literal | signed decimal (`IntExpr`) |
| Unsigned literal | decimal + `u` suffix (`UintExpr`) |
| Float literal | Go `'g'` formatting, with `.0` appended when the result contains none of `.`, `e`, `E` |
| Bool literal | `true` \| `false` |
| Attribute | `[[` name ( `(` arg `,` … `)` )? `]]` |
| Preprocessor line | `#` directive, always at **column 0** |
| Line comment | `//` to end of line |
| Raw text | verbatim, re-indented line by line (`RawStmt`) or emitted as-is (`RawDecl`) |

Statements terminate with `;`. Blocks use `{ }`. There are no block comments and no string literals in the modelled grammar — the only quoted text is a local `#include "…"`.

Whitespace is insignificant to the frontend and significant only to the printer (§15).

Trailing comments (`Instr`-style `// …` after a statement) are emitted **only** when the printer is configured with `WithComments(true)`; the default is **on**. The module header comment block is controlled by `WithHeader` and defaults to `Generated by vertex`.

**Model deviation:** float literals carry **no** `f` or `h` suffix, so literal typing follows C++ default rules at the use site. See §19 gap 5 for the consequences for non-finite values.

---

## 3. Module Structure and Grammar

### 3.1 Emission order

`Module` holds declarations in **one ordered list**, not per-kind lists, because MSL requires declaration before use and the relative order of includes, aliases, structs, constants, and functions is semantically meaningful: a `constant Params kDefaults = {…};` MUST follow `struct Params`.

The printer emits, in this order:

1. **Header comment** — optional, `//`-delimited block, followed by a blank line.
2. **`-std=` comment** — `// -std=<Version.Std()>`, followed by a blank line.
3. **Declarations** — `Module.Decls` in source order, with blank-line separation per §15.3.

There is no preamble the printer synthesises. `<metal_stdlib>` and `using namespace metal;` are ordinary declarations seeded by `NewModule`, and a caller MAY remove or reorder them.

### 3.2 Global rules

* **Declare before use.** The declaration list is the source order; the package does not reorder.
* **One flat module namespace**, plus per-function and per-block scopes. `Resolve` is what enforces hygiene across them (§12).
* **Include dedupe.** `Module.Include(name)` is a no-op if a *system* include of that name is already present. A local include (`Include{Local: true}`) with the same name does not suppress it, and vice versa.
* **Using dedupe.** `Module.Using(ns)` is a no-op if that namespace is already in use.
* **Bodiless functions.** A `*Function` with a nil `Body` prints as a declaration (`;` in place of the block). A *stage* function MUST have a body (**V3**).

### 3.3 Grammar

```text
module        := header-comment? std-comment? decl*

decl          := include | using-dir | alias-decl | comment-decl | raw-decl
               | var-decl | struct-def | function | pp-if-decl

include       := "#include" ("<" name ">" | '"' name '"')
using-dir     := "using" "namespace" ident ";"
alias-decl    := "using" ident "=" type ";"
comment-decl  := "//" text
raw-decl      := text
pp-if-decl    := "#if" cond decl* ("#else" decl*)? "#endif"

struct-def    := "struct" ident "{" field* method* "};"
field         := declarator attr-list? ";"
method        := function

function      := attr-line* stage? type ident "(" param-list? ")" attr-list?
                 (block | ";")
attr-line     := attr
stage         := "kernel" | "vertex" | "fragment" | "intersection"
               | "object" | "mesh"
param-list    := param ("," param)*
param         := declarator attr-list?

var-decl      := space? declarator attr-list? ("=" expr)? ";"
declarator    := type ident | type ident "[" int "]"
space         := "device" | "constant" | "threadgroup"
               | "threadgroup_imageblock" | "thread" | "ray_data"
               | "object_data"

block         := "{" stmt* "}"
stmt          := var-decl | assign | expr-stmt | return | inc-dec
               | if | for | while | do-while | switch | scope
               | "break" ";" | "continue" ";" | comment | blank
               | raw-stmt | pp-if

assign        := expr assign-op expr ";"
assign-op     := "=" | "+=" | "-=" | "*=" | "/=" | "%="
               | "&=" | "|=" | "^=" | "<<=" | ">>="
expr-stmt     := expr ";"
return        := "return" expr? ";"
inc-dec       := ("++" | "--") expr ";"
if            := "if" "(" expr ")" block ("else" (if | block))?
for           := "for" "(" stmt? ";" expr? ";" stmt? ")" block
while         := "while" "(" expr ")" block
do-while      := "do" block "while" "(" expr ")" ";"
switch        := "switch" "(" expr ")" "{" case* default? "}"
case          := ("case" expr ":")+ stmt* ("break" ";")?
default       := "default" ":" stmt* "break" ";"
scope         := block
pp-if         := "#if" cond stmt* ("#else" stmt*)? "#endif"

expr          := ident | int | uint | float | bool | raw
               | expr bin-op expr | un-op expr
               | expr "[" expr "]" | expr "." ident
               | ident targs? "(" arg-list? ")"
               | expr "." ident targs? "(" arg-list? ")"
               | type "(" arg-list? ")"
               | expr "?" expr ":" expr
               | "{" arg-list? "}"
               | "(" expr ")"
targs         := "<" targ ("," targ)* ">"
targ          := type | expr

type          := scalar | ident | "const" type
               | scalar int | scalar int "x" int | "packed_" scalar int
               | "atomic_" scalar
               | space type "*" | space type "&"
               | texture-type | "sampler" | "imageblock" "<" ident ">"
               | ident targs
               | "tensor" "<" space type "," dextents "," tensor-tag ">"
               | "cooperative_tensor" "<" type "," dextents ">"
texture-type  := ident "<" type "," "access" "::" access ">"
access        := "sample" | "read" | "write" | "read_write"
dextents      := "dextents" "<" "int" "," int ">"
tensor-tag    := ("tensor_handle" | "tensor_inline") ("," "tensor_offset")?
attr          := "[[" ident ("(" text ("," text)* ")")? "]]"
attr-list     := attr (" " attr)*
```

**Model deviation:** the grammar above is the *emitted* subset, not all of MSL. Templates, lambdas, namespaces other than `metal`, operator overloads, inheritance, and constructors/destructors on modelled structs are not represented; `RawDecl` and `Struct.Methods` are the pressure valves.

---

## 4. Versions

`Version{Major, Minor, OS}` identifies a language revision. MSL has no in-source version pragma, so the revision is carried by the module and surfaces in three places: the `-std=` flag, the `__METAL_VERSION__` macro, and the feature floors `Verify` enforces.

| Constant | `-std=` | `Macro()` | Introduces |
|---|---|---|---|
| `Metal23` | `metal2.3` | 230 | function pointers, intersection functions |
| `Metal24` | `metal2.4` | 240 | `[[stitchable]]` |
| `Metal30` | `metal3.0` | 300 | unified revision, mesh shaders |
| `Metal31` | `metal3.1` | 310 | `bfloat`, atomic texture ops |
| `Metal32` | `metal3.2` | 320 | lambdas, `auto`, shader logging |
| `Metal40` | `metal4.0` | 400 | tensors, cooperative tensors, Shader ML |
| `Metal41` | `metal4.1` | 410 | low-precision float formats |

* `IsZero` tests for absence; `GTE` compares revisions; `Macro()` is `Major*100 + Minor*10`; `Std()` is the `-std=` value; `String()` is `Std()`.
* Pre-3.0 split revisions are spelled through `OS`: `Version{Major: 2, Minor: 2, OS: "ios"}` prints `ios-metal2.2`. There are no predeclared constants for them.
* **`GTE` ignores `OS`.** `ios-metal2.4` therefore compares greater than `macos-metal2.3`, which is meaningless across OS lines. Callers comparing split revisions MUST check `OS` themselves (§19 gap 16).

`Module.VersionGate(v, then, els)` and `Block.VersionAtLeast(v, then)` build `#if __METAL_VERSION__ >= <Macro()>` conditionals directly. A shipping library that must load on more than one OS floor carries gated source rather than a single raised revision; this is why feature-floor findings are **warnings, not errors** (§16) — a gated branch may intentionally target a newer revision than the module's baseline.

---

## 5. Types

`Type` is a closed interface in the sense that only this package's types implement it, but several of them wrap open strings (P2).

### 5.1 Scalars

`ScalarType` is a string type spelled exactly as it prints. Predeclared: `Bool`, `Char`, `UChar`, `Short`, `UShort`, `Int`, `UInt`, `Long`, `ULong`, `Half`, `Float`, `BFloat`, `Size`, `PtrDiff`, `Void`, `Auto`.

Unlisted spellings are constructed directly — `ScalarType("float8_e4m3")` — and pass through `Verify` **unchecked and ungated**, since the package has no floor to check them against.

### 5.2 Constructed types

| Constructor | Prints | Floor | Notes |
|---|---|---|---|
| `Named(n)` | `n` | — | struct, alias, or any declared name |
| `Const(t)` | `const T` | — | |
| `Vec(e, n)` | `float4` | — | |
| `Mat(e, c, r)` | `float4x4` | — | columns × rows |
| `Packed(e, n)` | `packed_float3` | — | |
| `Atomic(e)` | `atomic_uint` | — | |
| `Array(e, n)` | `float t[256]` at a declarator; `float[256]` elsewhere | — | see §19 gap 10 |
| `Ptr(space, e)` | `device float*` | — | **V11** |
| `Ref(space, e)` | `device float&` | — | **V12** |
| `Texture(kind, e, acc)` | `texture2d<float, access::sample>` | — | `kind` is an open string |
| `Sampler` | `sampler` | — | |
| `ImageBlock(layout)` | `imageblock<L>` | — | |
| `Template(name, args…)` | `name<a, b>` | — | |
| `TensorHandle(space, e, rank)` | `tensor<device float, dextents<int, 2>, tensor_handle>` | Metal40 | **V13** |
| `TensorInline(space, e, rank)` | `tensor<device float, dextents<int, 2>, tensor_inline>` | Metal40 | |
| `CoopTensor(e, rank)` | `cooperative_tensor<float, dextents<int, 2>>` | Metal40 | no address space, by construction |
| `BFloat` | `bfloat` | Metal31 | |
| `Auto` | `auto` | Metal32 | |

`TensorType.Offset` adds the `tensor_offset` tag, permitting GPU-side slicing without a new descriptor.

**Ordering note (normative):** the address space precedes the pointee spelling, so `Ptr(Device, Const(Float))` prints `device const float*` (mutable pointer to const float) while `Const(Ptr(Device, Float))` prints `const device float*` (const pointer). The two are different types and the package does not normalise between them.

**Model deviation:** there is no unqualified pointer constructor. The frontend rejects one, so it is made unrepresentable rather than diagnosed.

**Model deviation:** array length lives at the declarator site (`float tile[256]`), not in the type spelling, because that is where C++ puts it. `ArrayType.Len` is therefore consumed by the printer's `declarator`, and any position that reaches the type through `typeStr` alone prints the C-invalid form.

### 5.3 Template arguments

`TypeArg` is either a type (`TArg(t)`) or a constant expression (`VArg(e)`). Metal 4's tensor-operator surface is template-shaped, so template arguments are modelled as grammar rather than raw text. `DynamicExtent` is the predeclared `dynamic_extent` sentinel for a dimension the operator loops internally.

---

## 6. Address Spaces

`AddressSpace` is a string type spelled as it prints.

| Constant | Prints | Notes |
|---|---|---|
| `NoSpace` | *(nothing)* | plain locals and function-scope temporaries |
| `Device` | `device` | |
| `Constant` | `constant` | requires an initializer (**V7**) |
| `Threadgroup` | `threadgroup` | |
| `ThreadgroupImageblock` | `threadgroup_imageblock` | |
| `Thread` | `thread` | |
| `RayData` | `ray_data` | |
| `ObjectData` | `object_data` | |

Address spaces qualify pointers, references, and variable declarations. They are **part of the type system, not of operations** (P4): there is no space qualifier on a load or a store, because MSL has none.

`Verify` checks only that pointer and reference types carry *some* space (**V11**, **V12**). It does not check that the space is legal for the position — a `threadgroup` module-scope variable passes and is rejected by the frontend.

---

## 7. Attributes

### 7.1 Shape

`Attr{Name AttrName, Args []string}`. The zero value means "no attribute" and is reported by `IsZero`. `Args` is `[]string`, not `[]Expr`: an attribute argument is a literal in every modelled case (§19 gap 20).

### 7.2 Placement

`Place` is a bit set of the syntactic positions an attribute may occupy: `OnParam`, `OnField`, `OnFunc`, `OnReturn`, `OnVar`.

### 7.3 The attribute table

`attrTable` drives `Verify`: `place` is the legal position mask (**V9**), `args` the required arity with `-1` meaning any (**V10**), `since` the revision floor with the zero `Version` meaning ungated (**W1**).

**Binding and indexing**

| Attribute | Constructor | Place | Args | Since |
|---|---|---|---|---|
| `buffer` | `Buffer(i)` | param | 1 | — |
| `texture` | `TextureAt(i)` | param | 1 | — |
| `sampler` | `SamplerAt(i)` | param | 1 | — |
| `threadgroup` | `ThreadgroupSlot(i)` | param | 1 | — |
| `id` | `ID(i)` | field | 1 | — |
| `function_constant` | `FunctionConstant(i)` | var, param | 1 | — |

**Kernel and dispatch built-ins** — all `OnParam`, arity 0, ungated: `thread_position_in_grid`, `thread_position_in_threadgroup`, `threadgroup_position_in_grid`, `threads_per_threadgroup`, `threads_per_grid`, `thread_index_in_threadgroup`, `simdgroup_index_in_threadgroup`, `thread_index_in_simdgroup`, `threads_per_simdgroup`, `dispatch_threads_per_threadgroup`.

**Graphics-stage built-ins**

| Attribute | Place | Args | Since |
|---|---|---|---|
| `stage_in`, `vertex_id`, `instance_id`, `front_facing` | param | 0 | — |
| `patch` | param | any | — |
| `payload` | param | any | Metal30 |
| `position`, `point_size` | field, return | 0 | — |
| `color` | field, return, param | 1 | — |
| `attribute` | field | 1 | — |

**Function-level**

| Attribute | Place | Args | Since |
|---|---|---|---|
| `max_total_threads_per_threadgroup` | func | 1 | Metal30 |
| `visible` | func | 0 | Metal23 |
| `stitchable` | func | 0 | Metal24 |
| `early_fragment_tests` | func | 0 | — |
| `invariant` | func, field | 0 | — |

### 7.4 The escape hatch

`RawAttr(name, args…)` builds an attribute with any spelling. Attributes absent from `attrTable` — including every `RawAttr` — are **accepted anywhere and never version-gated**. This is the P2 trade made explicit: the table is a convenience for the attributes the package knows, not a whitelist.

### 7.5 Printed positions

* **Function attributes** print one per line, at the function's indentation, *before* the signature.
* **Return attributes** (`Function.RetAttrs`) print as a suffix *after* the closing parenthesis of the parameter list.
* **Parameter, field, and variable attributes** print as a space-separated suffix after the declarator.

---

## 8. Declarations

`Decl` is implemented by `*Include`, `*Using`, `*Alias`, `*CommentDecl`, `*RawDecl`, `*PPIfDecl`, `*Struct`, `*Function`, and `*VarDecl`.

### 8.1 VarDecl is both a Decl and a Stmt

MSL makes no distinction between a module-scope `constant float kPi = 3.14;` and a local `threadgroup float tile[256];` — they are the same production at different scopes — so `*VarDecl` implements both interfaces and there is one node type, not two.

Function constants are `VarDecl`s in the `Constant` space carrying `[[function_constant(n)]]`; `Module.FnConst` is the constructor. They are the one exception to **V7**, which otherwise requires a `constant`-space variable to have an initializer.

### 8.2 Structs

`Struct{Name, Fields, Methods}`. Vertex and fragment IO, argument buffers, and imageblock layouts are all structs with attributed fields. MSL is C++, so a struct may also carry member functions; `Struct.Method(name, ret)` appends one.

`Struct.Field` returns a `*Field` handle, and `Expr.Fld(f)` builds member access from that handle, so a member reference cannot drift out of sync with the struct definition. `Expr.Sel(name)` is the untyped form, used for swizzles and stdlib members.

`Struct.Type()` and `Alias.Type_()` return a `NamedType` referring to the declaration.

**Model deviation:** `Alias.Type_` carries a trailing underscore because `Alias.Type` is already the field holding the aliased type. This is a wart, not design (§19).

### 8.3 Functions and stages

`Function{Stage, Name, Ret, RetAttrs, Params, Attrs, Body}`.

`Stage` is a **field, not a type**, because that is what it is in the grammar: `Plain` (no qualifier), `KernelStage`, `VertexStage`, `FragmentStage`, `IntersectionStage`, `ObjectStage`, `MeshStage`. Plain functions are distinguished by their attributes (`[[visible]]`, `[[stitchable]]`).

Constructors: `NewKernel`, `NewVertex`, `NewFragment`, `NewFunction`, and `NewStageFunction(stage, name)` for the rest. All seed a non-nil `Body`; assign `nil` explicitly for a forward declaration.

A nil `Ret` prints `void`.

`Function.Param(name, t, attrs…)` appends a parameter and **returns an `Expr` referring to it**. Thread and dispatch indices are declaration-site concerns: declare the attributed parameter once, then use the returned `Expr` throughout the body.

* **V1** — a function has a name.
* **V2** — a `kernel` function returns `void`.
* **V3** — a stage function has a body.
* **V4** — parameter names are unique within a function.

---

## 9. Blocks

`Block` is an ordered, editable statement list, and the **only** container for statements. Nested blocks are built with closures, so a block cannot be left unclosed and there is no depth to track.

Structural: `Stmts()`, `Len()`, `Append`, `InsertBefore`, `Replace`, `Remove`. `Stmts()` aliases the block's backing slice.

Emit methods are sugar over `Append` — every statement they build is an ordinary value that can also be constructed directly:

| Method | Emits | Returns |
|---|---|---|
| `Let(t, name, init)` | `T name = init;` | `Expr` referring to `name` |
| `Var(space, t, name)` | `space T name;` | `Expr` |
| `Declare(v)` | the given `*VarDecl` | `Expr` |
| `Assign(dst, src)` | `dst = src;` | `*Assign` |
| `SetOp(dst, op, src)` | `dst op src;` | `*Assign` |
| `Do(x)` | `x;` | `*ExprStmt` |
| `Return(x…)` | `return;` / `return x;` | `*Return` |
| `If(cond, fn)` | `if (cond) { … }` | `*If` |
| `For(init, cond, post, fn)` | `for (…) { … }` | `*For` |
| `Range(name, lo, hi, fn)` | `for (uint name = lo; name < hi; ++name)` | `*For` |
| `While` / `DoWhile` | as spelled | node |
| `Switch(tag)` | `switch (tag) { … }` | `*Switch` |
| `Scope(fn)` | bare nested block | `*Scope` |
| `PPIf(cond, fn)` / `VersionAtLeast(v, fn)` | `#if …` | `*PPIf` |
| `Break` / `Continue` / `Comment` / `Blank` / `Raw` | as spelled | — |

`Return` panics when given more than one value. It is the only panic in the package.

**Model deviation:** `Block` is neither a `Node` case in `Walk` nor a `Stmt`. The grammar's compound statement is `*Scope`, which owns a `*Block`. Passes that need to walk a bare block wrap it: `Walk(&Scope{Body: b}, fn)`. `Verify`'s unexported `b2node` does exactly this. The consequence is that a callback can never observe a `*Block` — see §19 gap 2, where `Resolve` assumes it can.

---

## 10. Statements

| Node | Prints |
|---|---|
| `*VarDecl` | §8.1 |
| `*Assign` | `Dst Op Src;` |
| `*ExprStmt` | `X;` |
| `*Return` | `return;` or `return X;` |
| `*IncDec` | `++X;` / `--X;` — **prefix only** |
| `*If` | `if (C) { … }`, with `Els` per §15.6 |
| `*For` | `for (Init; Cond; Post) { … }`; any clause may be nil/zero, and the semicolons are always emitted (`for (; i < n; )`) |
| `*While` | `while (C) { … }` |
| `*DoWhile` | `do { … } while (C);` |
| `*Switch` | §15.7 |
| `*Break` / `*Continue` | `break;` / `continue;` |
| `*Scope` | bare `{ … }`, used to bound the lifetime of locals |
| `*Comment` | `// text` on its own line |
| `*Blank` | an empty line, emitted at **column 0** |
| `*RawStmt` | verbatim text, re-indented line by line |
| `*PPIf` | `#if` / `#else` / `#endif`, at **column 0** regardless of nesting |

`If.Else(fn)` attaches an else branch and returns the receiver; `If.ElseIf(cond, fn)` wraps a nested `*If` in the else block and returns **the nested `*If`**, so further branches chain onto it. An else-if chain is therefore an `Els` block holding a single `*If`, which is exactly how it prints (§15.6).

`Switch.Case(vals, fn)`, `Switch.Fallthrough(vals, fn)`, and `Switch.Default(fn)` append arms and return the `*Switch` for chaining. `Case.Fall` omits the implicit trailing `break`.

Only `*VarDecl`, `*Assign`, `*ExprStmt`, and `*Return` carry a `Comment` field; there is no way to attach a trailing comment to a control-flow statement.

---

## 11. Expressions

### 11.1 Shape

`Expr` wraps an `ExprNode`. **The zero `Expr` means "absent"** — a bare `return`, a missing for-condition, an uninitialized declaration — and `IsZero` reports it. This is why `Expr` is a struct wrapper rather than a bare interface: absence is a first-class state that reads the same everywhere.

Leaf constructors: `Name`, `I`, `U`, `F`, `B`, `Raw`. Node constructors: `Call`, `TCall`, `Ctor`, `Cast`, `Cond`, `Init`.

Postfix builders: `Add` `Sub` `Mul` `Div` `Rem`, `Eq` `Ne` `Lt` `Le` `Gt` `Ge`, `And` `Or` `BitAnd` `BitOr` `BitXor` `Shl` `Shr`, `Neg` `Not` `BitNot` `Addr` `Deref`, `At`, `Sel`, `Fld`, `Method`, `TMethod`.

### 11.2 Nodes

`NameExpr`, `IntExpr`, `UintExpr`, `FloatExpr`, `BoolExpr`, `RawExpr`, `*BinaryExpr`, `*UnaryExpr`, `*IndexExpr`, `*MemberExpr`, `*CallExpr`, `*MethodExpr`, `*CtorExpr`, `*CastExpr`, `*CondExpr`, `*ListExpr`.

`*CallExpr` and `*MethodExpr` carry `TArgs`, so anything in `<metal_stdlib>` — including the template-shaped Metal 4 operator surface — is reachable without raw text.

### 11.3 Precedence and parenthesization

Levels, ordered as in C++; higher binds tighter:

| Constant | Value | Operators |
|---|---|---|
| `PrecTernary` | 3 | `?:` |
| `PrecOr` | 4 | `\|\|` |
| `PrecAnd` | 5 | `&&` |
| `PrecBitOr` | 6 | `\|` |
| `PrecBitXor` | 7 | `^` |
| `PrecBitAnd` | 8 | `&` |
| `PrecEquality` | 9 | `==` `!=` |
| `PrecRelational` | 10 | `<` `<=` `>` `>=` |
| `PrecShift` | 11 | `<<` `>>` |
| `PrecAdd` | 12 | `+` `-` |
| `PrecMul` | 13 | `*` `/` `%` |
| `PrecUnary` | 14 | prefix `-` `!` `~` `&` `*` |
| `PrecPostfix` | 15 | `[]` `.` `.f()` |
| `PrecPrimary` | 16 | literals, names, calls, ctors, casts, init lists, raw |

`Prec(n)` returns the level of a node. The parenthesization rule is, normatively:

1. A child rendered in a context requiring level *min* is wrapped in parentheses **iff** `Prec(child) < min`.
2. `*BinaryExpr` at level *p* renders its left operand at *p* and its right operand at *p+1* — all modelled binary operators are left-associative.
3. `*UnaryExpr` renders its operand at `PrecUnary`.
4. `*IndexExpr`, `*MemberExpr`, and `*MethodExpr` render their receiver at `PrecPostfix`.
5. `*CondExpr` renders `Cond` at `PrecTernary+1`, `Then` unwrapped (it is bracketed by `?` and `:`), and `Else` at `PrecTernary`, so nested conditionals chain to the right without parentheses.
6. `RawExpr` is treated as primary and is **never** wrapped. Raw text that is not a primary expression MUST parenthesize itself.

Assignment and comma are not expression-level operators in this model, so levels 1 and 2 are unused.

**Model deviation:** an operator whose `BinOp` is absent from `binPrec` — reachable because `BinOp` is an open string type — is assigned `PrecAdd`. This is a guess, and a custom operator spelling that binds differently will be mis-parenthesized.

---

## 12. Name Resolution

`Resolve(m)` renames declarations that shadow an enclosing name — `sum`, `sum_1`, `sum_2` — so generated code never shadows accidentally.

Renaming is a **pass, not a construction-time side effect** (P8). Building a body detached and splicing it into a function is therefore safe: names are only disambiguated when asked for, and until then two independently generated bodies may legitimately both contain `i`.

The algorithm:

1. Walk the module collecting every module-scope name — `*VarDecl`, `*Function`, `*Struct`, `*Alias` — into a global set. Function bodies are skipped in this pass.
2. For each function, seed a scope from the global set, rename any parameter that collides, then resolve the body block.
3. Within a block, walk statements in order; a `*VarDecl` whose name is already in scope is renamed and its references rewritten.

Rewriting is structural: references are `NameExpr` values held inside `Expr` wrappers, and rewriting one requires its parent, so `rewriteNames` dispatches on the containing node and fixes each `Expr` field in place.

**`Resolve` is the least complete part of the package.** Four defects are documented in §19: parameter renaming does not rewrite uses at all (gap 1), nested block scopes are never visited (gap 2), rewriting is not bounded to the statements following the declaration (gap 4), and three expression positions are missed (gap 7). A caller that requires correct hygiene today SHOULD generate unique names rather than rely on `Resolve`.

---

## 13. Escape Hatches

MSL surfaces new stdlib functions, attributes, and scalar spellings faster than any typed package tracks them. Every layer has an untyped hatch that still participates in walking, resolution, and printing (P10):

```go
m.Add(&msl.RawDecl{Text: "// hand-written epilogue"})   // module scope
body.Raw("simdgroup_barrier(mem_flags::mem_threadgroup);") // statement scope
x := msl.Raw("as_type<float>(bits)")                     // expression
p.Attrs = append(p.Attrs, msl.RawAttr("some_new_attribute", "1"))
t := msl.ScalarType("float8_e4m3")                       // type
```

Prefer the typed handles where they exist: `Struct.Field` + `Expr.Fld`, `Function.Param`'s returned `Expr`, `Alias.Type_`, `Struct.Type`, `Template`/`TArg`/`VArg` for template spellings.

Three limits, all deliberate:

* `RawExpr` is primary and never parenthesized (§11.3 rule 6).
* `RawAttr` and unlisted `ScalarType` spellings pass `Verify` unchecked and are never version-gated (§7.4, §5.1).
* `RawDecl` and `RawStmt` text is opaque to `Resolve`: a name inside raw text is neither collected nor rewritten.

---

## 14. Traversal

`Walk(n, fn)` visits `n` and every node beneath it in source order; returning `false` from `fn` skips the subtree. Nodes are visited **as pointers where they are pointers**, so passes can rewrite in place; types are visited as values, since every `Type` implementation is a value type.

`Walk` accepts a `*Module`, any `Decl`, any `Stmt`, any `ExprNode`, or any `Type`. It descends: module → decls → function signature types, parameter types, body → statements → sub-blocks, expressions, and types → template arguments.

Two properties matter to pass authors:

* **`*Block` is never visited** (§9). A callback matching on `*Block` never fires. Wrap the block in a `*Scope` to walk it.
* **`Expr` wrappers are not visited**, only the `ExprNode` inside them. A pass that must rewrite a reference needs the *parent* node, which is why `rewriteNames` is written as a parent dispatch rather than a leaf rewrite.

`Module.Func(name)` is a thin `Walk` over the module returning the first matching `*Function`.

---

## 15. Canonical Text Form

The printer controls **layout only**. It makes no decisions about spelling, type rendering beyond `typeStr`, or parenthesization beyond the precedence rule (§11.3), so output is byte-stable for a given module.

Options: `WithComments(bool)` (default **true**), `WithHeader(string)` (default `Generated by vertex`; `""` disables the block), `WithIndent(string)` (default **four spaces**).

Normative formatting rules:

1. **Header.** When enabled: `//`, then `// ` + each line of the header, then `//`, then a blank line.
2. **Revision comment.** `// -std=<Version.Std()>` followed by a blank line.
3. **Declaration separation.** Declarations are classified `line` (`*Include`, `*Using`), `var` (`*VarDecl`, `*Alias`, `*CommentDecl`), or `block` (everything else). A blank line precedes a declaration when its class differs from the previous one, **or** when its class is `block`. Runs of `line` or `var` declarations are therefore compact; every struct, function, raw declaration, and preprocessor conditional is separated.
4. **Indentation** is one `indent` unit per nesting depth, prefixed by the enclosing declaration's base indent (empty at module scope, one unit for struct methods).
5. **Function signature.** Function attributes each on their own line, then `stage? ret name(params)` + return attributes. Parameters print **aligned, one per line** when the function is a `kernel`, `vertex`, or `fragment` stage with two or more parameters, **or** when the inline form exceeds 100 characters; otherwise inline, comma-space separated.
6. **Aligned parameters.** Type and name columns are padded to the widest of each, plus one space; array-typed parameters are excluded from the width computation and print as `elem name[len]`; a parameter with no attributes omits the name column padding; trailing spaces are trimmed. The closing `)` terminates the last line, with no trailing comma.
7. **Else chains.** An `Els` block containing exactly one `*If` collapses to `} else if (…) {`, recursively. Any other `Els` prints `} else {`.
8. **Switch.** `case` and `default` labels print at the `switch`'s indentation; arm bodies print one level deeper; a `break;` is synthesized at body indentation for every arm except those marked `Fall`.
9. **Preprocessor lines** print at column 0 regardless of nesting depth, at both declaration and statement scope. `*Blank` also prints at column 0.
10. **Raw text.** `*RawDecl` prints verbatim with a newline appended if absent; `*RawStmt` prints line by line at the current indentation, with empty lines left empty.
11. **Literals.** Integers in signed decimal; unsigned with a `u` suffix; floats in Go `'g'` form with `.0` appended when no `.`, `e`, or `E` is present; bools as `true`/`false`. No `f` or `h` literal suffix is emitted.
12. **Trailing comments** print as two spaces, `//`, one space, the text — only under `WithComments(true)`, and only on the four statement types that carry a `Comment` field.

**Byte-stability (conformance):** for any module *m* and fixed printer options, two calls to `Print(m)` yield identical bytes. Alignment widths are computed from the IR alone, never from environment or map iteration order.

`Print` currently returns a nil error unconditionally; the signature is reserved for the case where a module cannot produce valid text at all. Everything else is `Verify`'s job — the printer does not validate, so work-in-progress modules stay printable.

---

## 16. Verification Rule Index

`Verify` returns `[]Diag`; each `Diag` carries a `Sev`, a `Where`, and a message, and formats as `msl: <sev>: <where>: <msg>`. It never mutates and never stops at the first finding. `Errors(ds)` reports whether any diagnostic is an error.

`Where` strings are structural paths: `"function vector_add"`, `"function vector_add parameter tid"`, `"function vector_add return type"`, `"function vector_add body"`, `"function vector_add body local n"`, `"struct VOut.pos"`, `"method Mat::det"`, `"variable kPi"`, `"alias Desc"`.

**Warning deduplication.** Warnings are deduplicated on `(Where, Msg)`; errors are not. This is load-bearing rather than cosmetic: a top-level local's type is checked twice — once through `varDecl` and once through the body's type walk — so an ungated type would otherwise be reported twice per declaration.

| # | Rule | Severity | § |
|---|---|---|---|
| **V1** | Function has a name | error | 8.3 |
| **V2** | `kernel` functions return `void` | error | 8.3 |
| **V3** | A stage function has a body | error | 8.3 |
| **V4** | Parameter names are unique within a function | error | 8.3 |
| **V5** | Variable has a name | error | 8.1 |
| **V6** | Variable has a type | error | 8.1 |
| **V7** | `constant`-space variable has an initializer, unless it carries `[[function_constant]]` | error | 8.1 |
| **V8** | Attribute is non-empty | error | 7.1 |
| **V9** | Attribute is legal in its syntactic position | error | 7.3 |
| **V10** | Attribute arity matches the table | error | 7.3 |
| **V11** | Pointer type carries an address space | error | 5.2 |
| **V12** | Reference type carries an address space | error | 5.2 |
| **V13** | `tensor_handle` carries an address space | error | 5.2 |
| **W1** | Attribute postdates the module's revision | warning | 7.3 |
| **W2** | Type postdates the module's revision | warning | 5 |

**W2** floors: `bfloat` → Metal31, `auto` → Metal32, tensor types → Metal40, cooperative tensors → Metal40.

Findings are **warnings, not errors**, wherever a `VersionGate` branch could legitimately target a newer revision than the module's baseline (§4).

---

## 17. The Physical Line *(informative)*

```text
Go builder calls
   │
   ├─► msl.Module ──┬─► Walk                        (analysis)
   │                ├─► Block edits                 (rewrite passes)
   │                ├─► msl.Resolve                 (name hygiene)
   │                ├─► msl.Verify                  (V1–V13, W1–W2)
   │                └─► text.Print ──► .metal source
   │                                     │
   │                                     ├─► metal ──► .air ──► .metallib
   │                                     │              (verifier of record)
   │                                     └─► newLibraryWithSource: (runtime)
   │
   └─(no parser: .metal is emit-only)
```

`Resolve` MUST run before `Print` if hygiene is wanted. `Verify` may run on either side of `Resolve`; running it after means `Where` paths reflect the renamed identifiers.

---

## 18. Worked Example

Builder:

```go
m := msl.NewModule(msl.Metal30)

k := msl.NewKernel("vector_add")
a := k.Param("a", msl.Ptr(msl.Device, msl.Const(msl.Float)), msl.Buffer(0))
b := k.Param("b", msl.Ptr(msl.Device, msl.Const(msl.Float)), msl.Buffer(1))
c := k.Param("c", msl.Ptr(msl.Device, msl.Float), msl.Buffer(2))
tid := k.Param("tid", msl.UInt, msl.ThreadPositionInGrid)

body := k.Body
n := body.Let(msl.UInt, "n", msl.U(1<<20))

body.If(tid.Ge(n), func(blk *msl.Block) {
	blk.Return()
})
body.Assign(c.At(tid), a.At(tid).Add(b.At(tid)))

m.Add(k)
```

Emitted text:

```text
//
// Generated by vertex
//

// -std=metal3.0

#include <metal_stdlib>
using namespace metal;

kernel void vector_add(
    device const float* a   [[buffer(0)]],
    device const float* b   [[buffer(1)]],
    device float*       c   [[buffer(2)]],
    uint                tid [[thread_position_in_grid]])
{
    uint n = 1048576u;
    if (tid >= n) {
        return;
    }
    c[tid] = a[tid] + b[tid];
}
```

Note that the include and the using-directive are adjacent with no blank line (both class `line`, §15.3); that the parameter list is aligned because the function is a kernel with four parameters (§15.5); that the type column is nineteen characters wide, set by `device const float*` (§15.6); that `a[tid] + b[tid]` needs no parentheses because `PrecPostfix` exceeds `PrecAdd` (§11.3); and that `U(1<<20)` prints with the `u` suffix but the `Let` type is not restated (§15.11).

---

## 19. Known Gaps and Deferred Work

### 19.1 Discrepancies between code and its own documentation

1. **`renameIn` is a no-op stub.** `Resolve` renames a colliding parameter and then calls `renameIn(f.Body, p.Name)`, whose body is `_ = b; _ = to`. The parameter's declaration changes; every use of it in the body still names the old identifier. It also takes only the new name, so it could not rewrite anything even if implemented.
2. **`resolveBlock`'s nested-block recursion is unreachable.** It descends with `Walk`, matching on `*Block` — but `Walk` never visits a `*Block` (§9, §14), only the statements inside one. Only the outermost block of each function is ever resolved; a shadowing declaration inside an `if`, `for`, or bare scope is never renamed.
3. **`renameFrom` opens with a dead walk.** It walks the block matching `*MemberExpr` and discards the result (`_ = e`) before delegating to `rewriteNames`. The walk has no effect and should be deleted.
4. **`renameFrom` rewrites the whole block.** It is called after a shadowing declaration is renamed but rewrites every reference in the block, including statements *preceding* the declaration — which legitimately refer to the outer name. The rewrite should be bounded to the statements from the declaration onward.
5. **Non-finite floats print invalid source.** `floatStr` formats with Go's `'g'` verb and appends `.0` when the result contains no `.`, `e`, or `E`. `+Inf` becomes `+Inf.0` and `NaN` becomes `NaN.0`. Contrast the `ptx` package's exact-bits rule, which makes an unrepresentable literal unconstructible; here it is merely undiagnosed.
6. **`Print` cannot fail.** It returns `(string, error)` and the error is always nil. Either a failure mode should exist or the signature should shed the error.
7. **The `-std=` guard is always true.** `if p.m.Version.Std() != ""` never fails, because `Std()` formats unconditionally. A `Module` built as a literal rather than through `NewModule` prints `// -std=metal0.0`.

### 19.2 Modelling gaps

8. **`rewriteNames` misses three positions.** `Switch.Tag`, `Case.Vals`, and `TypeArg.Val` (template non-type arguments) hold `Expr` values whose parents are not cases in the dispatch. A renamed local used as a switch tag, a case label, or a template argument keeps its old spelling.
9. **`Verify` applies variable rules only to top-level locals.** `verifier.block` iterates `b.Stmts()` for `*VarDecl` and checks nothing deeper. A `constant`-space variable or a misplaced attribute inside a nested block escapes **V7**, **V9**, and **V10**; its *types* are still checked, because the type walk descends.
10. **`ArrayType` through `typeStr` prints C-invalid text.** Only `declarator` places the length correctly. An array as a template argument, a pointee, or a struct field type reached without a declarator prints `float[256]`.
11. **No name-uniqueness, declare-before-use, or reference checking.** `Verify` does not detect two functions with the same name, a `NamedType` that names nothing, an `Alias` cycle, or a use that precedes its declaration. `Resolve` is the only name-aware pass, and it is incomplete (§12).
12. **Aligned parameters trigger on three stages only.** `object`, `mesh`, and `intersection` stage functions fall back to the inline form unless they cross the 100-character threshold, which is an arbitrary asymmetry.
13. **Struct fields are not column-aligned** even though parameters are.
14. **`Block.InsertBefore` mutates shared backing storage.** It appends into `b.stmts[:i]`, which can overwrite elements a previously returned `Stmts()` slice still aliases.
15. **Switch arms always synthesize a `break`.** An explicitly appended `*Break` in a non-`Fall` arm produces two.
16. **`Version.GTE` ignores `OS`,** so split pre-3.0 revisions compare meaninglessly across OS lines (§4).
17. **Large areas of MSL are unmodelled** and reachable only through `Named`, `Template`, and the `Raw` hatches: ray-tracing types and intersection function tables, `simdgroup_matrix`, mesh grid and topology declarations, argument-buffer tiering, `[[function_constant]]` conditional overloads, lambdas, and namespaces other than `metal`.

### 19.3 Deferred

18. **No parser.** Round-tripping is out of scope by design (§0.4); if it is ever wanted, §15 is the specification it must satisfy.
19. **No split-revision constants.** Pre-3.0 `ios-` / `macos-` revisions must be spelled as `Version{Major, Minor, OS}` by hand.
20. **`Attr.Args` is `[]string`.** A `[[function_constant(kIndex)]]` whose index is a named constant expression is not representable without `RawAttr`.
21. **No width control beyond the 100-character inline threshold.** Long expressions are never wrapped; the printer breaks only parameter lists.
22. **`Alias.Type_` and the `*Stage`-suffixed constants** are naming warts kept for compatibility, not design.