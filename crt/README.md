# `crt` — vvm's C-runtime-startup package

**Import path:** `github.com/vertex-language/vvm/crt`

## What is `crt`?

`crt` contains the code that handles the very first machine instructions a program executes, long before your `main()` function is ever called. In traditional toolchains, this is commonly known as **crt0** (like `crt0.o` in glibc or the startup code in macOS and Windows).

Because this code runs before any runtime exists, it cannot be written using vvm's standard intermediate representation (`vir`). The `vir` vocabulary intentionally lacks instructions for reading uninitialized, raw register states. Instead, `crt` sits one layer below `vir` and is hand-coded directly into raw machine instructions based on the target architecture.

## Why is it needed?

When an operating system launches a program, it doesn't cleanly call `main(argc, argv, envp)`. It simply jumps to an entry address, leaving raw data sitting in registers or on the stack.

`crt` acts as the necessary bridge to:

1. **Read** the raw, ABI-specific incoming state from the OS.
2. **Stage** the arguments so they match what your specific `main` function expects (whether it takes zero arguments, `argc/argv`, or `argc/argv/envp`).
3. **Call** your actual `main` function.
4. **Exit** the process cleanly by turning `main`'s return value into a proper exit — either via a raw syscall or a libc `exit()`.
5. **Halt** (trap) the program as a safety net if the exit call somehow returns.

## How it works

Wiring up `crt` happens automatically during the build phase:

1. **The gatekeeper** — during compilation, `resolveEntryPoint` checks if `crt` is needed. It's skipped if your entry function is manually named `_start`, the output isn't an executable, or the function signature doesn't match a recognized `main()` shape.
2. **The lookup** — if `crt` is required, the package looks up the correct builder for your specific architecture/OS combination (e.g., `x86_64` and `linux`).
3. **The build** — the selected builder hand-encodes the startup instructions and produces a real relocatable object (`vvm_crt.o`).
4. **The link** — this startup object is handed to the linker right alongside your compiled code (`vvm_module.o`) to produce the final executable.

## Package organization

The package separates shared logic from OS/architecture-specific machine code:

- **`crt.go`** — shared types (`BuildArgs`, `MainSignature`, `Format`, `Stub`) and the lookup registry.
- **Target stubs** — one file per supported architecture/OS pair. Currently:
  - `x86_64_linux_stub.go`
  - `aarch64_macos_stub.go`
  - `x86_64_windows_stub.go`

Each stub registers itself on initialization. Compiling for an architecture/OS pair that isn't registered fails loudly — in that case, name your entry function `_start` and write the startup sequence manually.