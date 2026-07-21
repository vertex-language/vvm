# vvmtest

An in-memory, no-`.vir`-files, no-`go test` correctness suite for `ir/vir`
and the rest of the `vvm` pipeline. Every case builds a `*vir.Module`
directly via the `vir.FunctionBuilder` API (ir/vir/builder.go), runs it
through `vvm.RunModule`, and checks **one thing**: either a single printed
integer, or an exit code. Nothing else about stdout is checked — no
string/format matching, no golden files.

## Why not `go test`

`go test`'s subtests, parallelism, and benchmarking machinery add nothing
here — there's no fixture-file I/O, no table of string diffs, no need for
`-run` regex filtering across hundreds of cases. Correctness fully lives in
"did the module produce the right number." A ~120-line runner (`runner.go`)
covers registration, host autodetection, and PASS/FAIL/SKIP reporting, so
we run it as a plain program instead:

```sh
go run ./vvmtest
```

or build once and reuse the binary:

```sh
go build -o vvmtest ./vvmtest && ./vvmtest
```

Exit code is `0` if every applicable case passed, `1` otherwise — safe to
wire into CI directly (`go run ./vvmtest || exit 1`).

## Layout

```
vvmtest/
├── case.go                  — testCase type, registry, host autodetect, run()
├── main.go                    — func main() { os.Exit(run()) }
├── arithmetic_bitwise.go        — §4 add/sub/mul, bitwise, shifts, rotl
├── overflow_saturating.go        — §4 overflow predicates, *_sat, umulh/smulh
├── compare_select.go              — §4 comparisons, select, smin/smax/umin/umax
├── conversions.go                  — §4 sext/zext/trunc, stoint_sat, bitcast
├── memory_struct.go                 — §4 alloca/load/store, field.ptr, index.ptr
├── control_flow.go                   — §5 loops (Join Convention), switch
├── calls_tailcall.go                  — §4/§5 direct call, guaranteed tailcall
├── vectors.go                          — §4 vec add/reduce/shuffle
└── asm_exit.go                          — §4 inline asm, arch-gated (x86_64/aarch64)
```

Files are grouped to mirror `docs/ir.md`'s own §4/§5 section headings —
finding "where does `shuffle` get exercised" should be as easy as reading
the spec's table of contents.

## Adding a case

1. Pick the file matching the opcode's ir.md section (or add a new file
   for a section that isn't covered yet — e.g. `atomics.go` for §4
   Atomics).
2. Call `register(testCase{...})` from that file's own `init()`. Nothing
   else needs editing — no central table, no manifest.
3. For anything that returns a scalar you can print with `%d`, use the
   `intPrintingModule` helper (declares `printf` + the format global, runs
   your `body` callback, prints its return operand, returns 0):

   ```go
   register(testCase{name: "my_case", build: func(a, o string) *vir.Module {
       return intPrintingModule("my_module", func(fb *vir.FunctionBuilder) vir.Operand {
           return fb.Add("r", vir.I32, vir.IntLiteral(2), vir.IntLiteral(2))
       })
   }, wantValue: val(4)})
   ```

4. If the case needs more than one function, a struct decl, or an
   `extern`/`asm` block that doesn't fit the single-`body`-callback shape,
   build the `*vir.Module` by hand instead (see `struct_field_ptr` in
   `memory_struct.go` or `tailcall_accumulator` in `calls_tailcall.go` for
   examples) — `intPrintingModule` is a convenience, not a requirement.
5. If the case only makes sense on certain hardware (raw syscalls,
   register-specific asm), set `hostArches`/`hostOSes` using vir's
   **canonical** spellings (ir.md §10.1/§10.2 — `x86_64`, `aarch64`,
   `linux`, `macos`, etc., never aliases). Leave both `nil` for anything
   portable (pure compute + `extern printf`, which resolves against the
   anonymous default namespace on any hosted target).

## `wantValue` vs `wantExit`

- Set `wantValue` when the module prints one integer via `intPrintingModule`
  (or an equivalent hand-built `printf` call) and returns exit 0. The
  runner parses trimmed stdout with `strconv.ParseInt` and compares.
- Set only `wantExit` (leave `wantValue` nil) when the thing under test
  *is* the exit code — e.g. `asm_exit.go`'s raw-syscall cases, or an
  `extern exit()` case — and there's no meaningful stdout to check.
- Don't set both unless the module genuinely does both; most cases need
  exactly one.

## Host autodetection

`hostArch()`/`hostOS()` translate `runtime.GOARCH`/`runtime.GOOS` into
vir's canonical target vocabulary (the same mapping `vvm/run.go`'s
`hostTarget()` uses internally for `vvm.Run`). A case with `hostArches`/
`hostOSes` set only runs when the detected host matches; otherwise it's
reported `SKIP` with the reason, not silently dropped. This means:

- `go run ./vvmtest` behaves correctly on any dev machine or CI runner
  without configuration — no cross-compiling, no QEMU, no manual target
  flags.
- Arch-specific cases (asm dialects, register tables, syscall numbers)
  can sit in the same binary as portable ones; you always see which ones
  ran vs. which were skipped and why.

## Known inference caveats

A few numeric/layout assumptions were worked out by hand from `docs/ir.md`
rather than verified against the actual lowering backends — worth a second
look if the corresponding case fails instead of assuming the harness is
wrong:

- **`struct_field_ptr`** (`memory_struct.go`): assumes `struct Point(x i32,
  y i32)` is exactly 8 bytes with no inter-field padding (§7.1). True for
  this specific pair of same-size fields on every ABI I'm aware of, but
  not something this file re-derives generically.
- **`bitcast_ptr_roundtrip`** (`conversions.go`): assumes a 64-bit pointer
  width (`vir.I64`) for the `ptr <-> usize-int` round trip (§4 Conversions,
  §6.2). Correct for `x86_64`/`aarch64` hosts; would need `vir.I32` on
  `x86`/`arm` hosts if those are ever added as CI targets.
- **`umulh_widening`**, **`stoint_sat_clamps`**, **`vector_extract_shuffle`**
  (`overflow_saturating.go`, `conversions.go`, `vectors.go`): expected
  values were computed by hand from the opcode semantics in ir.md §4,
  not cross-checked against a second implementation. If one of these
  fails, treat it as "check my arithmetic" before "assume the verifier/
  lowering is broken."

## Not yet covered

- **Atomics** (§4 Atomics: `atomic_load`/`atomic_store`/`atomic_add`/
  `cmpxchg`/`fence`) — legal to exercise single-threaded on an `alloca`'d
  slot even without real concurrency; no `atomics.go` yet.
- **Masked/gather/scatter vector ops** — tier-gated (§10.4); needs a
  `target-decl` with a tier list most host CPUs may not actually support,
  so these need either a feature-detection story or an explicit skip
  condition beyond plain arch/os matching.
- **TLS globals**, **link/extern-to-a-real-shared-lib** beyond the
  anonymous-namespace `printf`/`exit` pattern already used here.