# ir/vir

`github.com/vertex-language/vvm/ir/vir`

The Vertex IR data model: the in-memory representation of a verified program, a construction API for building one, and the verifier that enforces every invariant the rest of the pipeline is allowed to assume. Every other package in the repository either produces a `vir.Module` or consumes one.

---

## Import path

```go
import "github.com/vertex-language/vvm/ir/vir"
```

---

## Package layout

```
ir/vir/
├── module.go     Module, Target, Struct, FnSig, Const, Global, ConstInit variants,
│                 Link, ExternGroup/ExternFn, Func, Block, Inst, Terminator variants
├── types.go      Type interface: IntType, FloatType, PtrType, VoidType, VecType,
│                 StructType, ArrayType; Equal, IsInt/IsFloat/.../IsValueType
├── operand.go    Operand, OperandKind, constructors (V, Int, Flt, Str, Bl, Null, Ty, Ord, VecLit)
├── float.go      formatFloat — canonical float-literal text
├── targets.go    canonical arch/OS/ABI vocabularies, rejected-alias tables,
│                 PointerBits, BinFormat, FormatOf
├── builder.go    NewModule + FuncBuilder — construction API, never checks
└── verify.go     Verify — the single place invariants are enforced
```

---

## Design: construct, then check

`builder.go` and `verify.go` never overlap. The builder mirrors the IR one-to-one and only appends; `Verify` is the only place that validates anything. A `Module` built by hand, or produced by a decoder, is only as trustworthy as the last call to `Verify` made on it — this package does not track verification state on the value itself.

```go
m := vir.NewModule("add_example")
m.SetTarget("x86_64", "linux", "gnu")

fmt := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 14},
    vir.InitBytes{Data: []byte("%d + %d = %d\n\x00")})

fb := m.DeclareFn("main", nil, vir.I32, true)
a := fb.Emit("a", "mov", vir.I32, vir.Int(7))
b := fb.Emit("b", "mov", vir.I32, vir.Int(35))
sum := fb.Add("sum", vir.I32, a, b)
fb.Call("r", "printf", vir.V(fmt.Name), a, b, sum)
fb.Return(vir.Int(0))

if err := vir.Verify(m); err != nil {
    panic(err)
}
```

Nothing above validated anything — `Verify` is the first point at which name collisions, type mismatches, or malformed control flow would surface.

---

## Core concepts

### `Module`

Field order mirrors the mandatory section order a `.vir` file must follow: `Target`, `Structs`, `FnSigs`, `Consts`, `Globals`, `Links`, `Externs`, `Funcs`. Nothing downstream (`format/`, `lower/`, `object/`, `objectfile/`, `objectwriter/`) is allowed to touch an unverified `Module`.

```go
type Module struct {
    Name    string
    Target  *Target // nil for pure-compute modules
    Structs []*Struct
    FnSigs  []*FnSig
    Consts  []*Const
    Globals []*Global
    Links   []*Link
    Externs []*ExternGroup
    Funcs   []*Func
}
```

### `Type`

One interface, seven implementations. `IsAggregate`/`IsValueType` distinguish memory-only types (`StructType`, `ArrayType`) from types that may name a value.

```go
vir.I32                 // IntType{32}
vir.F64                 // FloatType{64}
vir.Ptr                 // PtrType{}
vir.VecType{Elem: vir.I32, Len: 4}
vir.ArrayType{Elem: vir.I8, Len: 14}
vir.StructType{Name: "Point"}

vir.Equal(vir.I32, vir.IntType{Bits: 32}) // true — structural equality
```

### `Operand`

A tagged union covering every position an operand can appear in: idents, literals, `null`, a type used in operand position (`index.ptr`), atomic orderings, and vector literals.

```go
vir.V("x")            // ident
vir.Int(42)           // integer literal
vir.Flt(3.14)         // float literal
vir.Bl(true)          // bool literal
vir.Null()            // null
vir.Ty(vir.I32)       // type-in-operand-position
vir.Ord("acquire")    // atomic ordering
vir.VecLit(0, 4, 1, 5) // shuffle mask / vector const
```

### Instructions and terminators

`Inst.Op` holds the bare mnemonic; exactly one of `Inst.Suffix` (a `Type`) or `Inst.Sig` (an `fnsig` name, for indirect `call`/`tailcall`) may be set — the `<op>.<suffix>` split is structural, not string-parsed downstream. Terminators are a separate interface from instructions, so "exactly one terminator, nothing after it" doesn't need to be checked by scanning:

```go
type Terminator interface{ isTerm() }
// Br, BrIf, Switch, Return, TailCall, Trap, Unreachable

func Successors(t Terminator) []string // labels a terminator may transfer to
```

---

## `Verify` — what gets checked

`Verify` runs one forward pass over module-level sections, then a per-function pass:

```go
func Verify(m *Module) error
```

1. **Target** — arch/OS/ABI must be canonical; a recognized alias (`amd64`, `arm64`, `darwin`, ...) fails with the canonical spelling named in the error, not silently rewritten.
2. **Name resolution** — one flat namespace across structs, fnsigs, consts, globals, externs, functions, and block labels. Redeclaring a name, or using a reserved keyword, fails immediately with the conflicting kind named.
3. **Per-section checks** — struct fields, fnsig signatures, const/global initializers (`checkInit` walks `ConstInit` recursively against the declared type), link-to-extern-group correspondence, filename derivation per target `BinFormat`.
4. **Per-function body shape** — every block terminates, every label is both defined and referenced, `Successors` resolves.
5. **Type fixation** — each value's type is computed once (`resultType`) and locked in at first assignment; a later assignment with a different type is rejected.
6. **Definite assignment** — a forward must-analysis (`in`/`out` sets per block, meet-over-predecessors) confirms every read is preceded by an assignment on every path.

```go
if err := vir.Verify(m); err != nil {
    // err names the exact rule violated, e.g.:
    // "value \"sum\" assigned as i64 here but fixed as i32 at first
    //  assignment (§5 rule 2)"
}
```

### Known gaps

A handful of obligations are intentionally incomplete, each marked `TODO` at its call site rather than silently skipped:

- Feature-tier tables — `Target.Tiers` entries aren't validated against per-target tier data yet.
- Deep per-opcode operand-type unification against an instruction's suffix.
- The `noreturn`→`unreachable` adjacency rule.
- Shuffle-mask bounds checking for `OVecLit`.
- TLS on `os=none` is rejected outright rather than allowed under a TLS-capable tier, pending the same tier-table work.

---

## Design notes

**Nothing here is a serialization format.** `ir/vir` has no `[]byte` in or out — that's `format/`'s job entirely. This package only ever holds the in-memory shape and the rules it must satisfy.

**Verification is centralized on purpose.** Every downstream package — lowering, object translation, container-file writing — is written under the assumption that whatever `Module` it receives already passed `Verify`. Putting all of that logic in one place means every consumer gets identical guarantees regardless of whether the module came from `.vir` text, `.vbyte` bytes, or a hand-written builder call.

**The builder never second-guesses you.** `DeclareFn`, `Emit`, `Br`, and friends all just append to the structure. If you want a hand-built module to be usable by anything downstream, you must call `Verify` yourself — nothing does it for you implicitly.