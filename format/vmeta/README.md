# vmeta/binary

`github.com/vertex-language/vvm/format/vmeta/binary`

Converts between a `vir.ModuleShape` (`ir/vir`'s Stage 0 extraction, §7.3) and the on-disk `.vmeta` container (`file_formats.md` §F5). Mirrors `format/vbyte/binary`'s low-level container conventions (§F2) but carries a different, smaller section set, and never compresses (§F5.1).

## Import path

```go
import "github.com/vertex-language/vvm/format/vmeta/binary"
```

## API

```go
func Encode(in Input) ([]byte, error)
func Decode(data []byte) (*Result, error)
```

`Input`/`Result` bundle `*vir.ModuleShape` with the two things a `ModuleShape` doesn't itself carry: the producing module's `Target` (§F5.2, compatibility-only) and its direct `import` strings (§F5.2 `IMPD`, a `NONSEMANTIC` build-graph hint).

Neither direction calls `vir.Verify` — Stage 0 extraction (`vir.ExtractShape`) is expected to run against an already-verified module, matching `format/vbyte`'s "neither codec re-validates" stance.

## Known limitations

`.vmeta`'s structural-only design (§F5.3: "struct names never enter the comparison") means round-tripping is lossy in ways the *format* mandates, not accidents of this code — see the `binary` package doc comment (`format.go`) for the itemized list: decoded struct types recover a name only by matching this file's own exports; decoded struct field names and `byval`/`sret` parameter names don't survive at all; `vir.GlobalShape` has no `Align` field to round-trip through the `align` slot §F5.4 reserves for it.

## Package layout

* **`format.go`** — container assembly: header, section table, trailer, `Encode`/`Decode` entry points, section ordering/duplication checks.
* **`leb128.go`** — `uleb`/`sleb` read/write (§F2.1), minimal-length enforced both ways.
* **`strtable.go`** — `STRT` string table: intern-on-first-reference build side, dedup-checked parse side (§F2.5).
* **`typetable.go`** — `TYPE` table: hash-consed, `STRUCT_S`-only (§F2.6, §F5.3) build side; raw-entry parse side plus the dependency-ordered materialization pass that resolves struct entries to `vir.StructType` once names are known.
* **`literal.go`** — `§F2.7` literal codec, extended with `0x05 BOOL` / `0x06 VECTOR` (mirroring `vbyte/binary`'s own documented deviation, since §6.2 scalar consts include `i1` and `vec[T,N]`).
* **`shapehash.go`** — `§F5.5` `shape_hash`: a canonical, table-index-free structural encoding, so two independently-built `.vmeta` files for equal shapes hash identically regardless of each file's own declaration order.
* **`sections.go`** — `MODU`/`TARG`/`IMPD`/`XPRT` payload codecs, including symbol mangling (via `vir.MangledSymbol`) and the `(kind, name)`-sorted export table (§F5.4).