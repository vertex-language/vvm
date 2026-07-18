# Vertex Virtual Machine Architecture

The Vertex Virtual Machine (`vvm`) is a high-performance execution engine built on a foundation of Ahead-of-Time (AOT) compilation. It ingests Typed Bytecode (`.vbyte`) and lowers it directly to native CPU instructions. While the current architecture prioritizes zero-cost AOT execution, its lean design provides an ideal baseline for introducing Just-In-Time (JIT) compilation and runtime optimizations in the future.

## 1. The Frontend Contract: `.vbyte` vs `.vir`

* **`.vbyte` is the Boundary:** Frontends directly target `.vbyte`, a binary, portable, typed bytecode.


* **Pre-Parsed Efficiency:** Because `.vbyte` is a pre-parsed binary encoding, `vvm` skips lexing and text-to-structure translation. This enables extremely fast AOT compilation today, and will allow future JIT implementations to bypass the heavy startup costs typical of traditional interpreters.


* **`.vir` is for Debugging:** `.vir` is the human-readable text equivalent of `.vbyte`. While `vvm` accepts it, parsing `.vir` incurs an extra cost, making it strictly a debugging tool rather than a standard build input.



## 2. An AOT-Primary Foundation

`vvm` is engineered to provide the absolute predictability of an AOT compiled binary, while structuring its intermediate representation to accommodate future dynamic tiering:

* **CPU-Focused Targets:** `vvm` targets native instruction sets decoded by physical silicon (e.g., `x86_64`, `aarch64`).


* **Minimal Baseline Overhead:** The core architecture eschews mandatory garbage collection, embedded runtimes, and sandboxing. Any future JIT capabilities will be strictly additive, ensuring the baseline engine remains lightweight.


* **Manual Memory:** The only built-in allocation is stack-based. Heap allocation relies entirely on standard extern calls (like `malloc`).


* **Hardware-Mapped Types:** Data types map seamlessly to hardware register classes without runtime boxing or tagging.



## 3. The Execution Model

Modern hybrid runtimes often rely heavily on JIT compilation to offset the massive overhead of parsing and translating bytecode at runtime. `vvm` flips this paradigm:

* **No Translation Penalty:** Because `.vbyte` is a structured binary, there is no expensive parse-then-translate step. The intermediate representation is ready for immediate lowering.


* **Build-Time Economics:** Currently, the compilation cost is paid exactly once during `vvm build` for a statically linked binary. As JIT features are introduced, they will serve as targeted, opt-in enhancements for dynamic workloads rather than a mandatory resident requirement.



## 4. Core Architecture & Advantages

* **No Object Files or Linkers:** The module contains self-describing `link`/`extern` sections that declare dependencies natively. This eliminates external linker flags and the `.o` file toolchain entirely.


* **Flat Control Flow:** Functions utilize structured, non-nested basic blocks. Values merge across blocks via same-name reassignment rather than SSA phi nodes.


* **Declarative Target Binding:** Modules can optionally declare an `(arch, os, abi)` target. This is mandatory if link dependencies exist, but pure-compute modules can omit it to remain target-agnostic.


* **Strict Opcode Semantics:** Every opcode has exactly one behavior, actively rejecting fast-math relaxations or target-specific semantic variations.


* **Minimal Undefined Behavior (UB):** The UB surface is limited to an exhaustive, explicitly enumerated list.


* **Single-Pass Verification:** Enforced section ordering and declare-before-use rules guarantee that a module is completely verifiable before any native code is emitted.



## 5. Developer Workflows

The `vvm` CLI exposes two primary workflows built around `.vbyte`, both of which benefit from the engine's AOT-first architecture:

* **`vvm run`:** Compiles a module into a temporary native binary and executes it. The lack of parsing overhead makes this fast enough to feel like interpreting a script, laying the perfect groundwork for future tiered execution and hot-path JIT compilation.


* **`vvm build`:** Compiles the module into a statically linked, zero-dependency executable. Cross-compiling simply requires supplying a different `--target` flag against the identical `.vbyte` input.