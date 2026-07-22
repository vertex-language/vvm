# `asm.md` — Assembly Support

**Status: not yet implemented. This is a placeholder describing the planned direction.**

Inline and native assembly (`asmdialect`, `asm: ... code: ... end` blocks, and all of `intel`/`att`/`a32`/`t32`/`native` mnemonic/operand grammar) have been removed from the Vertex IR spec (`README.md`) as of v1.9. They are not gone — they're moving to their own package, on their own timeline, with their own file format.

### Why pull it out

Assembly support doesn't share Vertex IR's design center. The IR is built around opcode-first, strictly-typed, one-behavior-per-opcode semantics — the whole point is that `lower/<arch>` never has to think about dialect-specific mnemonics or register allocation quirks. Keeping raw `intel`/`att`/`a32`/`t32` text embedded inside `.vir`/`.vbyte` meant every consumer of the IR (verifier, importer, lowering) had to carry dialect-aware parsing and validation for something that isn't really IR at all — it's just text destined for a real assembler. Splitting it out keeps the bytecode and IR clean, small, and easy to reason about in one pass, and lets the assembly side evolve independently — modern-first, without dragging the IR's verification model along with it.

### The plan

* **New file extension: `.ss`** — a unified "asmv2" format. This is where hand-written assembly blocks and native dialect syntax (`intel`, `att`, `a32`, `t32`, etc.) live going forward, entirely outside `.vir`/`.vbyte`.
* **Own package, own pipeline.** `.ss` files are handled by a dedicated assembler package — not `ir/vir`, not `ir/verify`, not `importer`. It has no dependency on the Vertex IR grammar and the IR has no dependency on it.
* **Compiles straight to `.o`.** A `.ss` file assembles directly to an ordinary object file, using the same target triple concepts (`arch`/`os`/`abi`) as the rest of the toolchain where applicable.
* **Opt-in linking.** The resulting `.o` is not implicitly pulled in. You link it alongside the `.o` files produced from your `.vir` modules exactly the way you'd link any other object file today — same as an `extern`/`link`-declared external dependency, just produced in-house instead of from a system library.

### What this means for existing `.vir`

Nothing needs an assembly escape hatch inside the IR anymore. If you need hand-written machine code, you write it in a `.ss` file, assemble it to a `.o`, and link that `.o` in alongside your `.vir`-compiled output. The `.vir` module stays exactly what §1 always wanted it to be: opcode-first, strictly typed, target-dependent only in fixed, opcode-defined ways — never in raw dialect text.

Further detail on `.ss` grammar, the asmv2 instruction/operand model, and the assembler package's CLI/API will land here once implementation starts.