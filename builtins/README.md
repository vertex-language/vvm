# `os` — vvm's thin platform module

**Import path:** `github.com/vertex-language/vvm/os`
**Module name:** `os` (namespace `vvm`)

## What this is

`os` is a small set of prebuilt Vertex IR modules that wrap the handful of
operating-system calls every program needs, so a frontend doesn't have to
emit `write` vs `WriteFile` itself.

It is deliberately tiny. `os` covers:

* **Heap memory** — `mem_alloc` / `mem_free`
* **Writing to the screen** — `print`, `eprint`, `print_line` on stdout/stderr
* **Exiting** — `halt`

That's the whole scope. There is no file I/O, no directories, no sockets, no
environment access, no time, no threads. If you need to open a file, call the
platform API yourself through your own `extern` group — `os` is not going to
grow into a standard library. Its job is to make "print a string and allocate
some bytes" portable, and stop there.

Everything above that line is pure computation and doesn't belong here: a
number formatter, a string builder, refcount arithmetic. Those have no `link`
section, so they carry no target-decl and build for any triple from a single
file. `os` is only the part that genuinely differs per platform.

## API

Identical on every target. A frontend emits the same call on all three.

| Function | Meaning |
| --- | --- |
| `mem_alloc(size i64) ptr` | Allocate `size` bytes. Null on failure. Not zeroed. |
| `mem_free(p ptr) void` | Free a pointer from `mem_alloc`. |
| `print(buf ptr, len i64) i64` | Write `len` bytes to stdout. Returns bytes written. |
| `eprint(buf ptr, len i64) i64` | Same, to stderr. |
| `print_line(s ptr) i64` | Write a NUL-terminated string plus `\n` to stdout. |
| `str_len(s ptr) i64` | Length of a NUL-terminated string, excluding the NUL. |
| `halt(code i32) void noreturn` | Exit the process. |

Constants: `EXIT_OK` (0), `EXIT_ERR` (1).

Usage:

```vir
module main
import "builtins/os"
target x86_64 linux gnu

global greeting array[i8, 14] = "hello, world\n\x00"

export fn main() i32 entry:
    n = call os.print_line, greeting
    return 0
end
```

## Files

Each file pins a **full triple**, not just an OS. §2.1 requires a target-decl
whenever `link` is present, and a target-decl fixes arch and abi too — so
these are per-cell, the same way `crt`'s stubs are.

| File | Triple | Backing API |
| --- | --- | --- |
| `os_linux.vir` | `x86_64-linux-gnu` | libc: `malloc`/`free`/`write`/`exit` |
| `os_darwin.vir` | `aarch64-macos` | libSystem, same four symbols |
| `os_windows.vir` | `x86_64-windows-msvc` | kernel32: `HeapAlloc`/`WriteFile`/`ExitProcess` |

Adding `aarch64-linux-gnu` or `x86_64-linux-musl` means another file, not a
flag. If that count grows, rename to the explicit `os_x86_64_linux_gnu.vir`
form rather than pretending `os_linux.vir` is portable across arches.

## Gotchas

**Your root module must declare its own libc link.** `entrypoint.go`'s
`linksLibC` inspects only the *root* module's `link` declarations, not the
whole graph — so importing `os` is not enough to tell `crt` that a real C
runtime is available. On Linux this is merely suboptimal (the stub falls back
to a bare `SYS_exit`, which is harmless here since `print` uses raw `write`
and never buffers). **On macOS it is a hard build failure**: the aarch64 stub
rejects `NeedsLibC == false` outright. Declare `link shared "c"` (Linux) or
`link shared "System"` (macOS) in your root module alongside `import "os"`.

**Windows truncates lengths above 2 GiB.** `WriteFile` takes a 32-bit count,
so `print` narrows `len` with `trunc.i32`. Fine for anything a frontend
actually prints; don't hand it a multi-gigabyte buffer.

**No newline translation.** `print_line` emits a bare `\n` on every platform,
including Windows. Modern consoles handle it; if you need CRLF, write it
yourself.

**Every module in the set gets linked.** `BuildModuleGraph` has no dead
stripping — if you inject `os` into a build that never calls it, its libc
dependency comes along anyway. Add it only when the graph actually contains
an unresolved `import "vvm/os"`.

**Return values are unchecked.** `mem_alloc` returns whatever the allocator
returned, null included. `print` returns the byte count, which may be short.
Checking is the caller's job; `os` adds no policy.

## Loading these into a build

`os` ships as `.vir` source, not as a Go package with behavior. The intended
wiring is: `go:embed` the three files, scan the decoded modules for an
unresolved `import "vvm/os"`, pick the file matching the build `Target`, and
append it to the slice handed to `BuildModuleGraph` before `importer.NewSet`
runs. From there the existing pipeline needs no changes —
`resolveELFLinkDependencies` and friends already walk *every* module's
`m.Links`, so `os`'s own `link shared "c"` propagates for free.